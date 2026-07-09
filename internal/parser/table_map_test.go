package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func buildTableMapPayload(tableID uint64, db, table string,
	colTypes []byte, colMeta []byte, nullBitmap []byte) []byte {

	var payload []byte

	// 6-byte table ID (LE)
	payload = append(payload, byte(tableID))
	payload = append(payload, byte(tableID>>8))
	payload = append(payload, byte(tableID>>16))
	payload = append(payload, byte(tableID>>24))
	payload = append(payload, byte(tableID>>32))
	payload = append(payload, byte(tableID>>40))

	// 2-byte flags
	payload = append(payload, 0x00, 0x00)

	// Database name: length + string + null
	payload = append(payload, byte(len(db)))
	payload = append(payload, []byte(db)...)
	payload = append(payload, 0x00)

	// Table name: length + string + null
	payload = append(payload, byte(len(table)))
	payload = append(payload, []byte(table)...)
	payload = append(payload, 0x00)

	// Column count (packed int)
	payload = append(payload, byte(len(colTypes)))

	// Column types
	payload = append(payload, colTypes...)

	// Column metadata: packed length + bytes
	payload = append(payload, byte(len(colMeta)))
	payload = append(payload, colMeta...)

	// Null bitmap
	payload = append(payload, nullBitmap...)

	return payload
}

func TestParseTableMap_Basic(t *testing.T) {
	// Single table with 3 columns: INT, VARCHAR(100), TINYINT
	payload := buildTableMapPayload(
		42,
		"testdb",
		"users",
		[]byte{MYSQL_TYPE_LONG, MYSQL_TYPE_VARCHAR, MYSQL_TYPE_TINY},
		[]byte{
			0x00,                     // LONG: no metadata
			0x64, 0x00,               // VARCHAR(100): length=100 (LE)
			0x00,                     // TINY: no metadata
		},
		[]byte{0x00}, // null bitmap (no nullable columns)
	)

	tm, err := ParseTableMap(payload)
	require.NoError(t, err)
	assert.Equal(t, uint64(42), tm.TableID)
	assert.Equal(t, "testdb", tm.Database)
	assert.Equal(t, "users", tm.Table)
	assert.Equal(t, uint64(3), tm.ColumnCount)
	assert.Equal(t, []byte{MYSQL_TYPE_LONG, MYSQL_TYPE_VARCHAR, MYSQL_TYPE_TINY}, tm.ColumnTypes)
	require.Len(t, tm.ColumnMeta, 3)

	// Column 0: INT
	assert.Equal(t, byte(MYSQL_TYPE_LONG), tm.ColumnMeta[0].Type)
	assert.Equal(t, 11, tm.ColumnMeta[0].Length)

	// Column 1: VARCHAR(100)
	assert.Equal(t, byte(MYSQL_TYPE_VARCHAR), tm.ColumnMeta[1].Type)
	assert.Equal(t, 100, tm.ColumnMeta[1].Length)

	// Column 2: TINYINT
	assert.Equal(t, byte(MYSQL_TYPE_TINY), tm.ColumnMeta[2].Type)
	assert.Equal(t, 4, tm.ColumnMeta[2].Length)
}

func TestParseTableMap_NEWDECIMAL(t *testing.T) {
	// DECIMAL(10,2)
	payload := buildTableMapPayload(
		1,
		"db",
		"t1",
		[]byte{MYSQL_TYPE_NEWDECIMAL},
		[]byte{10, 2}, // precision=10, scale=2
		[]byte{0x00},
	)

	tm, err := ParseTableMap(payload)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), tm.TableID)
	assert.Equal(t, "db", tm.Database)
	assert.Equal(t, "t1", tm.Table)
	require.Len(t, tm.ColumnMeta, 1)
	assert.Equal(t, 10, tm.ColumnMeta[0].Precision)
	assert.Equal(t, 2, tm.ColumnMeta[0].Scale)
}

func TestParseTableMap_BIT(t *testing.T) {
	// BIT(8): 1 byte for 8 bits, 0 bits in last byte
	payload := buildTableMapPayload(
		2,
		"db",
		"t2",
		[]byte{MYSQL_TYPE_BIT},
		[]byte{0x00, 0x01}, // bits_in_last_byte=0, number_of_bytes=1 => total 8 bits
		[]byte{0x00},
	)

	tm, err := ParseTableMap(payload)
	require.NoError(t, err)
	assert.Equal(t, 8, tm.ColumnMeta[0].Length)
	assert.Equal(t, 0, tm.ColumnMeta[0].BitBits)
	assert.Equal(t, 1, tm.ColumnMeta[0].BitBytes)
}

