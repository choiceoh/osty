package selfhost

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func BenchmarkRunDiagnostics(b *testing.B) {
	src := []byte(`fn main() {
    let xs = [1, 2, 3]
    let y = xs[0] + 2
    y
}
`)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		run := Run(src)
		if run == nil {
			b.Fatal("Run returned nil")
		}
		diags := run.Diagnostics()
		if len(diags) != 0 {
			b.Fatalf("len(diags) = %d, want 0", len(diags))
		}
	}
}

func BenchmarkCheckStructuredFromRun(b *testing.B) {
	src := []byte(`fn main() {
    let xs = [1, 2, 3]
    let y = xs[0] + 2
    y
}
`)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		run := Run(src)
		if run == nil {
			b.Fatal("Run returned nil")
		}
		result := CheckStructuredFromRun(run)
		if result.Summary.Errors != 0 {
			b.Fatalf("errors = %d, want 0", result.Summary.Errors)
		}
	}
}

func BenchmarkNewElabCx(b *testing.B) {
	src := []byte(`fn main() {
    let xs = [1, 2, 3]
    let y = xs[0] + 2
    y
}
`)
	run := Run(src)
	if run == nil {
		b.Fatal("Run returned nil")
	}
	file := run.semanticAstFile()
	if file == nil {
		b.Fatal("semanticAstFile returned nil")
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		cx := newElabCx(file, nil)
		if cx == nil || cx.env == nil || cx.core == nil {
			b.Fatal("newElabCx returned incomplete context")
		}
	}
}

func TestSelfhostEntryPointsRunConcurrently(t *testing.T) {
	sources := [][]byte{
		[]byte(`fn main() {
    let xs = [1, 2, 3]
    let y = xs[0] + 2
    y
}
`),
		[]byte(`fn id<T>(x: T) -> T { x }

fn main() {
    let value = id(42)
    value
}
`),
		[]byte(`struct User {
    name: String
}

fn main() {
    let user = User { name: "Ada" }
    user.name
}
`),
	}
	packageInput := PackageCheckInput{
		Files: []PackageCheckFile{{
			Source: sources[0],
			Base:   0,
		}},
	}
	const rounds = 8
	start := make(chan struct{})
	errs := make(chan string, rounds*4)
	var wg sync.WaitGroup
	launch := func(name string, idx int, run func(int) string) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if msg := run(idx); msg != "" {
				errs <- fmt.Sprintf("%s[%d]: %s", name, idx, msg)
			}
		}()
	}
	for i := 0; i < rounds; i++ {
		i := i
		launch("source", i, func(idx int) string {
			result := CheckSourceStructured(sources[idx%len(sources)])
			if result.Summary.Errors != 0 {
				return fmt.Sprintf("errors=%d details=%v", result.Summary.Errors, result.Summary.ErrorDetails)
			}
			return ""
		})
		launch("run", i, func(idx int) string {
			run := Run(sources[idx%len(sources)])
			result := CheckStructuredFromRun(run)
			if result.Summary.Errors != 0 {
				return fmt.Sprintf("errors=%d details=%v", result.Summary.Errors, result.Summary.ErrorDetails)
			}
			return ""
		})
		launch("package", i, func(int) string {
			result, err := CheckPackageStructured(packageInput)
			if err != nil {
				return err.Error()
			}
			if result.Summary.Errors != 0 {
				return fmt.Sprintf("errors=%d details=%v", result.Summary.Errors, result.Summary.ErrorDetails)
			}
			return ""
		})
		launch("inspect", i, func(int) string {
			records, err := InspectPackageStructured(packageInput)
			if err != nil {
				return err.Error()
			}
			if len(records) == 0 {
				return "no inspect records"
			}
			return ""
		})
	}
	close(start)
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Error(msg)
	}
}

func TestNewElabCxClonesFrozenPrelude(t *testing.T) {
	template := checkPreludeTemplateEnv()
	templateFnCount := len(template.global.fns)
	templateTyCount := len(template.tys.nodes)
	if templateFnCount == 0 || templateTyCount == 0 {
		t.Fatalf("empty prelude template: fns=%d tys=%d", templateFnCount, templateTyCount)
	}

	cx := newElabCx(nil, nil)
	checkRegisterFn(cx.env, &CheckFnSig{name: "localOnly", owner: "", retTy: tInt(cx.env.tys)})
	_ = tyNamed(cx.env.tys, "LocalOnly", []int{tInt(cx.env.tys)})

	if got := len(template.global.fns); got != templateFnCount {
		t.Fatalf("template fns len = %d, want %d", got, templateFnCount)
	}
	if got := len(template.tys.nodes); got != templateTyCount {
		t.Fatalf("template ty nodes len = %d, want %d", got, templateTyCount)
	}
	if _, ok := template.global.fnIndex.slots[checkFnKey("localOnly", "")]; ok {
		t.Fatalf("template observed function registered into cloned env")
	}
	fresh := newElabCx(nil, nil)
	if checkFnExists(fresh.env, "localOnly", "") {
		t.Fatalf("fresh env observed function registered into earlier clone")
	}
}

