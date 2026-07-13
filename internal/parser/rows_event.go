package parser

import (
	"fmt"
	"io"
	"time"
)

// RowEventData is the intermediate parsed representation of a ROW event.
type RowEventData struct {
	TableID     uint64
	Flags       uint16
	ColumnCount uint64
	Rows        []RowData
}

// RowData holds the parsed row values from a ROW event.
// For INSERT: After is populated, Before is nil.
// For DELETE: Before is populated, After is nil.
// For UPDATE: both Before and After are populated.
type RowData struct {
	Before map[string]interface{} // nil for INSERT
	After  map[string]interface{} // nil for DELETE
}

// RowEventParser parses WriteRows/UpdateRows/DeleteRows events using a
// TableMapRegistry to resolve column metadata.
type RowEventParser struct {
	registry *TableMapRegistry
}

// NewRowEventParser creates a RowEventParser with the given registry.
func NewRowEventParser(registry *TableMapRegistry) *RowEventParser {
	return &RowEventParser{registry: registry}
}

// SetRegistry updates the registry reference.
func (p *RowEventParser) SetRegistry(r *TableMapRegistry) {
	p.registry = r
}

// ParseWriteRowsEvent parses a WriteRows event payload (V1 or V2).
func (p *RowEventParser) ParseWriteRowsEvent(payload []byte, v2 bool) (*RowEventData, error) {
	pos, tableID, flags, err := p.parseRowPostHeader(payload, v2)
	if err != nil {
		return nil, err
	}

	// Body: column count + columns-present bitmap + rows
	colCount, pos, err := LengthEncodedInt(payload, pos)
	if err != nil {
		return nil, fmt.Errorf("column count: %w", err)
	}

	bitmapBytes := BitmapByteSize(int(colCount))
	if pos+bitmapBytes > len(payload) {
		return nil, io.ErrUnexpectedEOF
	}
	colPresentBitmap := payload[pos : pos+bitmapBytes]
	pos += bitmapBytes

	result := &RowEventData{
		TableID:     tableID,
		Flags:       flags,
		ColumnCount: colCount,
	}

	// Parse rows (each row has "after" image only)
	for pos < len(payload) {
		row, newPos, err := p.parseRow(payload, pos, colCount, colPresentBitmap, false)
		if err != nil {
			return nil, fmt.Errorf("row at offset %d: %w", pos, err)
		}
		result.Rows = append(result.Rows, row)
		pos = newPos
	}

	return result, nil
}

// ParseDeleteRowsEvent parses a DeleteRows event payload (V1 or V2).
func (p *RowEventParser) ParseDeleteRowsEvent(payload []byte, v2 bool) (*RowEventData, error) {
	pos, tableID, flags, err := p.parseRowPostHeader(payload, v2)
	if err != nil {
		return nil, err
	}

	colCount, pos, err := LengthEncodedInt(payload, pos)
	if err != nil {
		return nil, fmt.Errorf("column count: %w", err)
	}

	bitmapBytes := BitmapByteSize(int(colCount))
	if pos+bitmapBytes > len(payload) {
		return nil, io.ErrUnexpectedEOF
	}
	colPresentBitmap := payload[pos : pos+bitmapBytes]
	pos += bitmapBytes

	result := &RowEventData{
		TableID:     tableID,
		Flags:       flags,
		ColumnCount: colCount,
	}

	// Parse rows (each row has "before" image only)
	for pos < len(payload) {
		row, newPos, err := p.parseRowAfter(payload, pos, colCount, colPresentBitmap)
		if err != nil {
			return nil, fmt.Errorf("row at offset %d: %w", pos, err)
		}
		result.Rows = append(result.Rows, row)
		pos = newPos
	}

	return result, nil
}

