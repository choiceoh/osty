package gen

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/types"
)

// emitCollectionMethod rewrites §10.6 method calls on List/Map/Set
// receivers to Go equivalents. Returns true when handled. The
// receiver's type comes from the checker; method names fall through
// to the default call path when the type isn't one of the recognized
// collections (e.g. user types that happen to share a name).
//
// Lowering strategy:
//   - Pure methods call the generic helpers injected at file top
//     (ostyList*, ostyMap*, ostySet*). `needCollections` gates the
//     helper block.
//   - Mutating List methods take *[]T so the caller's slice header is
//     updated in place; callers are expected to be addressable
//     lvalues, which §10.6 enforces via `mut self`.
//   - Tuple-returning methods (enumerate / zip / entries) emit inline
//     IIFEs so the element type matches `emitTupleExpr`'s anonymous-
//     struct encoding.
func (g *gen) emitCollectionMethod(c *ast.CallExpr, f *ast.FieldExpr) bool {
	recvT := g.typeOf(f.X)
	n, ok := recvT.(*types.Named)
	if !ok || n.Sym == nil {
		return false
	}
	switch n.Sym.Name {
	case "List":
		if len(n.Args) != 1 {
			return false
		}
		return g.emitListMethod(c, f, n.Args[0])
	case "Map":
		if len(n.Args) != 2 {
			return false
		}
		return g.emitMapMethod(c, f, n.Args[0], n.Args[1])
	case "Set":
		if len(n.Args) != 1 {
			return false
		}
		return g.emitSetMethod(c, f, n.Args[0])
	}
	return false
}

// ------------------------------------------------------------------
// Shared emission helpers
// ------------------------------------------------------------------

// helperCall emits `helperName(recv, args...)`. When byPtr is true, the
// receiver is passed as `&recv` (for *[]T / *map mutation).
func (g *gen) helperCall(helperName string, recv ast.Expr, byPtr bool, args []*ast.Arg) {
	g.needCollections = true
	g.body.write(helperName)
	g.body.write("(")
	if byPtr {
		g.body.write("&")
	}
	g.emitExpr(recv)
	if len(args) > 0 {
		g.body.write(", ")
		g.emitCallArgList(args)
	}
	g.body.write(")")
}

// lenExpr writes `len(recv)` — shared by List/Map/Set `len()`.
func (g *gen) lenExpr(recv ast.Expr) {
	g.body.write("len(")
	g.emitExpr(recv)
	g.body.write(")")
}

// isEmptyExpr writes `len(recv) == 0`.
func (g *gen) isEmptyExpr(recv ast.Expr) {
	g.body.write("(len(")
	g.emitExpr(recv)
	g.body.write(") == 0)")
}

// tuple2Type returns the Go anonymous-struct form for an Osty 2-tuple
// `(a, b)`, matching emitTupleExpr's encoding so destructuring lines
// up at the for-range loop.
func tuple2Type(a, b string) string {
	return "struct{F0 " + a + "; F1 " + b + "}"
}

// ------------------------------------------------------------------
// List<T>
// ------------------------------------------------------------------

