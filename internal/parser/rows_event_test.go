package parser

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildRowBody creates the body portion of a WriteRows/DeleteRows event
// (after the post-header), including column count, columns-present bitmap, and rows.
func buildRowBody(colCount int, colPresent []byte, rows ...[]byte) []byte {
	var body []byte
	// Column count (packed int)
	body = append(body, byte(colCount))
	// Columns-present bitmap
	body = append(body, colPresent...)
	// Row data
	for _, row := range rows {
		body = append(body, row...)
	}
	return body
}

// buildNullBitmap creates a null bitmap with the specified null columns set.
func buildNullBitmap(colCount int, nullCols ...int) []byte {
	bytesLen := (colCount + 7) / 8
	bmp := make([]byte, bytesLen)
	for _, col := range nullCols {
		byteIdx := col / 8
		bitIdx := col % 8
		bmp[byteIdx] |= 1 << bitIdx
	}
	return bmp
}

// tests for column value parsing
func TestParseValue_TINY(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// Positive 42
	vals, pos, err := parser.readColumnValue([]byte{42}, 0, MYSQL_TYPE_TINY, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(42), vals["col_0"])
	assert.Equal(t, 1, pos)

	// Negative -1
	vals, pos, err = parser.readColumnValue([]byte{0xFF}, 0, MYSQL_TYPE_TINY, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(-1), vals["col_0"])
}

func TestParseValue_SHORT(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// 0x1234 = 4660
	data := []byte{0x34, 0x12}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_SHORT, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(4660), vals["col_0"])
	assert.Equal(t, 2, pos)

	// Negative
	data = []byte{0x00, 0x80} // -32768
	vals, _, err = parser.readColumnValue(data, 0, MYSQL_TYPE_SHORT, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(-32768), vals["col_0"])
}

func TestParseValue_LONG(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	data := []byte{0xEF, 0xBE, 0xAD, 0xDE} // 0xDEADBEEF as signed = -559038737
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_LONG, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(-559038737), vals["col_0"])
	assert.Equal(t, 4, pos)
}

func TestParseValue_LONGLONG(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_LONGLONG, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0x0807060504030201), vals["col_0"])
	assert.Equal(t, 8, pos)
}

func TestParseValue_INT24(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// 0x123456 = 1193046
	data := []byte{0x56, 0x34, 0x12}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_INT24, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0x123456), vals["col_0"])
	assert.Equal(t, 3, pos)
}

func TestParseValue_FLOAT(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// 3.14
	data := []byte{0xC3, 0xF5, 0x48, 0x40}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_FLOAT, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.InDelta(t, 3.14, vals["col_0"].(float64), 0.001)
	assert.Equal(t, 4, pos)
}

func TestParseValue_DOUBLE(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	data := []byte{0x18, 0x2D, 0x44, 0x54, 0xFB, 0x21, 0x09, 0x40}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_DOUBLE, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.InDelta(t, 3.141592653589793, vals["col_0"].(float64), 1e-15)
	assert.Equal(t, 8, pos)
}

func TestParseValue_NULL(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	vals, pos, err := parser.readColumnValue(nil, 0, MYSQL_TYPE_NULL, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Nil(t, vals["col_0"])
	assert.Equal(t, 0, pos)
}

func TestParseValue_VARCHAR(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	data := []byte{0x05, 0x00, 0x48, 0x65, 0x6C, 0x6C, 0x6F} // len=5, "Hello"
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_VARCHAR, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, "Hello", vals["col_0"])
	assert.Equal(t, 7, pos)
}

func TestParseValue_VARCHAR_Over255(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// VARCHAR(300): 2-byte length = 0x012C = 300
	str := make([]byte, 300)
	for i := range str {
		str[i] = 'A'
	}
	data := []byte{0x2C, 0x01}
	data = append(data, str...)
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_VARCHAR, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, string(str), vals["col_0"])
	assert.Equal(t, 302, pos)
}

