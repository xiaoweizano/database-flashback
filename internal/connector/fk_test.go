package connector

import (
	"context"
	"database/sql"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func newFKMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db, mock
}

// patternForFK builds the regexp to match GetFKOrder's dynamic query.
// The query has two IN clauses with the same number of placeholders.
func patternForFK(tables []string) string {
	placeholders := make([]byte, len(tables)*2-1)
	for i := range tables {
		if i > 0 {
			placeholders[i*2-1] = ','
		}
		placeholders[i*2] = '?'
	}
	ph := string(placeholders)
	return regexp.QuoteMeta(
		"SELECT DISTINCT TABLE_NAME, REFERENCED_TABLE_NAME FROM information_schema.REFERENTIAL_CONSTRAINTS WHERE TABLE_NAME IN (" + ph + ") AND REFERENCED_TABLE_NAME IN (" + ph + ")")
}

// ---------------------------------------------------------------------------
// GetFKOrder
// ---------------------------------------------------------------------------

func TestGetFKOrder_EmptyTables(t *testing.T) {
	db, _ := newFKMock(t)
	result, err := GetFKOrder(context.Background(), db, nil)
	require.NoError(t, err)
	assert.Nil(t, result)

	result, err = GetFKOrder(context.Background(), db, []string{})
	require.NoError(t, err)
	assert.Nil(t, result)
}

func TestGetFKOrder_NoFKs(t *testing.T) {
	db, mock := newFKMock(t)

	mock.ExpectQuery(patternForFK([]string{"t1", "t2"})).
		WithArgs("t1", "t2", "t1", "t2").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME", "REFERENCED_TABLE_NAME"}))

	result, err := GetFKOrder(context.Background(), db, []string{"t1", "t2"})
	require.NoError(t, err)
	// No FK means no reordering needed: input order preserved.
	assert.Equal(t, []string{"t1", "t2"}, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetFKOrder_SimpleChain(t *testing.T) {
	db, mock := newFKMock(t)

	// orders.user_id -> users.id
	mock.ExpectQuery(patternForFK([]string{"users", "orders"})).
		WithArgs("users", "orders", "users", "orders").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME", "REFERENCED_TABLE_NAME"}).
			AddRow("orders", "users"))

	result, err := GetFKOrder(context.Background(), db, []string{"users", "orders"})
	require.NoError(t, err)
	// users (parent) should come before orders (child).
	assert.Equal(t, []string{"users", "orders"}, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetFKOrder_MultipleDependencies(t *testing.T) {
	db, mock := newFKMock(t)

	// order_items.order_id -> orders.id
	// orders.user_id -> users.id
	// order_items.product_id -> products.id
	mock.ExpectQuery(patternForFK([]string{"users", "products", "orders", "order_items"})).
		WithArgs("users", "products", "orders", "order_items", "users", "products", "orders", "order_items").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME", "REFERENCED_TABLE_NAME"}).
			AddRow("orders", "users").
			AddRow("order_items", "orders").
			AddRow("order_items", "products"))

	result, err := GetFKOrder(context.Background(), db, []string{"users", "products", "orders", "order_items"})
	require.NoError(t, err)
	// Users and products should come first (no deps), then orders (depends on users),
	// then order_items (depends on orders and products).
	assert.Equal(t, "users", result[0])
	assert.Equal(t, "products", result[1])
	assert.Equal(t, "orders", result[2])
	assert.Equal(t, "order_items", result[3])
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetFKOrder_DisconnectedTables(t *testing.T) {
	db, mock := newFKMock(t)

	// Only orders->users has FK; t3 is entirely disconnected.
	mock.ExpectQuery(patternForFK([]string{"users", "orders", "t3"})).
		WithArgs("users", "orders", "t3", "users", "orders", "t3").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME", "REFERENCED_TABLE_NAME"}).
			AddRow("orders", "users"))

	result, err := GetFKOrder(context.Background(), db, []string{"t3", "users", "orders"})
	require.NoError(t, err)
	// t3 has no deps, so it stays first. Then users, then orders.
	assert.Equal(t, []string{"t3", "users", "orders"}, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetFKOrder_CycleInGraph(t *testing.T) {
	db, mock := newFKMock(t)

	// a -> b -> c -> a (cycle)
	mock.ExpectQuery(patternForFK([]string{"a", "b", "c"})).
		WithArgs("a", "b", "c", "a", "b", "c").
		WillReturnRows(sqlmock.NewRows([]string{"TABLE_NAME", "REFERENCED_TABLE_NAME"}).
			AddRow("b", "a").
			AddRow("c", "b").
			AddRow("a", "c"))

	result, err := GetFKOrder(context.Background(), db, []string{"a", "b", "c"})
	require.NoError(t, err)
	// Cycle should not cause an error; tables are appended at the end.
	assert.Len(t, result, 3)
	assert.Subset(t, []string{"a", "b", "c"}, result)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetFKOrder_QueryError(t *testing.T) {
	db, mock := newFKMock(t)

	mock.ExpectQuery(patternForFK([]string{"t1"})).
		WithArgs("t1", "t1").
		WillReturnError(sql.ErrConnDone)

	_, err := GetFKOrder(context.Background(), db, []string{"t1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "fk: query")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// ---------------------------------------------------------------------------
// topoSort (unit-level)
// ---------------------------------------------------------------------------

func TestTopoSort_Empty(t *testing.T) {
	assert.Nil(t, topoSort(nil, nil))
	assert.Empty(t, topoSort([]string{}, []FKConstraint{}))
}

func TestTopoSort_NoConstraints(t *testing.T) {
	result := topoSort([]string{"a", "b", "c"}, nil)
	assert.Equal(t, []string{"a", "b", "c"}, result)
}

func TestTopoSort_SingleConstraint(t *testing.T) {
	constraints := []FKConstraint{
		{TableName: "child", RefTableName: "parent"},
	}
	result := topoSort([]string{"parent", "child"}, constraints)
	assert.Equal(t, []string{"parent", "child"}, result)
}

func TestTopoSort_ReverseInputOrder(t *testing.T) {
	constraints := []FKConstraint{
		{TableName: "child", RefTableName: "parent"},
	}
	// Input order reversed.
	result := topoSort([]string{"child", "parent"}, constraints)
	assert.Equal(t, []string{"parent", "child"}, result)
}

func TestTopoSort_ForeignKeyReferencesOutsideSet(t *testing.T) {
	// Constraint references a table not in the input set.
	constraints := []FKConstraint{
		{TableName: "child", RefTableName: "outside_table"},
	}
	result := topoSort([]string{"child"}, constraints)
	assert.Equal(t, []string{"child"}, result)
}

func TestTopoSort_DiamondDependency(t *testing.T) {
	// a -> b -> d
	// a -> c -> d
	constraints := []FKConstraint{
		{TableName: "b", RefTableName: "a"},
		{TableName: "c", RefTableName: "a"},
		{TableName: "d", RefTableName: "b"},
		{TableName: "d", RefTableName: "c"},
	}
	result := topoSort([]string{"a", "b", "c", "d"}, constraints)
	assert.Equal(t, "a", result[0])
	// b and c can be in either order, but both before d.
	assert.Equal(t, "d", result[3])
	assert.Subset(t, result[1:3], []string{"b", "c"})
}
