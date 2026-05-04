package selfhost

import (
	"strings"
	"testing"
)

// TestDurationMembersResolveOnPreludeBuiltin pins the SPEC_GAPS
// `duration-builtin-methods` fix: prelude registers `Duration` as
// a builtin name (so `Int.s` etc. constructors return a real type
// without forcing every caller to `use std.time`), but the checker
// also needs the struct's methods + field from
// `internal/stdlib/modules/time.osty`. Pre-fix behavior was every
// `d.toString()` / `d.nanoseconds` access surfaced as E0703 / E0702.
func TestDurationMembersResolveOnPreludeBuiltin(t *testing.T) {
	src := `fn main() {
    let d = 5.s
    let s: String = d.toString()
    let n: Int64 = d.nanoseconds
    let m: Int = d.millis()
    let mc: Int = d.micros()
    let sec: Int = d.seconds()
    let abs_d: Duration = d.abs()
    println(s)
    println("{n}")
    println("{m}")
    println("{mc}")
    println("{sec}")
    println(abs_d.toString())
}
`
	file := astParse(src)
	cx := newElabCx(file, nil)
	elabFile(cx)
	for _, d := range cx.env.local.diagnostics {
		switch d.code {
		case "E0702", "E0703":
			if strings.Contains(d.message, "Duration") {
				t.Fatalf("Duration member access surfaced as %s: %s", d.code, d.message)
			}
		}
	}
}