func (g *gen) emitListMethod(c *ast.CallExpr, f *ast.FieldExpr, elem types.Type) bool {
	// Pure helpers dispatched by name.
	switch f.Name {
	case "len":
		g.lenExpr(f.X)
		return true
	case "isEmpty":
		g.isEmptyExpr(f.X)
		return true
	case "first":
		g.helperCall("ostyListFirst", f.X, false, nil)
		return true
	case "last":
		g.helperCall("ostyListLast", f.X, false, nil)
		return true
	case "get":
		g.helperCall("ostyListGet", f.X, false, c.Args)
		return true
	case "contains":
		g.helperCall("ostyListContains", f.X, false, c.Args)
		return true
	case "indexOf":
		g.helperCall("ostyListIndexOf", f.X, false, c.Args)
		return true
	case "find":
		g.helperCall("ostyListFind", f.X, false, c.Args)
		return true
	case "map":
		g.helperCall("ostyListMap", f.X, false, c.Args)
		return true
	case "filter":
		g.helperCall("ostyListFilter", f.X, false, c.Args)
		return true
	case "fold":
		g.helperCall("ostyListFold", f.X, false, c.Args)
		return true
	case "reversed":
		g.helperCall("ostyListReversed", f.X, false, nil)
		return true
	case "appended":
		g.helperCall("ostyListAppended", f.X, false, c.Args)
		return true
	case "concat":
		g.helperCall("ostyListConcat", f.X, false, c.Args)
		return true

	// Inline IIFEs — either tuple-returning or sort-based.
	case "sorted":
		g.emitListSorted(f, elem, nil)
		return true
	case "sortedBy":
		if len(c.Args) != 1 {
			return false
		}
		g.emitListSorted(f, elem, c.Args[0].Value)
		return true
	case "enumerate":
		g.emitListEnumerate(f, elem)
		return true
	case "zip":
		if len(c.Args) != 1 {
			return false
		}
		g.emitListZip(f, elem, c.Args[0].Value)
		return true
	case "toSet":
		g.emitListToSet(f, elem)
		return true
	case "iter", "toList":
		// Identity — iterator fluency. Osty's iter/toList on a list
		// round-trips to itself.
		g.emitExpr(f.X)
		return true

	// Mutating helpers — receiver passed by pointer.
	case "push":
		g.helperCall("ostyListPush", f.X, true, c.Args)
		return true
	case "pop":
		g.helperCall("ostyListPop", f.X, true, nil)
		return true
	case "insert":
		g.helperCall("ostyListInsert", f.X, true, c.Args)
		return true
	case "removeAt":
		g.helperCall("ostyListRemoveAt", f.X, true, c.Args)
		return true
	case "clear":
		g.helperCall("ostyListClear", f.X, true, nil)
		return true
	case "reverse":
		g.helperCall("ostyListReverseInPlace", f.X, true, nil)
		return true
	case "sort":
		g.emitListSortInPlace(f)
		return true
	}
	return false
}

// emitListSortInPlace lowers the mutating `xs.sort()` to a direct
// sort.SliceStable call on the receiver. The receiver is evaluated
// exactly once via a short closure that captures it, so complex
// expressions (struct-field access, index) don't fire twice.
func (g *gen) emitListSortInPlace(f *ast.FieldExpr) {
	g.use("sort")
	g.body.write("func() { _s := ")
	g.emitExpr(f.X)
	g.body.write("; sort.SliceStable(_s, func(_i, _j int) bool { return _s[_i] < _s[_j] }) }()")
}

// emitListSorted lowers `xs.sorted()` / `xs.sortedBy(key)` to an IIFE
// that clones the slice and runs sort.SliceStable. The receiver is
// evaluated exactly once (bound to `_xs`) so side effects in the
// receiver expression don't fire twice. Ordering uses `<` on either
// the element (T: Ordered) or the key's return type (K: Ordered);
// the checker doesn't currently validate the Ordered constraint, so a
// non-ordered element type surfaces as a Go build error at run.
func (g *gen) emitListSorted(f *ast.FieldExpr, elem types.Type, keyFn ast.Expr) {
	g.use("sort")
	et := g.goType(elem)
	g.body.writef("func() []%s { _xs := ", et)
	g.emitExpr(f.X)
	g.body.writef("; _cp := make([]%s, len(_xs)); copy(_cp, _xs); ", et)
	if keyFn == nil {
		g.body.write("sort.SliceStable(_cp, func(_i, _j int) bool { return _cp[_i] < _cp[_j] })")
	} else {
		g.body.write("_key := ")
		g.emitExpr(keyFn)
		g.body.write("; sort.SliceStable(_cp, func(_i, _j int) bool { return _key(_cp[_i]) < _key(_cp[_j]) })")
	}
	g.body.write("; return _cp }()")
}

// emitListEnumerate writes an IIFE pairing each element with its
// index. Tuple shape matches emitTupleExpr so `for (i, v) in ...`
// destructuring lines up.
func (g *gen) emitListEnumerate(f *ast.FieldExpr, elem types.Type) {
	et := g.goType(elem)
	ty := tuple2Type("int", et)
	g.body.writef("func() []%s { _xs := ", ty)
	g.emitExpr(f.X)
	g.body.writef("; _out := make([]%s, len(_xs)); for _i, _v := range _xs { _out[_i] = %s{F0: _i, F1: _v} }; return _out }()", ty, ty)
}

