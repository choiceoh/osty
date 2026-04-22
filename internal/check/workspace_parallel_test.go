package check

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// synthWorkspace builds an on-disk workspace with N packages under a
// tmp directory and returns its loaded Workspace + Resolve results.
// Each package has a trivial `add(a, b: Int) -> Int { a + b }` to
// keep the checker surface non-empty but cheap — the point of the
// benchmark is scheduler overhead, not per-package work complexity.
//
// Packages are NOT imported by each other, so the workspace is a
// "fanout" shape: the checker can process every package in parallel
// with zero cross-dependency. That isolates the speedup upper bound
// from dependency-sequencing effects.
func synthWorkspace(tb testing.TB, n int) (*resolve.Workspace, map[string]*resolve.PackageResult) {
	tb.Helper()
	dir := tb.TempDir()
	members := make([]byte, 0, n*16)
	members = append(members, "[workspace]\nmembers = ["...)
	for i := 0; i < n; i++ {
		if i > 0 {
			members = append(members, ", "...)
		}
		members = append(members, fmt.Sprintf("\"pkg%d\"", i)...)
	}
	members = append(members, "]\n"...)
	if err := os.WriteFile(filepath.Join(dir, "osty.toml"), members, 0o644); err != nil {
		tb.Fatalf("write root osty.toml: %v", err)
	}
	for i := 0; i < n; i++ {
		pkgDir := filepath.Join(dir, fmt.Sprintf("pkg%d", i))
		if err := os.MkdirAll(pkgDir, 0o755); err != nil {
			tb.Fatalf("mkdir pkg%d: %v", i, err)
		}
		if err := os.WriteFile(filepath.Join(pkgDir, "osty.toml"),
			[]byte(fmt.Sprintf("[package]\nname = \"pkg%d\"\nversion = \"0.1.0\"\n", i)),
			0o644); err != nil {
			tb.Fatalf("write pkg%d osty.toml: %v", i, err)
		}
		body := fmt.Sprintf(`pub fn add%d(a: Int, b: Int) -> Int {
    a + b
}

pub fn sub%d(a: Int, b: Int) -> Int {
    a - b
}

pub struct Pair%d {
    pub lo: Int,
    pub hi: Int,
}
`, i, i, i)
		if err := os.WriteFile(filepath.Join(pkgDir, "lib.osty"),
			[]byte(body), 0o644); err != nil {
			tb.Fatalf("write pkg%d lib.osty: %v", i, err)
		}
	}
	ws, err := resolve.NewWorkspace(dir)
	if err != nil {
		tb.Fatalf("resolve.NewWorkspace: %v", err)
	}
	ws.Stdlib = stdlib.LoadCached()
	for i := 0; i < n; i++ {
		if _, err := ws.LoadPackage(fmt.Sprintf("pkg%d", i)); err != nil {
			tb.Fatalf("load pkg%d: %v", i, err)
		}
	}
	resolved := ws.ResolveAll()
	return ws, resolved
}

// TestWorkspaceParallelCheckMatchesSequential fences the parallel
// native-checker path against the serial one on a synthetic 8-package
// workspace. The public observable (Diags + shared type-map contents)
// must match bit-for-bit — if they diverge, the overlay mutex or the
// native-call ordering has a bug that would show up as flaky checker
// errors in production.
func TestWorkspaceParallelCheckMatchesSequential(t *testing.T) {
	ws, resolved := synthWorkspace(t, 8)

	t.Setenv("OSTY_CHECK_PARALLEL", "0")
	seq := Workspace(ws, resolved)

	t.Setenv("OSTY_CHECK_PARALLEL", "1")
	par := Workspace(ws, resolved)

	if len(seq) != len(par) {
		t.Fatalf("package count: seq=%d par=%d", len(seq), len(par))
	}
	for path, sr := range seq {
		pr, ok := par[path]
		if !ok {
			t.Errorf("parallel missing package %q", path)
			continue
		}
		if got, want := len(pr.Diags), len(sr.Diags); got != want {
			t.Errorf("pkg %s diag count: par=%d seq=%d", path, got, want)
		}
	}
	// The shared type maps fan in from every per-package overlay. They
	// must carry the same total assignment count under both orderings.
	var seqAny, parAny *Result
	for _, r := range seq {
		seqAny = r
		break
	}
	for _, r := range par {
		parAny = r
		break
	}
	if seqAny != nil && parAny != nil {
		if got, want := len(parAny.Types), len(seqAny.Types); got != want {
			t.Errorf("Types map size: par=%d seq=%d", got, want)
		}
		if got, want := len(parAny.LetTypes), len(seqAny.LetTypes); got != want {
			t.Errorf("LetTypes map size: par=%d seq=%d", got, want)
		}
		if got, want := len(parAny.SymTypes), len(seqAny.SymTypes); got != want {
			t.Errorf("SymTypes map size: par=%d seq=%d", got, want)
		}
	}
}

func benchWorkspaceCheck(b *testing.B, n int, parallel string) {
	ws, resolved := synthWorkspace(b, n)
	b.Setenv("OSTY_CHECK_PARALLEL", parallel)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = Workspace(ws, resolved)
	}
}

func BenchmarkWorkspaceCheck8Serial(b *testing.B)   { benchWorkspaceCheck(b, 8, "0") }
func BenchmarkWorkspaceCheck8Parallel(b *testing.B) { benchWorkspaceCheck(b, 8, "1") }
func BenchmarkWorkspaceCheck32Serial(b *testing.B)  { benchWorkspaceCheck(b, 32, "0") }
func BenchmarkWorkspaceCheck32Parallel(b *testing.B) {
	benchWorkspaceCheck(b, 32, "1")
}
