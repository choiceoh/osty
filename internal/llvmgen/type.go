// type.go — source-type (ast.Type) → LLVM IR type mapping, runtime-ABI
// mapping, container element-type / key-type introspection, aggregate type
// naming (Tuple / Result / struct), static expression type inference
// (staticExprInfo, staticCollectionMethodResult, list/map/set MethodInfo),
// and GC root-path analysis for managed type shapes.
//
// NOTE(osty-migration): mapping AST types to LLVM IR types is another
// AST-bound concern; the natural migration target is an IR-typed surface
// (toolchain/ir.osty IrNode.typeName) once the backend consumes IR directly.
package llvmgen

import (
	"strings"

	"github.com/osty/osty/internal/ast"
)

type typeEnv struct {
	structs    map[string]*structInfo
	enums      map[string]*enumInfo
	interfaces map[string]*interfaceInfo
	aliases    map[string]*typeAliasInfo
}

func llvmBuiltinAggregateName(prefix string, parts ...string) string {
	return mirLlvmBuiltinAggregateName(prefix, parts)
}

// llvmBuiltinAggregatePart folds a type-name fragment into the LLVM
// identifier alphabet. Delegates to the Osty-sourced
// `mirLLVMBuiltinAggregatePart` (`toolchain/mir_generator.osty`).
func llvmBuiltinAggregatePart(part string) string {
	return mirLLVMBuiltinAggregatePart(part)
}

func llvmResultTypeName(okTyp, errTyp string) string {
	return mirLlvmResultTypeName(okTyp, errTyp)
}

func llvmTupleTypeName(elemTypes []string) string {
	return mirLlvmTupleTypeName(elemTypes)
}

func llvmRuntimeABIType(t ast.Type, env typeEnv) (string, error) {
	resolved, err := llvmResolveAliasType(t, env, map[string]bool{})
	if err != nil {
		return "", err
	}
	t = resolved
	switch tt := t.(type) {
	case nil:
		return "void", nil
	case *ast.NamedType:
		if info, ok := builtinRangeTypeFromAST(tt, env); ok {
			return info.typ, nil
		}
		name := ""
		structType := ""
		enumType := ""
		if len(tt.Path) == 1 {
			name = tt.Path[0]
			if info := env.structs[name]; info != nil {
				structType = info.typ
			}
			if info := env.enums[name]; info != nil {
				enumType = info.typ
			}
			if env.interfaces[name] != nil {
				// Phase 6b: interface values are `%osty.iface` fat
				// pointers (data ptr + vtable ptr). At the runtime-ABI
				// boundary they're passed by value just like any other
				// aggregate.
				return "%osty.iface", nil
			}
		}
		return llvmRuntimeAbiNamedType(name, len(tt.Path), len(tt.Args), structType, enumType), nil
	case *ast.OptionalType, *ast.TupleType, *ast.FnType:
		return "ptr", nil
	default:
		return "", unsupportedf("type-system", "runtime ABI type %T", t)
	}
}

func llvmListElementType(t ast.Type, env typeEnv) (string, bool, error) {
	resolved, err := llvmResolveAliasType(t, env, map[string]bool{})
	if err != nil {
		return "", false, err
	}
	t = resolved
	named, ok := t.(*ast.NamedType)
	if !ok {
		return "", false, nil
	}
	name := ""
	if len(named.Path) == 1 {
		name = named.Path[0]
	}
	if !llvmIsRuntimeAbiListType(name, len(named.Path), len(named.Args)) {
		return "", false, nil
	}
	// Lists store the backend's concrete element representation, not the
	// runtime-call ABI form. That keeps aggregate elements such as tuples,
	// structs, and Result payloads on the aggregate-bytes path instead of
	// collapsing them to ptr during IR->AST bridge emission.
	elemTyp, err := llvmType(named.Args[0], env)
	if err != nil {
		return "", true, err
	}
	return elemTyp, true, nil
}

func llvmListElementInfo(t ast.Type, env typeEnv) (string, bool, bool, error) {
	elemTyp, ok, err := llvmListElementType(t, env)
	if err != nil || !ok {
		return elemTyp, false, ok, err
	}
	return elemTyp, llvmNamedTypeIsString(t.(*ast.NamedType).Args[0]), true, nil
}

func llvmMapTypes(t ast.Type, env typeEnv) (string, string, bool, bool, error) {
	named, ok := t.(*ast.NamedType)
	if !ok {
		return "", "", false, false, nil
	}
	if len(named.Path) != 1 || named.Path[0] != "Map" || len(named.Args) != 2 {
		return "", "", false, false, nil
	}
	keyTyp, err := llvmRuntimeABIType(named.Args[0], env)
	if err != nil {
		return "", "", false, true, err
	}
	valueTyp, err := llvmRuntimeABIType(named.Args[1], env)
	if err != nil {
		return "", "", false, true, err
	}
	return keyTyp, valueTyp, llvmNamedTypeIsString(named.Args[0]), true, nil
}

func llvmSetElementType(t ast.Type, env typeEnv) (string, bool, bool, error) {
	named, ok := t.(*ast.NamedType)
	if !ok {
		return "", false, false, nil
	}
	if len(named.Path) != 1 || named.Path[0] != "Set" || len(named.Args) != 1 {
		return "", false, false, nil
	}
	elemTyp, err := llvmRuntimeABIType(named.Args[0], env)
	if err != nil {
		return "", false, true, err
	}
	return elemTyp, llvmNamedTypeIsString(named.Args[0]), true, nil
}

func llvmNamedTypeIsString(t ast.Type) bool {
	named, ok := t.(*ast.NamedType)
	return ok && len(named.Path) == 1 && named.Path[0] == "String" && len(named.Args) == 0
}

func llvmNamedTypeIsBytes(t ast.Type) bool {
	named, ok := t.(*ast.NamedType)
	return ok && len(named.Path) == 1 && named.Path[0] == "Bytes" && len(named.Args) == 0
}

func llvmNamedTypeIsByte(t ast.Type) bool {
	named, ok := t.(*ast.NamedType)
	return ok && len(named.Path) == 1 && named.Path[0] == "Byte" && len(named.Args) == 0
}

func llvmEnumPayloadType(t ast.Type, env typeEnv) (string, error) {
	resolved, err := llvmResolveAliasType(t, env, map[string]bool{})
	if err != nil {
		return "", err
	}
	switch resolved.(type) {
	case *ast.FnType, *ast.OptionalType, *ast.TupleType:
		return "ptr", nil
	}
	named, ok := t.(*ast.NamedType)
	if resolvedNamed, resolvedOK := resolved.(*ast.NamedType); resolvedOK {
		named = resolvedNamed
		ok = true
	}
	if ok {
		name := ""
		if len(named.Path) == 1 {
			name = named.Path[0]
		}
		if typ := llvmEnumPayloadNamedType(name, len(named.Path), len(named.Args)); typ != "" {
			return typ, nil
		}
	}
	// Fall through to the runtime-ABI mapping so interface, struct,
	// enum, and container payload types all get a well-defined LLVM
	// representation. Canonical motivating case: `Result<T, Error>`
	// after `injectReachableStdlibTypes` — `Err` carries an `Error`
	// interface payload which the primitive table doesn't list but
	// the rest of the backend already passes around as
	// `%osty.iface`. Tuples / Optional / Fn types fold into `ptr` the
	// same way a runtime-ABI boundary treats them.
	abi, err := llvmRuntimeABIType(resolved, env)
	if err != nil {
		return "", err
	}
	if abi == "" || abi == "void" {
		return "", unsupported("type-system", "LLVM enum payloads currently support primitives (Int / Float / Char / Byte / String), interfaces, structs, enums, and container / tuple / Optional / Fn types — got an empty runtime-ABI projection")
	}
	return abi, nil
}

func (g *generator) structInfoForExpr(expr ast.Expr) (*structInfo, string, error) {
	typeName, ok := structTypeExprName(expr)
	if !ok {
		return nil, "", unsupportedf("type-system", "struct literal type %T", expr)
	}
	if info := g.structsByName[typeName]; info != nil {
		return info, typeName, nil
	}
	resolved, ok, err := resolveAliasNamedTarget(typeName, g.typeEnv(), map[string]bool{})
	if err != nil {
		return nil, typeName, err
	}
	if ok {
		if info := g.structsByName[resolved]; info != nil {
			return info, typeName, nil
		}
	}
	return nil, typeName, unsupportedf("type-system", "unknown struct %q", typeName)
}

func (g *generator) enumInfoByName(name string) *enumInfo {
	if info := g.enumsByName[name]; info != nil {
		return info
	}
	resolved, ok, err := resolveAliasNamedTarget(name, g.typeEnv(), map[string]bool{})
	if err != nil || !ok {
		return nil
	}
	return g.enumsByName[resolved]
}

