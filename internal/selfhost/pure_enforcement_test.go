package selfhost

import (
	"strings"
	"testing"
)

// TestPureAnnotationEnforcedByE0775 pins the SPEC_GAPS
// `pure-enforce` contract: `#[pure]` is not just an LLVM
// `readnone` hint — the checker walks the body and emits E0775
// for any of the categories listed in §3.8.11 / SPEC_GAPS:
//
//	(a) non-local write — assigning to a non-local binding
//	(b) I/O — println / print / etc.
//	(c) impure call — calling a non-`#[pure]` function
//	(e) allocation — list / map / struct / closure literals
//
// (d) volatile/atomic is N/A: Osty doesn't expose those primitives.
//
// A genuinely pure body must compile clean. The
// implementation lives in `toolchain/check_gates.osty::runPureGate`
// (mirrored in the seed at the same name).
func TestPureAnnotationEnforcedByE0775(t *testing.T) {
	src := `let mut globalState: Int = 0

#[pure]
fn impureWrite() {
    globalState = 100
}

#[pure]
fn impureIO(x: Int) -> Int {
    println("io")
    x + 1
}

fn helper(x: Int) -> Int { x * 2 }

#[pure]
fn impureCall(x: Int) -> Int {
    helper(x)
}

#[pure]
fn impureAlloc() -> List<Int> {
    [1, 2, 3]
}

#[pure]
fn pureOk(x: Int) -> Int {
    x + 1
}

fn main() {}
`
	file := astParse(src)
	cx := newElabCx(file, nil)
	elabFile(cx)

	wantViolations := map[string]string{
		"impureWrite": "write non-local state",
		"impureIO":    "perform I/O",
		"impureCall":  "call a function that is not `#[pure]`",
		"impureAlloc": "allocate a list literal",
	}
	got := map[string]string{}
	for _, d := range cx.env.local.diagnostics {
		if d.code != "E0775" {
			continue
		}
		for fnName, what := range wantViolations {
			if strings.Contains(d.message, fnName) && strings.Contains(d.message, what) {
				got[fnName] = d.message
			}
		}
	}
	for fnName, what := range wantViolations {
		if _, ok := got[fnName]; !ok {
			t.Errorf("expected E0775 on %s mentioning %q, none found", fnName, what)
		}
	}
	for _, d := range cx.env.local.diagnostics {
		if d.code == "E0775" && strings.Contains(d.message, "pureOk") {
			t.Errorf("genuinely-pure pureOk should not trigger E0775: %s", d.message)
		}
	}
}
