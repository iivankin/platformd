package daemon

import (
	"testing"

	"github.com/iivankin/platformd/internal/managedpostgres"
)

func TestPostgresSizeResultReadsOneUnsignedCell(t *testing.T) {
	value, err := postgresSizeResult(managedpostgres.QueryResult{Statements: []managedpostgres.StatementResult{{
		Rows: [][]managedpostgres.Cell{{{Text: "4294967296"}}},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if value != 4_294_967_296 {
		t.Fatalf("database size = %d", value)
	}
}

func TestPostgresSizeResultRejectsUnexpectedShape(t *testing.T) {
	for _, result := range []managedpostgres.QueryResult{
		{},
		{Truncated: true},
		{Statements: []managedpostgres.StatementResult{{Rows: [][]managedpostgres.Cell{{{Null: true}}}}}},
		{Statements: []managedpostgres.StatementResult{{Rows: [][]managedpostgres.Cell{{{Text: "-1"}}}}}},
	} {
		if _, err := postgresSizeResult(result); err == nil {
			t.Fatalf("unexpected result was accepted: %+v", result)
		}
	}
}
