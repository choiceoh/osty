package cst_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/osty/osty/internal/cst"
	"github.com/osty/osty/internal/selfhost"
)

// TestBuildRoundTripCorpus is the CST round-trip: the Green tree produced
// by ParseCST, when walked and concatenated, reconstructs the
// normalized source byte-for-byte for every corpus file.
func TestBuildRoundTripCorpus(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, rel := range corpus {
		rel := rel
		t.Run(rel, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(root, rel))
			if err != nil {
				t.Skipf("corpus missing: %v", err)
				return
			}
			src := cst.Normalize(raw)
			tree, _ := selfhost.ParseCST(src)
			if tree == nil {
				t.Skip("parser returned nil CST")
				return
			}
			got := emitTreeBytes(tree)
			if string(got) != string(src) {
				diff := firstDiff(src, got)
				t.Fatalf("%s: round-trip mismatch at byte %d\nwant: %q\n got: %q",
					rel, diff, preview(src, diff), preview(got, diff))
			}
		})
	}
}

// TestBuildTopLevelStructuring checks that entities get wrapped in their
// intended Green kind.
func TestBuildTopLevelStructuring(t *testing.T) {
	const source = `pub fn main() {
    let x = 1
}
`
	src := cst.Normalize([]byte(source))
	tree, _ := selfhost.ParseCST(src)

	var found bool
	tree.Root().Walk(func(r cst.Red) bool {
		if r.Kind() == cst.GkFnDecl {
			found = true
			text := string(src[r.Offset():r.End()])
			if !strings.Contains(text, "fn main") {
				t.Errorf("GkFnDecl text range does not cover 'fn main': %q", text)
			}
		}
		return true
	})
	if !found {
		t.Fatal("no GkFnDecl node found in tree for `pub fn main() {...}`")
	}
}

// TestBuildNestedNativeStructuring checks that ParseCST preserves nested
// native syntax nodes instead of stopping at top-level declaration wrappers.
func TestBuildNestedNativeStructuring(t *testing.T) {
	const source = `fn main() {
    let x = add(1, 2)
    return x
}
`
	tree := buildTreeFromSource(t, source)

	var fn cst.Red
	var foundFn bool
	tree.Root().Walk(func(r cst.Red) bool {
		if r.Kind() == cst.GkFnDecl {
			fn = r
			foundFn = true
			return false
		}
		return true
	})
	if !foundFn {
		t.Fatal("no GkFnDecl node found")
	}

	for _, kind := range []cst.GreenKind{cst.GkBlock, cst.GkLetStmt, cst.GkCall, cst.GkReturnStmt} {
		if got := countKindBelow(fn, kind); got == 0 {
			t.Fatalf("GkFnDecl subtree has no %v node", kind)
		}
	}
}

func TestBuildNestedContextualKinds(t *testing.T) {
	const source = `#[run(mode = "fast")]
let top = Point { x: 1, y: 2 }
`
	tree := buildTreeFromSource(t, source)

	for _, kind := range []cst.GreenKind{
		cst.GkAnnotation,
		cst.GkAnnotationArg,
		cst.GkLetDecl,
		cst.GkStructLit,
		cst.GkStructLitField,
	} {
		if got := countKindBelow(tree.Root(), kind); got == 0 {
			t.Fatalf("tree has no %v node", kind)
		}
	}
}

func TestParseGreenNestedGenericShiftTokens(t *testing.T) {
	const source = `struct Demo {
    nested: List<List<Int>>,
    map: Map<String, List<User>>,
}
struct User {}
`
	tree := buildTreeFromSource(t, source)
	if report := cst.ValidateTree(tree); !report.OK() {
		t.Fatalf("CST validation failed: %s", report.Error())
	}
	if got := string(emitTreeBytes(tree)); got != source {
		t.Fatalf("round-trip mismatch:\nwant: %q\n got: %q", source, got)
	}
	if got := countKindBelow(tree.Root(), cst.GkGenericParamList); got < 3 {
		t.Fatalf("nested generic type args produced %d GenericParamList nodes, want at least 3", got)
	}
}