func unwrapOptionalSourceType(t ast.Type) (ast.Type, bool) {
	if opt, ok := t.(*ast.OptionalType); ok && opt != nil && opt.Inner != nil {
		return opt.Inner, true
	}
	// Long form `Option<T>` is semantically identical to the `T?`
	// shorthand — both lower to a ptr-backed Option in the LLVM
	// runtime. Unwrap it the same way so context-sensitive paths
	// (None literal, Option-aware coalesce, etc.) work for either
	// surface.
	if nt, ok := t.(*ast.NamedType); ok && nt != nil && len(nt.Path) == 1 && nt.Path[0] == "Option" && len(nt.Args) == 1 && nt.Args[0] != nil {
		return nt.Args[0], true
	}
	return nil, false
}

func wrapOptionalSourceType(t ast.Type) ast.Type {
	if t == nil {
		return nil
	}
	if _, ok := t.(*ast.OptionalType); ok {
		return t
	}
	return &ast.OptionalType{Inner: t}
}

func (g *generator) rootPathsForType(typ string) [][]int {
	return g.rootPathsForTypeSeen(typ, map[string]bool{})
}

func (g *generator) rootPathsForTypeSeen(typ string, seen map[string]bool) [][]int {
	if typ == "" || typ == "ptr" || typ == "i64" || typ == "i1" || typ == "double" {
		return nil
	}
	if seen[typ] {
		return nil
	}
	if info := g.structsByType[typ]; info != nil {
		seen[typ] = true
		var out [][]int
		for _, field := range info.fields {
			if field.listElemTyp != "" || field.mapKeyTyp != "" || field.setElemTyp != "" {
				out = append(out, []int{field.index})
				continue
			}
			out = append(out, prependRootIndex(field.index, g.rootPathsForTypeSeen(field.typ, seen))...)
		}
		delete(seen, typ)
		return out
	}
	if info := g.enumsByType[typ]; info != nil && info.hasPayload {
		seen[typ] = true
		defer delete(seen, typ)
		if info.isBoxed {
			return [][]int{{1}}
		}
		slotTypes := info.payloadSlotTypes
		if len(slotTypes) == 0 && info.payloadTyp != "" {
			slotTypes = []string{info.payloadTyp}
		}
		var out [][]int
		for i, slotTyp := range slotTypes {
			slot := i + 1
			if slotTyp == "ptr" {
				out = append(out, []int{slot})
				continue
			}
			out = append(out, prependRootIndex(slot, g.rootPathsForTypeSeen(slotTyp, seen))...)
		}
		return out
	}
	if info, ok := g.resultTypes[typ]; ok {
		seen[typ] = true
		defer delete(seen, typ)
		var out [][]int
		if info.okTyp == "ptr" {
			out = append(out, []int{1})
		} else {
			out = append(out, prependRootIndex(1, g.rootPathsForTypeSeen(info.okTyp, seen))...)
		}
		if info.errTyp == "ptr" {
			out = append(out, []int{2})
		} else {
			out = append(out, prependRootIndex(2, g.rootPathsForTypeSeen(info.errTyp, seen))...)
		}
		return out
	}
	if info, ok := g.tupleTypes[typ]; ok {
		seen[typ] = true
		defer delete(seen, typ)
		var out [][]int
		for i, elemTyp := range info.elems {
			if i < len(info.elemListElemTyps) && info.elemListElemTyps[i] != "" {
				out = append(out, []int{i})
				continue
			}
			out = append(out, prependRootIndex(i, g.rootPathsForTypeSeen(elemTyp, seen))...)
		}
		return out
	}
	return nil
}

func (g *generator) aggregateFieldType(typ string, index int) (string, bool) {
	if info := g.structsByType[typ]; info != nil {
		for _, field := range info.fields {
			if field.index == index {
				return field.typ, true
			}
		}
		return "", false
	}
	if info := g.enumsByType[typ]; info != nil && info.hasPayload {
		if index == 0 {
			return "i64", true
		}
		if info.isBoxed {
			if index == 1 {
				return "ptr", true
			}
			return "", false
		}
		slot := index - 1
		if slot >= 0 && slot < len(info.payloadSlotTypes) {
			return info.payloadSlotTypes[slot], true
		}
		if slot == 0 && info.payloadTyp != "" {
			return info.payloadTyp, true
		}
	}
	if info, ok := g.tupleTypes[typ]; ok {
		if index < 0 || index >= len(info.elems) {
			return "", false
		}
		return info.elems[index], true
	}
	return "", false
}

func (g *generator) staticExprSourceType(expr ast.Expr) (ast.Type, bool) {
	switch e := expr.(type) {
	case *ast.IntLit:
		return &ast.NamedType{Path: []string{"Int"}}, true
	case *ast.FloatLit:
		return &ast.NamedType{Path: []string{"Float"}}, true
	case *ast.BoolLit:
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case *ast.CharLit:
		return &ast.NamedType{Path: []string{"Char"}}, true
	case *ast.ByteLit:
		return &ast.NamedType{Path: []string{"Byte"}}, true
	case *ast.StringLit:
		return &ast.NamedType{Path: []string{"String"}}, true
	case *ast.Ident:
		if v, ok := g.lookupBinding(e.Name); ok {
			if v.sourceType != nil {
				return v.sourceType, true
			}
			if src := fnSourceTypeFromSignature(v.fnSigRef); src != nil {
				return src, true
			}
		}
		if sig := g.functions[e.Name]; sig != nil && sig.receiverType == "" {
			if src := fnSourceTypeFromSignature(sig); src != nil {
				return src, true
			}
		}
	case *ast.ParenExpr:
		return g.staticExprSourceType(e.X)
	case *ast.TupleExpr:
		return g.staticTupleLiteralSourceType(e)
	case *ast.ListExpr:
		return g.staticListLiteralSourceType(e)
	case *ast.MapExpr:
		return g.staticMapLiteralSourceType(e)
	case *ast.RangeExpr:
		return g.staticRangeExprSourceType(e)
	case *ast.QuestionExpr:
		src, ok := g.staticExprSourceType(e.X)
		if !ok {
			return nil, false
		}
		return unwrapOptionalSourceType(src)
	case *ast.CallExpr:
		if src, ok := g.staticStringMethodSourceType(e); ok {
			return src, true
		}
		if src, ok := g.staticBytesMethodSourceType(e); ok {
			return src, true
		}
		if src, ok := g.staticBytesNamespaceCallSourceType(e); ok {
			return src, true
		}
		if src, ok := g.staticStdBytesCallSourceType(e); ok {
			return src, true
		}
		if src, ok := g.staticStdStringsCallSourceType(e); ok {
			return src, true
		}
		if src, ok := g.staticStdEnvCallSourceType(e); ok {
			return src, true
		}
		if src, ok := g.staticStdCryptoCallSourceType(e); ok {
			return src, true
		}
		if src, ok := g.staticStdOsCallSourceType(e); ok {
			return src, true
		}
		if src, ok := g.staticPtrBackedErrorCallSourceType(e); ok {
			return src, true
		}
		if src, ok := g.staticCollectionMethodSourceType(e); ok {
			return src, true
		}
		if src, ok := g.staticMapMethodSourceType(e); ok {
			return src, true
		}
		if src, ok := g.staticListMethodSourceType(e); ok {
			return src, true
		}
		if id, ok := e.Fn.(*ast.Ident); ok {
			if sig := g.functions[id.Name]; sig != nil && sig.returnSourceType != nil {
				return sig.returnSourceType, true
			}
		}
		if field, ok := e.Fn.(*ast.FieldExpr); ok {
			baseSource, ok := g.staticExprSourceType(field.X)
			if ok {
				ownerSource := baseSource
				if field.IsOptional {
					var hasInner bool
					ownerSource, hasInner = unwrapOptionalSourceType(baseSource)
					if !hasInner {
						return nil, false
					}
				}
				if ownerTyp, err := llvmType(ownerSource, g.typeEnv()); err == nil {
					if methods := g.methods[ownerTyp]; methods != nil {
						if sig := methods[field.Name]; sig != nil && sig.returnSourceType != nil {
							if field.IsOptional {
								return wrapOptionalSourceType(sig.returnSourceType), true
							}
							return sig.returnSourceType, true
						}
					}
				}
			}
		}
		if fn, found, err := g.runtimeFFICallTarget(e); found && err == nil && fn.returnSourceType != nil {
			return fn.returnSourceType, true
		}
	case *ast.FieldExpr:
		baseSource, ok := g.staticExprSourceType(e.X)
		if !ok {
			return nil, false
		}
		if field, ok := g.builtinRangeFieldInfo(baseSource, e.Name); ok {
			if e.IsOptional {
				return wrapOptionalSourceType(field.sourceType), true
			}
			return field.sourceType, true
		}
		ownerSource := baseSource
		if e.IsOptional {
			var hasInner bool
			ownerSource, hasInner = unwrapOptionalSourceType(baseSource)
			if !hasInner {
				return nil, false
			}
		}
		ownerTyp, err := llvmType(ownerSource, g.typeEnv())
		if err != nil {
			return nil, false
		}
		if info := g.structsByType[ownerTyp]; info != nil {
			if field, ok := info.byName[e.Name]; ok && field.sourceType != nil {
				if e.IsOptional {
					return wrapOptionalSourceType(field.sourceType), true
				}
				return field.sourceType, true
			}
		}
	case *ast.IndexExpr:
		baseSource, ok := g.staticExprSourceType(e.X)
		if !ok {
			return nil, false
		}
		resolved, err := llvmResolveAliasType(baseSource, g.typeEnv(), map[string]bool{})
		if err != nil {
			return nil, false
		}
		named, ok := resolved.(*ast.NamedType)
		if !ok || len(named.Path) != 1 {
			return nil, false
		}
		switch named.Path[0] {
		case "List":
			if len(named.Args) == 1 {
				return named.Args[0], true
			}
		case "Map":
			if len(named.Args) == 2 {
				return named.Args[1], true
			}
		}
	}
	return nil, false
}