func TestParseValue_STRING(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// STRING with 1-byte length prefix
	data := []byte{0x05, 0x48, 0x65, 0x6C, 0x6C, 0x6F}
	meta := ColumnMeta{Type: MYSQL_TYPE_STRING, RealType: MYSQL_TYPE_STRING}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_STRING, meta, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, "Hello", vals["col_0"])
	assert.Equal(t, 6, pos)
}

func TestParseValue_ENUM(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// ENUM value = 2 (second enum member), stored as 1 byte
	meta := ColumnMeta{Type: MYSQL_TYPE_ENUM, EnumSetSize: 1}
	data := []byte{0x02}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_ENUM, meta, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(2), vals["col_0"])
	assert.Equal(t, 1, pos)
}

func TestParseValue_BIT(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// BIT(8): 1 byte
	meta := ColumnMeta{Type: MYSQL_TYPE_BIT, BitBytes: 1, BitBits: 8}
	data := []byte{0xAB}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_BIT, meta, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0xAB), vals["col_0"])
	assert.Equal(t, 1, pos)

	// BIT(5): only 5 bits, mask applied
	meta = ColumnMeta{Type: MYSQL_TYPE_BIT, BitBytes: 1, BitBits: 5}
	data = []byte{0xFF}
	vals, _, err = parser.readColumnValue(data, 0, MYSQL_TYPE_BIT, meta, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0x1F), vals["col_0"]) // only 5 bits: 0b11111 = 31
}

func TestParseValue_TINYTEXT_BLOB(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// TINY_BLOB: 1-byte length + data
	data := []byte{0x05, 0x48, 0x65, 0x6C, 0x6C, 0x6F}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_TINY_BLOB, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("Hello"), vals["col_0"])
	assert.Equal(t, 6, pos)
}

func TestParseValue_BLOB(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// BLOB: 2-byte length + data
	data := []byte{0x05, 0x00, 0x48, 0x65, 0x6C, 0x6C, 0x6F}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_BLOB, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, []byte("Hello"), vals["col_0"])
	assert.Equal(t, 7, pos)
}

func TestParseValue_YEAR(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// Year 2024 = 1900 + 124
	data := []byte{0x7C} // 124
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_YEAR, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, int64(2024), vals["col_0"])
	assert.Equal(t, 1, pos)
}

func TestParseValue_DATE(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// DATE 2024-01-15 stored as YYYYMMDD = 20240115
	data := []byte{0x4F, 0xAC, 0x34, 0x01} // 20240115 LE
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_DATE, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, "2024-01-15", vals["col_0"])
	assert.Equal(t, 4, pos)

	// Zero date
	data = []byte{0x00, 0x00, 0x00, 0x00}
	vals, pos, err = parser.readColumnValue(data, 0, MYSQL_TYPE_DATE, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, "0000-00-00", vals["col_0"])
}

func TestParseValue_TIME(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// TIME 14:30:25 stored as HHMMSS = 143025
	data := []byte{0xF1, 0x2E, 0x02} // 143025 LE... wait
	// 143025 = 0x22EB1. In LE: 0xB1, 0x2E, 0x02
	data = []byte{0xB1, 0x2E, 0x02}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_TIME, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, "14:30:25", vals["col_0"])
	assert.Equal(t, 3, pos)
}

func TestParseValue_TIMESTAMP(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// Unix timestamp 1705312800 = 2024-01-15 10:00:00 UTC
	// 1705312800 = 0x65A5BBA0
	data := []byte{0xA0, 0xBB, 0xA5, 0x65}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_TIMESTAMP, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, "2024-01-15 10:00:00", vals["col_0"])
	assert.Equal(t, 4, pos)
}

