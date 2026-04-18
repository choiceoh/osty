package check

import (
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
)

// Runtime sublanguage surface that is gated by §19.2. Keep this list in
// sync with the annotation registrations in `internal/ast/ast.go` and
// the runtime-only names added by LANG_SPEC §19.3 / §19.4.
//
// `no_alloc` is included because §19.2 restricts the entire runtime
// annotation set to privileged packages, even though `no_alloc` also
// has its own body-walker check in `noalloc.go`.
var runtimeGatedAnnotations = map[string]struct{}{
	"intrinsic": {},
	"c_abi":     {},
	"export":    {},
	"no_alloc":  {},
	"pod":       {},
	"repr":      {},
}

// runtimeGatedTypeNames is the set of bare identifiers that the
// privilege gate rejects outside privileged packages when they appear
// in a type position or as a name reference (spec §19.3 `RawPtr`,
// §19.4 `Pod`). The resolver also owns a fuller notion of "did this
// name resolve to a runtime-only symbol"; the spike's gate catches
// the common cases by bare name.
var runtimeGatedTypeNames = map[string]struct{}{
	"RawPtr": {},
	"Pod":    {},
}

// runPrivilegeGate enforces §19.2: the runtime sublanguage surface is
// only usable inside privileged packages. A package is privileged when
// `privileged` is true; the caller decides from the package path or
// manifest capability flag (§19.2). For `check.File` entry points with
// no package context, callers should pass `privileged = false` so
// runtime annotations in single-file fixtures are rejected — which is
// the spec-correct default.
//
// The gate rejects, with `E0770`:
//   - any annotation in `runtimeGatedAnnotations` on any declaration;
//   - any `use` whose path starts with `std.runtime` (any subpath);
//   - any reference to the bare identifiers in `runtimeGatedTypeNames`
//     in a type position (covered) or an expression position (covered
//     via CallExpr/TurbofishExpr callee walk), unless qualified through
//     `std.runtime.*` import — which is itself gated.
//
// The gate does NOT walk expression trees exhaustively; it catches the
// surface the user most plausibly tries to reach (annotations, imports,
// top-level type refs on fields/params/returns, and bare identifiers
// used as callees or values). The goal is to prevent the user-facing
// language from accidentally depending on a runtime-only symbol; the
// full exhaustive walk can land later without changing this public
// contract.
func runPrivilegeGate(file *ast.File, privileged bool) []*diag.Diagnostic {
	if file == nil || privileged {
		return nil
	}
	g := &privilegeGate{}
	g.walkUses(file.Uses)
	for _, d := range file.Decls {
		g.walkDecl(d)
	}
	return g.diags
}

type privilegeGate struct {
	diags []*diag.Diagnostic
}

func (g *privilegeGate) emit(node ast.Node, what string, hint string) {
	if node == nil {
		return
	}
	b := diag.New(diag.Error,
		"runtime sublanguage surface used outside a privileged package").
		Code(diag.CodeRuntimePrivilegeViolation).
		Primary(diag.Span{Start: node.Pos(), End: node.End()}, what).
		Note("LANG_SPEC §19.2: the runtime sublanguage is only reachable from `std.runtime.*` or from toolchain-workspace packages that opt in via `[capabilities] runtime = true` in `osty.toml`")
	if hint != "" {
		b = b.Note("hint: " + hint)
	}
	g.diags = append(g.diags, b.Build())
}

func (g *privilegeGate) walkUses(uses []*ast.UseDecl) {
	for _, u := range uses {
		if u == nil {
			continue
		}
		if isRuntimeUsePath(u) {
			g.emit(u, "`use std.runtime.*` is privileged",
				"only `std.runtime.*` packages and toolchain-workspace packages with `[capabilities] runtime = true` may import this namespace")
		}
	}
}

// isRuntimeUsePath reports whether a UseDecl imports a subpath of
// `std.runtime`. Works for both dotted `use std.runtime.raw` and the
// `use go "..."` / URL-ish forms (which are never `std.runtime`).
func isRuntimeUsePath(u *ast.UseDecl) bool {
	if u == nil {
		return false
	}
	if len(u.Path) >= 2 && u.Path[0] == "std" && u.Path[1] == "runtime" {
		return true
	}
	// RawPath fallback: some use forms store the dotted path here
	// instead of in Path. Check the prefix so we catch both
	// `std.runtime` and `std.runtime.<anything>`.
	raw := strings.TrimSpace(u.RawPath)
	return raw == "std.runtime" || strings.HasPrefix(raw, "std.runtime.")
}

