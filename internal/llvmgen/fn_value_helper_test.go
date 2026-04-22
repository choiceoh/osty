package llvmgen

import (
	"strings"
	"testing"
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
