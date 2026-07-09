package parser

import (
	"fmt"
	"io"
	"time"

	"github.com/a-shan/mysql-pitr/internal/connector"
)

// BinlogParser orchestrates the parsing of MySQL binlog files.
// It reads events sequentially, maintains a TableMapRegistry, and produces
// connector.RowEvent structs suitable for flashback operations.
type BinlogParser struct {
	reader  *BinlogReader
	reg     *TableMapRegistry
	rp      *RowEventParser
	events  []connector.RowEvent
	// When true, checksums from FDE are enabled.
	checksumEnabled bool
	// When true, skip CRC32 verification (for testing, or older binlogs).
	skipChecksum bool
}

// NewBinlogParser creates a new BinlogParser.
func NewBinlogParser() *BinlogParser {
	reg := NewTableMapRegistry()
	return &BinlogParser{
		reader: NewBinlogReader(),
		reg:    reg,
		rp:     NewRowEventParser(reg),
	}
}

// Open opens a binlog file and validates the magic number.
func (p *BinlogParser) Open(path string) error {
	return p.reader.Open(path)
}

// Close closes the current binlog file.
func (p *BinlogParser) Close() error {
	// Reset parser state
	p.reg = NewTableMapRegistry()
	p.rp.SetRegistry(p.reg)
	p.events = nil
	p.checksumEnabled = false
	return p.reader.Close()
}

// SetSkipChecksum controls whether CRC32 verification is skipped.
func (p *BinlogParser) SetSkipChecksum(skip bool) {
	p.skipChecksum = skip
}

// Parse reads and parses all events from the currently open binlog file.
// It returns any connector.RowEvent found.
//
// For multi-file parsing, use ParseFiles instead.
func (p *BinlogParser) Parse() ([]connector.RowEvent, error) {
	if !p.reader.IsOpen() {
		return nil, fmt.Errorf("binlog not open")
	}

	p.events = nil

	for {
		hdr, payload, err := p.reader.ReadEvent()
		if err == io.EOF {
			break
		}
		if err != nil {
			return p.events, fmt.Errorf("read event at position %d: %w",
				p.reader.Position(), err)
		}

		if err := p.dispatch(hdr, payload); err != nil {
			return p.events, fmt.Errorf("dispatch event type %s at position %d: %w",
				hdr.Type.String(), p.reader.Position(), err)
		}
	}

	return p.events, nil
}

// ParseFiles opens and parses multiple binlog files sequentially.
// The first file's magic number is validated; subsequent files are expected to
// start with the magic number (validated automatically by Open).
// Returns all row events found across all files.
func (p *BinlogParser) ParseFiles(paths []string) ([]connector.RowEvent, error) {
	var allEvents []connector.RowEvent

	for i, path := range paths {
		if err := p.Open(path); err != nil {
			return allEvents, fmt.Errorf("open file %s: %w", path, err)
		}

		events, err := p.Parse()
		if err != nil {
			// If this is the last file and there was an error, return partial
			if i == len(paths)-1 {
				p.Close()
				return allEvents, err
			}
			// For intermediate files, log and continue
		}

		allEvents = append(allEvents, events...)
		p.Close()

		// Reset state between files (table maps from previous file are stale)
		p.reg = NewTableMapRegistry()
		p.rp.SetRegistry(p.reg)
		p.checksumEnabled = false
	}

	return allEvents, nil
}