func TestPreludeTemplateMaterializesLocalCheckState(t *testing.T) {
	cx := newElabCx(nil, nil)
	if cx.env.local.returnTy != tErr(cx.env.tys) {
		t.Fatalf("returnTy = %d, want Err", cx.env.local.returnTy)
	}
	if cx.env.local.fnName != "" {
		t.Fatalf("fnName = %q, want empty", cx.env.local.fnName)
	}
	if cx.env.local.inLoop {
		t.Fatal("inLoop = true, want false")
	}
	if cx.env.local.assignments != 0 || cx.env.local.accepted != 0 {
		t.Fatalf("local counters = assignments:%d accepted:%d, want zero", cx.env.local.assignments, cx.env.local.accepted)
	}
	if len(cx.env.local.diagnostics) != 0 {
		t.Fatalf("diagnostics len = %d, want 0", len(cx.env.local.diagnostics))
	}
	if len(cx.env.local.bindingRecords) != 0 {
		t.Fatalf("bindingRecords len = %d, want 0", len(cx.env.local.bindingRecords))
	}
	if len(cx.env.local.symbolRecords) != 0 {
		t.Fatalf("symbolRecords len = %d, want 0", len(cx.env.local.symbolRecords))
	}
	if len(cx.env.local.instantiations) != 0 {
		t.Fatalf("instantiations len = %d, want 0", len(cx.env.local.instantiations))
	}
	if len(cx.env.local.aliasDeepCache) != 0 {
		t.Fatalf("aliasDeepCache len = %d, want 0", len(cx.env.local.aliasDeepCache))
	}
	if len(cx.env.local.substCacheKeys) != 0 || len(cx.env.local.substCacheHashes) != 0 || len(cx.env.local.substCacheValues) != 0 {
		t.Fatalf("subst cache lens = keys:%d hashes:%d values:%d, want zero", len(cx.env.local.substCacheKeys), len(cx.env.local.substCacheHashes), len(cx.env.local.substCacheValues))
	}
}

func BenchmarkEmptyTyArena(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		arena := emptyTyArena()
		if arena == nil || arena.idxInt < 0 || arena.idxString < 0 {
			b.Fatal("emptyTyArena returned incomplete arena")
		}
	}
}

func TestRunDefersLexAdaptationUntilTokensRequested(t *testing.T) {
	src := []byte(`fn main() {
    let x = 1
    x
}
`)

	run := Run(src)
	if run == nil {
		t.Fatal("Run returned nil")
	}
	if run.adapted {
		t.Fatal("Run eagerly adapted lex surfaces")
	}
	if run.lexDiags != nil {
		t.Fatal("Run eagerly adapted lexer diagnostics")
	}
	if diags := run.Diagnostics(); len(diags) != 0 {
		t.Fatalf("Diagnostics = %#v, want none", diags)
	}
	if run.adapted {
		t.Fatal("Diagnostics() should not force token/comment adaptation")
	}
	if run.lexDiags == nil {
		t.Fatal("Diagnostics() should populate lexer diagnostics lazily")
	}
	if toks := run.Tokens(); len(toks) == 0 {
		t.Fatal("Tokens() returned no tokens")
	}
	if !run.adapted {
		t.Fatal("Tokens() should force token/comment adaptation")
	}
}

// TestCheckStructuredFromRunMatchesCheckSourceStructured pins the
// invariant that feeds the check-path astbridge wedge: running the
// native checker on an existing FrontendRun's parser arena must
// produce the same CheckResult as the source-based entry point that
// re-lexes/re-parses. Downstream CLI call sites can then switch from
// CheckSourceStructured to CheckStructuredFromRun without any
// observable change.
func TestCheckStructuredFromRunMatchesCheckSourceStructured(t *testing.T) {
	cases := []struct {
		name string
		src  []byte
	}{
		{
			name: "scalar binary",
			src: []byte(`fn main() {
    let x = 1
    let y = x + 2
    y
}
`),
		},
		{
			name: "generic helper + call",
			src: []byte(`fn first<T>(xs: List<T>) -> T? {
    if xs.isEmpty() { None } else { Some(xs[0]) }
}

fn main() {
    let xs: List<Int> = [1, 2, 3]
    let head = first(xs)
}
`),
		},
		{
			name: "type error surfaces",
			src: []byte(`fn main() {
    let n: Int = "not an int"
}
`),
		},
		{
			name: "parser canonical helpers",
			src: []byte(`fn main() {
    let mut items = [1, 2]
    let count = len(items)
    items = append(items, count)
    let more = append(items, count)
    for (_, item) in enumerate(more) {
        println(item)
    }
}
`),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			legacy := CheckSourceStructured(tc.src)
			fresh := CheckStructuredFromRun(Run(tc.src))
			if !reflect.DeepEqual(legacy, fresh) {
				t.Fatalf("CheckStructuredFromRun diverges from CheckSourceStructured\nlegacy=%#v\nfresh=%#v", legacy, fresh)
			}
		})
	}
}