func (g *generator) staticRangeExprSourceType(expr *ast.RangeExpr) (ast.Type, bool) {
	if expr == nil {
		return nil, false
	}
	elem := ast.Type(&ast.NamedType{Path: []string{"Int"}})
	var candidates []ast.Type
	for _, part := range []ast.Expr{expr.Start, expr.Stop, expr.Step} {
		if part == nil {
			continue
		}
		src, ok := g.staticExprSourceType(part)
		if !ok || src == nil {
			continue
		}
		candidates = append(candidates, src)
	}
	if len(candidates) > 0 {
		elem = candidates[0]
		for _, candidate := range candidates[1:] {
			if sameSourceType(elem, candidate) {
				continue
			}
			elem = &ast.NamedType{Path: []string{"Int"}}
			break
		}
	}
	return &ast.NamedType{Path: []string{"Range"}, Args: []ast.Type{elem}}, true
}

type builtinRangeField struct {
	index      int
	typ        string
	sourceType ast.Type
}

func (g *generator) builtinRangeFieldInfo(sourceType ast.Type, name string) (builtinRangeField, bool) {
	if sourceType == nil {
		return builtinRangeField{}, false
	}
	resolved, err := llvmResolveAliasType(sourceType, g.typeEnv(), map[string]bool{})
	if err != nil {
		return builtinRangeField{}, false
	}
	named, ok := resolved.(*ast.NamedType)
	if !ok || len(named.Path) != 1 || named.Path[0] != "Range" || len(named.Args) != 1 {
		return builtinRangeField{}, false
	}
	info, ok := builtinRangeTypeFromAST(named, g.typeEnv())
	if !ok {
		return builtinRangeField{}, false
	}
	switch name {
	case "start":
		return builtinRangeField{index: 0, typ: info.elemTyp, sourceType: named.Args[0]}, true
	case "stop":
		return builtinRangeField{index: 1, typ: info.elemTyp, sourceType: named.Args[0]}, true
	case "hasStart":
		return builtinRangeField{index: 2, typ: "i1", sourceType: &ast.NamedType{Path: []string{"Bool"}}}, true
	case "hasStop":
		return builtinRangeField{index: 3, typ: "i1", sourceType: &ast.NamedType{Path: []string{"Bool"}}}, true
	case "inclusive":
		return builtinRangeField{index: 4, typ: "i1", sourceType: &ast.NamedType{Path: []string{"Bool"}}}, true
	default:
		return builtinRangeField{}, false
	}
}

func fnSourceTypeFromSignature(sig *fnSig) ast.Type {
	if sig == nil {
		return nil
	}
	params := make([]ast.Type, 0, len(sig.params))
	for _, p := range sig.params {
		if p.sourceType == nil {
			return nil
		}
		params = append(params, p.sourceType)
	}
	if sig.ret != "void" && sig.returnSourceType == nil {
		return nil
	}
	return &ast.FnType{
		Params:     params,
		ReturnType: sig.returnSourceType,
	}
}

func (g *generator) staticTupleLiteralSourceType(expr *ast.TupleExpr) (ast.Type, bool) {
	if expr == nil {
		return nil, false
	}
	elems := make([]ast.Type, 0, len(expr.Elems))
	for _, elem := range expr.Elems {
		src, ok := g.staticExprSourceType(elem)
		if !ok {
			return nil, false
		}
		elems = append(elems, src)
	}
	return &ast.TupleType{Elems: elems}, true
}

func (g *generator) staticListLiteralSourceType(expr *ast.ListExpr) (ast.Type, bool) {
	if expr == nil || len(expr.Elems) == 0 {
		return nil, false
	}
	elemSource, ok := g.staticExprSourceType(expr.Elems[0])
	if !ok {
		return nil, false
	}
	for _, elem := range expr.Elems[1:] {
		src, ok := g.staticExprSourceType(elem)
		if !ok || !sameSourceType(elemSource, src) {
			return nil, false
		}
	}
	return &ast.NamedType{Path: []string{"List"}, Args: []ast.Type{elemSource}}, true
}

func (g *generator) staticMapLiteralSourceType(expr *ast.MapExpr) (ast.Type, bool) {
	if expr == nil || len(expr.Entries) == 0 {
		return nil, false
	}
	first := expr.Entries[0]
	keySource, ok := g.staticExprSourceType(first.Key)
	if !ok {
		return nil, false
	}
	valueSource, ok := g.staticExprSourceType(first.Value)
	if !ok {
		return nil, false
	}
	for _, entry := range expr.Entries[1:] {
		keySrc, ok := g.staticExprSourceType(entry.Key)
		if !ok || !sameSourceType(keySource, keySrc) {
			return nil, false
		}
		valSrc, ok := g.staticExprSourceType(entry.Value)
		if !ok || !sameSourceType(valueSource, valSrc) {
			return nil, false
		}
	}
	return &ast.NamedType{Path: []string{"Map"}, Args: []ast.Type{keySource, valueSource}}, true
}

// staticCollectionMethodSourceType reconstructs source-level return
// types for list/map/set intrinsic methods so local binds and nested
// container expressions can keep element metadata without every emit
// site assigning sourceType manually.
func (g *generator) staticCollectionMethodSourceType(call *ast.CallExpr) (ast.Type, bool) {
	if field, _, _, found := g.listMethodInfo(call); found {
		baseSource, ok := g.staticExprSourceType(field.X)
		if !ok {
			return nil, false
		}
		resolved, err := llvmResolveAliasType(baseSource, g.typeEnv(), map[string]bool{})
		if err != nil {
			return nil, false
		}
		named, ok := resolved.(*ast.NamedType)
		if !ok || len(named.Path) != 1 || named.Path[0] != "List" || len(named.Args) != 1 {
			return nil, false
		}
		switch field.Name {
		case "len":
			return &ast.NamedType{Path: []string{"Int"}}, true
		case "isEmpty":
			return &ast.NamedType{Path: []string{"Bool"}}, true
		case "sorted":
			return baseSource, true
		case "toSet":
			return &ast.NamedType{Path: []string{"Set"}, Args: []ast.Type{named.Args[0]}}, true
		}
	}
	if field, _, _, _, found := g.mapMethodInfo(call); found {
		baseSource, ok := g.staticExprSourceType(field.X)
		if !ok {
			return nil, false
		}
		resolved, err := llvmResolveAliasType(baseSource, g.typeEnv(), map[string]bool{})
		if err != nil {
			return nil, false
		}
		named, ok := resolved.(*ast.NamedType)
		if !ok || len(named.Path) != 1 || named.Path[0] != "Map" || len(named.Args) != 2 {
			return nil, false
		}
		keyAST := named.Args[0]
		valAST := named.Args[1]
		switch field.Name {
		case "len":
			return &ast.NamedType{Path: []string{"Int"}}, true
		case "isEmpty", "containsKey":
			return &ast.NamedType{Path: []string{"Bool"}}, true
		case "get":
			return &ast.OptionalType{Inner: valAST}, true
		case "getOr", "getOrInsert", "getOrInsertWith":
			return valAST, true
		case "keys":
			return &ast.NamedType{Path: []string{"List"}, Args: []ast.Type{keyAST}}, true
		case "mergeWith":
			return baseSource, true
		case "mapValues":
			if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Value == nil {
				return nil, false
			}
			callbackSource, ok := g.staticExprSourceType(call.Args[0].Value)
			if !ok {
				return nil, false
			}
			resolvedCallback, err := llvmResolveAliasType(callbackSource, g.typeEnv(), map[string]bool{})
			if err != nil {
				return nil, false
			}
			ft, ok := resolvedCallback.(*ast.FnType)
			if !ok || ft.ReturnType == nil {
				return nil, false
			}
			return &ast.NamedType{Path: []string{"Map"}, Args: []ast.Type{keyAST, ft.ReturnType}}, true
		}
	}
	if field, _, _, found := g.setMethodInfo(call); found {
		baseSource, ok := g.staticExprSourceType(field.X)
		if !ok {
			return nil, false
		}
		resolved, err := llvmResolveAliasType(baseSource, g.typeEnv(), map[string]bool{})
		if err != nil {
			return nil, false
		}
		named, ok := resolved.(*ast.NamedType)
		if !ok || len(named.Path) != 1 || named.Path[0] != "Set" || len(named.Args) != 1 {
			return nil, false
		}
		switch field.Name {
		case "len":
			return &ast.NamedType{Path: []string{"Int"}}, true
		case "isEmpty", "contains", "remove":
			return &ast.NamedType{Path: []string{"Bool"}}, true
		case "toList":
			return &ast.NamedType{Path: []string{"List"}, Args: []ast.Type{named.Args[0]}}, true
		}
	}
	return nil, false
}