// ParseUpdateRowsEvent parses an UpdateRows event payload (V1 or V2).
func (p *RowEventParser) ParseUpdateRowsEvent(payload []byte, v2 bool) (*RowEventData, error) {
	pos, tableID, flags, err := p.parseRowPostHeader(payload, v2)
	if err != nil {
		return nil, err
	}

	colCount, pos, err := LengthEncodedInt(payload, pos)
	if err != nil {
		return nil, fmt.Errorf("column count: %w", err)
	}

	bitmapBytes := BitmapByteSize(int(colCount))
	// UPDATE has two columns-present bitmaps: before and after
	if pos+bitmapBytes*2 > len(payload) {
		return nil, io.ErrUnexpectedEOF
	}
	beforeBitmap := payload[pos : pos+bitmapBytes]
	pos += bitmapBytes
	afterBitmap := payload[pos : pos+bitmapBytes]
	pos += bitmapBytes

	result := &RowEventData{
		TableID:     tableID,
		Flags:       flags,
		ColumnCount: colCount,
	}

	// Parse rows (each row has before + after images)
	for pos < len(payload) {
		beforeRow, newPos, err := p.parseRowAfter(payload, pos, colCount, beforeBitmap)
		if err != nil {
			return nil, fmt.Errorf("before row at offset %d: %w", pos, err)
		}

		afterRow, newPos, err := p.parseRowAfter(payload, newPos, colCount, afterBitmap)
		if err != nil {
			return nil, fmt.Errorf("after row at offset %d: %w", newPos, err)
		}

		result.Rows = append(result.Rows, RowData{
			Before: beforeRow.After,
			After:  afterRow.After,
		})
		pos = newPos
	}

	return result, nil
}

// parseRowPostHeader parses the post-header section of a ROW event.
// Returns the new position, table ID, flags, and any error.
func (p *RowEventParser) parseRowPostHeader(payload []byte, v2 bool) (int, uint64, uint16, error) {
	pos := 0

	// Table ID: 6 bytes LE
	if pos+6 > len(payload) {
		return 0, 0, 0, io.ErrUnexpectedEOF
	}
	tableID := uint64(payload[pos]) |
		uint64(payload[pos+1])<<8 |
		uint64(payload[pos+2])<<16 |
		uint64(payload[pos+3])<<24 |
		uint64(payload[pos+4])<<32 |
		uint64(payload[pos+5])<<40
	pos += 6

	// Flags: 2 bytes
	if pos+2 > len(payload) {
		return 0, 0, 0, io.ErrUnexpectedEOF
	}
	flags := uint16(payload[pos]) | uint16(payload[pos+1])<<8
	pos += 2

	if v2 {
		// Extra data length: 2 bytes (includes itself)
		if pos+2 > len(payload) {
			return 0, 0, 0, io.ErrUnexpectedEOF
		}
		extraLen := int(payload[pos]) | int(payload[pos+1])<<8
		if extraLen < 2 {
			return 0, 0, 0, fmt.Errorf("invalid extra_data_length: %d", extraLen)
		}
		// Skip the extra data
		if pos+extraLen > len(payload) {
			return 0, 0, 0, io.ErrUnexpectedEOF
		}
		pos += extraLen
	}

	return pos, tableID, flags, nil
}

// parseRow reads a single row's "after" image from the binary, producing RowData
// where After is populated and Before is nil.
func (p *RowEventParser) parseRow(payload []byte, pos int, colCount uint64,
	colPresentBitmap []byte, isAfter bool) (RowData, int, error) {

	return p.parseRowAfter(payload, pos, colCount, colPresentBitmap)
}

