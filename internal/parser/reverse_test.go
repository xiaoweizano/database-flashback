package parser

import (
	"testing"

	"github.com/a-shan/mysql-pitr/internal/connector"
)

// ---------------------------------------------------------------------------
// INSERT → DELETE
// ---------------------------------------------------------------------------

func TestReverseSQL_Insert_WithPK(t *testing.T) {
	event := connector.RowEvent{
		Type:   connector.InsertEvent,
		Table:  "users",
		Before: nil,
		After: map[string]interface{}{
			"id":   int64(42),
			"name": "Alice",
			"age":  int64(30),
		},
	}

	got, err := ReverseSQL(event, []string{"id"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "DELETE FROM `users` WHERE `id` = 42 LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(insert+pk) = %q, want %q", got, want)
	}
}

func TestReverseSQL_Insert_WithoutPK(t *testing.T) {
	event := connector.RowEvent{
		Type:  connector.InsertEvent,
		Table: "users",
		After: map[string]interface{}{
			"id":   int64(42),
			"name": "Alice",
		},
	}

	got, err := ReverseSQL(event, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "DELETE FROM `users` WHERE `id` = 42 AND `name` = 'Alice' LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(insert+no-pk) = %q, want %q", got, want)
	}
}

func TestReverseSQL_Insert_WithMultiplePKColumns(t *testing.T) {
	event := connector.RowEvent{
		Type:  connector.InsertEvent,
		Table: "order_items",
		After: map[string]interface{}{
			"order_id": int64(1001),
			"item_id":  int64(5),
			"sku":      "ABC-123",
			"qty":      int64(2),
		},
	}

	got, err := ReverseSQL(event, []string{"order_id", "item_id"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "DELETE FROM `order_items` WHERE `order_id` = 1001 AND `item_id` = 5 LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(insert+multi-pk) = %q, want %q", got, want)
	}
}

func TestReverseSQL_Insert_NullValues(t *testing.T) {
	event := connector.RowEvent{
		Type:  connector.InsertEvent,
		Table: "contacts",
		After: map[string]interface{}{
			"id":    int64(1),
			"email": nil,
			"phone": nil,
		},
	}

	got, err := ReverseSQL(event, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "DELETE FROM `contacts` WHERE `email` IS NULL AND `id` = 1 AND `phone` IS NULL LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(insert+null) = %q, want %q", got, want)
	}
}

func TestReverseSQL_Insert_BinaryColumn(t *testing.T) {
	event := connector.RowEvent{
		Type:  connector.InsertEvent,
		Table: "assets",
		After: map[string]interface{}{
			"id":   int64(1),
			"data": []byte{0x89, 0x50, 0x4E, 0x47}, // PNG header
		},
	}

	got, err := ReverseSQL(event, []string{"id"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "DELETE FROM `assets` WHERE `id` = 1 LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(insert+binary) = %q, want %q", got, want)
	}
}

func TestReverseSQL_Insert_EmptyString(t *testing.T) {
	event := connector.RowEvent{
		Type:  connector.InsertEvent,
		Table: "users",
		After: map[string]interface{}{
			"id":   int64(1),
			"note": "",
		},
	}

	got, err := ReverseSQL(event, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "DELETE FROM `users` WHERE `id` = 1 AND `note` = '' LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(insert+empty) = %q, want %q", got, want)
	}
}

