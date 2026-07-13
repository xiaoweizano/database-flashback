package parser

import (
	"fmt"
	"io"
)

// MySQL column type constants used in binlog TableMapEvent.
const (
	MYSQL_TYPE_DECIMAL     = 0
	MYSQL_TYPE_TINY        = 1
	MYSQL_TYPE_SHORT       = 2
	MYSQL_TYPE_LONG        = 3
	MYSQL_TYPE_FLOAT       = 4
	MYSQL_TYPE_DOUBLE      = 5
	MYSQL_TYPE_NULL        = 6
	MYSQL_TYPE_TIMESTAMP   = 7
	MYSQL_TYPE_LONGLONG    = 8
	MYSQL_TYPE_INT24       = 9
	MYSQL_TYPE_DATE        = 10
	MYSQL_TYPE_TIME        = 11
	MYSQL_TYPE_DATETIME    = 12
	MYSQL_TYPE_YEAR        = 13
	MYSQL_TYPE_NEWDATE     = 14
	MYSQL_TYPE_VARCHAR     = 15
	MYSQL_TYPE_BIT         = 16
	MYSQL_TYPE_TIMESTAMP2  = 17
	MYSQL_TYPE_DATETIME2   = 18
	MYSQL_TYPE_TIME2       = 19
	MYSQL_TYPE_JSON        = 245
	MYSQL_TYPE_NEWDECIMAL  = 246
	MYSQL_TYPE_ENUM        = 247
	MYSQL_TYPE_SET         = 248
	MYSQL_TYPE_TINY_BLOB   = 249
	MYSQL_TYPE_MEDIUM_BLOB = 250
	MYSQL_TYPE_LONG_BLOB   = 251
	MYSQL_TYPE_BLOB        = 252
	MYSQL_TYPE_VAR_STRING  = 253
	MYSQL_TYPE_STRING      = 254
	MYSQL_TYPE_GEOMETRY    = 255
)

// ColumnMeta stores parsed metadata for a single column from a TableMapEvent.
type ColumnMeta struct {
	Type      byte
	Precision int  // for DECIMAL / NEWDECIMAL
	Scale     int  // for DECIMAL / NEWDECIMAL
	Length    int  // for string types: max length / display size
	Signed    bool // for integer types (estimated)
	// FSP is the fractional seconds precision for temporal types (0-6).
	FSP int
	// Bits for BIT type.
	BitBits     int
	BitBytes    int
	// RealType for STRING type (may be ENUM or SET).
	RealType    byte
	// Enum/Set size for ENUM/SET types (1 or 2 bytes).
	EnumSetSize int
}

// TableMap stores parsed metadata from a TABLE_MAP_EVENT.
type TableMap struct {
	TableID     uint64
	Database    string
	Table       string
	ColumnCount uint64
	ColumnTypes []byte
	ColumnMeta  []ColumnMeta
}

// Clone returns a deep copy of the TableMap.
func (tm *TableMap) Clone() *TableMap {
	if tm == nil {
		return nil
	}
	clone := &TableMap{
		TableID:     tm.TableID,
		Database:    tm.Database,
		Table:       tm.Table,
		ColumnCount: tm.ColumnCount,
		ColumnTypes: make([]byte, len(tm.ColumnTypes)),
		ColumnMeta:  make([]ColumnMeta, len(tm.ColumnMeta)),
	}
	copy(clone.ColumnTypes, tm.ColumnTypes)
	copy(clone.ColumnMeta, tm.ColumnMeta)
	return clone
}

// TableMapRegistry stores TableMap entries indexed by table ID.
type TableMapRegistry struct {
	maps map[uint64]*TableMap
}

// NewTableMapRegistry creates an empty registry.
func NewTableMapRegistry() *TableMapRegistry {
	return &TableMapRegistry{
		maps: make(map[uint64]*TableMap),
	}
}

// Set stores a TableMap for the given table ID.
func (r *TableMapRegistry) Set(tm *TableMap) {
	r.maps[tm.TableID] = tm
}

// Get retrieves a TableMap by table ID. Returns nil if not found.
func (r *TableMapRegistry) Get(tableID uint64) *TableMap {
	return r.maps[tableID]
}

