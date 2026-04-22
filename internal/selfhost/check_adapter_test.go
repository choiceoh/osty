package selfhost

import (
	"reflect"
	"testing"
)

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

// TestCheckStructuredFromRunIsAstbridgeFree pins the PR8 wedge:
// CheckStructuredFromRun runs the native checker plus the
// intrinsic-body gate entirely off the FrontendRun's AstArena, so the
// astbridge *ast.File lowering is never invoked. The only bump path
// is run.File() on explicit demand.
func TestCheckStructuredFromRunIsAstbridgeFree(t *testing.T) {
	src := []byte(`fn main() {
    let x = 1
    let y = x + 2
    y
}
`)
	ResetAstbridgeLowerCount()

	run := Run(src)
	if got := AstbridgeLowerCount(); got != 0 {
		t.Fatalf("Run alone: AstbridgeLowerCount = %d, want 0", got)
	}

	_ = CheckStructuredFromRun(run)
	if got := AstbridgeLowerCount(); got != 0 {
		t.Fatalf("after CheckStructuredFromRun: AstbridgeLowerCount = %d, want 0 (arena-direct check + arena-direct gate must not touch astbridge)", got)
	}

	_ = CheckStructuredFromRun(run)
	if got := AstbridgeLowerCount(); got != 0 {
		t.Fatalf("second CheckStructuredFromRun on same run: AstbridgeLowerCount = %d, want 0", got)
	}

	// Explicit run.File() still works and is the only way to bump the
	// counter — proves the counter wiring isn't accidentally short-
	// circuited by the earlier CheckStructuredFromRun calls.
	if f := run.File(); f == nil {
		t.Fatalf("run.File() returned nil")
	}
	if got := AstbridgeLowerCount(); got != 1 {
		t.Fatalf("after explicit run.File(): AstbridgeLowerCount = %d, want 1", got)
	}
}

// TestCheckStructuredFromRunIntrinsicGateEquivalence cross-checks the
// arena gate walker against the legacy *ast.File walker. Both must
// flag the same `#[intrinsic]` non-empty-body violation with identical
// code, offsets, and notes, even in the presence of methods, use-go
// bodies, and multi-annotation groups.
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
			got[binding.Name] = binding.TypeName
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
		got[binding.Name] = binding.TypeName
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