func TestParseValue_DATETIME(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// DATETIME 2024-01-15 14:30:25 = YYYYMMDDHHMMSS = 20240115143025
	// In hex: 0x000012669F2DF1... let me compute: 20240115143025 = 0x12669F2DF1
	// But this fits in less than 8 bytes. Let me compute:
	// 20240115143025 = 20,240,115,143,025
	// 0x12669F2E9F1 (this is only like 5 bytes...)
	// Wait: 20240115143025 decimal
	// 20240115143025 / 256 = 79062949777 R 113 (0x71)
	// Hmm, this is getting complex. Let me just encode it.
	data := make([]byte, 8)
	// 20240115143025 in LE bytes
	v := uint64(20240115143025)
	for i := 0; i < 8; i++ {
		data[i] = byte(v >> (8 * i))
	}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_DATETIME, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, "2024-01-15 14:30:25", vals["col_0"])
	assert.Equal(t, 8, pos)

	// Zero datetime
	data = make([]byte, 8)
	vals, _, err = parser.readColumnValue(data, 0, MYSQL_TYPE_DATETIME, ColumnMeta{}, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, "0000-00-00 00:00:00", vals["col_0"])
}

func TestParseValue_TIMESTAMP2(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// TIMESTAMP(6) 2024-01-15 10:00:00.500000
	// secs = 1705312800 = 0x65A5BBA0
	// frac = 500000 microseconds -> stored = 500000 -> 0x0007A120
	secs := uint64(time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC).Unix())
	// 1705312800 = 0x65A5BBA0

	data := make([]byte, 4+3)
	data[0] = byte(secs)
	data[1] = byte(secs >> 8)
	data[2] = byte(secs >> 16)
	data[3] = byte(secs >> 24)
	// micros = 500000
	micros := uint64(500000)
	data[4] = byte(micros)
	data[5] = byte(micros >> 8)
	data[6] = byte(micros >> 16)

	meta := ColumnMeta{FSP: 6}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_TIMESTAMP2, meta, nil, 0, nil)
	require.NoError(t, err)
	// The timestamp should be formatted with fractional part
	ts := vals["col_0"].(string)
	assert.Contains(t, ts, "2024-01-15 10:00:00.")
	assert.Equal(t, 7, pos)
}

func TestParseValue_DATETIME2(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// DATETIME2 2024-01-15 14:30:25 (fsp=0)
	// Packed: (year*13+month) << 22 | day << 17 | hour << 12 | minute << 6 | second
	ym := uint64(2024*13 + 1) // 26313
	day := uint64(15)
	hour := uint64(14)
	minute := uint64(30)
	sec := uint64(25)
	packed := ym<<22 | day<<17 | hour<<12 | minute<<6 | sec

	data := make([]byte, 5)
	data[0] = byte(packed >> 32)
	data[1] = byte(packed >> 24)
	data[2] = byte(packed >> 16)
	data[3] = byte(packed >> 8)
	data[4] = byte(packed)

	meta := ColumnMeta{FSP: 0}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_DATETIME2, meta, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, "2024-01-15 14:30:25", vals["col_0"])
	assert.Equal(t, 5, pos)
}

func TestParseValue_DATETIME2_WithFSP(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	ym := uint64(2024*13 + 1)
	packed := ym<<22 | uint64(15)<<17 | uint64(14)<<12 | uint64(30)<<6 | uint64(25)

	data := make([]byte, 5+2) // 5 bytes packed + 2 bytes fractional
	data[0] = byte(packed >> 32)
	data[1] = byte(packed >> 24)
	data[2] = byte(packed >> 16)
	data[3] = byte(packed >> 8)
	data[4] = byte(packed)
	// Fractional: 500000 microseconds -> stored = 500000/100 = 5000 = 0x1388
	data[5] = 0x88
	data[6] = 0x13

	meta := ColumnMeta{FSP: 3}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_DATETIME2, meta, nil, 0, nil)
	require.NoError(t, err)
	assert.Contains(t, vals["col_0"].(string), "2024-01-15 14:30:25.")
	assert.Equal(t, 7, pos)
}

