package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// Spec §10.20 declares `Int.ns / .us / .ms / .s / .minutes / .h /
// .days / .weeks` and the Float-receiver analogues as Duration
// constructors. The lowering folds the receiver-times-factor into a
// single `mul` + `inttoptr` (Duration's LLVM ABI is an opaque ptr)
// for integer kinds, and `fmul` + `fptosi` + `inttoptr` for floats.
//
// The tests run the full parser → resolve → check → ir.Lower →
// monomorphize → mir.Lower → GenerateFromMIR pipeline because the
// intrinsic lowering lives on the MIR side; the legacy AST emitter
// (generateFromAST) does not see these symbols.

func emitDurationLLVM(t *testing.T, src string) string {
	t.Helper()
	file := parseLLVMGenFile(t, src)
	reg := stdlib.LoadCached()
	res := resolve.ResolveFileSourceDefault([]byte(src), file, reg)
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
	})
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	monoMod, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("monomorphize: %v", monoErrs)
	}
	mirMod := mir.Lower(monoMod)
	out, err := GenerateFromMIR(mirMod, Options{PackageName: "main", SourcePath: "/tmp/dur.osty"})
	if err != nil {
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	return string(out)
}

func TestDurationConstructorIntSecondsLowersToMulAndInttoptr(t *testing.T) {
	got := emitDurationLLVM(t, `fn main() {
    let _ = 5.s()
}
`)
	for _, want := range []string{
		"mul i64 ",
		", 1000000000",
		"inttoptr i64 ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Int.s() missing %q in IR:\n%s", want, got)
		}
	}
}

func TestDurationConstructorIntMinutesUsesSpelledOutFactor(t *testing.T) {
	// `Int.minutes` (not `Int.min`) avoids collision with the existing
	// 2-arg `Int.min(self, other) -> Self` from §10.5.
	got := emitDurationLLVM(t, `fn main() {
    let _ = 30.minutes()
}
`)
	if !strings.Contains(got, ", 60000000000") {
		t.Fatalf("Int.minutes() missing 60s factor in IR:\n%s", got)
	}
}

func TestDurationConstructorIntWeeksFactorIsExact(t *testing.T) {
	got := emitDurationLLVM(t, `fn main() {
    let _ = 1.weeks()
}
`)
	// 7 * 24 * 60 * 60 * 1e9 = 604_800_000_000_000
	if !strings.Contains(got, ", 604800000000000") {
		t.Fatalf("Int.weeks() missing weeks factor in IR:\n%s", got)
	}
}

func TestDurationConstructorNarrowIntWidens(t *testing.T) {
	// Int8 receivers must sext to i64 before scaling so the full
	// nanosecond range is reachable; matches the existing toString
	// widening pattern in emitPrimitiveMethodCall.
	got := emitDurationLLVM(t, `fn main() {
    let n: Int8 = 5
    let _ = n.s()
}
`)
	if !strings.Contains(got, "sext i8 ") {
		t.Fatalf("Int8.s() missing sext widening in IR:\n%s", got)
	}
}

func TestDurationConstructorUnsignedIntZExt(t *testing.T) {
	got := emitDurationLLVM(t, `fn main() {
    let n: UInt32 = 100
    let _ = n.ms()
}
`)
	if !strings.Contains(got, "zext i32 ") {
		t.Fatalf("UInt32.ms() missing zext widening (unsigned receiver) in IR:\n%s", got)
	}
}

func TestDurationConstructorFloatLowersToFMulAndFPToSI(t *testing.T) {
	// Float receivers (`1.5.s()`) multiply as double then truncate.
	got := emitDurationLLVM(t, `fn main() {
    let _ = (1.5).s()
}
`)
	for _, want := range []string{
		"fmul double ",
		// LLVM rejects bare `1e+09`; the lowering emits `1000000000.0`
		// so the constant carries an explicit floating-point form.
		"1000000000.0",
		"fptosi double ",
		"inttoptr i64 ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("Float.s() missing %q in IR:\n%s", want, got)
		}
	}
}

// Spec §10.20 also documents the **paren-less** form `5.s` /
// `100.ms`. The selfhost checker accepts these via
// `elabNullaryMethodFallback` and the Go-host lowerer (lower.go's
// `lowerFieldExpr` → `isParenlessDurationMethod`) routes them to the
// same MethodCall HIR node a paren'd `5.s()` produces, so the LLVM
// IR for the two shapes is identical.
func TestDurationConstructorParenlessFormProducesSameMul(t *testing.T) {
	parenLess := emitDurationLLVM(t, `fn main() {
    let _ = 50.ms
}
`)
	parened := emitDurationLLVM(t, `fn main() {
    let _ = 50.ms()
}
`)
	for _, want := range []string{"mul i64 ", ", 1000000", "inttoptr i64 "} {
		if !strings.Contains(parenLess, want) {
			t.Fatalf("paren-less 50.ms missing %q in IR:\n%s", want, parenLess)
		}
		if !strings.Contains(parened, want) {
			t.Fatalf("paren'd 50.ms() missing %q in IR:\n%s", want, parened)
		}
	}
}

// `time.sleep(d)` is the canonical user-facing API — the spec example
// `time.sleep(100.ms)` exercises both the Duration alias bridge
// (`time.Duration` ↔ bare `Duration`) and the IntrinsicSleep route
// for the `time` qualifier (lower.go::concurrencyIntrinsicForFree).
// Without the qualifier extension the call falls through to "call to
// unresolved symbol std.time.sleep".
func TestTimeSleepLowersToThreadSleepIntrinsic(t *testing.T) {
	got := emitDurationLLVM(t, `use std.time

fn main() {
    let _ = time.sleep(50.ms())
}
`)
	for _, want := range []string{
		"declare void @osty_rt_thread_sleep(ptr)",
		"call void @osty_rt_thread_sleep(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("time.sleep(50.ms()) missing %q in IR:\n%s", want, got)
		}
	}
}

// `time.now()` returns an Instant, the spec-canonical wall-clock entry
// point (LANG_SPEC §10.20). The LLVM lowering threads through
// IntrinsicTimeNow → `osty_rt_time_now_nanos` (i64) → `inttoptr` into
// the opaque-ptr Instant ABI (same shape Duration uses for its
// Int64-payload value type). Without the intrinsic the call falls
// through to "call to unresolved symbol std.time.now".
func TestTimeNowLowersToRuntimeCallAndInstantPtrWrap(t *testing.T) {
	got := emitDurationLLVM(t, `use std.time

fn main() {
    let x = time.now()
}
`)
	for _, want := range []string{
		"declare i64 @osty_rt_time_now_nanos()",
		"call i64 @osty_rt_time_now_nanos()",
		"inttoptr i64 ",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("time.now() missing %q in IR:\n%s", want, got)
		}
	}
}
