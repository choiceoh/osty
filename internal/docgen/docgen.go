// Package docgen extracts API documentation from parsed Osty source
// files and renders it to human-readable formats (currently markdown).
//
// The generator walks every `pub` top-level declaration — functions,
// structs, enums, interfaces, type aliases, and pub-let bindings —
// pairs each with the `///` doc comment the parser already attached
// (see internal/lexer + internal/parser), and emits one markdown
// document per Osty package.
//
// Private declarations, anonymous blocks, and `use` imports are
// intentionally excluded: the doc output describes a package's external
// contract, which is exactly the "public API surface" the L0070 lint
// rule already asks authors to document.
//
// Typical usage (from the `osty doc` subcommand):
//
//	pkg := docgen.FromPackage(resolvedPkg)
//	md := docgen.RenderMarkdown(pkg)
//
// Single-file input is supported via FromFile; callers pass any label
// they like as the "package" name.
package docgen

import (
	"fmt"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/resolve"
)

// Package is the root of one rendered document: one Osty package
// (or one standalone file, which is treated as a one-file package).
type Package struct {
	// Name is the header title — typically the package directory's
	// basename or the file's stem for single-file mode.
	Name string
	// Dir is the package directory (empty for single-file mode). Used
	// by the renderer as a subtitle.
	Dir string
	// Modules is one entry per source file in the package, in
	// lexicographic path order (matching resolve.Package.Files).
	Modules []*Module
}

// Module is one .osty file's documented declarations.
type Module struct {
	// Path is the file's filesystem path as passed to the parser.
	Path string
	// File is the parsed AST, retained so custom renderers can reach
	// back for extra fields (positions, raw types) without re-parsing.
	File *ast.File
	// FileDoc is the leading `///` comment block attached to the first
	// declaration's top-of-file position. Osty does not yet model a
	// dedicated module doc — this mirrors the convention used in
	// stdlib stubs where a file-level description precedes the first
	// decl. May be empty.
	FileDoc string
	// Decls holds every pub declaration in source order. Private
	// declarations are filtered out before renderer sees them.
	Decls []*Decl
}

// DeclKind tags the AST category of a documented declaration so
// renderers can group or style them without a Go type switch.
type DeclKind int

const (
	KindFunction  DeclKind = iota + 1 // `fn`
	KindStruct                        // `struct`
	KindEnum                          // `enum`
	KindInterface                     // `interface`
	KindTypeAlias                     // `type`
	KindConstant                      // `pub let`
)

// String returns the word used in rendered headings and anchors. Kept
// in sync with the user-facing vocabulary of the language spec.
func (k DeclKind) String() string {
	switch k {
	case KindFunction:
		return "function"
	case KindStruct:
		return "struct"
	case KindEnum:
		return "enum"
	case KindInterface:
		return "interface"
	case KindTypeAlias:
		return "type"
	case KindConstant:
		return "constant"
	}
	return "decl"
}

// Decl is one documented declaration. The struct is intentionally
// renderer-agnostic: it holds formatted fragments (Signature, type
// strings) alongside raw metadata (Line, Doc, Deprecated) so both
// markdown and HTML renderers can compose the exact layout they want.
type Decl struct {
	Kind       DeclKind
	Name       string
	Doc        string  // raw `///` block, newline-joined, as the parser saw it
	Info       DocInfo // structured parse of Doc (Summary, Params, Returns, Example, See)
	Line       int     // 1-based source line of the declaration keyword
	Signature  string  // one-line signature (e.g. "fn new(name: String) -> Self")
	Deprecated string  // `#[deprecated(message = ...)]` message, empty if absent
	DeprecatedSince string // `#[deprecated(since = "0.5")]` version, empty if absent
	DeprecatedUse   string // `#[deprecated(use = "newFn")]` replacement hint, empty if absent

	// Fields populated for KindStruct.
	Fields []*Field
	// Variants populated for KindEnum.
	Variants []*Variant
	// Methods populated for KindStruct, KindEnum, KindInterface.
	// Private methods are filtered out to match the exported-surface
	// contract of the generator.
	Methods []*Decl

	// For KindTypeAlias — the rendered right-hand side.
	AliasTarget string
	// For KindConstant — the rendered type annotation (if declared).
	ConstType string
}