// parseRowAfter reads a single row image (null bitmap + values) from the data.
func (p *RowEventParser) parseRowAfter(payload []byte, pos int, colCount uint64,
	colPresentBitmap []byte) (RowData, int, error) {

	bitmapBytes := BitmapByteSize(int(colCount))
	if pos+bitmapBytes > len(payload) {
		return RowData{}, pos, io.ErrUnexpectedEOF
	}
	nullBitmap := payload[pos : pos+bitmapBytes]
	pos += bitmapBytes

	// Lookup table map for column names and metadata
	tm := p.lookupTableMap(payload, pos)
	values := make(map[string]interface{})

	for i := 0; i < int(colCount); i++ {
		if !IsBitSet(colPresentBitmap, i) {
			continue // Column not included in this event
		}
		if IsBitSet(nullBitmap, i) {
			// Value is NULL
			key := colName(tm, i)
			values[key] = nil
			continue
		}

		// Read value based on column type
		var colType byte
		var colMeta ColumnMeta
		if tm != nil && i < len(tm.ColumnTypes) {
			colType = tm.ColumnTypes[i]
			colMeta = tm.ColumnMeta[i]
		}

		var err error
		values, pos, err = p.readColumnValue(payload, pos, colType, colMeta, values, i, tm)
		if err != nil {
			return RowData{}, pos, fmt.Errorf("column %d: %w", i, err)
		}
	}

	return RowData{After: values, Before: nil}, pos, nil
}

// lookupTableMap tries to find the table map for the current event.
// Since we don't have the table ID at this point in the body parsing,
// we rely on the caller to set it, or we extract it from the payload.
// This is a no-op stub; the actual table map lookup is done by the caller.
func (p *RowEventParser) lookupTableMap(payload []byte, pos int) *TableMap {
	_ = payload
	_ = pos
	return nil
}

// keyName returns the column name for position i.
func colName(tm *TableMap, i int) string {
	_ = tm
	return fmt.Sprintf("col_%d", i)
}

// readColumnValue reads a single column value from the binary data.
func (p *RowEventParser) readColumnValue(data []byte, pos int, colType byte,
	colMeta ColumnMeta, values map[string]interface{}, colIdx int, tm *TableMap) (map[string]interface{}, int, error) {

	key := colName(tm, colIdx)
	var val interface{}
	var err error

	switch colType {
	case MYSQL_TYPE_TINY:
		if pos+1 > len(data) {
			return values, pos, io.ErrUnexpectedEOF
		}
		val = int64(int8(data[pos]))
		pos++

	case MYSQL_TYPE_SHORT:
		val, pos, err = readInt64Value(data, pos, 2)

	case MYSQL_TYPE_LONG:
		val, pos, err = readInt64Value(data, pos, 4)

	case MYSQL_TYPE_LONGLONG:
		var v int64
		v, pos, err = readInt64(data, pos)
		val = v

	case MYSQL_TYPE_INT24:
		var v int32
		v, pos, err = readInt24(data, pos)
		val = int64(v)

	case MYSQL_TYPE_FLOAT:
		var v float32
		v, pos, err = readFloat32(data, pos)
		val = float64(v)

	case MYSQL_TYPE_DOUBLE:
		val, pos, err = readFloat64(data, pos)

	case MYSQL_TYPE_NULL:
		val = nil

	case MYSQL_TYPE_DECIMAL, MYSQL_TYPE_NEWDECIMAL:
		val, pos, err = p.parseDecimal(data, pos, colMeta)

	case MYSQL_TYPE_TIMESTAMP:
		val, pos, err = p.parseOldTimestamp(data, pos)

	case MYSQL_TYPE_TIMESTAMP2:
		val, pos, err = p.parseTimestamp2(data, pos, colMeta.FSP)

	case MYSQL_TYPE_DATETIME:
		val, pos, err = p.parseOldDatetime(data, pos)

	case MYSQL_TYPE_DATETIME2:
		val, pos, err = p.parseDatetime2(data, pos, colMeta.FSP)

	case MYSQL_TYPE_DATE, MYSQL_TYPE_NEWDATE:
		val, pos, err = p.parseDate(data, pos)

	case MYSQL_TYPE_TIME:
		val, pos, err = p.parseOldTime(data, pos)

	case MYSQL_TYPE_TIME2:
		val, pos, err = p.parseTime2(data, pos, colMeta.FSP)

	case MYSQL_TYPE_YEAR:
		if pos+1 > len(data) {
			return values, pos, io.ErrUnexpectedEOF
		}
		val = int64(1900 + int(data[pos]))
		pos++

	case MYSQL_TYPE_VARCHAR, MYSQL_TYPE_VAR_STRING:
		val, pos, err = p.parseVarchar(data, pos, colMeta)

	case MYSQL_TYPE_STRING:
		val, pos, err = p.parseString(data, pos, colMeta)

	case MYSQL_TYPE_BIT:
		val, pos, err = p.parseBit(data, pos, colMeta)

	case MYSQL_TYPE_ENUM:
		val, pos, err = p.parseEnum(data, pos, colMeta)

	case MYSQL_TYPE_SET:
		val, pos, err = p.parseSet(data, pos, colMeta)

	case MYSQL_TYPE_TINY_BLOB:
		val, pos, err = p.parseBlob(data, pos, 1)

	case MYSQL_TYPE_BLOB:
		val, pos, err = p.parseBlob(data, pos, 2)

	case MYSQL_TYPE_MEDIUM_BLOB:
		val, pos, err = p.parseBlob(data, pos, 3)

	case MYSQL_TYPE_LONG_BLOB, MYSQL_TYPE_JSON, MYSQL_TYPE_GEOMETRY:
		val, pos, err = p.parseBlob(data, pos, 4)

	default:
		// Unknown type: skip 4 bytes speculatively
		val = nil
		if pos+4 <= len(data) {
			pos += 4
		} else {
			pos = len(data)
		}
	}

	if err != nil {
		return values, pos, err
	}
	values[key] = val
	return values, pos, nil
}

