package lsp

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
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
	if got := LSPHoverSignatureLine("struct", "User", ""); got != "struct User" {
		t.Fatalf("hover signature = %q", got)
	}
	if got := LSPHoverSignatureLine("binding", "value", "Int"); got != "let value: Int" {
		t.Fatalf("hover binding = %q", got)
	}
	if got := LSPCompletionDetail("function", "map", "fn(Int) -> String"); got != "fn map(Int) -> String" {
		t.Fatalf("completion detail = %q", got)
	}
}

func TestHoverMarkdownWrapsSelfHostedPolicy(t *testing.T) {
	view := hoverSymbolView(&resolve.Symbol{Name: "User", Kind: resolve.SymStruct}, "", nil)
	if !view.HasSym || view.Kind != "struct" || view.Name != "User" {
		t.Fatalf("view = %+v", view)
	}
	got := selfhost.LSPHoverMarkdown(view)
	if want := "```osty\nstruct User\n```"; got != want {
		t.Fatalf("hover markdown = %q, want %q", got, want)
	}

	fallback := selfhost.LSPHoverMarkdown(hoverSymbolView(nil, "raw", nil))
	if want := "```osty\nraw\n```"; fallback != want {
		t.Fatalf("fallback markdown = %q, want %q", fallback, want)
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

func TestFixAllActionUsesAIRepairForForeignSyntax(t *testing.T) {
	src := []byte("import std.testing as t\nfunc main() {}\n")
	s := NewServer(bytes.NewReader(nil), io.Discard, io.Discard)
	doc := &document{
		uri:      "file:///tmp/main.osty",
		src:      src,
		analysis: s.analyzeSingleFileViaEngine("file:///tmp/main.osty", src),
	}

	action := fixAllAction(doc)
	if action == nil {
		t.Fatal("fixAllAction() = nil, want airepair-backed action")
	}
	if action.Kind != CodeActionSourceFixAllOsty {
		t.Fatalf("kind = %q, want %q", action.Kind, CodeActionSourceFixAllOsty)
	}
	edits := action.Edit.Changes[doc.uri]
	if len(edits) != 1 {
		t.Fatalf("len(edits) = %d, want 1", len(edits))
	}
	if got, want := edits[0].NewText, "use std.testing as t\n\nfn main() {}\n"; got != want {
		t.Fatalf("newText = %q, want %q", got, want)
	}
}

func TestFixAllActionUsesAIRepairForPythonBlocks(t *testing.T) {
	src := []byte("fn main():\n    println(1)\n")
	s := NewServer(bytes.NewReader(nil), io.Discard, io.Discard)
	doc := &document{
		uri:      "file:///tmp/main.osty",
		src:      src,
		analysis: s.analyzeSingleFileViaEngine("file:///tmp/main.osty", src),
	}

	action := fixAllAction(doc)
	if action == nil {
		t.Fatal("fixAllAction() = nil, want airepair-backed action")
	}
	edits := action.Edit.Changes[doc.uri]
	if len(edits) != 1 {
		t.Fatalf("len(edits) = %d, want 1", len(edits))
	}
	if got, want := edits[0].NewText, "fn main() {\n    println(1)\n}\n"; got != want {
		t.Fatalf("newText = %q, want %q", got, want)
	}
}

func TestFixAllActionUsesAIRepairForPythonElif(t *testing.T) {
	src := []byte("fn main() {\n    if a:\n        println(1)\n    elif b:\n        println(2)\n    else:\n        println(0)\n}\n")
	s := NewServer(bytes.NewReader(nil), io.Discard, io.Discard)
	doc := &document{
		uri:      "file:///tmp/main.osty",
		src:      src,
		analysis: s.analyzeSingleFileViaEngine("file:///tmp/main.osty", src),
	}

	action := fixAllAction(doc)
	if action == nil {
		t.Fatal("fixAllAction() = nil, want airepair-backed action")
	}
	edits := action.Edit.Changes[doc.uri]
	if len(edits) != 1 {
		t.Fatalf("len(edits) = %d, want 1", len(edits))
	}
	if got, want := edits[0].NewText, "fn main() {\n    if a {\n        println(1)\n    } else if b {\n        println(2)\n    } else {\n        println(0)\n    }\n}\n"; got != want {
		t.Fatalf("newText = %q, want %q", got, want)
	}
}

func TestFixAllActionUsesAIRepairForPythonBareTupleLoop(t *testing.T) {
	src := []byte("fn main() {\n    let items = [(1, 2)]\n    for k, v in items:\n        println(k)\n}\n")
	s := NewServer(bytes.NewReader(nil), io.Discard, io.Discard)
	doc := &document{
		uri:      "file:///tmp/main.osty",
		src:      src,
		analysis: s.analyzeSingleFileViaEngine("file:///tmp/main.osty", src),
	}

	action := fixAllAction(doc)
	if action == nil {
		t.Fatal("fixAllAction() = nil, want airepair-backed action")
	}
	edits := action.Edit.Changes[doc.uri]
	if len(edits) != 1 {
		t.Fatalf("len(edits) = %d, want 1", len(edits))
	}
	if got, want := edits[0].NewText, "fn main() {\n    let items = [(1, 2)]\n    for (k, v) in items {\n        println(k)\n    }\n}\n"; got != want {
		t.Fatalf("newText = %q, want %q", got, want)
	}
}

func TestFixAllActionUsesAIRepairForJSForOfLoop(t *testing.T) {
	src := []byte("fn main() {\n    let items = [1, 2]\n    for (const item of items) {\n        println(item)\n    }\n}\n")
	s := NewServer(bytes.NewReader(nil), io.Discard, io.Discard)
	doc := &document{
		uri:      "file:///tmp/main.osty",
		src:      src,
		analysis: s.analyzeSingleFileViaEngine("file:///tmp/main.osty", src),
	}

	action := fixAllAction(doc)
	if action == nil {
		t.Fatal("fixAllAction() = nil, want airepair-backed action")
	}
	edits := action.Edit.Changes[doc.uri]
	if len(edits) != 1 {
		t.Fatalf("len(edits) = %d, want 1", len(edits))
	}
	if got, want := edits[0].NewText, "fn main() {\n    let items = [1, 2]\n    for item in items {\n        println(item)\n    }\n}\n"; got != want {
		t.Fatalf("newText = %q, want %q", got, want)
	}
}

func TestFixAllActionUsesAIRepairForJSDestructuringForOfLoop(t *testing.T) {
	src := []byte("fn main() {\n    let entries = [(1, 2)]\n    for (const [k, v] of entries) {\n        println(k)\n    }\n}\n")
	s := NewServer(bytes.NewReader(nil), io.Discard, io.Discard)
	doc := &document{
		uri:      "file:///tmp/main.osty",
		src:      src,
		analysis: s.analyzeSingleFileViaEngine("file:///tmp/main.osty", src),
	}

	action := fixAllAction(doc)
	if action == nil {
		t.Fatal("fixAllAction() = nil, want airepair-backed action")
	}
	edits := action.Edit.Changes[doc.uri]
	if len(edits) != 1 {
		t.Fatalf("len(edits) = %d, want 1", len(edits))
	}
	if got, want := edits[0].NewText, "fn main() {\n    let entries = [(1, 2)]\n    for (k, v) in entries {\n        println(k)\n    }\n}\n"; got != want {
		t.Fatalf("newText = %q, want %q", got, want)
	}
}

func TestFixAllActionUsesAIRepairForPythonRangeLoop(t *testing.T) {
	src := []byte("fn main() {\n    for i in range(3):\n        println(i)\n}\n")
	s := NewServer(bytes.NewReader(nil), io.Discard, io.Discard)
	doc := &document{
		uri:      "file:///tmp/main.osty",
		src:      src,
		analysis: s.analyzeSingleFileViaEngine("file:///tmp/main.osty", src),
	}

	action := fixAllAction(doc)
	if action == nil {
		t.Fatal("fixAllAction() = nil, want airepair-backed action")
	}
	edits := action.Edit.Changes[doc.uri]
	if len(edits) != 1 {
		t.Fatalf("len(edits) = %d, want 1", len(edits))
	}
	if got, want := edits[0].NewText, "fn main() {\n    for i in 0..3 {\n        println(i)\n    }\n}\n"; got != want {
		t.Fatalf("newText = %q, want %q", got, want)
	}
}

func TestFixAllActionUsesAIRepairForPythonEnumerateLoop(t *testing.T) {
	src := []byte("fn main() {\n    let items = [1, 2]\n    for i, item in enumerate(items):\n        println(item)\n}\n")
	s := NewServer(bytes.NewReader(nil), io.Discard, io.Discard)
	doc := &document{
		uri:      "file:///tmp/main.osty",
		src:      src,
		analysis: s.analyzeSingleFileViaEngine("file:///tmp/main.osty", src),
	}

	action := fixAllAction(doc)
	if action == nil {
		t.Fatal("fixAllAction() = nil, want airepair-backed action")
	}
	edits := action.Edit.Changes[doc.uri]
	if len(edits) != 1 {
		t.Fatalf("len(edits) = %d, want 1", len(edits))
	}
	if got, want := edits[0].NewText, "fn main() {\n    let items = [1, 2]\n    let _osty_enumerate0 = items\n    for i in 0.._osty_enumerate0.len() {\n        let item = _osty_enumerate0[i]\n        println(item)\n    }\n}\n"; got != want {
		t.Fatalf("newText = %q, want %q", got, want)
	}
}

func TestFixAllActionUsesAIRepairForSemanticHelpers(t *testing.T) {
	src := []byte("fn main() {\n    let mut items = [1, 2]\n    let count = len(items)\n    let size = items.length\n    items = append(items, count + size)\n    println(items)\n}\n")
	s := NewServer(bytes.NewReader(nil), io.Discard, io.Discard)
	doc := &document{
		uri:      "file:///tmp/main.osty",
		src:      src,
		analysis: s.analyzeSingleFileViaEngine("file:///tmp/main.osty", src),
	}

	action := fixAllAction(doc)
	if action == nil {
		t.Fatal("fixAllAction() = nil, want airepair-backed action")
	}
	edits := action.Edit.Changes[doc.uri]
	if len(edits) != 1 {
		t.Fatalf("len(edits) = %d, want 1", len(edits))
	}
	if got, want := edits[0].NewText, "fn main() {\n    let mut items = [1, 2]\n    let count = items.len()\n    let size = items.len()\n    items.push(count + size)\n    println(items)\n}\n"; got != want {
		t.Fatalf("newText = %q, want %q", got, want)
	}
}

func TestFixAllActionUsesAIRepairForPythonMatchCase(t *testing.T) {
	src := []byte("fn main() {\n    let value = 0\n    match value:\n        case 0:\n            println(0)\n        default:\n            println(1)\n}\n")
	s := NewServer(bytes.NewReader(nil), io.Discard, io.Discard)
	doc := &document{
		uri:      "file:///tmp/main.osty",
		src:      src,
		analysis: s.analyzeSingleFileViaEngine("file:///tmp/main.osty", src),
	}

	action := fixAllAction(doc)
	if action == nil {
		t.Fatal("fixAllAction() = nil, want airepair-backed action")
	}
	edits := action.Edit.Changes[doc.uri]
	if len(edits) != 1 {
		t.Fatalf("len(edits) = %d, want 1", len(edits))
	}
	if got, want := edits[0].NewText, "fn main() {\n    let value = 0\n    match value {\n        0 -> {\n            println(0)\n        },\n        _ -> {\n            println(1)\n        },\n    }\n}\n"; got != want {
		t.Fatalf("newText = %q, want %q", got, want)
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
	stdSrc := []byte("use std.fmt  \n")
	std := &ast.UseDecl{Path: []string{"std", "fmt"}, PosV: token.Pos{Offset: 0}, EndV: token.Pos{Offset: len(stdSrc)}}
	raw := &ast.UseDecl{RawPath: "github.com/acme/pkg"}
	goFFI := &ast.UseDecl{IsGoFFI: true, GoPath: "net/http"}
	views := useDeclViews([]*ast.UseDecl{std, raw, goFFI})
	if len(views) != 3 {
		t.Fatalf("view count = %d, want 3", len(views))
	}

	if got := LSPUseGroup(views[0].IsFFI, views[0].Path); got != 0 {
		t.Fatalf("std group = %d, want 0", got)
	}
	if got := LSPUseGroup(views[1].IsFFI, views[1].Path); got != 1 {
		t.Fatalf("external group = %d, want 1", got)
	}
	if got := LSPUseGroup(views[2].IsFFI, views[2].Path); got != 2 {
		t.Fatalf("go group = %d, want 2", got)
	}
	if got := LSPUseKey(views[0].IsFFI, views[0].FFIPath, views[0].RawPath, views[0].Path); got != "std.fmt" {
		t.Fatalf("std key = %q", got)
	}
	if got := LSPUseKey(views[1].IsFFI, views[1].FFIPath, views[1].RawPath, views[1].Path); got != "github.com/acme/pkg" {
		t.Fatalf("raw key = %q", got)
	}
	if got := LSPUseKey(views[2].IsFFI, views[2].FFIPath, views[2].RawPath, views[2].Path); got != "net/http" {
		t.Fatalf("go key = %q", got)
	}
	if got := LSPKeyWithAlias(1, "pkg", "alias"); got != "1|pkg|alias" {
		t.Fatalf("dedup key = %q", got)
	}
	sorted := sortImportEntries([]keyedUse{
		{view: selfhost.LSPUseDeclView{}, group: 1, key: "zeta"},
		{view: selfhost.LSPUseDeclView{}, group: 0, key: "fmt"},
		{view: selfhost.LSPUseDeclView{Alias: "b"}, group: 1, key: "alpha"},
		{view: selfhost.LSPUseDeclView{Alias: "a"}, group: 1, key: "alpha"},
	})
	if got := []string{sorted[0].key, sorted[1].view.Alias, sorted[2].view.Alias, sorted[3].key}; !reflect.DeepEqual(got, []string{"fmt", "a", "b", "zeta"}) {
		t.Fatalf("import order = %#v", got)
	}
	if got := LSPUseSourceText(stdSrc, views[0].PosOffset, views[0].EndOffset); got != "use std.fmt" {
		t.Fatalf("source text = %q", got)
	}
	if got := LSPEndOfLineOffset([]byte("use a  \r\nnext"), 5); got != 9 {
		t.Fatalf("line end = %d, want 9", got)
	}
	gapOK := []selfhost.LSPUseDeclView{
		{EndOffset: 5},
		{PosOffset: 7},
	}
	if hasTriviaBetweenUseViews([]byte("use a\n\nuse b"), gapOK) {
		t.Fatal("blank line gap should be safe")
	}
	gapBad := []selfhost.LSPUseDeclView{
		{EndOffset: 5},
		{PosOffset: 13},
	}
	if !hasTriviaBetweenUseViews([]byte("use a\n// note\nuse b"), gapBad) {
		t.Fatal("comment gap should be treated as trivia")
	}
}