// Delete removes a TableMap entry.
func (r *TableMapRegistry) Delete(tableID uint64) {
	delete(r.maps, tableID)
}

// ParseTableMap parses a TABLE_MAP_EVENT from its payload (after the 19-byte
// common header and excluding any trailing CRC32).
//
// The payload format:
//   Post-header (8 bytes):
//     6 bytes: table ID
//     2 bytes: flags
//   Body:
//     Database name: 1-byte length + N bytes + null terminator
//     Table name: 1-byte length + N bytes + null terminator
//     Column count: packed integer
//     Column types: column_count bytes
//     Column metadata: packed integer length + metadata bytes
//     Null bitmap: (column_count + 7) / 8 bytes
func ParseTableMap(payload []byte) (*TableMap, error) {
	pos := 0

	// Post-header: table ID (6 bytes LE)
	if pos+8 > len(payload) {
		return nil, io.ErrUnexpectedEOF
	}
	tableID := uint64(payload[pos]) |
		uint64(payload[pos+1])<<8 |
		uint64(payload[pos+2])<<16 |
		uint64(payload[pos+3])<<24 |
		uint64(payload[pos+4])<<32 |
		uint64(payload[pos+5])<<40
	pos += 6

	// Flags (2 bytes)
	_ = uint16(payload[pos]) | uint16(payload[pos+1])<<8
	pos += 2

	// Database name: 1-byte length + string + null terminator
	if pos >= len(payload) {
		return nil, io.ErrUnexpectedEOF
	}
	dbLen := int(payload[pos])
	pos++
	if pos+dbLen+1 > len(payload) {
		return nil, io.ErrUnexpectedEOF
	}
	database := string(payload[pos : pos+dbLen])
	pos += dbLen + 1 // skip null terminator

	// Table name: 1-byte length + string + null terminator
	if pos >= len(payload) {
		return nil, io.ErrUnexpectedEOF
	}
	tblLen := int(payload[pos])
	pos++
	if pos+tblLen+1 > len(payload) {
		return nil, io.ErrUnexpectedEOF
	}
	table := string(payload[pos : pos+tblLen])
	pos += tblLen + 1

	// Column count (packed integer)
	colCount, pos, err := LengthEncodedInt(payload, pos)
	if err != nil {
		return nil, fmt.Errorf("column count: %w", err)
	}

	// Column types: colCount bytes
	if pos+int(colCount) > len(payload) {
		return nil, io.ErrUnexpectedEOF
	}
	colTypes := make([]byte, colCount)
	copy(colTypes, payload[pos:pos+int(colCount)])
	pos += int(colCount)

	// Column metadata: packed length + metadata bytes
	metaLength, pos, err := LengthEncodedInt(payload, pos)
	if err != nil {
		return nil, fmt.Errorf("column metadata length: %w", err)
	}
	if pos+int(metaLength) > len(payload) {
		return nil, io.ErrUnexpectedEOF
	}
	metaBytes := payload[pos : pos+int(metaLength)]
	pos += int(metaLength)

	// Parse column metadata
	colMeta := make([]ColumnMeta, colCount)
	metaPos := 0
	for i := uint64(0); i < colCount; i++ {
		colType := colTypes[i]
		meta := ColumnMeta{Type: colType}
		metaPos = parseColumnMetadata(metaBytes, metaPos, colType, &meta)
		colMeta[i] = meta
	}

	tm := &TableMap{
		TableID:     tableID,
		Database:    database,
		Table:       table,
		ColumnCount: colCount,
		ColumnTypes: colTypes,
		ColumnMeta:  colMeta,
	}
	return tm, nil
}

