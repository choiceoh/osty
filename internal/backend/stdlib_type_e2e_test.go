package backend

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/llvmgen"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func requireIRCallsSpecializedMethod(t *testing.T, irText, method string) {
	t.Helper()
	pattern := regexp.MustCompile(`call [^@]+@[^[:space:]]*__` + regexp.QuoteMeta(method) + `[^[:space:]]*\(`)
	if !pattern.MatchString(irText) {
		t.Fatalf("expected a call to specialized method %q in generated IR:\n%s", method, irText)
	}
}

// TestPhase2MangledNamesAreValidLLVMIdentifiers locks in the Phase 2
// fix for LLVM016: `Map<String, V>` now mangles to a name built from
// valid identifier characters (`Ss` for String, `By` for Bytes),
// instead of `?` placeholders that llvmIsIdent rejects. Without this
// the whole `GenerateModule` entry point fails before reaching the
// interesting method-emission code.
func TestPhase2MangledNamesAreValidLLVMIdentifiers(t *testing.T) {
	src := `fn touch(m: Map<String, Int>) -> Int { m.len() }
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)
	for _, d := range monoMod.Decls {
		sd, ok := d.(*ir.StructDecl)
		if !ok || len(sd.Generics) != 0 {
			continue
		}
		if !strings.Contains(sd.Name, "Map") {
			continue
		}
		if strings.ContainsRune(sd.Name, '?') ||
			strings.ContainsRune(sd.Name, '<') ||
			strings.ContainsRune(sd.Name, '>') ||
			strings.ContainsRune(sd.Name, ' ') {
			t.Fatalf("specialized Map name %q contains illegal LLVM identifier chars — mangler regressed on String/Bytes", sd.Name)
		}
	}
}

// TestPhase2SpecializedBuiltinBodylessMethodsAreDropped locks in the
// Phase 2 fix for LLVM010: the AST bridge
// (legacyStructDeclFromIR) strips bodyless intrinsic methods from
// `_ZTS…` specialized structs so the legacy emitter doesn't wall on
// "function has no body". The runtime backs those calls at dispatch
// time via osty_rt_map_* helpers, so the LLVM layer never needs the
// empty definitions.
func TestPhase2SpecializedBuiltinBodylessMethodsAreDropped(t *testing.T) {
	src := `fn touch(m: Map<String, Int>) -> Int { m.len() }
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)
	opts := llvmgen.Options{
		PackageName: "main",
		SourcePath:  "/tmp/phase2_bodyless.osty",
		Source:      []byte(src),
	}
	_, err := llvmgen.GenerateModule(monoMod, opts)
	if err != nil && strings.Contains(err.Error(), "LLVM010") && strings.Contains(err.Error(), "has no body") {
		t.Fatalf("bodyless intrinsic method leaked into LLVM emission: %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "LLVM016") {
		t.Fatalf("mangled specialized name still failing ASCII-identifier check: %v", err)
	}
	// Any other error is the next Phase 2 blocker — not a regression
	// of what this test covers.
	if err != nil {
		t.Logf("later Phase 2 blocker (not the bodyless one this test covers): %v", err)
	}
}

// TestPhase2gUserCallsiteDispatchesToSpecializedMethod locks the
// Phase 2g fix: when user code calls `m.forEach(f)` /
// `m.getOr(k, d)` / etc. on a `Map<String, Int>` receiver,
// `userCallTarget` now consults `specializedBuiltinMangledForSurface`
// to find the specialized method registered under the `_ZTSN…`
// mangled owner type — instead of walling at the surface-typed
// `ptr` receiver with "no methods registered". The inner wall
// from the specialized body (f(key, value) → Ident call) is a
// separate Phase 2h concern.
func TestPhase2gUserCallsiteDispatchesToSpecializedMethod(t *testing.T) {
	src := `fn printEntry(k: String, v: Int) { println(v) }
fn walk(m: Map<String, Int>) {
    m.forEach(printEntry)
}
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)
	opts := llvmgen.Options{
		PackageName: "main",
		SourcePath:  "/tmp/phase2g_foreach.osty",
		Source:      []byte(src),
	}
	_, err := llvmgen.GenerateModule(monoMod, opts)
	// Phase 2g advances the wall past the user-side `m.forEach(…)`
	// callsite (which previously failed at `got *ast.FieldExpr at
	// 3:5`) into the specialized forEach body's indirect call
	// (`got *ast.Ident at 346:13`). A regression would surface the
	// earlier form.
	if err != nil && strings.Contains(err.Error(), "got *ast.FieldExpr") {
		t.Fatalf("user callsite m.forEach(f) regressed to FieldExpr wall — specialized dispatch broken: %v", err)
	}
	if err != nil {
		t.Logf("Phase 2 pipeline still incomplete past user callsite (expected): %v", err)
	}
}

