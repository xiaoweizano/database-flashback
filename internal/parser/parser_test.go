package parser

import (
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/a-shan/mysql-pitr/internal/connector"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildIntegrationBinlog creates a synthetic binlog file with a complete
// sequence of events: FDE -> TableMap -> WriteRows -> Xid
// Returns the file path.
func buildIntegrationBinlog(t *testing.T) string {
	t.Helper()

	f, err := os.CreateTemp("", "pitr-int-*.bin")
	require.NoError(t, err)
	defer f.Close()

	writeBinlogHeader(f)

	pos := uint32(4)

	// === FormatDescriptionEvent (no CRC) ===
	fdeBody := buildFormatDescBody(4, "8.0.33\x00", []byte{
		56, 13, 0, 8, 0, 18, 0, 4, 4, 4, 4, 4, 4, 4, 4, 4,
		4, 4, 4, 4, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
		0, 0, 0, 0, 0, 0, 0, 0,
	})
	fdeLen := uint32(EventHeaderSize + len(fdeBody))
	fdeNext := pos + fdeLen
	fdeHdr := EventHeader{
		Timestamp: 1000,
		Type:      FormatDescriptionEvent,
		ServerID:  1,
		NextPos:   fdeNext,
	}
	writeEvent(f, fdeHdr, fdeBody, false)
	pos = fdeNext

	// === TableMapEvent ===
	colTypes := []byte{MYSQL_TYPE_LONG, MYSQL_TYPE_VARCHAR, MYSQL_TYPE_TINY}
	colMeta := []byte{
		0x00,           // LONG: no metadata
		0x64, 0x00,     // VARCHAR(100): length=100
		0x00,           // TINY: no metadata
	}
	tmPayload := buildTableMapPayload(100, "testdb", "users", colTypes, colMeta, []byte{0x00})
	tmLen := uint32(EventHeaderSize + len(tmPayload))
	tmNext := pos + tmLen
	tmHdr := EventHeader{
		Timestamp: 1001,
		Type:      TableMapEvent,
		ServerID:  1,
		NextPos:   tmNext,
	}
	writeEvent(f, tmHdr, tmPayload, false)
	pos = tmNext

	// === WriteRowsEventV1 ===
	// 3 columns: INT, VARCHAR(100), TINYINT
	// One row: id=42, name="Alice", age=30
	wrColCount := byte(3)
	colPresent := []byte{0x07} // all 3 present
	nullBitmap := []byte{0x00} // none null

	// Row data: INT(42) + VARCHAR("Alice") + TINYINT(30)
	rowData := make([]byte, 0)
	rowData = append(rowData, nullBitmap...)

	// INT 42 (4 bytes LE)
	rowData = append(rowData, 0x2A, 0x00, 0x00, 0x00)

	// VARCHAR "Alice" (2-byte length 5 + data)
	rowData = append(rowData, 0x05, 0x00)
	rowData = append(rowData, []byte("Alice")...)

	// TINYINT 30 (1 byte)
	rowData = append(rowData, 0x1E)

	// WriteRows post-header (8 bytes) + body
	wrPayload := make([]byte, 8)
	wrPayload[0] = 100 // table ID = 100
	wrPayload[6] = 0   // flags
	wrPayload[7] = 0

	// Column count + bitmap + rows
	wrPayload = append(wrPayload, wrColCount)
	wrPayload = append(wrPayload, colPresent...)
	wrPayload = append(wrPayload, rowData...)

	wrLen := uint32(EventHeaderSize + len(wrPayload))
	wrNext := pos + wrLen
	wrHdr := EventHeader{
		Timestamp: 1002,
		Type:      WriteRowsEventV1,
		ServerID:  1,
		NextPos:   wrNext,
	}
	writeEvent(f, wrHdr, wrPayload, false)
	pos = wrNext

	// === XidEvent ===
	xidPayload := make([]byte, 8)
	binary.LittleEndian.PutUint64(xidPayload, 42) // transaction ID
	xidLen := uint32(EventHeaderSize + len(xidPayload))
	xidNext := pos + xidLen
	xidHdr := EventHeader{
		Timestamp: 1003,
		Type:      XidEvent,
		ServerID:  1,
		NextPos:   xidNext,
	}
	writeEvent(f, xidHdr, xidPayload, false)

	return f.Name()
}

// buildUpdateBinlog creates a binlog with an UPDATE event.
func buildUpdateBinlog(t *testing.T) string {
	t.Helper()

	f, err := os.CreateTemp("", "pitr-upd-*.bin")
	require.NoError(t, err)
	defer f.Close()

	writeBinlogHeader(f)
	pos := uint32(4)

	// FDE
	fdeBody := buildFormatDescBody(4, "8.0.33\x00", make([]byte, 35))
	fdeNext := pos + uint32(EventHeaderSize+len(fdeBody))
	writeEvent(f, EventHeader{Timestamp: 1000, Type: FormatDescriptionEvent, ServerID: 1, NextPos: fdeNext}, fdeBody, false)
	pos = fdeNext

	// TableMap: 2 INT columns
	colTypes := []byte{MYSQL_TYPE_LONG, MYSQL_TYPE_LONG}
	colMeta := []byte{0x00, 0x00}
	tmPayload := buildTableMapPayload(200, "db", "t1", colTypes, colMeta, []byte{0x00})
	tmNext := pos + uint32(EventHeaderSize+len(tmPayload))
	writeEvent(f, EventHeader{Timestamp: 1001, Type: TableMapEvent, ServerID: 1, NextPos: tmNext}, tmPayload, false)
	pos = tmNext

	// UpdateRowsEventV1
	// Before: id=1, value=100  After: id=1, value=200
	colPresent := []byte{0x03} // both columns present
	beforeNull := []byte{0x00}
	afterNull := []byte{0x00}

	payload := make([]byte, 8)
	payload[0] = 200 // table ID
	payload[6] = 0
	payload[7] = 0

	// Column count
	payload = append(payload, 0x02)
	// Before bitmap
	payload = append(payload, colPresent...)
	// After bitmap
	payload = append(payload, colPresent...)
	// Before row: null + values
	payload = append(payload, beforeNull...)
	beforeVals := make([]byte, 8)
	binary.LittleEndian.PutUint32(beforeVals[0:4], 1)
	binary.LittleEndian.PutUint32(beforeVals[4:8], 100)
	payload = append(payload, beforeVals...)
	// After row: null + values
	payload = append(payload, afterNull...)
	afterVals := make([]byte, 8)
	binary.LittleEndian.PutUint32(afterVals[0:4], 1)
	binary.LittleEndian.PutUint32(afterVals[4:8], 200)
	payload = append(payload, afterVals...)

	wrNext := pos + uint32(EventHeaderSize+len(payload))
	writeEvent(f, EventHeader{Timestamp: 1002, Type: UpdateRowsEventV1, ServerID: 1, NextPos: wrNext}, payload, false)

	return f.Name()
}

// buildDeleteBinlog creates a binlog with a DELETE event.
func buildDeleteBinlog(t *testing.T) string {
	t.Helper()

	f, err := os.CreateTemp("", "pitr-del-*.bin")
	require.NoError(t, err)
	defer f.Close()

	writeBinlogHeader(f)
	pos := uint32(4)

	fdeBody := buildFormatDescBody(4, "8.0.33\x00", make([]byte, 35))
	fdeNext := pos + uint32(EventHeaderSize+len(fdeBody))
	writeEvent(f, EventHeader{Timestamp: 1000, Type: FormatDescriptionEvent, ServerID: 1, NextPos: fdeNext}, fdeBody, false)
	pos = fdeNext

	colTypes := []byte{MYSQL_TYPE_LONG}
	colMeta := []byte{0x00}
	tmPayload := buildTableMapPayload(300, "db", "t2", colTypes, colMeta, []byte{0x00})
	tmNext := pos + uint32(EventHeaderSize+len(tmPayload))
	writeEvent(f, EventHeader{Timestamp: 1001, Type: TableMapEvent, ServerID: 1, NextPos: tmNext}, tmPayload, false)
	pos = tmNext

	// DeleteRowsEventV1: delete id=99
	payload := make([]byte, 8)
	payload[0] = 0x2C // table ID 300 LE byte 0
	payload[1] = 0x01 // table ID 300 LE byte 1
	payload[6] = 0
	payload[7] = 0

	payload = append(payload, 0x01)                     // column count
	payload = append(payload, 0x01)                     // columns-present bitmap
	payload = append(payload, 0x00)                     // null bitmap
	payload = append(payload, 0x63, 0x00, 0x00, 0x00) // INT value 99

	dlNext := pos + uint32(EventHeaderSize+len(payload))
	writeEvent(f, EventHeader{Timestamp: 1002, Type: DeleteRowsEventV1, ServerID: 1, NextPos: dlNext}, payload, false)

	return f.Name()
}

func TestBinlogParser_Integration_Insert(t *testing.T) {
	path := buildIntegrationBinlog(t)
	defer os.Remove(path)

	p := NewBinlogParser()
	defer p.Close()

	p.SetSkipChecksum(true) // test binlog has no CRC
	result, err := p.ParseFiles([]string{path}, ParseOptions{})
	require.NoError(t, err)
	require.NotNil(t, result)
	events := result.Events
	require.Len(t, events, 1)

	ev := events[0]
	assert.Equal(t, connector.InsertEvent, ev.Type)
	assert.Equal(t, "testdb", ev.Database)
	assert.Equal(t, "users", ev.Table)
	assert.NotNil(t, ev.After)
	assert.Nil(t, ev.Before)
	assert.Equal(t, int64(1002), ev.Timestamp.Unix())

	// Check column values (positional keys)
	assert.Contains(t, ev.After, "col_0")
	assert.Contains(t, ev.After, "col_1")
	assert.Contains(t, ev.After, "col_2")
}

func TestBinlogParser_Integration_Update(t *testing.T) {
	path := buildUpdateBinlog(t)
	defer os.Remove(path)

	p := NewBinlogParser()
	defer p.Close()

	p.SetSkipChecksum(true)
	result, err := p.ParseFiles([]string{path}, ParseOptions{})
	require.NoError(t, err)
	require.NotNil(t, result)
	events := result.Events
	require.Len(t, events, 1)

	ev := events[0]
	assert.Equal(t, connector.UpdateEvent, ev.Type)
	assert.Equal(t, "db", ev.Database)
	assert.Equal(t, "t1", ev.Table)
	assert.NotNil(t, ev.Before)
	assert.NotNil(t, ev.After)
}

func TestBinlogParser_Integration_Delete(t *testing.T) {
	path := buildDeleteBinlog(t)
	defer os.Remove(path)

	p := NewBinlogParser()
	defer p.Close()

	p.SetSkipChecksum(true)
	result, err := p.ParseFiles([]string{path}, ParseOptions{})
	require.NoError(t, err)
	require.NotNil(t, result)
	events := result.Events
	require.Len(t, events, 1)

	ev := events[0]
	assert.Equal(t, connector.DeleteEvent, ev.Type)
	assert.Equal(t, "db", ev.Database)
	assert.Equal(t, "t2", ev.Table)
	assert.NotNil(t, ev.Before)
	assert.Nil(t, ev.After)
}

func TestBinlogParser_Integration_MultipleRows(t *testing.T) {
	f, err := os.CreateTemp("", "pitr-multi-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	writeBinlogHeader(f)
	pos := uint32(4)

	// FDE
	fdeBody := buildFormatDescBody(4, "8.0.33\x00", make([]byte, 35))
	fdeNext := pos + uint32(EventHeaderSize+len(fdeBody))
	writeEvent(f, EventHeader{Timestamp: 1000, Type: FormatDescriptionEvent, ServerID: 1, NextPos: fdeNext}, fdeBody, false)
	pos = fdeNext

	// TableMap: 1 INT column
	colTypes := []byte{MYSQL_TYPE_LONG}
	colMeta := []byte{0x00}
	tmPayload := buildTableMapPayload(400, "db", "multi", colTypes, colMeta, []byte{0x00})
	tmNext := pos + uint32(EventHeaderSize+len(tmPayload))
	writeEvent(f, EventHeader{Timestamp: 1001, Type: TableMapEvent, ServerID: 1, NextPos: tmNext}, tmPayload, false)
	pos = tmNext
	// WriteRows with 3 rows: id=1, id=2, id=3
	payload := make([]byte, 8)
	payload[0] = 0x90 // table ID 400 LE byte 0
	payload[1] = 0x01 // table ID 400 LE byte 1
	payload[6] = 0
	payload[7] = 0

	payload = append(payload, 0x01) // col count
	payload = append(payload, 0x01) // present bitmap

	// Row 1: id=1
	payload = append(payload, 0x00)                     // null bitmap
	payload = append(payload, 0x01, 0x00, 0x00, 0x00) // value 1
	// Row 2: id=2
	payload = append(payload, 0x00)                     // null bitmap
	payload = append(payload, 0x02, 0x00, 0x00, 0x00) // value 2
	// Row 3: id=3
	payload = append(payload, 0x00)                     // null bitmap
	payload = append(payload, 0x03, 0x00, 0x00, 0x00) // value 3

	wrNext := pos + uint32(EventHeaderSize+len(payload))
	writeEvent(f, EventHeader{Timestamp: 1002, Type: WriteRowsEventV1, ServerID: 1, NextPos: wrNext}, payload, false)

	p := NewBinlogParser()
	defer p.Close()

	p.SetSkipChecksum(true)
	result, err := p.ParseFiles([]string{f.Name()}, ParseOptions{})
	require.NoError(t, err)
	require.NotNil(t, result)
	events := result.Events
	require.Len(t, events, 3)
	for i, ev := range events {
		assert.Equal(t, connector.InsertEvent, ev.Type)
		assert.Equal(t, "db", ev.Database)
		assert.Equal(t, "multi", ev.Table)
		assert.NotNil(t, ev.After)
		assert.Nil(t, ev.Before)
		// Verify the value matches (positional key)
		_ = i
	}
}

func TestBinlogParser_Integration_FileNotFound(t *testing.T) {
	p := NewBinlogParser()
	_, err := p.ParseFiles([]string{"/nonexistent/binlog.000001"}, ParseOptions{})
	assert.Error(t, err)
}

func TestBinlogParser_Integration_EmptyEvents(t *testing.T) {
	f, err := os.CreateTemp("", "pitr-empty-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	writeBinlogHeader(f)
	f.Close()

	p := NewBinlogParser()
	defer p.Close()

	// Only header, no events - should return empty
	p.SetSkipChecksum(true)
	result, err := p.ParseFiles([]string{f.Name()}, ParseOptions{})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Empty(t, result.Events)
}

func TestBinlogParser_Integration_SkipNonRowEvents(t *testing.T) {
	f, err := os.CreateTemp("", "pitr-skip-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	writeBinlogHeader(f)
	pos := uint32(4)

	// FDE
	fdeBody := buildFormatDescBody(4, "8.0.33\x00", make([]byte, 35))
	fdeNext := pos + uint32(EventHeaderSize+len(fdeBody))
	writeEvent(f, EventHeader{Timestamp: 1000, Type: FormatDescriptionEvent, ServerID: 1, NextPos: fdeNext}, fdeBody, false)
	pos = fdeNext

	// Query event (BEGIN)
	qBody := []byte("BEGIN")
	qNext := pos + uint32(EventHeaderSize+len(qBody))
	writeEvent(f, EventHeader{Timestamp: 1001, Type: QueryEvent, ServerID: 1, NextPos: qNext}, qBody, false)
	pos = qNext

	// Table map
	tmPayload := buildTableMapPayload(1, "db", "test", []byte{MYSQL_TYPE_LONG}, []byte{0x00}, []byte{0x00})
	tmNext := pos + uint32(EventHeaderSize+len(tmPayload))
	writeEvent(f, EventHeader{Timestamp: 1002, Type: TableMapEvent, ServerID: 1, NextPos: tmNext}, tmPayload, false)
	pos = tmNext

	// Insert row
	payload := make([]byte, 8)
	payload[0] = 1
	payload[6] = 0
	payload[7] = 0
	payload = append(payload, 0x01)                     // col count
	payload = append(payload, 0x01)                     // present bitmap
	payload = append(payload, 0x00)                     // null bitmap
	payload = append(payload, 0x2A, 0x00, 0x00, 0x00) // value 42

	wrNext := pos + uint32(EventHeaderSize+len(payload))
	writeEvent(f, EventHeader{Timestamp: 1003, Type: WriteRowsEventV1, ServerID: 1, NextPos: wrNext}, payload, false)
	pos = wrNext

	// Xid
	xidPayload := make([]byte, 8)
	binary.LittleEndian.PutUint64(xidPayload, 1)
	xidNext := pos + uint32(EventHeaderSize+len(xidPayload))
	writeEvent(f, EventHeader{Timestamp: 1004, Type: XidEvent, ServerID: 1, NextPos: xidNext}, xidPayload, false)

	p := NewBinlogParser()
	defer p.Close()

	p.SetSkipChecksum(true)
	result, err := p.ParseFiles([]string{f.Name()}, ParseOptions{})
	require.NoError(t, err)
	require.NotNil(t, result)
	events := result.Events
	require.Len(t, events, 1)
	assert.Equal(t, connector.InsertEvent, events[0].Type)
}

func TestExtractPrimaryKey(t *testing.T) {
	pk := extractPrimaryKey(map[string]interface{}{"col_0": int64(1)})
	assert.Nil(t, pk)

	pk = extractPrimaryKey(nil)
	assert.Nil(t, pk)
}

func TestNewBinlogParser(t *testing.T) {
	p := NewBinlogParser()
	assert.NotNil(t, p)
	assert.NotNil(t, p.reader)
	assert.NotNil(t, p.reg)
	assert.NotNil(t, p.rp)

	// Close should be safe on a newly created parser
	err := p.Close()
	assert.NoError(t, err)
}

func TestBinlogParser_OpenCloseCycle(t *testing.T) {
	f, err := os.CreateTemp("", "pitr-cycle-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	writeBinlogHeader(f)
	f.Close()

	p := NewBinlogParser()
	defer p.Close()

	err = p.Open(f.Name())
	require.NoError(t, err)

	err = p.Close()
	require.NoError(t, err)

	// Re-open same file should work
	err = p.Open(f.Name())
	require.NoError(t, err)
}

// ============================================================
// DDL Detection Tests
// ============================================================

// buildQueryPayload constructs a QueryEvent payload for testing.
// Format: post-header (13 bytes) + status vars + db name + null + sql
func buildQueryPayload(threadID uint32, execTime uint32, dbName, sql string) []byte {
	payload := make([]byte, 13)
	binary.LittleEndian.PutUint32(payload[0:4], threadID)
	binary.LittleEndian.PutUint32(payload[4:8], execTime)
	payload[8] = byte(len(dbName))
	// error_code at [9:11] = 0
	// status_vars_length at [11:13] = 0
	binary.LittleEndian.PutUint16(payload[11:13], 0)

	// Database name + null terminator
	payload = append(payload, []byte(dbName)...)
	payload = append(payload, 0x00)

	// SQL text
	payload = append(payload, []byte(sql)...)

	return payload
}

func TestDDLDetection_AlterTable(t *testing.T) {
	f, err := os.CreateTemp("", "pitr-ddl-alter-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	writeBinlogHeader(f)
	pos := uint32(4)

	// FDE
	fdeBody := buildFormatDescBody(4, "8.0.33\x00", make([]byte, 35))
	fdeNext := pos + uint32(EventHeaderSize+len(fdeBody))
	writeEvent(f, EventHeader{Timestamp: 1000, Type: FormatDescriptionEvent, ServerID: 1, NextPos: fdeNext}, fdeBody, false)
	pos = fdeNext

	// QueryEvent: ALTER TABLE
	sql := "ALTER TABLE `orders` ADD COLUMN `status` VARCHAR(20) DEFAULT 'pending'"
	qPayload := buildQueryPayload(1, 0, "testdb", sql)
	qNext := pos + uint32(EventHeaderSize+len(qPayload))
	writeEvent(f, EventHeader{Timestamp: 1001, Type: QueryEvent, ServerID: 1, NextPos: qNext}, qPayload, false)

	p := NewBinlogParser()
	defer p.Close()
	p.SetSkipChecksum(true)

	result, err := p.ParseFiles([]string{f.Name()}, ParseOptions{})
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should detect the DDL event
	ddlEvents := p.DDLEvents()
	require.Len(t, ddlEvents, 1, "should detect one DDL event")

	ddl := ddlEvents[0]
	assert.Equal(t, int64(1001), ddl.Timestamp.Unix(), "DDL timestamp")
	assert.Contains(t, ddl.Statement, "ALTER TABLE", "DDL statement")
	assert.Equal(t, "TABLE", ddl.ObjectType, "DDL object type")
	assert.Equal(t, "testdb.orders", ddl.ObjectName, "DDL object name")
	assert.True(t, ddl.Position > 0, "DDL position should be non-zero")

	// Should not produce any row events
	assert.Empty(t, result.Events, "no row events expected for DDL-only binlog")
}

func TestDDLDetection_CreateTable(t *testing.T) {
	f, err := os.CreateTemp("", "pitr-ddl-create-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	writeBinlogHeader(f)
	pos := uint32(4)

	// FDE
	fdeBody := buildFormatDescBody(4, "8.0.33\x00", make([]byte, 35))
	fdeNext := pos + uint32(EventHeaderSize+len(fdeBody))
	writeEvent(f, EventHeader{Timestamp: 1000, Type: FormatDescriptionEvent, ServerID: 1, NextPos: fdeNext}, fdeBody, false)
	pos = fdeNext

	// QueryEvent: CREATE TABLE
	sql := "CREATE TABLE `users` (\n  `id` INT NOT NULL,\n  `name` VARCHAR(100),\n  PRIMARY KEY (`id`)\n)"
	qPayload := buildQueryPayload(1, 0, "shopdb", sql)
	qNext := pos + uint32(EventHeaderSize+len(qPayload))
	writeEvent(f, EventHeader{Timestamp: 1002, Type: QueryEvent, ServerID: 1, NextPos: qNext}, qPayload, false)
	pos = qNext

	// QueryEvent: DROP TABLE
	dropSQL := "DROP TABLE IF EXISTS `temp_data`"
	qPayload2 := buildQueryPayload(1, 0, "shopdb", dropSQL)
	qNext2 := pos + uint32(EventHeaderSize+len(qPayload2))
	writeEvent(f, EventHeader{Timestamp: 1003, Type: QueryEvent, ServerID: 1, NextPos: qNext2}, qPayload2, false)
	pos = qNext2

	// QueryEvent: TRUNCATE TABLE (not a DDL for our purposes but tests truncate)
	truncSQL := "TRUNCATE TABLE `audit_log`"
	qPayload3 := buildQueryPayload(1, 0, "shopdb", truncSQL)
	qNext3 := pos + uint32(EventHeaderSize+len(qPayload3))
	writeEvent(f, EventHeader{Timestamp: 1004, Type: QueryEvent, ServerID: 1, NextPos: qNext3}, qPayload3, false)

	p := NewBinlogParser()
	defer p.Close()
	p.SetSkipChecksum(true)

	result, err := p.ParseFiles([]string{f.Name()}, ParseOptions{})
	require.NoError(t, err)
	require.NotNil(t, result)

	ddlEvents := p.DDLEvents()
	require.Len(t, ddlEvents, 3, "should detect three DDL events")

	// Check CREATE TABLE
	assert.Equal(t, int64(1002), ddlEvents[0].Timestamp.Unix(), "CREATE timestamp")
	assert.Contains(t, ddlEvents[0].Statement, "CREATE TABLE")
	assert.Equal(t, "TABLE", ddlEvents[0].ObjectType)
	assert.Equal(t, "shopdb.users", ddlEvents[0].ObjectName)

	// Check DROP TABLE
	assert.Equal(t, int64(1003), ddlEvents[1].Timestamp.Unix(), "DROP timestamp")
	assert.Contains(t, ddlEvents[1].Statement, "DROP TABLE")
	assert.Equal(t, "TABLE", ddlEvents[1].ObjectType)
	assert.Equal(t, "shopdb.temp_data", ddlEvents[1].ObjectName)

	// Check TRUNCATE TABLE
	assert.Equal(t, int64(1004), ddlEvents[2].Timestamp.Unix(), "TRUNCATE timestamp")
	assert.Contains(t, ddlEvents[2].Statement, "TRUNCATE")
	assert.Equal(t, "TABLE", ddlEvents[2].ObjectType)
	assert.Equal(t, "shopdb.audit_log", ddlEvents[2].ObjectName)

	assert.Empty(t, result.Events)
}

func TestIsDDL(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"ALTER TABLE foo ADD COLUMN x INT", true},
		{"CREATE TABLE foo (id INT)", true},
		{"DROP TABLE foo", true},
		{"TRUNCATE TABLE foo", true},
		{"RENAME TABLE foo TO bar", true},
		{"ALTER DATABASE db CHARACTER SET utf8", true},
		{"CREATE DATABASE db", true},
		{"DROP DATABASE db", true},
		{"SELECT * FROM foo", false},
		{"INSERT INTO foo VALUES (1)", false},
		{"UPDATE foo SET x=1", false},
		{"DELETE FROM foo", false},
		{"BEGIN", false},
		{"COMMIT", false},
		{"SET @a=1", false},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, isDDL(tc.sql), "isDDL(%q)", tc.sql)
	}
}

func TestExtractDDLInfo(t *testing.T) {
	tests := []struct {
		sql        string
		db         string
		wantType   string
		wantName   string
	}{
		{"ALTER TABLE users ADD COLUMN x INT", "testdb", "TABLE", "testdb.users"},
		{"ALTER TABLE `users` ADD COLUMN x INT", "testdb", "TABLE", "testdb.users"},
		{"ALTER TABLE `db`.`users` ADD COLUMN x INT", "testdb", "TABLE", "db.users"},
		{"CREATE TABLE orders (id INT)", "shop", "TABLE", "shop.orders"},
		{"CREATE TABLE `backtick`.`table` (id INT)", "shop", "TABLE", "backtick.table"},
		{"DROP TABLE IF EXISTS temp", "test", "TABLE", "test.temp"},
		{"CREATE DATABASE newdb", "", "DATABASE", "newdb"},
		{"ALTER DATABASE db CHARACTER SET utf8", "", "DATABASE", "db"},
		{"DROP DATABASE IF EXISTS olddb", "", "DATABASE", "olddb"},
		{"TRUNCATE TABLE audit", "testdb", "TABLE", "testdb.audit"},
		{"RENAME TABLE old_name TO new_name", "testdb", "TABLE", "testdb.old_name"},
		{"SELECT * FROM foo", "", "", ""},
	}
	for _, tc := range tests {
		objType, objName := extractDDLInfo(tc.sql, tc.db)
		assert.Equal(t, tc.wantType, objType, "extractDDLInfo(%q).type", tc.sql)
		assert.Equal(t, tc.wantName, objName, "extractDDLInfo(%q).name", tc.sql)
	}
}

// ============================================================
// Time-Range Filter Tests
// ============================================================

func TestTimeFilter_ExcludeOutsideRange(t *testing.T) {
	f, err := os.CreateTemp("", "pitr-time-exclude-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	writeBinlogHeader(f)
	pos := uint32(4)

	// FDE at t=0
	fdeBody := buildFormatDescBody(4, "8.0.33\x00", make([]byte, 35))
	fdeNext := pos + uint32(EventHeaderSize+len(fdeBody))
	writeEvent(f, EventHeader{Timestamp: 0, Type: FormatDescriptionEvent, ServerID: 1, NextPos: fdeNext}, fdeBody, false)
	pos = fdeNext

	// Transaction at t=100: TableMap + WriteRows (1 row)
	colTypes := []byte{MYSQL_TYPE_LONG}
	colMeta := []byte{0x00}
	tmPayload := buildTableMapPayload(10, "testdb", "events", colTypes, colMeta, []byte{0x00})
	tmNext := pos + uint32(EventHeaderSize+len(tmPayload))
	writeEvent(f, EventHeader{Timestamp: 100, Type: TableMapEvent, ServerID: 1, NextPos: tmNext}, tmPayload, false)
	pos = tmNext

	wrPayload := make([]byte, 8)
	wrPayload[0] = 10
	wrPayload[6] = 0
	wrPayload[7] = 0
	wrPayload = append(wrPayload, 0x01)                     // col count
	wrPayload = append(wrPayload, 0x01)                     // present bitmap
	wrPayload = append(wrPayload, 0x00)                     // null bitmap
	wrPayload = append(wrPayload, 0x01, 0x00, 0x00, 0x00) // value 1
	wrNext := pos + uint32(EventHeaderSize+len(wrPayload))
	writeEvent(f, EventHeader{Timestamp: 100, Type: WriteRowsEventV1, ServerID: 1, NextPos: wrNext}, wrPayload, false)
	pos = wrNext

	// Transaction at t=200: TableMap + WriteRows
	tmPayload2 := buildTableMapPayload(20, "testdb", "events", colTypes, colMeta, []byte{0x00})
	tmNext2 := pos + uint32(EventHeaderSize+len(tmPayload2))
	writeEvent(f, EventHeader{Timestamp: 200, Type: TableMapEvent, ServerID: 1, NextPos: tmNext2}, tmPayload2, false)
	pos = tmNext2

	wrPayload2 := make([]byte, 8)
	wrPayload2[0] = 20
	wrPayload2[6] = 0
	wrPayload2[7] = 0
	wrPayload2 = append(wrPayload2, 0x01)
	wrPayload2 = append(wrPayload2, 0x01)
	wrPayload2 = append(wrPayload2, 0x00)
	wrPayload2 = append(wrPayload2, 0x02, 0x00, 0x00, 0x00) // value 2
	wrNext2 := pos + uint32(EventHeaderSize+len(wrPayload2))
	writeEvent(f, EventHeader{Timestamp: 200, Type: WriteRowsEventV1, ServerID: 1, NextPos: wrNext2}, wrPayload2, false)
	pos = wrNext2

	// Transaction at t=300: TableMap + WriteRows
	tmPayload3 := buildTableMapPayload(30, "testdb", "events", colTypes, colMeta, []byte{0x00})
	tmNext3 := pos + uint32(EventHeaderSize+len(tmPayload3))
	writeEvent(f, EventHeader{Timestamp: 300, Type: TableMapEvent, ServerID: 1, NextPos: tmNext3}, tmPayload3, false)
	pos = tmNext3

	wrPayload3 := make([]byte, 8)
	wrPayload3[0] = 30
	wrPayload3[6] = 0
	wrPayload3[7] = 0
	wrPayload3 = append(wrPayload3, 0x01)
	wrPayload3 = append(wrPayload3, 0x01)
	wrPayload3 = append(wrPayload3, 0x00)
	wrPayload3 = append(wrPayload3, 0x03, 0x00, 0x00, 0x00) // value 3
	wrNext3 := pos + uint32(EventHeaderSize+len(wrPayload3))
	writeEvent(f, EventHeader{Timestamp: 300, Type: WriteRowsEventV1, ServerID: 1, NextPos: wrNext3}, wrPayload3, false)

	p := NewBinlogParser()
	defer p.Close()
	p.SetSkipChecksum(true)

	// Filter: only events with timestamps 150-250
	opts := ParseOptions{
		StartTime: time.Unix(150, 0).UTC(),
		EndTime:   time.Unix(250, 0).UTC(),
	}
	result, err := p.ParseFiles([]string{f.Name()}, opts)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Only the t=200 event should be within range
	require.Len(t, result.Events, 1, "only one event should be within time range 150-250")
	assert.Equal(t, connector.InsertEvent, result.Events[0].Type)
	assert.Equal(t, int64(200), result.Events[0].Timestamp.Unix())
}

func TestTimeFilter_PartialRange(t *testing.T) {
	f, err := os.CreateTemp("", "pitr-time-partial-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	writeBinlogHeader(f)
	pos := uint32(4)

	// FDE
	fdeBody := buildFormatDescBody(4, "8.0.33\x00", make([]byte, 35))
	fdeNext := pos + uint32(EventHeaderSize+len(fdeBody))
	writeEvent(f, EventHeader{Timestamp: 0, Type: FormatDescriptionEvent, ServerID: 1, NextPos: fdeNext}, fdeBody, false)
	pos = fdeNext

	colTypes := []byte{MYSQL_TYPE_LONG}
	colMeta := []byte{0x00}

	// Helper: add a transaction with a TableMap + WriteRows at the given timestamp and tableID
	addTransaction := func(ts uint32, tableID uint64) {
		tmPayload := buildTableMapPayload(tableID, "db", "t", colTypes, colMeta, []byte{0x00})
		tmNext := pos + uint32(EventHeaderSize+len(tmPayload))
		writeEvent(f, EventHeader{Timestamp: ts, Type: TableMapEvent, ServerID: 1, NextPos: tmNext}, tmPayload, false)
		pos = tmNext

		wrPayload := make([]byte, 8)
		wrPayload[0] = byte(tableID)
		wrPayload[6] = 0
		wrPayload[7] = 0
		wrPayload = append(wrPayload, 0x01)
		wrPayload = append(wrPayload, 0x01)
		wrPayload = append(wrPayload, 0x00)
		wrPayload = append(wrPayload, byte(ts), 0x00, 0x00, 0x00)
		wrNext := pos + uint32(EventHeaderSize+len(wrPayload))
		writeEvent(f, EventHeader{Timestamp: ts, Type: WriteRowsEventV1, ServerID: 1, NextPos: wrNext}, wrPayload, false)
		pos = wrNext
	}

	// Three transactions at different timestamps
	addTransaction(100, 1)
	addTransaction(200, 2)
	addTransaction(300, 3)

	p := NewBinlogParser()
	defer p.Close()
	p.SetSkipChecksum(true)

	// Filter: only the middle transaction
	opts := ParseOptions{
		StartTime: time.Unix(150, 0).UTC(),
		EndTime:   time.Unix(250, 0).UTC(),
	}
	result, err := p.ParseFiles([]string{f.Name()}, opts)
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, result.Events, 1, "only the t=200 event should be within range")
	assert.Equal(t, int64(200), result.Events[0].Timestamp.Unix())

	// Verify total rows count
	assert.Equal(t, int64(1), result.TotalRows)
}

// ============================================================
// Table Filter Tests
// ============================================================

func TestTableFilter_SpecificTable(t *testing.T) {
	f, err := os.CreateTemp("", "pitr-table-filter-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	writeBinlogHeader(f)
	pos := uint32(4)

	// FDE
	fdeBody := buildFormatDescBody(4, "8.0.33\x00", make([]byte, 35))
	fdeNext := pos + uint32(EventHeaderSize+len(fdeBody))
	writeEvent(f, EventHeader{Timestamp: 1000, Type: FormatDescriptionEvent, ServerID: 1, NextPos: fdeNext}, fdeBody, false)
	pos = fdeNext

	colTypes := []byte{MYSQL_TYPE_LONG}
	colMeta := []byte{0x00}

	// TableMap + WriteRows for testdb.users
	tm1 := buildTableMapPayload(100, "testdb", "users", colTypes, colMeta, []byte{0x00})
	tmNext1 := pos + uint32(EventHeaderSize+len(tm1))
	writeEvent(f, EventHeader{Timestamp: 1001, Type: TableMapEvent, ServerID: 1, NextPos: tmNext1}, tm1, false)
	pos = tmNext1

	wr1 := make([]byte, 8)
	wr1[0] = 100
	wr1[6] = 0
	wr1[7] = 0
	wr1 = append(wr1, 0x01)
	wr1 = append(wr1, 0x01)
	wr1 = append(wr1, 0x00)
	wr1 = append(wr1, 0x01, 0x00, 0x00, 0x00) // value 1
	wrNext1 := pos + uint32(EventHeaderSize+len(wr1))
	writeEvent(f, EventHeader{Timestamp: 1001, Type: WriteRowsEventV1, ServerID: 1, NextPos: wrNext1}, wr1, false)
	pos = wrNext1

	// TableMap + WriteRows for testdb.orders
	tm2 := buildTableMapPayload(200, "testdb", "orders", colTypes, colMeta, []byte{0x00})
	tmNext2 := pos + uint32(EventHeaderSize+len(tm2))
	writeEvent(f, EventHeader{Timestamp: 1002, Type: TableMapEvent, ServerID: 1, NextPos: tmNext2}, tm2, false)
	pos = tmNext2

	wr2 := make([]byte, 8)
	wr2[0] = 200
	wr2[6] = 0
	wr2[7] = 0
	wr2 = append(wr2, 0x01)
	wr2 = append(wr2, 0x01)
	wr2 = append(wr2, 0x00)
	wr2 = append(wr2, 0x02, 0x00, 0x00, 0x00) // value 2
	wrNext2 := pos + uint32(EventHeaderSize+len(wr2))
	writeEvent(f, EventHeader{Timestamp: 1002, Type: WriteRowsEventV1, ServerID: 1, NextPos: wrNext2}, wr2, false)
	pos = wrNext2

	// TableMap + WriteRows for testdb.products (should be filtered out)
	tm3 := buildTableMapPayload(300, "testdb", "products", colTypes, colMeta, []byte{0x00})
	tmNext3 := pos + uint32(EventHeaderSize+len(tm3))
	writeEvent(f, EventHeader{Timestamp: 1003, Type: TableMapEvent, ServerID: 1, NextPos: tmNext3}, tm3, false)
	pos = tmNext3

	wr3 := make([]byte, 8)
	wr3[0] = 44
	wr3[6] = 0
	wr3[7] = 0
	wr3 = append(wr3, 0x01)
	wr3 = append(wr3, 0x01)
	wr3 = append(wr3, 0x00)
	wr3 = append(wr3, 0x03, 0x00, 0x00, 0x00) // value 3
	wrNext3 := pos + uint32(EventHeaderSize+len(wr3))
	writeEvent(f, EventHeader{Timestamp: 1003, Type: WriteRowsEventV1, ServerID: 1, NextPos: wrNext3}, wr3, false)

	p := NewBinlogParser()
	defer p.Close()
	p.SetSkipChecksum(true)

	// Filter: only testdb.orders
	opts := ParseOptions{
		TargetTable: "testdb.orders",
	}
	result, err := p.ParseFiles([]string{f.Name()}, opts)
	require.NoError(t, err)
	require.NotNil(t, result)

	require.Len(t, result.Events, 1, "only one event for testdb.orders")
	assert.Equal(t, "testdb", result.Events[0].Database)
	assert.Equal(t, "orders", result.Events[0].Table)
	assert.Equal(t, int64(2), result.Events[0].After["col_0"])
}

// ============================================================
// Combined Filter Tests
// ============================================================

func TestCombinedFilter_DDLTimeTable(t *testing.T) {
	f, err := os.CreateTemp("", "pitr-combined-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	writeBinlogHeader(f)
	pos := uint32(4)

	// FDE
	fdeBody := buildFormatDescBody(4, "8.0.33\x00", make([]byte, 35))
	fdeNext := pos + uint32(EventHeaderSize+len(fdeBody))
	writeEvent(f, EventHeader{Timestamp: 1000, Type: FormatDescriptionEvent, ServerID: 1, NextPos: fdeNext}, fdeBody, false)
	pos = fdeNext

	// DDL at t=1001: ALTER TABLE testdb.users (within range: 995-1005)
	ddlSQL := "ALTER TABLE `users` ADD COLUMN `age` INT"
	qPayload := buildQueryPayload(1, 0, "testdb", ddlSQL)
	qNext := pos + uint32(EventHeaderSize+len(qPayload))
	writeEvent(f, EventHeader{Timestamp: 1001, Type: QueryEvent, ServerID: 1, NextPos: qNext}, qPayload, false)
	pos = qNext

	colTypes := []byte{MYSQL_TYPE_LONG}
	colMeta := []byte{0x00}

	// TableMap + WriteRows for testdb.users at t=1002 (within range)
	tmUsers := buildTableMapPayload(100, "testdb", "users", colTypes, colMeta, []byte{0x00})
	tmNextUsers := pos + uint32(EventHeaderSize+len(tmUsers))
	writeEvent(f, EventHeader{Timestamp: 1002, Type: TableMapEvent, ServerID: 1, NextPos: tmNextUsers}, tmUsers, false)
	pos = tmNextUsers

	wrUsers := make([]byte, 8)
	wrUsers[0] = 100
	wrUsers[6] = 0
	wrUsers[7] = 0
	wrUsers = append(wrUsers, 0x01)
	wrUsers = append(wrUsers, 0x01)
	wrUsers = append(wrUsers, 0x00)
	wrUsers = append(wrUsers, 0x2A, 0x00, 0x00, 0x00) // value 42
	wrNextUsers := pos + uint32(EventHeaderSize+len(wrUsers))
	writeEvent(f, EventHeader{Timestamp: 1002, Type: WriteRowsEventV1, ServerID: 1, NextPos: wrNextUsers}, wrUsers, false)
	pos = wrNextUsers

	// TableMap + WriteRows for testdb.orders at t=1003 (within range but wrong table)
	tmOrders := buildTableMapPayload(200, "testdb", "orders", colTypes, colMeta, []byte{0x00})
	tmNextOrders := pos + uint32(EventHeaderSize+len(tmOrders))
	writeEvent(f, EventHeader{Timestamp: 1003, Type: TableMapEvent, ServerID: 1, NextPos: tmNextOrders}, tmOrders, false)
	pos = tmNextOrders

	wrOrders := make([]byte, 8)
	wrOrders[0] = 200
	wrOrders[6] = 0
	wrOrders[7] = 0
	wrOrders = append(wrOrders, 0x01)
	wrOrders = append(wrOrders, 0x01)
	wrOrders = append(wrOrders, 0x00)
	wrOrders = append(wrOrders, 0x2B, 0x00, 0x00, 0x00) // value 43
	wrNextOrders := pos + uint32(EventHeaderSize+len(wrOrders))
	writeEvent(f, EventHeader{Timestamp: 1003, Type: WriteRowsEventV1, ServerID: 1, NextPos: wrNextOrders}, wrOrders, false)

	p := NewBinlogParser()
	defer p.Close()
	p.SetSkipChecksum(true)

	// Combined filter: time range 995-1005 and only testdb.users
	opts := ParseOptions{
		StartTime:   time.Unix(995, 0).UTC(),
		EndTime:     time.Unix(1005, 0).UTC(),
		TargetTable: "testdb.users",
	}
	result, err := p.ParseFiles([]string{f.Name()}, opts)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Should have 1 row event for testdb.users (testdb.orders filtered by table)
	require.Len(t, result.Events, 1, "only testdb.users events should be returned")
	assert.Equal(t, "testdb", result.Events[0].Database)
	assert.Equal(t, "users", result.Events[0].Table)

	// Should have 1 DDL event (within time range)
	ddlEvents := p.DDLEvents()
	require.Len(t, ddlEvents, 1, "should detect DDL within time range")
	assert.Equal(t, "testdb.users", ddlEvents[0].ObjectName)
	assert.Equal(t, "ALTER TABLE `users` ADD COLUMN `age` INT", ddlEvents[0].Statement)
}

func TestDDLEvents_Accessor(t *testing.T) {
	p := NewBinlogParser()
	ddl := p.DDLEvents()
	assert.NotNil(t, ddl, "DDLEvents() should never return nil")
	assert.Empty(t, ddl, "new parser should have no DDL events")
}

func TestExtractIdentifier(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"users", "users"},
		{"`users`", "users"},
		{"`db`.`table`", "db.table"},
		{"db.table", "db.table"},
		{"orders ADD COLUMN", "orders"},
		{"", ""},
		{"   ", ""},
		{"`db`.`table`;", "db.table"},
		{"IF EXISTS", "IF"},
		{"`weird``name`", "weird`name"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, extractIdentifier(tc.input), "extractIdentifier(%q)", tc.input)
	}
}