func TestParseGreenControlHeadersDoNotLeakStrayBraces(t *testing.T) {
	const source = `fn f(xs: List<Int>) -> Int {
    let mut total = 0
    for x in xs {
        if x > 0 { total = total + x }
    }
    let picked = 'outer: loop {
        break 'outer total
    }
    picked
}
`
	tree := buildTreeFromSource(t, source)
	if report := cst.ValidateTree(tree); !report.OK() {
		t.Fatalf("CST validation failed: %s", report.Error())
	}
	if got := string(emitTreeBytes(tree)); got != source {
		t.Fatalf("round-trip mismatch:\nwant: %q\n got: %q", source, got)
	}
}

func TestParseGreenMapLiteralsAreStructured(t *testing.T) {
	const source = `fn main() {
    let empty = {:}
    let scores = {
        "alice": 3,
        "bob": add(1, 2),
    }
    let block = {
        let x = 1
        x
    }
}
`
	tree := buildTreeFromSource(t, source)
	if report := cst.ValidateTree(tree); !report.OK() {
		t.Fatalf("CST validation failed: %s", report.Error())
	}
	if got := string(emitTreeBytes(tree)); got != source {
		t.Fatalf("round-trip mismatch:\nwant: %q\n got: %q", source, got)
	}
	if got := countKindBelow(tree.Root(), cst.GkMap); got != 2 {
		t.Fatalf("map literal count = %d, want 2", got)
	}
	if got := countKindBelow(tree.Root(), cst.GkMapEntry); got != 2 {
		t.Fatalf("map entry count = %d, want 2", got)
	}
	if got := countKindBelow(tree.Root(), cst.GkBlock); got < 2 {
		t.Fatalf("block count = %d, want at least function body and block expression", got)
	}
}

func TestParseGreenParenTupleAndElseAreStructured(t *testing.T) {
	const source = `fn main() {
    let grouped = (1 + 2)
    let unit = ()
    let single = (grouped,)
    let pair = (grouped, single)
    if grouped > 0 {
        grouped
    } else {
        pair
    }
}
`
	tree := buildTreeFromSource(t, source)
	if report := cst.ValidateTree(tree); !report.OK() {
		t.Fatalf("CST validation failed: %s", report.Error())
	}
	if got := string(emitTreeBytes(tree)); got != source {
		t.Fatalf("round-trip mismatch:\nwant: %q\n got: %q", source, got)
	}
	if got := countKindBelow(tree.Root(), cst.GkParen); got != 1 {
		t.Fatalf("paren expr count = %d, want 1", got)
	}
	if got := countKindBelow(tree.Root(), cst.GkTuple); got != 3 {
		t.Fatalf("tuple expr count = %d, want 3", got)
	}
	if got := countKindBelow(tree.Root(), cst.GkElse); got != 1 {
		t.Fatalf("else count = %d, want 1", got)
	}
}

func TestParseGreenLoopAndListsAreStructured(t *testing.T) {
	const source = `enum Choice {
    One,
    Two(Int),
}

fn main() {
    let picked = loop {
        break 1
    }
    let labelled = 'outer: loop {
        break 'outer picked
    }
    match Choice.One {
        Choice.One -> labelled,
        Choice.Two(n) -> n,
    }
}
`
	tree := buildTreeFromSource(t, source)
	if report := cst.ValidateTree(tree); !report.OK() {
		t.Fatalf("CST validation failed: %s", report.Error())
	}
	if got := string(emitTreeBytes(tree)); got != source {
		t.Fatalf("round-trip mismatch:\nwant: %q\n got: %q", source, got)
	}
	if got := countKindBelow(tree.Root(), cst.GkLoop); got != 2 {
		t.Fatalf("loop count = %d, want 2", got)
	}
	if got := countKindBelow(tree.Root(), cst.GkVariantList); got != 1 {
		t.Fatalf("variant list count = %d, want 1", got)
	}
	if got := countKindBelow(tree.Root(), cst.GkVariant); got != 2 {
		t.Fatalf("variant count = %d, want 2", got)
	}
	if got := countKindBelow(tree.Root(), cst.GkMatchArmList); got != 1 {
		t.Fatalf("match arm list count = %d, want 1", got)
	}
	if got := countKindBelow(tree.Root(), cst.GkMatchArm); got != 2 {
		t.Fatalf("match arm count = %d, want 2", got)
	}
}

