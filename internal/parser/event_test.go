package parser

import (
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEventTypeString(t *testing.T) {
	tests := []struct {
		et   EventType
		want string
	}{
		{UnknownEvent, "UNKNOWN"},
		{QueryEvent, "QUERY"},
		{RotateEvent, "ROTATE"},
		{XidEvent, "XID"},
		{TableMapEvent, "TABLE_MAP"},
		{WriteRowsEventV1, "WRITE_ROWS_V1"},
		{UpdateRowsEventV1, "UPDATE_ROWS_V1"},
		{DeleteRowsEventV1, "DELETE_ROWS_V1"},
		{WriteRowsEventV2, "WRITE_ROWS_V2"},
		{UpdateRowsEventV2, "UPDATE_ROWS_V2"},
		{DeleteRowsEventV2, "DELETE_ROWS_V2"},
		{FormatDescriptionEvent, "FORMAT_DESCRIPTION"},
		{EventType(99), "EVENT(99)"},
	}
	for _, tc := range tests {
		assert.Equal(t, tc.want, tc.et.String(), "EventType(%d).String()", byte(tc.et))
	}
}

func TestParseEventHeader(t *testing.T) {
	// Construct a 19-byte binlog header:
	// timestamp = 0x5A5B5C5D (little-endian)
	// type = 19 (TABLE_MAP)
	// server_id = 1
	// event_length = 57
	// next_pos = 57
	// flags = 0
	data := []byte{
		0x5D, 0x5C, 0x5B, 0x5A, // timestamp LE
		0x13, // type = 19 (TABLE_MAP)
		0x01, 0x00, 0x00, 0x00, // server_id = 1
		0x39, 0x00, 0x00, 0x00, // event_length = 57
		0x39, 0x00, 0x00, 0x00, // next_pos = 57
		0x00, 0x00, // flags = 0
	}

	hdr, err := ParseEventHeader(data)
	require.NoError(t, err)
	assert.Equal(t, uint32(0x5A5B5C5D), hdr.Timestamp)
	assert.Equal(t, TableMapEvent, hdr.Type)
	assert.Equal(t, uint32(1), hdr.ServerID)
	assert.Equal(t, uint32(57), hdr.EventLen)
	assert.Equal(t, uint32(57), hdr.NextPos)
	assert.Equal(t, uint16(0), hdr.Flags)
}

func TestParseEventHeader_TooShort(t *testing.T) {
	_, err := ParseEventHeader(make([]byte, 10))
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestParseEventHeader_RoundTrip(t *testing.T) {
	// Build header with specific values
	original := EventHeader{
		Timestamp: 1234567890,
		Type:      WriteRowsEventV2,
		ServerID:  42,
		EventLen:  1024,
		NextPos:   2048,
		Flags:     0x0001,
	}

	buf := make([]byte, EventHeaderSize)
	binaryPutUint32(buf[0:4], original.Timestamp)
	buf[4] = byte(original.Type)
	binaryPutUint32(buf[5:9], original.ServerID)
	binaryPutUint32(buf[9:13], original.EventLen)
	binaryPutUint32(buf[13:17], original.NextPos)
	binaryPutUint16(buf[17:19], original.Flags)

	parsed, err := ParseEventHeader(buf)
	require.NoError(t, err)
	assert.Equal(t, original, parsed)
}

// Helper to put uint32 LE.
func binaryPutUint32(buf []byte, v uint32) {
	buf[0] = byte(v)
	buf[1] = byte(v >> 8)
	buf[2] = byte(v >> 16)
	buf[3] = byte(v >> 24)
}

// Helper to put uint16 LE.
func binaryPutUint16(buf []byte, v uint16) {
	buf[0] = byte(v)
	buf[1] = byte(v >> 8)
}

func TestBinaryHelpers_ReadUint16(t *testing.T) {
	data := []byte{0x34, 0x12}
	v, pos, err := readUint16(data, 0)
	require.NoError(t, err)
	assert.Equal(t, uint16(0x1234), v)
	assert.Equal(t, 2, pos)

	// Short buffer
	_, _, err = readUint16([]byte{0x01}, 0)
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestBinaryHelpers_ReadInt16(t *testing.T) {
	// Negative value: -1 = 0xFFFF
	data := []byte{0xFF, 0xFF}
	v, pos, err := readInt16(data, 0)
	require.NoError(t, err)
	assert.Equal(t, int16(-1), v)
	assert.Equal(t, 2, pos)

	// Positive: 0x7FFF = 32767
	data2 := []byte{0xFF, 0x7F}
	v, _, err = readInt16(data2, 0)
	require.NoError(t, err)
	assert.Equal(t, int16(32767), v)
}

func TestBinaryHelpers_ReadUint24(t *testing.T) {
	data := []byte{0x78, 0x56, 0x34} // 0x345678
	v, pos, err := readUint24(data, 0)
	require.NoError(t, err)
	assert.Equal(t, uint32(0x345678), v)
	assert.Equal(t, 3, pos)

	_, _, err = readUint24([]byte{0x01, 0x02}, 0)
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestBinaryHelpers_ReadInt24(t *testing.T) {
	// Positive
	data := []byte{0x78, 0x56, 0x34} // 0x345678 = 3433080
	v, pos, err := readInt24(data, 0)
	require.NoError(t, err)
	assert.Equal(t, int32(0x345678), v)
	assert.Equal(t, 3, pos)

	// Negative (sign bit set)
	data2 := []byte{0x00, 0x00, 0x80} // 0x800000 = -8388608
	v, _, err = readInt24(data2, 0)
	require.NoError(t, err)
	assert.Equal(t, int32(-8388608), v)
}

func TestBinaryHelpers_ReadUint32(t *testing.T) {
	data := []byte{0xEF, 0xBE, 0xAD, 0xDE} // 0xDEADBEEF
	v, pos, err := readUint32(data, 0)
	require.NoError(t, err)
	assert.Equal(t, uint32(0xDEADBEEF), v)
	assert.Equal(t, 4, pos)
}

func TestBinaryHelpers_ReadInt32(t *testing.T) {
	// -2
	data := []byte{0xFE, 0xFF, 0xFF, 0xFF}
	v, pos, err := readInt32(data, 0)
	require.NoError(t, err)
	assert.Equal(t, int32(-2), v)
	assert.Equal(t, 4, pos)
}

func TestBinaryHelpers_ReadUint64(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}
	v, pos, err := readUint64(data, 0)
	require.NoError(t, err)
	assert.Equal(t, uint64(0x0807060504030201), v)
	assert.Equal(t, 8, pos)
}

func TestBinaryHelpers_ReadFloat32(t *testing.T) {
	// 3.14 in IEEE 754 LE
	data := []byte{0xC3, 0xF5, 0x48, 0x40}
	v, pos, err := readFloat32(data, 0)
	require.NoError(t, err)
	assert.InDelta(t, 3.14, float64(v), 0.001)
	assert.Equal(t, 4, pos)
}

func TestBinaryHelpers_ReadFloat64(t *testing.T) {
	// 3.141592653589793 in IEEE 754 LE
	data := []byte{0x18, 0x2D, 0x44, 0x54, 0xFB, 0x21, 0x09, 0x40}
	v, pos, err := readFloat64(data, 0)
	require.NoError(t, err)
	assert.InDelta(t, 3.141592653589793, v, 0.000000000000001)
	assert.Equal(t, 8, pos)
}

func TestBinaryHelpers_ReadUintBE(t *testing.T) {
	// 5 bytes big-endian: 0x01 0x02 0x03 0x04 0x05 = 0x0102030405
	data := []byte{0x01, 0x02, 0x03, 0x04, 0x05}
	v, pos, err := readUintBE(data, 0, 5)
	require.NoError(t, err)
	assert.Equal(t, uint64(0x0102030405), v)
	assert.Equal(t, 5, pos)

	// 3 bytes big-endian
	v, pos, err = readUintBE(data, 0, 3)
	require.NoError(t, err)
	assert.Equal(t, uint64(0x010203), v)
	assert.Equal(t, 3, pos)

	// Short buffer
	_, _, err = readUintBE(data, 0, 6)
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestLengthEncodedInt(t *testing.T) {
	tests := []struct {
		name  string
		data  []byte
		want  uint64
		adv   int
	}{
		{"1-byte (0)", []byte{0x00}, 0, 1},
		{"1-byte (250)", []byte{0xFA}, 250, 1},
		{"2-byte (0xFC)", []byte{0xFC, 0x10, 0x00}, 0x10, 3},
		{"2-byte (0xFC big)", []byte{0xFC, 0xEF, 0xBE}, 0xBEEF, 3},
		{"3-byte (0xFD)", []byte{0xFD, 0x78, 0x56, 0x34}, 0x345678, 4},
		{"8-byte (0xFE)", []byte{0xFE, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}, 0x0807060504030201, 9},
		{"null marker (0xFB)", []byte{0xFB}, 0, 1},
	}

	for _, tc := range tests {
		v, pos, err := LengthEncodedInt(tc.data, 0)
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.want, v, "%s: value", tc.name)
		assert.Equal(t, tc.adv, pos, "%s: advance", tc.name)
	}
}

func TestLengthEncodedInt_ShortBuffer(t *testing.T) {
	// 0xFC requires 2 more bytes, only 1 available
	_, _, err := LengthEncodedInt([]byte{0xFC, 0x01}, 0)
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestLengthEncodedString(t *testing.T) {
	data := []byte{0x05, 0x48, 0x65, 0x6C, 0x6C, 0x6F} // length=5, "Hello"
	s, pos, err := LengthEncodedString(data, 0)
	require.NoError(t, err)
	assert.Equal(t, "Hello", s)
	assert.Equal(t, 6, pos)
}

func TestNullTerminatedString(t *testing.T) {
	data := []byte{0x48, 0x65, 0x6C, 0x6C, 0x6F, 0x00, 0xFF}
	s, pos, err := NullTerminatedString(data, 0)
	require.NoError(t, err)
	assert.Equal(t, "Hello", s)
	assert.Equal(t, 6, pos)
}

func TestNullTerminatedString_NoNull(t *testing.T) {
	_, _, err := NullTerminatedString([]byte{0x48, 0x65}, 0)
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestFixedLengthString(t *testing.T) {
	data := []byte{0x48, 0x65, 0x6C, 0x6C, 0x6F}
	s, pos, err := FixedLengthString(data, 0, 5)
	require.NoError(t, err)
	assert.Equal(t, "Hello", s)
	assert.Equal(t, 5, pos)

	// Short buffer
	_, _, err = FixedLengthString(data, 0, 6)
	assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
}

func TestBitmapByteSize(t *testing.T) {
	assert.Equal(t, 0, BitmapByteSize(0))
	assert.Equal(t, 1, BitmapByteSize(1))
	assert.Equal(t, 1, BitmapByteSize(7))
	assert.Equal(t, 1, BitmapByteSize(8))
	assert.Equal(t, 2, BitmapByteSize(9))
	assert.Equal(t, 2, BitmapByteSize(15))
	assert.Equal(t, 2, BitmapByteSize(16))
	assert.Equal(t, 3, BitmapByteSize(17))
}

func TestIsBitSet(t *testing.T) {
	// bitmap = 0b01010101 = 0x55
	bitmap := []byte{0x55}

	assert.True(t, IsBitSet(bitmap, 0))  // bit 0 = 1
	assert.False(t, IsBitSet(bitmap, 1)) // bit 1 = 0
	assert.True(t, IsBitSet(bitmap, 2))  // bit 2 = 1
	assert.False(t, IsBitSet(bitmap, 3)) // bit 3 = 0
	assert.True(t, IsBitSet(bitmap, 4))  // bit 4 = 1
	assert.False(t, IsBitSet(bitmap, 5)) // bit 5 = 0
	assert.True(t, IsBitSet(bitmap, 6))  // bit 6 = 1
	assert.False(t, IsBitSet(bitmap, 7)) // bit 7 = 0
	assert.False(t, IsBitSet(bitmap, 8)) // out of range
}

func TestPopCount(t *testing.T) {
	bitmap := []byte{0x55} // 0b01010101 = 4 bits set
	assert.Equal(t, 4, PopCount(bitmap, 8))
	assert.Equal(t, 2, PopCount(bitmap, 3)) // bits 0,1,2: 101 -> 2
	assert.Equal(t, 0, PopCount([]byte{0x00}, 8))
}
