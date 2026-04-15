package stdlib

import (
	"testing"

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

// TestTier2ModuleCoverage mirrors TestTier1ModuleCoverage for the
// Tier 2 modules listed in spec §10.2. Only the ones whose stubs have
// landed are covered here; more names are added as each module grows.
func TestTier2ModuleCoverage(t *testing.T) {
	reg := Load()
	cases := []struct {
		module string
		names  []string
	}{
		{"env", []string{"args", "get", "set", "unset", "vars"}},
		{"iter", []string{"Iter", "from", "empty", "once", "repeat", "range"}},
		{"regex", []string{"Regex", "Match", "Captures", "RegexError",
			"compile", "matches", "find", "findAll",
			"replace", "replaceAll", "split", "captures"}},
		{"log", []string{"Level", "LogValue", "Fields", "Record", "Handler",
			"TextHandler", "JsonHandler", "ToLogValue",
			"debug", "info", "warn", "error", "setLevel"}},
		{"json", []string{"Json", "Encode", "Decode",
			"encode", "encodePretty", "decode", "parse", "encodeValue"}},
		{"os", []string{"Output", "Signal",
			"exec", "execShell", "exit", "pid", "hostname", "onSignal"}},
		{"http", []string{"Method", "Request", "Response", "Client", "Server",
			"get", "post", "send", "client", "server"}},
		{"time", []string{"Duration", "Instant", "Zone", "ZonedTime", "Weekday",
			"ISO_8601", "RFC_3339", "RFC_2822",
			"now", "sleep", "parse", "zone", "local", "utc"}},
		{"thread", []string{"Group", "Handle", "Chan", "SelectBuilder", "Cancelled",
			"collectAll", "race", "chan", "select", "isCancelled", "checkCancelled"}},
		{"sync", []string{"Mutex", "RwLock", "AtomicInt", "AtomicBool",
			"mutex", "rwlock", "atomicInt", "atomicBool"}},
		{"encoding", []string{"base64Encode", "base64Decode",
			"base64UrlEncode", "base64UrlDecode",
			"hexEncode", "hexDecode", "urlEncode", "urlDecode"}},
		{"crypto", []string{"sha256", "sha512", "sha1", "md5",
			"hmacSha256", "hmacSha512", "randomBytes", "constantTimeEq"}},
		{"uuid", []string{"Uuid", "v4", "v7", "parse", "nil"}},
		{"random", []string{"Rng", "default", "seeded"}},
		{"url", []string{"Url", "UrlBuilder", "parse", "join", "builder"}},
		{"math", []string{"PI", "E", "TAU", "INFINITY", "NAN",
			"sin", "cos", "tan", "asin", "acos", "atan", "atan2",
			"sinh", "cosh", "tanh",
			"exp", "log", "logBase", "log2", "log10",
			"sqrt", "cbrt", "pow",
			"floor", "ceil", "round", "trunc",
			"abs", "min", "max", "hypot"}},
		{"csv", []string{"CsvOptions",
			"encode", "encodeWith", "decode", "decodeHeaders", "decodeWith",
			"defaultOptions"}},
		{"compress", []string{"gzipEncode", "gzipDecode"}},
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

// TestJsonRealizedInOsty pins the in-Osty std.json body: the
// encoder/parser are written in Osty rather than routed through a Go
// builtin, so the public surface — `encodeValue`, `parse` — must
// resolve and type-check when called from user code.
func TestJsonRealizedInOsty(t *testing.T) {
	res := resolveSrc(t, `use std.json

pub fn build() -> String {
    let j = json.ObjectValue({
        "name": json.StringValue("alice"),
        "count": json.NumberValue(3.0),
    })
    json.encodeValue(j)
}

pub fn read(text: String) -> Result<json.Json, Error> {
    json.parse(text)
}
`, Load())
	for _, d := range res.Diags {
		if d.Severity == diag.Error {
			t.Errorf("unexpected diag on json usage: %s", d.Error())
		}
	}
}

// TestRegexRealizedInOsty pins the in-Osty std.regex body: compile /
// matches / findAll / replaceAll resolve and type-check through the
// Osty-implemented Pike-style matcher.
func TestRegexRealizedInOsty(t *testing.T) {
	res := resolveSrc(t, `use std.regex

pub fn anyLetter(text: String) -> Bool {
    match regex.compile("[A-Za-z]+") {
        Ok(re) -> regex.matches(re, text),
        Err(_) -> false,
    }
}

pub fn scrub(text: String) -> String {
    match regex.compile("\\s+") {
        Ok(re) -> regex.replaceAll(re, text, " "),
        Err(_) -> text,
    }
}

pub fn numbers(text: String) -> List<regex.Match> {
    match regex.compile("\\d+") {
        Ok(re) -> regex.findAll(re, text),
        Err(_) -> [],
    }
}
`, Load())
	for _, d := range res.Diags {
		if d.Severity == diag.Error {
			t.Errorf("unexpected diag on regex usage: %s", d.Error())
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
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives})
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
	chk := check.File(file, res, check.Opts{Primitives: reg.Primitives})
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
		"io",             // missing "std." prefix
		"std",            // bare prefix
		"std.nonexistent",
		"github.com/foo/bar",
	}
	for _, c := range cases {
		if pkg := r.LookupPackage(c); pkg != nil {
			t.Errorf("LookupPackage(%q) = non-nil; want nil", c)
		}
	}
}