func TestParseGreenPatternsAreStructured(t *testing.T) {
	const source = `fn main() {
    let (x, _) = pair
    if let Some(n) = maybe {
        n
    }
    match value {
        Value.IntVal(n @ 1..=9) -> n,
        Point { x, y: _ } -> 0,
        1 | 2 -> 12,
        _ -> 1,
    }
}
`
	tree := buildTreeFromSource(t, source)
	if report := cst.ValidateTree(tree); !report.OK() {
		t.Fatalf("CST validation failed: %s", report.Error())
	}
	if got := string(emitTreeBytes(tree)); got != source {
		t.Fatalf("round-trip mismatch:\nwant: %q\n got: %q", source, got)
	}
	for _, kind := range []cst.GreenKind{
		cst.GkTuplePat,
		cst.GkWildcardPat,
		cst.GkIfLet,
		cst.GkVariantPat,
		cst.GkBindingPat,
		cst.GkRangePat,
		cst.GkStructPat,
		cst.GkStructPatField,
		cst.GkOrPat,
	} {
		if got := countKindBelow(tree.Root(), kind); got == 0 {
			t.Fatalf("tree has no %v node", kind)
		}
	}
}

func TestParseGreenForHeadersAreStructured(t *testing.T) {
	const source = `fn main() {
    for item in items {
        item
    }
    for (key, value) in entries {
        value
    }
    for let Some(next) = maybe {
        next
    }
    for count < limit {
        count = count + 1
    }
    'outer: for x in xs {
        continue 'outer
    }
}
`
	tree := buildTreeFromSource(t, source)
	if report := cst.ValidateTree(tree); !report.OK() {
		t.Fatalf("CST validation failed: %s", report.Error())
	}
	if got := string(emitTreeBytes(tree)); got != source {
		t.Fatalf("round-trip mismatch:\nwant: %q\n got: %q", source, got)
	}
	if got := countKindBelow(tree.Root(), cst.GkForStmt); got != 5 {
		t.Fatalf("for stmt count = %d, want 5", got)
	}
	for _, kind := range []cst.GreenKind{
		cst.GkIdentPat,
		cst.GkTuplePat,
		cst.GkVariantPat,
		cst.GkBinary,
		cst.GkContinueStmt,
	} {
		if got := countKindBelow(tree.Root(), kind); got == 0 {
			t.Fatalf("tree has no %v node", kind)
		}
	}
}

func TestParseGreenClosuresAreStructured(t *testing.T) {
	const source = `fn main() {
    let empty = || { 1 }
    let inc = |x| x + 1
    let labels = pairs.map(|(n, s)| "{n} -> {s}")
    let addDiff = |label: String, old: String, new: String| {
        label
    }
}
`
	tree := buildTreeFromSource(t, source)
	if report := cst.ValidateTree(tree); !report.OK() {
		t.Fatalf("CST validation failed: %s", report.Error())
	}
	if got := string(emitTreeBytes(tree)); got != source {
		t.Fatalf("round-trip mismatch:\nwant: %q\n got: %q", source, got)
	}
	if got := countKindBelow(tree.Root(), cst.GkClosure); got != 4 {
		t.Fatalf("closure count = %d, want 4", got)
	}
	for _, kind := range []cst.GreenKind{
		cst.GkParamList,
		cst.GkParam,
		cst.GkTuplePat,
		cst.GkIdentPat,
		cst.GkNamedType,
		cst.GkBinary,
		cst.GkBlock,
	} {
		if got := countKindBelow(tree.Root(), kind); got == 0 {
			t.Fatalf("tree has no %v node", kind)
		}
	}
}

func TestParseGreenCompositeTypesAndGenericBoundsAreStructured(t *testing.T) {
	const source = `struct Box<T: Display + Clone> {
    unit: ()
    owner: Self?
    callback: fn((), [String]) -> ()
    reader: std.io.Reader?
    maybeTuple: (String, Int)?
    maybeList: [String]?
}
`
	tree := buildTreeFromSource(t, source)
	if report := cst.ValidateTree(tree); !report.OK() {
		t.Fatalf("CST validation failed: %s", report.Error())
	}
	if got := string(emitTreeBytes(tree)); got != source {
		t.Fatalf("round-trip mismatch:\nwant: %q\n got: %q", source, got)
	}
	for _, kind := range []cst.GreenKind{
		cst.GkFunctionType,
		cst.GkParamList,
		cst.GkParam,
		cst.GkTupleType,
		cst.GkListType,
		cst.GkNamedType,
		cst.GkGenericBound,
		cst.GkUnitType,
		cst.GkSelfType,
		cst.GkOptionalType,
	} {
		if got := countKindBelow(tree.Root(), kind); got == 0 {
			t.Fatalf("tree has no %v node", kind)
		}
	}
	if got := countKindBelow(tree.Root(), cst.GkGenericBound); got != 2 {
		t.Fatalf("generic bound count = %d, want 2", got)
	}
	if got := countKindBelow(tree.Root(), cst.GkOptionalType); got != 4 {
		t.Fatalf("optional type count = %d, want 4", got)
	}
}