// TestPhase2gContainsKeyUserCallsiteUsesSpecializedMethod locks the
// follow-up to Phase 2g: once a specialized Map.containsKey body is
// available, the user callsite should no longer hit the legacy
// `osty_rt_map_contains_*` shortcut. Instead it must dispatch to the
// specialized method body (`self.get(key).isSome()`), which keeps the
// monomorphized helper stack authoritative end-to-end.
func TestPhase2gContainsKeyUserCallsiteUsesSpecializedMethod(t *testing.T) {
	src := `fn touch(m: Map<String, Int>, k: String) -> Bool {
    m.containsKey(k)
}
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)
	opts := llvmgen.Options{
		PackageName: "main",
		SourcePath:  "/tmp/phase2g_containskey.osty",
		Source:      []byte(src),
	}
	irOut, err := llvmgen.GenerateModule(monoMod, opts)
	if err != nil {
		t.Fatalf("containsKey specialized dispatch failed: %v", err)
	}
	got := string(irOut)
	if strings.Contains(got, "osty_rt_map_contains_") {
		t.Fatalf("containsKey user callsite still routed through legacy intrinsic:\n%s", got)
	}
	requireIRCallsSpecializedMethod(t, got, "containsKey")
}

func TestPhase2gGetOrUserCallsiteUsesSpecializedMethod(t *testing.T) {
	src := `fn touch(m: Map<String, Int>, k: String) -> Int {
    m.getOr(k, 0)
}
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)
	opts := llvmgen.Options{
		PackageName: "main",
		SourcePath:  "/tmp/phase2g_getor.osty",
		Source:      []byte(src),
	}
	irOut, err := llvmgen.GenerateModule(monoMod, opts)
	if err != nil {
		t.Fatalf("getOr specialized dispatch failed: %v", err)
	}
	requireIRCallsSpecializedMethod(t, string(irOut), "getOr")
}

func TestPhase2gMapValuesUserCallsiteUsesSpecializedMethod(t *testing.T) {
	src := `fn stringify(n: Int) -> String { "{n}" }
fn touch(m: Map<String, Int>) -> Map<String, String> {
    m.mapValues(stringify)
}
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)
	opts := llvmgen.Options{
		PackageName: "main",
		SourcePath:  "/tmp/phase2g_mapvalues.osty",
		Source:      []byte(src),
	}
	irOut, err := llvmgen.GenerateModule(monoMod, opts)
	if err != nil {
		t.Fatalf("mapValues specialized dispatch failed: %v", err)
	}
	requireIRCallsSpecializedMethod(t, string(irOut), "mapValues")
}

// Statement-position mutators intentionally keep their dedicated
// lowering even when a specialized stdlib body exists: update's
// hand-emit wraps get + callback + insert in one recursive per-map
// critical section so no concurrent mutator can slip between the read
// and the write. Specialized user-call dispatch must not bypass that.
func TestPhase2gUpdateUserCallsiteKeepsLockedLowering(t *testing.T) {
	src := `fn bump(n: Int?) -> Int { (n ?? 0) + 1 }
fn touch(m: Map<String, Int>, k: String) {
    m.update(k, bump)
}
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)
	opts := llvmgen.Options{
		PackageName: "main",
		SourcePath:  "/tmp/phase2g_update.osty",
		Source:      []byte(src),
	}
	irOut, err := llvmgen.GenerateModule(monoMod, opts)
	if err != nil {
		t.Fatalf("update lowering failed: %v", err)
	}
	got := string(irOut)
	for _, want := range []string{
		"call void @osty_rt_map_lock(",
		"call void @osty_rt_map_unlock(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("update must preserve locked lowering, missing %q:\n%s", want, got)
		}
	}
}