func TestParseValue_TIME2(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// TIME2 14:30:25 (fsp=0)
	hour := uint64(14)
	minute := uint64(30)
	sec := uint64(25)
	packed := hour<<12 | minute<<6 | sec

	data := make([]byte, 3)
	data[0] = byte(packed >> 16)
	data[1] = byte(packed >> 8)
	data[2] = byte(packed)

	meta := ColumnMeta{FSP: 0}
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_TIME2, meta, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, "14:30:25", vals["col_0"])
	assert.Equal(t, 3, pos)
}

func TestParseValue_SET(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// SET value stored as bitmap (1 byte)
	meta := ColumnMeta{Type: MYSQL_TYPE_SET, EnumSetSize: 1}
	data := []byte{0x05} // 0b101 = members 0 and 2
	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_SET, meta, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, []byte{0x05}, vals["col_0"].([]byte))
	assert.Equal(t, 1, pos)
}

func TestParseValue_NEWDECIMAL(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	// We'll test the decimal parser with a simple case.
	// DECIMAL(5,2) value 123.45
	// Binary format: 1 sign byte + groups of 9 digits in 4 bytes
	// For precision=5 (3 integral + 2 fractional), integral has 1 group (9 digit slots),
	// fractional has 1 group (9 digit slots).
	// But only 3 integral digits matter. For 123.45:
	// integral group value: 123
	// fractional group value: 45
	meta := ColumnMeta{Precision: 5, Scale: 2}

	// Sign byte (positive = 0)
	// Since the decimal is represented with base-10^9 groups, and our number fits
	// in one group, the group value is:
	// For positive: stored value = actual digits
	// integral group: 123
	// We need to compute: each byte is 0-255, 4 bytes LE = 32-bit value = 123
	// Then fractional group: 45
	// But actual format is different...

	// The binary format stores the number as groups of 9 digits in 4 bytes LE.
	// The sign byte is at the start.
	// Positive sign byte = 0x00
	data := []byte{
		0x00,       // sign (positive)
		0x7B, 0x00, 0x00, 0x00, // integral part: 123 (LE uint32)
		0x2D, 0x00, 0x00, 0x00, // fractional part: 45 (LE uint32)
	}

	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_NEWDECIMAL, meta, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, "123.45", vals["col_0"])
	assert.Equal(t, 9, pos)
}

func TestParseValue_NEWDECIMAL_Negative(t *testing.T) {
	parser := NewRowEventParser(NewTableMapRegistry())
	meta := ColumnMeta{Precision: 5, Scale: 2}

	// Negative: sign byte = 0x80, values are complemented
	data := []byte{
		0x80,                               // sign (negative)
		0x84, 0xFF, 0xFF, 0xFF,             // complement of 123
		0xD2, 0xFF, 0xFF, 0xFF,             // complement of 45 (0x2D)
	}

	vals, pos, err := parser.readColumnValue(data, 0, MYSQL_TYPE_NEWDECIMAL, meta, nil, 0, nil)
	require.NoError(t, err)
	assert.Equal(t, "-123.45", vals["col_0"])
	assert.Equal(t, 9, pos)
}

func TestParseRows_WriteRowsV1_Basic(t *testing.T) {
	// Create a single row with 2 TINYINT columns, both present, neither null
	colCount := 2
	colPresent := buildNullBitmap(colCount, 0, 1) // all 1s = all present
	nullBitmap := buildNullBitmap(colCount)        // all 0s = none null
	rowData := append(nullBitmap, []byte{42, 100}...)

	body := buildRowBody(colCount, colPresent, rowData)

	// Build full event payload: 8-byte post-header + body
	payload := make([]byte, 8)
	payload[0] = 42       // table ID low byte
	payload[6] = 0x01     // flags
	payload = append(payload, body...)

	parser := NewRowEventParser(NewTableMapRegistry())
	result, err := parser.ParseWriteRowsEvent(payload, false)
	require.NoError(t, err)
	assert.Equal(t, uint64(42), result.TableID)
	assert.Equal(t, uint64(2), result.ColumnCount)
	assert.Len(t, result.Rows, 1)
	assert.Nil(t, result.Rows[0].Before)
	assert.NotNil(t, result.Rows[0].After)
}

