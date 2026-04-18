package check

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
)

// runNoAllocChecks walks every top-level `fn` (and method) in `file` that
// carries `#[no_alloc]` and rejects expressions that require the managed
// allocator. The check is local to one file in this spike — the
// transitive call-graph rule from §19.6.1 is implemented as "the callee
// must also be `#[no_alloc]` and live in the same file or in
// `std.runtime.*`". Cross-package fixed-point analysis is a follow-up.
//
// Spec: LANG_SPEC_v0.4/19-runtime-primitives.md §19.6.1
// Diagnostic: diag.CodeNoAllocViolation (E0772)
//
// The privilege gate from §19.2 (E0770) is not enforced here; that
// arrives in a separate phase together with the manifest [capabilities]
// schema. This walker fires regardless of package path so the discipline
// can be exercised against ordinary test files.
func runNoAllocChecks(file *ast.File, _ *resolve.Result) []*diag.Diagnostic {
	if file == nil {
		return nil
	}
	noAllocFns := collectNoAllocFns(file)
	if len(noAllocFns) == 0 {
		return nil
	}
	w := &noAllocWalker{noAllocFns: noAllocFns}
	for _, fn := range noAllocFns {
		w.activeFn = fn
		w.walkBlock(fn.Body)
	}
	return w.diags
}

// collectNoAllocFns scans top-level declarations and struct/enum methods
// for `#[no_alloc]`. Returns a name-set for the body walker's call check.
func collectNoAllocFns(file *ast.File) map[string]*ast.FnDecl {
	out := map[string]*ast.FnDecl{}
	for _, d := range file.Decls {
		switch n := d.(type) {
		case *ast.FnDecl:
			if hasNoAlloc(n.Annotations) && n.Body != nil {
				out[n.Name] = n
			}
		case *ast.StructDecl:
			for _, m := range n.Methods {
				if m != nil && hasNoAlloc(m.Annotations) && m.Body != nil {
					out[m.Name] = m
				}
			}
		case *ast.EnumDecl:
			for _, m := range n.Methods {
				if m != nil && hasNoAlloc(m.Annotations) && m.Body != nil {
					out[m.Name] = m
				}
			}
		}
	}
	return out
}

func hasNoAlloc(annots []*ast.Annotation) bool {
	for _, a := range annots {
		if a != nil && a.Name == "no_alloc" {
			return true
		}
	}
	return false
}

type noAllocWalker struct {
	noAllocFns map[string]*ast.FnDecl
	activeFn   *ast.FnDecl
	diags      []*diag.Diagnostic
}

func (w *noAllocWalker) emit(node ast.Node, msg string, notes ...string) {
	if node == nil {
		return
	}
	b := diag.New(diag.Error, fmt.Sprintf(
		"`#[no_alloc]` function `%s` cannot %s", w.activeFn.Name, msg)).
		Code(diag.CodeNoAllocViolation).
		Primary(diag.Span{Start: node.Pos(), End: node.End()},
			"managed allocation here").
		Note("LANG_SPEC §19.6.1: a `#[no_alloc]` body must not reach the managed allocator")
	for _, n := range notes {
		if n != "" {
			b = b.Note(n)
		}
	}
	w.diags = append(w.diags, b.Build())
}

func (w *noAllocWalker) walkBlock(b *ast.Block) {
	if b == nil {
		return
	}
	for _, s := range b.Stmts {
		w.walkStmt(s)
	}
}

func (w *noAllocWalker) walkStmt(s ast.Stmt) {
	switch n := s.(type) {
	case nil:
		return
	case *ast.LetStmt:
		w.walkExpr(n.Value)
	case *ast.ExprStmt:
		w.walkExpr(n.X)
	case *ast.AssignStmt:
		for _, t := range n.Targets {
			w.walkExpr(t)
		}
		w.walkExpr(n.Value)
	case *ast.ReturnStmt:
		w.walkExpr(n.Value)
	case *ast.BreakStmt, *ast.ContinueStmt:
		return
	case *ast.ChanSendStmt:
		w.walkExpr(n.Channel)
		w.walkExpr(n.Value)
	case *ast.DeferStmt:
		w.walkExpr(n.X)
	case *ast.ForStmt:
		w.walkExpr(n.Iter)
		w.walkBlock(n.Body)
	case *ast.Block:
		w.walkBlock(n)
	}
}

