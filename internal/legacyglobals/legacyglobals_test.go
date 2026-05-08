package legacyglobals_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/legacyglobals"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
)

// TestDetectAST_FlagsLegacyGlobalCallSites verifies the detector walks a
// representative file and emits one W0750 per legacy-global call site,
// covering every capability bucket on the v0.6 catalog (Clock, Rng,
// Env, Fs, Net, Process). Calls that look similar but are not in the
// catalog (e.g., `cap.now()` on a passed Clock parameter) must NOT
// fire — the detector is selector-head-keyed by module name so user
// idents shadowing those names would also trigger; that is intended
// since the whole point of `--legacy-globals` is to enumerate every
// call site that needs migration.
func TestDetectAST_FlagsLegacyGlobalCallSites(t *testing.T) {
	src := []byte(`fn main() {
    let _ = time.now()
    let _ = random.next()
    let _ = env.get("HOME")
    let _ = fs.read("/etc/passwd")
    let _ = net.dial("example.com", 443)
    let _ = os.exec("ls", [])
    println("hello")
}
`)
	run := selfhost.Run(src)
	file := selfhost.LowerPublicFileFromRun(run)
	if file == nil {
		t.Fatalf("selfhost lower returned nil file (parse diags: %v)", run.Diagnostics())
	}
	diags := legacyglobals.DetectAST(file, "test.osty")
	if got := len(diags); got != 6 {
		t.Fatalf("DetectAST W0750 count = %d, want 6\n%s", got, formatDiags(diags))
	}
	// Every diagnostic must carry the W0750 code at Warning severity.
	for i, d := range diags {
		if d == nil {
			t.Fatalf("diagnostic[%d] is nil", i)
		}
		if d.Code != diag.CodeDeprecatedUse {
			t.Errorf("diagnostic[%d].Code = %q, want %q", i, d.Code, diag.CodeDeprecatedUse)
		}
		if d.Severity != diag.Warning {
			t.Errorf("diagnostic[%d].Severity = %v, want Warning", i, d.Severity)
		}
		if d.File != "test.osty" {
			t.Errorf("diagnostic[%d].File = %q, want %q", i, d.File, "test.osty")
		}
	}

	// Spot-check that the message names the legacy form and the
	// capability bucket. The first emission corresponds to
	// `time.now()` so the message has both `time.now` and `Clock`.
	first := diags[0]
	if !strings.Contains(first.Message, "time.now") {
		t.Errorf("first diagnostic message = %q, want it to mention `time.now`", first.Message)
	}
	if !strings.Contains(first.Message, "Clock") {
		t.Errorf("first diagnostic message = %q, want it to mention `Clock`", first.Message)
	}
	// Hint must reference CLAUDE.md 부록 C.1 so users know where to
	// learn the capability migration pattern.
	if !strings.Contains(first.Hint, "capability parameter") {
		t.Errorf("first diagnostic hint = %q, want it to mention capability parameter", first.Hint)
	}
}

// TestDetectAST_BareIdentCallsAreSilent guards against the
// false-positive that would scrub every test file: bare-Ident callees
// like `println("...")` are *not* in the catalog (which is selector
// shaped), so they must not produce W0750 even though the migration
// table mentions a `console.println` capability form.
func TestDetectAST_BareIdentCallsAreSilent(t *testing.T) {
	src := []byte(`fn main() {
    println("hello")
    let n = 42
    let _ = n
}
`)
	run := selfhost.Run(src)
	file := selfhost.LowerPublicFileFromRun(run)
	if file == nil {
		t.Fatalf("selfhost lower returned nil file")
	}
	diags := legacyglobals.DetectAST(file, "test.osty")
	if len(diags) != 0 {
		t.Fatalf("DetectAST emitted %d diagnostic(s) for bare-Ident calls; want 0\n%s",
			len(diags), formatDiags(diags))
	}
}