// readInt64Value reads an n-byte LE signed integer and returns int64.
func readInt64Value(data []byte, pos int, n int) (int64, int, error) {
	if pos+n > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	var v uint64
	for i := 0; i < n; i++ {
		v |= uint64(data[pos+i]) << (8 * i)
	}
	// Sign extend
	if n < 8 && (v&(1<<(8*n-1))) != 0 {
		v |= ^uint64(0) << (8 * n)
	}
	return int64(v), pos + n, nil
}

// parseVarchar reads a VARCHAR / VAR_STRING value.
// Length prefix is 2 bytes LE.
func (p *RowEventParser) parseVarchar(data []byte, pos int, meta ColumnMeta) (string, int, error) {
	// Read 2-byte length
	if pos+2 > len(data) {
		return "", pos, io.ErrUnexpectedEOF
	}
	strLen := int(data[pos]) | int(data[pos+1])<<8
	pos += 2
	if pos+strLen > len(data) {
		return "", pos, io.ErrUnexpectedEOF
	}
	return string(data[pos : pos+strLen]), pos + strLen, nil
}

// parseString reads a STRING value. The metadata determines if it's a
// plain string, ENUM, or SET.
func (p *RowEventParser) parseString(data []byte, pos int, meta ColumnMeta) (interface{}, int, error) {
	if meta.RealType == MYSQL_TYPE_ENUM {
		return p.parseEnum(data, pos, meta)
	}
	if meta.RealType == MYSQL_TYPE_SET {
		return p.parseSet(data, pos, meta)
	}

	// Plain string: 1-byte length prefix
	if pos >= len(data) {
		return nil, pos, io.ErrUnexpectedEOF
	}
	strLen := int(data[pos])
	pos++
	if pos+strLen > len(data) {
		return nil, pos, io.ErrUnexpectedEOF
	}
	return string(data[pos : pos+strLen]), pos + strLen, nil
}

// parseBlob reads a BLOB value with the given length prefix size.
func (p *RowEventParser) parseBlob(data []byte, pos int, lenBytes int) ([]byte, int, error) {
	if pos+lenBytes > len(data) {
		return nil, pos, io.ErrUnexpectedEOF
	}
	var blobLen int
	for i := 0; i < lenBytes; i++ {
		blobLen |= int(data[pos+i]) << (8 * i)
	}
	pos += lenBytes
	if pos+blobLen > len(data) {
		return nil, pos, io.ErrUnexpectedEOF
	}
	result := make([]byte, blobLen)
	copy(result, data[pos:pos+blobLen])
	return result, pos + blobLen, nil
}

