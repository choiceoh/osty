package check_test

import (
	"sync"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
	"github.com/osty/osty/internal/types"
)

// registryShim is the minimal interface the test helpers consume from
// a Registry — just the primitive method table. Split out so the
// primary test file doesn't have to import the stdlib package twice.
type registryShim struct {
	Primitives map[types.PrimitiveKind]map[string]*ast.FnDecl
	lookup     func(string) *resolve.Package
}

func (r registryShim) LookupPackage(dotPath string) *resolve.Package {
	if r.lookup == nil {
		return nil
	}
	return r.lookup(dotPath)
}

var (
	registryCache     *registryShim
	registryCacheOnce sync.Once
)

// loadRegistryOnce returns the cached stdlib Registry wrapped in a shim
// so tests can construct check.Opts without re-parsing stubs.
func loadRegistryOnce() registryShim {
	registryCacheOnce.Do(func() {
		reg := stdlib.Load()
		registryCache = &registryShim{Primitives: reg.Primitives, lookup: reg.LookupPackage}
	})
	return *registryCache
}