// TestDetectAST_NestedCallSitesAreVisited walks calls inside nested
// expressions (closures, if-then bodies, struct-lit field values) so
// the visitor's coverage of the AST shape is pinned. Without this the
// detector might silently miss legacy calls inside e.g. `let _ = if c
// { time.now() } else { 0 }`.
func TestDetectAST_NestedCallSitesAreVisited(t *testing.T) {
	src := []byte(`fn main() {
    let cb = || time.now()
    let _ = cb
    let _ = if true { random.next() } else { 0 }
    for x in [1, 2, 3] {
        let _ = x + env.get("X").len()
    }
}
`)
	run := selfhost.Run(src)
	file := selfhost.LowerPublicFileFromRun(run)
	if file == nil {
		t.Fatalf("selfhost lower returned nil file (parse diags: %v)", run.Diagnostics())
	}
	diags := legacyglobals.DetectAST(file, "test.osty")
	if got := len(diags); got != 3 {
		t.Fatalf("nested-walk W0750 count = %d, want 3\n%s", got, formatDiags(diags))
	}
}

// TestLookupRule_CatalogContainsCanonicalEntries pins the catalog
// surface so a refactor that drops a row breaks fast. The rules tested
// here are the ones called out by name in BREAKING_v0.6.md §3 and
// LANG_SPEC_v0.6 §20.15.
func TestLookupRule_CatalogContainsCanonicalEntries(t *testing.T) {
	cases := []struct {
		module, method string
	}{
		{"time", "now"},
		{"time", "monotonic"},
		{"time", "sleep"},
		{"random", "next"},
		{"random", "default"},
		{"env", "get"},
		{"env", "set"},
		{"env", "args"},
		{"fs", "read"},
		{"fs", "readToString"},
		{"fs", "write"},
		{"net", "dial"},
		{"net", "listen"},
		{"os", "exec"},
		{"os", "exit"},
	}
	for _, tc := range cases {
		src := []byte("fn main() {\n    let _ = " + tc.module + "." + tc.method + "()\n}\n")
		run := selfhost.Run(src)
		file := selfhost.LowerPublicFileFromRun(run)
		if file == nil {
			t.Errorf("%s.%s: selfhost lower returned nil", tc.module, tc.method)
			continue
		}
		diags := legacyglobals.DetectAST(file, "test.osty")
		if len(diags) != 1 {
			t.Errorf("%s.%s: emitted %d diags, want 1", tc.module, tc.method, len(diags))
		}
	}
}

// TestDetectPackage_StampsFilePath builds a real *resolve.Package with
// two source files and asserts (a) every emitted W0750 carries the
// owning file path so multi-file diagnostics route correctly, and (b)
// passing a nil Package is a quiet no-op (the package resolver may
// hand a nil to downstream phases when load itself fails).
func TestDetectPackage_StampsFilePath(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.osty")
	b := filepath.Join(dir, "b.osty")
	if err := os.WriteFile(a, []byte(`fn first() {
    let _ = time.now()
}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte(`fn second() {
    let _ = fs.read("/etc/passwd")
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	pkg, err := resolve.LoadPackageForNative(dir)
	if err != nil {
		t.Fatalf("LoadPackageForNative: %v", err)
	}
	diags := legacyglobals.Detect(pkg)
	if got := len(diags); got != 2 {
		t.Fatalf("Detect produced %d diagnostics, want 2\n%s",
			got, formatDiags(diags))
	}
	files := map[string]bool{}
	for _, d := range diags {
		files[d.File] = true
	}
	if !files[a] {
		t.Errorf("expected diagnostic stamped %q, got files = %v", a, keys(files))
	}
	if !files[b] {
		t.Errorf("expected diagnostic stamped %q, got files = %v", b, keys(files))
	}

	if got := legacyglobals.Detect(nil); got != nil {
		t.Errorf("Detect(nil) = %v, want nil", got)
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func formatDiags(diags []*diag.Diagnostic) string {
	var b strings.Builder
	for _, d := range diags {
		if d == nil {
			b.WriteString("(nil)\n")
			continue
		}
		b.WriteString(d.Code)
		b.WriteString(": ")
		b.WriteString(d.Message)
		b.WriteString("\n")
	}
	return b.String()
}