// parseBit reads a BIT value.
func (p *RowEventParser) parseBit(data []byte, pos int, meta ColumnMeta) (interface{}, int, error) {
	byteCount := meta.BitBytes
	if byteCount <= 0 {
		byteCount = 1
	}
	if pos+byteCount > len(data) {
		return nil, pos, io.ErrUnexpectedEOF
	}

	if byteCount <= 8 {
		var v uint64
		for i := 0; i < byteCount; i++ {
			v |= uint64(data[pos+i]) << (8 * i)
		}
		// Mask out unused bits in the last byte
		bitsInLast := meta.BitBits
		if bitsInLast > 0 && bitsInLast < 8 {
			mask := uint64((1 << bitsInLast) - 1)
			v &= mask
		}
		return int64(v), pos + byteCount, nil
	}

	// More than 8 bytes: return as raw bytes
	result := make([]byte, byteCount)
	copy(result, data[pos:pos+byteCount])
	return result, pos + byteCount, nil
}

// parseEnum reads an ENUM value (1 or 2 byte integer).
func (p *RowEventParser) parseEnum(data []byte, pos int, meta ColumnMeta) (int64, int, error) {
	size := meta.EnumSetSize
	if size <= 0 {
		size = 1
	}
	if pos+size > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	var v int64
	for i := 0; i < size; i++ {
		v |= int64(data[pos+i]) << (8 * i)
	}
	return v, pos + size, nil
}

// parseSet reads a SET value (variable bytes, stored as a bitmap).
func (p *RowEventParser) parseSet(data []byte, pos int, meta ColumnMeta) ([]byte, int, error) {
	size := meta.EnumSetSize
	if size <= 0 {
		size = 1
	}
	if pos+size > len(data) {
		return nil, pos, io.ErrUnexpectedEOF
	}
	result := make([]byte, size)
	copy(result, data[pos:pos+size])
	return result, pos + size, nil
}

// parseDate reads a DATE value (4 bytes LE, YYYYMMDD).
func (p *RowEventParser) parseDate(data []byte, pos int) (string, int, error) {
	if pos+4 > len(data) {
		return "", pos, io.ErrUnexpectedEOF
	}
	v := uint64(data[pos]) | uint64(data[pos+1])<<8 |
		uint64(data[pos+2])<<16 | uint64(data[pos+3])<<24
	if v == 0 {
		return "0000-00-00", pos + 4, nil
	}
	year := v / 10000
	month := (v / 100) % 100
	day := v % 100
	return fmt.Sprintf("%04d-%02d-%02d", year, month, day), pos + 4, nil
}

// parseOldTime reads a TIME value (3 bytes LE, HHMMSS).
func (p *RowEventParser) parseOldTime(data []byte, pos int) (string, int, error) {
	if pos+3 > len(data) {
		return "", pos, io.ErrUnexpectedEOF
	}
	v := uint64(data[pos]) | uint64(data[pos+1])<<8 | uint64(data[pos+2])<<16
	hour := v / 10000
	minute := (v / 100) % 100
	second := v % 100
	return fmt.Sprintf("%02d:%02d:%02d", hour, minute, second), pos + 3, nil
}

// parseOldTimestamp reads a TIMESTAMP value (4 bytes LE, seconds since epoch).
func (p *RowEventParser) parseOldTimestamp(data []byte, pos int) (string, int, error) {
	if pos+4 > len(data) {
		return "", pos, io.ErrUnexpectedEOF
	}
	secs := uint64(data[pos]) | uint64(data[pos+1])<<8 |
		uint64(data[pos+2])<<16 | uint64(data[pos+3])<<24
	if secs == 0 {
		return "0000-00-00 00:00:00", pos + 4, nil
	}
	t := time.Unix(int64(secs), 0).UTC()
	return t.Format("2006-01-02 15:04:05"), pos + 4, nil
}

