package parser

import (
	"testing"
	"time"
)

func TestFormatColumnValue_Nil(t *testing.T) {
	got, err := FormatColumnValue(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "NULL" {
		t.Errorf("expected NULL, got %q", got)
	}
}

func TestFormatColumnValue_Int64(t *testing.T) {
	tests := []struct {
		val  int64
		want string
	}{
		{0, "0"},
		{42, "42"},
		{-1, "-1"},
		{9223372036854775807, "9223372036854775807"},
		{-9223372036854775808, "-9223372036854775808"},
	}
	for _, tt := range tests {
		got, err := FormatColumnValue(tt.val)
		if err != nil {
			t.Errorf("FormatColumnValue(%d): unexpected error: %v", tt.val, err)
			continue
		}
		if got != tt.want {
			t.Errorf("FormatColumnValue(%d) = %q, want %q", tt.val, got, tt.want)
		}
	}
}

func TestFormatColumnValue_Float64(t *testing.T) {
	tests := []struct {
		val  float64
		want string
	}{
		{0.0, "0"},
		{3.14, "3.14"},
		{-2.5, "-2.5"},
		{1e10, "1e+10"},
		{1.23456789, "1.23456789"},
	}
	for _, tt := range tests {
		got, err := FormatColumnValue(tt.val)
		if err != nil {
			t.Errorf("FormatColumnValue(%v): unexpected error: %v", tt.val, err)
			continue
		}
		if got != tt.want {
			t.Errorf("FormatColumnValue(%v) = %q, want %q", tt.val, got, tt.want)
		}
	}
}

func TestFormatColumnValue_String(t *testing.T) {
	tests := []struct {
		val  string
		want string
	}{
		{"hello", "'hello'"},
		{"", "''"},
		{"it's", "'it''s'"},
		{"a'b'c", "'a''b''c'"},
		{"simple", "'simple'"},
		{"with`backtick", "'with`backtick'"},
	}
	for _, tt := range tests {
		got, err := FormatColumnValue(tt.val)
		if err != nil {
			t.Errorf("FormatColumnValue(%q): unexpected error: %v", tt.val, err)
			continue
		}
		if got != tt.want {
			t.Errorf("FormatColumnValue(%q) = %q, want %q", tt.val, got, tt.want)
		}
	}
}

func TestFormatColumnValue_ByteSlice(t *testing.T) {
	tests := []struct {
		name string
		val  []byte
		want string
	}{
		{
			name: "printable ASCII",
			val:  []byte("hello"),
			want: "'hello'",
		},
		{
			name: "printable with quotes",
			val:  []byte("it's"),
			want: "'it''s'",
		},
		{
			name: "binary data -> hex",
			val:  []byte{0x1A, 0x2B, 0x3C, 0xFF},
			want: "X'1A2B3CFF'",
		},
		{
			name: "mixed null bytes -> hex",
			val:  []byte{0x00, 0x41, 0x00},
			want: "X'004100'",
		},
		{
			name: "empty slice -> empty string",
			val:  []byte{},
			want: "''",
		},
		{
			name: "tab and newline -> printable",
			val:  []byte("line1\nline2\tindented"),
			want: "'line1\nline2\tindented'",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := FormatColumnValue(tt.val)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("FormatColumnValue(%v) = %q, want %q", tt.val, got, tt.want)
			}
		})
	}
}

func TestFormatColumnValue_Time(t *testing.T) {
	val := time.Date(2023, 1, 15, 10, 30, 0, 0, time.UTC)
	got, err := FormatColumnValue(val)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "'2023-01-15 10:30:00'"
	if got != want {
		t.Errorf("FormatColumnValue(time) = %q, want %q", got, want)
	}
}

func TestFormatColumnValue_UnknownType(t *testing.T) {
	val := 42 // int, not int64
	got, err := FormatColumnValue(val)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "'42'"
	if got != want {
		t.Errorf("FormatColumnValue(int) = %q, want %q", got, want)
	}
}

func TestFormatColumnValue_Bool(t *testing.T) {
	got, err := FormatColumnValue(true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "'true'"
	if got != want {
		t.Errorf("FormatColumnValue(true) = %q, want %q", got, want)
	}
}

func TestEscapeString(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"", ""},
		{"it's", "it''s"},
		{"'hello'", "''hello''"},
		{"no quotes here", "no quotes here"},
		{"multiple'quote's", "multiple''quote''s"},
	}
	for _, tt := range tests {
		got := escapeString(tt.input)
		if got != tt.want {
			t.Errorf("escapeString(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestIsPrintableBinary(t *testing.T) {
	tests := []struct {
		name  string
		data  []byte
		print bool
	}{
		{"printable text", []byte("hello world"), true},
		{"with newline", []byte("line1\nline2"), true},
		{"with tab", []byte("col1\tcol2"), true},
		{"with carriage return", []byte("line1\r\nline2"), true},
		{"binary null byte", []byte{0x00, 0x41}, false},
		{"binary high bit", []byte{0xFF, 0xFE}, false},
		{"control char", []byte{0x01, 0x02}, false},
		{"empty", []byte{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isPrintableBinary(tt.data)
			if got != tt.print {
				t.Errorf("isPrintableBinary(%v) = %v, want %v", tt.data, got, tt.print)
			}
		})
	}
}

func TestHexEncode(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"simple", []byte{0x1A, 0x2B, 0x3C}, "1A2B3C"},
		{"empty", []byte{}, ""},
		{"all zeros", []byte{0x00, 0x00}, "0000"},
		{"max bytes", []byte{0xFF, 0xFE, 0xFD}, "FFFEFD"},
		{"text as hex", []byte("hello"), "68656C6C6F"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hexEncode(tt.data)
			if got != tt.want {
				t.Errorf("hexEncode(%v) = %q, want %q", tt.data, got, tt.want)
			}
		})
	}
}
