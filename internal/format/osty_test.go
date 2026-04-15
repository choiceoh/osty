package format

import (
	"bytes"
	"strings"
	"testing"

	"github.com/osty/osty/internal/parser"
)

func TestOstySourceMatchesCanonicalFixtures(t *testing.T) {
	for _, name := range fixtureNames(t) {
		t.Run(name, func(t *testing.T) {
			src := readFixture(t, name)
			want, wantDiags, wantErr := Source(normalizeOstyFormatterInput(src))
			got, gotDiags, gotErr := OstySource(src)
			if (gotErr != nil) != (wantErr != nil) {
				t.Fatalf("error mismatch: OstySource=%v Source=%v", gotErr, wantErr)
			}
			if len(gotDiags) != len(wantDiags) {
				t.Fatalf("diagnostic count mismatch: OstySource=%d Source=%d", len(gotDiags), len(wantDiags))
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("output mismatch:\n--- osty ---\n%s--- source ---\n%s", got, want)
			}
		})
	}
}

func TestOstySourceMatchesCanonicalTortureCases(t *testing.T) {
	cases := map[string]string{
		"angles": `fn max<T: Ordered + Hashable>(a:T,b:T)->T{if a>b{return a}else{return b}}
type Handler = fn(List<Int>, Map<String,List<Int>>) -> Result<(), Error?>
fn load(json:Json,text:String)->Result<Config,Error>{let cfg=json.parse::<Config>(text)? return Ok(cfg)}
fn splitClose()->Int{let xs:List<Int>=[] let m:Map<Int,List<Int>>=m return 0}
fn compare(a:Int,b:Int)->Bool{return a < b}
`,
		"patterns": `struct User{name:Int}
fn f(u:User)->Int{let User{name}=u let User{name}:User=u return name}
fn g(u:User)->Int{if let User{name}=u{return name}else{return 0}}
fn h(users:List<User>)->Int{for User{name} in users{return name} return 0}
fn k(xs:List<User>)->List<Int>{return xs.map(|User{name}:User|name)}
`,
		"keyword-exprs": `enum Maybe{Some(Int),None}
fn f(opt:Maybe,ok:Bool)->Int{if let Some(x)=opt{return x}else{return 0} for let Some(y)=opt{return y} return if ok{1}else{2}}
fn g(opt:Maybe)->Int{return match opt { Some(v) if v > 0 -> v, Some(v) -> v, _ -> 0 }}
fn h(opt:Maybe)->Int{return if let Some(z)=opt{z}else{0}}
fn i(opt:Maybe)->Int{if match opt { Some(v) -> v > 0, _ -> false } { return 1 } else { return 0 }}
`,
		"operators": `fn f(ok:Bool,x:Int)->Int{return if ok{1}else{2}+x}
fn g(x:Int)->Int{return match x { 0 -> 1, _ -> 2 } ?? 0}
fn h(x:Int)->Bool{return Box{value:x}==Box{value:0}}
fn r(ok:Bool)->Range{return if ok{0}else{1}..10}
fn lt(ok:Bool,x:Int)->Bool{return if ok{1}else{2}<x}
fn gt(ok:Bool,x:Int)->Bool{return if ok{1}else{2}>x}
fn neg(n:Int)->Int{return -n}
fn not(ok:Bool)->Bool{return !ok}
fn flip(n:Int)->Int{return ~n}
fn cmp(a:Int,b:Int)->Bool{return a < -b && a > -b}
fn loop(done:Bool){for !done{return}}
fn scrutinee(ok:Bool)->Int{return match !ok { true -> 1, _ -> 0 }}
fn later(ok:Bool){defer !ok}
`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			want, wantDiags, wantErr := Source([]byte(src))
			got, gotDiags, gotErr := OstySource([]byte(src))
			if (gotErr != nil) != (wantErr != nil) {
				t.Fatalf("error mismatch: OstySource=%v Source=%v", gotErr, wantErr)
			}
			if len(gotDiags) != len(wantDiags) {
				t.Fatalf("diagnostic count mismatch: OstySource=%d Source=%d", len(gotDiags), len(wantDiags))
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("output mismatch:\n--- osty ---\n%s--- source ---\n%s", got, want)
			}
		})
	}
}