// parseOldDatetime reads a DATETIME value (8 bytes LE, YYYYMMDDHHMMSS).
func (p *RowEventParser) parseOldDatetime(data []byte, pos int) (string, int, error) {
	if pos+8 > len(data) {
		return "", pos, io.ErrUnexpectedEOF
	}
	v := uint64(data[pos]) | uint64(data[pos+1])<<8 |
		uint64(data[pos+2])<<16 | uint64(data[pos+3])<<24 |
		uint64(data[pos+4])<<32 | uint64(data[pos+5])<<40 |
		uint64(data[pos+6])<<48 | uint64(data[pos+7])<<56
	if v == 0 {
		return "0000-00-00 00:00:00", pos + 8, nil
	}
	datePart := v / 1000000
	timePart := v % 1000000
	year := datePart / 10000
	month := (datePart / 100) % 100
	day := datePart % 100
	hour := timePart / 10000
	minute := (timePart / 100) % 100
	second := timePart % 100
	return fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d", year, month, day, hour, minute, second), pos + 8, nil
}

// parseTimestamp2 reads a TIMESTAMP2 value.
// Storage: 4 bytes LE (seconds) + fractional bytes.
func (p *RowEventParser) parseTimestamp2(data []byte, pos int, fsp int) (string, int, error) {
	if pos+4 > len(data) {
		return "", pos, io.ErrUnexpectedEOF
	}
	secs := uint64(data[pos]) | uint64(data[pos+1])<<8 |
		uint64(data[pos+2])<<16 | uint64(data[pos+3])<<24
	pos += 4

	fracNanos, pos, err := readFractionalPart(data, pos, fsp)
	if err != nil {
		return "", pos, err
	}

	if secs == 0 && fracNanos == 0 {
		return "0000-00-00 00:00:00", pos, nil
	}

	t := time.Unix(int64(secs), int64(fracNanos)).UTC()
	if fsp == 0 {
		return t.Format("2006-01-02 15:04:05"), pos, nil
	}
	return t.Format("2006-01-02 15:04:05." + fractionalFormat(fsp)), pos, nil
}

// parseDatetime2 reads a DATETIME2 value.
// Storage: 5 bytes BE (packed) + fractional bytes.
func (p *RowEventParser) parseDatetime2(data []byte, pos int, fsp int) (string, int, error) {
	if pos+5 > len(data) {
		return "", pos, io.ErrUnexpectedEOF
	}

	// Read 5 bytes big-endian
	intPart, pos, err := readUintBE(data, pos, 5)
	if err != nil {
		return "", pos, err
	}

	// Decode packed datetime
	// Bit 0: sign (not used for datetime, always positive)
	// Bits 1-17: year*13 + month
	// Bits 18-22: day   (5 bits)
	// Bits 23-27: hour  (5 bits)
	// Bits 28-33: minute (6 bits)
	// Bits 34-39: second (6 bits)
	sign := (intPart >> 40) & 0x1
	_ = sign
	ym := (intPart >> 22) & 0x1FFFF // 17 bits
	day := (intPart >> 17) & 0x1F  // 5 bits
	hour := (intPart >> 12) & 0x1F // 5 bits
	minute := (intPart >> 6) & 0x3F // 6 bits
	sec := intPart & 0x3F           // 6 bits

	year := ym / 13
	month := ym % 13

	if year == 0 && month == 0 && day == 0 && hour == 0 && minute == 0 && sec == 0 {
		// Read fractional part even for zero datetime, but return zero
		_, pos, err = readFractionalPart(data, pos, fsp)
		if err != nil {
			return "", pos, err
		}
		return "0000-00-00 00:00:00", pos, nil
	}

	// Read fractional part
	fracNanos, pos, err := readFractionalPart(data, pos, fsp)
	if err != nil {
		return "", pos, err
	}

	if fsp == 0 {
		return fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d",
			year, month, day, hour, minute, sec), pos, nil
	}

	fracStr := fmt.Sprintf("%09d", fracNanos)
	// Truncate to fsp digits
	return fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d.%s",
		year, month, day, hour, minute, sec, fracStr[:fsp]), pos, nil
}

