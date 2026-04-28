package selfhost

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

var checkSnapshotFixtures = []struct {
	name string
	src  string
}{
	{
		name: "basic-let",
		src:  "fn main() {\n    let answer: Int = 42\n}\n",
	},
	{
		name: "generic-instantiation",
		src:  "fn id<T>(value: T) -> T { value }\nfn main() { let answer = id::<Int>(1) }\n",
	},
	{
		name: "diagnostic-type-mismatch",
		src:  "fn main() {\n    let answer: Int = \"oops\"\n}\n",
	},
	{
		name: "diagnostic-unknown-symbol",
		src:  "fn main() {\n    missing\n}\n",
	},
	{
		name: "diagnostic-generic-arity",
		src:  "fn id<T>(value: T) -> T { value }\nfn main() { let answer = id::<Int, String>(1) }\n",
	},
	{
		name: "diagnostic-field-not-found",
		src:  "struct User { name: String }\nfn main() {\n    let user = User { name: \"Ada\" }\n    let age = user.age\n}\n",
	},
	{
		name: "diagnostic-method-not-found",
		src:  "struct User { name: String }\nfn main() {\n    let user = User { name: \"Ada\" }\n    let label = user.label()\n}\n",
	},
	{
		name: "diagnostic-interface-bound",
		src:  "interface Named { fn name(self) -> String }\nstruct User { age: Int }\nfn display<T: Named>(value: T) -> String { value.name() }\nfn main() {\n    let user: User = User { age: 37 }\n    let label: String = display(user)\n}\n",
	},
	{
		name: "diagnostic-non-exhaustive-match",
		src:  "fn code(o: Option<Int>) -> Int {\n    match o {\n        Some(42) -> 1,\n        None -> 0,\n    }\n}\n",
	},
	{
		name: "diagnostic-pure-violation",
		src:  "fn impure() -> Int { 1 }\n#[pure]\nfn bad() -> Int {\n    let xs = [1]\n    impure()\n}\n",
	},
	{
		name: "diagnostic-intrinsic-violation",
		src:  "#[intrinsic]\nfn bad() -> Int {\n    42\n}\n",
	},
}

func TestCheckSnapshot(t *testing.T) {
	update := os.Getenv("UPDATE_SNAPSHOT") == "1"
	snapshotDir := filepath.Join("testdata", "check_snapshots")
	if update {
		if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
			t.Fatalf("mkdir snapshots: %v", err)
		}
	}

	for _, fixture := range checkSnapshotFixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			got := dumpCheckSnapshot(CheckSourceStructured([]byte(fixture.src)))
			goldenPath := filepath.Join(snapshotDir, fixture.name+".txt")
			if update {
				if err := os.WriteFile(goldenPath, []byte(got), 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				return
			}
			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden (set UPDATE_SNAPSHOT=1 to seed): %v", err)
			}
			wantStr := strings.ReplaceAll(string(want), "\r\n", "\n")
			if got != wantStr {
				t.Fatalf("check snapshot drift for %s\nfirst diff line: %s\n(re-run with UPDATE_SNAPSHOT=1 after a deliberate behavior change)", fixture.name, firstDiffLine(wantStr, got))
			}
		})
	}
}

func dumpCheckSnapshot(r CheckResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "summary assignments=%d accepted=%d errors=%d\n", r.Summary.Assignments, r.Summary.Accepted, r.Summary.Errors)
	writeCheckSnapshotMap(&b, "errorsByContext", r.Summary.ErrorsByContext)
	writeCheckSnapshotNestedMap(&b, "errorDetails", r.Summary.ErrorDetails)

	b.WriteString("typedNodes\n")
	for _, node := range r.TypedNodes {
		fmt.Fprintf(&b, "  node=%d nodeId=%d typeId=%d kind=%s span=%d:%d type=%s\n",
			node.Node, node.NodeID, node.TypeID, node.Kind, node.Start, node.End, checkSnapshotTypeString(node.Type))
	}

	b.WriteString("bindings\n")
	for _, binding := range r.Bindings {
		fmt.Fprintf(&b, "  bindingId=%d node=%d nodeId=%d name=%s mutable=%t typeId=%d span=%d:%d type=%s\n",
			binding.BindingID, binding.Node, binding.NodeID, binding.Name, binding.Mutable, binding.TypeID, binding.Start, binding.End, checkSnapshotTypeString(binding.Type))
	}

	b.WriteString("symbols\n")
	for _, symbol := range r.Symbols {
		fmt.Fprintf(&b, "  symbolId=%d node=%d nodeId=%d kind=%s name=%s owner=%s typeId=%d span=%d:%d type=%s\n",
			symbol.SymbolID, symbol.Node, symbol.NodeID, symbol.Kind, symbol.Name, symbol.Owner, symbol.TypeID, symbol.Start, symbol.End, checkSnapshotTypeString(symbol.Type))
	}

	b.WriteString("instantiations\n")
	for _, inst := range r.Instantiations {
		typeArgs := make([]string, 0, len(inst.TypeArgs))
		for i := range inst.TypeArgs {
			typeArgs = append(typeArgs, inst.TypeArgs[i].String())
		}
		fmt.Fprintf(&b, "  instantiationId=%d node=%d nodeId=%d callee=%s span=%d:%d typeArgIds=%v typeArgs=[%s] resultTypeId=%d resultType=%s\n",
			inst.InstantiationID, inst.Node, inst.NodeID, inst.Callee, inst.Start, inst.End, inst.TypeArgIDs, strings.Join(typeArgs, ", "), inst.ResultTypeID, checkSnapshotTypeString(inst.ResultType))
	}

	b.WriteString("diagnostics\n")
	for _, d := range r.Diagnostics {
		fmt.Fprintf(&b, "  code=%s severity=%s span=%d:%d line=%d:%d-%d:%d message=%s\n",
			d.Code, d.Severity, d.Start, d.End, d.StartLine, d.StartColumn, d.EndLine, d.EndColumn, d.Message)
		for _, note := range d.Notes {
			fmt.Fprintf(&b, "    note=%s\n", note)
		}
	}

	return b.String()
}

func writeCheckSnapshotMap(b *strings.Builder, label string, items map[string]int) {
	if len(items) == 0 {
		return
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		fmt.Fprintf(b, "%s %s=%d\n", label, key, items[key])
	}
}

func writeCheckSnapshotNestedMap(b *strings.Builder, label string, items map[string]map[string]int) {
	if len(items) == 0 {
		return
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		writeCheckSnapshotMap(b, label+" "+key, items[key])
	}
}

func checkSnapshotTypeString(typ *TypeRepr) string {
	if typ == nil {
		return ""
	}
	return typ.String()
}