func TestOstySourceFormatsFunctionSpacing(t *testing.T) {
	src := []byte("fn add(a:Int,b:Int)->Int{return a+b}\n")
	out, diags, err := OstySource(src)
	if err != nil {
		t.Fatalf("OstySource: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	want := "fn add(a: Int, b: Int) -> Int {\n    return a + b\n}\n"
	if string(out) != want {
		t.Fatalf("formatted mismatch:\nwant:\n%s\ngot:\n%s", want, out)
	}
}

func TestOstySourceIdempotentForFormattedSubset(t *testing.T) {
	src := []byte("fn add(a: Int, b: Int) -> Int {\n    return a + b\n}\n")
	once, _, err := OstySource(src)
	if err != nil {
		t.Fatalf("first format: %v", err)
	}
	twice, _, err := OstySource(once)
	if err != nil {
		t.Fatalf("second format: %v", err)
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("not idempotent:\n--- once ---\n%s--- twice ---\n%s", once, twice)
	}
}

func TestOstySourceOutputParses(t *testing.T) {
	out, diags, err := OstySource([]byte("struct Box<T>{value:T}\nfn get<T>(b:Box<T>)->T{return b.value}\n"))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("unexpected lex diagnostics: %v", diags)
	}
	if _, parseDiags := parser.ParseDiagnostics(out); len(parseDiags) != 0 {
		t.Fatalf("formatted output parse diagnostics: %v\n%s", parseDiags, out)
	}
}

func TestOstySourceAngleDepthTorture(t *testing.T) {
	src := []byte(`fn max<T: Ordered + Hashable>(a:T,b:T)->T{if a>b{return a}else{return b}}
type Handler = fn(List<Int>, Map<String,List<Int>>) -> Result<(), Error?>
fn load(json:Json,text:String)->Result<Config,Error>{let cfg=json.parse::<Config>(text)? return Ok(cfg)}
fn splitClose()->Int{let xs:List<Int>=[] let m:Map<Int,List<Int>>=m return 0}
fn compare(a:Int,b:Int)->Bool{return a < b}
`)
	out, diags, err := OstySource(src)
	if err != nil {
		t.Fatalf("format: %v\ndiags=%v", err, diags)
	}
	got := string(out)
	for _, want := range []string{
		"fn max<T: Ordered + Hashable>(a: T, b: T) -> T {",
		"type Handler = fn(List<Int>, Map<String, List<Int>>) -> Result<(), Error?>",
		"let cfg = json.parse::<Config>(text)?",
		"let xs: List<Int> = []",
		"let m: Map<Int, List<Int>> = m",
		"return a < b",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, got)
		}
	}
	once := out
	twice, _, err := OstySource(once)
	if err != nil {
		t.Fatalf("reformat: %v\n%s", err, once)
	}
	if !bytes.Equal(once, twice) {
		t.Fatalf("not idempotent:\n--- once ---\n%s--- twice ---\n%s", once, twice)
	}
	if _, parseDiags := parser.ParseDiagnostics(out); len(parseDiags) != 0 {
		t.Fatalf("formatted output parse diagnostics: %v\n%s", parseDiags, out)
	}
}

func TestOstySourceClosureAndAnnotationDepth(t *testing.T) {
	src := []byte(`#[deprecated(message = "old")]
fn f(xs:List<Int>)->List<Int>{return xs.map(|x:Int|x+1).filter(|x|x>1)}
fn g()->fn(Int)->Int{return || 1}
`)
	out, diags, err := OstySource(src)
	if err != nil {
		t.Fatalf("format: %v\ndiags=%v", err, diags)
	}
	got := string(out)
	for _, want := range []string{
		"#[deprecated(message = \"old\")]",
		"xs.map(|x: Int| x + 1).filter(|x| x > 1)",
		"return || 1",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, got)
		}
	}
}

func TestOstySourceTypeContextDepth(t *testing.T) {
	src := []byte(`enum Wire<T>{Packet(Result<List<T>,Error>),Fn(fn(Result<T,Error>,Map<String,List<T>>)->Result<(),Error>)}
fn tuple()->(Result<Int,Error>,fn(List<Int>,Result<String,Error>)->Map<String,Int>){return make()}
fn expr(a:Int,b:Int,c:Int,d:Int)->Bool{return call(a, b < c, d > a)}
`)
	out, diags, err := OstySource(src)
	if err != nil {
		t.Fatalf("format: %v\ndiags=%v", err, diags)
	}
	got := string(out)
	for _, want := range []string{
		"Packet(Result<List<T>, Error>)",
		"Fn(fn(Result<T, Error>, Map<String, List<T>>) -> Result<(), Error>)",
		"fn tuple() -> (Result<Int, Error>, fn(List<Int>, Result<String, Error>) -> Map<String, Int>)",
		"return call(a, b < c, d > a)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, got)
		}
	}
	if _, parseDiags := parser.ParseDiagnostics(out); len(parseDiags) != 0 {
		t.Fatalf("formatted output parse diagnostics: %v\n%s", parseDiags, out)
	}
	twice, _, err := OstySource(out)
	if err != nil {
		t.Fatalf("reformat: %v\n%s", err, out)
	}
	if !bytes.Equal(out, twice) {
		t.Fatalf("not idempotent:\n--- once ---\n%s--- twice ---\n%s", out, twice)
	}
}