func (g *generator) staticExprInfo(expr ast.Expr) (value, bool) {
	switch e := expr.(type) {
	case *ast.IntLit:
		return value{typ: "i64"}, true
	case *ast.FloatLit:
		return value{typ: "double"}, true
	case *ast.BoolLit:
		return value{typ: "i1"}, true
	case *ast.CharLit:
		return value{typ: "i32"}, true
	case *ast.ByteLit:
		return value{typ: "i8"}, true
	case *ast.StringLit:
		return value{typ: "ptr"}, true
	case *ast.Ident:
		if v, ok := g.lookupLocal(e.Name); ok {
			return v, true
		}
		if v, ok := g.lookupGlobal(e.Name); ok {
			return v, true
		}
	case *ast.ParenExpr:
		return g.staticExprInfo(e.X)
	case *ast.UnaryExpr:
		inner, ok := g.staticExprInfo(e.X)
		if !ok {
			return value{}, false
		}
		if llvmIsCompareOp(e.Op.String()) {
			return value{typ: "i1"}, true
		}
		return value{typ: inner.typ}, true
	case *ast.BinaryExpr:
		// Compare/logical ops fold to i1; arithmetic keeps the operand
		// type so Char/Byte conversion dispatch can see through e.g.
		// `'0'.toInt() + n` as i64.
		if llvmIsCompareOp(e.Op.String()) {
			return value{typ: "i1"}, true
		}
		leftInfo, lok := g.staticExprInfo(e.Left)
		rightInfo, rok := g.staticExprInfo(e.Right)
		if lok && rok && leftInfo.typ == rightInfo.typ && leftInfo.typ != "" {
			return value{typ: leftInfo.typ}, true
		}
		if lok && leftInfo.typ != "" {
			return value{typ: leftInfo.typ}, true
		}
		if rok && rightInfo.typ != "" {
			return value{typ: rightInfo.typ}, true
		}
		return value{}, false
	case *ast.TupleExpr:
		elemTypes := make([]string, 0, len(e.Elems))
		for _, elem := range e.Elems {
			info, ok := g.staticExprInfo(elem)
			if !ok {
				return value{}, false
			}
			elemTypes = append(elemTypes, info.typ)
		}
		return value{typ: llvmTupleTypeName(elemTypes)}, true
	case *ast.ListExpr:
		if elemTyp, elemString, ok := g.staticListLiteralElementInfo(e); ok {
			return value{typ: "ptr", gcManaged: true, listElemTyp: elemTyp, listElemString: elemString}, true
		}
	case *ast.MapExpr:
		return value{}, false
	case *ast.RangeExpr:
		sourceType, ok := g.staticRangeExprSourceType(e)
		if !ok {
			return value{}, false
		}
		info, ok := builtinRangeTypeFromAST(sourceType, g.typeEnv())
		if !ok {
			return value{}, false
		}
		return value{typ: info.typ, sourceType: sourceType}, true
	case *ast.CallExpr:
		if out, ok := g.staticStringMethodResult(e); ok {
			return out, true
		}
		if out, ok := g.staticBytesMethodResult(e); ok {
			return out, true
		}
		if out, ok := g.staticBytesNamespaceCallResult(e); ok {
			return out, true
		}
		if out, ok := g.staticCharByteConversionResult(e); ok {
			return out, true
		}
		if out, ok := g.staticCharPredicateResult(e); ok {
			return out, true
		}
		if out, found, ok := g.staticCollectionMethodResult(e); found {
			return out, ok
		}
		if id, ok := e.Fn.(*ast.Ident); ok {
			if sig := g.functions[id.Name]; sig != nil && sig.ret != "" && sig.ret != "void" {
				return value{
					typ:            sig.ret,
					listElemTyp:    sig.retListElemTyp,
					listElemString: sig.retListString,
					mapKeyTyp:      sig.retMapKeyTyp,
					mapValueTyp:    sig.retMapValueTyp,
					mapKeyString:   sig.retMapKeyString,
					setElemTyp:     sig.retSetElemTyp,
					setElemString:  sig.retSetElemString,
					gcManaged:      sig.retListElemTyp != "" || sig.retMapKeyTyp != "" || sig.retSetElemTyp != "",
				}, true
			}
		}
		if field, ok := e.Fn.(*ast.FieldExpr); ok && !field.IsOptional {
			if baseInfo, ok := g.staticExprInfo(field.X); ok {
				if methods := g.methods[baseInfo.typ]; methods != nil {
					if sig := methods[field.Name]; sig != nil && sig.ret != "" && sig.ret != "void" {
						return value{
							typ:            sig.ret,
							listElemTyp:    sig.retListElemTyp,
							listElemString: sig.retListString,
							mapKeyTyp:      sig.retMapKeyTyp,
							mapValueTyp:    sig.retMapValueTyp,
							mapKeyString:   sig.retMapKeyString,
							setElemTyp:     sig.retSetElemTyp,
							setElemString:  sig.retSetElemString,
							gcManaged:      sig.retListElemTyp != "" || sig.retMapKeyTyp != "" || sig.retSetElemTyp != "",
						}, true
					}
				}
			}
		}
		if fn, found, err := g.runtimeFFICallTarget(e); found && err == nil && fn.ret != "" && fn.ret != "void" {
			return value{typ: fn.ret, listElemTyp: fn.listElemTyp, gcManaged: fn.listElemTyp != ""}, true
		}
		if out, ok := g.stdBytesCallStaticResult(e); ok {
			return out, true
		}
		if out, ok := g.stdStringsCallStaticResult(e); ok {
			return out, true
		}
		if out, ok := g.stdEnvCallStaticResult(e); ok {
			return out, true
		}
		if out, ok := g.stdCryptoCallStaticResult(e); ok {
			return out, true
		}
		if out, ok := g.stdOsCallStaticResult(e); ok {
			return out, true
		}
	case *ast.FieldExpr:
		if e.IsOptional {
			return value{}, false
		}
		if baseSource, ok := g.staticExprSourceType(e.X); ok {
			if field, ok := g.builtinRangeFieldInfo(baseSource, e.Name); ok {
				out := value{typ: field.typ, sourceType: field.sourceType}
				return g.decorateStaticValueFromSourceType(out, e), true
			}
		}
		baseInfo, ok := g.staticExprInfo(e.X)
		if !ok {
			return value{}, false
		}
		if info := g.structsByType[baseInfo.typ]; info != nil {
			if field, ok := info.byName[e.Name]; ok {
				return value{
					typ:            field.typ,
					listElemTyp:    field.listElemTyp,
					listElemString: field.listElemString,
					mapKeyTyp:      field.mapKeyTyp,
					mapValueTyp:    field.mapValueTyp,
					mapKeyString:   field.mapKeyString,
					setElemTyp:     field.setElemTyp,
					setElemString:  field.setElemString,
					gcManaged:      field.listElemTyp != "" || field.mapKeyTyp != "" || field.setElemTyp != "",
				}, true
			}
		}
	case *ast.IndexExpr:
		if baseInfo, ok := g.staticExprInfo(e.X); ok {
			switch {
			case baseInfo.listElemTyp != "":
				out := value{typ: baseInfo.listElemTyp, gcManaged: baseInfo.listElemTyp == "ptr", rootPaths: g.rootPathsForType(baseInfo.listElemTyp)}
				return g.decorateStaticValueFromSourceType(out, e), true
			case baseInfo.mapKeyTyp != "":
				out := value{typ: baseInfo.mapValueTyp, gcManaged: baseInfo.mapValueTyp == "ptr", rootPaths: g.rootPathsForType(baseInfo.mapValueTyp)}
				return g.decorateStaticValueFromSourceType(out, e), true
			}
		}
	}
	return value{}, false
}

func (g *generator) decorateStaticValueFromSourceType(out value, expr ast.Expr) value {
	sourceType, ok := g.staticExprSourceType(expr)
	if !ok {
		return out
	}
	if meta, err := containerMetadataFromSourceType(sourceType, g.typeEnv()); err == nil {
		meta.applyToValue(&out)
	}
	return out
}

func tupleElementSourceType(sourceType ast.Type, index int, env typeEnv) (ast.Type, bool) {
	resolved, err := llvmResolveAliasType(sourceType, env, map[string]bool{})
	if err != nil {
		return nil, false
	}
	tuple, ok := resolved.(*ast.TupleType)
	if !ok || index < 0 || index >= len(tuple.Elems) {
		return nil, false
	}
	return tuple.Elems[index], true
}