// parseTime2 reads a TIME2 value.
// Storage: 3 bytes BE (packed) + fractional bytes.
func (p *RowEventParser) parseTime2(data []byte, pos int, fsp int) (string, int, error) {
	if pos+3 > len(data) {
		return "", pos, io.ErrUnexpectedEOF
	}

	intPart, pos, err := readUintBE(data, pos, 3)
	if err != nil {
		return "", pos, err
	}

	// Decode packed time
	// Bit 23: sign
	// Bits 12-22: hour (11 bits)
	// Bits 6-11: minute (6 bits)
	// Bits 0-5: second (6 bits)
	sign := (intPart >> 23) & 0x1
	hour := (intPart >> 12) & 0x7FF // 11 bits
	minute := (intPart >> 6) & 0x3F // 6 bits
	sec := intPart & 0x3F           // 6 bits

	if sign == 1 {
		// Negative: complement and negate
		intPart = (^intPart) & 0x7FFFFF
		hour = (intPart >> 12) & 0x7FF
		minute = (intPart >> 6) & 0x3F
		sec = intPart & 0x3F
	}

	if hour == 0 && minute == 0 && sec == 0 {
		_, pos, err = readFractionalPart(data, pos, fsp)
		if err != nil {
			return "", pos, err
		}
		return "00:00:00", pos, nil
	}

	fracNanos, pos, err := readFractionalPart(data, pos, fsp)
	if err != nil {
		return "", pos, err
	}

	if sign == 1 {
		// Negative time
		if fsp == 0 {
			return fmt.Sprintf("-%02d:%02d:%02d", hour, minute, sec), pos, nil
		}
		fracStr := fmt.Sprintf("%09d", fracNanos)
		return fmt.Sprintf("-%02d:%02d:%02d.%s", hour, minute, sec, fracStr[:fsp]), pos, nil
	}

	if fsp == 0 {
		return fmt.Sprintf("%02d:%02d:%02d", hour, minute, sec), pos, nil
	}
	fracStr := fmt.Sprintf("%09d", fracNanos)
	return fmt.Sprintf("%02d:%02d:%02d.%s", hour, minute, sec, fracStr[:fsp]), pos, nil
}

// readFractionalPart reads the fractional seconds part based on FSP.
// Returns the value in nanoseconds.
func readFractionalPart(data []byte, pos int, fsp int) (int64, int, error) {
	var fracLen int
	switch {
	case fsp >= 5:
		fracLen = 3
	case fsp >= 3:
		fracLen = 2
	case fsp >= 1:
		fracLen = 1
	default:
		return 0, pos, nil
	}

	if pos+fracLen > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}

	var fracValue int64
	for i := 0; i < fracLen; i++ {
		fracValue |= int64(data[pos+i]) << (8 * i)
	}

	// Convert to nanoseconds based on byte count
	var nanos int64
	switch fracLen {
	case 1:
		// 1 byte: stored value = micros / 10000 -> micros = value * 10000 -> nanos = value * 10000 * 1000
		nanos = fracValue * 10000000
	case 2:
		// 2 bytes: stored value = micros / 100 -> micros = value * 100 -> nanos = value * 100 * 1000
		nanos = fracValue * 100000
	case 3:
		// 3 bytes: stored value = micros -> nanos = value * 1000
		nanos = fracValue * 1000
	}

	return nanos, pos + fracLen, nil
}

// fractionalFormat returns a format string for the given FSP.
func fractionalFormat(fsp int) string {
	switch fsp {
	case 1:
		return "0"
	case 2:
		return "00"
	case 3:
		return "000"
	case 4:
		return "0000"
	case 5:
		return "00000"
	case 6:
		return "000000"
	default:
		return "000000"
	}
}