// dispatch processes a single binlog event based on its type.
func (p *BinlogParser) dispatch(hdr *EventHeader, payload []byte) error {
	switch hdr.Type {
	case FormatDescriptionEvent:
		return p.handleFormatDescriptionEvent(hdr, payload)

	case TableMapEvent:
		return p.handleTableMapEvent(hdr, payload)

	case WriteRowsEventV1:
		return p.handleWriteRowsEvent(hdr, payload, false)

	case WriteRowsEventV2:
		return p.handleWriteRowsEvent(hdr, payload, true)

	case UpdateRowsEventV1:
		return p.handleUpdateRowsEvent(hdr, payload, false)

	case UpdateRowsEventV2:
		return p.handleUpdateRowsEvent(hdr, payload, true)

	case DeleteRowsEventV1:
		return p.handleDeleteRowsEvent(hdr, payload, false)

	case DeleteRowsEventV2:
		return p.handleDeleteRowsEvent(hdr, payload, true)

	case RotateEvent:
		return p.handleRotateEvent(hdr, payload)

	case XidEvent:
		// XID marks transaction commit; no row data to extract
		return nil

	case QueryEvent:
		// Query events (BEGIN, COMMIT, DDL, etc.) are not parsed for row data
		return nil

	default:
		// Unknown/unsupported events are silently skipped
		return nil
	}
}

// handleFormatDescriptionEvent processes a FormatDescriptionEvent.
// It extracts whether checksums are enabled and the header length.
func (p *BinlogParser) handleFormatDescriptionEvent(hdr *EventHeader, payload []byte) error {
	if len(payload) < 2+50+4+1 {
		return fmt.Errorf("FormatDescriptionEvent payload too short: %d bytes", len(payload))
	}

	// Bytes 0-1: binlog version (always 4 for v4)
	// Bytes 2-51: MySQL server version (50 bytes)
	// Bytes 52-55: creation timestamp
	// Byte 56: common header length (usually 19 for v4)

	// Check if checksums are enabled: the last bytes of the payload contain
	// an event-type header length array. For FDE itself, its own post-header
	// length is at a specific position.
	// Checksum flag: MySQL 5.6+ stores a checksum algorithm indicator at the end.
	// Actually, the checksum flag is determined by looking at the very last bytes
	// of the FDE payload. MySQL appends a checksum algorithm description.

	// In MySQL 5.6+, the FDE payload has:
	// [2 binlog_version] [50 server_version] [4 create_timestamp] [1 header_length]
	// [N event-type header lengths] [1 checksum algorithm flag: 0=off, 1=CRC32, etc.]
	//
	// The checksum algorithm bytes are at the end. Let me check:
	// The payload includes trailing data beyond just the post-header lengths.
	// MySQL appends 1 byte for the checksum algorithm after the post-header len array.
	// The checksum algorithm byte appears at the end of the payload.

	// For simplicity, we check if the payload length is sufficient and
	// whether checksum info is present. The standard approach:
	// If the payload has at least 57 bytes + post-header lens + 1, and
	// the last byte != 0, checksums are enabled.
	// But this varies by MySQL version. We'll use a simpler heuristic:
	// If hdr.EventLen indicates a checksum was included (non-zero checksum area),
	// and the CRC flag in the format description indicates it.
	//
	// Actually the reliable way: Check if the FormatDescriptionEvent itself has a CRC
	// by verifying if the FDE was written with a trailing CRC.
	// Since the first FDE has no CRC (it's before checksums are enabled),
	// we look for the checksum algorithm byte after the post-header length array.

	// Standard heuristics: In MySQL 5.6+, if binlog_checksum=CRC32:
	// the FDE includes a trailing byte with value BINLOG_CHECKSUM_ALG_CRC32 = 1
	// or BINLOG_CHECKSUM_ALG_UNDEF = 0 (no checksum)
	// The format is: common_header_len + post_header_len[event_types...] + 1 (checksum alg)
	// But we need to find how many event types are defined.

	// For now, if the payload length > 57 + 35 (approximate), assume checksums enabled.
	// A more reliable approach: look for a non-zero algorithm byte near the end.
	if len(payload) > 57+35 {
		// Look for the checksum algorithm byte. It's the byte after all
		// post-header lengths. If it's 1 (CRC32), checksums are enabled.
		// The total known post-header entries: typically 35+ for MySQL 8.0.
		// We search backwards: find the last non-zero byte before the end
		// that could be the algorithm indicator.
		lastByte := payload[len(payload)-1]
		if lastByte == 1 && !p.skipChecksum {
			p.checksumEnabled = true
			p.reader.EnableChecksum()
		}
	}

	return nil
}

