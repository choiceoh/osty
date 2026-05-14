package runner

import "testing"

func TestRewritePythonColonHeader(t *testing.T) {
	cases := []struct {
		name          string
		in            string
		wantOk        bool
		wantRewritten string
		wantChange    string
		wantScope     int
		wantElseish   bool
		wantRequires  bool
	}{
		{
			name:          "arrow-arm",
			in:            "Foo(x) ->",
			wantOk:        true,
			wantRewritten: "Foo(x) -> {",
			wantChange:    "python_arrow_arm_block",
			wantScope:     PythonScopeMatchArm,
			wantRequires:  true,
		},
		{
			name:   "arrow-empty-rejected",
			in:     "->",
			wantOk: false,
		},
		{
			name:          "else",
			in:            "else:",
			wantOk:        true,
			wantRewritten: "else {",
			wantChange:    "python_else_block",
			wantScope:     PythonScopeBlock,
			wantElseish:   true,
		},
		{
			name:          "elif",
			in:            "elif x > 0:",
			wantOk:        true,
			wantRewritten: "else if x > 0 {",
			wantChange:    "python_elif_block",
			wantScope:     PythonScopeBlock,
			wantElseish:   true,
		},
		{
			name:          "elseif",
			in:            "elseif n == 0:",
			wantOk:        true,
			wantRewritten: "else if n == 0 {",
			wantChange:    "python_elif_block",
			wantScope:     PythonScopeBlock,
			wantElseish:   true,
		},
		{
			name:          "else-if",
			in:            "else if y < 0:",
			wantOk:        true,
			wantRewritten: "else if y < 0 {",
			wantChange:    "python_else_if_block",
			wantScope:     PythonScopeBlock,
			wantElseish:   true,
		},
		{
			name:          "if",
			in:            "if cond:",
			wantOk:        true,
			wantRewritten: "if cond {",
			wantChange:    "python_if_block",
			wantScope:     PythonScopeBlock,
		},
		{
			name:          "for",
			in:            "for x in xs:",
			wantOk:        true,
			wantRewritten: "for x in xs {",
			wantChange:    "python_for_block",
			wantScope:     PythonScopeBlock,
		},
		{
			name:          "while",
			in:            "while n > 0:",
			wantOk:        true,
			wantRewritten: "for n > 0 {",
			wantChange:    "python_while_block",
			wantScope:     PythonScopeBlock,
		},
		{
			name:          "match",
			in:            "match value:",
			wantOk:        true,
			wantRewritten: "match value {",
			wantChange:    "python_match_block",
			wantScope:     PythonScopeMatch,
		},
		{
			name:          "case",
			in:            "case Foo(x):",
			wantOk:        true,
			wantRewritten: "Foo(x) -> {",
			wantChange:    "python_case_arm",
			wantScope:     PythonScopeMatchArm,
			wantRequires:  true,
		},
		{
			name:          "default",
			in:            "default:",
			wantOk:        true,
			wantRewritten: "_ -> {",
			wantChange:    "python_default_arm",
			wantScope:     PythonScopeMatchArm,
			wantRequires:  true,
		},
		{
			name:          "fn",
			in:            "fn foo() -> Int:",
			wantOk:        true,
			wantRewritten: "fn foo() -> Int {",
			wantChange:    "python_fn_block",
			wantScope:     PythonScopeBlock,
		},
		{
			name:          "pub-fn",
			in:            "pub fn bar() -> String:",
			wantOk:        true,
			wantRewritten: "pub fn bar() -> String {",
			wantChange:    "python_fn_block",
			wantScope:     PythonScopeBlock,
		},
		{
			name:          "struct",
			in:            "struct Point:",
			wantOk:        true,
			wantRewritten: "struct Point {",
			wantChange:    "python_struct_block",
			wantScope:     PythonScopeBlock,
		},
		{
			name:          "interface",
			in:            "interface Reader:",
			wantOk:        true,
			wantRewritten: "interface Reader {",
			wantChange:    "python_interface_block",
			wantScope:     PythonScopeBlock,
		},
		{
			name:   "no-colon-rejected",
			in:     "if cond",
			wantOk: false,
		},
		{
			name:   "bare-colon-rejected",
			in:     ":",
			wantOk: false,
		},
		{
			name:   "unknown-prefix",
			in:     "randomToken:",
			wantOk: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := RewritePythonColonHeader(c.in)
			if got.Ok != c.wantOk {
				t.Fatalf("Ok = %v, want %v", got.Ok, c.wantOk)
			}
			if !c.wantOk {
				return
			}
			if got.Rewritten != c.wantRewritten {
				t.Errorf("Rewritten = %q, want %q", got.Rewritten, c.wantRewritten)
			}
			if got.ChangeKind != c.wantChange {
				t.Errorf("ChangeKind = %q, want %q", got.ChangeKind, c.wantChange)
			}
			if got.ScopeKind != c.wantScope {
				t.Errorf("ScopeKind = %d, want %d", got.ScopeKind, c.wantScope)
			}
			if got.Elseish != c.wantElseish {
				t.Errorf("Elseish = %v, want %v", got.Elseish, c.wantElseish)
			}
			if got.RequiresMatch != c.wantRequires {
				t.Errorf("RequiresMatch = %v, want %v", got.RequiresMatch, c.wantRequires)
			}
		})
	}
}
