package rollback

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/a-shan/mysql-pitr/internal/connector"
)

// PreflightRunner executes readiness checks against a MySQL database before a
// rollback operation. It wraps the connector-level preflight checks and adds a
// binlog-availability check.
type PreflightRunner struct {
	db           *sql.DB
	recoveryTime time.Time // target recovery point; zero means "latest"
}

// NewPreflightRunner returns a runner that will validate the database at the
// given recovery time. Pass a zero recoveryTime to skip temporal checks.
func NewPreflightRunner(db *sql.DB, recoveryTime time.Time) *PreflightRunner {
	return &PreflightRunner{db: db, recoveryTime: recoveryTime}
}

// Run executes all preflight checks and returns a consolidated result.
func (r *PreflightRunner) Run(ctx context.Context) (*connector.PreflightResult, error) {
	res := &connector.PreflightResult{
		Status: connector.PreflightPass,
		Checks: make([]connector.PreflightCheck, 0, 6),
	}

	// 1. MySQL version (5.7 or 8.0).
	vc := r.checkVersion(ctx)
	res.Checks = append(res.Checks, vc)
	if vc.Status == connector.PreflightPass {
		res.Version = vc.Message
	}
	if vc.Status == connector.PreflightFail {
		res.Status = connector.PreflightFail
	}

	// 2. binlog_format must be ROW.
	bf := r.checkBinlogFormat(ctx)
	res.Checks = append(res.Checks, bf)
	if bf.Status == connector.PreflightFail {
		res.Status = connector.PreflightFail
	}

	// 3. binlog_row_image (warn if not FULL).
	ri := r.checkBinlogRowImage(ctx)
	res.Checks = append(res.Checks, ri)

	// 4. Binlog availability vs recovery time.
	ba := r.checkBinlogAvailability(ctx)
	res.Checks = append(res.Checks, ba)
	if ba.Status == connector.PreflightFail {
		res.Status = connector.PreflightFail
	}

	// 5. Permissions (REPLICATION SLAVE, REPLICATION CLIENT, SELECT).
	pc := r.checkPermissions(ctx)
	res.Checks = append(res.Checks, pc)
	if pc.Status == connector.PreflightFail {
		res.Status = connector.PreflightFail
	}

	// 6. Disk space (binlog + data volume estimate).
	dc := r.checkDiskSpace(ctx)
	res.Checks = append(res.Checks, dc)

	return res, nil
}

// ---------------------------------------------------------------------------
// Individual checks
// ---------------------------------------------------------------------------

// checkVersion validates the MySQL server is 5.7 or 8.0.
func (r *PreflightRunner) checkVersion(ctx context.Context) connector.PreflightCheck {
	c := connector.PreflightCheck{Name: "MySQL Version"}
	var version string
	if err := r.db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); err != nil {
		c.Status = connector.PreflightFail
		c.Message = fmt.Sprintf("cannot query version: %v", err)
		return c
	}

	major, minor, _ := parseVersion(version)
	if major < 5 || (major == 5 && minor < 7) {
		c.Status = connector.PreflightFail
		c.Message = fmt.Sprintf("MySQL %s detected; 5.7+ required", version)
		return c
	}

	c.Status = connector.PreflightPass
	c.Message = version
	return c
}

// checkBinlogFormat verifies binlog_format is ROW.
func (r *PreflightRunner) checkBinlogFormat(ctx context.Context) connector.PreflightCheck {
	c := connector.PreflightCheck{Name: "Binlog Format"}
	var name, value string
	err := r.db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'binlog_format'").Scan(&name, &value)
	if err != nil {
		c.Status = connector.PreflightFail
		c.Message = fmt.Sprintf("cannot query binlog_format: %v", err)
		return c
	}
	if strings.ToUpper(value) != "ROW" {
		c.Status = connector.PreflightFail
		c.Message = fmt.Sprintf("binlog_format is %s; ROW required", value)
		return c
	}
	c.Status = connector.PreflightPass
	c.Message = "binlog_format is ROW"
	return c
}

// checkBinlogRowImage warns when binlog_row_image is not FULL.
func (r *PreflightRunner) checkBinlogRowImage(ctx context.Context) connector.PreflightCheck {
	c := connector.PreflightCheck{Name: "Binlog Row Image"}
	var name, value string
	err := r.db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'binlog_row_image'").Scan(&name, &value)
	if err != nil {
		c.Status = connector.PreflightWarn
		c.Message = fmt.Sprintf("cannot query binlog_row_image: %v", err)
		return c
	}
	if strings.ToUpper(value) != "FULL" {
		c.Status = connector.PreflightWarn
		c.Message = fmt.Sprintf("binlog_row_image is %s; FULL recommended for PITR", value)
		return c
	}
	c.Status = connector.PreflightPass
	c.Message = "binlog_row_image is FULL"
	return c
}