func TestParseTableMap_TIMESTAMP2(t *testing.T) {
	// TIMESTAMP(3)
	payload := buildTableMapPayload(
		3,
		"db",
		"t3",
		[]byte{MYSQL_TYPE_TIMESTAMP2},
		[]byte{0x03}, // fsp=3
		[]byte{0x00},
	)

	tm, err := ParseTableMap(payload)
	require.NoError(t, err)
	assert.Equal(t, 3, tm.ColumnMeta[0].FSP)
}

func TestParseTableMap_STRING(t *testing.T) {
	// CHAR(20) stored as STRING type
	payload := buildTableMapPayload(
		4,
		"db",
		"t4",
		[]byte{MYSQL_TYPE_STRING},
		[]byte{20, MYSQL_TYPE_STRING}, // max_len=20, real_type=STRING
		[]byte{0x00},
	)

	tm, err := ParseTableMap(payload)
	require.NoError(t, err)
	require.Len(t, tm.ColumnMeta, 1)
	assert.Equal(t, MYSQL_TYPE_STRING, int(tm.ColumnMeta[0].RealType))
	assert.Equal(t, 20, tm.ColumnMeta[0].Length)
}

func TestParseTableMap_ENUM(t *testing.T) {
	// ENUM('a','b','c') stored as STRING type with real_type=ENUM
	payload := buildTableMapPayload(
		5,
		"db",
		"t5",
		[]byte{MYSQL_TYPE_STRING},
		[]byte{0x01, MYSQL_TYPE_ENUM}, // enum_size=1 byte, real_type=ENUM
		[]byte{0x00},
	)

	tm, err := ParseTableMap(payload)
	require.NoError(t, err)
	require.Len(t, tm.ColumnMeta, 1)
	assert.Equal(t, MYSQL_TYPE_ENUM, int(tm.ColumnMeta[0].RealType))
	assert.Equal(t, 1, tm.ColumnMeta[0].EnumSetSize)
}

func TestParseTableMap_TooShort(t *testing.T) {
	_, err := ParseTableMap([]byte{0x01})
	assert.Error(t, err)
}

func TestParseTableMap_9ColumnBitmap(t *testing.T) {
	// 9 columns should have a 2-byte null bitmap
	payload := buildTableMapPayload(
		10,
		"db",
		"bigtable",
		[]byte{
			MYSQL_TYPE_TINY, MYSQL_TYPE_TINY, MYSQL_TYPE_TINY,
			MYSQL_TYPE_TINY, MYSQL_TYPE_TINY, MYSQL_TYPE_TINY,
			MYSQL_TYPE_TINY, MYSQL_TYPE_TINY, MYSQL_TYPE_TINY,
		},
		[]byte{0, 0, 0, 0, 0, 0, 0, 0, 0}, // 9 zero-byte metadata entries
		[]byte{0x00, 0x00},                  // 2-byte null bitmap
	)

	tm, err := ParseTableMap(payload)
	require.NoError(t, err)
	assert.Equal(t, uint64(9), tm.ColumnCount)
	assert.Len(t, tm.ColumnTypes, 9)
	assert.Len(t, tm.ColumnMeta, 9)
}

func TestTableMapRegistry(t *testing.T) {
	reg := NewTableMapRegistry()

	tm1 := &TableMap{TableID: 1, Database: "db1", Table: "t1"}
	tm2 := &TableMap{TableID: 2, Database: "db2", Table: "t2"}

	reg.Set(tm1)
	reg.Set(tm2)

	assert.Same(t, tm1, reg.Get(1))
	assert.Same(t, tm2, reg.Get(2))
	assert.Nil(t, reg.Get(99))

	reg.Delete(1)
	assert.Nil(t, reg.Get(1))
	assert.Same(t, tm2, reg.Get(2))
}

func TestTableMapClone(t *testing.T) {
	original := &TableMap{
		TableID:     42,
		Database:    "test",
		Table:       "users",
		ColumnCount: 2,
		ColumnTypes: []byte{MYSQL_TYPE_LONG, MYSQL_TYPE_VARCHAR},
		ColumnMeta: []ColumnMeta{
			{Type: MYSQL_TYPE_LONG, Length: 11},
			{Type: MYSQL_TYPE_VARCHAR, Length: 100},
		},
	}

	clone := original.Clone()
	assert.Equal(t, original.Database, clone.Database)
	assert.Equal(t, original.Table, clone.Table)

	// Modify original, clone should be unaffected
	original.Database = "changed"
	assert.Equal(t, "changed", original.Database)
	assert.Equal(t, "test", clone.Database)

	// Modify slice
	original.ColumnTypes[0] = 99
	assert.Equal(t, byte(99), original.ColumnTypes[0])
	assert.Equal(t, byte(MYSQL_TYPE_LONG), clone.ColumnTypes[0])
}
