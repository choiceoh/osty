package stdlib

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/types"
)

// TestLoadAllModules asserts every embedded stub parses without
// producing error- or warning-severity diagnostics. Treating warnings
// as failures surfaces lint/style drift in new stubs immediately
// rather than letting it accumulate.
func TestLoadAllModules(t *testing.T) {
	r := Load()
	if r == nil {
		t.Fatal("Load() returned nil Registry")
	}
	if len(r.Modules) == 0 {
		t.Fatal("Registry has no modules — expected at least one stub to be embedded")
	}
	for _, d := range r.Diags {
		switch d.Severity {
		case diag.Error:
			t.Errorf("parse error in stdlib stub: [%s] %s", d.Code, d.Message)
		case diag.Warning:
			t.Errorf("parse warning in stdlib stub: [%s] %s", d.Code, d.Message)
		}
	}
}

func TestWorkspaceResolveReusesResolvedStdlibScopes(t *testing.T) {
	dir := t.TempDir()
	src := `use std.debug

fn main() {
    let _ = debug.dbg(1)
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.osty"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ws, err := resolve.NewWorkspace(dir)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	ws.Stdlib = Load()
	if _, err := ws.LoadPackage(""); err != nil {
		t.Fatalf("load package: %v", err)
	}
	results := ws.ResolveAll()
	for path, res := range results {
		for _, d := range res.Diags {
			if d.Code == diag.CodeDuplicateDecl {
				t.Fatalf("unexpected duplicate while resolving %s: %s", path, d.Error())
			}
		}
	}
}

func TestWorkspaceCheckUsesStdlibSignaturesWithoutCheckingStubBodies(t *testing.T) {
	dir := t.TempDir()
	src := `use std.fs

pub fn load(path: String) -> Result<String, Error> {
    fs.readToString(path)
}
`
	if err := os.WriteFile(filepath.Join(dir, "lib.osty"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	ws, err := resolve.NewWorkspace(dir)
	if err != nil {
		t.Fatalf("workspace: %v", err)
	}
	ws.Stdlib = Load()
	if _, err := ws.LoadPackage(""); err != nil {
		t.Fatalf("load package: %v", err)
	}
	resolved := ws.ResolveAll()
	checked := check.Workspace(ws, resolved)
	for path, res := range checked {
		for _, d := range res.Diags {
			if strings.HasPrefix(path, resolve.StdPrefix) {
				t.Fatalf("unexpected stdlib body diagnostic in %s: %s", path, d.Error())
			}
			if d.Severity == diag.Error {
				t.Fatalf("unexpected user diagnostic in %s: %s", path, d.Error())
			}
		}
	}
}

// TestAllSignatureStubsCheck pins the v0.4 G18 contract: stdlib
// protocol surfaces are executable front-end stubs, not unchecked text.
// Every embedded .osty file must parse, resolve, and type-check; the
// std.thread stub is allowed to trip E0743 in dummy bodies because its
// public signatures intentionally construct the non-escaping Handle
// capability that user code is otherwise forbidden to return/store.
func TestAllSignatureStubsCheck(t *testing.T) {
	reg := Load()
	for _, p := range collectStubPaths() {
		src, err := stubs.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		file, parseDiags := parser.ParseDiagnostics(src)
		for _, d := range parseDiags {
			if d.Severity == diag.Error {
				t.Fatalf("%s parse: %s", p, d.Error())
			}
		}
		pkg := &resolve.Package{
			Name: moduleName(p),
			Files: []*resolve.PackageFile{{
				Path:       p,
				Source:     src,
				File:       file,
				ParseDiags: parseDiags,
			}},
		}
		res := resolve.ResolvePackage(pkg, resolve.NewPrelude())
		for _, d := range res.Diags {
			if d.Severity == diag.Error {
				t.Fatalf("%s resolve: %s", p, d.Error())
			}
		}
		chk := check.Package(pkg, res, check.Opts{Primitives: reg.Primitives})
		for _, d := range chk.Diags {
			if d.Severity == diag.Error {
				if p == "modules/thread.osty" && d.Code == diag.CodeCapabilityEscape {
					continue
				}
				t.Fatalf("%s check: %s", p, d.Error())
			}
		}
	}
}

// TestLoadIsIdempotent pins the contract that Load can be called
// repeatedly: the resulting Registries are equivalently populated and
// do not share mutable state.
func TestLoadIsIdempotent(t *testing.T) {
	r1 := Load()
	r2 := Load()
	if len(r1.Modules) != len(r2.Modules) {
		t.Fatalf("module count drift: first call loaded %d, second %d",
			len(r1.Modules), len(r2.Modules))
	}
	for name := range r1.Modules {
		if _, ok := r2.Modules[name]; !ok {
			t.Errorf("module %q present in first Load but missing in second", name)
		}
	}
	// Registries must not share the top-level map (mutating one should
	// not affect the other).
	if &r1.Modules == &r2.Modules {
		t.Error("Load() returned the same underlying Modules map on both calls — state is shared")
	}
}

// TestOptionModuleParsed anchors the exemplar stub: `option` loads,
// parses, and yields at least one top-level declaration at the expected
// path. Analogous assertions accompany each additional stub.
func TestOptionModuleParsed(t *testing.T) {
	r := Load()
	m, ok := r.Modules["option"]
	if !ok {
		t.Fatalf(`Registry missing module "option" (have %d modules: %v)`,
			len(r.Modules), moduleNames(r))
	}
	if m.File == nil {
		t.Fatal(`Module "option" has nil File — parse did not complete`)
	}
	if len(m.File.Decls) == 0 {
		t.Error(`Module "option" parsed but has zero Decls`)
	}
	if m.Path != "modules/option.osty" {
		t.Errorf(`Module "option" Path = %q, want "modules/option.osty"`, m.Path)
	}
}

func TestResultModuleMethods(t *testing.T) {
	r := Load()
	m, ok := r.Modules["result"]
	if !ok {
		t.Fatal(`Registry missing module "result"`)
	}
	var resultEnum *ast.EnumDecl
	for _, d := range m.File.Decls {
		if e, ok := d.(*ast.EnumDecl); ok && e.Name == "Result" {
			resultEnum = e
			break
		}
	}
	if resultEnum == nil {
		t.Fatal(`Module "result" missing Result enum`)
	}
	got := map[string]bool{}
	hasBody := map[string]bool{}
	for _, method := range resultEnum.Methods {
		got[method.Name] = true
		hasBody[method.Name] = method.Body != nil
	}
	for _, name := range []string{
		"isOk", "isErr", "unwrap", "unwrapErr", "unwrapOr",
		"ok", "err", "map", "mapErr", "toString",
	} {
		if !got[name] {
			t.Errorf("Result missing method %q", name)
		}
		if r.ResultMethods[name] == nil {
			t.Errorf("Registry.ResultMethods missing method %q", name)
		}
	}
	for _, name := range []string{"isOk", "isErr", "unwrapOr", "ok", "err", "map", "mapErr", "toString"} {
		if !hasBody[name] {
			t.Errorf("Result.%s should have an Osty body", name)
		}
	}
	for _, name := range []string{"unwrap", "unwrapErr"} {
		if hasBody[name] {
			t.Errorf("Result.%s should stay runtime-intrinsic", name)
		}
	}
}

func TestResultModuleBodiesTypeCheck(t *testing.T) {
	mod := Load().Modules["result"]
	if mod == nil {
		t.Fatal("std.result not loaded")
	}
	file, parseDiags := parser.ParseDiagnostics(mod.Source)
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)
	all := append(append([]*diag.Diagnostic{}, parseDiags...), res.Diags...)
	all = append(all, chk.Diags...)
	for _, d := range all {
		if d.Severity == diag.Error {
			t.Fatalf("std.result should type-check: [%s] %s", d.Code, d.Message)
		}
	}
}

func moduleNames(r *Registry) []string {
	out := make([]string, 0, len(r.Modules))
	for name := range r.Modules {
		out = append(out, name)
	}
	return out
}

// TestErrorModuleResolves checks that the std.error stub resolves into
// a Package whose PkgScope exports the documented types. This is the
// load-bearing assertion that downstream `use std.error; error.Error`
// will succeed.
func TestErrorModuleResolves(t *testing.T) {
	r := Load()
	mod, ok := r.Modules["error"]
	if !ok {
		t.Fatal(`Registry missing module "error"`)
	}
	if mod.Package == nil {
		t.Fatal(`Module "error" has nil Package — Load did not resolve it`)
	}
	if mod.Package.PkgScope == nil {
		t.Fatal(`Module "error" has nil PkgScope — resolve did not populate it`)
	}
	for _, name := range []string{"Error", "BasicError"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("PkgScope missing expected export %q", name)
			continue
		}
		if !sym.Pub {
			t.Errorf("export %q is not pub", name)
		}
	}
}

// TestLookupPackageReturnsResolvedPackage exercises the StdlibProvider
// surface: LookupPackage hands back the exact same Package instance
// stored on the Module, which the workspace caches under the dotted
// path.
func TestLookupPackageReturnsResolvedPackage(t *testing.T) {
	r := Load()
	pkg := r.LookupPackage("std.error")
	if pkg == nil {
		t.Fatal(`LookupPackage("std.error") returned nil`)
	}
	if pkg != r.Modules["error"].Package {
		t.Error("LookupPackage returned a different Package instance than the Module holds")
	}
}

// TestSingleFileResolveValidatesStdlibMember is the end-to-end check
// that `use std.error; error.Error` type-references resolve through the
// provider rather than silently accepting unknown names. Without the
// provider wiring, member access on a stdlib stub is opaque; with it,
// a typo like `error.Nope` would surface as a diagnostic.
func TestSingleFileResolveValidatesStdlibMember(t *testing.T) {
	res := resolveSrc(t, `use std.error

pub fn describe(e: error.Error) -> String {
    ""
}
`, Load())
	for _, d := range res.Diags {
		if d.Code == diag.CodeUnknownExportedMember || d.Code == diag.CodePrivateAcrossPackages {
			t.Errorf("unexpected stdlib member diag with provider attached: %s", d.Error())
		}
	}
}

// TestSingleFileResolveRejectsUnknownMember flips the previous test:
// referencing a name that the std.error stub does not export must
// surface CodeUnknownExportedMember. This pins the contract that the
// provider actually exercises validation, not just caches a Package.
func TestSingleFileResolveRejectsUnknownMember(t *testing.T) {
	res := resolveSrc(t, `use std.error

pub fn describe(e: error.Nope) -> String {
    ""
}
`, Load())
	found := false
	for _, d := range res.Diags {
		if d.Code == diag.CodeUnknownExportedMember {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected CodeUnknownExportedMember for error.Nope; got %d diags: %v",
			len(res.Diags), diagCodes(res.Diags))
	}
}

func diagCodes(ds []*diag.Diagnostic) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Code)
	}
	return out
}

// resolveSrc parses src, fails the test on any parse error, and
// resolves the file against a fresh prelude with the given registry
// attached. Covers the happy-path boilerplate reused by most stdlib
// integration tests; cases that deliberately exercise parse failures
// should call parser.ParseDiagnostics directly.
func resolveSrc(t *testing.T, src string, reg *Registry) *resolve.Result {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	for _, d := range parseDiags {
		if d.Severity == diag.Error {
			t.Fatalf("parse error: %s", d.Error())
		}
	}
	return resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
}

// TestAliasedStdlibImport covers `use std.error as err`: the alias must
// route through the provider exactly like the unaliased form.
func TestAliasedStdlibImport(t *testing.T) {
	res := resolveSrc(t, `use std.error as err

pub fn describe(e: err.Error) -> String {
    ""
}
`, Load())
	for _, d := range res.Diags {
		if d.Severity == diag.Error {
			t.Errorf("unexpected diag on aliased stdlib import: %s", d.Error())
		}
	}
}

// TestStdlibTypeRefPopulatesTypeRefs confirms the resolver records a
// TypeRef for a NamedType whose head resolves through the stdlib
// provider, matching the shape the type checker consumes. Without this
// link the checker could not look up stdlib interfaces by symbol.
func TestStdlibTypeRefPopulatesTypeRefs(t *testing.T) {
	res := resolveSrc(t, `use std.error

pub fn describe(e: error.Error) -> String {
    ""
}
`, Load())
	if len(res.TypeRefs) == 0 {
		t.Fatal("resolver produced no TypeRefs for a file that references a stdlib type")
	}
	var found bool
	for _, sym := range res.TypeRefs {
		if sym != nil && sym.Name == "Error" {
			found = true
			break
		}
	}
	if !found {
		t.Error("TypeRefs missing the std.error `Error` head symbol")
	}
}

// TestRegistryShareableAcrossInvocations confirms that a single
// Registry can back multiple independent resolve passes — typical when
// the CLI processes several files in one run.
func TestRegistryShareableAcrossInvocations(t *testing.T) {
	reg := Load()
	for i, src := range []string{
		`use std.error
pub fn a(e: error.Error) -> String { "" }`,
		`use std.error
pub fn b(e: error.Error) -> String { "" }`,
	} {
		res := resolveSrc(t, src, reg)
		for _, d := range res.Diags {
			if d.Severity == diag.Error {
				t.Errorf("file %d: unexpected diag: %s", i, d.Error())
			}
		}
	}
}

// TestOptionVariantViaPkgAccess confirms that variant lookups work
// through the provider the same way type lookups do: `option.Some` and
// `option.None` must resolve to SymVariants exported by std.option.
func TestOptionVariantViaPkgAccess(t *testing.T) {
	mod := Load().Modules["option"]
	if mod == nil || mod.Package == nil {
		t.Fatal("std.option not loaded")
	}
	for _, name := range []string{"Option", "Some", "None"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.option PkgScope missing export %q", name)
		}
	}
}

// TestOptionModuleMethodsResolves pins the public combinator surface on
// std.option. The methods are authored in the Osty stub, even though the
// Go backend lowers calls directly because Option<T> is represented as
// a pointer.
func TestOptionModuleMethodsResolves(t *testing.T) {
	mod := Load().Modules["option"]
	if mod == nil || mod.File == nil {
		t.Fatal("std.option not loaded")
	}
	var methods map[string]bool
	for _, d := range mod.File.Decls {
		if ed, ok := d.(*ast.EnumDecl); ok && ed.Name == "Option" {
			methods = map[string]bool{}
			for _, m := range ed.Methods {
				if m.Pub {
					methods[m.Name] = true
				}
			}
			break
		}
	}
	if methods == nil {
		t.Fatal("std.option missing Option enum declaration")
	}
	for _, name := range []string{"isSome", "isNone", "unwrap", "unwrapOr", "orElse", "map", "orError", "toString"} {
		if !methods[name] {
			t.Errorf("std.option Option missing pub method %q", name)
		}
	}
}

// TestResultModuleResolves mirrors TestErrorModuleResolves for the
// std.result stub: the Package's PkgScope must expose the Result type
// and both variant constructors.
func TestResultModuleResolves(t *testing.T) {
	mod := Load().Modules["result"]
	if mod == nil || mod.Package == nil {
		t.Fatal("std.result not loaded")
	}
	for _, name := range []string{"Result", "Ok", "Err"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.result PkgScope missing export %q", name)
			continue
		}
		if !sym.Pub {
			t.Errorf("export %q is not pub", name)
		}
	}
}

// TestCmpModuleResolves verifies std.cmp exports the three built-in
// comparison interfaces with their Equal-hierarchy intact. All three
// are auto-imported via the prelude, so the module-local exports must
// stay in sync with the prelude list.
func TestCmpModuleResolves(t *testing.T) {
	mod := Load().Modules["cmp"]
	if mod == nil || mod.Package == nil {
		t.Fatal("std.cmp not loaded")
	}
	for _, name := range []string{"Equal", "Ordered", "Hashable"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.cmp PkgScope missing export %q", name)
			continue
		}
		if !sym.Pub {
			t.Errorf("export %q is not pub", name)
		}
		if sym.Kind != resolve.SymInterface {
			t.Errorf("export %q has kind %s; want interface", name, sym.Kind)
		}
	}
}

// TestCollectionsModuleResolves asserts that std.collections exports
// the three built-in collection structs. The prelude still aliases
// these names to its own SymBuiltin entries; the module-local symbols
// are the landing pad for the eventual prelude rebind step.
func TestCollectionsModuleResolves(t *testing.T) {
	mod := Load().Modules["collections"]
	if mod == nil || mod.Package == nil {
		t.Fatal("std.collections not loaded")
	}
	for _, name := range []string{"List", "Map", "Set"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.collections PkgScope missing export %q", name)
			continue
		}
		if !sym.Pub {
			t.Errorf("export %q is not pub", name)
		}
		if sym.Kind != resolve.SymStruct {
			t.Errorf("export %q has kind %s; want struct", name, sym.Kind)
		}
	}
}

// TestCollectionsViaPkgAccess verifies the user-visible shape: a file
// that explicitly imports std.collections and references one of its
// types resolves cleanly. Bare `List<T>` still flows through the
// prelude's SymBuiltin; this test covers the qualified form.
func TestCollectionsViaPkgAccess(t *testing.T) {
	res := resolveSrc(t, `use std.collections

pub fn take(xs: collections.List<Int>) -> collections.List<Int> {
    xs
}
`, Load())
	for _, d := range res.Diags {
		if d.Severity == diag.Error {
			t.Errorf("unexpected diag on std.collections reference: %s", d.Error())
		}
	}
}

// TestCmpInterfaceAsGenericBound pins the primary use-site for these
// interfaces: generic bounds. `<T: Ordered>` must resolve the bound
// through std.cmp (or the prelude alias) without emitting an unknown
// name diagnostic.
func TestCmpInterfaceAsGenericBound(t *testing.T) {
	res := resolveSrc(t, `pub fn max<T: Ordered>(a: T, b: T) -> T {
    if a.lt(b) {
        b
    } else {
        a
    }
}
`, Load())
	for _, d := range res.Diags {
		if d.Severity == diag.Error {
			t.Errorf("unexpected diag on generic-bound reference: %s", d.Error())
		}
	}
}

// TestResultGenericTypeReference exercises the most common stdlib
// reference shape: a generic return type built from std.result and the
// prelude's Error. The file-level resolver must find each head symbol
// and both type arguments without complaint.
func TestResultGenericTypeReference(t *testing.T) {
	res := resolveSrc(t, `use std.result

pub fn load(path: String) -> result.Result<Int, Error> {
    result.Ok(0)
}
`, Load())
	for _, d := range res.Diags {
		if d.Severity == diag.Error {
			t.Errorf("unexpected diag on std.result reference: %s", d.Error())
		}
	}
}

// TestTier1ModuleCoverage asserts each Tier 1 std.* module exports the
// public names documented in spec §10.1. Module stubs may refine or
// extend their surface over time; the expected-symbol lists below
// must stay a subset of what the stub actually declares.
func TestTier1ModuleCoverage(t *testing.T) {
	reg := Load()
	cases := []struct {
		module string
		names  []string
	}{
		{"io", []string{"print", "println", "eprint", "eprintln", "readLine"}},
		{"fs", []string{"readToString", "writeString", "exists", "remove"}},
		{"strings", []string{"split", "join", "contains", "startsWith", "endsWith",
			"trim", "trimStart", "trimEnd", "toUpper", "toLower", "repeat", "replace"}},
		{"random", []string{"Rng", "default", "seeded"}},
		{"url", []string{"Url", "parse", "join"}},
		{"json", []string{"Json", "Encode", "Decode", "encode", "decode", "parse", "stringify"}},
		{"time", []string{"Duration", "Instant", "Zone", "ZonedTime", "Weekday", "now", "parse", "sleep", "zone", "local", "utc", "ISO_8601", "RFC_3339", "RFC_2822"}},
		{"log", []string{"Level", "Fields", "LogValue", "Record", "Handler", "TextHandler", "JsonHandler", "debug", "info", "warn", "error", "setLevel", "setHandler"}},
		{"http", []string{"Method", "Headers", "Request", "Response", "Handler", "request", "get", "post", "serve"}},
		{"sync", []string{"Mutex", "Locked", "RwLock", "ReadLocked", "WriteLocked", "AtomicBool", "AtomicInt"}},
		{"iter", []string{"Iter", "from", "empty", "range"}},
		{"thread", []string{"Group", "Select", "spawn", "collectAll", "race", "chan", "sleep", "yield", "isCancelled", "checkCancelled", "select"}},
		{"os", []string{"path", "Path", "Output", "Signal", "exec", "execShell", "exit", "pid", "hostname", "onSignal"}},
		{"ref", []string{"same"}},
		{"process", []string{"abort", "unreachable", "todo", "ignoreError", "logError"}},
		{"debug", []string{"dbg"}},
	}
	for _, c := range cases {
		mod := reg.Modules[c.module]
		if mod == nil || mod.Package == nil {
			t.Errorf("std.%s not loaded", c.module)
			continue
		}
		for _, name := range c.names {
			sym := mod.Package.PkgScope.LookupLocal(name)
			if sym == nil {
				t.Errorf("std.%s missing export %q", c.module, name)
				continue
			}
			if !sym.Pub {
				t.Errorf("std.%s export %q is not pub", c.module, name)
			}
		}
	}
}

func TestRandomAndURLModulesResolve(t *testing.T) {
	reg := Load()
	for _, c := range []struct {
		module string
		typ    string
	}{
		{"random", "Rng"},
		{"url", "Url"},
	} {
		mod := reg.Modules[c.module]
		if mod == nil || mod.Package == nil {
			t.Fatalf("std.%s not loaded", c.module)
		}
		sym := mod.Package.PkgScope.LookupLocal(c.typ)
		if sym == nil {
			t.Fatalf("std.%s missing type %q", c.module, c.typ)
		}
		if sym.Kind != resolve.SymStruct {
			t.Errorf("std.%s %q kind = %s; want struct", c.module, c.typ, sym.Kind)
		}
	}
}

// TestMathModuleCoverage pins the std.math Tier 2 surface from spec
// §10.17: constants plus the Float-valued function set. The stdlib
// loader only needs signatures here; the transpiler owns the Go math
// lowering for executable code.
func TestMathModuleCoverage(t *testing.T) {
	reg := Load()
	mod := reg.Modules["math"]
	if mod == nil || mod.Package == nil {
		t.Fatal("std.math not loaded")
	}
	for _, name := range []string{
		"PI", "E", "TAU", "INFINITY", "NAN",
		"sin", "cos", "tan", "asin", "acos", "atan", "atan2",
		"sinh", "cosh", "tanh", "exp", "log", "log2", "log10",
		"sqrt", "cbrt", "pow", "floor", "ceil", "round", "trunc",
		"abs", "min", "max", "hypot",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.math missing export %q", name)
			continue
		}
		if !sym.Pub {
			t.Errorf("std.math export %q is not pub", name)
		}
	}
}

// TestMathPackageTypeChecks covers the user-facing front-end path:
// importing std.math, reading constants, and calling functions should
// produce concrete Float types instead of opaque package-member errors.
func TestMathPackageTypeChecks(t *testing.T) {
	src := []byte(`use std.math

pub fn score(r: Float) -> Float {
    let circle: Float = math.PI * r * r
    let angle: Float = math.sin(math.PI / 4.0)
    let scaled: Float = math.log(100.0, 10.0)
    math.max(circle, angle) + scaled
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	for _, d := range parseDiags {
		if d.Severity == diag.Error {
			t.Fatalf("parse error: %s", d.Error())
		}
	}
	reg := Load()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	for _, d := range append(res.Diags, chk.Diags...) {
		if d.Severity == diag.Error {
			t.Errorf("unexpected std.math diagnostic: %s", d.Error())
		}
	}
}

// TestRegexModuleCoverage pins the std.regex surface from spec §10.9:
// compile, Regex methods, Match fields, and Captures accessors.
func TestRegexModuleCoverage(t *testing.T) {
	reg := Load()
	mod := reg.Modules["regex"]
	if mod == nil || mod.Package == nil {
		t.Fatal("std.regex not loaded")
	}
	for _, name := range []string{"Regex", "RegexError", "Match", "Captures", "compile"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.regex missing export %q", name)
			continue
		}
		if !sym.Pub {
			t.Errorf("std.regex export %q is not pub", name)
		}
	}
}