// Field is a documented struct field (pub only).
type Field struct {
	Name string
	Type string // rendered
	Doc  string // leading /// comment on the field (none today; reserved)
}

// Variant is a documented enum variant (variants are always part of
// the enum's pub API when the enum itself is pub).
type Variant struct {
	Name    string
	Payload []string // rendered payload type strings; empty for bare variants
	Doc     string
}

// FromPackage extracts documentation for every pub declaration across
// every file in pkg. File order is pkg.Files's lexicographic order.
func FromPackage(pkg *resolve.Package) *Package {
	out := &Package{
		Name: pkg.Name,
		Dir:  pkg.Dir,
	}
	for _, f := range pkg.Files {
		out.Modules = append(out.Modules, fromFile(f.Path, f.File))
	}
	return out
}

// FromFile extracts documentation from a single parsed file. label is
// used as both the Package.Name and the single Module.Path header;
// pass an empty string to derive it from the file's contents.
func FromFile(label string, file *ast.File) *Package {
	m := fromFile(label, file)
	return &Package{
		Name:    label,
		Modules: []*Module{m},
	}
}

// fromFile walks one AST File and pulls out its pub declarations.
func fromFile(path string, file *ast.File) *Module {
	m := &Module{Path: path, File: file}
	if file == nil {
		return m
	}
	for _, d := range file.Decls {
		if doc := fromDecl(d); doc != nil {
			m.Decls = append(m.Decls, doc)
		}
	}
	// Stable source-order output: decls were appended in source order,
	// but stabilize by position in case parser recovery ever reorders.
	sort.SliceStable(m.Decls, func(i, j int) bool {
		return m.Decls[i].Line < m.Decls[j].Line
	})
	return m
}

// fillDeprecated populates the three deprecation fields from an
// annotation list in one pass so each call site stays readable.
func fillDeprecated(dst *Decl, annots []*ast.Annotation) {
	dst.Deprecated = deprecatedMessage(annots)
	dst.DeprecatedSince = deprecatedArg(annots, "since")
	dst.DeprecatedUse = deprecatedArg(annots, "use")
}

