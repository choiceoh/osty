package lsp

import (
	"reflect"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/token"
)

func TestCompletionItemUsesSelfHostedPolicy(t *testing.T) {
	tests := []struct {
		label    string
		kind     resolve.SymbolKind
		wantKind CompletionItemKind
		wantSort string
	}{
		{label: "std", kind: resolve.SymPackage, wantKind: CompletionItemModule, wantSort: "0_std"},
		{label: "value", kind: resolve.SymLet, wantKind: CompletionItemVariable, wantSort: "1_value"},
		{label: "main", kind: resolve.SymFn, wantKind: CompletionItemFunction, wantSort: "2_main"},
		{label: "User", kind: resolve.SymStruct, wantKind: CompletionItemStruct, wantSort: "3_User"},
	}

	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			got := completionItemFromSym(tt.label, &resolve.Symbol{Kind: tt.kind}, nil)
			if got.Kind != tt.wantKind {
				t.Fatalf("kind = %d, want %d", got.Kind, tt.wantKind)
			}
			if got.SortText != tt.wantSort {
				t.Fatalf("sortText = %q, want %q", got.SortText, tt.wantSort)
			}
		})
	}
}

func TestCompletionSortUsesSelfHostedPolicy(t *testing.T) {
	got := sortCompletionItems([]CompletionItem{
		{Label: "zeta"},
		{Label: "alpha"},
		{Label: "middle"},
	})
	if labels := []string{got[0].Label, got[1].Label, got[2].Label}; !reflect.DeepEqual(labels, []string{"alpha", "middle", "zeta"}) {
		t.Fatalf("completion labels = %#v", labels)
	}
}

func TestSymbolKindUsesSelfHostedPolicy(t *testing.T) {
	tests := []struct {
		name string
		got  SymbolKind
		want SymbolKind
	}{
		{name: "fn", got: lspSymbolKindForDecl("fn", false), want: SymKindFunction},
		{name: "struct", got: lspSymbolKindForDecl("struct", false), want: SymKindStruct},
		{name: "enum", got: lspSymbolKindForDecl("enum", false), want: SymKindEnum},
		{name: "interface", got: lspSymbolKindForDecl("interface", false), want: SymKindInterface},
		{name: "type alias", got: lspSymbolKindForDecl("typeAlias", false), want: SymKindClass},
		{name: "let", got: lspSymbolKindForDecl("let", false), want: SymKindConstant},
		{name: "mut let", got: lspSymbolKindForDecl("let", true), want: SymKindVariable},
		{name: "field", got: lspSymbolKindForMember("field"), want: SymKindField},
		{name: "variant", got: lspSymbolKindForMember("variant"), want: SymKindEnumMember},
		{name: "method", got: lspSymbolKindForMember("method"), want: SymKindMethod},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Fatalf("kind = %d, want %d", tt.got, tt.want)
			}
		})
	}
}

func TestWantsKindUsesSelfHostedPrefixPolicy(t *testing.T) {
	if !wantsKind(nil, CodeActionSourceOrganizeImports) {
		t.Fatal("empty only filter should allow source.organizeImports")
	}
	if !wantsKind([]string{CodeActionSource}, CodeActionSourceFixAllOsty) {
		t.Fatal("source should allow source.fixAll.osty")
	}
	if wantsKind([]string{CodeActionQuickFix}, CodeActionSourceFixAll) {
		t.Fatal("quickfix should not allow source.fixAll")
	}
}

func TestDisplayTextUsesSelfHostedPolicy(t *testing.T) {
	var b strings.Builder
	writeSymSignature(&b, &resolve.Symbol{Name: "User", Kind: resolve.SymStruct}, nil)
	if got := b.String(); got != "struct User\n" {
		t.Fatalf("hover signature = %q", got)
	}
	if got := LSPHoverSignatureLine("binding", "value", "Int"); got != "let value: Int" {
		t.Fatalf("hover binding = %q", got)
	}
	if got := LSPCompletionDetail("function", "map", "fn(Int) -> String"); got != "fn map(Int) -> String" {
		t.Fatalf("completion detail = %q", got)
	}
}

func TestFindNameOffsetUsesSelfHostedLexerPolicy(t *testing.T) {
	src := []byte("/// 카페 docs\npub fn greet(name: String) -> String { name }\n")
	declStart := strings.Index(string(src), "pub")
	got := findNameOffset(src, declStart, len(src), "greet")
	want := strings.Index(string(src), "greet")
	if got != want {
		t.Fatalf("name offset = %d, want %d", got, want)
	}
	if got := findNameOffset(src, declStart, len(src), "missing"); got != -1 {
		t.Fatalf("missing name offset = %d, want -1", got)
	}
}

