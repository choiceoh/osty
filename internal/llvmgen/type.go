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
	"fmt"
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
	names := make([]string, 0, len(parts)+1)
	names = append(names, prefix)
	for _, part := range parts {
		names = append(names, llvmBuiltinAggregatePart(part))
	}
	return "%" + strings.Join(names, ".")
}

func llvmBuiltinAggregatePart(part string) string {
	if part == "" {
		return "void"
	}
	var b strings.Builder
	for i := 0; i < len(part); i++ {
		c := part[i]
		if c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || ('0' <= c && c <= '9') {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return "value"
	}
	return b.String()
}

func llvmResultTypeName(okTyp, errTyp string) string {
	return llvmBuiltinAggregateName("Result", okTyp, errTyp)
}

func llvmTupleTypeName(elemTypes []string) string {
	return llvmBuiltinAggregateName("Tuple", elemTypes...)
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

func llvmEnumPayloadType(t ast.Type, env typeEnv) (string, error) {
	resolved, err := llvmResolveAliasType(t, env, map[string]bool{})
	if err != nil {
		return "", err
	}
	named, ok := t.(*ast.NamedType)
	if resolvedNamed, resolvedOK := resolved.(*ast.NamedType); resolvedOK {
		named = resolvedNamed
		ok = true
	}
	if !ok {
		return "", unsupported("type-system", "LLVM enum payloads currently support Int or Float only")
	}
	name := ""
	if len(named.Path) == 1 {
		name = named.Path[0]
	}
	if typ := llvmEnumPayloadNamedType(name, len(named.Path), len(named.Args)); typ != "" {
		return typ, nil
	}
	return "", unsupported("type-system", "LLVM enum payloads currently support Int, Float, or String only")
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
	opt, ok := t.(*ast.OptionalType)
	if !ok || opt == nil || opt.Inner == nil {
		return nil, false
	}
	return opt.Inner, true
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
	case *ast.StringLit:
		return &ast.NamedType{Path: []string{"String"}}, true
	case *ast.Ident:
		if v, ok := g.lookupBinding(e.Name); ok && v.sourceType != nil {
			return v.sourceType, true
		}
	case *ast.ParenExpr:
		return g.staticExprSourceType(e.X)
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
	case *ast.CallExpr:
		if out, ok := g.staticStringMethodResult(e); ok {
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
		if out, ok := g.stdStringsCallStaticResult(e); ok {
			return out, true
		}
	case *ast.FieldExpr:
		if e.IsOptional {
			return value{}, false
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
				return value{typ: baseInfo.listElemTyp, gcManaged: baseInfo.listElemTyp == "ptr", rootPaths: g.rootPathsForType(baseInfo.listElemTyp)}, true
			case baseInfo.mapKeyTyp != "":
				return value{typ: baseInfo.mapValueTyp, gcManaged: baseInfo.mapValueTyp == "ptr", rootPaths: g.rootPathsForType(baseInfo.mapValueTyp)}, true
			}
		}
	}
	return value{}, false
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
	resolved, err := llvmResolveAliasType(src, g.typeEnv(), map[string]bool{})
	if err != nil {
		return nil, false
	}
	named, ok := resolved.(*ast.NamedType)
	if !ok || len(named.Path) != 1 || named.Path[0] != "List" || len(named.Args) != 1 {
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

func (g *generator) staticExprListElemIsBytes(expr ast.Expr) bool {
	sourceType, ok := g.staticExprSourceType(expr)
	if !ok {
		return false
	}
	resolved, err := llvmResolveAliasType(sourceType, g.typeEnv(), map[string]bool{})
	if err != nil {
		return false
	}
	named, ok := resolved.(*ast.NamedType)
	if !ok || len(named.Path) != 1 || named.Path[0] != "List" || len(named.Args) != 1 {
		return false
	}
	elemResolved, err := llvmResolveAliasType(named.Args[0], g.typeEnv(), map[string]bool{})
	if err != nil {
		return false
	}
	return llvmNamedTypeIsBytes(elemResolved)
}

func (g *generator) staticStringMethodSourceType(call *ast.CallExpr) (ast.Type, bool) {
	field, ok := g.stringMethodInfo(call)
	if !ok {
		return nil, false
	}
	switch field.Name {
	case "len":
		return &ast.NamedType{Path: []string{"Int"}}, true
	case "isEmpty", "startsWith":
		return &ast.NamedType{Path: []string{"Bool"}}, true
	case "split":
		return &ast.NamedType{
			Path: []string{"List"},
			Args: []ast.Type{&ast.NamedType{Path: []string{"String"}}},
		}, true
	case "trim", "toString":
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

func (g *generator) staticStringMethodResult(call *ast.CallExpr) (value, bool) {
	field, ok := g.stringMethodInfo(call)
	if !ok {
		return value{}, false
	}
	switch field.Name {
	case "len":
		return value{typ: "i64"}, true
	case "isEmpty", "startsWith":
		return value{typ: "i1"}, true
	case "split":
		return value{typ: "ptr", gcManaged: true, listElemTyp: "ptr", listElemString: true}, true
	case "trim", "toString":
		return value{typ: "ptr", gcManaged: true}, true
	case "chars":
		return value{typ: "ptr", gcManaged: true, listElemTyp: "i32"}, true
	case "bytes":
		return value{typ: "ptr", gcManaged: true, listElemTyp: "i8"}, true
	default:
		return value{}, false
	}
}

func (g *generator) stringMethodInfo(call *ast.CallExpr) (*ast.FieldExpr, bool) {
	if call == nil {
		return nil, false
	}
	field, ok := call.Fn.(*ast.FieldExpr)
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
	case "len", "isEmpty", "startsWith", "split", "trim", "toString", "chars", "bytes":
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
		case "sorted":
			return value{typ: "ptr", gcManaged: true, listElemTyp: elemTyp, listElemString: elemString}, true, true
		case "toSet":
			return value{typ: "ptr", gcManaged: true, setElemTyp: elemTyp, setElemString: elemString}, true, true
		default:
			return value{}, true, false
		}
	}
	if _, keyTyp, _, keyString, found := g.mapMethodInfo(call); found {
		switch field := call.Fn.(*ast.FieldExpr); field.Name {
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
	if _, elemTyp, elemString, found := g.setMethodInfo(call); found {
		switch field := call.Fn.(*ast.FieldExpr); field.Name {
		case "len":
			return value{typ: "i64"}, true, true
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

func (g *generator) listMethodInfo(call *ast.CallExpr) (*ast.FieldExpr, string, bool, bool) {
	if call == nil {
		return nil, "", false, false
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok || field.IsOptional {
		return nil, "", false, false
	}
	switch field.Name {
	case "len", "push", "sorted", "toSet":
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
	if call == nil {
		return nil, "", "", false, false
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok || field.IsOptional {
		return nil, "", "", false, false
	}
	switch field.Name {
	case "containsKey", "insert", "remove", "keys":
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
	if call == nil {
		return nil, "", false, false
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok || field.IsOptional {
		return nil, "", false, false
	}
	switch field.Name {
	case "len", "contains", "insert", "remove", "toList":
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

func llvmAggregatePathIndices(path []int) string {
	parts := make([]string, 0, len(path)+1)
	parts = append(parts, "i32 0")
	for _, index := range path {
		parts = append(parts, fmt.Sprintf("i32 %d", index))
	}
	return strings.Join(parts, ", ")
}

func llvmType(t ast.Type, env typeEnv) (string, error) {
	resolved, err := llvmResolveAliasType(t, env, map[string]bool{})
	if err != nil {
		return "", err
	}
	t = resolved
	switch tt := t.(type) {
	case *ast.NamedType:
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

func llvmMethodIRName(ownerName, methodName string) string {
	return sanitizeLLVMName(ownerName) + "__" + sanitizeLLVMName(methodName)
}

func llvmMethodReceiverIRType(ownerType string, mutable bool) string {
	if mutable {
		return "ptr"
	}
	return ownerType
}

func llvmParamIRType(param paramInfo) string {
	if param.irTyp != "" {
		return param.irTyp
	}
	return param.typ
}

func sanitizeLLVMName(name string) string {
	if name == "" {
		return "anon"
	}
	var b strings.Builder
	for i, c := range name {
		if c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z') || (i > 0 && '0' <= c && c <= '9') {
			b.WriteRune(c)
			continue
		}
		if i == 0 && '0' <= c && c <= '9' {
			b.WriteByte('_')
			b.WriteRune(c)
			continue
		}
		b.WriteByte('_')
	}
	if b.Len() == 0 {
		return "anon"
	}
	return b.String()
}