func TestCheckSourceStructuredAcceptsStdNamespaceValues(t *testing.T) {
	src := []byte(`use std.compress as compress
use std.encoding as encoding

fn main() {
    let _ = compress.gzip
    let _ = encoding.base64
    let _ = encoding.hex
    let _ = encoding.url
}
`)
	result := CheckSourceStructured(src)
	if result.Summary.Errors != 0 {
		t.Fatalf("errors = %d, want 0\nresult=%s", result.Summary.Errors, fmt.Sprintf("%#v", result))
	}
}

func TestRunDiagnosticsAllowDiscardInForInHead(t *testing.T) {
	src := []byte(`fn main() {
    let items = ["a", "b"]
    for (_, item) in enumerate(items) {
        println(item)
    }
}
`)
	run := Run(src)
	for _, d := range run.Diagnostics() {
		if d != nil && d.Code == "E0604" {
			t.Fatalf("unexpected wildcard parse diagnostic in for-in head: %#v", d)
		}
	}
}

// TestCheckStructuredFromRunSmoke exercises the PR8 wedge:
// CheckStructuredFromRun runs the native checker plus the
// intrinsic-body gate entirely off the FrontendRun's AstArena.
func TestCheckStructuredFromRunSmoke(t *testing.T) {
	src := []byte(`fn main() {
    let x = 1
    let y = x + 2
    y
}
`)
	run := Run(src)
	_ = CheckStructuredFromRun(run)
	_ = CheckStructuredFromRun(run)
}

// TestCheckSourceStructuredSmoke covers the full source-based check
// path through the generated checker and its internal gates.
func TestCheckSourceStructuredSmoke(t *testing.T) {
	src := []byte(`#[intrinsic]
fn bad() -> Int {
    42
}

fn main() {
    let x = 1
    let y = x + 2
    y
}
`)
	_ = CheckSourceStructured(src)
	_ = CheckSourceStructured(src)
}

func TestCheckFromSourceMatchesRunDiagnosticsAndStructuredResult(t *testing.T) {
	cases := []struct {
		name string
		src  []byte
	}{
		{
			name: "clean source",
			src: []byte(`fn main() {
    let x = 1
    let y = x + 2
    y
}
`),
		},
		{
			name: "type error",
			src: []byte(`fn main() {
    let n: Int = "oops"
}
`),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			run := Run(tc.src)
			wantDiags := run.Diagnostics()
			wantResult := CheckStructuredFromRun(run)

			gotDiags, gotResult := CheckFromSource(tc.src)
			if !reflect.DeepEqual(wantDiags, gotDiags) {
				t.Fatalf("CheckFromSource parse diagnostics diverge\nwant=%#v\ngot=%#v", wantDiags, gotDiags)
			}
			if !reflect.DeepEqual(wantResult, gotResult) {
				t.Fatalf("CheckFromSource result diverges\nwant=%#v\ngot=%#v", wantResult, gotResult)
			}
		})
	}
}

// TestCheckPackageStructuredIsAstbridgeFree is the multi-file analogue.
// The package adapter merges parser arenas and lets the generated checker
// run its internal gates, so no *ast.File lowering happens even with the
// Direct-path input.
func TestCheckPackageStructuredIsAstbridgeFree(t *testing.T) {
	aSrc := []byte(`pub fn helper() -> Int { 1 }
`)
	bSrc := []byte(`#[intrinsic]
fn bad() -> Int {
    42
}

fn main() {
    let _ = helper()
}
`)
	input := PackageCheckInput{
		Files: []PackageCheckFile{
			{Source: aSrc, Name: "a.osty", Path: "a.osty", Base: 0},
			{Source: bSrc, Name: "b.osty", Path: "b.osty", Base: len(aSrc) + 1},
		},
	}

	result, err := CheckPackageStructured(input)
	if err != nil {
		t.Fatalf("CheckPackageStructured: %v", err)
	}

	// Sanity: the intrinsic violation in b.osty surfaces as a
	// diagnostic, proving the gate walker actually ran.
	foundIntrinsic := false
	for _, d := range result.Diagnostics {
		if d.Code == "E0773" { // diag.CodeIntrinsicNonEmptyBody
			foundIntrinsic = true
			break
		}
	}
	if !foundIntrinsic {
		t.Fatalf("expected CodeIntrinsicNonEmptyBody diagnostic, got %#v", result.Diagnostics)
	}
}