// TestBuildUseDeclStructuring verifies use-decls get their own structured
// node.
func TestBuildUseDeclStructuring(t *testing.T) {
	const source = `use std.io as io
use std.strings as strings

pub fn main() {}
`
	src := cst.Normalize([]byte(source))
	tree, _ := selfhost.ParseCST(src)

	useCount := 0
	tree.Root().Walk(func(r cst.Red) bool {
		if r.Kind() == cst.GkUseDecl {
			useCount++
		}
		return true
	})
	if useCount != 2 {
		t.Fatalf("expected 2 GkUseDecl nodes, found %d", useCount)
	}
}

func TestBuildGroupedUseDeclsHaveDistinctRanges(t *testing.T) {
	const source = `use std::{fs, io as io}
`
	tree := buildTreeFromSource(t, source)

	var ranges [][2]int
	tree.Root().Walk(func(r cst.Red) bool {
		if r.Kind() == cst.GkUseDecl {
			lo, hi := r.TextRange()
			ranges = append(ranges, [2]int{lo, hi})
		}
		return true
	})
	if len(ranges) != 2 {
		t.Fatalf("grouped use produced %d GkUseDecl nodes, want 2", len(ranges))
	}
	if ranges[0][0] == ranges[1][0] && ranges[0][1] == ranges[1][1] {
		t.Fatalf("grouped use child ranges should be distinct, got %v", ranges)
	}
	for _, r := range ranges {
		if r[1] <= r[0] {
			t.Fatalf("grouped use child range must be positive-width, got %v", ranges)
		}
	}
}

func TestParseGreenUseDeclsAreStructured(t *testing.T) {
	const source = `use std.io as io
use std.strings
use std::{fs, io as groupedIO}
use go "net/http" as http {
    fn Get(url: String) -> String
}
`
	tree := buildTreeFromSource(t, source)
	if report := cst.ValidateTree(tree); !report.OK() {
		t.Fatalf("CST validation failed: %s", report.Error())
	}
	if got := string(emitTreeBytes(tree)); got != source {
		t.Fatalf("round-trip mismatch:\nwant: %q\n got: %q", source, got)
	}
	if got := countKindBelow(tree.Root(), cst.GkUseFFIBody); got != 1 {
		t.Fatalf("use FFI body count = %d, want 1", got)
	}
	if got := countKindBelow(tree.Root(), cst.GkUseAlias); got != 3 {
		t.Fatalf("use alias count = %d, want 3", got)
	}
	if got := countKindBelow(tree.Root(), cst.GkUsePath); got < 5 {
		t.Fatalf("use path count = %d, want at least 5", got)
	}
}

// TestBuildLeadingTriviaAttachment sanity-checks that a leading comment is
// attached to the next real token, not dropped.
func TestBuildLeadingTriviaAttachment(t *testing.T) {
	const source = `// top comment
pub fn main() {}
`
	src := cst.Normalize([]byte(source))
	tree, _ := selfhost.ParseCST(src)

	first, ok := tree.Root().FirstToken()
	if !ok {
		t.Fatal("expected at least one real token")
	}
	tok := first.Token()
	sawComment := false
	for _, triID := range tok.LeadingTrivia {
		if tree.Arena.TriviaAt(triID).Kind == cst.TriviaLineComment {
			sawComment = true
			break
		}
	}
	if !sawComment {
		t.Fatal("first token's leading trivia has no TriviaLineComment")
	}
}

