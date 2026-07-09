package parser

import (
	"encoding/binary"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/a-shan/mysql-pitr/internal/connector"
)

// DDLEvent represents a DDL statement detected in the binlog stream.
type DDLEvent struct {
	Timestamp  time.Time
	Statement  string // Full SQL of the DDL
	ObjectType string // "TABLE", "DATABASE"
	ObjectName string // Fully qualified name if TABLE
	Position   uint32 // Binlog position (start offset of the event)
}

// ParseOptions controls binlog parsing behavior such as time-range
// and table filtering.
type ParseOptions struct {
	StartTime   time.Time
	EndTime     time.Time
	TargetTable string // schema.table format, e.g. "mydb.orders"
}

// BinlogParser orchestrates the parsing of MySQL binlog files.
// It reads events sequentially, maintains a TableMapRegistry, and produces
// connector.RowEvent structs suitable for flashback operations.
type BinlogParser struct {
	reader          *BinlogReader
	reg             *TableMapRegistry
	rp              *RowEventParser
	events          []connector.RowEvent
	checksumEnabled bool
	skipChecksum    bool
	ddlEvents       []DDLEvent
	opts            ParseOptions
}

// NewBinlogParser creates a new BinlogParser.
func NewBinlogParser() *BinlogParser {
	reg := NewTableMapRegistry()
	return &BinlogParser{
		reader: NewBinlogReader(),
		reg:    reg,
		rp:     NewRowEventParser(reg),
	}
}

// DDLEvents returns any DDL events detected during the last ParseFiles call.
func (p *BinlogParser) DDLEvents() []DDLEvent {
	return p.ddlEvents
}

// Open opens a binlog file and validates the magic number.
func (p *BinlogParser) Open(path string) error {
	return p.reader.Open(path)
}

// Close closes the current binlog file.
func (p *BinlogParser) Close() error {
	// Reset parser state
	p.reg = NewTableMapRegistry()
	p.rp.SetRegistry(p.reg)
	p.events = nil
	p.checksumEnabled = false
	return p.reader.Close()
}

// SetSkipChecksum controls whether CRC32 verification is skipped.
func (p *BinlogParser) SetSkipChecksum(skip bool) {
	p.skipChecksum = skip
}

// Parse reads and parses all events from the currently open binlog file.
// It returns any connector.RowEvent found.
//
// For multi-file parsing, use ParseFiles instead.
func (p *BinlogParser) Parse() ([]connector.RowEvent, error) {
	if !p.reader.IsOpen() {
		return nil, fmt.Errorf("binlog not open")
	}

	p.events = nil

	for {
		hdr, payload, err := p.reader.ReadEvent()
		if err == io.EOF {
			break
		}
		if err != nil {
			return p.events, fmt.Errorf("read event at position %d: %w",
				p.reader.Position(), err)
		}

		// Time-range filtering: skip events before StartTime.
		// Always process FormatDescriptionEvent for checksum detection.
		if !p.opts.StartTime.IsZero() && hdr.Type != FormatDescriptionEvent {
			ts := time.Unix(int64(hdr.Timestamp), 0).UTC()
			if ts.Before(p.opts.StartTime) {
				continue
			}
		}

		// Time-range filtering: stop after EndTime
		if !p.opts.EndTime.IsZero() {
			ts := time.Unix(int64(hdr.Timestamp), 0).UTC()
			if ts.After(p.opts.EndTime) {
				break
			}
		}

		if err := p.dispatch(hdr, payload); err != nil {
			return p.events, fmt.Errorf("dispatch event type %s at position %d: %w",
				hdr.Type.String(), p.reader.Position(), err)
		}
	}

	return p.events, nil
}