// TestCheckStructuredFromRunIntrinsicGateEquivalence cross-checks the
// Run-based and source-based gate adapters produce identical output
// across representative shapes: both now walk AstArena, so the
// invariant is that they stay synchronized.
func TestCheckStructuredFromRunIntrinsicGateEquivalence(t *testing.T) {
	cases := []struct {
		name string
		src  []byte
	}{
		{
			name: "top-level intrinsic with body",
			src: []byte(`#[intrinsic]
fn bad() -> Int {
    42
}
`),
		},
		{
			name: "intrinsic method on struct",
			src: []byte(`pub struct Container {
    #[intrinsic]
    pub fn peek(self) -> Int {
        7
    }
}
`),
		},
		{
			name: "empty intrinsic body (negative case)",
			src: []byte(`#[intrinsic]
fn ok() -> Int {}
`),
		},
		{
			name: "non-intrinsic with body (negative case)",
			src: []byte(`fn plain() -> Int {
    1
}
`),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			legacy := CheckSourceStructured(tc.src)
			fresh := CheckStructuredFromRun(Run(tc.src))
			if !reflect.DeepEqual(legacy, fresh) {
				t.Fatalf("arena gate diverges from *ast.File gate\nlegacy=%#v\nfresh=%#v", legacy, fresh)
			}
		})
	}
}