// fromDecl converts one AST declaration. Returns nil for non-pub
// declarations and for declaration kinds that don't participate in
// the API surface (UseDecl).
func fromDecl(d ast.Decl) *Decl {
	switch n := d.(type) {
	case *ast.FnDecl:
		if !n.Pub {
			return nil
		}
		dc := &Decl{
			Kind:      KindFunction,
			Name:      n.Name,
			Doc:       n.DocComment,
			Info:      parseDocComment(n.DocComment),
			Line:      n.PosV.Line,
			Signature: RenderFnSignature(n),
		}
		fillDeprecated(dc, n.Annotations)
		return dc
	case *ast.StructDecl:
		if !n.Pub {
			return nil
		}
		dc := &Decl{
			Kind:      KindStruct,
			Name:      n.Name,
			Doc:       n.DocComment,
			Info:      parseDocComment(n.DocComment),
			Line:      n.PosV.Line,
			Signature: renderStructHeader(n),
		}
		fillDeprecated(dc, n.Annotations)
		for _, f := range n.Fields {
			if !f.Pub {
				continue
			}
			dc.Fields = append(dc.Fields, &Field{
				Name: f.Name,
				Type: RenderType(f.Type),
			})
		}
		for _, m := range n.Methods {
			if !m.Pub {
				continue
			}
			md := &Decl{
				Kind:      KindFunction,
				Name:      m.Name,
				Doc:       m.DocComment,
				Info:      parseDocComment(m.DocComment),
				Line:      m.PosV.Line,
				Signature: RenderFnSignature(m),
			}
			fillDeprecated(md, m.Annotations)
			dc.Methods = append(dc.Methods, md)
		}
		return dc
	case *ast.EnumDecl:
		if !n.Pub {
			return nil
		}
		dc := &Decl{
			Kind:      KindEnum,
			Name:      n.Name,
			Doc:       n.DocComment,
			Info:      parseDocComment(n.DocComment),
			Line:      n.PosV.Line,
			Signature: renderEnumHeader(n),
		}
		fillDeprecated(dc, n.Annotations)
		for _, v := range n.Variants {
			payload := make([]string, 0, len(v.Fields))
			for _, t := range v.Fields {
				payload = append(payload, RenderType(t))
			}
			dc.Variants = append(dc.Variants, &Variant{
				Name:    v.Name,
				Payload: payload,
				Doc:     v.DocComment,
			})
		}
		for _, m := range n.Methods {
			if !m.Pub {
				continue
			}
			md := &Decl{
				Kind:      KindFunction,
				Name:      m.Name,
				Doc:       m.DocComment,
				Info:      parseDocComment(m.DocComment),
				Line:      m.PosV.Line,
				Signature: RenderFnSignature(m),
			}
			fillDeprecated(md, m.Annotations)
			dc.Methods = append(dc.Methods, md)
		}
		return dc
	case *ast.InterfaceDecl:
		if !n.Pub {
			return nil
		}
		dc := &Decl{
			Kind:      KindInterface,
			Name:      n.Name,
			Doc:       n.DocComment,
			Info:      parseDocComment(n.DocComment),
			Line:      n.PosV.Line,
			Signature: renderInterfaceHeader(n),
		}
		fillDeprecated(dc, n.Annotations)
		// Interface methods are all part of the contract regardless of
		// pub — the interface itself being pub exposes them.
		for _, m := range n.Methods {
			dc.Methods = append(dc.Methods, &Decl{
				Kind:      KindFunction,
				Name:      m.Name,
				Doc:       m.DocComment,
				Info:      parseDocComment(m.DocComment),
				Line:      m.PosV.Line,
				Signature: RenderFnSignature(m),
			})
		}
		return dc
	case *ast.TypeAliasDecl:
		if !n.Pub {
			return nil
		}
		dc := &Decl{
			Kind:        KindTypeAlias,
			Name:        n.Name,
			Doc:         n.DocComment,
			Info:        parseDocComment(n.DocComment),
			Line:        n.PosV.Line,
			Signature:   renderTypeAliasHeader(n),
			AliasTarget: RenderType(n.Target),
		}
		fillDeprecated(dc, n.Annotations)
		return dc
	case *ast.LetDecl:
		if !n.Pub {
			return nil
		}
		dc := &Decl{
			Kind:      KindConstant,
			Name:      n.Name,
			Doc:       n.DocComment,
			Info:      parseDocComment(n.DocComment),
			Line:      n.PosV.Line,
			Signature: renderLetHeader(n),
		}
		fillDeprecated(dc, n.Annotations)
		if n.Type != nil {
			dc.ConstType = RenderType(n.Type)
		}
		return dc
	}
	return nil
}

// deprecatedMessage returns the user-facing message for a `#[deprecated(...)]`
// annotation, or "" if the decl isn't deprecated. The annotation's
// `message = "..."` argument is preferred; we fall back to the bare
// marker form which just surfaces "deprecated".
func deprecatedMessage(annots []*ast.Annotation) string {
	for _, a := range annots {
		if a.Name != "deprecated" {
			continue
		}
		for _, arg := range a.Args {
			if arg.Key == "message" {
				if s, ok := stringLiteral(arg.Value); ok {
					return s
				}
			}
		}
		return "deprecated"
	}
	return ""
}

// deprecatedArg returns the value of one string-typed arg on the
// `#[deprecated(...)]` annotation. Empty string when the annotation or
// the requested key is absent. Keeps the `since` / `use` lookups
// decoupled so renderers can present them independently.
func deprecatedArg(annots []*ast.Annotation, key string) string {
	for _, a := range annots {
		if a.Name != "deprecated" {
			continue
		}
		for _, arg := range a.Args {
			if arg.Key == key {
				if s, ok := stringLiteral(arg.Value); ok {
					return s
				}
			}
		}
	}
	return ""
}

// stringLiteral unwraps a StringLit node to its literal text content,
// concatenating the literal segments and skipping interpolated parts.
// Returns ("", false) for any other expression kind. Used to read
// `#[deprecated(message = "...")]` payloads.
func stringLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.StringLit)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, p := range lit.Parts {
		if p.IsLit {
			b.WriteString(p.Lit)
		}
	}
	return b.String(), true
}

// ---- signature rendering ----

