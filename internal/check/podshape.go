package check

import (
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/diag"
)

// runPodShapeChecks enforces LANG_SPEC §19.4: a `struct` declaration
// marked `#[pod]` is plain-old-data when AND ONLY when:
//
//  1. it also carries `#[repr(c)]` (C ABI layout fixed),
//  2. every field's type is `Pod` per the §19.4 derivation, and
//  3. for generic structs, every type parameter carries a `T: Pod`
//     bound at the declaration site (unbounded `#[pod]` generics are
//     rejected outright; per-instantiation `Pod` is not v0.4 surface).
//
// The first violation per struct is reported with `E0771`; the
// diagnostic names the offending field or generic parameter so the
// user has a concrete fix.
//
// The check is local to one file in this spike: a `Pod` struct field
// type that names another `#[pod]` struct is only accepted when that
// struct is declared in the same file. Cross-file `#[pod]` resolution
// is a follow-up; for the runtime sublanguage's intended use (a small
// privileged package) the file-local rule is enough.
func runPodShapeChecks(file *ast.File) []*diag.Diagnostic {
	if file == nil {
		return nil
	}
	w := &podWalker{
		podStructs: collectPodStructNames(file),
	}
	for _, d := range file.Decls {
		s, ok := d.(*ast.StructDecl)
		if !ok {
			continue
		}
		if !hasAnnotation(s.Annotations, "pod") {
			continue
		}
		w.checkStruct(s)
	}
	return w.diags
}

// collectPodStructNames returns the set of locally-declared `#[pod]`
// struct names, used by the field-type Pod check to resolve named
// references.
func collectPodStructNames(file *ast.File) map[string]struct{} {
	out := map[string]struct{}{}
	for _, d := range file.Decls {
		if s, ok := d.(*ast.StructDecl); ok && hasAnnotation(s.Annotations, "pod") {
			out[s.Name] = struct{}{}
		}
	}
	return out
}

func hasAnnotation(annots []*ast.Annotation, name string) bool {
	for _, a := range annots {
		if a != nil && a.Name == name {
			return true
		}
	}
	return false
}

type podWalker struct {
	podStructs map[string]struct{}
	// podGenericParams is the set of generic parameter names on the
	// struct currently being checked that carry a `T: Pod` bound.
	// Reset per struct in checkStruct.
	podGenericParams map[string]struct{}
	diags            []*diag.Diagnostic
}

func (w *podWalker) emit(node ast.Node, msg string, notes ...string) {
	if node == nil {
		return
	}
	b := diag.New(diag.Error, msg).
		Code(diag.CodePodShapeViolation).
		Primary(diag.Span{Start: node.Pos(), End: node.End()}, "here").
		Note("LANG_SPEC §19.4: `#[pod]` requires C ABI layout, no managed references, and `T: Pod` bounds on every generic parameter")
	for _, n := range notes {
		if n != "" {
			b = b.Note(n)
		}
	}
	w.diags = append(w.diags, b.Build())
}

func (w *podWalker) checkStruct(s *ast.StructDecl) {
	// Rule 1: must also carry #[repr(c)].
	if !hasAnnotation(s.Annotations, "repr") {
		w.emit(s,
			fmt.Sprintf("`#[pod]` struct `%s` is missing `#[repr(c)]`", s.Name),
			"hint: add `#[repr(c)]` so the field layout is C ABI-stable")
		return
	}

	// Rule 3: generic params must each carry `T: Pod`. Build a name
	// set of Pod-bound parameters so the field-type walker treats
	// references to those names as Pod (rule from §19.4: with a Pod
	// bound, the parameter satisfies Pod unconditionally inside the
	// declaration body).
	w.podGenericParams = map[string]struct{}{}
	for _, gp := range s.Generics {
		if gp == nil {
			continue
		}
		if !genericParamHasPodBound(gp) {
			w.emit(gp,
				fmt.Sprintf("`#[pod]` struct `%s` has unbounded generic parameter `%s`", s.Name, gp.Name),
				"hint: add `: Pod` to the parameter — per-instantiation `Pod` is not supported in v0.4 (§19.4)")
			continue
		}
		w.podGenericParams[gp.Name] = struct{}{}
	}

	// Rule 2: every field's type is Pod.
	for _, f := range s.Fields {
		if f == nil {
			continue
		}
		if !w.isPodType(f.Type) {
			w.emit(f,
				fmt.Sprintf("field `%s.%s` has non-Pod type `%s`", s.Name, f.Name, formatType(f.Type)),
				"hint: replace with a primitive, `RawPtr`, `Option<T: Pod>`, a tuple of Pod, or another `#[pod] #[repr(c)]` struct")
		}
	}
}