// ParseFiles opens and parses multiple binlog files sequentially.
// The first file's magic number is validated; subsequent files are expected to
// start with the magic number (validated automatically by Open).
// It applies the provided ParseOptions for time-range and table filtering.
func (p *BinlogParser) ParseFiles(paths []string, opts ParseOptions) (*connector.ParseResult, error) {
	p.opts = opts
	p.ddlEvents = nil

	var allEvents []connector.RowEvent

	for i, path := range paths {
		if err := p.Open(path); err != nil {
			return &connector.ParseResult{Events: allEvents, TotalRows: int64(len(allEvents))},
				fmt.Errorf("open file %s: %w", path, err)
		}

		events, err := p.Parse()
		if err != nil {
			// If this is the last file and there was an error, return partial
			if i == len(paths)-1 {
				p.Close()
				return &connector.ParseResult{Events: allEvents, TotalRows: int64(len(allEvents))}, err
			}
			// For intermediate files, log and continue
		}

		allEvents = append(allEvents, events...)
		p.Close()

		// Reset state between files (table maps from previous file are stale)
		p.reg = NewTableMapRegistry()
		p.rp.SetRegistry(p.reg)
		p.checksumEnabled = false
	}

	return &connector.ParseResult{
		Events:    allEvents,
		TotalRows: int64(len(allEvents)),
	}, nil
}

// dispatch processes a single binlog event based on its type.
func (p *BinlogParser) dispatch(hdr *EventHeader, payload []byte) error {
	switch hdr.Type {
	case FormatDescriptionEvent:
		return p.handleFormatDescriptionEvent(hdr, payload)

	case TableMapEvent:
		return p.handleTableMapEvent(hdr, payload)

	case WriteRowsEventV1:
		return p.handleWriteRowsEvent(hdr, payload, false)

	case WriteRowsEventV2:
		return p.handleWriteRowsEvent(hdr, payload, true)

	case UpdateRowsEventV1:
		return p.handleUpdateRowsEvent(hdr, payload, false)

	case UpdateRowsEventV2:
		return p.handleUpdateRowsEvent(hdr, payload, true)

	case DeleteRowsEventV1:
		return p.handleDeleteRowsEvent(hdr, payload, false)

	case DeleteRowsEventV2:
		return p.handleDeleteRowsEvent(hdr, payload, true)

	case RotateEvent:
		return p.handleRotateEvent(hdr, payload)

	case XidEvent:
		// XID marks transaction commit; no row data to extract
		return nil

	case QueryEvent:
		return p.handleQueryEvent(hdr, payload)

	default:
		// Unknown/unsupported events are silently skipped
		return nil
	}
}

// handleFormatDescriptionEvent processes a FormatDescriptionEvent.
// It extracts whether checksums are enabled and the header length.
func (p *BinlogParser) handleFormatDescriptionEvent(hdr *EventHeader, payload []byte) error {
	if len(payload) < 2+50+4+1 {
		return fmt.Errorf("FormatDescriptionEvent payload too short: %d bytes", len(payload))
	}

	// Bytes 0-1: binlog version (always 4 for v4)
	// Bytes 2-51: MySQL server version (50 bytes)
	// Bytes 52-55: creation timestamp
	// Byte 56: common header length (usually 19 for v4)

	// For now, if the payload length > 57 + 35 (approximate), assume checksums enabled.
	if len(payload) > 57+35 {
		lastByte := payload[len(payload)-1]
		if lastByte == 1 && !p.skipChecksum {
			p.checksumEnabled = true
			p.reader.EnableChecksum()
		}
	}

	return nil
}

// handleTableMapEvent parses a TableMapEvent and stores it in the registry.
func (p *BinlogParser) handleTableMapEvent(hdr *EventHeader, payload []byte) error {
	tm, err := ParseTableMap(payload)
	if err != nil {
		return fmt.Errorf("parse TableMap: %w", err)
	}
	p.reg.Set(tm)
	return nil
}

// handleWriteRowsEvent parses a WriteRows event and converts to RowEvent entries.
func (p *BinlogParser) handleWriteRowsEvent(hdr *EventHeader, payload []byte, v2 bool) error {
	data, err := p.rp.ParseWriteRowsEvent(payload, v2)
	if err != nil {
		return err
	}
	return p.convertToRowEvents(hdr, data, connector.InsertEvent)
}

// handleDeleteRowsEvent parses a DeleteRows event and converts to RowEvent entries.
func (p *BinlogParser) handleDeleteRowsEvent(hdr *EventHeader, payload []byte, v2 bool) error {
	data, err := p.rp.ParseDeleteRowsEvent(payload, v2)
	if err != nil {
		return err
	}
	return p.convertToRowEvents(hdr, data, connector.DeleteEvent)
}