// walkExpr is the heart of the check. Every expression that produces a
// fresh managed allocation is reported. Sub-expressions are still
// walked so the diagnostic can name the deepest cause.
func (w *noAllocWalker) walkExpr(e ast.Expr) {
	switch n := e.(type) {
	case nil:
		return

	// Allocating constructs — reject directly.

	case *ast.ListExpr:
		w.emit(n, "construct a list literal",
			"hint: pre-allocate the backing storage with `raw.alloc` and write through `raw.write`")
		for _, el := range n.Elems {
			w.walkExpr(el)
		}
	case *ast.MapExpr:
		w.emit(n, "construct a map literal",
			"hint: maps require a managed allocator; runtime code uses `raw.alloc`-backed open-addressing tables")
		for _, ent := range n.Entries {
			if ent != nil {
				w.walkExpr(ent.Key)
				w.walkExpr(ent.Value)
			}
		}
	case *ast.StringLit:
		if isAllocatingString(n) {
			what := "use a string literal that allocates at runtime"
			switch {
			case hasInterpolation(n):
				what = "use string interpolation"
			case n.IsTriple:
				what = "use a triple-quoted string"
			case n.IsRaw:
				what = "use a raw string literal"
			}
			w.emit(n, what,
				"hint: only plain `\"...\"` literals (no interpolation, no triple-quoting, no raw form) are statically interned and allocation-free")
			for _, p := range n.Parts {
				if !p.IsLit {
					w.walkExpr(p.Expr)
				}
			}
		}
	case *ast.StructLit:
		// Spike conservatism: any struct literal is rejected. The Pod
		// carve-out from §19.4 requires resolver/checker integration
		// that is not part of this spike.
		w.emit(n, "construct a struct literal",
			"hint: until the `#[pod]` checker lands, runtime code uses `raw.alloc` + field offsets instead of struct literals")
		for _, f := range n.Fields {
			if f != nil {
				w.walkExpr(f.Value)
			}
		}
		w.walkExpr(n.Spread)

	// Calls — only allowed if the callee is itself `#[no_alloc]` in this file.

	case *ast.CallExpr:
		if !w.calleeIsNoAlloc(n.Fn) {
			w.emit(n, "call into a function that is not `#[no_alloc]`",
				"hint: mark the callee `#[no_alloc]` (if it is in fact allocation-free), or restructure to avoid the call")
		}
		w.walkExpr(n.Fn)
		for _, a := range n.Args {
			if a != nil {
				w.walkExpr(a.Value)
			}
		}

	// Composite expressions — recurse.

	case *ast.UnaryExpr:
		w.walkExpr(n.X)
	case *ast.BinaryExpr:
		w.walkExpr(n.Left)
		w.walkExpr(n.Right)
	case *ast.QuestionExpr:
		w.walkExpr(n.X)
	case *ast.FieldExpr:
		w.walkExpr(n.X)
	case *ast.IndexExpr:
		w.walkExpr(n.X)
		w.walkExpr(n.Index)
	case *ast.TurbofishExpr:
		w.walkExpr(n.Base)
	case *ast.RangeExpr:
		w.walkExpr(n.Start)
		w.walkExpr(n.Stop)
	case *ast.ParenExpr:
		w.walkExpr(n.X)
	case *ast.TupleExpr:
		for _, el := range n.Elems {
			w.walkExpr(el)
		}
	case *ast.IfExpr:
		w.walkExpr(n.Cond)
		w.walkBlock(n.Then)
		w.walkExpr(n.Else)
	case *ast.MatchExpr:
		w.walkExpr(n.Scrutinee)
		for _, arm := range n.Arms {
			if arm == nil {
				continue
			}
			w.walkExpr(arm.Guard)
			w.walkExpr(arm.Body)
		}
	case *ast.ClosureExpr:
		// Closures inside a #[no_alloc] body are themselves managed
		// allocations (they capture an environment). Reject the
		// closure expression and skip its body — the body would be
		// analyzed in its own callable context if it ever ran.
		w.emit(n, "construct a closure",
			"hint: closures carry a captured environment and require managed allocation")
	case *ast.Block:
		w.walkBlock(n)
	}
}

// calleeIsNoAlloc returns true when the Fn expression of a CallExpr
// resolves to one of the file's `#[no_alloc]` declarations OR when the
// call targets a function in the privileged runtime intrinsic
// namespace (`raw.*` after `use std.runtime.raw`). The spike does not
// run cross-package fixed-point analysis; ordinary stdlib calls are
// always rejected.
func (w *noAllocWalker) calleeIsNoAlloc(fn ast.Expr) bool {
	switch e := fn.(type) {
	case *ast.Ident:
		_, ok := w.noAllocFns[e.Name]
		return ok
	case *ast.FieldExpr:
		// `raw.alloc`, `raw.read`, etc. — accept any method on a
		// receiver named `raw` for the spike. Once the resolver knows
		// about std.runtime.raw, this widens to "the receiver
		// resolves to a privileged runtime package".
		if recv, ok := e.X.(*ast.Ident); ok && recv.Name == "raw" {
			return true
		}
		// `self.foo()` calling another `#[no_alloc]` method on the
		// same receiver type is allowed when the method name is in
		// the file-scoped no_alloc set. The spike does not check the
		// receiver type; it accepts the name match.
		if recv, ok := e.X.(*ast.Ident); ok && recv.Name == "self" {
			_, found := w.noAllocFns[e.Name]
			return found
		}
		return false
	case *ast.TurbofishExpr:
		return w.calleeIsNoAlloc(e.Base)
	}
	return false
}

func hasInterpolation(s *ast.StringLit) bool {
	for _, p := range s.Parts {
		if !p.IsLit {
			return true
		}
	}
	return false
}

func isAllocatingString(s *ast.StringLit) bool {
	if s == nil {
		return false
	}
	if hasInterpolation(s) {
		return true
	}
	if s.IsRaw || s.IsTriple {
		return true
	}
	return false
}
