package selfhost_test

import (
	"testing"

	"github.com/osty/osty/internal/format"
	"github.com/osty/osty/internal/selfhost"
)

func TestFormatSourceMatchesHostFormatter(t *testing.T) {
	cases := map[string][]byte{
		"compact-fn":   []byte("fn add(a:Int,b:Int)->Int{return a+b}"),
		"angle-depth":  []byte("fn max<T: Ordered + Hashable>(a:T,b:T)->T{if a>b{return a}else{return b}}\ntype Handler = fn(List<Int>, Map<String,List<Int>>) -> Result<(), Error?>\nfn load(json:Json,text:String)->Result<Config,Error>{let cfg=json.parse::<Config>(text)? return Ok(cfg)}"),
		"keyword-expr": []byte("enum Maybe{Some(Int),None}\nfn f(opt:Maybe,ok:Bool)->Int{if let Some(x)=opt{return x}else{return 0} for let Some(y)=opt{return y} return if ok{1}else{2}}\nfn g(opt:Maybe)->Int{return match opt { Some(v) if v > 0 -> v, Some(v) -> v, _ -> 0 }}"),
		"patterns":     []byte("enum Maybe{Some(Int),None}\nstruct User{name:Int,age:Int}\nfn f(v:Maybe,u:User)->Int{let User{name:n,age,..}=u return match v { Some( 1..= 3 ) | None -> 0, x @ Some( _ ) -> 1, _ -> n }}"),
		"prefix-ops":   []byte("fn neg(n:Int)->Int{return -n}\nfn not(ok:Bool)->Bool{return !ok}\nfn flip(n:Int)->Int{return ~n}\nfn expr(ok:Bool,x:Int)->Bool{return if ok{1}else{2}< -x}"),
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			want, wantDiags, err := format.OstySource(src)
			if err != nil {
				t.Fatalf("host format.OstySource error: %v", err)
			}
			if len(wantDiags) != 0 {
				t.Fatalf("host format.OstySource diagnostics = %d, want 0", len(wantDiags))
			}

			got, gotDiags, err := selfhost.FormatSource(src)
			if err != nil {
				t.Fatalf("selfhost.FormatSource error: %v", err)
			}
			if len(gotDiags) != 0 {
				t.Fatalf("selfhost.FormatSource diagnostics = %d, want 0", len(gotDiags))
			}
			if string(got) != string(want) {
				t.Fatalf("formatted output mismatch\nwant:\n%s\ngot:\n%s", want, got)
			}
		})
	}
}

func TestFormatSourceNormalizesCompatibilityInputLikeHostFormatter(t *testing.T) {
	src := append([]byte{0xEF, 0xBB, 0xBF}, []byte("fn add(a:Int,b:Int)->Int{\r\nreturn a+b\r\n}\r")...)

	want, wantDiags, err := format.OstySource(src)
	if err != nil {
		t.Fatalf("host format.OstySource error: %v", err)
	}
	if len(wantDiags) != 0 {
		t.Fatalf("host format.OstySource diagnostics = %d, want 0", len(wantDiags))
	}

	got, gotDiags, err := selfhost.FormatSource(src)
	if err != nil {
		t.Fatalf("selfhost.FormatSource error: %v", err)
	}
	if len(gotDiags) != 0 {
		t.Fatalf("selfhost.FormatSource diagnostics = %d, want 0", len(gotDiags))
	}
	if string(got) != string(want) {
		t.Fatalf("formatted output mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestFormatCheckTreatsRawInputNormalizationAsChange(t *testing.T) {
	src := append([]byte{0xEF, 0xBB, 0xBF}, []byte("fn add(a: Int, b: Int) -> Int {\r\n    return a + b\r\n}\r\n")...)

	checked, diags, err := selfhost.FormatCheck(src)
	if err != nil {
		t.Fatalf("selfhost.FormatCheck error: %v", err)
	}
	if len(diags) != 0 {
		t.Fatalf("selfhost.FormatCheck diagnostics = %d, want 0", len(diags))
	}
	if !checked.Changed {
		t.Fatal("FormatCheck Changed = false, want true for BOM/CRLF-normalized input")
	}

	want, wantDiags, err := format.OstySource(src)
	if err != nil {
		t.Fatalf("host format.OstySource error: %v", err)
	}
	if len(wantDiags) != 0 {
		t.Fatalf("host format.OstySource diagnostics = %d, want 0", len(wantDiags))
	}
	if string(checked.Output) != string(want) {
		t.Fatalf("FormatCheck output mismatch\nwant:\n%s\ngot:\n%s", want, checked.Output)
	}
}

func TestFormatSourceReturnsParseDiagnosticsOnFailure(t *testing.T) {
	src := []byte("fn f(){a < b < c}")

	out, diags, err := selfhost.FormatSource(src)
	if err == nil {
		t.Fatal("FormatSource error = nil, want parse error")
	}
	if out != nil {
		t.Fatalf("FormatSource output = %q, want nil", out)
	}
	if len(diags) == 0 {
		t.Fatal("FormatSource diagnostics = 0, want parse diagnostics")
	}
}