// RenderFnSignature returns a one-line signature for fn n:
//
//	fn name<T: Ord>(self, x: Int, y: Int = 0) -> Result<Int, Error>
//
// The receiver (for methods) is rendered as the first parameter and
// generic parameters as `<T, U: Ord>`.
func RenderFnSignature(n *ast.FnDecl) string {
	var b strings.Builder
	if n.Pub {
		b.WriteString("pub ")
	}
	b.WriteString("fn ")
	b.WriteString(n.Name)
	if len(n.Generics) > 0 {
		b.WriteString(renderGenerics(n.Generics))
	}
	b.WriteByte('(')
	first := true
	if n.Recv != nil {
		if n.Recv.Mut {
			b.WriteString("mut self")
		} else {
			b.WriteString("self")
		}
		first = false
	}
	for _, p := range n.Params {
		if !first {
			b.WriteString(", ")
		}
		first = false
		if p.Name != "" {
			b.WriteString(p.Name)
			b.WriteString(": ")
		}
		if p.Type != nil {
			b.WriteString(RenderType(p.Type))
		}
		if p.Default != nil {
			b.WriteString(" = ")
			b.WriteString(renderLiteralExpr(p.Default))
		}
	}
	b.WriteByte(')')
	if n.ReturnType != nil {
		b.WriteString(" -> ")
		b.WriteString(RenderType(n.ReturnType))
	}
	return b.String()
}

// renderStructHeader returns the struct's declaration line without
// its body — useful as a code-block heading.
//
//	pub struct User<T: Hashable>
func renderStructHeader(n *ast.StructDecl) string {
	var b strings.Builder
	if n.Pub {
		b.WriteString("pub ")
	}
	b.WriteString("struct ")
	b.WriteString(n.Name)
	if len(n.Generics) > 0 {
		b.WriteString(renderGenerics(n.Generics))
	}
	return b.String()
}

// renderEnumHeader returns `pub enum Name<T>` — no body.
func renderEnumHeader(n *ast.EnumDecl) string {
	var b strings.Builder
	if n.Pub {
		b.WriteString("pub ")
	}
	b.WriteString("enum ")
	b.WriteString(n.Name)
	if len(n.Generics) > 0 {
		b.WriteString(renderGenerics(n.Generics))
	}
	return b.String()
}

// renderInterfaceHeader returns `pub interface Name<T>: A + B`.
func renderInterfaceHeader(n *ast.InterfaceDecl) string {
	var b strings.Builder
	if n.Pub {
		b.WriteString("pub ")
	}
	b.WriteString("interface ")
	b.WriteString(n.Name)
	if len(n.Generics) > 0 {
		b.WriteString(renderGenerics(n.Generics))
	}
	if len(n.Extends) > 0 {
		b.WriteString(": ")
		for i, e := range n.Extends {
			if i > 0 {
				b.WriteString(" + ")
			}
			b.WriteString(RenderType(e))
		}
	}
	return b.String()
}

// renderTypeAliasHeader returns `pub type Name<T> = Target`.
func renderTypeAliasHeader(n *ast.TypeAliasDecl) string {
	var b strings.Builder
	if n.Pub {
		b.WriteString("pub ")
	}
	b.WriteString("type ")
	b.WriteString(n.Name)
	if len(n.Generics) > 0 {
		b.WriteString(renderGenerics(n.Generics))
	}
	b.WriteString(" = ")
	b.WriteString(RenderType(n.Target))
	return b.String()
}

// renderLetHeader returns `pub let NAME: T = <expr>` or a simpler form
// when the type or value is absent. Rendering the RHS uses the same
// literal renderer as fn defaults — top-level pub lets are restricted
// to literal initializers.
func renderLetHeader(n *ast.LetDecl) string {
	var b strings.Builder
	b.WriteString("pub let ")
	if n.Mut {
		b.WriteString("mut ")
	}
	b.WriteString(n.Name)
	if n.Type != nil {
		b.WriteString(": ")
		b.WriteString(RenderType(n.Type))
	}
	if n.Value != nil {
		b.WriteString(" = ")
		b.WriteString(renderLiteralExpr(n.Value))
	}
	return b.String()
}

// renderGenerics prints `<T, U: Ord + Hashable>`.
func renderGenerics(gs []*ast.GenericParam) string {
	var b strings.Builder
	b.WriteByte('<')
	for i, g := range gs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(g.Name)
		if len(g.Constraints) > 0 {
			b.WriteString(": ")
			for j, c := range g.Constraints {
				if j > 0 {
					b.WriteString(" + ")
				}
				b.WriteString(RenderType(c))
			}
		}
	}
	b.WriteByte('>')
	return b.String()
}