func TestReverseSQL_Insert_SpecialChars(t *testing.T) {
	event := connector.RowEvent{
		Type:  connector.InsertEvent,
		Table: "items",
		After: map[string]interface{}{
			"id":   int64(1),
			"name": "it's a \"test\"",
		},
	}

	got, err := ReverseSQL(event, []string{"id"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "DELETE FROM `items` WHERE `id` = 1 LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(insert+special) = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// DELETE → INSERT
// ---------------------------------------------------------------------------

func TestReverseSQL_Delete(t *testing.T) {
	event := connector.RowEvent{
		Type:   connector.DeleteEvent,
		Table:  "users",
		After:  nil,
		Before: map[string]interface{}{
			"id":   int64(42),
			"name": "Alice",
			"age":  int64(30),
		},
	}

	got, err := ReverseSQL(event, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "INSERT INTO `users` (`age`, `id`, `name`) VALUES (30, 42, 'Alice');"
	if got != want {
		t.Errorf("ReverseSQL(delete) = %q, want %q", got, want)
	}
}

func TestReverseSQL_Delete_BinaryColumn(t *testing.T) {
	event := connector.RowEvent{
		Type:   connector.DeleteEvent,
		Table:  "docs",
		Before: map[string]interface{}{
			"id":   int64(1),
			"blob": []byte{0x00, 0xFF, 0xAB},
		},
	}

	got, err := ReverseSQL(event, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The INSERT should hex-encode the binary data
	want := "INSERT INTO `docs` (`blob`, `id`) VALUES (X'00FFAB', 1);"
	if got != want {
		t.Errorf("ReverseSQL(delete+binary) = %q, want %q", got, want)
	}
}

func TestReverseSQL_Delete_NullValues(t *testing.T) {
	event := connector.RowEvent{
		Type:   connector.DeleteEvent,
		Table:  "contacts",
		Before: map[string]interface{}{
			"id":    int64(1),
			"email": nil,
			"phone": nil,
		},
	}

	got, err := ReverseSQL(event, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "INSERT INTO `contacts` (`email`, `id`, `phone`) VALUES (NULL, 1, NULL);"
	if got != want {
		t.Errorf("ReverseSQL(delete+null) = %q, want %q", got, want)
	}
}

func TestReverseSQL_Delete_FloatColumn(t *testing.T) {
	event := connector.RowEvent{
		Type:   connector.DeleteEvent,
		Table:  "metrics",
		Before: map[string]interface{}{
			"id":    int64(1),
			"value": float64(3.14),
		},
	}

	got, err := ReverseSQL(event, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "INSERT INTO `metrics` (`id`, `value`) VALUES (1, 3.14);"
	if got != want {
		t.Errorf("ReverseSQL(delete+float) = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// UPDATE → UPDATE (restore before-image)
// ---------------------------------------------------------------------------

func TestReverseSQL_Update_WithPK(t *testing.T) {
	event := connector.RowEvent{
		Type: connector.UpdateEvent,
		Table: "users",
		Before: map[string]interface{}{
			"id":   int64(42),
			"name": "Alice",
			"age":  int64(29),
		},
		After: map[string]interface{}{
			"id":   int64(42),
			"name": "Alice",
			"age":  int64(30),
		},
	}

	got, err := ReverseSQL(event, []string{"id"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "UPDATE `users` SET `age` = 29, `id` = 42, `name` = 'Alice' WHERE `id` = 42 LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(update+pk) = %q, want %q", got, want)
	}
}

func TestReverseSQL_Update_AllColumnMatch(t *testing.T) {
	event := connector.RowEvent{
		Type: connector.UpdateEvent,
		Table: "users",
		Before: map[string]interface{}{
			"id":   int64(42),
			"name": "Alice",
			"age":  int64(29),
		},
		After: map[string]interface{}{
			"id":   int64(42),
			"name": "Alice",
			"age":  int64(30),
		},
	}

	got, err := ReverseSQL(event, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "UPDATE `users` SET `age` = 29, `id` = 42, `name` = 'Alice' WHERE `age` = 30 AND `id` = 42 AND `name` = 'Alice' LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(update+all) = %q, want %q", got, want)
	}
}

func TestReverseSQL_Update_NullAfter(t *testing.T) {
	event := connector.RowEvent{
		Type: connector.UpdateEvent,
		Table: "users",
		Before: map[string]interface{}{
			"id":   int64(1),
			"name": "Bob",
		},
		After: map[string]interface{}{
			"id":   int64(1),
			"name": nil,
		},
	}

	got, err := ReverseSQL(event, []string{"id"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "UPDATE `users` SET `id` = 1, `name` = 'Bob' WHERE `id` = 1 LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(update+null-after) = %q, want %q", got, want)
	}
}

func TestReverseSQL_Update_MultipleColumns(t *testing.T) {
	event := connector.RowEvent{
		Type: connector.UpdateEvent,
		Table: "inventory",
		Before: map[string]interface{}{
			"product_id": int64(10),
			"warehouse":  "NY",
			"quantity":   int64(100),
			"price":      float64(9.99),
		},
		After: map[string]interface{}{
			"product_id": int64(10),
			"warehouse":  "NY",
			"quantity":   int64(95),
			"price":      float64(9.99),
		},
	}

	got, err := ReverseSQL(event, []string{"product_id", "warehouse"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "UPDATE `inventory` SET `price` = 9.99, `product_id` = 10, `quantity` = 100, `warehouse` = 'NY' WHERE `product_id` = 10 AND `warehouse` = 'NY' LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(update+multi-pk) = %q, want %q", got, want)
	}
}

func TestReverseSQL_Update_SpecialChars(t *testing.T) {
	event := connector.RowEvent{
		Type: connector.UpdateEvent,
		Table: "items",
		Before: map[string]interface{}{
			"id":   int64(1),
			"desc": "old description",
		},
		After: map[string]interface{}{
			"id":   int64(1),
			"desc": "it's updated",
		},
	}

	got, err := ReverseSQL(event, []string{"id"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "UPDATE `items` SET `desc` = 'old description', `id` = 1 WHERE `id` = 1 LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(update+special) = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Batch
// ---------------------------------------------------------------------------

func TestReverseSQLBatch(t *testing.T) {
	events := []connector.RowEvent{
		{
			Type:   connector.InsertEvent,
			Table:  "users",
			After:  map[string]interface{}{"id": int64(1), "name": "Alice"},
		},
		{
			Type:   connector.InsertEvent,
			Table:  "users",
			After:  map[string]interface{}{"id": int64(2), "name": "Bob"},
		},
		{
			Type:   connector.DeleteEvent,
			Table:  "users",
			Before: map[string]interface{}{"id": int64(3), "name": "Carol"},
		},
	}

	got, err := ReverseSQLBatch(events, []string{"id"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(got))
	}

	wants := []string{
		"DELETE FROM `users` WHERE `id` = 1 LIMIT 1;",
		"DELETE FROM `users` WHERE `id` = 2 LIMIT 1;",
		"INSERT INTO `users` (`id`, `name`) VALUES (3, 'Carol');",
	}
	for i := range wants {
		if got[i] != wants[i] {
			t.Errorf("statement %d = %q, want %q", i, got[i], wants[i])
		}
	}
}

// ---------------------------------------------------------------------------
// Error cases
// ---------------------------------------------------------------------------

func TestReverseSQL_UnsupportedEventType(t *testing.T) {
	event := connector.RowEvent{
		Type: "TRUNCATE",
	}

	_, err := ReverseSQL(event, nil)
	if err == nil {
		t.Fatal("expected error for unsupported event type, got nil")
	}
}

func TestReverseSQL_Insert_MissingAfter(t *testing.T) {
	event := connector.RowEvent{
		Type:   connector.InsertEvent,
		Table:  "t",
		Before: map[string]interface{}{"id": int64(1)},
		After:  nil,
	}

	_, err := ReverseSQL(event, nil)
	if err == nil {
		t.Fatal("expected error for missing After image, got nil")
	}
}

func TestReverseSQL_Delete_MissingBefore(t *testing.T) {
	event := connector.RowEvent{
		Type:  connector.DeleteEvent,
		Table: "t",
		After: map[string]interface{}{"id": int64(1)},
	}

	_, err := ReverseSQL(event, nil)
	if err == nil {
		t.Fatal("expected error for missing Before image, got nil")
	}
}

func TestReverseSQL_Update_MissingBefore(t *testing.T) {
	event := connector.RowEvent{
		Type: connector.UpdateEvent,
		Table: "t",
		After: map[string]interface{}{"id": int64(1)},
	}

	_, err := ReverseSQL(event, nil)
	if err == nil {
		t.Fatal("expected error for missing Before image, got nil")
	}
}

func TestReverseSQL_Update_MissingAfter(t *testing.T) {
	event := connector.RowEvent{
		Type:   connector.UpdateEvent,
		Table:  "t",
		Before: map[string]interface{}{"id": int64(1)},
	}

	_, err := ReverseSQL(event, nil)
	if err == nil {
		t.Fatal("expected error for missing After image, got nil")
	}
}

func TestReverseSQL_EmptyWhereClause(t *testing.T) {
	event := connector.RowEvent{
		Type:  connector.InsertEvent,
		Table: "t",
		After: map[string]interface{}{},
	}

	_, err := ReverseSQL(event, nil)
	if err == nil {
		t.Fatal("expected error for empty WHERE clause, got nil")
	}
}

// ---------------------------------------------------------------------------
// Integration: All column types in one event
// ---------------------------------------------------------------------------

func TestReverseSQL_AllColumnTypes(t *testing.T) {
	event := connector.RowEvent{
		Type:   connector.InsertEvent,
		Table:  "all_types",
		Before: nil,
		After: map[string]interface{}{
			"tiny_int":   int64(127),
			"small_int":  int64(32767),
			"medium_int": int64(8388607),
			"int_col":    int64(2147483647),
			"big_int":    int64(9223372036854775807),
			"float_col":  float64(3.14),
			"double_col": float64(2.71828),
			"decimal_col": "123.45",
			"varchar":    "hello",
			"char_col":   "a",
			"date":       "2023-01-15",
			"datetime":   "2023-01-15 10:30:00",
			"timestamp":  "2023-01-15 10:30:00",
			"null_col":   nil,
			"binary_data": []byte{0xDE, 0xAD, 0xBE, 0xEF},
		},
	}

	got, err := ReverseSQL(event, []string{"int_col"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "DELETE FROM `all_types` WHERE `int_col` = 2147483647 LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(all-types) = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Verify that pkColumns not present in the image are silently skipped
// ---------------------------------------------------------------------------

func TestReverseSQL_PKColumnMissingFromImage(t *testing.T) {
	event := connector.RowEvent{
		Type:  connector.InsertEvent,
		Table: "t",
		After: map[string]interface{}{
			"id":   int64(1),
			"name": "x",
		},
	}

	// "unknown_col" is not in the After map; it should be skipped
	got, err := ReverseSQL(event, []string{"id", "unknown_col"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "DELETE FROM `t` WHERE `id` = 1 LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(skip-missing-pk) = %q, want %q", got, want)
	}
}

func TestReverseSQL_WithOnlyMissingPKColumn(t *testing.T) {
	event := connector.RowEvent{
		Type:  connector.InsertEvent,
		Table: "t",
		After: map[string]interface{}{
			"id": int64(1),
		},
	}

	// All PK columns are missing from the image; should fall through to the
	// "no columns available" error.
	_, err := ReverseSQL(event, []string{"missing_col"})
	if err == nil {
		t.Fatal("expected error when pk columns are all missing, got nil")
	}
}

// ---------------------------------------------------------------------------
// Binary data in all-column WHERE clause (no PK)
// ---------------------------------------------------------------------------

func TestReverseSQL_Insert_BinaryInWhereClause(t *testing.T) {
	event := connector.RowEvent{
		Type:  connector.InsertEvent,
		Table: "assets",
		After: map[string]interface{}{
			"id":   int64(1),
			"data": []byte{0x89, 0x50, 0x4E, 0x47},
		},
	}

	got, err := ReverseSQL(event, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	want := "DELETE FROM `assets` WHERE `data` = X'89504E47' AND `id` = 1 LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(insert+binary+where) = %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// Special characters in UPDATE SET clause
// ---------------------------------------------------------------------------

func TestReverseSQL_Update_SpecialCharsInSet(t *testing.T) {
	event := connector.RowEvent{
		Type: connector.UpdateEvent,
		Table: "items",
		Before: map[string]interface{}{
			"id":   int64(1),
			"desc": "it's the \"original\"",
		},
		After: map[string]interface{}{
			"id":   int64(1),
			"desc": "updated value",
		},
	}

	got, err := ReverseSQL(event, []string{"id"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The SET clause should escape the single quote in the before-image value
	want := "UPDATE `items` SET `desc` = 'it''s the \"original\"', `id` = 1 WHERE `id` = 1 LIMIT 1;"
	if got != want {
		t.Errorf("ReverseSQL(update+special+set) = %q, want %q", got, want)
	}
}

func TestReverseSQLBatch_ErrorPropagation(t *testing.T) {
	events := []connector.RowEvent{
		{
			Type:   connector.InsertEvent,
			Table:  "t",
			After:  map[string]interface{}{"id": int64(1)},
		},
		{
			Type:  connector.InsertEvent,
			Table: "t",
			// Missing After → should error
		},
	}

	_, err := ReverseSQLBatch(events, nil)
	if err == nil {
		t.Fatal("expected error from batch, got nil")
	}
}