func TestOstySourceBracePostfixAndEmptyMapDepth(t *testing.T) {
	src := []byte(`fn f()->String{return User{name:"Ada",meta:{:}}.name}
fn g()->Int{return makeMap()["answer"]}
`)
	out, diags, err := OstySource(src)
	if err != nil {
		t.Fatalf("format: %v\ndiags=%v", err, diags)
	}
	got := string(out)
	for _, want := range []string{
		"meta: {:}",
		"}.name",
		`return makeMap()["answer"]`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, got)
		}
	}
	if _, parseDiags := parser.ParseDiagnostics(out); len(parseDiags) != 0 {
		t.Fatalf("formatted output parse diagnostics: %v\n%s", parseDiags, out)
	}
	twice, _, err := OstySource(out)
	if err != nil {
		t.Fatalf("reformat: %v\n%s", err, out)
	}
	if !bytes.Equal(out, twice) {
		t.Fatalf("not idempotent:\n--- once ---\n%s--- twice ---\n%s", out, twice)
	}
}

func TestOstySourcePatternBraceDepth(t *testing.T) {
	src := []byte(`struct User{name:Int}
fn score(u:User)->Int{return match u { User{name} if name > 0 -> name, User{name} -> name, _ -> 0 }}
fn names(xs:List<User>)->List<Int>{return xs.map(|User{name}|name)}
`)
	out, diags, err := OstySource(src)
	if err != nil {
		t.Fatalf("format: %v\ndiags=%v", err, diags)
	}
	got := string(out)
	for _, want := range []string{
		"} if name > 0 -> name",
		"} -> name",
		"}| name",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, got)
		}
	}
	if _, parseDiags := parser.ParseDiagnostics(out); len(parseDiags) != 0 {
		t.Fatalf("formatted output parse diagnostics: %v\n%s", parseDiags, out)
	}
	twice, _, err := OstySource(out)
	if err != nil {
		t.Fatalf("reformat: %v\n%s", err, out)
	}
	if !bytes.Equal(out, twice) {
		t.Fatalf("not idempotent:\n--- once ---\n%s--- twice ---\n%s", out, twice)
	}
}

func TestOstySourceKeywordExpressionDepth(t *testing.T) {
	src := []byte(`enum Maybe{Some(Int),None}
fn f(opt:Maybe,ok:Bool)->Int{if let Some(x)=opt{return x}else{return 0} for let Some(y)=opt{return y} return if ok{1}else{2}}
fn g(opt:Maybe)->Int{return match opt { Some(v) if v > 0 -> v, Some(v) -> v, _ -> 0 }}
fn h(opt:Maybe)->Int{return if let Some(z)=opt{z}else{0}}
fn i(opt:Maybe)->Int{if match opt { Some(v) -> v > 0, _ -> false } { return 1 } else { return 0 }}
`)
	out, diags, err := OstySource(src)
	if err != nil {
		t.Fatalf("format: %v\ndiags=%v", err, diags)
	}
	got := string(out)
	for _, want := range []string{
		"if let Some(x) = opt {",
		"for let Some(y) = opt {",
		"return if ok {",
		"return match opt {",
		"Some(v) if v > 0 -> v",
		"return if let Some(z) = opt {",
		"if match opt {",
		"} {",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, got)
		}
	}
	if _, parseDiags := parser.ParseDiagnostics(out); len(parseDiags) != 0 {
		t.Fatalf("formatted output parse diagnostics: %v\n%s", parseDiags, out)
	}
	twice, _, err := OstySource(out)
	if err != nil {
		t.Fatalf("reformat: %v\n%s", err, out)
	}
	if !bytes.Equal(out, twice) {
		t.Fatalf("not idempotent:\n--- once ---\n%s--- twice ---\n%s", out, twice)
	}
}

func TestOstySourcePatternContinuationDepth(t *testing.T) {
	src := []byte(`struct User{name:Int}
fn f(u:User)->Int{let User{name}=u let User{name}:User=u return name}
fn g(u:User)->Int{if let User{name}=u{return name}else{return 0}}
fn h(users:List<User>)->Int{for User{name} in users{return name} return 0}
fn k(xs:List<User>)->List<Int>{return xs.map(|User{name}:User|name)}
`)
	out, diags, err := OstySource(src)
	if err != nil {
		t.Fatalf("format: %v\ndiags=%v", err, diags)
	}
	got := string(out)
	for _, want := range []string{
		"} = u",
		"}: User = u",
		"} in users {",
		"}: User| name",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, got)
		}
	}
	if _, parseDiags := parser.ParseDiagnostics(out); len(parseDiags) != 0 {
		t.Fatalf("formatted output parse diagnostics: %v\n%s", parseDiags, out)
	}
	twice, _, err := OstySource(out)
	if err != nil {
		t.Fatalf("reformat: %v\n%s", err, out)
	}
	if !bytes.Equal(out, twice) {
		t.Fatalf("not idempotent:\n--- once ---\n%s--- twice ---\n%s", out, twice)
	}
}

