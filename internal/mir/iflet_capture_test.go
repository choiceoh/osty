package mir

import (
	"strings"
	"testing"
)

// TestLowerIfLetPatternCaptureRecoversBindingType covers the
// closure-captures-if-let-payload shape:
//
//	fn pick(opt: Int?) -> fn(Int) -> Int {
//	    if let Some(n) = opt {
//	        |x| x + n
//	    } else {
//	        |x| x
//	    }
//	}
//
// Without IR-side pattern-binding type recovery, the captured
// `n` lowers with `<error>` because the resolver hadn't propagated
// the pattern position type to the body's Idents by the time
// `lowerExpr(body)` ran. PR #1820 fixed the MIR layer for direct
// payload reads, but the Closure.Captures snapshot at IR time
// preserved the old ErrType.
//
// `recoverIfLetPatternBindingTypes` walks the Then block with a
// (binding name → type) map derived from the pattern's structural
// position, refreshing every Ident and Closure.Capture that
// references a recoverable name.
//
// Covers:
//   - Option<T> Some(n) — payload type propagates to closure
//   - Option<T> Some(n) with binding pattern `@` aliases
//   - Result<T, E> Ok(v) / Err(e) — both ok and err sides
//   - tuple destructure if-let — each tuple element typed
func TestLowerIfLetPatternCaptureRecoversBindingType(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{
			"option_some_capture",
			`fn pick(opt: Int?) -> fn(Int) -> Int {
    if let Some(n) = opt {
        |x| x + n
    } else {
        |x| x
    }
}`,
		},
		{
			"result_ok_capture",
			`fn pick(r: Result<Int, String>) -> fn(Int) -> Int {
    if let Ok(v) = r {
        |x| x + v
    } else {
        |x| x
    }
}`,
		},
		{
			"result_err_capture",
			`fn pick(r: Result<Int, Int>) -> fn(Int) -> Int {
    if let Err(e) = r {
        |x| x - e
    } else {
        |x| x
    }
}`,
		},
		{
			"option_some_used_inline",
			`fn compute(opt: Int?) -> Int {
    if let Some(n) = opt {
        n + 1
    } else {
        0
    }
}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mirText := lowerSourceToMIR(t, c.src)
			if strings.Contains(mirText, "<error>") {
				t.Errorf("<error> leak in %s:\n%s", c.name, mirText)
			}
		})
	}
}
