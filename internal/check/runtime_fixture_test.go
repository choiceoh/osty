package check

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// runtimeFixturePath resolves the path to testdata/runtime_fixture.osty
// from the repository root. The test binary's working directory is the
// package under test (`internal/check/`), so we walk up two levels.
func runtimeFixturePath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	// internal/check/ -> repository root
	root := filepath.Join(wd, "..", "..")
	return filepath.Join(root, "testdata", "runtime_fixture.osty")
}

// TestRuntimeFixtureResolverResolvesRawOperations pins that the
// stdlib registry surfaces `std.runtime.raw` and the resolver can bind
// its exported names. The §19 gate-level privileged/unprivileged
// fixture smokes were retired alongside the Go gate walkers (#770 +
// this PR): their coverage is now carried by host_boundary_test.go
// (E0770 stripping policy), rawptr_test.go (E0770 emission on
// unprivileged usage via the selfhost checker), and pod_bound_test.go
// (E0770 on `<T: Pod>`). The fixture itself stays as a living
// reference for every §19 surface in one place.
func TestRuntimeFixtureResolverResolvesRawOperations(t *testing.T) {
	src, err := os.ReadFile(runtimeFixturePath(t))
	if err != nil {
		t.Fatalf("reading fixture: %v", err)
	}
	file, parseDiags := parser.ParseDiagnostics(src)
	if len(parseDiags) > 0 {
		t.Fatalf("fixture failed to parse: %v", parseDiags)
	}
	reg := stdlib.LoadCached()
	if reg.LookupPackage("std.runtime.raw") == nil {
		t.Fatal("stdlib registry is missing std.runtime.raw (regression in #319)")
	}
	res := resolve.ResolveFileDefault(file, reg)
	for _, d := range res.Diags {
		if d == nil {
			continue
		}
		if d.Code == "E0500" {
			t.Errorf("resolver could not bind a name — stdlib or prelude regression: %s", d.Message)
		}
	}
}