func (g *generator) staticListLiteralElementInfo(expr *ast.ListExpr) (string, bool, bool) {
	if expr == nil || len(expr.Elems) == 0 {
		return "", false, false
	}
	elemInfo, ok := g.staticExprInfo(expr.Elems[0])
	if !ok {
		return "", false, false
	}
	elemTyp := elemInfo.typ
	elemString := g.staticExprIsString(expr.Elems[0])
	for _, elem := range expr.Elems[1:] {
		info, ok := g.staticExprInfo(elem)
		if !ok || info.typ != elemTyp {
			return "", false, false
		}
		if g.staticExprIsString(elem) != elemString {
			return "", false, false
		}
	}
	return elemTyp, elemString, true
}

func (g *generator) staticExprIsString(expr ast.Expr) bool {
	sourceType, ok := g.staticExprSourceType(expr)
	if !ok {
		return false
	}
	resolved, err := llvmResolveAliasType(sourceType, g.typeEnv(), map[string]bool{})
	if err != nil {
		return false
	}
	return llvmNamedTypeIsString(resolved)
}

// iterableElemSourceType returns the element source type of a list-shaped
// iterable, resolving aliases. The fallback reaches into structsByType so
// nested `for` loops over List-typed struct fields still recover the element
// source type when the base binding (itself an iter var) has no sourceType.
func (g *generator) iterableElemSourceType(iter ast.Expr) (ast.Type, bool) {
	src, ok := g.staticExprSourceType(iter)
	if !ok {
		src = g.structFieldListSourceType(iter)
		if src == nil {
			return nil, false
		}
	}
	return llvmListElementSourceType(src, g.typeEnv())
}

func llvmListElementSourceType(sourceType ast.Type, env typeEnv) (ast.Type, bool) {
	resolved, err := llvmResolveAliasType(sourceType, env, map[string]bool{})
	if err != nil {
		return nil, false
	}
	named, ok := resolved.(*ast.NamedType)
	if !ok || len(named.Path) != 1 || named.Path[0] != "List" || len(named.Args) != 1 {
		return nil, false
	}
	return named.Args[0], true
}

func (g *generator) iterableMapSourceTypes(iter ast.Expr) (ast.Type, ast.Type, bool) {
	src, ok := g.staticExprSourceType(iter)
	if !ok {
		return nil, nil, false
	}
	return llvmMapSourceTypes(src, g.typeEnv())
}

func llvmMapSourceTypes(sourceType ast.Type, env typeEnv) (ast.Type, ast.Type, bool) {
	resolved, err := llvmResolveAliasType(sourceType, env, map[string]bool{})
	if err != nil {
		return nil, nil, false
	}
	named, ok := resolved.(*ast.NamedType)
	if !ok || len(named.Path) != 1 || named.Path[0] != "Map" || len(named.Args) != 2 {
		return nil, nil, false
	}
	return named.Args[0], named.Args[1], true
}

func llvmSetElementSourceType(sourceType ast.Type, env typeEnv) (ast.Type, bool) {
	resolved, err := llvmResolveAliasType(sourceType, env, map[string]bool{})
	if err != nil {
		return nil, false
	}
	named, ok := resolved.(*ast.NamedType)
	if !ok || len(named.Path) != 1 || named.Path[0] != "Set" || len(named.Args) != 1 {
		return nil, false
	}
	return named.Args[0], true
}

func (g *generator) structFieldListSourceType(expr ast.Expr) ast.Type {
	field, ok := expr.(*ast.FieldExpr)
	if !ok || field.IsOptional {
		return nil
	}
	baseInfo, ok := g.staticExprInfo(field.X)
	if !ok {
		return nil
	}
	info := g.structsByType[baseInfo.typ]
	if info == nil {
		return nil
	}
	f, ok := info.byName[field.Name]
	if !ok {
		return nil
	}
	return f.sourceType
}

func (g *generator) setElemSourceType(expr ast.Expr) (ast.Type, bool) {
	src, ok := g.staticExprSourceType(expr)
	if !ok {
		return nil, false
	}
	return llvmSetElementSourceType(src, g.typeEnv())
}

func (g *generator) staticExprListElemIsBytes(expr ast.Expr) bool {
	sourceType, ok := g.staticExprSourceType(expr)
	if ok {
		resolved, err := llvmResolveAliasType(sourceType, g.typeEnv(), map[string]bool{})
		if err == nil {
			named, ok := resolved.(*ast.NamedType)
			if ok && len(named.Path) == 1 && named.Path[0] == "List" && len(named.Args) == 1 {
				elemResolved, err := llvmResolveAliasType(named.Args[0], g.typeEnv(), map[string]bool{})
				if err == nil && llvmNamedTypeIsBytes(elemResolved) {
					return true
				}
			}
		}
	}
	if list, ok := expr.(*ast.ListExpr); ok && len(list.Elems) != 0 {
		for _, elem := range list.Elems {
			elemSource, ok := g.staticExprSourceType(elem)
			if !ok {
				return false
			}
			elemResolved, err := llvmResolveAliasType(elemSource, g.typeEnv(), map[string]bool{})
			if err != nil || !llvmNamedTypeIsBytes(elemResolved) {
				return false
			}
		}
		return true
	}
	return false
}

func (g *generator) staticExprListElemIsByte(expr ast.Expr) bool {
	sourceType, ok := g.staticExprSourceType(expr)
	if ok {
		resolved, err := llvmResolveAliasType(sourceType, g.typeEnv(), map[string]bool{})
		if err == nil {
			named, ok := resolved.(*ast.NamedType)
			if ok && len(named.Path) == 1 && named.Path[0] == "List" && len(named.Args) == 1 {
				elemResolved, err := llvmResolveAliasType(named.Args[0], g.typeEnv(), map[string]bool{})
				if err == nil && llvmNamedTypeIsByte(elemResolved) {
					return true
				}
			}
		}
	}
	info, ok := g.staticExprInfo(expr)
	if !ok {
		return false
	}
	return info.typ == "ptr" && info.listElemTyp == "i8" && !info.listElemString
}

// staticStdStringsCallSourceType recovers the source-level return type
// for `strings.<method>(...)` alias-qualified calls (`use std.strings
// as strings`). Keeps the dispatch layer (`stdStringsCallStaticResult`)
// and the source-type layer in sync so nested forms like
// `strings.join([strings.join(parts, ", "), ...], "")` don't pinball
// between "String" and "unknown ptr-backed" when a list literal asks
// `isString?` about the inner call.
func (g *generator) staticStdStringsCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	if call == nil || len(g.stdStringsAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdStringsAliases[alias.Name] {
		return nil, false
	}
	stringT := &ast.NamedType{Path: []string{"String"}}
	switch field.Name {
	case "compare", "count":
		return &ast.NamedType{Path: []string{"Int"}}, true
	case "indexOf":
		return &ast.OptionalType{Inner: &ast.NamedType{Path: []string{"Int"}}}, true
	case "toInt":
		return stringToIntResultSourceType(), true
	case "toFloat":
		return stringToFloatResultSourceType(), true
	case "toBytes":
		return &ast.NamedType{Path: []string{"Bytes"}}, true
	case "contains", "hasPrefix", "hasSuffix":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "concat", "join", "repeat", "replace", "replaceAll", "slice", "trim", "trimSpace", "trimStart", "trimEnd", "trimPrefix", "trimSuffix", "toUpper", "toLower":
		return stringT, true
	case "split", "splitN", "fields":
		return &ast.NamedType{Path: []string{"List"}, Args: []ast.Type{stringT}}, true
	}
	return nil, false
}

func (g *generator) staticStdBytesCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	if call == nil || len(g.stdBytesAliases) == 0 {
		return nil, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok {
		return nil, false
	}
	alias, ok := field.X.(*ast.Ident)
	if !ok || !g.stdBytesAliases[alias.Name] {
		return nil, false
	}
	switch field.Name {
	case "len":
		return &ast.NamedType{Path: []string{"Int"}}, true
	case "isEmpty":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "contains", "startsWith", "endsWith":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "indexOf", "lastIndexOf":
		return &ast.OptionalType{Inner: &ast.NamedType{Path: []string{"Int"}}}, true
	case "split":
		return bytesListSourceType(), true
	case "get":
		return &ast.OptionalType{Inner: &ast.NamedType{Path: []string{"Byte"}}}, true
	case "fromString", "from", "concat", "join", "repeat", "replace", "replaceAll", "trimLeft", "trimRight", "trim", "trimSpace", "toUpper", "toLower", "slice":
		return &ast.NamedType{Path: []string{"Bytes"}}, true
	case "toHex":
		return &ast.NamedType{Path: []string{"String"}}, true
	case "fromHex":
		return bytesFromHexResultSourceType(), true
	case "toString":
		return bytesToStringResultSourceType(), true
	}
	return nil, false
}

func (g *generator) staticBytesNamespaceCallSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.bytesNamespaceCallInfo(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "len":
		return &ast.NamedType{Path: []string{"Int"}}, true
	case "isEmpty":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "contains", "startsWith", "endsWith":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "indexOf", "lastIndexOf":
		return &ast.OptionalType{Inner: &ast.NamedType{Path: []string{"Int"}}}, true
	case "split":
		return bytesListSourceType(), true
	case "get":
		return &ast.OptionalType{Inner: &ast.NamedType{Path: []string{"Byte"}}}, true
	case "from", "concat", "join", "repeat", "replace", "replaceAll", "trimLeft", "trimRight", "trim", "trimSpace", "toUpper", "toLower", "slice":
		return &ast.NamedType{Path: []string{"Bytes"}}, true
	case "toHex":
		return &ast.NamedType{Path: []string{"String"}}, true
	case "fromHex":
		return bytesFromHexResultSourceType(), true
	case "toString":
		return bytesToStringResultSourceType(), true
	}
	return nil, false
}

// staticMapMethodSourceType recovers the source-level return type of
// a Map intrinsic method call so downstream passes (notably the
// `??` coalesce emitter that demands an Option<V> source type)
// can reason about `self.get(k) ?? default` inside specialized
// Map method bodies. Returns `(nil, false)` unless the call is a
// FieldExpr on a receiver with mapKey/mapValue metadata.
//
// Method → source-type map:
//
//	len              → Int
//	isEmpty          → Bool
//	containsKey(K)   → Bool
//	get(K)           → V?           (Option<V>, what feeds `??`)
//	keys()           → List<K>
//
// Other map methods (update / retainIf / mergeWith /
// mapValues / …) are either bodied (their source type flows from the
// body) or not yet exercised at this layer.
func (g *generator) staticMapMethodSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, keyTyp, valTyp, _, found := g.mapMethodInfo(call)
	if !found {
		return nil, false
	}
	_ = keyTyp
	_ = valTyp
	baseSrc, ok := g.staticExprSourceType(field.X)
	if !ok {
		return nil, false
	}
	resolved, err := llvmResolveAliasType(baseSrc, g.typeEnv(), map[string]bool{})
	if err != nil {
		return nil, false
	}
	named, ok := resolved.(*ast.NamedType)
	if !ok || len(named.Path) != 1 || named.Path[0] != "Map" || len(named.Args) != 2 {
		return nil, false
	}
	keyAST := named.Args[0]
	valAST := named.Args[1]
	switch field.Name {
	case "len":
		return &ast.NamedType{Path: []string{"Int"}}, true
	case "isEmpty", "containsKey":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "get":
		// V? — wraps the Map's value type.
		return &ast.OptionalType{Inner: valAST}, true
	case "getOr", "getOrInsert", "getOrInsertWith":
		return valAST, true
	case "keys":
		return &ast.NamedType{Path: []string{"List"}, Args: []ast.Type{keyAST}}, true
	}
	return nil, false
}

// staticListMethodSourceType recovers the source-level return type of
// a List intrinsic method call. The most important consumer is the
// Optional match-as-expression dispatch, which needs to see `T?` for
// `xs.get(i)` so a downstream `match xs.get(0) { Some(x) -> a, None
// -> b }` routes to emitOptionalMatchExprValue rather than walling
// with "match scrutinee type ptr, want enum tag".
//
// Method → source-type map:
//
//	len        → Int
//	isEmpty    → Bool
//	get(Int)   → T?
func (g *generator) staticListMethodSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, _, _, found := g.listMethodInfo(call)
	if !found {
		return nil, false
	}
	baseSrc, ok := g.staticExprSourceType(field.X)
	if !ok {
		return nil, false
	}
	resolved, err := llvmResolveAliasType(baseSrc, g.typeEnv(), map[string]bool{})
	if err != nil {
		return nil, false
	}
	named, ok := resolved.(*ast.NamedType)
	if !ok || len(named.Path) != 1 || named.Path[0] != "List" || len(named.Args) != 1 {
		return nil, false
	}
	elemAST := named.Args[0]
	switch field.Name {
	case "len":
		return &ast.NamedType{Path: []string{"Int"}}, true
	case "isEmpty":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "get":
		return &ast.OptionalType{Inner: elemAST}, true
	}
	return nil, false
}

func (g *generator) staticStringMethodSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.stringMethodInfo(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "len", "charCount":
		return &ast.NamedType{Path: []string{"Int"}}, true
	case "isEmpty", "startsWith", "endsWith", "contains":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "indexOf":
		return &ast.OptionalType{Inner: &ast.NamedType{Path: []string{"Int"}}}, true
	case "get":
		return &ast.OptionalType{Inner: &ast.NamedType{Path: []string{"Byte"}}}, true
	case "split", "lines":
		return &ast.NamedType{
			Path: []string{"List"},
			Args: []ast.Type{&ast.NamedType{Path: []string{"String"}}},
		}, true
	case "toInt":
		return stringToIntResultSourceType(), true
	case "toFloat":
		return stringToFloatResultSourceType(), true
	case "toBytes":
		return &ast.NamedType{Path: []string{"Bytes"}}, true
	case "trim", "trimStart", "trimEnd", "trimPrefix", "trimSuffix", "toString", "join", "replace", "repeat", "substring", "slice", "toUpper", "toLower":
		return &ast.NamedType{Path: []string{"String"}}, true
	case "chars":
		return &ast.NamedType{
			Path: []string{"List"},
			Args: []ast.Type{&ast.NamedType{Path: []string{"Char"}}},
		}, true
	case "bytes":
		return &ast.NamedType{
			Path: []string{"List"},
			Args: []ast.Type{&ast.NamedType{Path: []string{"Byte"}}},
		}, true
	default:
		return nil, false
	}
}

// staticCharByteConversionResult mirrors emitCharByteConversionCall at
// the type-inference layer so `.toInt()` / `.toChar()` on a Char/Byte/Int
// receiver resolves statically — which in turn lets nested forms like
// `('0'.toInt() + n).toChar()` type-check without materialising each
// intermediate value.
func (g *generator) staticCharByteConversionResult(call *ast.CallExpr) (value, bool) {
	if call == nil || len(call.Args) != 0 {
		return value{}, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional || field.X == nil {
		return value{}, false
	}
	baseInfo, ok := g.staticExprInfo(field.X)
	if !ok {
		return value{}, false
	}
	switch {
	case field.Name == "toInt" && (baseInfo.typ == "i32" || baseInfo.typ == "i8"):
		return value{typ: "i64"}, true
	case field.Name == "toChar" && baseInfo.typ == "i64":
		return value{typ: "i32"}, true
	case field.Name == "toChar" && baseInfo.typ == "i8":
		// Byte → Char is infallible widening (every u8 is a valid
		// ASCII-range codepoint). Toolchain uses `b.toChar().toString()`
		// to format a Byte as a single-char UTF-8 string.
		return value{typ: "i32"}, true
	case field.Name == "toByte" && baseInfo.typ == "i64":
		// Int → Byte is spec'd as `Result<Byte, Error>`, but the
		// toolchain consistently treats it as an infallible truncation
		// at call sites (`'\\'.toInt().toByte()`, `0x20.toByte()`).
		// Mirror that here so the narrow static-type path resolves the
		// receiver to i8 and the eq/<= comparisons that follow type
		// through emitBinary without an extra unwrap.
		return value{typ: "i8"}, true
	}
	return value{}, false
}

// staticCharPredicateResult mirrors staticCharByteConversionResult but
// for the Char predicate / case-conversion methods. Without this hook
// the `c.isDigit()` / `c.toUpper()` calls inside an injected stdlib
// body don't get a static return type and downstream type-driven
// dispatch (e.g. interpolation deciding whether to call
// emitRuntimeBoolToString vs emitRuntimeCharToString) can't pick the
// right path.
func (g *generator) staticCharPredicateResult(call *ast.CallExpr) (value, bool) {
	if call == nil || len(call.Args) != 0 {
		return value{}, false
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional || field.X == nil {
		return value{}, false
	}
	baseInfo, ok := g.staticExprInfo(field.X)
	if !ok || baseInfo.typ != "i32" {
		return value{}, false
	}
	switch field.Name {
	case "isDigit", "isAlpha", "isAlphanumeric", "isWhitespace", "isUpper", "isLower":
		return value{typ: "i1"}, true
	case "toUpper", "toLower":
		return value{typ: "i32"}, true
	}
	return value{}, false
}

func (g *generator) staticStringMethodResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.stringMethodInfo(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "len", "charCount":
		return value{typ: "i64"}, true
	case "isEmpty", "startsWith", "endsWith", "contains":
		return value{typ: "i1"}, true
	case "indexOf":
		return value{
			typ:       "ptr",
			gcManaged: true,
			sourceType: &ast.OptionalType{
				Inner: &ast.NamedType{Path: []string{"Int"}},
			},
		}, true
	case "get":
		return value{
			typ:       "ptr",
			gcManaged: true,
			sourceType: &ast.OptionalType{
				Inner: &ast.NamedType{Path: []string{"Byte"}},
			},
		}, true
	case "split", "lines":
		return value{typ: "ptr", gcManaged: true, listElemTyp: "ptr", listElemString: true}, true
	case "toInt":
		if info, ok := builtinResultTypeFromAST(stringToIntResultSourceType(), g.typeEnv()); ok {
			return value{typ: info.typ, sourceType: stringToIntResultSourceType(), rootPaths: g.rootPathsForType(info.typ)}, true
		}
		return value{}, false
	case "toFloat":
		if info, ok := builtinResultTypeFromAST(stringToFloatResultSourceType(), g.typeEnv()); ok {
			return value{typ: info.typ, sourceType: stringToFloatResultSourceType(), rootPaths: g.rootPathsForType(info.typ)}, true
		}
		return value{}, false
	case "toBytes":
		return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"Bytes"}}}, true
	case "trim", "trimStart", "trimEnd", "trimPrefix", "trimSuffix", "toString", "join", "replace", "repeat", "substring", "slice", "toUpper", "toLower":
		return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"String"}}}, true
	case "chars":
		return value{typ: "ptr", gcManaged: true, listElemTyp: "i32"}, true
	case "bytes":
		return value{typ: "ptr", gcManaged: true, listElemTyp: "i8"}, true
	default:
		return value{}, false
	}
}

