package airepair

import "testing"

func TestAnalyzeFrontEndAssistImprovesForeignSyntax(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("func main() {}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to change foreign syntax")
	}
	if got, want := string(result.Repaired), "fn main() {}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
	if !result.Improved {
		t.Fatal("expected airepair to mark the candidate as improved")
	}
	if !result.Accepted {
		t.Fatal("expected airepair to accept the repaired candidate")
	}
}

func TestAnalyzeRewriteModeAcceptsSafeLexicalRewrite(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("console.log(nil)\n"),
		Filename: "main.osty",
		Mode:     ModeRewriteOnly,
	})

	if !result.Changed {
		t.Fatal("expected rewrite mode to apply lexical rewrites")
	}
	if !result.Improved {
		t.Fatal("expected rewrite mode to treat applied rewrites as improvement")
	}
	if !result.Accepted {
		t.Fatal("expected rewrite mode to accept lexical rewrites")
	}
	if len(result.Repair.Changes) == 0 {
		t.Fatal("expected rewrite mode to surface change details")
	}
}

func TestAnalyzeFrontEndAssistRepairsJSStrictEquality(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("fn main() {\n    let ok = 1 === 1\n}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to change JS strict equality")
	}
	if got, want := string(result.Repaired), "fn main() {\n    let ok = 1 == 1\n}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}

func TestAnalyzeFrontEndAssistRewritesImportKeyword(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("import std.testing as t\nfn main() {}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to rewrite import keyword")
	}
	if got, want := string(result.Repaired), "use std.testing as t\nfn main() {}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}

func TestAnalyzeFrontEndAssistRewritesFromImport(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("from std import testing as t\nfn main() {}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to rewrite from-import syntax")
	}
	if got, want := string(result.Repaired), "use std.testing as t\nfn main() {}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}

func TestAnalyzeFrontEndAssistRepairsPythonFunctionBlock(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("fn main():\n    println(1)\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to rewrite Python-style function block")
	}
	if got, want := string(result.Repaired), "fn main() {\n    println(1)\n}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}

func TestAnalyzeFrontEndAssistRepairsPythonIfElseBlock(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("fn main() {\n    if true:\n        println(1)\n    else:\n        println(0)\n}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to rewrite Python-style if/else blocks")
	}
	if got, want := string(result.Repaired), "fn main() {\n    if true {\n        println(1)\n    } else {\n        println(0)\n    }\n}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}

func TestAnalyzeFrontEndAssistRepairsPythonElifBlock(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("fn main() {\n    if a:\n        println(1)\n    elif b:\n        println(2)\n    else:\n        println(0)\n}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to rewrite Python-style elif blocks")
	}
	if got, want := string(result.Repaired), "fn main() {\n    if a {\n        println(1)\n    } else if b {\n        println(2)\n    } else {\n        println(0)\n    }\n}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}

func TestAnalyzeFrontEndAssistRepairsBareTupleForLoopPattern(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("fn main() {\n    let items = [(1, 2)]\n    for k, v in items {\n        println(k)\n    }\n}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to rewrite a bare tuple for-loop pattern")
	}
	if got, want := string(result.Repaired), "fn main() {\n    let items = [(1, 2)]\n    for (k, v) in items {\n        println(k)\n    }\n}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}

func TestAnalyzeFrontEndAssistRewritesJSForOfLoop(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("fn main() {\n    let items = [1, 2]\n    for (const item of items) {\n        println(item)\n    }\n}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to rewrite a JS for-of loop")
	}
	if got, want := string(result.Repaired), "fn main() {\n    let items = [1, 2]\n    for item in items {\n        println(item)\n    }\n}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}

func TestAnalyzeFrontEndAssistRewritesJSDestructuringForOfLoop(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("fn main() {\n    let entries = [(1, 2)]\n    for (const [k, v] of entries) {\n        println(k)\n    }\n}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to rewrite a JS destructuring for-of loop")
	}
	if got, want := string(result.Repaired), "fn main() {\n    let entries = [(1, 2)]\n    for (k, v) in entries {\n        println(k)\n    }\n}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}

func TestAnalyzeFrontEndAssistRewritesPythonRangeLoop(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("fn main() {\n    for i in range(3):\n        println(i)\n}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to rewrite a Python range loop")
	}
	if got, want := string(result.Repaired), "fn main() {\n    for i in 0..3 {\n        println(i)\n    }\n}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}

func TestAnalyzeFrontEndAssistRewritesPythonRangeLoopWithStartAndUnitStep(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("fn main() {\n    for i in range(1, 4, 1):\n        println(i)\n}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to rewrite a Python range(start, end, 1) loop")
	}
	if got, want := string(result.Repaired), "fn main() {\n    for i in 1..4 {\n        println(i)\n    }\n}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}

func TestAnalyzeFrontEndAssistRewritesPythonEnumerateLoop(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("fn main() {\n    let items = [1, 2]\n    for i, item in enumerate(items):\n        println(item)\n}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to rewrite a Python enumerate loop")
	}
	if got, want := string(result.Repaired), "fn main() {\n    let items = [1, 2]\n    for (i, item) in items.enumerate() {\n        println(item)\n    }\n}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}

func TestAnalyzeFrontEndAssistCombinesWhileRewriteWithPythonBlockRepair(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("fn main() {\n    while true:\n        println(1)\n}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to rewrite while + colon block")
	}
	if got, want := string(result.Repaired), "fn main() {\n    for true {\n        println(1)\n    }\n}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}

func TestAnalyzeFrontEndAssistRepairsPythonMatchCaseBlock(t *testing.T) {
	result := Analyze(Request{
		Source:   []byte("fn main() {\n    let value = 0\n    match value:\n        case 0:\n            println(0)\n        default:\n            println(1)\n}\n"),
		Filename: "main.osty",
		Mode:     ModeFrontEndAssist,
	})

	if !result.Changed {
		t.Fatal("expected airepair to rewrite Python-style match/case blocks")
	}
	if got, want := string(result.Repaired), "fn main() {\n    let value = 0\n    match value {\n        0 -> {\n            println(0)\n        },\n        _ -> {\n            println(1)\n        },\n    }\n}\n"; got != want {
		t.Fatalf("repaired source = %q, want %q", got, want)
	}
	if result.Before.Parse.Errors == 0 {
		t.Fatalf("before.parse.errors = %d, want parse failures before repair", result.Before.Parse.Errors)
	}
	if result.After.Parse.Errors != 0 {
		t.Fatalf("after.parse.errors = %d, want 0 after repair", result.After.Parse.Errors)
	}
}