// TestBuildTrailingCommentInline checks that a same-line comment after a
// real (non-NEWLINE) token ends up as trailing trivia on that token. This is
// the central case the split policy exists to enable; pre-policy, the
// comment was buried in the next line's leading.
func TestBuildTrailingCommentInline(t *testing.T) {
	const source = `let x = 1 // hi
let y = 2
`
	tree := buildTreeFromSource(t, source)
	tokens := collectRealTokens(tree)

	oneTok, ok := findTokenByText(tokens, "1")
	if !ok {
		t.Fatal("expected a `1` token in the tree")
	}
	letIdx := findNthTokenByText(tokens, "let", 2)
	if letIdx < 0 {
		t.Fatal("expected a second `let` token in the tree")
	}
	secondLet := tokens[letIdx]

	gotTexts := triviaTexts(tree, oneTok.TrailingTrivia)
	gotKinds := triviaKinds(tree, oneTok.TrailingTrivia)
	if !containsKind(gotKinds, cst.TriviaLineComment) {
		t.Errorf("`1` trailing should contain the line comment; got kinds=%v texts=%q", gotKinds, gotTexts)
	}

	secondLeadKinds := triviaKinds(tree, secondLet.LeadingTrivia)
	if containsKind(secondLeadKinds, cst.TriviaLineComment) {
		t.Errorf("second `let` leading must not carry the inline comment; got kinds=%v", secondLeadKinds)
	}
}

// TestBuildTrailingNewlineTokenCarriesNoTrailing verifies the NEWLINE-token
// rule: trivia that sits after a NEWLINE token never becomes its trailing;
// it flows to the next token's leading. This keeps "the line that just
// ended" (the NEWLINE text alone) cleanly separated from "what's on the
// next line" (trivia in the following token's leading).
func TestBuildTrailingNewlineTokenCarriesNoTrailing(t *testing.T) {
	const source = `fn a() {}

fn b() {}
`
	tree := buildTreeFromSource(t, source)

	// Blank-line extra newline must land on the second fn's leading, never
	// on the NEWLINE token's trailing.
	var sawBlankLineOnFn bool
	var newlineTokensTrailed int
	tree.Root().Walk(func(r cst.Red) bool {
		if !r.IsToken() || r.Kind() != cst.GkToken {
			return true
		}
		tok := r.Token()
		if tok.Text == "\n" {
			if len(tok.TrailingTrivia) > 0 {
				newlineTokensTrailed++
			}
		}
		if tok.Text == "fn" && r.Offset() > 0 {
			if containsKind(triviaKinds(tree, tok.LeadingTrivia), cst.TriviaNewline) {
				sawBlankLineOnFn = true
			}
		}
		return true
	})

	if newlineTokensTrailed != 0 {
		t.Errorf("NEWLINE tokens must not carry trailing trivia; %d did", newlineTokensTrailed)
	}
	if !sawBlankLineOnFn {
		t.Error("second `fn` leading should carry the blank-line TriviaNewline")
	}
}

// TestBuildDocCommentAttachesToNext verifies the doc-comment carveout: `///`
// always stays with the following declaration, even if the previous real
// token would otherwise accumulate trailing trivia.
func TestBuildDocCommentAttachesToNext(t *testing.T) {
	const source = `let x = 1
/// doc
fn f() {}
`
	tree := buildTreeFromSource(t, source)
	tokens := collectRealTokens(tree)

	fnIdx := findNthTokenByText(tokens, "fn", 1)
	if fnIdx < 0 {
		t.Fatal("expected an `fn` token")
	}
	fnTok := tokens[fnIdx]

	fnLeadKinds := triviaKinds(tree, fnTok.LeadingTrivia)
	if !containsKind(fnLeadKinds, cst.TriviaDocComment) {
		t.Errorf("`fn` leading should own the doc comment; got kinds=%v", fnLeadKinds)
	}

	// No real token's trailing should swallow the doc comment.
	tree.Root().Walk(func(r cst.Red) bool {
		if !r.IsToken() || r.Kind() != cst.GkToken {
			return true
		}
		tok := r.Token()
		if containsKind(triviaKinds(tree, tok.TrailingTrivia), cst.TriviaDocComment) {
			t.Errorf("token %q trailing contains doc comment; doc must always lead the next decl", tok.Text)
		}
		return true
	})
}

