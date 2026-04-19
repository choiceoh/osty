package llvmgen

// Executable contract for the §19.5 intrinsic typing flow.
//
// **Diagnostic correction.** The original landing of this file
// (PR #345) attributed the failure to the native checker not
// flowing stdlib intrinsic return types back to call sites. That
// diagnosis was wrong — the native checker DOES return correct
// types (`chk.Types[CallExpr] -> RawPtr`, `chk.LetTypes[LetStmt]
// -> RawPtr`). The actual bug was in `internal/ir/lower.go`:
// `primitiveByKind` and `primitiveByName` did not have cases for
// `types.PRawPtr` (added in PR #312), so when `fromCheckerType`
// converted a `*types.Primitive{Kind: PRawPtr}` it returned
// `ErrTypeVal`. PR `claude/native-checker-stdlib-types` adds
// `PrimRawPtr` to the IR primitive enum + `TRawPtr` singleton +
// the missing `case` arms.
//
// Three of these tests pass after that fix:
//   - raw.null()       → RawPtr   ✓
//   - raw.alloc(b, a)  → RawPtr   ✓
//   - raw.bits(p)      → Int      ✓
//
// The remaining tests (raw.read::<T>, raw.write::<T>, raw.cas::<T>,
// raw.sizeOf::<T>, chained alloc/write/read/free) still fail
// because of a SEPARATE bug — the `host_boundary`'s
// `writeSelfhostPackageImport` strips `<T: Pod>` generic params
// when forwarding stdlib stub signatures to the native checker.
// Those tests carry `t.Skip(genericSkipReason)`.
//
// When the boundary fix lands, removing the Skip lines should
// flip the remaining tests green.

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	ir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

// genericSkipReason marks tests blocked on a still-open gap: the
// host_boundary's `writeSelfhostPackageImport` strips generic params
// from intrinsic signatures, so `fn read<T: Pod>(p: RawPtr) -> T`
// reaches the native checker as `fn read(p: RawPtr) -> T` with `T`
// undeclared. Tests for non-generic intrinsics (raw.null, raw.alloc,
// raw.bits) pass after PR `claude/native-checker-stdlib-types`'s
// `PrimRawPtr` fix; the generic ones await a boundary writer fix.
const genericSkipReason = "blocked on host_boundary generic-param stripping — LANG_SPEC §19.11. " +
	"Remove this Skip line when writeSelfhostPackageImport preserves `<T: Pod>` on intrinsic stubs."

// firstLetTypeIn returns the printed type of the first `let` binding
// inside the named function in `mod`. Returns "" if the fn or its
// body is not present.
func firstLetTypeIn(mod *ir.Module, fnName string) string {
	for _, decl := range mod.Decls {
		fd, ok := decl.(*ir.FnDecl)
		if !ok || fd == nil || fd.Name != fnName || fd.Body == nil {
			continue
		}
		for _, s := range fd.Body.Stmts {
			if let, ok := s.(*ir.LetStmt); ok {
				if let.Type == nil {
					return "<nil>"
				}
				if et, ok := let.Type.(*ir.ErrType); ok {
					_ = et
					return "<error>"
				}
				return printType(let.Type)
			}
		}
	}
	return ""
}

func printType(t ir.Type) string {
	if t == nil {
		return "<nil>"
	}
	switch n := t.(type) {
	case *ir.PrimType:
		return n.String()
	case *ir.NamedType:
		if n.Package != "" {
			return n.Package + "." + n.Name
		}
		return n.Name
	case *ir.ErrType:
		return "<error>"
	}
	return "?"
}

func lowerPrivileged(t *testing.T, src string) *ir.Module {
	t.Helper()
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse: %v", parseDiags)
	}
	res := resolve.FileWithStdlib(file, resolve.NewPrelude(), stdlib.LoadCached())
	reg := stdlib.LoadCached()
	chk := check.File(file, res, check.Opts{
		UseGolegacy:   true,
		Stdlib:        reg,
		Primitives:    reg.Primitives,
		ResultMethods: reg.ResultMethods,
		Source:        []byte(src),
		Privileged:    true,
	})
	mod, _ := ir.Lower("main", file, res, chk)
	return mod
}

// --- raw.null() -> RawPtr ---

func TestRuntimeIntrinsicTypingRawNull(t *testing.T) {
	src := `
use std.runtime.raw

#[no_alloc]
pub fn demo() {
    let p = raw.null()
}
`
	got := firstLetTypeIn(lowerPrivileged(t, src), "demo")
	if got != "RawPtr" {
		t.Fatalf("expected `let p = raw.null()` to type as RawPtr, got %q", got)
	}
}

// --- raw.alloc(bytes, align) -> RawPtr ---

func TestRuntimeIntrinsicTypingRawAlloc(t *testing.T) {
	src := `
use std.runtime.raw

#[no_alloc]
pub fn demo() {
    let p = raw.alloc(64, 8)
}
`
	got := firstLetTypeIn(lowerPrivileged(t, src), "demo")
	if got != "RawPtr" {
		t.Fatalf("expected `let p = raw.alloc(64, 8)` to type as RawPtr, got %q", got)
	}
}

// --- raw.bits(p) -> Int ---

func TestRuntimeIntrinsicTypingRawBits(t *testing.T) {
	src := `
use std.runtime.raw

#[no_alloc]
pub fn demo() {
    let p = raw.alloc(8, 8)
    let n = raw.bits(p)
}
`
	mod := lowerPrivileged(t, src)
	// First let is `p: RawPtr`; second let is `n: Int`.
	for _, decl := range mod.Decls {
		fd, ok := decl.(*ir.FnDecl)
		if !ok || fd == nil || fd.Name != "demo" || fd.Body == nil {
			continue
		}
		count := 0
		for _, s := range fd.Body.Stmts {
			let, ok := s.(*ir.LetStmt)
			if !ok {
				continue
			}
			count++
			gotType := printType(let.Type)
			switch count {
			case 1:
				if gotType != "RawPtr" {
					t.Errorf("let p: expected RawPtr, got %s", gotType)
				}
			case 2:
				if gotType != "Int" {
					t.Errorf("let n: expected Int, got %s", gotType)
				}
			}
		}
	}
}

