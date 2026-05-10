package lsp

import (
	"bufio"
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/selfhost/api"
	"github.com/osty/osty/internal/semanticdb"
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
			got := completionItemFromSym(tt.label, &resolve.Symbol{Kind: tt.kind}, nil, "")
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

	filtered := LSPCompletionItemsForCandidates([]LSPCompletionCandidateView{
		{Name: "zeta", Kind: "binding", TypeText: "Int", Include: true},
		{Name: "alpha", Kind: "function", TypeText: "fn() -> Int", DocText: "docs", Include: true},
		{Name: "alpha", Kind: "binding", TypeText: "String", Include: true},
		{Name: "hidden", Kind: "binding", TypeText: "Int"},
		{Name: "", Kind: "binding", TypeText: "Int", Include: true},
	}, "")
	if labels := []string{filtered[0].Label, filtered[1].Label}; !reflect.DeepEqual(labels, []string{"alpha", "zeta"}) {
		t.Fatalf("completion candidate labels = %#v", labels)
	}
	if filtered[0].Detail != "fn alpha() -> Int" || filtered[0].Documentation != "docs" {
		t.Fatalf("completion candidate item = %+v", filtered[0])
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

func TestJSONRPCHeaderPolicyUsesSelfHost(t *testing.T) {
	parsed := LSPParseHeaderLines([]string{
		"Content-Type: application/vscode-jsonrpc; charset=utf-8",
		"content-length: 42",
	})
	if !parsed.OK || parsed.ContentLength != 42 || parsed.Error != "" {
		t.Fatalf("header parse = %+v, want length 42", parsed)
	}
	missing := LSPParseHeaderLines([]string{"Content-Type: application/json"})
	if !missing.OK || missing.ContentLength != -1 {
		t.Fatalf("missing content length = %+v, want ok length -1", missing)
	}
	if got := LSPParseHeaderLines(nil); got.OK || got.Error != "lsp: empty header block" {
		t.Fatalf("empty header parse = %+v, want empty-block error", got)
	}
	if got := LSPParseHeaderLines([]string{"Content-Length: -1"}); got.OK {
		t.Fatalf("negative content length parsed ok: %+v", got)
	}
	if got := LSPFrameHeader(17); got != "Content-Length: 17\r\n\r\n" {
		t.Fatalf("frame header = %q", got)
	}
	if got := LSPTrimHeaderLine("Content-Length: 17\r\n"); got != "Content-Length: 17" {
		t.Fatalf("trimmed header line = %q", got)
	}
	length, err := readHeaders(bufio.NewReader(strings.NewReader("Content-Type: x\r\ncontent-length: 2\r\n\r\n{}")))
	if err != nil || length != 2 {
		t.Fatalf("readHeaders = (%d, %v), want (2, nil)", length, err)
	}
}

func TestNearbyNameRankingUsesSelfHostedPolicy(t *testing.T) {
	if got := LSPLevenshteinBounded("kitten", "sitting", 3); got != 3 {
		t.Fatalf("levenshtein = %d, want 3", got)
	}
	if got := LSPLevenshteinBounded("kitten", "sitting", 2); got != 3 {
		t.Fatalf("bounded levenshtein = %d, want limit+1", got)
	}
	got := nearbyStructuredSymbolNames([]structuredSymbol{
		{name: "helpr"},
		{name: "helper"},
		{name: "helper"},
		{name: "helmet"},
		{name: "builtin", builtin: true},
		{name: "local", depth: 1},
	}, "helper", 1)
	if want := []string{"helper", "helpr"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("nearby names = %#v, want %#v", got, want)
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

func TestNameURIAndFixAllPolicyUseSelfHost(t *testing.T) {
	if got := LSPServerName(); got != "osty-lsp" {
		t.Fatalf("server name = %q", got)
	}
	if got := LSPServerVersion(); got != "0.1.0" {
		t.Fatalf("server version = %q", got)
	}
	if got := LSPPositionEncodingUTF16(); got != "utf-16" {
		t.Fatalf("encoding = %q", got)
	}
	if got := LSPJSONNull(); got != "null" {
		t.Fatalf("json null = %q", got)
	}
	if got := []string{LSPCompletionTriggerDot(), LSPSignatureTriggerOpenParen(), LSPSignatureTriggerComma()}; !reflect.DeepEqual(got, []string{".", "(", ","}) {
		t.Fatalf("triggers = %#v", got)
	}
	if got := LSPSemanticTokenTypes(); got[0] != "namespace" || got[len(got)-1] != "enumMember" {
		t.Fatalf("semantic token legend = %#v", got)
	}
	if got := LSPSemanticTokenModifiers(); !reflect.DeepEqual(got, []string{"declaration", "readonly"}) {
		t.Fatalf("semantic token modifiers = %#v", got)
	}
	if got := LSPExitCode(true); got != 0 {
		t.Fatalf("exit code = %d, want 0", got)
	}
	if got := LSPExitCode(false); got != 1 {
		t.Fatalf("exit code = %d, want 1", got)
	}
	if got := LSPDispatchDecisionFor("initialize", false, false, false); got.Action != LSPDispatchActionInitialize() {
		t.Fatalf("initialize decision = %+v", got)
	}
	if got := LSPDispatchDecisionFor("textDocument/hover", false, false, false); got.ErrorCode != errServerNotInitialized {
		t.Fatalf("pre-init decision = %+v", got)
	}
	if got := LSPDispatchDecisionFor("shutdown", false, true, false); got.Action != LSPDispatchActionShutdown() {
		t.Fatalf("shutdown decision = %+v", got)
	}
	if got := LSPDispatchDecisionFor("textDocument/hover", false, true, false); got.Action != LSPDispatchActionDispatch() {
		t.Fatalf("hover decision = %+v", got)
	}
	if got := LSPDispatchDecisionFor("unknown/method", false, true, false); got.ErrorCode != errMethodNotFound {
		t.Fatalf("unknown request decision = %+v", got)
	}
	if got := LSPDispatchDecisionFor("unknown/method", true, true, false); got.Action != LSPDispatchActionIgnore() {
		t.Fatalf("unknown notification decision = %+v", got)
	}
	if got := LSPAsciiLowerText("HeLLo"); got != "hello" {
		t.Fatalf("lower text = %q, want hello", got)
	}
	if !LSPNameMatchesPrefix("helper", "hel") || LSPNameMatchesPrefix("helper", "map") {
		t.Fatal("prefix policy mismatch")
	}
	if !LSPNameMatchesQuery("HelperValue", "value") {
		t.Fatal("query policy did not match lowercase substring")
	}
	if got := LSPURIForSourcePath("inmemory:main"); got != "inmemory:main" {
		t.Fatalf("source URI = %q, want inmemory:main", got)
	}
	if got := LSPURIForSourcePath("/tmp/main.osty"); got != "file:///tmp/main.osty" {
		t.Fatalf("file source URI = %q", got)
	}
	if got := LSPFileURIPathRaw("file:///tmp/main.osty"); !got.OK || got.Path != "/tmp/main.osty" {
		t.Fatalf("file URI raw path = %+v", got)
	}
	if got := LSPFileURIPathRaw("file:///C:/tmp/main.osty"); !got.OK || got.Path != "C:/tmp/main.osty" {
		t.Fatalf("windows file URI raw path = %+v", got)
	}
	if got := LSPFileURIPathRaw("inmemory:main"); got.OK {
		t.Fatalf("non-file URI raw path = %+v", got)
	}
	if !LSPPreferAIRepairFixAll([]byte("x.length")) || LSPPreferAIRepairFixAll([]byte("x.len()")) {
		t.Fatal("fix-all preference policy mismatch")
	}
	if !LSPTextChanged([]byte("a"), []byte("b")) || LSPTextChanged([]byte("a"), []byte("a")) {
		t.Fatal("text changed policy mismatch")
	}
	if !LSPParamsAreEmpty(nil) || !LSPParamsAreEmpty([]byte("null")) || LSPParamsAreEmpty([]byte("{}")) {
		t.Fatal("params empty policy mismatch")
	}
	if got := LSPRenameEmptyNameMessage(); got != "new name is empty" {
		t.Fatalf("empty rename message = %q", got)
	}
	if got := LSPCannotRenameBuiltinMessage(); got != "cannot rename a builtin" {
		t.Fatalf("builtin rename message = %q", got)
	}
	if !LSPCanRenameKind("function") || LSPCanRenameKind("builtin") {
		t.Fatal("rename kind policy mismatch")
	}
	if got := LSPRenameTitle("helper"); got != "Rename to `helper`" {
		t.Fatalf("rename title = %q", got)
	}
	if got := LSPRemoveLineTitle(); got != "Remove unused import" {
		t.Fatalf("remove title = %q", got)
	}
	if got := LSPFixAllTitle(); got != "Fix all auto-fixable problems" {
		t.Fatalf("fix-all title = %q", got)
	}
	if got := LSPOrganizeImportsTitle(); got != "Organize imports" {
		t.Fatalf("organize title = %q", got)
	}
	if got := LSPInlayTypeLabel("Int"); got != ": Int" {
		t.Fatalf("inlay label = %q", got)
	}
	if got := LSPMethodNotImplementedMessage("custom/method"); got != "method not implemented: custom/method" {
		t.Fatalf("method message = %q", got)
	}
	if !LSPIsOstySourceFileName("main.osty") || LSPIsOstySourceFileName("main_test.osty") || LSPIsOstySourceFileName("main.go") {
		t.Fatal("source filename policy mismatch")
	}
	if !LSPHasOstyFileExtension("main_test.osty") || LSPHasOstyFileExtension("main.go") {
		t.Fatal("source extension policy mismatch")
	}
	item := LSPCompletionItemForSymbolView(selfhost.LSPSymbolView{
		Name:     "map",
		Kind:     "function",
		TypeText: "fn(Int) -> String",
		DocText:  "docs",
		HasSym:   true,
	})
	if item.Label != "map" || item.Kind != uint32(CompletionItemFunction) || item.SortText != "2_map" || item.Detail != "fn map(Int) -> String" || item.Documentation != "docs" {
		t.Fatalf("completion item policy = %+v", item)
	}
	if got := LSPSemanticHoverKind("generic"); got != "type parameter" {
		t.Fatalf("semantic hover kind = %q", got)
	}
}

func TestTargetLocationPolicyUsesSelfHost(t *testing.T) {
	refs := []LSPReferenceFact{
		{
			URI:                  "file:///b.osty",
			StartLine:            2,
			StartCharacter:       0,
			EndLine:              2,
			EndCharacter:         4,
			TargetSymbolID:       "sym.helper",
			TargetURI:            "file:///decl.osty",
			TargetStartLine:      1,
			TargetStartCharacter: 1,
			TargetEndLine:        1,
			TargetEndCharacter:   7,
		},
		{
			URI:            "file:///a.osty",
			StartLine:      4,
			StartCharacter: 1,
			EndLine:        4,
			EndCharacter:   7,
			TargetSymbolID: "sym.helper",
		},
	}
	symbols := []LSPSymbolFact{{
		ID:             "sym.helper",
		URI:            "file:///decl.osty",
		StartLine:      1,
		StartCharacter: 2,
		EndLine:        1,
		EndCharacter:   8,
	}}
	got := LSPLocationsForTarget(refs, symbols, "sym.helper", true)
	want := []LSPLocation{
		{URI: "file:///a.osty", StartLine: 4, StartCharacter: 1, EndLine: 4, EndCharacter: 7},
		{URI: "file:///b.osty", StartLine: 2, StartCharacter: 0, EndLine: 2, EndCharacter: 4},
		{URI: "file:///decl.osty", StartLine: 1, StartCharacter: 2, EndLine: 1, EndCharacter: 8},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("target locations = %#v, want %#v", got, want)
	}
	if got := LSPLocationsForTarget(refs, nil, "missing", true); len(got) != 0 {
		t.Fatalf("missing target locations = %#v, want none", got)
	}
}

func TestSignatureTypeParsingUsesSelfHostedPolicy(t *testing.T) {
	parsed := LSPParseFunctionType("fn(Int, Result<String, Error>, fn(Int) -> Bool) -> String")
	if !parsed.OK {
		t.Fatal("function type did not parse")
	}
	wantParams := []string{"Int", "Result<String, Error>", "fn(Int) -> Bool"}
	if !reflect.DeepEqual(parsed.ParameterTypes, wantParams) || parsed.ReturnType != "String" {
		t.Fatalf("parsed function type = %+v, want params %#v return String", parsed, wantParams)
	}
	if got := LSPParseFunctionType("List<Int>"); got.OK {
		t.Fatalf("non-function type parsed ok: %+v", got)
	}
	if got, want := LSPFallbackParameterNames(3), []string{"arg1", "arg2", "arg3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("fallback parameter names = %#v, want %#v", got, want)
	}
}

func TestHoverMarkdownWrapsSelfHostedPolicy(t *testing.T) {
	view := hoverSymbolView(&resolve.Symbol{Name: "User", Kind: resolve.SymStruct}, "", nil, "")
	if !view.HasSym || view.Kind != "struct" || view.Name != "User" {
		t.Fatalf("view = %+v", view)
	}
	got := selfhost.LSPHoverMarkdown(view)
	if want := "```osty\nstruct User\n```"; got != want {
		t.Fatalf("hover markdown = %q, want %q", got, want)
	}

	fallback := selfhost.LSPHoverMarkdown(hoverSymbolView(nil, "raw", nil, ""))
	if want := "```osty\nraw\n```"; fallback != want {
		t.Fatalf("fallback markdown = %q, want %q", fallback, want)
	}
}

func TestSemanticHoverUsesSemanticDB(t *testing.T) {
	path := "/tmp/main.osty"
	src, declStart, refStart, db := semanticNavFixture(path)
	hover, ok := semanticHoverAt(&docAnalysis{
		sourcePath: path,
		lines:      newLineIndex(src),
		semantic:   db,
	}, token.Pos{Offset: refStart + 1})
	if !ok {
		t.Fatal("semanticHoverAt returned !ok")
	}
	if !strings.Contains(hover.Contents.Value, "helper") || !strings.Contains(hover.Contents.Value, "Int") {
		t.Fatalf("semantic hover markdown = %q, want helper with Int type", hover.Contents.Value)
	}
	wantRange := newLineIndex(src).rangeFromOffsets(refStart, refStart+len("helper"))
	if hover.Range == nil || *hover.Range != wantRange {
		t.Fatalf("semantic hover range = %#v, want %#v", hover.Range, wantRange)
	}
	if declStart < 0 {
		t.Fatal("fixture did not find helper declaration")
	}
}

func TestSemanticDefinitionReferencesAndRenameUseSemanticDB(t *testing.T) {
	path := "/tmp/main.osty"
	src, declStart, refStart, db := semanticNavFixture(path)
	a := &docAnalysis{
		sourcePath: path,
		lines:      newLineIndex(src),
		semantic:   db,
	}
	doc := &document{uri: pathToURI(path), src: src, analysis: a}
	refPos := a.lines.offsetToLSP(refStart + 1)

	def, ok := semanticDefinitionAt(doc, token.Pos{Offset: refStart + 1})
	if !ok {
		t.Fatal("semanticDefinitionAt returned !ok")
	}
	if def.Range != a.lines.rangeFromOffsets(declStart, declStart+len("helper")) {
		t.Fatalf("definition range = %#v, want helper decl", def.Range)
	}

	locs, ok := (&Server{}).semanticReferences(doc, refPos, true)
	if !ok {
		t.Fatal("semanticReferences returned !ok")
	}
	if len(locs) != 2 {
		t.Fatalf("semantic references = %#v, want use + decl", locs)
	}
	edits, ok := (&Server{}).semanticRenameEdits(doc, refPos, "renamed")
	if !ok {
		t.Fatal("semanticRenameEdits returned !ok")
	}
	if got := len(edits[doc.uri]); got != 2 {
		t.Fatalf("rename edits = %#v, want 2 edits for document", edits)
	}
}

func semanticNavFixture(path string) ([]byte, int, int, *semanticdb.DB) {
	src := []byte("fn helper() -> Int { 1 }\nfn main() -> Int { helper() }\n")
	declStart := strings.Index(string(src), "helper")
	refStart := strings.LastIndex(string(src), "helper")
	db := semanticdb.New([]semanticdb.File{{Path: path, Source: src}}, api.ResolveResult{
		PackageID: "pkg",
		Symbols: []api.ResolvedSymbol{{
			ID:    "sym-helper",
			File:  path,
			Node:  1,
			Name:  "helper",
			Kind:  "fn",
			Start: declStart,
			End:   declStart + len("helper"),
		}},
		Refs: []api.ResolvedRef{{
			ID:             "ref-helper",
			File:           path,
			Node:           2,
			Name:           "helper",
			Start:          refStart,
			End:            refStart + len("helper"),
			TargetSymbolID: "sym-helper",
			TargetFile:     path,
			TargetNode:     1,
			TargetStart:    declStart,
			TargetEnd:      declStart + len("helper"),
		}},
	}).WithCheck(api.CheckResult{
		Symbols: []api.CheckedSymbol{{
			Name: "helper",
			Kind: "fn",
			Type: &api.TypeRepr{
				Kind:   "fn",
				Return: &api.TypeRepr{Kind: "primitive", Name: "Int"},
			},
			Start: declStart,
			End:   declStart + len("helper"),
		}},
	})
	return src, declStart, refStart, db
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
	if got := LSPNamedTypeReferenceEndOffset(10, 20, "helper", "help"); got != 16 {
		t.Fatalf("named type end = %d, want 16", got)
	}
	if got := LSPNamedTypeReferenceEndOffset(18, 20, "helper", ""); got != 20 {
		t.Fatalf("clamped named type end = %d, want 20", got)
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
	if got, want := edits[0].NewText, "fn main() {\n    let items = [1, 2]\n    for (i, item) in items.enumerate() {\n        println(item)\n    }\n}\n"; got != want {
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
	if got := LSPFullDocumentRangeFor([]byte("a😀\nend"), LSPLineStarts([]byte("a😀\nend"))); got.EndLine != 1 || got.EndCharacter != 3 {
		t.Fatalf("full document range = %+v", got)
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
	if got := SortLSPStrings([]string{"z.osty", "a.osty", "a.osty"}); !reflect.DeepEqual(got, []string{"a.osty", "a.osty", "z.osty"}) {
		t.Fatalf("string order = %#v", got)
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
	a := []LSPDiagnostic{got}
	b := append([]LSPDiagnostic(nil), a...)
	if !diagsEqual(a, b) {
		t.Fatalf("diagnostics should be equal: %#v %#v", a, b)
	}
	b[0].Message = "changed"
	if diagsEqual(a, b) {
		t.Fatalf("diagnostics should differ: %#v %#v", a, b)
	}
	if !LSPDiagnosticBelongsToFile(LSPDiagnosticFileFact{
		DiagnosticFile: "/tmp/main.osty",
		PackageFile:    "/tmp/main.osty",
		PrimaryOffset:  999,
		SourceLength:   1,
	}) {
		t.Fatal("diagnostic file match should win")
	}
	if !LSPDiagnosticBelongsToFile(LSPDiagnosticFileFact{
		SpanSourceFileID:    "src1",
		PackageSourceFileID: "src1",
		PrimaryOffset:       999,
		SourceLength:        1,
	}) {
		t.Fatal("diagnostic source file id should match")
	}
	if !LSPDiagnosticBelongsToFile(LSPDiagnosticFileFact{
		PrimaryLine:   1,
		PrimaryOffset: 3,
		SourceLength:  3,
	}) {
		t.Fatal("diagnostic offset fallback should match")
	}
	if LSPDiagnosticBelongsToFile(LSPDiagnosticFileFact{SourceLength: 3}) {
		t.Fatal("zero-line diagnostic should not match")
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
	block := LSPOrganizedUseBlock([]LSPOrganizeUseEntry{
		{Group: 1, Key: "zeta", Text: "use zeta"},
		{Group: 0, Key: "fmt", Text: "use std.fmt"},
		{Group: 1, Key: "alpha", Text: "use alpha"},
		{Group: 1, Key: "alpha", Text: "use alpha duplicate"},
		{Group: 0, Key: "io", Text: "use std.io", Unused: true},
	})
	if got, want := block, "use std.fmt\n\nuse alpha\nuse zeta\n"; got != want {
		t.Fatalf("organized use block = %q, want %q", got, want)
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