// parseColumnMetadata reads the metadata for a single column from metaBytes
// starting at metaPos. It updates the ColumnMeta struct and returns the new position.
func parseColumnMetadata(metaBytes []byte, metaPos int, colType byte, meta *ColumnMeta) int {
	switch colType {
	case MYSQL_TYPE_DECIMAL, MYSQL_TYPE_NEWDECIMAL:
		// 2 bytes: precision, scale
		if metaPos+2 <= len(metaBytes) {
			meta.Precision = int(metaBytes[metaPos])
			meta.Scale = int(metaBytes[metaPos+1])
			metaPos += 2
		}

	case MYSQL_TYPE_TINY:
		// No metadata for TINYINT
		meta.Length = 4

	case MYSQL_TYPE_SHORT:
		meta.Length = 6

	case MYSQL_TYPE_LONG:
		meta.Length = 11

	case MYSQL_TYPE_FLOAT:
		// 1 byte: display length
		if metaPos < len(metaBytes) {
			meta.Length = int(metaBytes[metaPos])
			metaPos++
		}

	case MYSQL_TYPE_DOUBLE:
		// 1 byte: display length
		if metaPos < len(metaBytes) {
			meta.Length = int(metaBytes[metaPos])
			metaPos++
		}

	case MYSQL_TYPE_TIMESTAMP, MYSQL_TYPE_DATE, MYSQL_TYPE_TIME, MYSQL_TYPE_DATETIME,
		MYSQL_TYPE_NEWDATE, MYSQL_TYPE_YEAR:
		// No metadata for these

	case MYSQL_TYPE_LONGLONG:
		meta.Length = 20

	case MYSQL_TYPE_INT24:
		meta.Length = 8

	case MYSQL_TYPE_VARCHAR, MYSQL_TYPE_VAR_STRING:
		// 2 bytes: max length (little-endian)
		if metaPos+2 <= len(metaBytes) {
			meta.Length = int(metaBytes[metaPos]) | int(metaBytes[metaPos+1])<<8
			metaPos += 2
		}

	case MYSQL_TYPE_BIT:
		// 2 bytes: bits_in_last_byte (1 byte), number_of_bytes (1 byte)
		if metaPos+2 <= len(metaBytes) {
			meta.BitBits = int(metaBytes[metaPos])
			meta.BitBytes = int(metaBytes[metaPos+1])
			bits := meta.BitBits
			if bits == 0 {
				bits = 8
			}
			meta.Length = (meta.BitBytes-1)*8 + bits
			metaPos += 2
		}

	case MYSQL_TYPE_TIMESTAMP2, MYSQL_TYPE_DATETIME2, MYSQL_TYPE_TIME2:
		// 1 byte: fractional seconds precision (FSP)
		if metaPos < len(metaBytes) {
			meta.FSP = int(metaBytes[metaPos])
			metaPos++
		}

	case MYSQL_TYPE_TINY_BLOB, MYSQL_TYPE_BLOB, MYSQL_TYPE_MEDIUM_BLOB, MYSQL_TYPE_LONG_BLOB,
		MYSQL_TYPE_JSON, MYSQL_TYPE_GEOMETRY:
		// 1 byte: length of the length field (packed integer size)
		if metaPos < len(metaBytes) {
			meta.Length = int(metaBytes[metaPos])
			metaPos++
		}

	case MYSQL_TYPE_ENUM:
		// 2 bytes: enum count (1 byte), storage size (1 byte)
		if metaPos+2 <= len(metaBytes) {
			meta.EnumSetSize = int(metaBytes[metaPos+1])
			meta.Length = int(metaBytes[metaPos]) // count
			metaPos += 2
		}

	case MYSQL_TYPE_SET:
		// 2 bytes: set member count (1 byte), storage bytes (1 byte)
		if metaPos+2 <= len(metaBytes) {
			meta.EnumSetSize = int(metaBytes[metaPos+1])
			meta.Length = int(metaBytes[metaPos]) // count
			metaPos += 2
		}

	case MYSQL_TYPE_STRING:
		// 2 bytes: real_type (high byte) | max_length (low byte)
		// If real_type is ENUM or SET, treat accordingly.
		if metaPos+2 <= len(metaBytes) {
			realType := metaBytes[metaPos+1]
			maxLen := int(metaBytes[metaPos])
			meta.RealType = realType
			meta.Length = maxLen
			if realType == MYSQL_TYPE_ENUM || realType == MYSQL_TYPE_SET {
				meta.EnumSetSize = maxLen
			}
			metaPos += 2
		}

	default:
		// Unknown column type; skip any metadata bytes.
		// Most metadata blocks are 0 or 2 bytes; just advance by 2 speculatively.
		if metaPos+2 <= len(metaBytes) {
			metaPos += 2
		} else if metaPos < len(metaBytes) {
			metaPos++
		}
	}
	return metaPos
}