// TestPhase2fOptionIsSomeLowersAsNullCheck locks the Phase 2f fix:
// `.isSome()` / `.isNone()` calls on a receiver whose source type
// is `T?` (OptionalType) now lower directly to an LLVM null check,
// no matter what T is. This unblocks the stdlib body of
// `Map.containsKey` — `self.get(key).isSome()` — inside specialized
// Map method bodies, where `self.get(key)` feeds through the
// Phase 2e staticMapMethodSourceType into isSome's intrinsic path.
func TestPhase2fOptionIsSomeLowersAsNullCheck(t *testing.T) {
	src := `fn check(x: Int?) -> Bool { x.isSome() }
fn checkNone(x: String?) -> Bool { x.isNone() }
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)
	opts := llvmgen.Options{
		PackageName: "main",
		SourcePath:  "/tmp/phase2f_isSome.osty",
		Source:      []byte(src),
	}
	out, err := llvmgen.GenerateModule(monoMod, opts)
	if err != nil {
		t.Fatalf("isSome/isNone lowering failed: %v", err)
	}
	ir := string(out)
	// Both isSome (ne) and isNone (eq) null checks must appear.
	if !strings.Contains(ir, "icmp ne ptr") {
		t.Errorf("expected `icmp ne ptr ... , null` for isSome, IR:\n%s", ir)
	}
	if !strings.Contains(ir, "icmp eq ptr") {
		t.Errorf("expected `icmp eq ptr ... , null` for isNone, IR:\n%s", ir)
	}
}

// TestPhase2eCoalesceSourceTypeRecoveredForMapGet locks the Phase
// 2e fix: `self.get(k) ?? default` inside specialized Map method
// bodies (notably Map.getOr) now reports `Option<V>` as the left
// source type through the new staticMapMethodSourceType pathway.
// Before this, the coalesce emitter walled on
// `LLVM011 ?? left source type unknown` because `self.get(k)`'s
// return type wasn't visible through `staticExprSourceType`'s
// CallExpr dispatch — Map's intrinsic methods aren't registered in
// `g.methods` the way user-defined ones are.
func TestPhase2eCoalesceSourceTypeRecoveredForMapGet(t *testing.T) {
	src := `fn touch(m: Map<String, Int>, k: String) -> Int { m.getOr(k, 0) }
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)
	opts := llvmgen.Options{
		PackageName: "main",
		SourcePath:  "/tmp/phase2e_coalesce.osty",
		Source:      []byte(src),
	}
	_, err := llvmgen.GenerateModule(monoMod, opts)
	// Phase 2e turns the outermost wall from LLVM011 ?? source-type
	// into LLVM015 *ast.CallExpr.isSome (next Phase 2f concern).
	if err != nil && strings.Contains(err.Error(), "?? left source type unknown") {
		t.Fatalf("`??` source-type recovery regressed for Map.get(k): %v", err)
	}
	if err != nil {
		t.Logf("Phase 2 pipeline still incomplete past the coalesce wall (expected): %v", err)
	}
}

// TestPhase2dSelfBindingRoutesIntrinsicDispatch locks the Phase 2d
// fix: inside a specialized Map method body, `self.len()` / `self.get(k)`
// / `self.insert(k, v)` intrinsic dispatch now fires via the
// surface-level mapKeyTyp / mapValueTyp populated on the receiver
// paramInfo. Before this fix, the receiver carried only the mangled
// struct type (e.g. `%_ZTSN4main3MapISslEE`) and failed the
// `mapMethodInfo` base-type check, falling through to user method
// dispatch which can't find the stripped intrinsic methods.
func TestPhase2dSelfBindingRoutesIntrinsicDispatch(t *testing.T) {
	src := `fn touch(m: Map<String, Int>) -> Int { m.len() }
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)
	opts := llvmgen.Options{
		PackageName: "main",
		SourcePath:  "/tmp/phase2d_self_intrinsic.osty",
		Source:      []byte(src),
	}
	_, err := llvmgen.GenerateModule(monoMod, opts)
	// Before Phase 2d: the emitter walled on
	// `LLVM015 call: call target *ast.FieldExpr (self.len)` from
	// within Map.isEmpty / Map.getOr / ... bodies. Phase 2d moves
	// the wall past that point. The exact next-blocker shape is
	// pinned by TestPhase2MonomorphMapReachesGenerateModule; all
	// this test asserts is that `self.len` is no longer the
	// outermost failure.
	if err != nil && strings.Contains(err.Error(), "self.len") {
		t.Fatalf("self-binding intrinsic dispatch regressed — receiver-type override to ptr didn't take effect: %v", err)
	}
	if err != nil {
		t.Logf("Phase 2 pipeline still incomplete past self.len (expected): %v", err)
	}
}

// TestPhase2cSpecializedBuiltinsCarryBuiltinSource verifies the
// monomorphizer records BuiltinSource / BuiltinSourceArgs on every
// specialized struct or enum cloned from a stdlib built-in generic
// template. Without this marker, downstream stages can't re-associate
// a `_ZTS…` mangled type back with its surface Map<K, V> / Option<T>
// form, and the llvm backend's intrinsic dispatch (keyed on the
// surface name) can't fire for methods on specialized builtins.
func TestPhase2cSpecializedBuiltinsCarryBuiltinSource(t *testing.T) {
	src := `fn touch(m: Map<String, Int>, k: String) -> Bool {
    m.containsKey(k)
}
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)

	foundMap := false
	for _, d := range monoMod.Decls {
		sd, ok := d.(*ir.StructDecl)
		if !ok {
			continue
		}
		if sd.BuiltinSource == "Map" {
			foundMap = true
			if len(sd.BuiltinSourceArgs) != 2 {
				t.Errorf("Map specialization has %d BuiltinSourceArgs, want 2", len(sd.BuiltinSourceArgs))
			}
		}
	}
	if !foundMap {
		t.Errorf("expected a Map-sourced specialization with BuiltinSource=\"Map\"")
	}
	// Option/Result enum tagging is covered by the EnumDecl clone +
	// monomorph additions, but only a NamedType reference to
	// Option<T> (not `T?` surface sugar) drives an enum
	// specialization today. A focused Option test belongs with Phase
	// 2d when the ?/chain handling is generalized.
}