func (g *generator) stringMethodInfo(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional || field.X == nil {
		return nil, false
	}
	sourceType, ok := g.staticExprSourceType(field.X)
	if !ok {
		return nil, false
	}
	resolved, err := llvmResolveAliasType(sourceType, g.typeEnv(), map[string]bool{})
	if err != nil || !llvmNamedTypeIsString(resolved) {
		return nil, false
	}
	switch field.Name {
	case "len", "charCount", "isEmpty", "startsWith", "endsWith", "contains",
		"indexOf",
		"get",
		"trimStart", "trimEnd", "trimPrefix", "trimSuffix",
		"split", "lines", "join", "trim", "replace", "repeat", "substring", "slice", "toUpper", "toLower", "toInt", "toFloat", "toString", "chars", "bytes", "toBytes":
		return field, true
	default:
		return nil, false
	}
}

func (g *generator) staticBytesMethodSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.bytesMethodInfo(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "len":
		return &ast.NamedType{Path: []string{"Int"}}, true
	case "isEmpty":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "contains", "startsWith", "endsWith":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "indexOf", "lastIndexOf":
		return &ast.OptionalType{Inner: &ast.NamedType{Path: []string{"Int"}}}, true
	case "get":
		return &ast.OptionalType{Inner: &ast.NamedType{Path: []string{"Byte"}}}, true
	case "split":
		return bytesListSourceType(), true
	case "concat", "join", "repeat", "replace", "replaceAll", "trimLeft", "trimRight", "trim", "trimSpace", "toUpper", "toLower", "slice":
		return &ast.NamedType{Path: []string{"Bytes"}}, true
	case "toHex":
		return &ast.NamedType{Path: []string{"String"}}, true
	case "toString":
		return bytesToStringResultSourceType(), true
	default:
		return nil, false
	}
}

func (g *generator) staticBytesMethodResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.bytesMethodInfo(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "len":
		return value{typ: "i64"}, true
	case "isEmpty":
		return value{typ: "i1"}, true
	case "contains", "startsWith", "endsWith":
		return value{typ: "i1"}, true
	case "indexOf", "lastIndexOf":
		return value{
			typ:       "ptr",
			gcManaged: true,
			sourceType: &ast.OptionalType{
				Inner: &ast.NamedType{Path: []string{"Int"}},
			},
		}, true
	case "get":
		return value{
			typ:       "ptr",
			gcManaged: true,
			sourceType: &ast.OptionalType{
				Inner: &ast.NamedType{Path: []string{"Byte"}},
			},
		}, true
	case "split":
		return value{typ: "ptr", gcManaged: true, listElemTyp: "ptr", sourceType: bytesListSourceType()}, true
	case "concat", "join", "repeat", "replace", "replaceAll", "trimLeft", "trimRight", "trim", "trimSpace", "toUpper", "toLower", "slice":
		return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"Bytes"}}}, true
	case "toHex":
		return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"String"}}}, true
	case "toString":
		if info, ok := builtinResultTypeFromAST(bytesToStringResultSourceType(), g.typeEnv()); ok {
			return value{typ: info.typ, sourceType: bytesToStringResultSourceType(), rootPaths: g.rootPathsForType(info.typ)}, true
		}
		return value{}, false
	default:
		return value{}, false
	}
}

func (g *generator) bytesMethodInfo(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional || field.X == nil {
		return nil, false
	}
	sourceType, ok := g.staticExprSourceType(field.X)
	if !ok {
		return nil, false
	}
	resolved, err := llvmResolveAliasType(sourceType, g.typeEnv(), map[string]bool{})
	if err != nil || !llvmNamedTypeIsBytes(resolved) {
		return nil, false
	}
	switch field.Name {
	case "len", "isEmpty", "get", "contains", "startsWith", "endsWith", "indexOf", "lastIndexOf", "split", "join", "concat", "repeat", "replace", "replaceAll", "trimLeft", "trimRight", "trim", "trimSpace", "toUpper", "toLower", "toHex", "slice", "toString":
		return field, true
	default:
		return nil, false
	}
}

func (g *generator) staticBytesNamespaceCallResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.bytesNamespaceCallInfo(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "len":
		return value{typ: "i64"}, true
	case "isEmpty":
		return value{typ: "i1"}, true
	case "contains", "startsWith", "endsWith":
		return value{typ: "i1"}, true
	case "indexOf", "lastIndexOf":
		return value{
			typ:       "ptr",
			gcManaged: true,
			sourceType: &ast.OptionalType{
				Inner: &ast.NamedType{Path: []string{"Int"}},
			},
		}, true
	case "get":
		return value{
			typ:       "ptr",
			gcManaged: true,
			sourceType: &ast.OptionalType{
				Inner: &ast.NamedType{Path: []string{"Byte"}},
			},
		}, true
	case "split":
		return value{typ: "ptr", gcManaged: true, listElemTyp: "ptr", sourceType: bytesListSourceType()}, true
	case "from", "concat", "join", "repeat", "replace", "replaceAll", "trimLeft", "trimRight", "trim", "trimSpace", "toUpper", "toLower", "slice":
		return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"Bytes"}}}, true
	case "toHex":
		return value{typ: "ptr", gcManaged: true, sourceType: &ast.NamedType{Path: []string{"String"}}}, true
	case "fromHex":
		if info, ok := builtinResultTypeFromAST(bytesFromHexResultSourceType(), g.typeEnv()); ok {
			return value{typ: info.typ, sourceType: bytesFromHexResultSourceType(), rootPaths: g.rootPathsForType(info.typ)}, true
		}
		return value{}, false
	case "toString":
		if info, ok := builtinResultTypeFromAST(bytesToStringResultSourceType(), g.typeEnv()); ok {
			return value{typ: info.typ, sourceType: bytesToStringResultSourceType(), rootPaths: g.rootPathsForType(info.typ)}, true
		}
		return value{}, false
	}
	return value{}, false
}

func (g *generator) bytesNamespaceCallInfo(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional || field.X == nil {
		return nil, false
	}
	owner, ok := field.X.(*ast.Ident)
	if !ok || owner.Name != "Bytes" {
		return nil, false
	}
	switch field.Name {
	case "from", "fromHex", "len", "isEmpty", "get", "contains", "startsWith", "endsWith", "indexOf", "lastIndexOf", "split", "join", "concat", "repeat", "replace", "replaceAll", "trimLeft", "trimRight", "trim", "trimSpace", "toUpper", "toLower", "toHex", "slice", "toString":
		return field, true
	default:
		return nil, false
	}
}

func (g *generator) staticCollectionMethodResult(call *ast.CallExpr) (value, bool, bool) {
	if field, elemTyp, elemString, found := g.listMethodInfo(call); found {
		switch field.Name {
		case "len":
			return value{typ: "i64"}, true, true
		case "isEmpty":
			return value{typ: "i1"}, true, true
		case "sorted":
			return value{typ: "ptr", gcManaged: true, listElemTyp: elemTyp, listElemString: elemString}, true, true
		case "toSet":
			return value{typ: "ptr", gcManaged: true, setElemTyp: elemTyp, setElemString: elemString}, true, true
		default:
			return value{}, true, false
		}
	}
	if field, keyTyp, _, keyString, found := g.mapMethodInfo(call); found {
		switch field.Name {
		case "len":
			return value{typ: "i64"}, true, true
		case "isEmpty":
			return value{typ: "i1"}, true, true
		case "containsKey":
			return value{typ: "i1"}, true, true
		case "keys":
			return value{typ: "ptr", gcManaged: true, listElemTyp: keyTyp, listElemString: keyString}, true, true
		case "remove":
			return value{typ: "ptr"}, true, true
		default:
			return value{}, true, false
		}
	}
	if field, elemTyp, elemString, found := g.setMethodInfo(call); found {
		switch field.Name {
		case "len":
			return value{typ: "i64"}, true, true
		case "isEmpty":
			return value{typ: "i1"}, true, true
		case "contains", "remove":
			return value{typ: "i1"}, true, true
		case "toList":
			return value{typ: "ptr", gcManaged: true, listElemTyp: elemTyp, listElemString: elemString}, true, true
		default:
			return value{}, true, false
		}
	}
	return value{}, false, false
}