func TestOstySourceRightBraceInfixContinuationDepth(t *testing.T) {
	src := []byte(`fn f(ok:Bool,x:Int)->Int{return if ok{1}else{2}+x}
fn g(x:Int)->Int{return match x { 0 -> 1, _ -> 2 } ?? 0}
fn h(x:Int)->Bool{return Box{value:x}==Box{value:0}}
fn r(ok:Bool)->Range{return if ok{0}else{1}..10}
fn lt(ok:Bool,x:Int)->Bool{return if ok{1}else{2}<x}
fn gt(ok:Bool,x:Int)->Bool{return if ok{1}else{2}>x}
`)
	out, diags, err := OstySource(src)
	if err != nil {
		t.Fatalf("format: %v\ndiags=%v", err, diags)
	}
	got := string(out)
	for _, want := range []string{
		"} + x",
		"} ?? 0",
		"} == Box",
		"}..10",
		"} < x",
		"} > x",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, got)
		}
	}
	if _, parseDiags := parser.ParseDiagnostics(out); len(parseDiags) != 0 {
		t.Fatalf("formatted output parse diagnostics: %v\n%s", parseDiags, out)
	}
	twice, _, err := OstySource(out)
	if err != nil {
		t.Fatalf("reformat: %v\n%s", err, out)
	}
	if !bytes.Equal(out, twice) {
		t.Fatalf("not idempotent:\n--- once ---\n%s--- twice ---\n%s", out, twice)
	}
}

func TestOstySourcePrefixOperatorDepth(t *testing.T) {
	src := []byte(`fn neg(n:Int)->Int{return -n}
fn not(ok:Bool)->Bool{return !ok}
fn flip(n:Int)->Int{return ~n}
fn cmp(a:Int,b:Int)->Bool{return a < -b && a > -b}
fn expr(ok:Bool,x:Int)->Bool{return if ok{1}else{2}< -x}
fn loop(done:Bool){for !done{return}}
fn scrutinee(ok:Bool)->Int{return match !ok { true -> 1, _ -> 0 }}
fn later(ok:Bool){defer !ok}
`)
	out, diags, err := OstySource(src)
	if err != nil {
		t.Fatalf("format: %v\ndiags=%v", err, diags)
	}
	got := string(out)
	for _, want := range []string{
		"return -n",
		"return !ok",
		"return ~n",
		"return a < -b && a > -b",
		"} < -x",
		"for !done {",
		"return match !ok {",
		"defer !ok",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatted output missing %q:\n%s", want, got)
		}
	}
	if _, parseDiags := parser.ParseDiagnostics(out); len(parseDiags) != 0 {
		t.Fatalf("formatted output parse diagnostics: %v\n%s", parseDiags, out)
	}
	twice, _, err := OstySource(out)
	if err != nil {
		t.Fatalf("reformat: %v\n%s", err, out)
	}
	if !bytes.Equal(out, twice) {
		t.Fatalf("not idempotent:\n--- once ---\n%s--- twice ---\n%s", out, twice)
	}
}

func TestOstySourcePreservesLeadingLineComment(t *testing.T) {
	out, _, err := OstySource([]byte("// hello\nfn main(){return}\n"))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	got := string(out)
	if !strings.HasPrefix(got, "// hello\nfn main()") {
		t.Fatalf("leading comment not preserved:\n%s", got)
	}
}

func TestOstySourcePreservesLiteralLexemes(t *testing.T) {
	out, _, err := OstySource([]byte("fn f(){let c='한' let s=\"hi\"}\n"))
	if err != nil {
		t.Fatalf("format: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "let c = '한'") {
		t.Fatalf("char literal lexeme not preserved:\n%s", got)
	}
	if !strings.Contains(got, "let s = \"hi\"") {
		t.Fatalf("string literal lexeme not preserved:\n%s", got)
	}
}

func TestOstySourceRejectsLexErrors(t *testing.T) {
	_, diags, err := OstySource([]byte("fn main(){ let s = \"unterminated }\n"))
	if err == nil {
		t.Fatalf("expected lexical error")
	}
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics")
	}
}

func TestOstySourceRejectsParseErrors(t *testing.T) {
	_, diags, err := OstySource([]byte("fn f(){let x = }\n"))
	if err == nil {
		t.Fatalf("expected parse error")
	}
	if len(diags) == 0 {
		t.Fatalf("expected diagnostics")
	}
}