func TestRegexPackageTypeChecks(t *testing.T) {
	src := []byte(`use std.regex

pub fn hasWord(text: String) -> Bool {
    let re = regex.compile("[a-z]+").unwrap()
    re.matches(text)
}

pub fn firstWord(text: String) -> String {
    let re = regex.compile("[a-z]+").unwrap()
    match re.find(text) {
        Some(m) -> m.text,
        None -> "",
    }
}

pub fn namedWord(text: String) -> String {
    let re = regex.compile("(?P<word>[a-z]+)").unwrap()
    match re.captures(text) {
        Some(caps) -> match caps.named("word") {
            Some(word) -> word,
            None -> "",
        },
        None -> "",
    }
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	for _, d := range parseDiags {
		if d.Severity == diag.Error {
			t.Fatalf("parse error: %s", d.Error())
		}
	}
	reg := Load()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	for _, d := range append(res.Diags, chk.Diags...) {
		if d.Severity == diag.Error {
			t.Errorf("unexpected std.regex diagnostic: %s", d.Error())
		}
	}
}

func TestEncodingModuleCoverage(t *testing.T) {
	reg := Load()
	mod := reg.Modules["encoding"]
	if mod == nil || mod.Package == nil {
		t.Fatal("std.encoding not loaded")
	}
	for _, name := range []string{"Base64Url", "Base64", "Hex", "UrlEncoding", "base64", "hex", "url"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.encoding missing export %q", name)
			continue
		}
		if !sym.Pub {
			t.Errorf("std.encoding export %q is not pub", name)
		}
	}
}

func TestEncodingPackageTypeChecks(t *testing.T) {
	src := []byte(`use std.encoding

pub fn roundTrip(text: String) -> Bool {
    let raw: Bytes = encoding.hex.decode("6869").unwrap()
    let b64: String = encoding.base64.encode(raw)
    let decoded: Bytes = encoding.base64.decode(b64).unwrap()
    let urlB64: String = encoding.base64.url.encode(decoded)
    let urlDecoded: Bytes = encoding.base64.url.decode(urlB64).unwrap()
    let safe: String = encoding.url.encode(text)
    let back: String = encoding.url.decode(safe).unwrap()
    back == text
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	for _, d := range parseDiags {
		if d.Severity == diag.Error {
			t.Fatalf("parse error: %s", d.Error())
		}
	}
	reg := Load()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	for _, d := range append(res.Diags, chk.Diags...) {
		if d.Severity == diag.Error {
			t.Errorf("unexpected std.encoding diagnostic: %s", d.Error())
		}
	}
}

func TestEnvModuleCoverage(t *testing.T) {
	reg := Load()
	mod := reg.Modules["env"]
	if mod == nil || mod.Package == nil {
		t.Fatal("std.env not loaded")
	}
	for _, name := range []string{"args", "get", "require", "set", "unset", "vars", "currentDir", "setCurrentDir"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.env missing export %q", name)
			continue
		}
		if !sym.Pub {
			t.Errorf("std.env export %q is not pub", name)
		}
	}
}

func TestEnvPackageTypeChecks(t *testing.T) {
	src := []byte(`use std.env

pub fn smoke(name: String) -> Result<String, Error> {
    let args: List<String> = env.args()
    let maybe: String? = env.get(name)
    env.set(name, "x")?
    let required: String = env.require(name)?
    let all: Map<String, String> = env.vars()
    env.unset(name)?
    let cwd: String = env.currentDir()?
    env.setCurrentDir(cwd)?
    Ok(required)
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	for _, d := range parseDiags {
		if d.Severity == diag.Error {
			t.Fatalf("parse error: %s", d.Error())
		}
	}
	reg := Load()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	for _, d := range append(res.Diags, chk.Diags...) {
		if d.Severity == diag.Error {
			t.Errorf("unexpected std.env diagnostic: %s", d.Error())
		}
	}
}

func TestCSVModuleCoverage(t *testing.T) {
	reg := Load()
	mod := reg.Modules["csv"]
	if mod == nil || mod.Package == nil {
		t.Fatal("std.csv not loaded")
	}
	for _, name := range []string{"CsvOptions", "encode", "encodeWith", "decode", "decodeHeaders", "decodeWith"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.csv missing export %q", name)
			continue
		}
		if !sym.Pub {
			t.Errorf("std.csv export %q is not pub", name)
		}
	}
}

func TestCSVPackageTypeChecks(t *testing.T) {
	src := []byte(`use std.csv

pub fn smoke(text: String) -> Result<String, Error> {
    let rows: List<List<String>> = csv.decode(text)?
    let records: List<Map<String, String>> = csv.decodeHeaders(text)?
    let opts = csv.CsvOptions { delimiter: ';', quote: '"', trimSpace: true }
    let out: String = csv.encodeWith(rows, opts)
    let again: List<List<String>> = csv.decodeWith(out, opts)?
    Ok(csv.encode(again))
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	for _, d := range parseDiags {
		if d.Severity == diag.Error {
			t.Fatalf("parse error: %s", d.Error())
		}
	}
	reg := Load()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	for _, d := range append(res.Diags, chk.Diags...) {
		if d.Severity == diag.Error {
			t.Errorf("unexpected std.csv diagnostic: %s", d.Error())
		}
	}
}

func TestCompressModuleCoverage(t *testing.T) {
	reg := Load()
	mod := reg.Modules["compress"]
	if mod == nil || mod.Package == nil {
		t.Fatal("std.compress not loaded")
	}
	for _, name := range []string{"Gzip", "gzip"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.compress missing export %q", name)
			continue
		}
		if !sym.Pub {
			t.Errorf("std.compress export %q is not pub", name)
		}
	}
}

func TestCompressPackageTypeChecks(t *testing.T) {
	src := []byte(`use std.compress

pub fn roundTrip(data: Bytes) -> Bytes {
    let zipped: Bytes = compress.gzip.encode(data)
    compress.gzip.decode(zipped).unwrap()
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	for _, d := range parseDiags {
		if d.Severity == diag.Error {
			t.Fatalf("parse error: %s", d.Error())
		}
	}
	reg := Load()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	for _, d := range append(res.Diags, chk.Diags...) {
		if d.Severity == diag.Error {
			t.Errorf("unexpected std.compress diagnostic: %s", d.Error())
		}
	}
}

func TestCryptoModuleCoverage(t *testing.T) {
	reg := Load()
	mod := reg.Modules["crypto"]
	if mod == nil || mod.Package == nil {
		t.Fatal("std.crypto not loaded")
	}
	for _, name := range []string{
		"Hmac", "hmac",
		"sha256", "sha512", "sha1", "md5",
		"randomBytes", "constantTimeEq",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.crypto missing export %q", name)
			continue
		}
		if !sym.Pub {
			t.Errorf("std.crypto export %q is not pub", name)
		}
	}
}

func TestCryptoPackageTypeChecks(t *testing.T) {
	src := []byte(`use std.crypto

pub fn digest(data: Bytes, key: Bytes) -> Bool {
    let sha: Bytes = crypto.sha256(data)
    let mac: Bytes = crypto.hmac.sha256(key, data)
    let secret: Bytes = crypto.randomBytes(8)
    crypto.constantTimeEq(sha, crypto.sha256(data)) && secret.len() == 8 && mac.len() == 32
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	for _, d := range parseDiags {
		if d.Severity == diag.Error {
			t.Fatalf("parse error: %s", d.Error())
		}
	}
	reg := Load()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	for _, d := range append(res.Diags, chk.Diags...) {
		if d.Severity == diag.Error {
			t.Errorf("unexpected std.crypto diagnostic: %s", d.Error())
		}
	}
}

func TestUUIDModuleCoverage(t *testing.T) {
	reg := Load()
	mod := reg.Modules["uuid"]
	if mod == nil || mod.Package == nil {
		t.Fatal("std.uuid not loaded")
	}
	for _, name := range []string{"Uuid", "v4", "v7", "parse", "nil"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Errorf("std.uuid missing export %q", name)
			continue
		}
		if !sym.Pub {
			t.Errorf("std.uuid export %q is not pub", name)
		}
	}
}

func TestUUIDPackageTypeChecks(t *testing.T) {
	src := []byte(`use std.uuid

pub fn smoke() -> Bool {
    let id = uuid.v4()
    let text: String = id.toString()
    let parsed = uuid.parse(text).unwrap()
    let sortable = uuid.v7()
    let zero = uuid.nil()
    parsed.toBytes().len() == 16 && sortable.toBytes().len() == 16 && zero.toString() == "00000000-0000-0000-0000-000000000000"
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	for _, d := range parseDiags {
		if d.Severity == diag.Error {
			t.Fatalf("parse error: %s", d.Error())
		}
	}
	reg := Load()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	for _, d := range append(res.Diags, chk.Diags...) {
		if d.Severity == diag.Error {
			t.Errorf("unexpected std.uuid diagnostic: %s", d.Error())
		}
	}
}

// TestPrimitivesNotExposedAsModule pins that the `primitives/` stubs
// populate the Primitives table only — they must not leak as if they
// were `std.int` or `std.float` modules addressable from user code.
func TestPrimitivesNotExposedAsModule(t *testing.T) {
	r := Load()
	for _, name := range []string{"int", "float"} {
		if _, ok := r.Modules[name]; ok {
			t.Errorf("primitives stub %q leaked into Modules", name)
		}
	}
}

// TestPrimitivesIntMethods asserts that integer primitive kinds each
// pick up the methods declared on the `#[intrinsic_methods(Int, ...)]`
// placeholder struct. The kinds come straight from the annotation's
// positional args, so adding a kind there is the only place that edit
// has to happen.
func TestPrimitivesIntMethods(t *testing.T) {
	r := Load()
	intKinds := []types.PrimitiveKind{
		types.PInt, types.PInt8, types.PInt16, types.PInt32, types.PInt64,
		types.PUInt8, types.PUInt16, types.PUInt32, types.PUInt64, types.PByte,
	}
	for _, k := range intKinds {
		methods := r.Primitives[k]
		if methods == nil {
			t.Errorf("primitive kind %v has no method table", k)
			continue
		}
		for _, m := range []string{"abs", "min", "max"} {
			if _, ok := methods[m]; !ok {
				t.Errorf("primitive kind %v missing method %q", k, m)
			}
		}
	}
}

// TestPrimitiveMethodCheckerIntegration is the end-to-end proof that
// the checker picks up intrinsic primitive methods when passed the
// stdlib Opts. `x.abs()` on an Int must type-check cleanly (no
// "no method `abs` on type `Int`" diagnostic) and the call site's
// inferred type must match the method's declared return.
func TestPrimitiveMethodCheckerIntegration(t *testing.T) {
	src := []byte(`pub fn demo() -> Int {
    let x: Int = 5
    x.abs()
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	for _, d := range parseDiags {
		if d.Severity == diag.Error {
			t.Fatalf("parse: %s", d.Error())
		}
	}
	reg := Load()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	for _, d := range chk.Diags {
		if d.Severity == diag.Error {
			t.Errorf("unexpected checker error: %s", d.Error())
		}
	}
}

// TestRegistryShadowsEscapeHatch_Abs pins that Registry-sourced
// primitive methods dispatch ahead of the legacy stdlibCallReturn
// escape hatch. `abs` is not listed in the escape hatch, so
// type-checking success here can only come from the Registry.
func TestRegistryShadowsEscapeHatch_Abs(t *testing.T) {
	src := []byte(`pub fn demo() -> Int {
    let x: Int = 5
    x.abs()
}
`)
	file, _ := parser.ParseDiagnostics(src)
	reg := Load()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives, ResultMethods: reg.ResultMethods})
	for _, d := range chk.Diags {
		if d.Code == diag.CodeUnknownMethod {
			t.Errorf("Registry dispatch regressed — abs fell through to escape hatch: %s",
				d.Error())
		}
	}
}

// TestPrimitiveMethodWithoutOptsStillFails confirms that the checker
// preserves its legacy behavior when Opts is omitted: with no primitive
// table attached, `x.abs()` falls through to the existing escape hatch
// or the "no method" error. The important invariant is that adding
// Opts is purely additive — omitting it never widens what the checker
// accepts relative to prior behavior.
func TestPrimitiveMethodWithoutOptsStillFails(t *testing.T) {
	src := []byte(`pub fn demo() -> Int {
    let x: Int = 5
    x.abs()
}
`)
	file, _ := parser.ParseDiagnostics(src)
	res := resolve.File(file, resolve.NewPrelude())
	chk := check.File(file, res)
	// Without Opts, `abs` is not a known stdlib fallback (stdlibMethods
	// only covers len/isEmpty/…); expect a CodeUnknownMethod diagnostic.
	var sawUnknown bool
	for _, d := range chk.Diags {
		if d.Code == diag.CodeUnknownMethod {
			sawUnknown = true
			break
		}
	}
	if !sawUnknown {
		t.Errorf("expected CodeUnknownMethod without Opts; got %d diags: %v",
			len(chk.Diags), diagCodes(chk.Diags))
	}
}

// TestPrimitivesFloatMethods mirrors the integer case for float kinds.
func TestPrimitivesFloatMethods(t *testing.T) {
	r := Load()
	for _, k := range []types.PrimitiveKind{types.PFloat, types.PFloat32, types.PFloat64} {
		methods := r.Primitives[k]
		if methods == nil {
			t.Errorf("primitive kind %v has no method table", k)
			continue
		}
		for _, m := range []string{"abs", "sqrt", "isNaN"} {
			if _, ok := methods[m]; !ok {
				t.Errorf("primitive kind %v missing method %q", k, m)
			}
		}
	}
}

// TestLookupPackageMissesNonStdlib confirms that paths without the
// "std." prefix or unknown module names yield nil, letting the
// workspace fall through to its default handling.
func TestLookupPackageMissesNonStdlib(t *testing.T) {
	r := Load()
	cases := []string{
		"",
		"io",  // missing "std." prefix
		"std", // bare prefix
		"std.nonexistent",
		"github.com/foo/bar",
	}
	for _, c := range cases {
		if pkg := r.LookupPackage(c); pkg != nil {
			t.Errorf("LookupPackage(%q) = non-nil; want nil", c)
		}
	}
}
