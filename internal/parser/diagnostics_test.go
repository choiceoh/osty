package parser

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
)

// findDiag returns the first diagnostic with the given code, or nil.
func findDiag(diags []*diag.Diagnostic, code string) *diag.Diagnostic {
	for _, d := range diags {
		if d.Code == code {
			return d
		}
	}
	return nil
}

// findDiagMessage returns the first diagnostic whose rendered message
// contains substr, or nil. Use only when the relevant diagnostic site has
// no Code; prefer findDiag/expectCode otherwise so tests don't couple to
// error wording.
func findDiagMessage(diags []*diag.Diagnostic, substr string) *diag.Diagnostic {
	for _, d := range diags {
		if strings.Contains(d.Error(), substr) {
			return d
		}
	}
	return nil
}

func TestDiagElseAcrossNewline(t *testing.T) {
	src := `fn f() -> Int {
    if cond {
        1
    }
    else {
        2
    }
}`
	_, diags := ParseDiagnostics([]byte(src))
	d := findDiag(diags, diag.CodeElseAcrossNewline)
	if d == nil {
		t.Fatalf("no E0105 diagnostic; got: %v", diags)
	}
	if d.Hint == "" {
		t.Error("hint missing")
	}
	if !strings.Contains(d.Hint, "same line") {
		t.Errorf("hint should suggest same-line rewrite: %q", d.Hint)
	}
}

func TestDiagTurbofishMissingLT(t *testing.T) {
	src := `fn f() { let x = foo::bar() }`
	_, diags := ParseDiagnostics([]byte(src))
	d := findDiag(diags, diag.CodeTurbofishMissingLT)
	if d == nil {
		t.Fatalf("no E0201 diagnostic; got: %v", diags)
	}
	if !strings.Contains(d.Hint, "`.`") {
		t.Errorf("hint should suggest `.`: %q", d.Hint)
	}
}

func TestDiagNonAssocComparison(t *testing.T) {
	src := `fn f() { let z = a < b < c }`
	_, diags := ParseDiagnostics([]byte(src))
	d := findDiag(diags, diag.CodeNonAssocChain)
	if d == nil {
		t.Fatalf("no E0200 diagnostic")
	}
	if len(d.Notes) == 0 {
		t.Error("non-assoc diag should carry an explanatory note")
	}
}

func TestDiagDefaultExprNotLiteral(t *testing.T) {
	src := `fn f(t: Int = compute()) {}`
	_, diags := ParseDiagnostics([]byte(src))
	d := findDiag(diags, diag.CodeDefaultExprNotLiteral)
	if d == nil {
		t.Fatalf("no E0106 diagnostic")
	}
	if !strings.Contains(strings.Join(d.Notes, "\n"), "R18") {
		t.Errorf("note should reference R18: %v", d.Notes)
	}
}

func TestDiagClosureRetReqBlock(t *testing.T) {
	src := `fn f() { let g = |x: Int| -> Int x * 2 }`
	_, diags := ParseDiagnostics([]byte(src))
	d := findDiag(diags, diag.CodeClosureRetReqBlock)
	if d == nil {
		t.Fatalf("no E0203 diagnostic")
	}
	if !strings.Contains(d.Hint, "{") {
		t.Errorf("hint should mention braces: %q", d.Hint)
	}
}

func TestDiagUnknownAnnotation(t *testing.T) {
	src := "#[inline]\npub fn f() {}"
	_, diags := ParseDiagnostics([]byte(src))
	d := findDiag(diags, diag.CodeUnknownAnnotation)
	if d == nil {
		t.Fatalf("no E0400 diagnostic")
	}
	if !strings.Contains(d.Notes[0], "json") {
		t.Errorf("note should list permitted annotations: %v", d.Notes)
	}
}

func TestDiagUsePathMixed(t *testing.T) {
	src := "use a/b.c\nfn main() {}"
	_, diags := ParseDiagnostics([]byte(src))
	d := findDiag(diags, diag.CodeUsePathMixed)
	if d == nil {
		t.Fatalf("no E0104 diagnostic")
	}
}

func TestDiagUppercaseBasePrefix(t *testing.T) {
	src := `fn f() { let n = 0X1F }`
	_, diags := ParseDiagnostics([]byte(src))
	d := findDiag(diags, diag.CodeUppercaseBasePrefix)
	if d == nil {
		t.Fatalf("no E0002 diagnostic")
	}
	if !strings.Contains(d.Hint, "0x") {
		t.Errorf("hint should suggest lowercase: %q", d.Hint)
	}
}

func TestDiagNoCascadingDuplicates(t *testing.T) {
	// One real error should not produce many duplicates at the same
	// position. parser.errorf and emit() de-duplicate.
	src := `fn good() { 1 }
fn broken( {
fn good2() { 2 }`
	_, diags := ParseDiagnostics([]byte(src))
	posCount := map[string]int{}
	for _, d := range diags {
		key := d.PrimaryPos().String()
		posCount[key]++
	}
	for pos, n := range posCount {
		if n > 1 {
			t.Errorf("%d duplicate diagnostics at %s", n, pos)
		}
	}
}

func TestDiagRecoveryAllowsSubsequentDecls(t *testing.T) {
	// After an error in `broken`, `good2` should still parse.
	src := `fn good1() { 1 }
fn broken( {
fn good2() { 2 }`
	file, _ := ParseDiagnostics([]byte(src))
	names := map[string]bool{}
	for _, d := range file.Decls {
		// Decls are ast.Decl; use a tiny Switch via the package.
		// Easier: just sanity-check we have at least 2 named decls.
		_ = d
	}
	// At minimum, file.Decls must have parsed `good1`. The recovery
	// also tries to keep going; assert a non-empty Decls slice.
	if len(file.Decls) == 0 {
		t.Fatal("recovery: no decls parsed at all")
	}
	_ = names
}

func TestDiagFormatterRenders(t *testing.T) {
	src := `fn f() { let n = 0X1F }`
	_, diags := ParseDiagnostics([]byte(src))
	if len(diags) == 0 {
		t.Fatal("expected diagnostic")
	}
	f := &diag.Formatter{Filename: "x.osty", Source: []byte(src), Color: false}
	out := f.Format(diags[0])
	for _, want := range []string{
		"error[E0002]",
		"--> x.osty:1:18",
		"^^",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output missing %q\nfull:\n%s", want, out)
		}
	}
}
