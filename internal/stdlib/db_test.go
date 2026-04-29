package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestDbModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["db"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.db not loaded")
	}
	for _, name := range []string{
		"sqlite", "postgres", "mysql", "withParam", "withPassword", "withoutPassword",
		"driverName", "defaultPort", "dialect", "dsn", "redactedDsn",
		"poolOptions", "withMaxOpen", "withMaxIdle", "withConnectTimeout", "withIdleTimeout",
		"txOptions", "readOnlyTx", "withIsolation", "isolationName",
		"cellNull", "cellString", "cellInt", "cellFloat", "cellBool", "cellText",
		"row", "rowGet", "rowText", "resultSet", "emptyResultSet", "first", "columnNames", "execResult",
		"migration", "migrationTableSql", "recordMigrationSql", "pendingMigrations", "plan", "queryText",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.db missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.db.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.db.%s not public", name)
		}
	}
	for _, name := range []string{
		"Driver", "Isolation", "Cell", "Config", "PoolOptions", "TxOptions",
		"Row", "ResultSet", "ExecResult", "Migration", "MigrationPlan",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.db missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.db.%s not public", name)
		}
	}
}

func TestDbModuleSourcePinsNonExecutingBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["db"]
	if mod == nil {
		t.Fatal("std.db module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn dsn(config: Config) -> Result<String, Error>`,
		`pub fn dialect(driver: Driver) -> sql.Dialect`,
		`pub fn resultSet(columns: List<String>, rows: List<Row>) -> Result<ResultSet, Error>`,
		`pub fn migration(version: Int, name: String, up: sql.Query, down: sql.Query) -> Result<Migration, Error>`,
		`Runtime-backed drivers can consume Config, PoolOptions, TxOptions, and`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.db source missing %q", want)
		}
	}
}