// RenderType converts an AST type into its source-like string form.
// Mirrors internal/format/printer.go's printType but returns a string
// so the caller doesn't need a printer context. Intended for doc-only
// use — not canonical formatting.
func RenderType(t ast.Type) string {
	if t == nil {
		return ""
	}
	switch n := t.(type) {
	case *ast.NamedType:
		// Match the formatter's `Option<T>` -> `T?` rewrite so docs
		// read the way users wrote them.
		if len(n.Path) == 1 && n.Path[0] == "Option" && len(n.Args) == 1 {
			return RenderType(n.Args[0]) + "?"
		}
		base := strings.Join(n.Path, ".")
		if len(n.Args) == 0 {
			return base
		}
		parts := make([]string, 0, len(n.Args))
		for _, a := range n.Args {
			parts = append(parts, RenderType(a))
		}
		return base + "<" + strings.Join(parts, ", ") + ">"
	case *ast.OptionalType:
		return RenderType(n.Inner) + "?"
	case *ast.TupleType:
		parts := make([]string, 0, len(n.Elems))
		for _, e := range n.Elems {
			parts = append(parts, RenderType(e))
		}
		if len(parts) == 1 {
			return "(" + parts[0] + ",)"
		}
		return "(" + strings.Join(parts, ", ") + ")"
	case *ast.FnType:
		parts := make([]string, 0, len(n.Params))
		for _, p := range n.Params {
			parts = append(parts, RenderType(p))
		}
		out := "fn(" + strings.Join(parts, ", ") + ")"
		if n.ReturnType != nil {
			out += " -> " + RenderType(n.ReturnType)
		}
		return out
	}
	return fmt.Sprintf("/* %T */", t)
}

// renderLiteralExpr produces a best-effort string for an AST literal —
// enough to display default parameter values and `pub let` RHS values.
// Non-literal expressions are rare here (pub lets are literal-only per
// spec §3.1) but fall through to a placeholder rather than panicking.
func renderLiteralExpr(e ast.Expr) string {
	switch n := e.(type) {
	case *ast.IntLit:
		return n.Text
	case *ast.FloatLit:
		return n.Text
	case *ast.StringLit:
		return renderStringLit(n)
	case *ast.BoolLit:
		if n.Value {
			return "true"
		}
		return "false"
	case *ast.CharLit:
		return "'" + string(n.Value) + "'"
	case *ast.Ident:
		// Matches `None` and other single-ident enum variants used as
		// literals (e.g. `age: Int? = None`).
		return n.Name
	case *ast.CallExpr:
		// Rarely hit for defaults but renders e.g. `Some(0)` well.
		return renderCall(n)
	}
	return "..."
}

// renderCall handles simple `Name(arg, arg)` calls used in default
// values — just enough to avoid a bare "..." placeholder when a user
// writes `x: Int? = Some(0)`.
func renderCall(n *ast.CallExpr) string {
	var b strings.Builder
	b.WriteString(renderLiteralExpr(n.Fn))
	b.WriteByte('(')
	for i, a := range n.Args {
		if i > 0 {
			b.WriteString(", ")
		}
		if a.Name != "" {
			b.WriteString(a.Name)
			b.WriteString(": ")
		}
		b.WriteString(renderLiteralExpr(a.Value))
	}
	b.WriteByte(')')
	return b.String()
}

// renderStringLit reconstitutes a StringLit to a source-like form.
// Interpolated segments render as `{...}`; literal segments print raw.
// The result is wrapped in the same quote style the lexer preserved
// (triple for triple-quoted, backtick for raw).
func renderStringLit(n *ast.StringLit) string {
	open, close := `"`, `"`
	if n.IsTriple {
		open, close = `"""`, `"""`
	} else if n.IsRaw {
		open, close = "`", "`"
	}
	var b strings.Builder
	b.WriteString(open)
	for _, p := range n.Parts {
		if p.IsLit {
			b.WriteString(p.Lit)
		} else {
			b.WriteString("{...}")
		}
	}
	b.WriteString(close)
	return b.String()
}
