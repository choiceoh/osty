package llvmgen

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
)

func TestFnValueSignatureRecognizesPtrWithSig(t *testing.T) {
	sig := &fnSig{ret: "i64"}
	got, ok := fnValueSignature(value{typ: "ptr", fnSigRef: sig})
	if !ok {
		t.Fatal("fnValueSignature returned ok=false, want true")
	}
	if got != sig {
		t.Fatalf("fnValueSignature returned %p, want %p", got, sig)
	}
}

func TestRequireFnValueSignatureRejectsNonFnValue(t *testing.T) {
	_, err := requireFnValueSignature(value{typ: "i64"}, "map.update callback")
	if err == nil {
		t.Fatal("requireFnValueSignature returned nil error, want failure")
	}
	if got := err.Error(); !strings.Contains(got, "map.update callback must be a fn value") {
		t.Fatalf("error = %q, want fn-value diagnostic", got)
	}
}

func TestProtectFnValueCallbackPreservesSignature(t *testing.T) {
	g := &generator{}
	g.beginFunction()

	sig := &fnSig{ret: "i64"}
	held, got, err := g.protectFnValueCallback(
		"mergewith.combine",
		value{typ: "ptr", ref: "%env", gcManaged: true, fnSigRef: sig},
		"map.mergeWith combine",
	)
	if err != nil {
		t.Fatalf("protectFnValueCallback returned error: %v", err)
	}
	if got != sig {
		t.Fatalf("protectFnValueCallback sig = %p, want %p", got, sig)
	}
	if !held.ptr {
		t.Fatalf("held.ptr = false, want true for protected slot")
	}
	if held.fnSigRef != sig {
		t.Fatalf("held.fnSigRef = %p, want %p", held.fnSigRef, sig)
	}
}

func TestAttachFnValueSignatureFromSourceTypeRecognizesFnType(t *testing.T) {
	g := &generator{}
	v := value{
		typ: "ptr",
		sourceType: &ast.FnType{
			Params: []ast.Type{
				&ast.NamedType{Path: []string{"Int"}},
			},
			ReturnType: &ast.NamedType{Path: []string{"Bool"}},
		},
	}

	if err := g.attachFnValueSignatureFromSourceType(&v); err != nil {
		t.Fatalf("attachFnValueSignatureFromSourceType returned error: %v", err)
	}
	if v.fnSigRef == nil {
		t.Fatal("fnSigRef = nil, want synthesized signature")
	}
	if got := v.fnSigRef.ret; got != "i1" {
		t.Fatalf("fnSigRef.ret = %q, want i1", got)
	}
	if len(v.fnSigRef.params) != 1 || v.fnSigRef.params[0].typ != "i64" {
		t.Fatalf("fnSigRef.params = %#v, want one i64 param", v.fnSigRef.params)
	}
}