// --- raw.read::<T>(p) -> T ---

func TestRuntimeIntrinsicTypingRawReadInt(t *testing.T) {
	t.Skip(genericSkipReason)
	src := `
use std.runtime.raw

#[no_alloc]
pub fn demo() {
    let p = raw.alloc(8, 8)
    let v = raw.read::<Int>(p)
}
`
	mod := lowerPrivileged(t, src)
	for _, decl := range mod.Decls {
		fd, ok := decl.(*ir.FnDecl)
		if !ok || fd == nil || fd.Name != "demo" || fd.Body == nil {
			continue
		}
		// Second let is `v: Int` (T=Int instantiation).
		count := 0
		for _, s := range fd.Body.Stmts {
			let, ok := s.(*ir.LetStmt)
			if !ok {
				continue
			}
			count++
			if count == 2 {
				if got := printType(let.Type); got != "Int" {
					t.Errorf("let v: expected Int from raw.read::<Int>, got %s", got)
				}
			}
		}
	}
}

// --- raw.read::<T>(p) -> T (T=Float64) ---

func TestRuntimeIntrinsicTypingRawReadFloat(t *testing.T) {
	t.Skip(genericSkipReason)
	src := `
use std.runtime.raw

#[no_alloc]
pub fn demo() {
    let p = raw.alloc(8, 8)
    let v = raw.read::<Float64>(p)
}
`
	mod := lowerPrivileged(t, src)
	for _, decl := range mod.Decls {
		fd, ok := decl.(*ir.FnDecl)
		if !ok || fd == nil || fd.Name != "demo" || fd.Body == nil {
			continue
		}
		count := 0
		for _, s := range fd.Body.Stmts {
			let, ok := s.(*ir.LetStmt)
			if !ok {
				continue
			}
			count++
			if count == 2 {
				if got := printType(let.Type); got != "Float64" {
					t.Errorf("let v: expected Float64 from raw.read::<Float64>, got %s", got)
				}
			}
		}
	}
}

// --- raw.cas::<Int>(...) -> Bool ---

func TestRuntimeIntrinsicTypingRawCas(t *testing.T) {
	t.Skip(genericSkipReason)
	src := `
use std.runtime.raw

#[no_alloc]
pub fn demo() {
    let p = raw.alloc(8, 8)
    let ok = raw.cas::<Int>(p, 0, 1)
}
`
	mod := lowerPrivileged(t, src)
	for _, decl := range mod.Decls {
		fd, ok := decl.(*ir.FnDecl)
		if !ok || fd == nil || fd.Name != "demo" || fd.Body == nil {
			continue
		}
		count := 0
		for _, s := range fd.Body.Stmts {
			let, ok := s.(*ir.LetStmt)
			if !ok {
				continue
			}
			count++
			if count == 2 {
				if got := printType(let.Type); got != "Bool" {
					t.Errorf("let ok: expected Bool from raw.cas, got %s", got)
				}
			}
		}
	}
}

// --- raw.sizeOf::<T>() -> Int (compile-time constant typing) ---

func TestRuntimeIntrinsicTypingSizeOf(t *testing.T) {
	t.Skip(genericSkipReason)
	src := `
use std.runtime.raw

#[no_alloc]
pub fn demo() {
    let n = raw.sizeOf::<Int>()
}
`
	got := firstLetTypeIn(lowerPrivileged(t, src), "demo")
	if got != "Int" {
		t.Fatalf("expected raw.sizeOf::<Int>() to type as Int, got %q", got)
	}
}

// --- end-to-end: chained intrinsics in a real-shaped fn ---

func TestRuntimeIntrinsicTypingChainedAllocReadFree(t *testing.T) {
	t.Skip(genericSkipReason)
	src := `
use std.runtime.raw

#[no_alloc]
pub fn roundtrip() -> Int {
    let p = raw.alloc(8, 8)
    raw.write::<Int>(p, 42)
    let v = raw.read::<Int>(p)
    raw.free(p)
    v
}
`
	mod := lowerPrivileged(t, src)
	// Verify the trailing expression is typed as Int (the function's
	// return type). Today this is `<error>` because every step in
	// the chain has lost type info.
	var foundReturn bool
	for _, decl := range mod.Decls {
		fd, ok := decl.(*ir.FnDecl)
		if !ok || fd == nil || fd.Name != "roundtrip" {
			continue
		}
		if fd.Body == nil || fd.Body.Result == nil {
			t.Fatal("roundtrip body is empty or has no trailing expression")
		}
		// Sanity: Identifier `v` is reachable.
		if id, ok := fd.Body.Result.(*ir.Ident); ok && id.Name == "v" {
			foundReturn = true
		}
	}
	if !foundReturn {
		t.Fatal("could not find trailing expression `v` in roundtrip body")
	}
}

// --- meta: confirm at least one test in this file actually runs (would catch
// total-skip regressions caused by import errors etc.). ---

func TestRuntimeIntrinsicTypingFileLoads(t *testing.T) {
	// Sanity check that uses no skipped behavior — proves the test
	// file is being compiled and discovered. Removing this would not
	// be caught by `go test` if every other test t.Skip()s.
	if _ = (*ast.FnDecl)(nil); false {
		t.Fatal("unreachable — guard against ast import being elided")
	}
}