// fieldExprOfCallFn returns the FieldExpr callee of `call`, peeking
// through a TurbofishExpr wrapper when present. The IR→AST bridge
// (`legacyMethodCallFromIR`) wraps `recv.method` in a TurbofishExpr
// whenever the IR MethodCall carries TypeArgs — which the checker
// records for *every* generic method call, including intrinsic ones
// like `list.push(1)` where the type arg is the receiver's own T.
// Dispatchers that used to match a bare FieldExpr would then fall
// through to the "unsupported call" path. Unwrapping the turbofish
// here makes the dispatch transparent to the bridge shape.
func fieldExprOfCallFn(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	if call == nil {
		return nil, false
	}
	switch fn := call.Fn.(type) {
	case *ast.FieldExpr:
		return fn, fn != nil
	case *ast.TurbofishExpr:
		if fn == nil {
			return nil, false
		}
		if fx, ok := fn.Base.(*ast.FieldExpr); ok {
			return fx, fx != nil
		}
	}
	return nil, false
}

func (g *generator) listMethodInfo(call *ast.CallExpr) (*ast.FieldExpr, string, bool, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, "", false, false
	}
	switch field.Name {
	case "len", "isEmpty", "pop", "push", "insert", "sorted", "toSet", "clear", "get":
	default:
		return nil, "", false, false
	}
	baseInfo, ok := g.staticExprInfo(field.X)
	if !ok || baseInfo.typ != "ptr" || baseInfo.listElemTyp == "" {
		return nil, "", false, false
	}
	return field, baseInfo.listElemTyp, baseInfo.listElemString, true
}

func (g *generator) mapMethodInfo(call *ast.CallExpr) (*ast.FieldExpr, string, string, bool, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, "", "", false, false
	}
	switch field.Name {
	case "containsKey", "insert", "remove", "keys", "len", "isEmpty", "get", "getOr", "getOrInsert", "getOrInsertWith", "update", "retainIf", "mergeWith", "mapValues", "clear":
	default:
		return nil, "", "", false, false
	}
	baseInfo, ok := g.staticExprInfo(field.X)
	if !ok || baseInfo.typ != "ptr" || baseInfo.mapKeyTyp == "" || baseInfo.mapValueTyp == "" {
		return nil, "", "", false, false
	}
	return field, baseInfo.mapKeyTyp, baseInfo.mapValueTyp, baseInfo.mapKeyString, true
}

func (g *generator) setMethodInfo(call *ast.CallExpr) (*ast.FieldExpr, string, bool, bool) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return nil, "", false, false
	}
	switch field.Name {
	case "len", "isEmpty", "contains", "insert", "remove", "toList":
	default:
		return nil, "", false, false
	}
	baseInfo, ok := g.staticExprInfo(field.X)
	if !ok || baseInfo.typ != "ptr" || baseInfo.setElemTyp == "" {
		return nil, "", false, false
	}
	return field, baseInfo.setElemTyp, baseInfo.setElemString, true
}

func llvmResolveAliasType(t ast.Type, env typeEnv, seen map[string]bool) (ast.Type, error) {
	named, ok := t.(*ast.NamedType)
	if !ok || len(named.Path) != 1 {
		return t, nil
	}
	alias := env.aliases[named.Path[0]]
	if alias == nil {
		return t, nil
	}
	if len(alias.decl.Generics) != 0 || len(named.Args) != 0 {
		return nil, unsupportedf("type-system", "generic type alias %q is not supported", alias.name)
	}
	if seen[alias.name] {
		return nil, unsupportedf("type-system", "cyclic type alias %q", alias.name)
	}
	seen[alias.name] = true
	defer delete(seen, alias.name)
	return llvmResolveAliasType(alias.decl.Target, env, seen)
}

func resolveAliasNamedTarget(name string, env typeEnv, seen map[string]bool) (string, bool, error) {
	alias := env.aliases[name]
	if alias == nil {
		return "", false, nil
	}
	if len(alias.decl.Generics) != 0 {
		return "", true, unsupportedf("type-system", "generic type alias %q is not supported", alias.name)
	}
	if seen[alias.name] {
		return "", true, unsupportedf("type-system", "cyclic type alias %q", alias.name)
	}
	target, ok := alias.decl.Target.(*ast.NamedType)
	if !ok || len(target.Path) != 1 || len(target.Args) != 0 {
		return "", true, nil
	}
	seen[alias.name] = true
	defer delete(seen, alias.name)
	if resolved, ok, err := resolveAliasNamedTarget(target.Path[0], env, seen); ok || err != nil {
		if err != nil {
			return "", true, err
		}
		return resolved, true, nil
	}
	return target.Path[0], true, nil
}

// llvmAggregatePathIndices joins a sequence of GEP path indices into the
// LLVM `i32 0, i32 N0, i32 N1, ...` form expected by GEP instructions
// that traverse nested aggregates. Delegates to the Osty-sourced
// `mirLlvmAggregatePathIndices` (`toolchain/mir_generator.osty`).
func llvmAggregatePathIndices(path []int) string {
	return mirLlvmAggregatePathIndices(path)
}

func llvmType(t ast.Type, env typeEnv) (string, error) {
	resolved, err := llvmResolveAliasType(t, env, map[string]bool{})
	if err != nil {
		return "", err
	}
	t = resolved
	switch tt := t.(type) {
	case *ast.NamedType:
		if info, ok := builtinRangeTypeFromAST(tt, env); ok {
			return info.typ, nil
		}
		if len(tt.Path) == 1 && tt.Path[0] == "Result" && len(tt.Args) == 2 {
			okTyp, err := llvmType(tt.Args[0], env)
			if err != nil {
				return "", err
			}
			errTyp, err := llvmType(tt.Args[1], env)
			if err != nil {
				return "", err
			}
			return llvmResultTypeName(okTyp, errTyp), nil
		}
		name := ""
		structType := ""
		enumType := ""
		if len(tt.Path) == 1 {
			name = tt.Path[0]
			if info := env.structs[name]; info != nil {
				structType = info.typ
			}
			if info := env.enums[name]; info != nil {
				enumType = info.typ
			}
			if env.interfaces[name] != nil {
				// Phase 6b: interface values carry a `{data, vtable}`
				// fat pointer laid out as `%osty.iface` (see
				// renderInterfaceVtables in generator.go).
				return "%osty.iface", nil
			}
		}
		if typ := llvmNamedType(name, len(tt.Path), len(tt.Args), structType, enumType); typ != "" {
			return typ, nil
		}
		return "", unsupportedf("type-system", "type %q", strings.Join(tt.Path, "."))
	case *ast.OptionalType, *ast.FnType:
		return "ptr", nil
	case *ast.TupleType:
		elemTypes := make([]string, 0, len(tt.Elems))
		for _, elem := range tt.Elems {
			elemTyp, err := llvmType(elem, env)
			if err != nil {
				return "", err
			}
			elemTypes = append(elemTypes, elemTyp)
		}
		return llvmTupleTypeName(elemTypes), nil
	default:
		return "", unsupportedf("type-system", "type %T", t)
	}
}

func llvmMethodOwnerType(ownerName string, structs map[string]*structInfo, enums map[string]*enumInfo) (string, bool) {
	if info := structs[ownerName]; info != nil {
		return info.typ, true
	}
	if info := enums[ownerName]; info != nil {
		return info.typ, true
	}
	return "", false
}

// llvmMethodIRName composes the LLVM IR symbol for a named method.
// Delegates to the Osty-sourced `mirLlvmMethodIRName`
// (`toolchain/mir_generator.osty`).
func llvmMethodIRName(ownerName, methodName string) string {
	return mirLlvmMethodIRName(ownerName, methodName)
}

// llvmMethodReceiverIRType returns the LLVM type for a method receiver.
// Delegates to `mirLlvmMethodReceiverIRType`.
func llvmMethodReceiverIRType(ownerType string, mutable bool) string {
	return mirLlvmMethodReceiverIRType(ownerType, mutable)
}

func llvmParamIRType(param paramInfo) string {
	if param.irTyp != "" {
		return param.irTyp
	}
	return param.typ
}

// sanitizeLLVMName folds an arbitrary identifier into the LLVM
// global-name alphabet `[A-Za-z_][A-Za-z0-9_]*`. Delegates to the
// Osty-sourced `mirSanitizeLLVMName` (`toolchain/mir_generator.osty`).
func sanitizeLLVMName(name string) string {
	return mirSanitizeLLVMName(name)
}
