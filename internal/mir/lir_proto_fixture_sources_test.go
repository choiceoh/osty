package mir

import (
	"strings"
	"testing"
)

// TestLirProtoSourceFixtureSourcesParseAndLowerCleanly verifies
// that the *source code embedded in the new LIR Proto parity
// fixtures* (PR adding `source_option_map_closure_infer`,
// `source_optional_chain_flatten`, `source_if_let_some_struct`,
// `source_match_result_struct`) actually parses, type-checks,
// and lowers through HIR/MIR without `<error>` leaks or
// projection failures.
//
// The toolchain `lir_proto_parity_test.osty` data file already
// asserts the resulting LLVM IR shape (when osty-self runs the
// parity check), but its declared source is opaque to Go-side
// tests. This test re-evaluates the same source code through
// the Go-side parser → resolve → check → ir.Lower → MIR pipeline
// so a regression in the front-end / HIR lowering surfaces
// before the toolchain test suite reaches the LIR Proto layer.
//
// Each subtest's `src` is byte-identical to the source embedded
// in the matching parity fixture (modulo Osty string-escape
// `\{`/`\}` → `{`/`}` and `\n` → newline).
func TestLirProtoSourceFixtureSourcesParseAndLowerCleanly(t *testing.T) {
	cases := []struct {
		fixture string
		src     string
	}{
		{
			fixture: "source_option_map_closure_infer",
			src: `fn doubled(opt: Int?) -> Int? {
    opt.map(|x| x * 2)
}
`,
		},
		{
			fixture: "source_optional_chain_flatten",
			src: `struct Outer {
    inner: Inner?,
}

struct Inner {
    value: Int,
}

fn deep(o: Outer?) -> Int {
    o?.inner?.value ?? -1
}
`,
		},
		{
			fixture: "source_if_let_some_struct",
			src: `struct Box {
    n: Int,
}

fn pick(b: Box?) -> Int {
    if let Some(box) = b {
        box.n
    } else {
        0
    }
}
`,
		},
		{
			fixture: "source_match_result_struct",
			src: `struct Box {
    n: Int,
}

fn unbox(r: Result<Box, String>) -> Int {
    match r {
        Ok(box) -> box.n,
        Err(_) -> -1,
    }
}
`,
		},
	}
	for _, c := range cases {
		t.Run(c.fixture, func(t *testing.T) {
			mirText := lowerSourceToMIR(t, c.src)
			if strings.Contains(mirText, "<error>") {
				t.Errorf("ErrTypeVal leaked into MIR for %s:\n%s", c.fixture, mirText)
			}
			for _, ban := range []string{
				"unsupported projection",
				"projection on non-aggregate",
			} {
				if strings.Contains(mirText, ban) {
					t.Errorf("GAP-INSTR-006 marker %q in MIR for %s:\n%s", ban, c.fixture, mirText)
				}
			}
		})
	}
}