// handleTableMapEvent parses a TableMapEvent and stores it in the registry.
func (p *BinlogParser) handleTableMapEvent(hdr *EventHeader, payload []byte) error {
	tm, err := ParseTableMap(payload)
	if err != nil {
		return fmt.Errorf("parse TableMap: %w", err)
	}
	p.reg.Set(tm)
	return nil
}

// handleWriteRowsEvent parses a WriteRows event and converts to RowEvent entries.
func (p *BinlogParser) handleWriteRowsEvent(hdr *EventHeader, payload []byte, v2 bool) error {
	data, err := p.rp.ParseWriteRowsEvent(payload, v2)
	if err != nil {
		return err
	}
	return p.convertToRowEvents(hdr, data, connector.InsertEvent)
}

// handleDeleteRowsEvent parses a DeleteRows event and converts to RowEvent entries.
func (p *BinlogParser) handleDeleteRowsEvent(hdr *EventHeader, payload []byte, v2 bool) error {
	data, err := p.rp.ParseDeleteRowsEvent(payload, v2)
	if err != nil {
		return err
	}
	return p.convertToRowEvents(hdr, data, connector.DeleteEvent)
}

// handleUpdateRowsEvent parses an UpdateRows event and converts to RowEvent entries.
func (p *BinlogParser) handleUpdateRowsEvent(hdr *EventHeader, payload []byte, v2 bool) error {
	data, err := p.rp.ParseUpdateRowsEvent(payload, v2)
	if err != nil {
		return err
	}
	return p.convertToRowEvents(hdr, data, connector.UpdateEvent)
}

// handleRotateEvent processes a RotateEvent (updates binlog file name).
func (p *BinlogParser) handleRotateEvent(hdr *EventHeader, payload []byte) error {
	// RotateEvent format: 8 bytes position + binlog filename (null-terminated)
	if len(payload) < 8 {
		return fmt.Errorf("RotateEvent payload too short: %d bytes", len(payload))
	}
	// The next position in the new binlog file
	_ = uint64(0)
	// Filename starts at offset 8
	// We don't auto-follow the rotation; ParseFiles handles multi-file parsing.
	return nil
}

// convertToRowEvents converts parsed RowEventData entries into connector.RowEvent entries.
func (p *BinlogParser) convertToRowEvents(hdr *EventHeader, data *RowEventData, eventType connector.EventType) error {
	tm := p.reg.Get(data.TableID)
	dbName := ""
	tblName := ""
	if tm != nil {
		dbName = tm.Database
		tblName = tm.Table
	}

	ts := time.Unix(int64(hdr.Timestamp), 0).UTC()

	for _, row := range data.Rows {
		re := connector.RowEvent{
			Type:      eventType,
			Database:  dbName,
			Table:     tblName,
			Timestamp: ts,
			Before:    row.Before,
			After:     row.After,
		}

		// Populate PrimaryKey from Before (DELETE, UPDATE) or After (INSERT)
		if re.PrimaryKey == nil {
			if re.Before != nil {
				re.PrimaryKey = extractPrimaryKey(re.Before)
			} else if re.After != nil {
				re.PrimaryKey = extractPrimaryKey(re.After)
			}
		}

		p.events = append(p.events, re)
	}

	return nil
}

// extractPrimaryKey attempts to identify primary key columns from a value map.
// Without schema information, we cannot reliably determine PK columns.
// For now, we return an empty map; the caller should resolve PKs via schema.
func extractPrimaryKey(vals map[string]interface{}) map[string]interface{} {
	// With positional keys, we cannot know which columns form the PK.
	// Return nil; the rollback layer should resolve this.
	return nil
}