func TestPrecedingContextUsesSelfHostedPolicy(t *testing.T) {
	prefix, afterDot := precedingContext([]byte("std.fmt.pr"), len("std.fmt.pr"))
	if prefix != "pr" || afterDot != "fmt" {
		t.Fatalf("dot context = (%q, %q), want (pr, fmt)", prefix, afterDot)
	}
	prefix, afterDot = precedingContext([]byte("let answer = value"), len("let answer = value"))
	if prefix != "value" || afterDot != "" {
		t.Fatalf("plain context = (%q, %q), want (value, empty)", prefix, afterDot)
	}
	prefix, afterDot = precedingContext([]byte("값.필"), len("값.필"))
	if prefix != "필" || afterDot != "값" {
		t.Fatalf("unicode context = (%q, %q), want (필, 값)", prefix, afterDot)
	}
}

func TestIdentifierAtUsesSelfHostedPolicy(t *testing.T) {
	src := []byte("let value = 1\n값 = 2\n")
	if got := identifierAt(src, strings.Index(string(src), "value")); got != "value" {
		t.Fatalf("identifierAt(value) = %q, want value", got)
	}
	if got := identifierAt(src, strings.Index(string(src), "let")+3); got != "" {
		t.Fatalf("identifierAt(space) = %q, want empty", got)
	}
	if got := identifierAt(src, strings.Index(string(src), "값")); got != "값" {
		t.Fatalf("identifierAt(unicode) = %q, want 값", got)
	}
}