// genericParamHasPodBound reports whether a generic parameter carries
// `Pod` in its constraint list. v0.4 represents bounds as a slice of
// constraint Type names; we look for an unqualified `Pod` entry.
func genericParamHasPodBound(gp *ast.GenericParam) bool {
	for _, c := range gp.Constraints {
		if isNamedType(c, "Pod") {
			return true
		}
	}
	return false
}

func isNamedType(t ast.Type, name string) bool {
	if n, ok := t.(*ast.NamedType); ok {
		return len(n.Path) == 1 && n.Path[0] == name
	}
	return false
}

// podPrimitives is the set of LANG_SPEC §2.1 primitive type names that
// satisfy `Pod` unconditionally per §19.4 rule 1, plus the runtime
// `RawPtr`. Generic-parameter references are NOT in this set; they
// satisfy `Pod` only when the parameter has a `T: Pod` bound, which
// is checked separately in `isPodType`.
var podPrimitives = map[string]struct{}{
	"Bool":    {},
	"Char":    {},
	"Byte":    {},
	"Int":     {},
	"Int8":    {},
	"Int16":   {},
	"Int32":   {},
	"Int64":   {},
	"UInt8":   {},
	"UInt16":  {},
	"UInt32":  {},
	"UInt64":  {},
	"Float":   {},
	"Float32": {},
	"Float64": {},
	"RawPtr":  {},
}

// isPodType walks a field's declared type and reports whether it
// satisfies the §19.4 derivation. The walk handles:
//   - primitives + RawPtr (rule 1)
//   - tuple of Pod (rule 4)
//   - Option<T: Pod> (rule 5) — both `Option<T>` and the `T?` sugar
//   - locally-declared `#[pod]` struct (rule 2 closure for self-refs)
//
// Anything else is non-Pod. The spike does NOT chase named references
// across files; that requires resolver-owned symbol resolution.
func (w *podWalker) isPodType(t ast.Type) bool {
	if t == nil {
		return false
	}
	switch n := t.(type) {
	case *ast.NamedType:
		if len(n.Path) == 1 {
			name := n.Path[0]
			if _, ok := podPrimitives[name]; ok {
				return len(n.Args) == 0
			}
			if _, ok := w.podGenericParams[name]; ok {
				// Generic parameter with a `T: Pod` bound on the
				// enclosing struct (§19.4 rule 3). Treated as Pod
				// inside the struct body.
				return len(n.Args) == 0
			}
			if _, ok := w.podStructs[name]; ok {
				// Local #[pod] struct. The spike accepts the reference
				// without recursively re-validating the referenced struct's
				// own #[pod] check — that struct's own check produces its
				// own diagnostics if any.
				return true
			}
			if name == "Option" && len(n.Args) == 1 {
				return w.isPodType(n.Args[0])
			}
		}
		return false
	case *ast.OptionalType:
		// `T?` sugar for `Option<T>`.
		return w.isPodType(n.Inner)
	case *ast.TupleType:
		for _, el := range n.Elems {
			if !w.isPodType(el) {
				return false
			}
		}
		return true
	}
	return false
}

// formatType produces a short human-readable type name for diagnostics.
// The full pretty-printer lives elsewhere in the codebase; for the
// spike we synthesize just enough of the surface to keep messages
// readable.
func formatType(t ast.Type) string {
	if t == nil {
		return "?"
	}
	switch n := t.(type) {
	case *ast.NamedType:
		if len(n.Path) == 0 {
			return "?"
		}
		base := n.Path[0]
		for _, p := range n.Path[1:] {
			base += "." + p
		}
		if len(n.Args) == 0 {
			return base
		}
		out := base + "<"
		for i, a := range n.Args {
			if i > 0 {
				out += ", "
			}
			out += formatType(a)
		}
		return out + ">"
	case *ast.OptionalType:
		return formatType(n.Inner) + "?"
	case *ast.TupleType:
		out := "("
		for i, el := range n.Elems {
			if i > 0 {
				out += ", "
			}
			out += formatType(el)
		}
		return out + ")"
	case *ast.FnType:
		out := "fn("
		for i, p := range n.Params {
			if i > 0 {
				out += ", "
			}
			out += formatType(p)
		}
		out += ")"
		if n.ReturnType != nil {
			out += " -> " + formatType(n.ReturnType)
		}
		return out
	}
	return "?"
}