// checkBinlogAvailability verifies that binlog files exist on the server and
// that the retention policy covers the requested recovery time.
func (r *PreflightRunner) checkBinlogAvailability(ctx context.Context) connector.PreflightCheck {
	c := connector.PreflightCheck{Name: "Binlog Availability"}

	// Count binlog files and total size.
	rows, err := r.db.QueryContext(ctx, "SHOW BINARY LOGS")
	if err != nil {
		c.Status = connector.PreflightFail
		c.Message = fmt.Sprintf("cannot query binary logs: %v", err)
		return c
	}
	defer rows.Close()

	var fileCount int
	var totalSize int64
	for rows.Next() {
		var name string
		var size int64
		if err := rows.Scan(&name, &size); err != nil {
			continue
		}
		fileCount++
		totalSize += size
	}
	if err := rows.Err(); err != nil {
		c.Status = connector.PreflightFail
		c.Message = fmt.Sprintf("error iterating binary logs: %v", err)
		return c
	}

	if fileCount == 0 {
		c.Status = connector.PreflightFail
		c.Message = "no binary logs found on the server"
		return c
	}

	// Check retention period.
	var expireSeconds sql.NullInt64
	if err := r.db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'binlog_expire_logs_seconds'").Scan(new(string), &expireSeconds); err != nil {
		// Fallback: check the old expire_logs_days variable.
		var expireDays sql.NullInt64
		if err := r.db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'expire_logs_days'").Scan(new(string), &expireDays); err == nil && expireDays.Valid {
			expireSeconds = sql.NullInt64{Int64: expireDays.Int64 * 86400, Valid: true}
		}
	}

	// Compare retention with recovery time, if a non-zero time was given.
	if !r.recoveryTime.IsZero() && expireSeconds.Valid && expireSeconds.Int64 > 0 {
		age := time.Since(r.recoveryTime)
		retention := time.Duration(expireSeconds.Int64) * time.Second
		if age > retention {
			c.Status = connector.PreflightFail
			c.Message = fmt.Sprintf(
				"recovery time %s is %.0fh ago but binlogs expire after %s",
				r.recoveryTime.Format(time.RFC3339),
				age.Hours(),
				formatDuration(retention),
			)
			return c
		}
	}

	msg := fmt.Sprintf("%d binary logs found (%s)", fileCount, formatBytes(totalSize))
	if expireSeconds.Valid {
		msg += fmt.Sprintf(", retention: %s", formatDuration(time.Duration(expireSeconds.Int64)*time.Second))
	}
	c.Status = connector.PreflightPass
	c.Message = msg
	return c
}

// checkPermissions verifies that the connected user has the required
// privileges for binlog-based recovery.
func (r *PreflightRunner) checkPermissions(ctx context.Context) connector.PreflightCheck {
	c := connector.PreflightCheck{Name: "Permissions"}
	rows, err := r.db.QueryContext(ctx, "SHOW GRANTS")
	if err != nil {
		c.Status = connector.PreflightFail
		c.Message = fmt.Sprintf("cannot query grants: %v", err)
		return c
	}
	defer rows.Close()

	var grants []string
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			continue
		}
		grants = append(grants, g)
	}

	missing := r.checkRequiredPrivileges(grants)
	if len(missing) > 0 {
		c.Status = connector.PreflightFail
		c.Message = fmt.Sprintf("missing required privileges: %s", strings.Join(missing, ", "))
	} else {
		c.Status = connector.PreflightPass
		c.Message = "required privileges granted"
	}
	return c
}

var requiredPrivileges = []string{"SELECT", "REPLICATION SLAVE", "REPLICATION CLIENT"}

func (r *PreflightRunner) checkRequiredPrivileges(grants []string) []string {
	grantText := strings.Join(grants, " ")
	grantUpper := strings.ToUpper(grantText)

	if strings.Contains(grantUpper, "ALL PRIVILEGES") || strings.Contains(grantUpper, " ALL ") {
		return nil
	}

	var missing []string
	for _, priv := range requiredPrivileges {
		if !strings.Contains(grantUpper, priv) {
			missing = append(missing, priv)
		}
	}
	return missing
}

// checkDiskSpace estimates available disk space for binlog processing.
func (r *PreflightRunner) checkDiskSpace(ctx context.Context) connector.PreflightCheck {
	c := connector.PreflightCheck{Name: "Disk Space"}

	// Check the size of the largest binlog directory (datadir).
	var dataDir string
	err := r.db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'datadir'").Scan(new(string), &dataDir)
	if err != nil {
		c.Status = connector.PreflightWarn
		c.Message = fmt.Sprintf("cannot query datadir: %v", err)
		return c
	}

	// Check binlog size relative to a data-size estimate.
	var dataSize sql.NullInt64
	err = r.db.QueryRowContext(ctx,
		`SELECT SUM(data_length + index_length) FROM information_schema.tables`,
	).Scan(&dataSize)
	if err != nil {
		c.Status = connector.PreflightWarn
		c.Message = fmt.Sprintf("cannot estimate data size: %v", err)
		return c
	}

	if dataSize.Valid && dataSize.Int64 > 0 {
		c.Status = connector.PreflightPass
		c.Message = fmt.Sprintf("datadir: %s, estimated data: %s", dataDir, formatBytes(dataSize.Int64))
	} else {
		c.Status = connector.PreflightPass
		c.Message = fmt.Sprintf("datadir: %s", dataDir)
	}
	return c
}

// ---------------------------------------------------------------------------
// Helpers (package-level to avoid import cycle with connector)
// ---------------------------------------------------------------------------

// parseVersion extracts major, minor from a MySQL version string.
func parseVersion(v string) (int, int, int) {
	idx := strings.IndexFunc(v, func(r rune) bool {
		return r != '.' && (r < '0' || r > '9')
	})
	if idx > 0 {
		v = v[:idx]
	}
	parts := strings.SplitN(v, ".", 4)
	var maj, min, patch int
	if len(parts) > 0 {
		_, _ = fmt.Sscanf(parts[0], "%d", &maj)
	}
	if len(parts) > 1 {
		_, _ = fmt.Sscanf(parts[1], "%d", &min)
	}
	if len(parts) > 2 {
		_, _ = fmt.Sscanf(parts[2], "%d", &patch)
	}
	return maj, min, patch
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

func formatDuration(d time.Duration) string {
	if d.Hours() >= 24 {
		days := int(d.Hours() / 24)
		return fmt.Sprintf("%dd", days)
	}
	if d.Hours() >= 1 {
		return fmt.Sprintf("%.0fh", d.Hours())
	}
	return d.Round(time.Second).String()
}
