package parser

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

const (
	// BinlogMagic is the 4-byte magic number at the start of every binlog file.
	BinlogMagic = "\xfe\x62\x69\x6e"
)

// BinlogReader reads and validates MySQL binlog files.
// It supports sequential event reading with optional CRC32 checksum verification
// and can follow binlog rotation across multiple files.
type BinlogReader struct {
	file        *os.File
	path        string
	position    uint32
	checksumCRC bool // true when FormatDescriptionEvent enables CRC verification
	crcTable    *crc32.Table
}

// NewBinlogReader creates a new BinlogReader.
func NewBinlogReader() *BinlogReader {
	return &BinlogReader{
		crcTable: crc32.MakeTable(crc32.IEEE),
	}
}

// Open opens a binlog file and validates its magic number.
func (r *BinlogReader) Open(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open binlog %s: %w", path, err)
	}
	r.file = f
	r.path = path
	r.position = 0

	// Validate magic number
	magic := make([]byte, 4)
	if _, err := io.ReadFull(f, magic); err != nil {
		r.file.Close()
		r.file = nil
		return fmt.Errorf("read binlog magic: %w", err)
	}
	if string(magic) != BinlogMagic {
		r.file.Close()
		r.file = nil
		return fmt.Errorf("invalid binlog magic: got %x, expected fe62696e", magic)
	}
	r.position = 4
	return nil
}

// Close closes the current binlog file.
func (r *BinlogReader) Close() error {
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	r.path = ""
	r.position = 0
	return err
}

// Position returns the current read position within the binlog file.
func (r *BinlogReader) Position() uint32 {
	return r.position
}

// Seek seeks to the given position within the binlog file.
func (r *BinlogReader) Seek(pos uint32) error {
	if r.file == nil {
		return fmt.Errorf("binlog not open")
	}
	_, err := r.file.Seek(int64(pos), io.SeekStart)
	if err != nil {
		return fmt.Errorf("seek to %d: %w", pos, err)
	}
	r.position = pos
	return nil
}

// ReadEvent reads the next event from the binlog file.
// Returns the parsed EventHeader and the raw event payload (everything after the
// 19-byte header, excluding the trailing CRC32 checksum if present).
//
// When r.checksumCRC is true, the last 4 bytes of the event are the CRC32 of the
// entire event (header + body), and the returned payload excludes those 4 bytes.
func (r *BinlogReader) ReadEvent() (*EventHeader, []byte, error) {
	if r.file == nil {
		return nil, nil, fmt.Errorf("binlog not open")
	}

	// Read the 19-byte header
	headerBuf := make([]byte, EventHeaderSize)
	if _, err := io.ReadFull(r.file, headerBuf); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, nil, io.EOF
		}
		return nil, nil, fmt.Errorf("read event header at position %d: %w", r.position, err)
	}

	hdr, err := ParseEventHeader(headerBuf)
	if err != nil {
		return nil, nil, fmt.Errorf("parse event header: %w", err)
	}

	bodyLen := hdr.EventLen - EventHeaderSize
	if hdr.EventLen < EventHeaderSize {
		return nil, nil, fmt.Errorf("event at position %d has invalid length %d < %d",
			r.position, hdr.EventLen, EventHeaderSize)
	}

	// Read body
	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(r.file, body); err != nil {
		return nil, nil, fmt.Errorf("read event body at position %d: %w", r.position, err)
	}

	// CRC verification
	var payload []byte
	if r.checksumCRC {
		if bodyLen < 4 {
			return nil, nil, fmt.Errorf("event too short for CRC32: length %d", bodyLen)
		}
		// Last 4 bytes are CRC32 checksum
		storedCRC := binary.LittleEndian.Uint32(body[bodyLen-4:])
		body = body[:bodyLen-4]

		// CRC covers the entire event: header + body (excluding CRC itself)
		crcData := append(headerBuf, body...)
		computedCRC := crc32.Checksum(crcData, r.crcTable)
		if computedCRC != storedCRC {
			return nil, nil, fmt.Errorf(
				"CRC32 mismatch at position %d: computed 0x%08X, stored 0x%08X",
				r.position, computedCRC, storedCRC,
			)
		}
		payload = body
	} else {
		payload = body
	}

	// Update position to next event
	r.position = hdr.NextPos

	return &hdr, payload, nil
}

// EnableChecksum enables CRC32 verification for subsequent events.
// This should be called after parsing FormatDescriptionEvent if checksums are enabled.
func (r *BinlogReader) EnableChecksum() {
	r.checksumCRC = true
}

// SetPosition updates the current position. Used during rotation.
func (r *BinlogReader) SetPosition(pos uint32) {
	r.position = pos
}

// Path returns the current binlog file path.
func (r *BinlogReader) Path() string {
	return r.path
}

// File returns the underlying file (or nil).
// Only used for testing.
func (r *BinlogReader) File() *os.File {
	return r.file
}

// IsOpen returns true if a binlog file is currently open.
func (r *BinlogReader) IsOpen() bool {
	return r.file != nil
}
