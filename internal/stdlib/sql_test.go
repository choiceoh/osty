package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestSqlModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["sql"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.sql not loaded")
	}
	for _, name := range []string{
		"null", "text", "integer", "float", "boolean", "rawValue",
		"query", "raw", "condition", "assignment", "asc", "desc",
		"quoteIdent", "quotePath", "literal", "placeholder",
		"genericDialect", "postgresDialect", "mysqlDialect", "sqliteDialect",
		"render", "debugSql",
		"eq", "ne", "gt", "gte", "lt", "lte", "like", "isNull", "isNotNull", "inList",
		"allOf", "anyOf", "select", "selectWhere", "insert", "update", "deleteFrom",
		"orderBy", "limit", "offset", "isIdent",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.sql missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.sql.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.sql.%s not public", name)
		}
	}
	for _, name := range []string{"Dialect", "Value", "Query", "Condition", "Assignment", "Order"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.sql missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.sql.%s not public", name)
		}
	}
}

func TestSqlModuleSourcePinsBuilderBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["sql"]
	if mod == nil {
		t.Fatal("std.sql module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn quoteIdent(name: String) -> Result<String, Error>`,
		`pub fn placeholder(dialect: Dialect, index: Int) -> String`,
		`pub fn render(q: Query, dialect: Dialect) -> String`,
		`pub fn debugSql(q: Query) -> String`,
		`pub fn insert(table: String, values: List<Assignment>) -> Result<Query, Error>`,
		`strings.replaceAll(value, "'", "''")`,
		`copySqlQuoted(chars, i, c)`,
		`startsSqlDollarQuote(chars, i)`,
		`copySqlBacktickQuoted(chars, i)`,
		`c.toInt() == 0x5C`,
		`startsSqlLineComment(chars, i)`,
		`questionLooksLikeOperator(chars, i)`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.sql source missing %q", want)
		}
	}
}
