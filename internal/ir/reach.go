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

type reachVisitor map[QualifiedRef]struct{}

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