// handleUpdateRowsEvent parses an UpdateRows event and converts to RowEvent entries.
func (p *BinlogParser) handleUpdateRowsEvent(hdr *EventHeader, payload []byte, v2 bool) error {
	data, err := p.rp.ParseUpdateRowsEvent(payload, v2)
	if err != nil {
		return err
	}
	return p.convertToRowEvents(hdr, data, connector.UpdateEvent)
}

// handleRotateEvent processes a RotateEvent (updates binlog file name).
func (p *BinlogParser) handleRotateEvent(hdr *EventHeader, payload []byte) error {
	// RotateEvent format: 8 bytes position + binlog filename (null-terminated)
	if len(payload) < 8 {
		return fmt.Errorf("RotateEvent payload too short: %d bytes", len(payload))
	}
	// The next position in the new binlog file
	// Filename starts at offset 8
	// We don't auto-follow the rotation; ParseFiles handles multi-file parsing.
	return nil
}

// handleQueryEvent processes a QueryEvent. It checks whether the event
// contains a DDL statement and records DDL markers accordingly.
func (p *BinlogParser) handleQueryEvent(hdr *EventHeader, payload []byte) error {
	if len(payload) < 13 {
		return nil
	}

	// Post-header layout (13 bytes):
	//   [0:4]  thread_id
	//   [4:8]  execution_time
	//   [8]    database name length
	//   [9:11] error_code
	//   [11:13] status_vars_length

	dbLen := int(payload[8])
	statusVarsLen := int(binary.LittleEndian.Uint16(payload[11:13]))

	pos := 13 + statusVarsLen
	if pos+dbLen+1 > len(payload) {
		return nil
	}

	dbName := string(payload[pos : pos+dbLen])
	pos += dbLen + 1

	sqlText := string(payload[pos:])

	// Check for DDL
	if isDDL(sqlText) {
		objType, objName := extractDDLInfo(sqlText, dbName)
		// Compute event start position: NextPos - EventLen
		eventPos := hdr.NextPos - hdr.EventLen
		ddl := DDLEvent{
			Timestamp:  time.Unix(int64(hdr.Timestamp), 0).UTC(),
			Statement:  sqlText,
			ObjectType: objType,
			ObjectName: objName,
			Position:   eventPos,
		}
		p.ddlEvents = append(p.ddlEvents, ddl)
	}

	return nil
}

// convertToRowEvents converts parsed RowEventData entries into connector.RowEvent entries.
// It applies table filtering if ParseOptions.TargetTable is set.
func (p *BinlogParser) convertToRowEvents(hdr *EventHeader, data *RowEventData, eventType connector.EventType) error {
	tm := p.reg.Get(data.TableID)
	dbName := ""
	tblName := ""
	if tm != nil {
		dbName = tm.Database
		tblName = tm.Table
	}

	// Table filtering
	if p.opts.TargetTable != "" {
		qualified := dbName + "." + tblName
		if qualified != p.opts.TargetTable {
			// Skip events for non-matching tables
			return nil
		}
	}

	ts := time.Unix(int64(hdr.Timestamp), 0).UTC()

	for _, row := range data.Rows {
		re := connector.RowEvent{
			Type:      eventType,
			Database:  dbName,
			Table:     tblName,
			Timestamp: ts,
			Before:    row.Before,
			After:     row.After,
		}

		// Populate PrimaryKey from Before (DELETE, UPDATE) or After (INSERT)
		if re.PrimaryKey == nil {
			if re.Before != nil {
				re.PrimaryKey = extractPrimaryKey(re.Before)
			} else if re.After != nil {
				re.PrimaryKey = extractPrimaryKey(re.After)
			}
		}

		p.events = append(p.events, re)
	}

	return nil
}

// extractPrimaryKey attempts to identify primary key columns from a value map.
// Without schema information, we cannot reliably determine PK columns.
// For now, we return an empty map; the caller should resolve PKs via schema.
func extractPrimaryKey(vals map[string]interface{}) map[string]interface{} {
	// With positional keys, we cannot know which columns form the PK.
	// Return nil; the rollback layer should resolve this.
	return nil
}

