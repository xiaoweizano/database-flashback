package parser

import (
	"fmt"
	"strings"
	"time"
)

// FormatColumnValue encodes any Go value produced by the binlog parser
// into a SQL-safe string representation.
//
// Supported types and their output formats:
//
//		int64             → decimal number (e.g. "42", "-1")
//		float64           → floating point ("%g")   (e.g. "3.14")
//		string            → single-quoted with '' escaping  (e.g. "'hello'", "'it''s'")
//		[]byte (printable) → single-quoted string            (e.g. "'hello'")
//		[]byte (binary)   → hex literal X'...'              (e.g. "X'1A2B'")
//		nil               → "NULL"
//		time.Time         → single-quoted datetime literal   (e.g. "'2023-01-15 10:30:00'")
//		other             → single-quoted "%v" fallback
func FormatColumnValue(val interface{}) (string, error) {
	switch v := val.(type) {
	case nil:
		return "NULL", nil

	case int64:
		return fmt.Sprintf("%d", v), nil

	case float64:
		return fmt.Sprintf("%g", v), nil

	case string:
		return "'" + escapeString(v) + "'", nil

	case []byte:
		if isPrintableBinary(v) {
			return "'" + escapeString(string(v)) + "'", nil
		}
		// Binary data: hex encode
		return "X'" + hexEncode(v) + "'", nil

	case time.Time:
		return "'" + v.Format("2006-01-02 15:04:05") + "'", nil

	default:
		// Fallback: wrap %v in quotes
		return "'" + escapeString(fmt.Sprintf("%v", v)) + "'", nil
	}
}

// escapeString escapes a string for safe inclusion in a single-quoted SQL string.
// Single quotes are doubled, backslashes are NOT escaped (standard SQL mode).
func escapeString(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

// isPrintableBinary reports whether every byte in b is a printable ASCII
// character (0x20–0x7E) or a common whitespace byte (tab, newline, carriage
// return).  If so the data can be treated as a text string rather than hex.
func isPrintableBinary(b []byte) bool {
	for _, c := range b {
		if c >= 0x20 && c <= 0x7E {
			continue
		}
		if c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		return false
	}
	return true
}

// hexEncode returns the upper-case hex representation of data.
func hexEncode(data []byte) string {
	return fmt.Sprintf("%X", data)
}