// TestBuildTailTriviaAfterNewline verifies that trivia following a trailing
// NEWLINE token becomes file-tail trivia (under the GkErrorMissing sentinel
// for now) rather than being attached as trailing of the NEWLINE. Round-trip
// must still reproduce the source byte-for-byte.
func TestBuildTailTriviaAfterNewline(t *testing.T) {
	const source = "fn f() {}\n// tail\n"
	tree := buildTreeFromSource(t, source)

	var newlineTrailedLineComment bool
	tree.Root().Walk(func(r cst.Red) bool {
		if !r.IsToken() || r.Kind() != cst.GkToken {
			return true
		}
		tok := r.Token()
		if tok.Text == "\n" && containsKind(triviaKinds(tree, tok.TrailingTrivia), cst.TriviaLineComment) {
			newlineTrailedLineComment = true
		}
		return true
	})
	if newlineTrailedLineComment {
		t.Error("NEWLINE token must not carry a trailing line-comment; it belongs to file tail")
	}

	if got := string(emitTreeBytes(tree)); got != source {
		t.Fatalf("round-trip mismatch:\nwant: %q\n got: %q", source, got)
	}
}

// --- test helpers ---

func buildTreeFromSource(t *testing.T, source string) *cst.Tree {
	t.Helper()
	tree, _ := selfhost.ParseCST([]byte(source))
	if tree == nil {
		t.Fatal("ParseCST returned nil tree")
	}
	return tree
}

func collectRealTokens(tree *cst.Tree) []cst.GreenToken {
	var out []cst.GreenToken
	tree.Root().Walk(func(r cst.Red) bool {
		if r.IsToken() && r.Kind() == cst.GkToken {
			out = append(out, r.Token())
		}
		return true
	})
	return out
}

func findTokenByText(tokens []cst.GreenToken, text string) (cst.GreenToken, bool) {
	for _, tok := range tokens {
		if tok.Text == text {
			return tok, true
		}
	}
	return cst.GreenToken{}, false
}

func findNthTokenByText(tokens []cst.GreenToken, text string, n int) int {
	seen := 0
	for i, tok := range tokens {
		if tok.Text == text {
			seen++
			if seen == n {
				return i
			}
		}
	}
	return -1
}

func triviaTexts(tree *cst.Tree, ids []int) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		tri := tree.Arena.TriviaAt(id)
		lo, hi := tri.Offset, tri.Offset+tri.Length
		if lo < 0 || hi > len(tree.Source) {
			out = append(out, "<oob>")
			continue
		}
		out = append(out, string(tree.Source[lo:hi]))
	}
	return out
}

func triviaKinds(tree *cst.Tree, ids []int) []cst.TriviaKind {
	out := make([]cst.TriviaKind, 0, len(ids))
	for _, id := range ids {
		out = append(out, tree.Arena.TriviaAt(id).Kind)
	}
	return out
}

func containsKind(kinds []cst.TriviaKind, target cst.TriviaKind) bool {
	for _, k := range kinds {
		if k == target {
			return true
		}
	}
	return false
}

func countKindBelow(root cst.Red, kind cst.GreenKind) int {
	count := 0
	root.Walk(func(r cst.Red) bool {
		if r.Kind() == kind {
			count++
		}
		return true
	})
	return count
}

// emitTreeBytes walks the tree in pre-order and concatenates, for each token
// leaf: leading trivia + text + trailing trivia. Result must equal
// tree.Source. Trailing trivia is the addition over the pre-split-policy
// implementation; omitting it here would break round-trip as soon as any
// token carries same-line trailing trivia.
func emitTreeBytes(tree *cst.Tree) []byte {
	src := tree.Source
	out := make([]byte, 0, len(src))
	emitRun := func(indices []int) {
		for _, triID := range indices {
			tri := tree.Arena.TriviaAt(triID)
			lo, hi := tri.Offset, tri.Offset+tri.Length
			if lo < 0 {
				lo = 0
			}
			if hi > len(src) {
				hi = len(src)
			}
			if lo < hi {
				out = append(out, src[lo:hi]...)
			}
		}
	}
	tree.Root().Walk(func(r cst.Red) bool {
		if !r.IsToken() {
			return true
		}
		tok := r.Token()
		emitRun(tok.LeadingTrivia)
		out = append(out, tok.Text...)
		emitRun(tok.TrailingTrivia)
		return true
	})
	return out
}
