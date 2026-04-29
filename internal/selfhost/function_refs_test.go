package selfhost

import "testing"

func TestPackageFunctionsFromRunReportsTopLevelTestShape(t *testing.T) {
	run := Run([]byte(`#[test]
fn inline_case() {
    let _ = 1
}

fn helper(n: Int) -> Int {
    n
}

struct Box {
    value: Int
    fn method(self) {}
}
`))
	if len(run.Diagnostics()) != 0 {
		t.Fatalf("parse diagnostics = %#v", run.Diagnostics())
	}

	funcs := PackageFunctionsFromRun(run)
	byName := map[string]PackageFunctionRef{}
	for _, fn := range funcs {
		byName[fn.Name] = fn
	}
	inline := byName["inline_case"]
	if !inline.HasBody || inline.HasReturn || inline.ParamCount != 0 {
		t.Fatalf("inline_case shape = %#v, want zero-param body with no return", inline)
	}
	if len(inline.Annotations) != 1 || inline.Annotations[0] != "test" {
		t.Fatalf("inline_case annotations = %#v, want [test]", inline.Annotations)
	}
	helper := byName["helper"]
	if helper.ParamCount != 1 || !helper.HasReturn {
		t.Fatalf("helper shape = %#v, want one param and return", helper)
	}
	if _, ok := byName["method"]; ok {
		t.Fatal("PackageFunctionsFromRun should not report struct methods")
	}
}