func TestResolveOverlapsUsesSelfHostedPolicy(t *testing.T) {
	got := resolveOverlaps([]TextEdit{
		{
			Range:   Range{Start: Position{Line: 2}, End: Position{Line: 2, Character: 4}},
			NewText: "third",
		},
		{
			Range:   Range{Start: Position{Line: 0, Character: 1}, End: Position{Line: 0, Character: 3}},
			NewText: "first",
		},
		{
			Range:   Range{Start: Position{Line: 0, Character: 2}, End: Position{Line: 0, Character: 5}},
			NewText: "overlap",
		},
		{
			Range:   Range{Start: Position{Line: 0, Character: 3}, End: Position{Line: 0, Character: 3}},
			NewText: "adjacent insert",
		},
		{
			Range:   Range{Start: Position{Line: 0, Character: 3}, End: Position{Line: 0, Character: 3}},
			NewText: "duplicate insert",
		},
	})
	want := []TextEdit{
		{
			Range:   Range{Start: Position{Line: 0, Character: 1}, End: Position{Line: 0, Character: 3}},
			NewText: "first",
		},
		{
			Range:   Range{Start: Position{Line: 0, Character: 3}, End: Position{Line: 0, Character: 3}},
			NewText: "adjacent insert",
		},
		{
			Range:   Range{Start: Position{Line: 2}, End: Position{Line: 2, Character: 4}},
			NewText: "third",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolved edits = %#v, want %#v", got, want)
	}
}

func TestURIAndLocationPolicyUsesSelfHost(t *testing.T) {
	if got := pathToURI("/tmp/main.osty"); got != "file:///tmp/main.osty" {
		t.Fatalf("pathToURI(posix) = %q", got)
	}
	if got := pathToURI("C:/tmp/main.osty"); got != "file:///C:/tmp/main.osty" {
		t.Fatalf("pathToURI(windows) = %q", got)
	}
	if got := pathToURI(""); got != "file://" {
		t.Fatalf("pathToURI(empty) = %q", got)
	}

	got := sortDedupLocations([]Location{
		{
			URI:   "file:///b.osty",
			Range: Range{Start: Position{Line: 2}, End: Position{Line: 2, Character: 4}},
		},
		{
			URI:   "file:///a.osty",
			Range: Range{Start: Position{Line: 4, Character: 1}, End: Position{Line: 4, Character: 5}},
		},
		{
			URI:   "file:///a.osty",
			Range: Range{Start: Position{Line: 4, Character: 1}, End: Position{Line: 4, Character: 5}},
		},
	})
	want := []Location{
		{
			URI:   "file:///a.osty",
			Range: Range{Start: Position{Line: 4, Character: 1}, End: Position{Line: 4, Character: 5}},
		},
		{
			URI:   "file:///b.osty",
			Range: Range{Start: Position{Line: 2}, End: Position{Line: 2, Character: 4}},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("locations = %#v, want %#v", got, want)
	}

	syms := sortSymbolInformation([]SymbolInformation{
		{Name: "Zoo", Location: Location{URI: "file:///b.osty"}},
		{Name: "App", Location: Location{URI: "file:///z.osty"}},
		{Name: "App", Location: Location{URI: "file:///a.osty"}},
	})
	if got := []string{syms[0].Location.URI, syms[1].Location.URI, syms[2].Location.URI}; !reflect.DeepEqual(got, []string{"file:///a.osty", "file:///z.osty", "file:///b.osty"}) {
		t.Fatalf("symbol order = %#v", got)
	}
}

func TestDiagnosticPayloadUsesSelfHostedPolicy(t *testing.T) {
	li := newLineIndex([]byte("let value = 1\n"))
	got := toLSPDiag(li, &diag.Diagnostic{
		Severity: diag.Warning,
		Code:     "L0001",
		Message:  "unused value",
		Hint:     "prefix it",
		Notes:    []string{"declared here"},
		Spans: []diag.LabeledSpan{{
			Span:    diag.Span{Start: token.Pos{Offset: 4, Line: 1, Column: 5}, End: token.Pos{Offset: 9, Line: 1, Column: 10}},
			Primary: true,
		}},
	})
	if got.Severity != SevWarning {
		t.Fatalf("severity = %d, want %d", got.Severity, SevWarning)
	}
	if got.Message != "unused value\nhelp: prefix it\nnote: declared here" {
		t.Fatalf("message = %q", got.Message)
	}
}

func TestImportOrganizeHelpersUseSelfHost(t *testing.T) {
	std := &ast.UseDecl{Path: []string{"std", "fmt"}, PosV: token.Pos{Offset: 0}, EndV: token.Pos{Offset: len("use std.fmt  \n")}}
	raw := &ast.UseDecl{RawPath: "github.com/acme/pkg"}
	goFFI := &ast.UseDecl{IsGoFFI: true, GoPath: "net/http"}

	if got := useGroup(std); got != 0 {
		t.Fatalf("std group = %d, want 0", got)
	}
	if got := useGroup(raw); got != 1 {
		t.Fatalf("external group = %d, want 1", got)
	}
	if got := useGroup(goFFI); got != 2 {
		t.Fatalf("go group = %d, want 2", got)
	}
	if got := useKey(std); got != "std.fmt" {
		t.Fatalf("std key = %q", got)
	}
	if got := useKey(raw); got != "github.com/acme/pkg" {
		t.Fatalf("raw key = %q", got)
	}
	if got := useKey(goFFI); got != "net/http" {
		t.Fatalf("go key = %q", got)
	}
	if got := keyWithAlias(1, "pkg", "alias"); got != "1|pkg|alias" {
		t.Fatalf("dedup key = %q", got)
	}
	sorted := sortImportEntries([]keyedUse{
		{u: &ast.UseDecl{}, group: 1, key: "zeta"},
		{u: &ast.UseDecl{}, group: 0, key: "fmt"},
		{u: &ast.UseDecl{Alias: "b"}, group: 1, key: "alpha"},
		{u: &ast.UseDecl{Alias: "a"}, group: 1, key: "alpha"},
	})
	if got := []string{sorted[0].key, sorted[1].u.Alias, sorted[2].u.Alias, sorted[3].key}; !reflect.DeepEqual(got, []string{"fmt", "a", "b", "zeta"}) {
		t.Fatalf("import order = %#v", got)
	}
	if got := useSourceText([]byte("use std.fmt  \n"), std); got != "use std.fmt" {
		t.Fatalf("source text = %q", got)
	}
	if got := endOfLineOffset([]byte("use a  \r\nnext"), 5); got != 9 {
		t.Fatalf("line end = %d, want 9", got)
	}
	if hasTriviaBetweenUses([]byte("use a\n\nuse b"), []*ast.UseDecl{
		{EndV: token.Pos{Offset: 5}},
		{PosV: token.Pos{Offset: 7}},
	}) {
		t.Fatal("blank line gap should be safe")
	}
	if !hasTriviaBetweenUses([]byte("use a\n// note\nuse b"), []*ast.UseDecl{
		{EndV: token.Pos{Offset: 5}},
		{PosV: token.Pos{Offset: 13}},
	}) {
		t.Fatal("comment gap should be treated as trivia")
	}
}