func TestParseRows_UpdateRowsV1(t *testing.T) {
	// Update event with 2 INT columns, all present, both images
	colCount := 2
	colCountByte := []byte{byte(colCount)}

	// Body: col count + 2 bitmaps (before, after) + rows (before null + values, after null + values)
	// But wait, for UPDATE, there are two columns-present bitmaps.
	// Let me just build the payload manually.

	// Post-header: 8 bytes
	payload := make([]byte, 8)
	payload[0] = 1
	payload[6] = 0

	// Body
	// Column count (packed int)
	payload = append(payload, colCountByte...)
	// Before bitmap (all ones)
	payload = append(payload, []byte{0x03}...)
	// After bitmap (all ones)
	payload = append(payload, []byte{0x03}...)
	// Before row: null bitmap (none null) + values
	beforeNull := buildNullBitmap(colCount)
	payload = append(payload, beforeNull...)
	bv := make([]byte, 8) // 2 INT values: 100, 200
	binaryPutUint32(bv[0:4], 100)
	binaryPutUint32(bv[4:8], 200)
	payload = append(payload, bv...)
	// After row: null bitmap (none null) + values
	afterNull := buildNullBitmap(colCount)
	payload = append(payload, afterNull...)
	av := make([]byte, 8) // 2 INT values: 101, 201
	binaryPutUint32(av[0:4], 101)
	binaryPutUint32(av[4:8], 201)
	payload = append(payload, av...)

	parser := NewRowEventParser(NewTableMapRegistry())
	result, err := parser.ParseUpdateRowsEvent(payload, false)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), result.TableID)
	assert.Len(t, result.Rows, 1)
	assert.NotNil(t, result.Rows[0].Before)
	assert.NotNil(t, result.Rows[0].After)
}

func TestParseRows_DeleteRowsV1(t *testing.T) {
	// Delete event with 1 INT column
	colCount := 1
	colPresent := buildNullBitmap(colCount, 0)

	payload := make([]byte, 8)
	payload[0] = 1
	payload[6] = 0

	// Column count
	payload = append(payload, byte(colCount))
	// Columns-present bitmap
	payload = append(payload, colPresent...)
	// Null bitmap
	payload = append(payload, buildNullBitmap(colCount)...)
	// Value: 42 as INT LE
	payload = append(payload, 0x2A, 0x00, 0x00, 0x00)

	parser := NewRowEventParser(NewTableMapRegistry())
	result, err := parser.ParseDeleteRowsEvent(payload, false)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), result.TableID)
	assert.Len(t, result.Rows, 1)
	assert.NotNil(t, result.Rows[0].Before)
	assert.Nil(t, result.Rows[0].After)
}

func TestParseRows_WriteRowsV2_ExtraData(t *testing.T) {
	// V2 with extra data: 10-byte post-header (6 table_id + 2 flags + 2 extra_data_len)
	colCount := 1
	colPresent := buildNullBitmap(colCount, 0)
	nullBitmap := buildNullBitmap(colCount)

	// Post-header: 10 bytes
	payload := make([]byte, 10)
	payload[0] = 1                                     // table ID
	payload[6] = 0                                     // flags
	payload[7] = 0
	payload[8] = 2                                     // extra_data_length = 2 (minimum, no data)
	payload[9] = 0

	// Body
	payload = append(payload, byte(colCount))
	payload = append(payload, colPresent...)
	payload = append(payload, nullBitmap...)
	payload = append(payload, 0x2A, 0x00, 0x00, 0x00) // INT value 42

	parser := NewRowEventParser(NewTableMapRegistry())
	result, err := parser.ParseWriteRowsEvent(payload, true)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), result.TableID)
	assert.Len(t, result.Rows, 1)
}