// ============================================================
// DDL Detection Helpers
// ============================================================

// ddlKeywords lists the DDL statement keywords we detect.
var ddlKeywords = []string{
	"ALTER ",
	"CREATE ",
	"DROP ",
	"TRUNCATE ",
	"RENAME ",
}

// ddlPrefixes maps SQL keyword to object type, ordered by specificity
// (longer match first to avoid "ALTER " matching when "ALTER TABLE " is needed).
var ddlPrefixes = []struct {
	keyword  string
	objType  string
}{
	{"ALTER TABLE ", "TABLE"},
	{"ALTER DATABASE ", "DATABASE"},
	{"ALTER SCHEMA ", "DATABASE"},
	{"CREATE TABLE ", "TABLE"},
	{"CREATE DATABASE ", "DATABASE"},
	{"CREATE SCHEMA ", "DATABASE"},
	{"DROP TABLE ", "TABLE"},
	{"DROP DATABASE ", "DATABASE"},
	{"DROP SCHEMA ", "DATABASE"},
	{"TRUNCATE TABLE ", "TABLE"},
	{"TRUNCATE ", "TABLE"},
	{"RENAME TABLE ", "TABLE"},
	{"ALTER ", "TABLE"},       // fallback: assume TABLE
	{"CREATE ", "TABLE"},      // fallback: assume TABLE
	{"DROP ", "TABLE"},        // fallback: assume TABLE
}

// isDDL returns true if the SQL text appears to be a DDL statement.
func isDDL(sql string) bool {
	upper := strings.ToUpper(strings.TrimSpace(sql))
	for _, kw := range ddlKeywords {
		if strings.HasPrefix(upper, kw) {
			return true
		}
	}
	return false
}

// extractDDLInfo parses the SQL and current database name to determine
// the object type and fully qualified object name.
func extractDDLInfo(sql, currentDB string) (objType, objName string) {
	upper := strings.ToUpper(strings.TrimSpace(sql))

	var matchedKeyword, matchedType string
	for _, entry := range ddlPrefixes {
		if strings.Contains(upper, entry.keyword) {
			matchedKeyword = entry.keyword
			matchedType = entry.objType
			break
		}
	}

	if matchedKeyword == "" {
		return "", ""
	}

	objType = matchedType

	// Find the keyword in the original SQL (preserving case)
	sqlUpper := strings.ToUpper(sql)
	idx := strings.Index(sqlUpper, matchedKeyword)
	if idx < 0 {
		return objType, ""
	}

	rest := strings.TrimSpace(sql[idx+len(matchedKeyword):])
	name := extractIdentifier(rest)
	if name == "" {
		return objType, ""
	}

	// If the SQL itself is qualified (contains a dot), use it as-is.
	// Otherwise, qualify with the current database for TABLE objects.
	if strings.Contains(name, ".") {
		objName = name
	} else if objType == "TABLE" && currentDB != "" {
		objName = currentDB + "." + name
	} else {
		objName = name
	}

	return
}

// extractIdentifier extracts the first identifier (table or database name)
// from a SQL fragment. It handles backtick quoting (including escaped
// backticks via doubling: ```` represents a literal backtick) and stops at the
// first non-identifier character (space, comma, parenthesis, etc.).
func extractIdentifier(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	var name strings.Builder
	i := 0
	for i < len(s) {
		ch := s[i]
		if ch == '`' {
			// Backtick-quoted segment
			i++
			// Read until closing backtick, handling escaped backticks (``)
			for i < len(s) {
				if s[i] == '`' {
					// Check if this is an escaped backtick
					if i+1 < len(s) && s[i+1] == '`' {
						name.WriteByte('`')
						i += 2
						continue
					}
					// This is the closing backtick
					i++
					break
				}
				name.WriteByte(s[i])
				i++
			}
		} else if ch == '.' {
			name.WriteByte('.')
			i++
		} else if ch == ' ' || ch == '\t' || ch == ',' || ch == '(' || ch == ')' || ch == ';' {
			break
		} else {
			name.WriteByte(ch)
			i++
		}
	}

	return name.String()
}