// parseDecimal reads a NEWDECIMAL binary value.
// The binary format stores the decimal as groups of 9 digits in 4-byte LE chunks.
func (p *RowEventParser) parseDecimal(data []byte, pos int, meta ColumnMeta) (string, int, error) {
	if meta.Precision <= 0 {
		meta.Precision = 10
	}
	if meta.Scale < 0 {
		meta.Scale = 0
	}

	integralLen := meta.Precision - meta.Scale
	fracLen := meta.Scale

	// Calculate binary size
	// Each 9 digits fit in 4 bytes, plus 1 byte for sign
	intDigitsPerGroup := 9
	fracDigitsPerGroup := 9
	groupBytes := 4

	intGroups := (integralLen + intDigitsPerGroup - 1) / intDigitsPerGroup
	fracGroups := (fracLen + fracDigitsPerGroup - 1) / fracDigitsPerGroup
	totalGroups := intGroups + fracGroups

	totalBytes := 1 + totalGroups*groupBytes // sign byte + groups
	if pos+totalBytes > len(data) {
		// Fallback: try reading what's available
		return p.parseDecimalSimple(data, pos, meta)
	}

	// Read sign byte
	signByte := data[pos]
	pos++
	isNegative := (signByte & 0x80) != 0

	// Read integral part groups (most significant first)
	intDigits := make([]int, 0, integralLen)
	for g := 0; g < intGroups; g++ {
		var groupVal uint32
		groupVal = uint32(data[pos]) | uint32(data[pos+1])<<8 |
			uint32(data[pos+2])<<16 | uint32(data[pos+3])<<24
		pos += 4

		if isNegative {
			groupVal = ^groupVal
		}

		// Convert group to digits
		digitsInGroup := intDigitsPerGroup
		if g == intGroups-1 {
			rem := integralLen % intDigitsPerGroup
			if rem > 0 {
				digitsInGroup = rem
			}
		}

		for d := digitsInGroup - 1; d >= 0; d-- {
			pow := 1
			for k := 0; k < d; k++ {
				pow *= 10
			}
			digit := int(groupVal / uint32(pow) % 10)
			intDigits = append(intDigits, digit)
		}
	}

	// Read fractional part groups
	fracDigits := make([]int, 0, fracLen)
	for g := 0; g < fracGroups; g++ {
		var groupVal uint32
		groupVal = uint32(data[pos]) | uint32(data[pos+1])<<8 |
			uint32(data[pos+2])<<16 | uint32(data[pos+3])<<24
		pos += 4

		if isNegative {
			groupVal = ^groupVal
		}

		digitsInGroup := fracDigitsPerGroup
		if g == fracGroups-1 {
			rem := fracLen % fracDigitsPerGroup
			if rem > 0 {
				digitsInGroup = rem
			}
		}

		// Extract digits (most significant first)
		for d := digitsInGroup - 1; d >= 0; d-- {
			pow := 1
			for k := 0; k < d; k++ {
				pow *= 10
			}
			digit := int(groupVal / uint32(pow) % 10)
			fracDigits = append(fracDigits, digit)
		}
	}

	// Build string
	result := ""
	if isNegative {
		result = "-"
	}

	// Integral part
	if len(intDigits) == 0 {
		result += "0"
	} else {
		for _, d := range intDigits {
			result += fmt.Sprintf("%d", d)
		}
	}

	// Fractional part
	if len(fracDigits) > 0 {
		result += "."
		for _, d := range fracDigits {
			result += fmt.Sprintf("%d", d)
		}
	}

	return result, pos, nil
}

// parseDecimalSimple is a fallback decimal parser for truncated data.
func (p *RowEventParser) parseDecimalSimple(data []byte, pos int, meta ColumnMeta) (string, int, error) {
	// Just return a placeholder
	return fmt.Sprintf("<decimal(%d,%d)>", meta.Precision, meta.Scale), pos, nil
}
