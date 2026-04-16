package osty

import (
	"github.com/osty/osty/internal/query"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// Engine bundles the Database, input handles, and registered query
// handles into one struct. Construct with [NewEngine]; the Engine is
// safe to store on long-lived server/CLI state.
//
// Typical usage:
//
//	eng := osty.NewEngine()
//	defer eng.Close()
//	eng.Inputs.SourceText.Set(eng.DB, "/abs/path/main.osty", srcBytes)
//	diags := eng.Queries.FileDiagnostics.Get(eng.DB, "/abs/path/main.osty")
//
// All path keys must be normalized via [NormalizePath] or [FromURI].
type Engine struct {
	DB      *query.Database
	Inputs  Inputs
	Queries Queries
}

// NewEngine constructs an Engine seeded with the process-singleton
// prelude and stdlib registry. The returned Engine is ready to accept
// input [Inputs.SourceText.Set] calls and serve derived queries via
// [Queries].
func NewEngine() *Engine {
	return newEngineWith(resolve.NewPrelude(), stdlib.LoadCached())
}

// NewEngineForTest constructs an Engine with custom prelude and stdlib,
// typically used by test harnesses that want a fresh prelude per test
// or want to simulate "no stdlib" environments.
func NewEngineForTest(prelude *resolve.Scope, reg *stdlib.Registry) *Engine {
	return newEngineWith(prelude, reg)
}

func newEngineWith(prelude *resolve.Scope, reg *stdlib.Registry) *Engine {
	db := query.NewDatabase(prelude, reg)
	inp := registerInputs(db)
	qs := registerQueries(db, inp)
	return &Engine{DB: db, Inputs: inp, Queries: qs}
}

// Close is reserved for future cleanup hooks (e.g. releasing cached
// slots on shutdown). Currently a no-op; calling it is always safe.
func (e *Engine) Close() {}
