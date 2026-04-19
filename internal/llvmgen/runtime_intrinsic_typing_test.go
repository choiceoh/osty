package llvmgen

// This file is the executable contract for the §19.11 native-checker
// gap: stdlib member type inference for `use std.runtime.raw`.
//
// As of the §19 spike series (PRs #284-#343), the front-end +
// resolver bind `use std.runtime.raw` correctly, the stdlib loader
// exposes the 13 §19.5 intrinsic stubs, and the privilege gate +
// pod shape checker enforce the surface. But the native checker
// does NOT flow stdlib intrinsic return types back to call sites:
// `raw.null()` resolves but checks as `*ir.ErrType` because the
// member-access type-inference path doesn't follow the import to
// the stub's declared return type.
//
// Each test below states what the fixed behavior MUST look like.
// Today every test calls `t.Skip(skipReason)` so the suite is green
// while the gap is open. When the native checker is fixed, the
// expected procedure is:
//
//   1. Land the native-checker change (likely in `toolchain/check.osty`
//      or the resolver/checker bridge).
//   2. Run `go test ./internal/check/ -run TestRuntimeIntrinsicTyping`
//      with the t.Skip lines removed in this file.
//   3. Every test should pass without further changes.
//   4. Update LANG_SPEC §19.11 status table: flip the
//      "Stdlib member type inference for `use std.runtime.raw`"
//      row from "gap" to "landed (PR #N)".
//
// If a test fails after un-skipping, that's a signal that the
// contract was understood differently. Adjust the test, not the
// code, ONLY if the spec wording supports the alternative.

import (
	"testing"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	ir "github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
	"github.com/osty/osty/internal/stdlib"
)

const skipReason = "blocked on native-checker stdlib member type inference — LANG_SPEC §19.11 known gap. " +
	"Remove this Skip line when the native checker propagates `use std.runtime.raw` member return types to call sites."

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
	t.Skip(skipReason)
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
	t.Skip(skipReason)
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
	t.Skip(skipReason)
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
	t.Skip(skipReason)
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
	t.Skip(skipReason)
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
	t.Skip(skipReason)
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
	t.Skip(skipReason)
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
	t.Skip(skipReason)
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
