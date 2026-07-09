package parser

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// EventType represents a MySQL binlog event type code.
type EventType byte

// Binlog event type constants.
const (
	UnknownEvent           EventType = 0
	QueryEvent             EventType = 2
	RotateEvent            EventType = 4
	XidEvent               EventType = 16
	TableMapEvent          EventType = 19
	WriteRowsEventV1       EventType = 23
	UpdateRowsEventV1      EventType = 24
	DeleteRowsEventV1      EventType = 25
	WriteRowsEventV2       EventType = 30
	UpdateRowsEventV2      EventType = 31
	DeleteRowsEventV2      EventType = 32
	FormatDescriptionEvent EventType = 15
)

// String returns a human-readable name for the event type.
func (t EventType) String() string {
	switch t {
	case UnknownEvent:
		return "UNKNOWN"
	case QueryEvent:
		return "QUERY"
	case RotateEvent:
		return "ROTATE"
	case XidEvent:
		return "XID"
	case TableMapEvent:
		return "TABLE_MAP"
	case WriteRowsEventV1:
		return "WRITE_ROWS_V1"
	case UpdateRowsEventV1:
		return "UPDATE_ROWS_V1"
	case DeleteRowsEventV1:
		return "DELETE_ROWS_V1"
	case WriteRowsEventV2:
		return "WRITE_ROWS_V2"
	case UpdateRowsEventV2:
		return "UPDATE_ROWS_V2"
	case DeleteRowsEventV2:
		return "DELETE_ROWS_V2"
	case FormatDescriptionEvent:
		return "FORMAT_DESCRIPTION"
	default:
		return fmt.Sprintf("EVENT(%d)", byte(t))
	}
}

// EventHeader represents the common header of every binlog event.
// MySQL binlog v4 header is 19 bytes.
const EventHeaderSize = 19

// EventHeader contains the parsed fields from the 19-byte binlog event header.
type EventHeader struct {
	Timestamp uint32
	Type      EventType
	ServerID  uint32
	EventLen  uint32
	NextPos   uint32
	Flags     uint16
}

// ParseEventHeader parses a 19-byte binlog event header from data.
// data must be at least EventHeaderSize bytes.
func ParseEventHeader(data []byte) (EventHeader, error) {
	if len(data) < EventHeaderSize {
		return EventHeader{}, io.ErrUnexpectedEOF
	}
	return EventHeader{
		Timestamp: binary.LittleEndian.Uint32(data[0:4]),
		Type:      EventType(data[4]),
		ServerID:  binary.LittleEndian.Uint32(data[5:9]),
		EventLen:  binary.LittleEndian.Uint32(data[9:13]),
		NextPos:   binary.LittleEndian.Uint32(data[13:17]),
		Flags:     binary.LittleEndian.Uint16(data[17:19]),
	}, nil
}

// ============================================================
// Binary parsing helpers
// ============================================================

// readUint16 reads a 2-byte little-endian unsigned integer.
func readUint16(data []byte, pos int) (uint16, int, error) {
	if pos+2 > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	return binary.LittleEndian.Uint16(data[pos:]), pos + 2, nil
}

// readInt16 reads a 2-byte little-endian signed integer.
func readInt16(data []byte, pos int) (int16, int, error) {
	if pos+2 > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	return int16(binary.LittleEndian.Uint16(data[pos:])), pos + 2, nil
}

// readUint24 reads a 3-byte little-endian unsigned integer.
func readUint24(data []byte, pos int) (uint32, int, error) {
	if pos+3 > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	return uint32(data[pos]) | uint32(data[pos+1])<<8 | uint32(data[pos+2])<<16, pos + 3, nil
}

// readInt24 reads a 3-byte little-endian signed integer (sign-extended).
func readInt24(data []byte, pos int) (int32, int, error) {
	if pos+3 > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	v := uint32(data[pos]) | uint32(data[pos+1])<<8 | uint32(data[pos+2])<<16
	// Sign extend 24-bit to 32-bit
	if v&0x800000 != 0 {
		v |= 0xFF000000
	}
	return int32(v), pos + 3, nil
}

// readUint32 reads a 4-byte little-endian unsigned integer.
func readUint32(data []byte, pos int) (uint32, int, error) {
	if pos+4 > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	return binary.LittleEndian.Uint32(data[pos:]), pos + 4, nil
}

// readInt32 reads a 4-byte little-endian signed integer.
func readInt32(data []byte, pos int) (int32, int, error) {
	if pos+4 > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	return int32(binary.LittleEndian.Uint32(data[pos:])), pos + 4, nil
}

