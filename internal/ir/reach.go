package ir

// QualifiedRef is a `qualifier.name` reference observed in a module,
// typically a stdlib module call like `strings.compare` or
// `collections.update`. Reach reports these so a caller can decide
// which stdlib declarations need to be lowered alongside user code.
//
// Qualifier is the left-hand Ident name exactly as it appears in
// source. Name is the FieldExpr name on the right. Both are raw
// strings with no stdlib validation attached; cross-referencing
// against a stdlib.Registry happens in the caller.
type QualifiedRef struct {
	Qualifier string
	Name      string
}

// Reach walks mod and returns every `qualifier.name` pair used in
// call positions. The result is deduplicated; iteration order is not
// guaranteed.
//
// A bare identifier not under a FieldExpr (e.g. a local variable
// reference) is ignored. TypeNames and turbofish arguments are also
// skipped — stdlib body reachability starts from call sites because
// stdlib APIs are consumed by call, not by type reference.
func Reach(mod *Module) map[QualifiedRef]struct{} {
	out := map[QualifiedRef]struct{}{}
	if mod == nil {
		return out
	}
	Walk(reachVisitor(out), mod)
	return out
}

// MethodRef is a method call observed on a value whose static type
// belongs to a named package. Module is the receiver type's Package
// (e.g. "encoding"), Type is the receiver's NamedType.Name (e.g.
// "Hex"), Method is the method name.
//
// MethodRef is the type-qualified counterpart to QualifiedRef:
// QualifiedRef captures `module.fn(...)` calls (free-function dispatch
// on a stdlib module alias) while MethodRef captures
// `value.method(...)` calls where the value's static type lets the
// caller route the body lookup through `Registry.LookupMethodDecl`.
//
// Methods on user-defined types (Package = "") are not represented;
// downstream consumers should drop them at the source.
type MethodRef struct {
	Module string
	Type   string
	Method string
}

// ReachMethods walks mod and returns every method call whose receiver
// has a NamedType with a non-empty Package, deduplicated. Methods on
// user-defined types (no package qualifier) and methods on built-in
// generic shapes (List, Map, Option, ...) when their package is empty
// are skipped — the former because user methods are already visible
// inline, the latter because their decls are injected via the existing
// `injectReachableStdlibTypes` path.
//
// Iteration order is not guaranteed. A nil module returns an empty
// map, mirroring Reach.
//
// Backends that drive stdlib body injection consume this alongside
// Reach: Reach feeds free-function injection, ReachMethods feeds the
// (still-WIP) method-body injector wired against
// `Registry.LookupMethodDecl`.
func ReachMethods(mod *Module) map[MethodRef]struct{} {
	out := map[MethodRef]struct{}{}
	if mod == nil {
		return out
	}
	Walk(methodReachVisitor(out), mod)
	return out
}

type reachVisitor map[QualifiedRef]struct{}

type methodReachVisitor map[MethodRef]struct{}

// Visit records (module, type, method) triples for every MethodCall
// whose receiver has a NamedType with a non-empty Package. The Package
// field is set during lowering by `fromCheckerType` whenever the
// receiver's named symbol resolves to a stdlib module — so this
// visitor naturally filters to stdlib-owned types without having to
// consult a registry.
func (r methodReachVisitor) Visit(n Node) Visitor {
	call, ok := n.(*MethodCall)
	if !ok || call == nil || call.Receiver == nil || call.Name == "" {
		return r
	}
	named, ok := call.Receiver.Type().(*NamedType)
	if !ok || named == nil || named.Package == "" || named.Name == "" {
		return r
	}
	r[MethodRef{Module: named.Package, Type: named.Name, Method: call.Name}] = struct{}{}
	return r
}

// Visit records qualifier.name pairs from two equivalent IR shapes:
//   - CallExpr{Callee: FieldExpr{X: Ident, Name: …}} — what a module
//     synthesised in a test or a direct IR builder looks like.
//   - MethodCall{Receiver: Ident, Name: …} — what ir.Lower produces for
//     every `x.m(args)` in source, whether `x` is a value, a stdlib
//     module, or a user alias. The lowerer cannot disambiguate at its
//     stage because method dispatch and module qualification share the
//     same syntax.
//
// The scan treats both shapes uniformly; the caller filters against the
// stdlib registry, so a user-defined method call on a local named
// `strings` simply fails the registry lookup and is discarded.
func (r reachVisitor) Visit(n Node) Visitor {
	switch call := n.(type) {
	case *CallExpr:
		if field, ok := call.Callee.(*FieldExpr); ok {
			if ident, ok := field.X.(*Ident); ok && ident.Name != "" && field.Name != "" {
				r[QualifiedRef{Qualifier: ident.Name, Name: field.Name}] = struct{}{}
			}
		}
	case *MethodCall:
		if ident, ok := call.Receiver.(*Ident); ok && ident.Name != "" && call.Name != "" {
			r[QualifiedRef{Qualifier: ident.Name, Name: call.Name}] = struct{}{}
		}
	}
	return r
}
