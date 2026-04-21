package selfhost

import "testing"

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
