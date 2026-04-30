package stdlib

import (
	"strings"
	"testing"
)

func TestTableModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["table"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.table not loaded")
	}

	for _, name := range []string{
		"empty", "row", "schema", "schemaFrom", "fromRows", "fromRecords", "fromCsv", "fromTsv",
		"fromDelimited", "toCsv", "toTsv", "toDelimited", "dataRows", "toRows",
		"records", "select", "drop", "sortBy", "sortByType", "sortByInt",
		"sortByFloat", "sortByBool", "groupBy", "countBy", "innerJoin",
		"leftJoin", "innerJoinIndex", "leftJoinIndex", "inferTypes",
		"validateSchema", "coerce", "summarize", "summarizeBy", "sum", "avg",
		"min", "max", "indexBy", "columnTypeName",
	} {
		requirePublicFn(t, mod, "table", name)
	}
	for _, name := range []string{"ColumnType", "ColumnSchema", "Row", "TableGroup", "NumericSummary", "JoinIndex", "Table"} {
		requirePublicType(t, mod, "table", name)
	}
	for _, method := range []string{"get", "getOr", "int", "float", "bool"} {
		if got := reg.LookupMethodDecl("table", "Row", method); got == nil {
			t.Fatalf("LookupMethodDecl(table, Row, %s) = nil", method)
		}
	}
	for _, method := range []string{
		"len", "isEmpty", "width", "hasColumn", "get", "select", "drop",
		"filter", "sortBy", "sortByType", "sortByInt", "sortByFloat",
		"sortByBool", "groupBy", "countBy", "innerJoin", "leftJoin",
		"innerJoinIndex", "leftJoinIndex", "inferTypes", "validateSchema",
		"coerce", "summarize", "summarizeBy", "sum", "avg", "min", "max",
		"indexBy", "dataRows", "toRows", "records", "toCsv", "toTsv",
	} {
		if got := reg.LookupMethodDecl("table", "Table", method); got == nil {
			t.Fatalf("LookupMethodDecl(table, Table, %s) = nil", method)
		}
	}
}

func TestTableModuleSourcePinsDataframeBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["table"]
	if mod == nil {
		t.Fatal("std.table module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		"csv.decodeTsv(text)?",
		"csv.encodeTsv(table.toRows())",
		"pub fn filter(self, pred: fn(Row) -> Bool) -> Table",
		"pub fn sortByType(table: Table, column: String, kind: ColumnType, descending: Bool) -> Result<Table, Error>",
		"pub fn validateSchema(table: Table, schema: List<ColumnSchema>) -> Result<(), Error>",
		"pub fn coerce(table: Table, schema: List<ColumnSchema>) -> Result<Table, Error>",
		"pub fn summarizeBy(table: Table, groupColumn: String, valueColumn: String) -> Result<Table, Error>",
		"pub fn indexBy(table: Table, column: String) -> Result<JoinIndex, Error>",
		"pub fn groupBy(table: Table, column: String) -> Result<List<TableGroup>, Error>",
		"pub fn innerJoin(left: Table, right: Table, leftColumn: String, rightColumn: String) -> Result<Table, Error>",
		"fn joinIndexed(left: Table, right: JoinIndex, leftColumn: String, includeUnmatchedLeft: Bool) -> Result<Table, Error>",
		"fn rightOutputName(leftColumns: List<String>, outputColumns: List<String>, column: String) -> String",
		"pub fn inferTypes(table: Table) -> List<ColumnSchema>",
		"strings.toInt(strings.trimSpace(value))",
		"strings.toFloat(strings.trimSpace(value))",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.table source missing %q", want)
		}
	}
}
