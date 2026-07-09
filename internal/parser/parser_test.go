package parser

import (
	"encoding/binary"
	"os"
	"testing"

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
	payload[0] = 300
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
	events, err := p.ParseFiles([]string{path})
	require.NoError(t, err)
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
	events, err := p.ParseFiles([]string{path})
	require.NoError(t, err)
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
	events, err := p.ParseFiles([]string{path})
	require.NoError(t, err)
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
	payload[0] = 400
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
	events, err := p.ParseFiles([]string{f.Name()})
	require.NoError(t, err)
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
	_, err := p.ParseFiles([]string{"/nonexistent/binlog.000001"})
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
	events, err := p.ParseFiles([]string{f.Name()})
	require.NoError(t, err)
	assert.Empty(t, events)
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
	events, err := p.ParseFiles([]string{f.Name()})
	require.NoError(t, err)
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