func TestParseRows_NullColumn(t *testing.T) {
	// 2 TINYINT columns, second is NULL
	colCount := 2
	colPresent := buildNullBitmap(colCount, 0, 1) // both present
	nullBitmap := buildNullBitmap(colCount, 1)     // column 1 is null

	payload := make([]byte, 8)
	payload[0] = 1
	payload[6] = 0

	payload = append(payload, byte(colCount))
	payload = append(payload, colPresent...)
	payload = append(payload, nullBitmap...)
	payload = append(payload, 42) // col_0 = 42 (col_1 is null, no data)
	// Wait, when a column is null, no data is written for it. So only col_0 data is present.

	parser := NewRowEventParser(NewTableMapRegistry())
	result, err := parser.ParseWriteRowsEvent(payload, false)
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
	// With positional keys and no table map, we can't get the exact column names
	_, hasCol0 := result.Rows[0].After["col_0"]
	assert.True(t, hasCol0)
}

func TestParseRows_ColumnNotPresent(t *testing.T) {
	// 3 columns, only column 0 and 2 are present in this event
	colCount := 3
	colPresent := buildNullBitmap(3, 0, 2) // bits 0 and 2 set
	nullBitmap := buildNullBitmap(3)        // none null

	// Register a table map so column types are resolved.
	reg := NewTableMapRegistry()
	reg.Set(&TableMap{
		TableID:     1,
		ColumnCount: 3,
		ColumnTypes: []byte{MYSQL_TYPE_TINY, MYSQL_TYPE_TINY, MYSQL_TYPE_TINY},
		ColumnMeta:  []ColumnMeta{{Type: MYSQL_TYPE_TINY}, {Type: MYSQL_TYPE_TINY}, {Type: MYSQL_TYPE_TINY}},
	})

	payload := make([]byte, 8)
	payload[0] = 1
	payload[6] = 0

	payload = append(payload, byte(colCount))
	payload = append(payload, colPresent...)
	payload = append(payload, nullBitmap...)
	// Only values for present columns: 42 for col_0, 99 for col_2
	payload = append(payload, 42, 99)

	parser := NewRowEventParser(reg)
	result, err := parser.ParseWriteRowsEvent(payload, false)
	require.NoError(t, err)
	require.Len(t, result.Rows, 1)
}

func TestReadFractionalPart(t *testing.T) {
	// FSP=6: 3 bytes, value = micros
	data := []byte{0x40, 0x42, 0x0F} // 1000000 microseconds = 1 second (nanos = 1000000000)
	// Actually: 0x0F4240 = 1000000. nanos = 1000000 * 1000 = 1000000000
	nanos, pos, err := readFractionalPart(data, 0, 6)
	require.NoError(t, err)
	assert.Equal(t, int64(1000000000), nanos)

	// FSP=3: 2 bytes, stored = micros/100
	// micros = 500000, stored = 5000 = 0x1388
	nanos, pos, err = readFractionalPart([]byte{0x88, 0x13}, 0, 3)
	require.NoError(t, err)
	// nanos = 5000 * 100000 = 500000000
	assert.Equal(t, int64(500000000), nanos)

	// FSP=1: 1 byte, stored = micros/10000
	// micros = 500000, stored = 50 = 0x32
	nanos, pos, err = readFractionalPart([]byte{0x32}, 0, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(500000000), nanos)

	// FSP=0: no bytes
	nanos, pos, err = readFractionalPart([]byte{}, 0, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(0), nanos)
	assert.Equal(t, 0, pos)

	// Short buffer
	_, _, err = readFractionalPart([]byte{0x01}, 0, 3)
	assert.Error(t, err)
}

func TestReadInt64Value(t *testing.T) {
	v, _, err := readInt64Value([]byte{0xFF}, 0, 1)
	require.NoError(t, err)
	assert.Equal(t, int64(-1), v)

	v, _, err = readInt64Value([]byte{0x00, 0x80}, 0, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(-32768), v)

	v, _, err = readInt64Value([]byte{0x01, 0x02}, 0, 2)
	require.NoError(t, err)
	assert.Equal(t, int64(0x0201), v)

	// Short buffer
	_, _, err = readInt64Value([]byte{0x01}, 0, 2)
	assert.Error(t, err)
}
