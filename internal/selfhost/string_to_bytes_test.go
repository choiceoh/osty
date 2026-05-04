package selfhost

import (
	"strings"
	"testing"
)

// TestStringToBytesMethodResolves pins the spec §2.4.1
// `String.toBytes(self) -> Bytes` method registration. The
// runtime symbol `osty_rt_strings_ToBytes` has been there since
// the first stdlib loader landed, but the method was never
// registered with the checker — every `s.toBytes()` surfaced as
// E0703 even though the byte string literal `b"..."` (the spec's
// other path to a Bytes value from text) also doesn't lex. This
// fix unblocks the simplest workaround: `"hello".toBytes()`
// returns a 5-byte Bytes value.
func TestStringToBytesMethodResolves(t *testing.T) {
	src := `fn main() {
    let s = "hello"
    let b = s.toBytes()
    let n = b.len()
    let _ = n
}
`
	file := astParse(src)
	cx := newElabCx(file, nil)
	elabFile(cx)
	for _, d := range cx.env.local.diagnostics {
		if d.code == "E0703" && strings.Contains(d.message, "toBytes") {
			t.Fatalf("String.toBytes should resolve: %s", d.message)
		}
	}
}
