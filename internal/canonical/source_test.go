package canonical

import (
	"bytes"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/selfhost"
)

func TestSourceWithMapProjectsLoweredCallSpans(t *testing.T) {
	src := []byte(`func main() {
    let xs = [1]
    let n = len(xs)
}
`)
	parsed := parser.ParseDetailed(src)
	if len(parsed.Diagnostics) != 0 {
		t.Fatalf("parse diagnostics = %#v, want none", parsed.Diagnostics)
	}
	canonicalSrc, sm := SourceWithMap(src, parsed.File)
	if sm == nil || sm.Empty() {
		t.Fatal("source map = nil, want canonical span mappings")
	}
	if !bytes.Contains(canonicalSrc, []byte("xs.len()")) {
		t.Fatalf("canonical source = %q, want lowered xs.len()", canonicalSrc)
	}
	mainDecl := parsed.File.Decls[0].(*ast.FnDecl)
	letStmt := mainDecl.Body.Stmts[1].(*ast.LetStmt)
	call := letStmt.Value.(*ast.CallExpr)
	original := diag.Span{Start: call.Pos(), End: call.End()}

	generated, ok := sm.GeneratedSpanForOriginal(original)
	if !ok {
		t.Fatal("GeneratedSpanForOriginal() = false, want canonical call span")
	}
	if got := string(canonicalSrc[generated.Start.Offset:generated.End.Offset]); got != "xs.len()" {
		t.Fatalf("generated call = %q, want xs.len()", got)
	}

	remapped, ok := sm.RemapSpan(generated)
	if !ok {
		t.Fatal("RemapSpan() = false, want original call span")
	}
	if remapped != original {
		t.Fatalf("remapped span = %#v, want %#v", remapped, original)
	}
}

func TestSourceEscapesBraceCharAndByteLiteralsForSelfhost(t *testing.T) {
	src := []byte(`fn main() {
    let openByte = b'\{'
    let closeByte = b'\}'
    let openChar = '\{'
    let closeChar = '\}'
}
`)
	parsed := parser.ParseDetailed(src)
	if len(parsed.Diagnostics) != 0 {
		t.Fatalf("parse diagnostics = %#v, want none", parsed.Diagnostics)
	}
	canonicalSrc, _ := SourceWithMap(src, parsed.File)
	for _, want := range []string{`b'\{'`, `b'\}'`, `'\{'`, `'\}'`} {
		if !bytes.Contains(canonicalSrc, []byte(want)) {
			t.Fatalf("canonical source = %q, want %s", canonicalSrc, want)
		}
	}
	if diags := selfhost.ParseDiagnostics(canonicalSrc); len(diags) != 0 {
		t.Fatalf("selfhost parse diagnostics = %#v", diags)
	}
}
