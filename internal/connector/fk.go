package connector

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// FKConstraint describes a foreign key relationship between two tables.
type FKConstraint struct {
	TableName    string // the child (referencing) table
	RefTableName string // the parent (referenced) table
}

// GetFKOrder queries MySQL INFORMATION_SCHEMA for foreign-key relationships
// among the given tables and returns them in FK-safe processing order (parents
// before children). Tables without FK dependencies or not involved in any FK
// graph are returned in their original relative order.
//
// The topological sort uses Kahn's algorithm. If the dependency graph contains
// a cycle the cyclic nodes are still included at the end of the result.
func GetFKOrder(ctx context.Context, db *sql.DB, tables []string) ([]string, error) {
	if len(tables) == 0 {
		return nil, nil
	}

	constraints, err := queryFKConstraints(ctx, db, tables)
	if err != nil {
		return nil, err
	}

	return topoSort(tables, constraints), nil
}

// queryFKConstraints fetches FK relationships where both sides are in the
// given set of tables.
func queryFKConstraints(ctx context.Context, db *sql.DB, tables []string) ([]FKConstraint, error) {
	placeholders := make([]string, len(tables))
	args := make([]interface{}, len(tables))
	for i, t := range tables {
		placeholders[i] = "?"
		args[i] = t
	}

	inClause := strings.Join(placeholders, ",")
	query := `SELECT DISTINCT TABLE_NAME, REFERENCED_TABLE_NAME
                FROM information_schema.REFERENTIAL_CONSTRAINTS
               WHERE TABLE_NAME IN (` + inClause + `)
                 AND REFERENCED_TABLE_NAME IN (` + inClause + `)`

	allArgs := append(args, args...)

	rows, err := db.QueryContext(ctx, query, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("fk: query: %w", err)
	}
	defer rows.Close()

	var constraints []FKConstraint
	for rows.Next() {
		var c FKConstraint
		if err := rows.Scan(&c.TableName, &c.RefTableName); err != nil {
			return nil, fmt.Errorf("fk: scan: %w", err)
		}
		constraints = append(constraints, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fk: rows: %w", err)
	}

	return constraints, nil
}

// topoSort returns tables ordered so that every referenced table appears
// before any table that references it, using Kahn's algorithm.
func topoSort(tables []string, constraints []FKConstraint) []string {
	inDegree := make(map[string]int, len(tables))
	children := make(map[string][]string, len(tables))

	for _, t := range tables {
		inDegree[t] = 0
	}

	for _, c := range constraints {
		// Only consider edges where both endpoints are in our table set.
		if _, ok := inDegree[c.RefTableName]; !ok {
			continue
		}
		if _, ok := inDegree[c.TableName]; !ok {
			continue
		}
		children[c.RefTableName] = append(children[c.RefTableName], c.TableName)
		inDegree[c.TableName]++
	}

	// Queue nodes with no incoming edges.
	queue := make([]string, 0, len(tables))
	for _, t := range tables {
		if inDegree[t] == 0 {
			queue = append(queue, t)
		}
	}

	ordered := make([]string, 0, len(tables))
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		ordered = append(ordered, node)
		for _, child := range children[node] {
			inDegree[child]--
			if inDegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}

	// Append any tables that Kahn's algorithm didn't cover (e.g. cycles).
	seen := make(map[string]bool, len(ordered))
	for _, t := range ordered {
		seen[t] = true
	}
	for _, t := range tables {
		if !seen[t] {
			ordered = append(ordered, t)
		}
	}

	return ordered
}
