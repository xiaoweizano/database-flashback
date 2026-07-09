package parser

import (
	"fmt"
	"sort"
	"strings"

	"github.com/a-shan/mysql-pitr/internal/connector"
)

// ReverseSQL generates the SQL statement that undoes a single row event.
//
// pkColumns specifies which columns form the primary key.  When non-empty the
// WHERE clause uses only those columns; otherwise it matches against every
// column in the row image.
//
// Event-type rules:
//
//	INSERT → DELETE FROM `table` WHERE <match> LIMIT 1
//	DELETE → INSERT INTO `table` (cols) VALUES (vals)
//	UPDATE → UPDATE `table` SET <before_vals> WHERE <after_match> LIMIT 1
func ReverseSQL(event connector.RowEvent, pkColumns []string) (string, error) {
	switch event.Type {
	case connector.InsertEvent:
		return reverseInsert(event, pkColumns)
	case connector.DeleteEvent:
		return reverseDelete(event)
	case connector.UpdateEvent:
		return reverseUpdate(event, pkColumns)
	default:
		return "", fmt.Errorf("unsupported event type: %s", event.Type)
	}
}

// ReverseSQLBatch generates reverse SQL for multiple row events in order.
// Each event produces one SQL statement; the returned slice has the same length
// as the input events slice.
func ReverseSQLBatch(events []connector.RowEvent, pkColumns []string) ([]string, error) {
	stmts := make([]string, 0, len(events))
	for i, ev := range events {
		sql, err := ReverseSQL(ev, pkColumns)
		if err != nil {
			return stmts, fmt.Errorf("event %d: %w", i, err)
		}
		stmts = append(stmts, sql)
	}
	return stmts, nil
}

// --------------------------------------------------------------------------
// Internal helpers
// --------------------------------------------------------------------------

// reverseInsert generates a DELETE that undoes an INSERT.
func reverseInsert(event connector.RowEvent, pkColumns []string) (string, error) {
	if event.After == nil {
		return "", fmt.Errorf("INSERT event has no After image")
	}
	where, err := buildWhereClause(event.After, pkColumns)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("DELETE FROM `%s`%s LIMIT 1;", event.Table, where), nil
}

// reverseDelete generates an INSERT that undoes a DELETE.
func reverseDelete(event connector.RowEvent) (string, error) {
	if event.Before == nil {
		return "", fmt.Errorf("DELETE event has no Before image")
	}

	cols := sortedKeys(event.Before)
	colNames := make([]string, len(cols))
	colValues := make([]string, len(cols))
	for i, c := range cols {
		colNames[i] = "`" + c + "`"
		v, err := FormatColumnValue(event.Before[c])
		if err != nil {
			return "", fmt.Errorf("column %s: %w", c, err)
		}
		colValues[i] = v
	}

	return fmt.Sprintf("INSERT INTO `%s` (%s) VALUES (%s);",
		event.Table,
		strings.Join(colNames, ", "),
		strings.Join(colValues, ", ")), nil
}

// reverseUpdate generates an UPDATE that restores the before-image.
func reverseUpdate(event connector.RowEvent, pkColumns []string) (string, error) {
	if event.Before == nil {
		return "", fmt.Errorf("UPDATE event has no Before image")
	}
	if event.After == nil {
		return "", fmt.Errorf("UPDATE event has no After image")
	}

	// SET clause: restore the before-image values.
	setCols := sortedKeys(event.Before)
	setParts := make([]string, 0, len(setCols))
	for _, c := range setCols {
		v, err := FormatColumnValue(event.Before[c])
		if err != nil {
			return "", fmt.Errorf("column %s: %w", c, err)
		}
		setParts = append(setParts, fmt.Sprintf("`%s` = %s", c, v))
	}

	// WHERE clause: locate the row using the after-image.
	where, err := buildWhereClause(event.After, pkColumns)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("UPDATE `%s` SET %s%s LIMIT 1;",
		event.Table,
		strings.Join(setParts, ", "),
		where), nil
}

// buildWhereClause constructs a WHERE clause from the given row image.
//
// If pkColumns is non-empty, only those columns are used in the condition;
// otherwise every column in the image is used.
//
// NULL values produce "`col` IS NULL" instead of "`col` = NULL".
func buildWhereClause(values map[string]interface{}, pkColumns []string) (string, error) {
	var columns []string
	if len(pkColumns) > 0 {
		columns = pkColumns
	} else {
		columns = sortedKeys(values)
	}

	var conditions []string
	for _, col := range columns {
		val, ok := values[col]
		if !ok {
			continue
		}
		cond, err := buildCondition(col, val)
		if err != nil {
			return "", fmt.Errorf("column %s: %w", col, err)
		}
		conditions = append(conditions, cond)
	}

	if len(conditions) == 0 {
		return "", fmt.Errorf("no columns available for WHERE clause")
	}
	return " WHERE " + strings.Join(conditions, " AND "), nil
}

// buildCondition returns a single SQL equality condition for a column.
// NULL values produce `col` IS NULL rather than `col` = NULL.
func buildCondition(col string, val interface{}) (string, error) {
	if val == nil {
		return "`" + col + "` IS NULL", nil
	}
	s, err := FormatColumnValue(val)
	if err != nil {
		return "", err
	}
	return "`" + col + "` = " + s, nil
}

// sortedKeys returns the keys of m in sorted order.
func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
