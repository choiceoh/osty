package selfhost

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/osty/osty/internal/selfhost/bundle"
)

const mirBootstrapSmokeSupport = `
fn listContainsString(xs: List<String>, needle: String) -> Bool {
    for x in xs {
        if x == needle {
            return true
        }
    }
    false
}

fn listContainsInt(xs: List<Int>, needle: Int) -> Bool {
    for x in xs {
        if x == needle {
            return true
        }
    }
    false
}

fn main() {
    let m = mirModule("pkg")
    let initFn = mirFunction("pkg.answer$init", "Int", mirNoSpan())
    let ret = mirFunctionNewLocal(initFn, "ret", "Int", false, mirNoSpan())
    initFn.locals[ret].isReturn = true
    initFn.returnLocal = ret
    let x = mirFunctionNewLocal(initFn, "x", "Int", false, mirNoSpan())
    let entry = mirFunctionNewBlock(initFn, mirNoSpan())
    initFn.entry = entry
    mirFunctionAppend(initFn, entry, mirAssignInstr(mirPlace(x), mirRVUse(mirOperandConst(mirConstInt(7, "Int"))), mirNoSpan()))
    mirFunctionAppend(initFn, entry, mirAssignInstr(mirPlace(ret), mirRVUse(mirOperandCopy(mirPlace(x), "Int")), mirNoSpan()))
    mirFunctionTerminate(initFn, entry, mirReturnTerm(mirNoSpan()))
    m.functions.push(initFn)

    let g = mirGlobal("answer", "Int", false, mirNoSpan())
    g.hasInit = true
    g.initSymbol = "pkg.answer$init"
    m.globals.push(g)

    mirOptimize(m)
    let errs = mirValidateModule(m)
    if errs.len() < 0 {
        println("bad")
        return
    }
    println("{m.functions[0].blocks[0].instrs[0].src.arg.const.intValue}")
}
`

func TestMirSourcesBootstrapSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping self-host MIR bootstrap smoke test in short mode")
	}

	repoRoot := filepath.Join("..", "..")
	merged, err := bundle.MergeFiles(repoRoot, []string{
		"toolchain/mir.osty",
		"toolchain/mir_optimize.osty",
		"toolchain/mir_validator.osty",
	})
	if err != nil {
		t.Fatalf("merge mir sources: %v", err)
	}

	tmpDir := t.TempDir()
	mergedPath := filepath.Join(tmpDir, "mir_smoke.osty")
	merged = append(merged, []byte(mirBootstrapSmokeSupport)...)
	if err := os.WriteFile(mergedPath, merged, 0o644); err != nil {
		t.Fatalf("write merged source: %v", err)
	}
	generatedPath := filepath.Join(tmpDir, "mir_smoke.go")

	gen := exec.Command(
		"go", "run", "./cmd/osty-bootstrap-gen",
		"--package", "main",
		"-o", generatedPath,
		mergedPath,
	)
	gen.Dir = repoRoot
	out, err := gen.CombinedOutput()
	if err != nil {
		t.Fatalf("transpile merged mir sources: %v\n%s", err, bytes.TrimSpace(out))
	}

	generated, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read generated file: %v", err)
	}
	if len(generated) == 0 {
		t.Fatal("generated file is empty")
	}
	if !bytes.Contains(generated, []byte("func mirOptimize(")) {
		t.Fatalf("generated file does not include mirOptimize")
	}
	if !bytes.Contains(generated, []byte("func mirValidateModule(")) {
		t.Fatalf("generated file does not include mirValidateModule")
	}

	run := exec.Command("go", "run", generatedPath)
	run.Dir = repoRoot
	out, err = run.CombinedOutput()
	if err != nil {
		t.Fatalf("run generated mir smoke:\n%v\n%s\nGenerated source:\n%s",
			err, bytes.TrimSpace(out), generated)
	}
	if got := bytes.TrimSpace(out); string(got) != "7" {
		t.Fatalf("unexpected generated mir smoke output: got %q want %q\nGenerated source:\n%s",
			got, "7", generated)
	}
}
