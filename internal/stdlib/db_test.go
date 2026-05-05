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
		`fn dsnHost(host: String) -> Result<String, Error>`,
		`fn validateHost(host: String) -> Result<(), Error>`,
		`db: host contains unsafe character`,
		`if isIpv6Host(clean)`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.db source missing %q", want)
		}
	}
}

func TestDbSqliteModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["db_sqlite"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.db.sqlite not loaded")
	}
	for _, name := range []string{
		"driverFactory", "open", "openPool",
		"pragma", "libVersion", "enableWal",
		"tables", "tableInfo", "execScript",
		"backup", "restore", "vacuum",
		"setBusyTimeout", "lastInsertRowId", "changes", "totalChanges", "interrupt",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.db.sqlite missing export %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.db.sqlite.%s not public", name)
		}
	}
}

func TestDbDriverModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["db_driver"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.db.driver not loaded")
	}
	for _, name := range []string{
		"newDriverRegistry", "registerDriver", "lookupDriver", "registeredDrivers",
		"defaultConnectionOptions", "defaultQueryOptions", "defaultIsolationLevel",
		"ConnectionOptions", "QueryOptions", "DriverRegistry", "IsolationLevel",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.db.driver missing export %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.db.driver.%s not public", name)
		}
	}
	for _, name := range []string{
		"Driver", "Connection", "Transaction", "Pool", "DriverFactory",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.db.driver missing interface %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.db.driver.%s not public", name)
		}
	}
}

func TestDbPoolModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["db_pool"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.db.pool not loaded")
	}
	for _, name := range []string{
		"poolStats", "newPool", "acquire", "release", "closePool",
		"stats", "reset", "ping",
		"setMaxOpen", "setMaxIdle", "setConnectTimeout", "setIdleTimeout",
		"prune",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.db.pool missing export %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.db.pool.%s not public", name)
		}
	}
	for _, name := range []string{
		"PoolStats",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.db.pool missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.db.pool.%s not public", name)
		}
	}
}

func TestDbMigrationModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["db_migration"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.db.migration not loaded")
	}
	for _, name := range []string{
		"migrationStatus", "migrationReport",
		"ensureMigrationTable", "appliedVersions", "currentVersion",
		"apply", "rollbackLast", "rollbackN", "rollbackTo",
		"status", "redo", "reset", "forceVersion",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.db.migration missing export %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.db.migration.%s not public", name)
		}
	}
	for _, name := range []string{
		"MigrationStatus", "MigrationReport",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.db.migration missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.db.migration.%s not public", name)
		}
	}
}

func TestDbOrmModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["db_orm"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.db.orm not loaded")
	}
	for _, name := range []string{
		"columnDef", "relationDef", "modelDef",
		"registerModel", "lookupModel", "registeredModels",
		"insert", "findByPk", "findBy", "update", "delete", "save",
		"insertMany", "deleteBy", "truncate",
		"selectAll", "selectColumns", "selectByPk", "selectWhere",
		"whereClause", "orderBy", "limitOffset",
		"related", "eagerLoad",
		"withTx", "txExec",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.db.orm missing export %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.db.orm.%s not public", name)
		}
	}
	for _, name := range []string{
		"ColumnDef", "RelationKind", "RelationDef", "ModelDef",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.db.orm missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.db.orm.%s not public", name)
		}
	}
}

func TestDbPgModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["db_pg"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.db.pg not loaded")
	}
	for _, name := range []string{
		"driverFactory", "open", "openPool",
		"serverVersion", "backendPid",
		"databases", "schemas", "tables", "tableInfo",
		"execScript", "copyIn", "copyOut",
		"prepare", "executePrepared", "preparedStatements",
		"setConfig", "getConfig", "cancelQuery", "terminate",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.db.pg missing export %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.db.pg.%s not public", name)
		}
	}
}

func TestDbMysqlModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["db_mysql"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.db.mysql not loaded")
	}
	for _, name := range []string{
		"driverFactory", "open", "openPool",
		"serverVersion", "connectionId",
		"databases", "tables", "tableInfo", "tableStatus",
		"execScript",
		"prepare", "executePrepared", "preparedStatements",
		"setSession", "getSession", "killConnection", "ping",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.db.mysql missing export %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.db.mysql.%s not public", name)
		}
	}
}