// emitListZip pairs matching indices into tuples of (T, U).
func (g *gen) emitListZip(f *ast.FieldExpr, elem types.Type, other ast.Expr) {
	var uType types.Type = types.ErrorType
	if n, ok := g.typeOf(other).(*types.Named); ok && n.Sym != nil &&
		n.Sym.Name == "List" && len(n.Args) == 1 {
		uType = n.Args[0]
	}
	ty := tuple2Type(g.goType(elem), g.goType(uType))
	g.body.writef("func() []%s { _a := ", ty)
	g.emitExpr(f.X)
	g.body.write("; _b := ")
	g.emitExpr(other)
	g.body.writef("; _n := len(_a); if len(_b) < _n { _n = len(_b) }; _out := make([]%s, _n); for _i := 0; _i < _n; _i++ { _out[_i] = %s{F0: _a[_i], F1: _b[_i]} }; return _out }()", ty, ty)
}

// emitListToSet converts List<T> into a map-backed Set<T>.
func (g *gen) emitListToSet(f *ast.FieldExpr, elem types.Type) {
	et := g.goType(elem)
	g.body.writef("func() map[%s]struct{} { _xs := ", et)
	g.emitExpr(f.X)
	g.body.writef("; _s := make(map[%s]struct{}, len(_xs)); for _, _v := range _xs { _s[_v] = struct{}{} }; return _s }()", et)
}

// ------------------------------------------------------------------
// Map<K, V>
// ------------------------------------------------------------------

func (g *gen) emitMapMethod(c *ast.CallExpr, f *ast.FieldExpr, k, v types.Type) bool {
	switch f.Name {
	case "len":
		g.lenExpr(f.X)
		return true
	case "isEmpty":
		g.isEmptyExpr(f.X)
		return true
	case "get":
		g.helperCall("ostyMapGet", f.X, false, c.Args)
		return true
	case "containsKey":
		g.helperCall("ostyMapContainsKey", f.X, false, c.Args)
		return true
	case "keys":
		g.helperCall("ostyMapKeys", f.X, false, nil)
		return true
	case "values":
		g.helperCall("ostyMapValues", f.X, false, nil)
		return true
	case "entries":
		g.emitMapEntries(f, k, v)
		return true
	// Map is a reference type (Go's built-in map is a header), so
	// mutations don't need a pointer receiver.
	case "insert":
		g.helperCall("ostyMapInsert", f.X, false, c.Args)
		return true
	case "remove":
		g.helperCall("ostyMapRemove", f.X, false, c.Args)
		return true
	case "clear":
		g.helperCall("ostyMapClear", f.X, false, nil)
		return true
	}
	return false
}

// emitMapEntries materialises the map as a slice of (K, V) tuples. Map
// iteration order is not stable in Go; callers that need a specific
// order should sort the result.
func (g *gen) emitMapEntries(f *ast.FieldExpr, k, v types.Type) {
	ty := tuple2Type(g.goType(k), g.goType(v))
	g.body.writef("func() []%s { _m := ", ty)
	g.emitExpr(f.X)
	g.body.writef("; _out := make([]%s, 0, len(_m)); for _k, _v := range _m { _out = append(_out, %s{F0: _k, F1: _v}) }; return _out }()", ty, ty)
}

// ------------------------------------------------------------------
// Set<T>
// ------------------------------------------------------------------

func (g *gen) emitSetMethod(c *ast.CallExpr, f *ast.FieldExpr, elem types.Type) bool {
	_ = elem // element type is already encoded in the map key type
	switch f.Name {
	case "len":
		g.lenExpr(f.X)
		return true
	case "isEmpty":
		g.isEmptyExpr(f.X)
		return true
	case "contains":
		g.helperCall("ostySetContains", f.X, false, c.Args)
		return true
	case "insert":
		g.helperCall("ostySetInsert", f.X, false, c.Args)
		return true
	case "remove":
		g.helperCall("ostySetRemove", f.X, false, c.Args)
		return true
	case "clear":
		g.helperCall("ostySetClear", f.X, false, nil)
		return true
	case "union":
		g.helperCall("ostySetUnion", f.X, false, c.Args)
		return true
	case "intersect":
		g.helperCall("ostySetIntersect", f.X, false, c.Args)
		return true
	case "difference":
		g.helperCall("ostySetDifference", f.X, false, c.Args)
		return true
	}
	return false
}
