package resolve

import (
	"reflect"
	"sort"
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/parser"
)

// TestWalkIdentAndNamedTypeParity exercises the hand-rolled
// `walkIdentAndNamedType` against the reflect-based legacy walker on a
// representative spec corpus, and asserts both visit the same set of
// `*ast.Ident` and `*ast.NamedType` nodes (id != 0). The hand-rolled
// path is on the install-self hot path; this guards against future
// schema additions in `internal/ast/ast.go` silently dropping nodes
// that the reflect walker would have caught.
func TestWalkIdentAndNamedTypeParity(t *testing.T) {
	sources := []struct {
		name string
		src  string
	}{
		{"empty_file", ""},
		{"simple_fn", `
fn add(a: Int, b: Int) -> Int { a + b }
`},
		{"generic_method", `
pub struct Box<T> {
    pub value: T,
    pub fn map<U>(self, f: fn(T) -> U) -> Box<U> {
        Box { value: f(self.value) }
    }
}
`},
		{"match_with_or_and_binding", `
fn classify(n: Int) -> String {
    match n {
        0 -> "zero",
        x @ 1..=9 -> "digit: {x}",
        _ -> "other",
    }
}
`},
		{"closure_and_pattern_param", `
fn group<K: Hashable, V>(xs: Map<K, V>) {
    let keys: List<K> = xs.entries().map(|(k, _)| k)
    let _ = keys
}
`},
		{"enum_with_methods", `
pub enum Event {
    Click(Int, Int),
    Key(String),
    Close,
    pub fn label(self) -> String {
        match self {
            Event.Click(_, _) -> "click",
            Event.Key(k) -> "key:{k}",
            Event.Close -> "close",
        }
    }
}
`},
		{"deeply_nested_types", `
type Lookup<K, V> = Map<K, Result<Option<List<V>>, Error>>
fn unwrap<T>(x: Result<Option<List<T>>, Error>) -> List<T> {
    x.unwrapOr(Ok(Some([]))).unwrapOr([])
}
`},
		{"annotations_and_doc", `
#[deprecated(since = "0.5")]
pub fn old() -> Int { 1 }

/// docs
pub struct User {
    #[json(key = "user_id")]
    pub id: Int,
    pub name: String,
}
`},
		{"string_interp_and_struct_lit", `
fn render(u: User) -> String {
    let updated = User { ..u, name: "new" }
    "user {updated.id}: {updated.name}"
}
`},
		{"for_let_and_range", `
fn drain(q: Queue) {
    for let Some(item) = q.pop() {
        let _ = item
    }
    for i in 0..10 by 2 { let _ = i }
}
`},
	}

	for _, tc := range sources {
		t.Run(tc.name, func(t *testing.T) {
			file, _ := parser.Parse([]byte(tc.src))
			if file == nil {
				return
			}

			identsReflect, typesReflect := collectViaReflect(file)
			identsHand, typesHand := collectViaHandWalker(file)

			sort.SliceStable(identsReflect, func(i, j int) bool { return identsReflect[i] < identsReflect[j] })
			sort.SliceStable(identsHand, func(i, j int) bool { return identsHand[i] < identsHand[j] })
			sort.SliceStable(typesReflect, func(i, j int) bool { return typesReflect[i] < typesReflect[j] })
			sort.SliceStable(typesHand, func(i, j int) bool { return typesHand[i] < typesHand[j] })

			if !reflect.DeepEqual(identsHand, identsReflect) {
				t.Fatalf("Ident set diverged.\n  hand:    %v\n  reflect: %v", identsHand, identsReflect)
			}
			if !reflect.DeepEqual(typesHand, typesReflect) {
				t.Fatalf("NamedType set diverged.\n  hand:    %v\n  reflect: %v", typesHand, typesReflect)
			}
		})
	}
}

func collectViaReflect(file *ast.File) ([]ast.NodeID, []ast.NodeID) {
	var idents, types []ast.NodeID
	walkReflect(reflect.ValueOf(file), func(id *ast.Ident) {
		if id != nil && id.ID != 0 {
			idents = append(idents, id.ID)
		}
	}, func(nt *ast.NamedType) {
		if nt != nil && nt.ID != 0 {
			types = append(types, nt.ID)
		}
	})
	return idents, types
}

func collectViaHandWalker(file *ast.File) ([]ast.NodeID, []ast.NodeID) {
	var idents, types []ast.NodeID
	walkIdentAndNamedType(file, func(id *ast.Ident) {
		if id != nil && id.ID != 0 {
			idents = append(idents, id.ID)
		}
	}, func(nt *ast.NamedType) {
		if nt != nil && nt.ID != 0 {
			types = append(types, nt.ID)
		}
	})
	return idents, types
}
