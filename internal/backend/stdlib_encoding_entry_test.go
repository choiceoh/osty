package backend

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ir"
)

func TestPrepareEntryRewritesStdEncodingSingletonMethods(t *testing.T) {
	req := newBackendRequest(t, EmitLLVMIR, `use std.bytes
use std.encoding

fn main() {
    let raw = bytes.fromString("hello world&foo=bar")
    let b64 = encoding.base64.encode(raw)
    let round = encoding.base64.decode(b64).unwrap()
    let b64url = encoding.base64.url.encode(bytes.fromString("??>>"))
    let hx = encoding.hex.encode(raw)
    let escaped = encoding.url.encode("hello world&foo=bar")
    println(b64)
    println(round.toString().unwrap())
    println(b64url)
    println(hx)
    println(escaped)
}
`)

	want := map[string]bool{
		"osty_std_encoding__Base64__encode":      false,
		"osty_std_encoding__Base64__decode":      false,
		"osty_std_encoding__Base64Url__encode":   false,
		"osty_std_encoding__Hex__encode":         false,
		"osty_std_encoding__UrlEncoding__encode": false,
	}
	ir.Inspect(req.Entry.IR, func(n ir.Node) bool {
		if mc, ok := n.(*ir.MethodCall); ok && mc != nil {
			if module, path, ok := stdlibFieldPath(mc.Receiver); ok && module == "encoding" && len(path) != 0 {
				t.Errorf("encoding singleton method call was not rewritten: %s.%s", strings.Join(path, "."), mc.Name)
			}
		}
		call, ok := n.(*ir.CallExpr)
		if !ok || call == nil {
			return true
		}
		ident, ok := call.Callee.(*ir.Ident)
		if !ok || ident == nil {
			return true
		}
		if _, hit := want[ident.Name]; !hit {
			return true
		}
		want[ident.Name] = true
		if _, ok := ident.Type().(*ir.FnType); !ok {
			t.Errorf("callee %s Type() = %T, want *ir.FnType", ident.Name, ident.Type())
		}
		return true
	})
	for name, seen := range want {
		if !seen {
			t.Fatalf("missing rewritten call to %s", name)
		}
	}
}
