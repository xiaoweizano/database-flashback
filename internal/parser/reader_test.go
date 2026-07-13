package parser

import (
	"encoding/binary"
	"hash/crc32"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeBinlogHeader writes the 4-byte magic number to f.
func writeBinlogHeader(f *os.File) {
	f.Write([]byte(BinlogMagic))
}

// writeEvent writes a complete binlog event with optional CRC32.
// Returns the event length.
func writeEvent(f *os.File, hdr EventHeader, body []byte, crc bool) int64 {
	// Write 19-byte header
	buf := make([]byte, EventHeaderSize)
	binary.LittleEndian.PutUint32(buf[0:4], hdr.Timestamp)
	buf[4] = byte(hdr.Type)
	binary.LittleEndian.PutUint32(buf[5:9], hdr.ServerID)

	totalLen := uint32(EventHeaderSize + len(body))
	if crc {
		totalLen += 4
	}
	hdr.EventLen = totalLen
	binary.LittleEndian.PutUint32(buf[9:13], hdr.EventLen)
	binary.LittleEndian.PutUint32(buf[13:17], hdr.NextPos)
	binary.LittleEndian.PutUint16(buf[17:19], hdr.Flags)

	f.Write(buf)

	// Write body
	f.Write(body)

	// Write CRC32 if needed
	if crc {
		crcData := append(buf, body...)
		crcVal := crc32.ChecksumIEEE(crcData)
		crcBytes := make([]byte, 4)
		binary.LittleEndian.PutUint32(crcBytes, crcVal)
		f.Write(crcBytes)
	}

	return int64(totalLen)
}

func TestBinlogReader_Open_ValidMagic(t *testing.T) {
	f, err := os.CreateTemp("", "binlog-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	defer f.Close()

	writeBinlogHeader(f)
	f.Close()

	r := NewBinlogReader()
	err = r.Open(f.Name())
	require.NoError(t, err)
	assert.Equal(t, uint32(4), r.Position())
	assert.True(t, r.IsOpen())
	assert.Equal(t, f.Name(), r.Path())
	r.Close()
}

func TestBinlogReader_Open_InvalidMagic(t *testing.T) {
	f, err := os.CreateTemp("", "binlog-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	// Write wrong magic
	f.Write([]byte{0x00, 0x00, 0x00, 0x00})
	f.Close()

	r := NewBinlogReader()
	err = r.Open(f.Name())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid binlog magic")
	assert.False(t, r.IsOpen())
}

func TestBinlogReader_Open_FileNotFound(t *testing.T) {
	r := NewBinlogReader()
	err := r.Open("/nonexistent/binlog.000001")
	assert.Error(t, err)
	assert.False(t, r.IsOpen())
}

func TestBinlogReader_ReadEvent_NoCRC(t *testing.T) {
	f, err := os.CreateTemp("", "binlog-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	writeBinlogHeader(f)

	// Write a FormatDescriptionEvent first
	fdeBody := buildFormatDescBody(4, "8.0.33\x00", []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	fdeNext := uint32(4 + EventHeaderSize + len(fdeBody))
	fdeHdr := EventHeader{
		Timestamp: 1000,
		Type:      FormatDescriptionEvent,
		ServerID:  1,
		NextPos:   fdeNext,
	}
	writeEvent(f, fdeHdr, fdeBody, false)

	// Write a RotateEvent
	rotateBody := make([]byte, 8)
	binary.LittleEndian.PutUint64(rotateBody, 0)
	rotateBody = append(rotateBody, []byte("binlog.000002\x00")...)
	rotateNext := fdeNext + uint32(EventHeaderSize+len(rotateBody))
	rotateHdr := EventHeader{
		Timestamp: 1001,
		Type:      RotateEvent,
		ServerID:  1,
		NextPos:   rotateNext,
	}
	writeEvent(f, rotateHdr, rotateBody, false)

	f.Close()

	r := NewBinlogReader()
	err = r.Open(f.Name())
	require.NoError(t, err)

	// Read FormatDescriptionEvent
	hdr, body, err := r.ReadEvent()
	require.NoError(t, err)
	assert.Equal(t, FormatDescriptionEvent, hdr.Type)
	assert.Equal(t, uint32(1000), hdr.Timestamp)
	assert.Equal(t, uint32(1), hdr.ServerID)
	assert.Equal(t, fdeNext, hdr.NextPos)
	assert.NotEmpty(t, body)

	// Read RotateEvent
	hdr, body, err = r.ReadEvent()
	require.NoError(t, err)
	assert.Equal(t, RotateEvent, hdr.Type)
	assert.Equal(t, uint32(1001), hdr.Timestamp)
	assert.Greater(t, len(body), 8)

	// Position should match the Rotate event's NextPos
	assert.Equal(t, rotateNext, r.Position())

	// Next ReadEvent should return EOF
	_, _, err = r.ReadEvent()
	assert.ErrorIs(t, err, io.EOF)

	r.Close()
}

func TestBinlogReader_ReadEvent_WithCRC(t *testing.T) {
	f, err := os.CreateTemp("", "binlog-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	writeBinlogHeader(f)

	// Write a FormatDescriptionEvent without CRC
	fdeBody := buildFormatDescBody(4, "8.0.33\x00", []byte{0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 6, 0, 0})
	fdeNext := uint32(4 + EventHeaderSize + len(fdeBody))
	fdeHdr := EventHeader{
		Timestamp: 2000,
		Type:      FormatDescriptionEvent,
		ServerID:  1,
		NextPos:   fdeNext,
	}
	writeEvent(f, fdeHdr, fdeBody, true) // CRC enabled

	// Write a QueryEvent body with CRC
	qBody := []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	qBody = append(qBody, []byte("BEGIN")...)
	qNext := fdeNext + uint32(EventHeaderSize+len(qBody)+4) // +4 for CRC
	qHdr := EventHeader{
		Timestamp: 2001,
		Type:      QueryEvent,
		ServerID:  1,
		NextPos:   qNext,
	}
	writeEvent(f, qHdr, qBody, true)

	f.Close()

	r := NewBinlogReader()
	err = r.Open(f.Name())
	require.NoError(t, err)

	// Read FormatDescription first
	hdr, _, err := r.ReadEvent()
	require.NoError(t, err)
	assert.Equal(t, FormatDescriptionEvent, hdr.Type)

	// Since FDE next_pos > current_pos, CRC checking is not yet enabled by auto-detection.
	// Manually enable it for the next event.
	r.EnableChecksum()

	// Read the query event
	hdr, body, err := r.ReadEvent()
	require.NoError(t, err)
	assert.Equal(t, QueryEvent, hdr.Type)
	assert.NotEmpty(t, body)
	assert.Equal(t, qNext, r.Position())

	r.Close()
}

func TestBinlogReader_Seek(t *testing.T) {
	f, err := os.CreateTemp("", "binlog-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	writeBinlogHeader(f)
	f.Close()

	r := NewBinlogReader()
	err = r.Open(f.Name())
	require.NoError(t, err)

	err = r.Seek(100)
	require.NoError(t, err)
	assert.Equal(t, uint32(100), r.Position())

	r.Close()
}

func TestBinlogReader_Seek_NotOpen(t *testing.T) {
	r := NewBinlogReader()
	err := r.Seek(0)
	assert.Error(t, err)
}

func TestBinlogReader_ReadEvent_NotOpen(t *testing.T) {
	r := NewBinlogReader()
	_, _, err := r.ReadEvent()
	assert.Error(t, err)
}

func TestBinlogReader_Close_Idempotent(t *testing.T) {
	r := NewBinlogReader()
	err := r.Close()
	assert.NoError(t, err) // Close on a closed reader is OK

	f, err := os.CreateTemp("", "binlog-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())
	writeBinlogHeader(f)
	f.Close()

	r.Open(f.Name())
	require.True(t, r.IsOpen())
	err = r.Close()
	assert.NoError(t, err)
	assert.False(t, r.IsOpen())

	// Second close should be safe
	err = r.Close()
	assert.NoError(t, err)
}

// buildFormatDescBody constructs a FormatDescriptionEvent body for testing.
// binlogVersion: typically 4.
// serverVersion: 50-byte server version string (can be shorter).
// postHeaderLens: byte array of post-header lengths for each event type.
func buildFormatDescBody(binlogVersion uint16, serverVersion string, postHeaderLens []byte) []byte {
	body := make([]byte, 2+50+4+1)
	// Binlog version
	binary.LittleEndian.PutUint16(body[0:2], binlogVersion)
	// Server version (50 bytes, null-padded)
	copy(body[2:52], serverVersion)
	// Creator timestamp
	binary.LittleEndian.PutUint32(body[52:56], 0)
	// Common header length (19 for v4)
	body[56] = 19
	// Post-header lengths
	body = append(body, postHeaderLens...)
	return body
}

func TestBuildFormatDescBody(t *testing.T) {
	body := buildFormatDescBody(4, "8.0.33", []byte{56, 13, 0, 8, 0, 18, 0, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 4, 0})
	// body should be at least 57 bytes (2+50+4+1) + post_header_lens
	assert.GreaterOrEqual(t, len(body), 57)
	// Check binlog version
	assert.Equal(t, uint16(4), binary.LittleEndian.Uint16(body[0:2]))
	// Check header length
	assert.Equal(t, byte(19), body[56])
}

// ---------------------------------------------------------------------------
// Edge case tests
// ---------------------------------------------------------------------------

func TestBinlogReader_EmptyBinlog(t *testing.T) {
	// A file with only the magic number and no events.
	f, err := os.CreateTemp("", "binlog-empty-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	writeBinlogHeader(f)
	f.Close()

	r := NewBinlogReader()
	err = r.Open(f.Name())
	require.NoError(t, err)

	// Reading should return EOF immediately since there are no events.
	_, _, err = r.ReadEvent()
	assert.ErrorIs(t, err, io.EOF)

	r.Close()
}

func TestBinlogReader_ReadEvent_TruncatedBody(t *testing.T) {
	f, err := os.CreateTemp("", "binlog-truncated-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	writeBinlogHeader(f)

	// Write a header that claims a body larger than the file.
	headerBuf := make([]byte, EventHeaderSize)
	binary.LittleEndian.PutUint32(headerBuf[0:4], 1000)          // timestamp
	headerBuf[4] = byte(QueryEvent)                               // type
	binary.LittleEndian.PutUint32(headerBuf[5:9], 1)              // server_id
	binary.LittleEndian.PutUint32(headerBuf[9:13], EventHeaderSize+100) // EventLen = 19 + 100
	binary.LittleEndian.PutUint32(headerBuf[13:17], 4+EventHeaderSize+100) // NextPos
	binary.LittleEndian.PutUint16(headerBuf[17:19], 0)            // flags

	// Write only the header and a few bytes — less than the claimed 100.
	f.Write(headerBuf)
	f.Write([]byte{0, 1, 2}) // only 3 bytes of body
	f.Close()

	r := NewBinlogReader()
	err = r.Open(f.Name())
	require.NoError(t, err)

	_, _, err = r.ReadEvent()
	assert.Error(t, err)
	assert.NotErrorIs(t, err, io.EOF) // should be a read error, not clean EOF

	r.Close()
}

func TestBinlogReader_ReadEvent_CRC32Mismatch(t *testing.T) {
	f, err := os.CreateTemp("", "binlog-crcbad-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	writeBinlogHeader(f)

	// Write a FormatDescriptionEvent without CRC first (since parse will set checksumCRC).
	fdeBody := buildFormatDescBody(4, "8.0.33\x00", []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	fdeNext := uint32(4 + EventHeaderSize + len(fdeBody) + 4) // +4 for CRC
	fdeHdr := EventHeader{
		Timestamp: 1000,
		Type:      FormatDescriptionEvent,
		ServerID:  1,
		NextPos:   fdeNext,
	}
	// Write FDE with CRC (for the FDE itself the CRC is correct).
	writeEvent(f, fdeHdr, fdeBody, true)

	// Write an event with correct body but WRONG CRC.
	qBody := []byte("DELETE FROM t1")
	qNext := fdeNext + uint32(EventHeaderSize+len(qBody)+4)
	qHdr := EventHeader{
		Timestamp: 1001,
		Type:      QueryEvent,
		ServerID:  1,
		NextPos:   qNext,
	}

	// Write event header.
	buf := make([]byte, EventHeaderSize)
	binary.LittleEndian.PutUint32(buf[0:4], qHdr.Timestamp)
	buf[4] = byte(qHdr.Type)
	binary.LittleEndian.PutUint32(buf[5:9], qHdr.ServerID)
	totalLen := uint32(EventHeaderSize + len(qBody) + 4)
	qHdr.EventLen = totalLen
	binary.LittleEndian.PutUint32(buf[9:13], qHdr.EventLen)
	binary.LittleEndian.PutUint32(buf[13:17], qHdr.NextPos)
	binary.LittleEndian.PutUint16(buf[17:19], qHdr.Flags)
	f.Write(buf)
	f.Write(qBody)
	// Write a deliberately wrong CRC.
	wrongCRC := make([]byte, 4)
	binary.LittleEndian.PutUint32(wrongCRC, 0xDEADBEEF)
	f.Write(wrongCRC)
	f.Close()

	r := NewBinlogReader()
	err = r.Open(f.Name())
	require.NoError(t, err)

	// Read FDE first (CRC is enabled by the FDE).
	hdr, _, err := r.ReadEvent()
	require.NoError(t, err)
	assert.Equal(t, FormatDescriptionEvent, hdr.Type)

	// Enable CRC for subsequent reads.
	r.EnableChecksum()

	// Attempt to read the query event with bad CRC.
	_, _, err = r.ReadEvent()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CRC32 mismatch")

	r.Close()
}

func TestBinlogReader_ReadEvent_UnknownEventType(t *testing.T) {
	f, err := os.CreateTemp("", "binlog-unknown-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	writeBinlogHeader(f)

	// Write an event with type code 99 (unknown/unused).
	body := []byte("some payload")
	nextPos := uint32(4 + EventHeaderSize + len(body))
	hdr := EventHeader{
		Timestamp: 2000,
		Type:      EventType(99), // unknown type
		ServerID:  1,
		NextPos:   nextPos,
	}
	writeEvent(f, hdr, body, false)
	f.Close()

	r := NewBinlogReader()
	err = r.Open(f.Name())
	require.NoError(t, err)

	gotHdr, payload, err := r.ReadEvent()
	require.NoError(t, err)
	assert.Equal(t, EventType(99), gotHdr.Type)
	assert.Equal(t, "EVENT(99)", gotHdr.Type.String())
	assert.Equal(t, body, payload)
	assert.Equal(t, nextPos, r.Position())

	r.Close()
}

func TestBinlogReader_ReadEvent_InvalidEventLen(t *testing.T) {
	f, err := os.CreateTemp("", "binlog-badlen-*.bin")
	require.NoError(t, err)
	defer os.Remove(f.Name())

	writeBinlogHeader(f)

	// Write a header with EventLen < EventHeaderSize.
	headerBuf := make([]byte, EventHeaderSize)
	binary.LittleEndian.PutUint32(headerBuf[0:4], 1000)
	headerBuf[4] = byte(QueryEvent)
	binary.LittleEndian.PutUint32(headerBuf[5:9], 1)
	binary.LittleEndian.PutUint32(headerBuf[9:13], 10)          // EventLen = 10 (< 19)
	binary.LittleEndian.PutUint32(headerBuf[13:17], 4+10)
	binary.LittleEndian.PutUint16(headerBuf[17:19], 0)
	f.Write(headerBuf)
	f.Close()

	r := NewBinlogReader()
	err = r.Open(f.Name())
	require.NoError(t, err)

	_, _, err = r.ReadEvent()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid length")

	r.Close()
}