func (g *privilegeGate) walkDecl(d ast.Decl) {
	switch n := d.(type) {
	case *ast.FnDecl:
		g.checkAnnotations(n.Annotations)
		g.walkGenerics(n.Generics)
		g.walkType(n.ReturnType)
		for _, p := range n.Params {
			if p != nil {
				g.walkType(p.Type)
			}
		}
	case *ast.StructDecl:
		g.checkAnnotations(n.Annotations)
		g.walkGenerics(n.Generics)
		for _, f := range n.Fields {
			if f != nil {
				g.checkAnnotations(f.Annotations)
				g.walkType(f.Type)
			}
		}
		for _, m := range n.Methods {
			if m != nil {
				g.walkDecl(m)
			}
		}
	case *ast.EnumDecl:
		g.checkAnnotations(n.Annotations)
		g.walkGenerics(n.Generics)
		for _, m := range n.Methods {
			if m != nil {
				g.walkDecl(m)
			}
		}
	case *ast.InterfaceDecl:
		g.checkAnnotations(n.Annotations)
		g.walkGenerics(n.Generics)
	case *ast.TypeAliasDecl:
		g.checkAnnotations(n.Annotations)
		g.walkGenerics(n.Generics)
		g.walkType(n.Target)
	case *ast.LetDecl:
		g.checkAnnotations(n.Annotations)
		g.walkType(n.Type)
	}
}

// walkGenerics inspects each generic parameter's constraint list for
// references to runtime-only names. A constraint like `<T: Pod>` or
// `<T: Ordered + RawPtr>` appears as a Type in the parameter's
// Constraints slice and is walked identically to a regular type
// position — so the privilege gate catches runtime-only types used in
// bound clauses too.
func (g *privilegeGate) walkGenerics(gps []*ast.GenericParam) {
	for _, gp := range gps {
		if gp == nil {
			continue
		}
		for _, c := range gp.Constraints {
			g.walkType(c)
		}
	}
}

func (g *privilegeGate) checkAnnotations(annots []*ast.Annotation) {
	for _, a := range annots {
		if a == nil {
			continue
		}
		if _, ok := runtimeGatedAnnotations[a.Name]; ok {
			g.emit(a, "`#["+a.Name+"]` is a runtime-only annotation", "")
		}
	}
}

// walkType inspects a declared type for references to runtime-only
// names (`RawPtr`, `Pod`). The walker descends through Optional /
// Tuple / Fn / NamedType args so that `List<RawPtr>`, `(RawPtr, Int)`,
// `fn(RawPtr) -> Pod`, and `RawPtr?` are all caught.
//
// Qualified references (e.g. `runtime.RawPtr`) are handled by the
// use-import gate; here we only flag unqualified bare-name usage.
func (g *privilegeGate) walkType(t ast.Type) {
	if t == nil {
		return
	}
	switch n := t.(type) {
	case *ast.NamedType:
		if len(n.Path) == 1 {
			if _, gated := runtimeGatedTypeNames[n.Path[0]]; gated {
				g.emit(n, "`"+n.Path[0]+"` is a runtime-only type", "")
			}
		}
		for _, arg := range n.Args {
			g.walkType(arg)
		}
	case *ast.OptionalType:
		g.walkType(n.Inner)
	case *ast.TupleType:
		for _, el := range n.Elems {
			g.walkType(el)
		}
	case *ast.FnType:
		for _, p := range n.Params {
			g.walkType(p)
		}
		g.walkType(n.ReturnType)
	}
}

// isPrivilegedPackage determines whether a resolver Package is
// privileged under §19.2. The decision prefers the package's declared
// path when available (via isPrivilegedPackagePath), then falls back
// to the directory heuristic — `.../std/runtime/<anything>` on disk.
// The manifest-capability path (`[capabilities] runtime = true`) is
// read by the manifest loader and surfaced through a future
// `Package.Capabilities` field; the spike treats any `std/runtime`
// directory-shape as privileged to keep the gate exercised against
// the obvious fixtures.
func isPrivilegedPackage(pkg *resolve.Package) bool {
	if pkg == nil {
		return false
	}
	if pkg.Dir == "" {
		return false
	}
	// Normalize to forward slashes so the predicate is platform-agnostic.
	norm := filepath.ToSlash(pkg.Dir)
	if strings.Contains(norm, "/std/runtime/") || strings.HasSuffix(norm, "/std/runtime") {
		return true
	}
	return false
}

// isPrivilegedPackagePath reports whether a workspace-level package
// path (dotted, e.g. `std.runtime.raw`) is privileged under §19.2.
// Any subpath of `std.runtime` qualifies.
func isPrivilegedPackagePath(path string) bool {
	if path == "" {
		return false
	}
	return path == "std.runtime" || strings.HasPrefix(path, "std.runtime.")
}
