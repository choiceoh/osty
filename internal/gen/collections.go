package gen

import (
	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/types"
)

// emitCollectionMethod rewrites §10.6 method calls on List/Map/Set
// receivers to Go equivalents. Returns true when handled. The
// receiver's type comes from the checker; methods fall through to the
// default call path when the type isn't one of the recognized
// collections (e.g. user types that happen to share a name).
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

// emitListMethod handles List<T> methods. Returns true when handled.
func (g *gen) emitListMethod(c *ast.CallExpr, f *ast.FieldExpr, elem types.Type) bool {
	switch f.Name {
	case "len":
		g.body.write("len(")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	case "isEmpty":
		g.body.write("(len(")
		g.emitExpr(f.X)
		g.body.write(") == 0)")
		return true
	case "first":
		g.needCollections = true
		g.body.write("ostyListFirst(")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	case "last":
		g.needCollections = true
		g.body.write("ostyListLast(")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	case "get":
		g.needCollections = true
		g.body.write("ostyListGet(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "contains":
		g.needCollections = true
		g.body.write("ostyListContains(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "indexOf":
		g.needCollections = true
		g.body.write("ostyListIndexOf(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "find":
		g.needCollections = true
		g.body.write("ostyListFind(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "map":
		g.needCollections = true
		g.body.write("ostyListMap(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "filter":
		g.needCollections = true
		g.body.write("ostyListFilter(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "fold":
		g.needCollections = true
		g.body.write("ostyListFold(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "reversed":
		g.needCollections = true
		g.body.write("ostyListReversed(")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	case "appended":
		g.needCollections = true
		g.body.write("ostyListAppended(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "concat":
		g.needCollections = true
		g.body.write("ostyListConcat(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
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
	case "iter", "toList":
		// Identity for iterable-chain fluency.
		g.emitExpr(f.X)
		return true
	case "toSet":
		g.emitListToSet(f, elem)
		return true
	case "push":
		g.needCollections = true
		g.body.write("ostyListPush(&")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "pop":
		g.needCollections = true
		g.body.write("ostyListPop(&")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	case "insert":
		g.needCollections = true
		g.body.write("ostyListInsert(&")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "removeAt":
		g.needCollections = true
		g.body.write("ostyListRemoveAt(&")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "clear":
		g.needCollections = true
		g.body.write("ostyListClear(&")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	case "reverse":
		g.needCollections = true
		g.body.write("ostyListReverseInPlace(&")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	case "sort":
		g.emitListSortInPlace(f, elem)
		return true
	}
	return false
}

// emitListSorted writes a sort-returning-new-slice IIFE. When keyFn is
// nil, elements are compared directly; otherwise the callback's return
// drives the ordering.
func (g *gen) emitListSorted(f *ast.FieldExpr, elem types.Type, keyFn ast.Expr) {
	g.use("sort")
	et := g.goType(elem)
	g.body.writef("func() []%s { _cp := make([]%s, len(", et, et)
	g.emitExpr(f.X)
	g.body.write(")); copy(_cp, ")
	g.emitExpr(f.X)
	g.body.write("); ")
	if keyFn == nil {
		g.body.writef("sort.SliceStable(_cp, func(_i, _j int) bool { return %s })", ostyLessExpr("_cp[_i]", "_cp[_j]", elem))
	} else {
		g.body.write("_key := ")
		g.emitExpr(keyFn)
		g.body.write("; sort.SliceStable(_cp, func(_i, _j int) bool { return ")
		g.body.writef("%s })", ostyLessExpr("_key(_cp[_i])", "_key(_cp[_j])", nil))
	}
	g.body.write("; return _cp }()")
}

// emitListSortInPlace writes the mutating sort lowering for sort().
func (g *gen) emitListSortInPlace(f *ast.FieldExpr, elem types.Type) {
	g.use("sort")
	g.body.write("sort.SliceStable(")
	g.emitExpr(f.X)
	g.body.write(", func(_i, _j int) bool { return ")
	lhs := "("
	// Need to reference the receiver slice; emit as IIFE-like form.
	// Simpler: compute via closure over the receiver expression.
	_ = lhs
	// Use an anonymous helper: capture receiver once.
	// Rewrite: sort.SliceStable(xs, func(i,j int) bool { return xs[i] < xs[j] })
	g.emitExpr(f.X)
	g.body.write("[_i] < ")
	g.emitExpr(f.X)
	g.body.write("[_j] })")
	_ = elem
}

// emitListEnumerate writes an IIFE that pairs each element with its
// index using the tuple anonymous struct shape `struct{F0 int; F1 T}`
// so it lines up with emitTupleExpr's encoding.
func (g *gen) emitListEnumerate(f *ast.FieldExpr, elem types.Type) {
	et := g.goType(elem)
	tupleTy := "struct{F0 int; F1 " + et + "}"
	g.body.writef("func() []%s { _xs := ", tupleTy)
	g.emitExpr(f.X)
	g.body.writef("; _out := make([]%s, len(_xs)); for _i, _v := range _xs { _out[_i] = %s{F0: _i, F1: _v} }; return _out }()", tupleTy, tupleTy)
}

// emitListZip pairs matching indices into tuples of (T, U).
func (g *gen) emitListZip(f *ast.FieldExpr, elem types.Type, other ast.Expr) {
	otherT := g.typeOf(other)
	var uType types.Type = types.ErrorType
	if n, ok := otherT.(*types.Named); ok && n.Sym != nil && n.Sym.Name == "List" && len(n.Args) == 1 {
		uType = n.Args[0]
	}
	et := g.goType(elem)
	ut := g.goType(uType)
	tupleTy := "struct{F0 " + et + "; F1 " + ut + "}"
	g.body.writef("func() []%s { _a := ", tupleTy)
	g.emitExpr(f.X)
	g.body.write("; _b := ")
	g.emitExpr(other)
	g.body.writef("; _n := len(_a); if len(_b) < _n { _n = len(_b) }; _out := make([]%s, _n); for _i := 0; _i < _n; _i++ { _out[_i] = %s{F0: _a[_i], F1: _b[_i]} }; return _out }()", tupleTy, tupleTy)
}

// emitListToSet converts a List<T> into a map-backed Set<T>.
func (g *gen) emitListToSet(f *ast.FieldExpr, elem types.Type) {
	et := g.goType(elem)
	g.body.writef("func() map[%s]struct{} { _xs := ", et)
	g.emitExpr(f.X)
	g.body.writef("; _s := make(map[%s]struct{}, len(_xs)); for _, _v := range _xs { _s[_v] = struct{}{} }; return _s }()", et)
}

// emitMapMethod handles Map<K, V> methods.
func (g *gen) emitMapMethod(c *ast.CallExpr, f *ast.FieldExpr, k, v types.Type) bool {
	switch f.Name {
	case "len":
		g.body.write("len(")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	case "isEmpty":
		g.body.write("(len(")
		g.emitExpr(f.X)
		g.body.write(") == 0)")
		return true
	case "get":
		g.needCollections = true
		g.body.write("ostyMapGet(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "containsKey":
		g.needCollections = true
		g.body.write("ostyMapContainsKey(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "keys":
		g.needCollections = true
		g.body.write("ostyMapKeys(")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	case "values":
		g.needCollections = true
		g.body.write("ostyMapValues(")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	case "entries":
		g.emitMapEntries(f, k, v)
		return true
	case "insert":
		g.needCollections = true
		g.body.write("ostyMapInsert(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "remove":
		g.needCollections = true
		g.body.write("ostyMapRemove(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "clear":
		g.needCollections = true
		g.body.write("ostyMapClear(")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	}
	return false
}

// emitMapEntries walks the map and materialises tuple-shaped entries.
func (g *gen) emitMapEntries(f *ast.FieldExpr, k, v types.Type) {
	kt := g.goType(k)
	vt := g.goType(v)
	tupleTy := "struct{F0 " + kt + "; F1 " + vt + "}"
	g.body.writef("func() []%s { _m := ", tupleTy)
	g.emitExpr(f.X)
	g.body.writef("; _out := make([]%s, 0, len(_m)); for _k, _v := range _m { _out = append(_out, %s{F0: _k, F1: _v}) }; return _out }()", tupleTy, tupleTy)
}

// emitSetMethod handles Set<T> methods.
func (g *gen) emitSetMethod(c *ast.CallExpr, f *ast.FieldExpr, elem types.Type) bool {
	switch f.Name {
	case "len":
		g.body.write("len(")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	case "isEmpty":
		g.body.write("(len(")
		g.emitExpr(f.X)
		g.body.write(") == 0)")
		return true
	case "contains":
		g.needCollections = true
		g.body.write("ostySetContains(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "insert":
		g.needCollections = true
		g.body.write("ostySetInsert(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "remove":
		g.needCollections = true
		g.body.write("ostySetRemove(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "clear":
		g.needCollections = true
		g.body.write("ostySetClear(")
		g.emitExpr(f.X)
		g.body.write(")")
		return true
	case "union":
		g.needCollections = true
		g.body.write("ostySetUnion(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "intersect":
		g.needCollections = true
		g.body.write("ostySetIntersect(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	case "difference":
		g.needCollections = true
		g.body.write("ostySetDifference(")
		g.emitExpr(f.X)
		g.body.write(", ")
		g.emitCallArgList(c.Args)
		g.body.write(")")
		return true
	}
	_ = elem
	return false
}

// ostyLessExpr returns the Go expression used to compare two values
// for sort ordering. Unused types parameter kept for future refinement
// (e.g. string vs numeric comparators); for now we trust Go's built-in
// `<` to work on the element type.
func ostyLessExpr(a, b string, _ types.Type) string {
	return a + " < " + b
}