// readUint64 reads an 8-byte little-endian unsigned integer.
func readUint64(data []byte, pos int) (uint64, int, error) {
	if pos+8 > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	return binary.LittleEndian.Uint64(data[pos:]), pos + 8, nil
}

// readInt64 reads an 8-byte little-endian signed integer.
func readInt64(data []byte, pos int) (int64, int, error) {
	if pos+8 > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	return int64(binary.LittleEndian.Uint64(data[pos:])), pos + 8, nil
}

// readFloat32 reads a 4-byte little-endian IEEE 754 float.
func readFloat32(data []byte, pos int) (float32, int, error) {
	if pos+4 > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	bits := binary.LittleEndian.Uint32(data[pos:])
	return math.Float32frombits(bits), pos + 4, nil
}

// readFloat64 reads an 8-byte little-endian IEEE 754 double.
func readFloat64(data []byte, pos int) (float64, int, error) {
	if pos+8 > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	bits := binary.LittleEndian.Uint64(data[pos:])
	return math.Float64frombits(bits), pos + 8, nil
}

// readUint64BE reads a 3-byte or 5-byte big-endian integer.
// Used for DATETIME2 / TIME2 packed formats. n must be 3 or 5.
func readUintBE(data []byte, pos int, n int) (uint64, int, error) {
	if pos+n > len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	var v uint64
	for i := 0; i < n; i++ {
		v = (v << 8) | uint64(data[pos+i])
	}
	return v, pos + n, nil
}

// LengthEncodedInt reads a MySQL length-encoded integer (packed integer).
// Returns the integer value, new position, and any error.
func LengthEncodedInt(data []byte, pos int) (uint64, int, error) {
	if pos >= len(data) {
		return 0, pos, io.ErrUnexpectedEOF
	}
	switch data[pos] {
	case 0xFB:
		// NULL value indicator
		return 0, pos + 1, nil
	case 0xFC:
		if pos+3 > len(data) {
			return 0, pos, io.ErrUnexpectedEOF
		}
		return uint64(binary.LittleEndian.Uint16(data[pos+1:])), pos + 3, nil
	case 0xFD:
		if pos+4 > len(data) {
			return 0, pos, io.ErrUnexpectedEOF
		}
		v := uint32(data[pos+1]) | uint32(data[pos+2])<<8 | uint32(data[pos+3])<<16
		return uint64(v), pos + 4, nil
	case 0xFE:
		if pos+9 > len(data) {
			return 0, pos, io.ErrUnexpectedEOF
		}
		return binary.LittleEndian.Uint64(data[pos+1:]), pos + 9, nil
	default:
		// 0-250: the byte itself is the value
		return uint64(data[pos]), pos + 1, nil
	}
}

// LengthEncodedString reads a MySQL length-encoded string.
// Format: length (packed integer) followed by that many bytes.
func LengthEncodedString(data []byte, pos int) (string, int, error) {
	length, pos, err := LengthEncodedInt(data, pos)
	if err != nil {
		return "", pos, err
	}
	end := pos + int(length)
	if end > len(data) {
		return "", pos, io.ErrUnexpectedEOF
	}
	return string(data[pos:end]), end, nil
}

// NullTerminatedString reads a null-terminated string from data starting at pos.
func NullTerminatedString(data []byte, pos int) (string, int, error) {
	end := pos
	for end < len(data) && data[end] != 0 {
		end++
	}
	if end >= len(data) {
		return "", pos, io.ErrUnexpectedEOF
	}
	return string(data[pos:end]), end + 1, nil
}

// FixedLengthString reads exactly n bytes as a string.
func FixedLengthString(data []byte, pos int, n int) (string, int, error) {
	end := pos + n
	if end > len(data) {
		return "", pos, io.ErrUnexpectedEOF
	}
	return string(data[pos:end]), end, nil
}

// BitmapByteSize returns the number of bytes needed for a bitmap of n bits.
func BitmapByteSize(n int) int {
	return (n + 7) / 8
}

// IsBitSet checks if the bit at position 'bit' (0-indexed) is set in the bitmap.
func IsBitSet(bitmap []byte, bit int) bool {
	byteIdx := bit / 8
	bitIdx := bit % 8
	if byteIdx >= len(bitmap) {
		return false
	}
	return (bitmap[byteIdx] & (1 << bitIdx)) != 0
}

// PopCount returns the number of set bits in the bitmap (up to n bits).
func PopCount(bitmap []byte, n int) int {
	count := 0
	for i := 0; i < n; i++ {
		if IsBitSet(bitmap, i) {
			count++
		}
	}
	return count
}
