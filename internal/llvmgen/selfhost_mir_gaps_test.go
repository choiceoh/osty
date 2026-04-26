package llvmgen

import (
	"errors"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/mir"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

func TestSelfhostDocgenAddRefsFromTextMIRDirect(t *testing.T) {
	src := `use std.strings as strings

struct SelfDocRef {
    name: String,
    anchor: String,
}

fn selfDocStringListCount(xs: List<String>) -> Int { xs.len() }
fn selfDocIdentStart(unit: String) -> Bool { unit == "_" || (unit >= "a" && unit <= "z") || (unit >= "A" && unit <= "Z") }
fn selfDocIdentCont(unit: String) -> Bool { selfDocIdentStart(unit) || (unit >= "0" && unit <= "9") }
fn selfDocIndexAnchor(refs: List<SelfDocRef>, name: String) -> String { "" }
fn listContainsString(xs: List<String>, needle: String) -> Bool { false }

fn selfDocAddRefsFromText(
    existing: List<String>,
    text: String,
    refs: List<SelfDocRef>,
    exclude: String,
) -> List<String> {
    let mut out = existing
    let units = strings.split(text, "")
    let mut idx = 0
    let count = selfDocStringListCount(units)
    for idx < count {
        let unit = units[idx]
        if selfDocIdentStart(unit) {
            let mut word = unit
            idx = idx + 1
            for idx < count && selfDocIdentCont(units[idx]) {
                word = "{word}{units[idx]}"
                idx = idx + 1
            }
            let anchor = selfDocIndexAnchor(refs, word)
            if anchor != "" && word != exclude && !(listContainsString(out, word)) {
                out.push(word)
            }
        } else {
            idx = idx + 1
        }
    }
    out
}
`
	file := parseLLVMGenFile(t, src)
	res := resolve.ResolveFileDefault(file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	mono, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("monomorphize: %v", monoErrs)
	}
	poisonSelfhostDocgenInterpolationIndexTypes(t, mono)
	mirMod := mir.Lower(mono)
	fn := mirMod.LookupFunction("selfDocAddRefsFromText")
	if fn == nil {
		t.Fatal("missing selfDocAddRefsFromText")
	}
	for _, loc := range fn.Locals {
		if loc == nil {
			continue
		}
		if _, ok := loc.Type.(*ir.ErrType); ok {
			t.Fatalf("selfhost MIR local stayed poisoned: id=%d name=%s\n%s", loc.ID, loc.Name, mir.PrintFunction(fn))
		}
	}
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/selfhost_docgen_add_refs.osty",
	})
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			t.Fatalf("MIR-direct selfhost docgen path unsupported: %v\n%s", err, mir.PrintFunction(fn))
		}
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"define ptr @selfDocAddRefsFromText(",
		"call ptr @osty_rt_strings_Split(",
		"call ptr @osty_rt_strings_Concat(",
		"call void @osty_rt_list_push_ptr(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestSelfhostHirOptimizeCharToDigitMIRDirect(t *testing.T) {
	src := `fn hirOptimizeCharToDigit(ch: Char) -> Int {
    match ch {
        '0' -> 0,
        '1' -> 1,
        '2' -> 2,
        '3' -> 3,
        '4' -> 4,
        '5' -> 5,
        '6' -> 6,
        '7' -> 7,
        '8' -> 8,
        '9' -> 9,
        'a' -> 10,
        'b' -> 11,
        'c' -> 12,
        'd' -> 13,
        'e' -> 14,
        'f' -> 15,
        'A' -> 10,
        'B' -> 11,
        'C' -> 12,
        'D' -> 13,
        'E' -> 14,
        'F' -> 15,
        _ -> -1,
    }
}
`
	file := parseLLVMGenFile(t, src)
	res := resolve.ResolveFileDefault(file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	mono, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("monomorphize: %v", monoErrs)
	}
	mirMod := mir.Lower(mono)
	fn := mirMod.LookupFunction("hirOptimizeCharToDigit")
	if fn == nil {
		t.Fatal("missing hirOptimizeCharToDigit")
	}
	for _, bb := range fn.Blocks {
		if bb != nil && bb.Term == nil {
			t.Fatalf("hirOptimizeCharToDigit MIR block missing terminator:\n%s", mir.PrintFunction(fn))
		}
	}
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/selfhost_hir_optimize_char_digit.osty",
	})
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			t.Fatalf("MIR-direct selfhost char digit path unsupported: %v\n%s", err, mir.PrintFunction(fn))
		}
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"define i64 @hirOptimizeCharToDigit(",
		"icmp eq i32",
		"ret i64",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestSelfhostHintListElemTrimMIRDirect(t *testing.T) {
	src := `fn hintListElem(hint: String) -> String {
    if !hint.startsWith("List<") {
        return ""
    }
    if !hint.endsWith(">") {
        return ""
    }
    let n = hint.len()
    if n <= 6 {
        return ""
    }
    hint[5..n - 1].trim()
}
`
	file := parseLLVMGenFile(t, src)
	res := resolve.ResolveFileDefault(file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	mono, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("monomorphize: %v", monoErrs)
	}
	mirMod := mir.Lower(mono)
	fn := mirMod.LookupFunction("hintListElem")
	if fn == nil {
		t.Fatal("missing hintListElem")
	}
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/selfhost_hint_list_elem.osty",
	})
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			t.Fatalf("MIR-direct selfhost hintListElem path unsupported: %v\n%s", err, mir.PrintFunction(fn))
		}
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"define ptr @hintListElem(",
		"call ptr @osty_rt_strings_Slice(",
		"call ptr @osty_rt_strings_TrimSpace(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestSelfhostMirLlvmTypeHeadNameIndexMIRDirect(t *testing.T) {
	src := `use std.strings as llvmStrings

fn mirLlvmTypeHeadName(typeText: String) -> String {
    let ltIdx = llvmStrings.Index(typeText, "<")
    let stripped = if ltIdx >= 0 {
        typeText[0..ltIdx]
    } else {
        typeText
    }
    let dotIdx = llvmStrings.Index(stripped, ".")
    if dotIdx >= 0 {
        stripped[(dotIdx + 1)..stripped.len()]
    } else {
        stripped
    }
}
`
	file := parseLLVMGenFile(t, src)
	res := resolve.ResolveFileDefault(file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	mono, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("monomorphize: %v", monoErrs)
	}
	mirMod := mir.Lower(mono)
	fn := mirMod.LookupFunction("mirLlvmTypeHeadName")
	if fn == nil {
		t.Fatal("missing mirLlvmTypeHeadName")
	}
	for _, loc := range fn.Locals {
		if loc == nil {
			continue
		}
		if _, ok := loc.Type.(*ir.ErrType); ok {
			t.Fatalf("mirLlvmTypeHeadName MIR local stayed poisoned: id=%d name=%s\n%s", loc.ID, loc.Name, mir.PrintFunction(fn))
		}
	}
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/selfhost_mir_type_head.osty",
	})
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			t.Fatalf("MIR-direct selfhost mirLlvmTypeHeadName path unsupported: %v\n%s", err, mir.PrintFunction(fn))
		}
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"define ptr @mirLlvmTypeHeadName(",
		"call i64 @osty_rt_strings_IndexOf(",
		"call ptr @osty_rt_strings_Slice(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestSelfhostCheckDiagRenderFieldInterpolationMIRDirect(t *testing.T) {
	src := `struct CheckDiagnostic {
    code: String,
    message: String,
}

fn checkDiagRender(d: CheckDiagnostic) -> String {
    let prefix = "error"
    if d.code == "" {
        return "{prefix}: {d.message}"
    }
    "{prefix}[{d.code}]: {d.message}"
}
`
	file := parseLLVMGenFile(t, src)
	res := resolve.ResolveFileDefault(file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	mono, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("monomorphize: %v", monoErrs)
	}
	poisonSelfhostCheckDiagFieldInterpolationTypes(t, mono)
	mirMod := mir.Lower(mono)
	fn := mirMod.LookupFunction("checkDiagRender")
	if fn == nil {
		t.Fatal("missing checkDiagRender")
	}
	assertStringConcatArgsTyped(t, fn)
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/selfhost_check_diag_render.osty",
	})
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			t.Fatalf("MIR-direct selfhost checkDiagRender path unsupported: %v\n%s", err, mir.PrintFunction(fn))
		}
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"define ptr @checkDiagRender(",
		"call ptr @osty_rt_strings_ConcatN(",
		"extractvalue %CheckDiagnostic",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func TestSelfhostCompositeListPopMIRDirect(t *testing.T) {
	src := `struct CheckBinding {
    name: String,
    depth: Int,
}

fn popBinding(xs: List<CheckBinding>) -> CheckBinding? {
    xs.pop()
}
`
	file := parseLLVMGenFile(t, src)
	res := resolve.ResolveFileDefault(file, stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.SelfhostFile(file, res, check.Opts{
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, issues := ir.Lower("main", file, res, chk)
	if len(issues) != 0 {
		t.Fatalf("ir.Lower issues: %v", issues)
	}
	mono, monoErrs := ir.Monomorphize(mod)
	if len(monoErrs) != 0 {
		t.Fatalf("monomorphize: %v", monoErrs)
	}
	mirMod := mir.Lower(mono)
	fn := mirMod.LookupFunction("popBinding")
	if fn == nil {
		t.Fatal("missing popBinding")
	}
	out, err := GenerateFromMIR(mirMod, Options{
		PackageName: "main",
		SourcePath:  "/tmp/selfhost_composite_list_pop.osty",
	})
	if err != nil {
		if errors.Is(err, ErrUnsupported) {
			t.Fatalf("MIR-direct selfhost composite list pop path unsupported: %v\n%s", err, mir.PrintFunction(fn))
		}
		t.Fatalf("GenerateFromMIR: %v", err)
	}
	got := string(out)
	for _, want := range []string{
		"define %Option.CheckBinding @popBinding(",
		"call void @osty_rt_list_get_bytes_v1(",
		"call void @osty_rt_list_pop_discard(",
		"call ptr @osty.gc.alloc_v1(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("generated IR missing %q:\n%s", want, got)
		}
	}
}

func poisonSelfhostDocgenInterpolationIndexTypes(t *testing.T, mod *ir.Module) {
	t.Helper()
	var fn *ir.FnDecl
	for _, decl := range mod.Decls {
		if f, ok := decl.(*ir.FnDecl); ok && f.Name == "selfDocAddRefsFromText" {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("missing IR selfDocAddRefsFromText")
	}
	poisoned := 0
	ir.Inspect(fn.Body, func(n ir.Node) bool {
		lit, ok := n.(*ir.StringLit)
		if !ok {
			return true
		}
		for _, part := range lit.Parts {
			idx, ok := part.Expr.(*ir.IndexExpr)
			if !ok {
				continue
			}
			base, ok := idx.X.(*ir.Ident)
			if !ok || base.Name != "units" {
				continue
			}
			base.T = &ir.TypeVar{Name: "T", Owner: "selfDocAddRefsFromText"}
			idx.T = &ir.ErrType{}
			poisoned++
		}
		return true
	})
	if poisoned == 0 {
		t.Fatal("did not find docgen interpolation index to poison")
	}
}

func poisonSelfhostCheckDiagFieldInterpolationTypes(t *testing.T, mod *ir.Module) {
	t.Helper()
	var fn *ir.FnDecl
	for _, decl := range mod.Decls {
		if f, ok := decl.(*ir.FnDecl); ok && f.Name == "checkDiagRender" {
			fn = f
			break
		}
	}
	if fn == nil {
		t.Fatal("missing IR checkDiagRender")
	}
	poisoned := 0
	ir.Inspect(fn.Body, func(n ir.Node) bool {
		lit, ok := n.(*ir.StringLit)
		if !ok {
			return true
		}
		for _, part := range lit.Parts {
			field, ok := part.Expr.(*ir.FieldExpr)
			if !ok {
				continue
			}
			base, ok := field.X.(*ir.Ident)
			if !ok || base.Name != "d" {
				continue
			}
			field.T = &ir.ErrType{}
			poisoned++
		}
		return true
	})
	if poisoned == 0 {
		t.Fatal("did not find checkDiagRender interpolation fields to poison")
	}
}

func assertStringConcatArgsTyped(t *testing.T, fn *mir.Function) {
	t.Helper()
	for _, bb := range fn.Blocks {
		if bb == nil {
			continue
		}
		for _, instr := range bb.Instrs {
			in, ok := instr.(*mir.IntrinsicInstr)
			if !ok || in.Kind != mir.IntrinsicStringConcat {
				continue
			}
			for idx, arg := range in.Args {
				if _, ok := arg.Type().(*ir.ErrType); ok {
					t.Fatalf("string_concat arg %d stayed poisoned:\n%s", idx, mir.PrintFunction(fn))
				}
			}
		}
	}
}