// TestPhase2cTurbofishExprStrippedFromSpecializedBodies verifies the
// AST bridge elides TypeArgs on MethodCalls whose receiver is a
// `_ZTS…` mangled built-in. Stale TypeArgs there turned every
// `self.get(k)` / `self.insert(k, v)` inside the specialized
// Map.containsKey / Map.update / … bodies into an `*ast.TurbofishExpr`
// that the legacy emitter can't dispatch, producing LLVM015.
func TestPhase2cTurbofishExprStrippedFromSpecializedBodies(t *testing.T) {
	src := `fn touch(m: Map<String, Int>, k: String) -> Bool {
    m.containsKey(k)
}
fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)
	opts := llvmgen.Options{
		PackageName: "main",
		SourcePath:  "/tmp/phase2c_turbofish.osty",
		Source:      []byte(src),
	}
	_, err := llvmgen.GenerateModule(monoMod, opts)
	if err != nil && strings.Contains(err.Error(), "TurbofishExpr") {
		t.Fatalf("TurbofishExpr leaked past the AST bridge — legacy emitter walled on stale TypeArgs: %v", err)
	}
	// Any other error is the next Phase 2 blocker — not what this
	// test covers.
	if err != nil {
		t.Logf("later Phase 2 blocker (not the TurbofishExpr one this test covers): %v", err)
	}
}

// TestPhase2cDiagnoseTurbofishSource probes which specialized-method
// expression still carries TypeArgs after monomorph — that's the
// root of the LLVM015 TurbofishExpr wall. The test lowers + injects +
// monomorphs a simple `m.containsKey(k)` program, then walks every
// CallExpr/MethodCall in the monomorphized IR, reporting any that
// still have non-empty TypeArgs (which means monomorph didn't
// substitute them out).
func TestPhase2cDiagnoseTurbofishSource(t *testing.T) {
	src := `fn touch(m: Map<String, Int>, k: String) -> Bool {
    m.containsKey(k)
}

fn main() {}
`
	monoMod := pipelineThroughMonomorph(t, src)

	var offenders []string
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		switch x := n.(type) {
		case *ir.CallExpr:
			if len(x.TypeArgs) > 0 {
				offenders = append(offenders, fmt.Sprintf("CallExpr(callee=%T) TypeArgs=%d", x.Callee, len(x.TypeArgs)))
			}
		case *ir.MethodCall:
			if len(x.TypeArgs) > 0 {
				offenders = append(offenders, fmt.Sprintf("MethodCall(name=%q) TypeArgs=%d", x.Name, len(x.TypeArgs)))
			}
		}
		return true
	}), monoMod)

	if len(offenders) > 0 {
		t.Logf("Phase 2c diagnosis: %d call(s) still carry TypeArgs after monomorph:", len(offenders))
		for _, o := range offenders {
			t.Logf("  - %s", o)
		}
		t.Fatalf("stale TypeArgs leaked past monomorph; expected 0 offenders")
	} else {
		t.Logf("no stale TypeArgs remain after monomorph")
	}
}

// TestPhase2MonomorphMapReachesGenerateModule is the smallest viable
// end-to-end probe for Option B's Phase 2 gap: after Phase 1's
// monomorphization produces `Map$String$Int` with 22 methods, does
// llvmgen.GenerateModule survive consuming that module?
//
// This test drives the full pipeline in-process (parse → resolve →
// check → ir.Lower → inject stdlib types → monomorphize → llvmgen)
// and records what the backend does with the specialized Map.
// Failures here are informational — they pin down which llvmgen
// subsystem needs to learn about specialized builtin types, so the
// Phase 2 fix can target that subsystem directly.
func TestPhase2MonomorphMapReachesGenerateModule(t *testing.T) {
	src := `fn touch(m: Map<String, Int>, k: String) -> Bool {
    m.containsKey(k)
}

fn main() {}
`
	file, parseDiags := parser.ParseCanonical([]byte(src))
	for _, d := range parseDiags {
		if d.Severity == 0 {
			t.Fatalf("parse error: %v", d)
		}
	}
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.SelfhostFile(file, res, check.Opts{
		Source: []byte(src),
		Stdlib: reg,
	})
	mod, _ := ir.Lower("main", file, res, chk)
	if mod == nil {
		t.Fatalf("ir.Lower returned nil")
	}

	// Phase 1: inject + monomorph.
	injected, _ := injectReachableStdlibTypes(mod, reg)
	mod.Decls = append(mod.Decls, injected...)
	monoMod, monoErrs := ir.Monomorphize(mod)
	if monoMod == nil {
		t.Fatalf("Monomorphize returned nil: %v", monoErrs)
	}

	// Phase 2 probe: feed to the LLVM backend.
	opts := llvmgen.Options{
		PackageName: "main",
		SourcePath:  "/tmp/phase2.osty",
		Source:      []byte(src),
	}
	irOut, err := llvmgen.GenerateModule(monoMod, opts)
	if err != nil {
		// Document the next-blocker shape without failing CI. Each
		// Phase 2 PR peels one layer; this test reports the current
		// outermost wall so the next iteration can target it
		// precisely.
		t.Logf("Phase 2 next-blocker: llvmgen.GenerateModule walls on monomorphized Map<String,Int>: %v", err)
		t.Skipf("Phase 2 pipeline not yet complete; see log above for the current outermost failure")
	}
	got := string(irOut)
	t.Logf("Phase 2 observation: LLVM IR size = %d bytes", len(got))

	// Does the emission include any `_ZTSN...Map...` symbol? That
	// would prove the specialized method actually made it into the
	// textual LLVM output, which is what we'd retire the hand-emit
	// onto in Phase 5.
	seesSpecialized := strings.Contains(got, "_ZTSN") && strings.Contains(got, "Map")
	if !seesSpecialized {
		t.Logf("observation: no Map$... symbol in generated IR — likely the existing emitMapMethodCall intercept still fired on 'Map' source name")
	} else {
		t.Logf("observation: specialized Map symbol present in IR")
	}

	// Also record whether the existing `osty_rt_map_contains_*`
	// intrinsic still shows — if so, the old path is still in use.
	if strings.Contains(got, "osty_rt_map_contains_") {
		t.Logf("observation: legacy contains intrinsic symbol present — hand-emit path is winning over the specialized method")
	}
}

// pipelineThroughMonomorph runs parse → resolve → check → ir.Lower →
// inject stdlib types → monomorphize and returns the resulting IR
// module. Shared by the Phase 2 probe tests so each focuses on one
// assertion rather than repeating the pipeline boilerplate.
func pipelineThroughMonomorph(t *testing.T, src string) *ir.Module {
	t.Helper()
	file, parseDiags := parser.ParseCanonical([]byte(src))
	for _, d := range parseDiags {
		if d.Severity == 0 {
			t.Fatalf("parse error: %v", d)
		}
	}
	reg := stdlib.LoadCached()
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), reg)
	chk := check.SelfhostFile(file, res, check.Opts{
		Source: []byte(src),
		Stdlib: reg,
	})
	mod, _ := ir.Lower("main", file, res, chk)
	if mod == nil {
		t.Fatalf("ir.Lower returned nil")
	}
	injected, _ := injectReachableStdlibTypes(mod, reg)
	mod.Decls = append(mod.Decls, injected...)
	monoMod, monoErrs := ir.Monomorphize(mod)
	if monoMod == nil {
		t.Fatalf("Monomorphize returned nil: %v", monoErrs)
	}
	return monoMod
}