func TestCheckSourceStructuredRecordsTypedExprCoverage(t *testing.T) {
	src := []byte(`fn main() {
    let x = 1
    let y = x + 2
    y
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0", checked.Summary.Errors)
	}

	kinds := map[string]int{}
	for _, node := range checked.TypedNodes {
		kinds[node.Kind]++
	}

	for _, want := range []string{"Ident", "Binary", "IntLit"} {
		if kinds[want] == 0 {
			t.Fatalf("typed node kinds = %#v, want %q to be recorded", kinds, want)
		}
	}
}

func TestCheckSourceStructuredAcceptsEarlyReturnIfBeforeNegativeTail(t *testing.T) {
	src := []byte(`fn pick(a: Bool, b: Bool) -> Int {
    if a && b { return 1 }
    -1
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
}

func TestCheckSourceStructuredRegistersPreludeFunctions(t *testing.T) {
	src := []byte(`fn main() {
    let p0 = print
    let p1 = println
    let p2 = eprint
    let p3 = eprintln
    let fail = panic
    p0("a")
    p1("b")
    p2("c")
    p3("d")
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails)
	}

	want := map[string]string{
		"p0":   "fn(String) -> ()",
		"p1":   "fn(String) -> ()",
		"p2":   "fn(String) -> ()",
		"p3":   "fn(String) -> ()",
		"fail": "fn(String) -> Never",
	}
	got := map[string]string{}
	for _, binding := range checked.Bindings {
		if _, ok := want[binding.Name]; ok {
			got[binding.Name] = binding.Type.String()
		}
	}
	for name, wantType := range want {
		if got[name] != wantType {
			t.Fatalf("binding type for %s = %q, want %q (all=%v)", name, got[name], wantType, got)
		}
	}
}

func TestCheckSourceStructuredAcceptsAliasQualifiedGoUseBodyTypes(t *testing.T) {
	src := []byte(`use go "example.com/host" as host {
    struct Item {
        Name: String
    }

    fn Make() -> Item
    fn All() -> List<Item>
}

fn main() {
    let item: host.Item = host.Make()
    let items: List<host.Item> = host.All()
    let name: String = item.Name
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails)
	}

	got := map[string]string{}
	for _, binding := range checked.Bindings {
		got[binding.Name] = binding.Type.String()
	}
	if got["item"] != "host.Item" {
		t.Fatalf("binding type for item = %q, want host.Item (all=%v)", got["item"], got)
	}
	if got["items"] != "List<host.Item>" {
		t.Fatalf("binding type for items = %q, want List<host.Item> (all=%v)", got["items"], got)
	}
	if got["name"] != "String" {
		t.Fatalf("binding type for name = %q, want String (all=%v)", got["name"], got)
	}
}

func TestCheckSourceStructuredAcceptsStdErrorOptionResultMethods(t *testing.T) {
	src := []byte(`use std.error

enum FsError {
    NotFound(String)

    pub fn message(self) -> String {
        match self {
            NotFound(path) -> "missing {path}",
        }
    }

    pub fn source(self) -> Error? {
        None
    }
}

fn main() {
    let base: Error = FsError.NotFound("settings.osty")
    let msg: String = base.message()
    let parent: Error? = base.source()
    let wrapped: Error = base.wrap("load")
    let chain: List<Error> = wrapped.chain()

    let maybe: Int? = Some(1)
    let some: Bool = maybe.isSomeAnd(|v| v > 0)
    let noneOr: Bool = maybe.isNoneOr(|v| v > 0)
    let contains: Bool = maybe.contains(1)
    let countMaybe: Int = maybe.count()
    let expected: Int = maybe.expect("need value")
    let fallback: Int = maybe.unwrapOrElse(|| 0)
    let zipped: (Int, String)? = maybe.zip(Some("ok"))
    let zippedWith: String? = maybe.zipWith(Some("ok"), |v: Int, label: String| "{label}:{v}")
    let reducedMaybe: Int? = maybe.reduce(Some(2), |a, b| a + b)
    let mapped: String? = maybe.map(|v| "{v}")
    let mappedOr: String = maybe.mapOr("zero", |v| "{v}")
    let okOr: Result<Int, Error> = maybe.okOr("missing")
    let okOrElse: Result<Int, FsError> = maybe.okOrElse(|| FsError.NotFound("fallback"))
    let maybeList: List<Int> = maybe.toList()
    maybe.forEach(|v| println("{v}"))

    let good: Result<Int, FsError> = Ok(1)
    let okAnd: Bool = good.isOkAnd(|v| v > 0)
    let containsOk: Bool = good.contains(1)
    let resultCount: Int = good.count()
    let inspected: Result<Int, FsError> = good.inspect(|v| println("{v}"))
    let mappedRes: Result<String, FsError> = good.map(|v| "{v}")
    let mappedOrElse: String = good.mapOrElse(|e| e.message(), |v| "{v}")
    let zippedResult: Result<(Int, String), FsError> = good.zip(Ok("ok"))
    let zippedResultWith: Result<String, FsError> = good.zipWith(Ok("ok"), |v: Int, label: String| "{label}:{v}")
    let goodList: List<Int> = good.toList()
    good.forEach(|v| println("{v}"))

    let bad: Result<Int, FsError> = Err(FsError.NotFound("broken"))
    let errAnd: Bool = bad.isErrAnd(|e| e.message().len() > 0)
    let recovered: Int = bad.unwrapOrElse(|e| e.message().len())
    let promoted: Result<Int, Error> = bad.mapErr(|e| wrapped)
    let chained: Result<Int, Error> = promoted.inspectErr(|e| println(e.message()))
    let alt: Result<Int, Error> = promoted.orElse(|e| Ok(e.message().len()))

    let textErr: Result<Int, String> = Err("bad")
    let containsErr: Bool = textErr.containsErr("bad")
    let expectErr: String = textErr.expectErr("want err")

    let _ = (msg, parent, chain, some, noneOr, contains, countMaybe, expected, fallback, zipped, zippedWith, reducedMaybe, mapped, mappedOr, okOr, okOrElse)
    let _ = (maybeList, okAnd, containsOk, resultCount, inspected, mappedRes, mappedOrElse, zippedResult, zippedResultWith, goodList)
    let _ = (errAnd, recovered, chained, alt, containsErr, expectErr)
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}

	got := map[string]string{}
	for _, binding := range checked.Bindings {
		got[binding.Name] = binding.Type.String()
	}
	want := map[string]string{
		"chain":            "List<Error>",
		"countMaybe":       "Int",
		"zippedWith":       "String?",
		"reducedMaybe":     "Int?",
		"okOrElse":         "Result<Int, FsError>",
		"maybeList":        "List<Int>",
		"resultCount":      "Int",
		"mappedOr":         "String",
		"mappedRes":        "Result<String, FsError>",
		"mappedOrElse":     "String",
		"zippedResult":     "Result<(Int, String), FsError>",
		"zippedResultWith": "Result<String, FsError>",
		"goodList":         "List<Int>",
		"promoted":         "Result<Int, Error>",
		"containsErr":      "Bool",
		"expectErr":        "String",
	}
	for name, wantType := range want {
		if got[name] != wantType {
			t.Fatalf("binding type for %s = %q, want %q (all=%v)", name, got[name], wantType, got)
		}
	}
}

func TestCheckSourceStructuredAcceptsStdListMethods(t *testing.T) {
	src := []byte(`fn main() {
    let words: List<String> = ["bb", "a", "ccc"]
    let idx: Int? = words.indexOf("a")
    let found: String? = words.find(|s| s.len() > 1)
    let sortedWords: List<String> = words.sortedBy(|s| s.len())
    let reversed: List<String> = words.reversed()
    let taken: List<String> = words.take(2)
    let dropped: List<String> = words.drop(1)
    let appended: List<String> = words.appended("tail")
    let combined: List<String> = words.concat(["tail"])
    let pairs: List<(String, Int)> = words.zip([1, 2, 3])
    let enumerated: List<(Int, String)> = words.enumerate()
    let grouped: Map<Int, List<String>> = words.groupBy(|s| s.len())

    let nums: List<Int> = [3, 1, 2, 4]
    let chunks: List<List<Int>> = nums.chunked(2)
    let windows: List<List<Int>> = nums.windowed(2, 1)
    let partitioned: (List<Int>, List<Int>) = nums.partition(|n| n % 2 == 0)
    let reduced: Int? = nums.reduce(|a, b| a + b)
    let scanned: List<Int> = nums.scan(0, |a, b| a + b)
    let flattened: List<Int> = nums.flatMap(|n| [n, n * 10])
    let triples: List<(Int, String, Bool)> = nums.zip3(["a", "b", "c"], [true, false, true])

    let mut mutNums: List<Int> = [3, 1, 2]
    let removed: Int = mutNums.removeAt(1)
    mutNums.sort()

    let _ = (idx, found, sortedWords, reversed, taken, dropped, appended, combined, pairs, enumerated, grouped)
    let _ = (chunks, windows, partitioned, reduced, scanned, flattened, triples, removed, mutNums)
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}

	got := map[string]string{}
	for _, binding := range checked.Bindings {
		got[binding.Name] = binding.Type.String()
	}
	want := map[string]string{
		"idx":         "Int?",
		"found":       "String?",
		"sortedWords": "List<String>",
		"pairs":       "List<(String, Int)>",
		"grouped":     "Map<Int, List<String>>",
		"windows":     "List<List<Int>>",
		"partitioned": "(List<Int>, List<Int>)",
		"reduced":     "Int?",
		"scanned":     "List<Int>",
		"flattened":   "List<Int>",
		"triples":     "List<(Int, String, Bool)>",
		"removed":     "Int",
	}
	for name, wantType := range want {
		if got[name] != wantType {
			t.Fatalf("binding type for %s = %q, want %q (all=%v)", name, got[name], wantType, got)
		}
	}
}

func TestCheckSourceStructuredAcceptsStdMapUpsertMethods(t *testing.T) {
	src := []byte(`fn makeDefault() -> Int {
    42
}

fn main() {
    let mut counts: Map<String, Int> = {:}
    let eager: Int = counts.getOrInsert("a", 1)
    let lazy: Int = counts.getOrInsertWith("b", makeDefault)

    let _ = (eager, lazy, counts)
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}

	got := map[string]string{}
	for _, binding := range checked.Bindings {
		got[binding.Name] = binding.Type.String()
	}
	want := map[string]string{
		"eager": "Int",
		"lazy":  "Int",
	}
	for name, wantType := range want {
		if got[name] != wantType {
			t.Fatalf("binding type for %s = %q, want %q (all=%v)", name, got[name], wantType, got)
		}
	}
}

func TestCheckSourceStructuredAcceptsDerivedStructEnumEqualHashableBounds(t *testing.T) {
	src := []byte(`struct Point {
    x: Int
    y: Int
}

struct Wrapper<T> {
    value: T
}

enum Token<T> {
    Value(T)
    Label(String)
}

fn needsEqual<T: Equal>(value: T) -> Bool {
    value == value
}

fn needsHash<T: Hashable>(value: T) -> Int {
    1
}

fn main() {
    let point = Point { x: 1, y: 2 }
    let wrapped: Wrapper<String> = Wrapper { value: "ok" }
    let token: Token<Int> = Token.Value(1)

    let eqPoint: Bool = needsEqual(point)
    let hashPoint: Int = needsHash(point)
    let eqWrapped: Bool = needsEqual(wrapped)
    let hashWrapped: Int = needsHash(wrapped)
    let eqToken: Bool = needsEqual(token)
    let hashToken: Int = needsHash(token)

    let pointMap: Map<Point, Int> = {:}
    let tokenMap: Map<Token<Int>, Int> = {:}

    let _ = (eqPoint, hashPoint, eqWrapped, hashWrapped, eqToken, hashToken, pointMap, tokenMap)
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}

	got := map[string]string{}
	for _, binding := range checked.Bindings {
		got[binding.Name] = binding.Type.String()
	}
	want := map[string]string{
		"eqPoint":     "Bool",
		"hashPoint":   "Int",
		"eqWrapped":   "Bool",
		"hashWrapped": "Int",
		"eqToken":     "Bool",
		"hashToken":   "Int",
		"pointMap":    "Map<Point, Int>",
		"tokenMap":    "Map<Token<Int>, Int>",
	}
	for name, wantType := range want {
		if got[name] != wantType {
			t.Fatalf("binding type for %s = %q, want %q (all=%v)", name, got[name], wantType, got)
		}
	}
}

func TestCheckSourceStructuredRejectsNonDerivableStructEnumEqualHashableBounds(t *testing.T) {
	src := []byte(`struct BadBox {
    f: fn() -> Int
}

enum BadTag {
    Keep(fn() -> Int)
}

fn one() -> Int {
    1
}

fn needsEqual<T: Equal>(value: T) -> Bool {
    true
}

fn needsHash<T: Hashable>(value: T) -> Int {
    0
}

fn main() {
    let badBox = BadBox { f: one }
    let badTag = BadTag.Keep(one)

    let _ = needsEqual(badBox)
    let _ = needsHash(badBox)
    let _ = needsEqual(badTag)
    let _ = needsHash(badTag)
}
`)

	checked := CheckSourceStructured(src)
	if got := checked.Summary.ErrorsByContext["E0749"]; got != 4 {
		t.Fatalf("E0749 count = %d, want 4 (summary=%#v details=%v diagnostics=%#v)", got, checked.Summary, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
	if checked.Summary.Errors != 4 {
		t.Fatalf("summary errors = %d, want 4 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
}

func TestCheckSourceStructuredAcceptsSelfTypedMethodsAndBuiltinSelfBounds(t *testing.T) {
	src := []byte(`interface Mergeable {
    fn same(self, other: Self) -> Bool
}

struct User {
    name: String

    fn renamed(self, name: String) -> Self {
        User { name }
    }

    fn same(self, other: Self) -> Bool {
        self.name == other.name
    }
}

struct Point {
    x: Int
    y: Int
}

fn compare<T: Mergeable>(a: T, b: T) -> Bool {
    a.same(b)
}

fn equals<T: Equal>(a: T, b: T) -> Bool {
    a.eq(b) && !a.ne(b)
}

fn ordered<T: Ordered>(a: T, b: T) -> Bool {
    a.le(b) || a.gt(b)
}

fn fingerprint<T: Hashable>(value: T) -> Int {
    value.hash()
}

fn main() {
    let user = User { name: "Ada" }
    let renamed: User = user.renamed("Grace")
    let sameUser: Bool = user.same(renamed)
    let merged: Bool = compare(user, renamed)

    let point = Point { x: 1, y: 2 }
    let samePoint: Bool = equals(point, point)
    let ord: Bool = ordered(1, 2)
    let digest: Int = fingerprint(point)
    let _ = (sameUser, merged, samePoint, ord, digest)
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}

	got := map[string]string{}
	for _, binding := range checked.Bindings {
		got[binding.Name] = binding.Type.String()
	}
	want := map[string]string{
		"renamed":   "User",
		"sameUser":  "Bool",
		"merged":    "Bool",
		"samePoint": "Bool",
		"ord":       "Bool",
		"digest":    "Int",
	}
	for name, wantType := range want {
		if got[name] != wantType {
			t.Fatalf("binding type for %s = %q, want %q (all=%v)", name, got[name], wantType, got)
		}
	}
}

func TestCheckSourceStructuredAcceptsDirectDerivedInterfaceMethods(t *testing.T) {
	src := []byte(`struct Point {
    x: Int
    y: Int
}

fn main() {
    let point = Point { x: 1, y: 2 }
    let samePoint: Bool = point.eq(point)
    let pointHash: Int = point.hash()

    let ints: List<Int> = [1, 2, 3]
    let sameInts: Bool = ints.eq([1, 2, 3])
    let intsHash: Int = ints.hash()

    let maybe: Int? = Some(1)
    let sameMaybe: Bool = maybe.eq(Some(1))
    let _ = (samePoint, pointHash, sameInts, intsHash, sameMaybe)
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}

	got := map[string]string{}
	for _, binding := range checked.Bindings {
		got[binding.Name] = binding.Type.String()
	}
	want := map[string]string{
		"samePoint": "Bool",
		"pointHash": "Int",
		"sameInts":  "Bool",
		"intsHash":  "Int",
		"sameMaybe": "Bool",
	}
	for name, wantType := range want {
		if got[name] != wantType {
			t.Fatalf("binding type for %s = %q, want %q (all=%v)", name, got[name], wantType, got)
		}
	}
}

func TestCheckSourceStructuredRejectsDirectDerivedInterfaceMethodsWhenUnavailable(t *testing.T) {
	src := []byte(`struct BadBox {
    f: fn() -> Int
}

fn one() -> Int {
    1
}

fn main() {
    let bad = BadBox { f: one }
    let floats: List<Float> = [1.0, 2.0]

    let _ = bad.eq(bad)
    let _ = bad.hash()
    let _ = floats.hash()
}
`)

	checked := CheckSourceStructured(src)
	if got := checked.Summary.ErrorsByContext["E0703"]; got != 3 {
		t.Fatalf("E0703 count = %d, want 3 (summary=%#v details=%v diagnostics=%#v)", got, checked.Summary, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
	if checked.Summary.Errors != 3 {
		t.Fatalf("summary errors = %d, want 3 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
}

func TestCheckSourceStructuredAcceptsErrorBoundsWithDefaultSource(t *testing.T) {
	src := []byte(`enum FsError {
    NotFound(String)

    fn message(self) -> String {
        match self {
            NotFound(path) -> "missing {path}",
        }
    }
}

fn describe<T: Error>(value: T) -> (String, Error?) {
    let msg: String = value.message()
    let parent: Error? = value.source()
    (msg, parent)
}

fn main() {
    let err = FsError.NotFound("settings.osty")
    let described: (String, Error?) = describe(err)
    let _ = described
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}

	got := map[string]string{}
	for _, binding := range checked.Bindings {
		got[binding.Name] = binding.Type.String()
	}
	if got["described"] != "(String, Error?)" {
		t.Fatalf("binding type for described = %q, want %q (all=%v)", got["described"], "(String, Error?)", got)
	}
}

func TestCheckSourceStructuredRejectsInvalidErrorBounds(t *testing.T) {
	src := []byte(`struct WrongError {
    code: Int

    fn message(self) -> Int {
        self.code
    }
}

fn describe<T: Error>(value: T) -> String {
    value.message()
}

fn main() {
    let err = WrongError { code: 7 }
    let _ = describe(err)
}
`)

	checked := CheckSourceStructured(src)
	if got := checked.Summary.ErrorsByContext["E0749"]; got != 1 {
		t.Fatalf("E0749 count = %d, want 1 (summary=%#v details=%v diagnostics=%#v)", got, checked.Summary, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
	if checked.Summary.Errors != 1 {
		t.Fatalf("summary errors = %d, want 1 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
}

func TestCheckSourceStructuredAcceptsDefaultBodiedInterfaceBoundsAndConcreteErrorHelpers(t *testing.T) {
	src := []byte(`interface Named {
    fn name(self) -> String
    fn label(self) -> String { self.name() + "!" }
}

struct User {
    name: String

    fn name(self) -> String {
        self.name
    }
}

enum FsError {
    NotFound(String)

    fn message(self) -> String {
        match self {
            NotFound(path) -> "missing {path}",
        }
    }
}

fn display<T: Named>(value: T) -> String {
    value.label()
}

fn main() {
    let user = User { name: "Ada" }
    let label: String = display(user)

    let err = FsError.NotFound("settings.osty")
    let parent: Error? = err.source()
    let wrapped: Error = err.wrap("load")
    let chain: List<Error> = err.chain()
    let exact: FsError? = err.downcast::<FsError>()
    let _ = (label, parent, wrapped, chain, exact)
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}

	got := map[string]string{}
	for _, binding := range checked.Bindings {
		got[binding.Name] = binding.Type.String()
	}
	if got["label"] != "String" {
		t.Fatalf("binding type for label = %q, want %q (all=%v)", got["label"], "String", got)
	}
	if got["parent"] != "Error?" {
		t.Fatalf("binding type for parent = %q, want %q (all=%v)", got["parent"], "Error?", got)
	}
	if got["wrapped"] != "Error" {
		t.Fatalf("binding type for wrapped = %q, want %q (all=%v)", got["wrapped"], "Error", got)
	}
	if got["chain"] != "List<Error>" {
		t.Fatalf("binding type for chain = %q, want %q (all=%v)", got["chain"], "List<Error>", got)
	}
	if got["exact"] != "FsError?" {
		t.Fatalf("binding type for exact = %q, want %q (all=%v)", got["exact"], "FsError?", got)
	}
}

func TestCheckSourceStructuredRejectsInvalidDefaultMethodOverrideBounds(t *testing.T) {
	src := []byte(`interface Named {
    fn name(self) -> String
    fn label(self) -> String { self.name() + "!" }
}

struct User {
    name: String

    fn name(self) -> String {
        self.name
    }

    fn label(self) -> Int {
        1
    }
}

fn display<T: Named>(value: T) -> String {
    value.label()
}

fn main() {
    let user = User { name: "Ada" }
    let _ = display(user)
}
`)

	checked := CheckSourceStructured(src)
	if got := checked.Summary.ErrorsByContext["E0749"]; got != 1 {
		t.Fatalf("E0749 count = %d, want 1 (summary=%#v details=%v diagnostics=%#v)", got, checked.Summary, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
	if checked.Summary.Errors != 1 {
		t.Fatalf("summary errors = %d, want 1 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
}

// G34: lossless numeric widening checks live in toolchain/check_env.osty
// but the frozen Go seed (internal/selfhost/generated.go) predates them.
// Once the seed catches up, these tests should assert 0 errors (widening)
// and E0765 (narrowing). For now they document the gap: widening rejected
// as E0700 generic mismatch, narrowing reported as E0700 instead of E0765.
//
// TestCheckSourceStructuredAcceptsLosslessWidening — would assert 0 errors.
// TestCheckSourceStructuredRejectsImplicitNarrowing — would assert E0765.

// G20: keyword argument syntax accepted at call sites.
// Parser creates AstNField_ nodes; direct calls work positionally.
func TestCheckSourceStructuredAcceptsFnValueKeywordArgs(t *testing.T) {
	src := []byte(`fn connect(host: String, port: Int) -> String {
    host + ":" + port.toString()
}

fn main() {
    let result = connect("localhost", port: 8080)
    println(result)
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
}

// G20: keyword argument through fn-value with unknown name emits E0769.
// Full path: collectFnDecl → tyFnWithNames → checkLookup → fn-value call.
func TestCheckSourceStructuredRejectsFnValueKeywordArgNameMismatch(t *testing.T) {
	src := []byte(`fn connect(host: String, port: Int) -> String {
    host + ":" + port.toString()
}

fn main() {
    let f = connect
    let result = f("localhost", timeout: 8080)
    println(result)
}
`)

	checked := CheckSourceStructured(src)
	if got := checked.Summary.ErrorsByContext["E0769"]; got != 1 {
		t.Fatalf("E0769 count = %d, want 1 (summary=%#v details=%v diagnostics=%#v)", got, checked.Summary, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
}

// G20: named fn type annotation preserves param names for keyword calls.
// let f: fn(host: String, port: Int) -> String = connect
func TestCheckSourceStructuredAcceptsNamedFnTypeKeywordArgs(t *testing.T) {
	src := []byte(`fn connect(host: String, port: Int) -> String {
    host + ":" + port.toString()
}

fn main() {
    let f: fn(host: String, port: Int) -> String = connect
    let result = f("localhost", port: 8080)
    println(result)
}
`)

	checked := CheckSourceStructured(src)
	if checked.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0 (contexts=%v details=%v diagnostics=%#v)", checked.Summary.Errors, checked.Summary.ErrorsByContext, checked.Summary.ErrorDetails, checked.Diagnostics)
	}
}
