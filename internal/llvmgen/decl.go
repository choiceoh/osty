// decl.go — top-level declaration collection (struct/enum/interface/type-alias/
// global-let shells + signatures), function-entry emission (emitScriptMain /
// emitMainFunction / emitUserFunction), and global-let constant evaluation.
//
// Data carriers (fnSig, paramInfo, structInfo, enumInfo, variantInfo, etc.)
// live here so collectors and emitters share a single source of truth.
//
// NOTE(osty-migration): AST traversal for top-level shells is deeply tied to
// Go ast.Decl types; porting requires either an Osty-side AST mirror
// (currently absent — toolchain/ast_lower.osty is Go-FFI only) or rerouting
// through the IR tier (toolchain/ir.osty). Const-eval (constExpr chain) is
// the closest candidate for near-term Osty ownership once simple Expr
// mirrors exist.
package llvmgen

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

type declarations struct {
	functionsOrdered  []*fnSig
	functionsByName   map[string]*fnSig
	methodsByType     map[string]map[string]*fnSig
	structsOrdered    []*structInfo
	structsByName     map[string]*structInfo
	structsByType     map[string]*structInfo
	enumsOrdered      []*enumInfo
	enumsByName       map[string]*enumInfo
	enumsByType       map[string]*enumInfo
	interfacesByName  map[string]*interfaceInfo
	typeAliasesByName map[string]*typeAliasInfo
	globalsOrdered    []*globalLetInfo
	globalsByName     map[string]*globalLetInfo
}

type fnSig struct {
	name             string
	irName           string
	receiverType     string
	receiverMut      bool
	ret              string
	retListElemTyp   string
	retListString    bool
	retMapKeyTyp     string
	retMapValueTyp   string
	retMapKeyString  bool
	retSetElemTyp    string
	retSetElemString bool
	returnSourceType ast.Type
	params           []paramInfo
	decl             *ast.FnDecl
}

type paramInfo struct {
	name           string
	typ            string
	irTyp          string
	listElemTyp    string
	listElemString bool
	mapKeyTyp      string
	mapValueTyp    string
	mapKeyString   bool
	setElemTyp     string
	setElemString  bool
	sourceType     ast.Type
	mutable        bool
	byRef          bool
}

type structInfo struct {
	name   string
	typ    string
	decl   *ast.StructDecl
	fields []fieldInfo
	byName map[string]fieldInfo
}

type fieldInfo struct {
	name           string
	typ            string
	index          int
	listElemTyp    string
	listElemString bool
	mapKeyTyp      string
	mapValueTyp    string
	mapKeyString   bool
	setElemTyp     string
	setElemString  bool
	sourceType     ast.Type
}

type containerMetadata struct {
	sourceType     ast.Type
	listElemTyp    string
	listElemString bool
	mapKeyTyp      string
	mapValueTyp    string
	mapKeyString   bool
	setElemTyp     string
	setElemString  bool
}

func containerMetadataFromSourceType(sourceType ast.Type, env typeEnv) (containerMetadata, error) {
	meta := containerMetadata{sourceType: sourceType}
	if sourceType == nil {
		return meta, nil
	}
	if listElemTyp, listElemString, ok, err := llvmListElementInfo(sourceType, env); err != nil {
		return containerMetadata{}, err
	} else if ok {
		meta.listElemTyp = listElemTyp
		meta.listElemString = listElemString
	}
	if mapKeyTyp, mapValueTyp, mapKeyString, ok, err := llvmMapTypes(sourceType, env); err != nil {
		return containerMetadata{}, err
	} else if ok {
		meta.mapKeyTyp = mapKeyTyp
		meta.mapValueTyp = mapValueTyp
		meta.mapKeyString = mapKeyString
	}
	if setElemTyp, setElemString, ok, err := llvmSetElementType(sourceType, env); err != nil {
		return containerMetadata{}, err
	} else if ok {
		meta.setElemTyp = setElemTyp
		meta.setElemString = setElemString
	}
	return meta, nil
}

func (m containerMetadata) applyToParam(info *paramInfo) {
	if info == nil {
		return
	}
	info.listElemTyp = m.listElemTyp
	info.listElemString = m.listElemString
	info.mapKeyTyp = m.mapKeyTyp
	info.mapValueTyp = m.mapValueTyp
	info.mapKeyString = m.mapKeyString
	info.setElemTyp = m.setElemTyp
	info.setElemString = m.setElemString
	info.sourceType = m.sourceType
}

func (m containerMetadata) applyToField(info *fieldInfo) {
	if info == nil {
		return
	}
	info.listElemTyp = m.listElemTyp
	info.listElemString = m.listElemString
	info.mapKeyTyp = m.mapKeyTyp
	info.mapValueTyp = m.mapValueTyp
	info.mapKeyString = m.mapKeyString
	info.setElemTyp = m.setElemTyp
	info.setElemString = m.setElemString
	info.sourceType = m.sourceType
}

func (m containerMetadata) applyToReturn(sig *fnSig) {
	if sig == nil {
		return
	}
	sig.retListElemTyp = m.listElemTyp
	sig.retListString = m.listElemString
	sig.retMapKeyTyp = m.mapKeyTyp
	sig.retMapValueTyp = m.mapValueTyp
	sig.retMapKeyString = m.mapKeyString
	sig.retSetElemTyp = m.setElemTyp
	sig.retSetElemString = m.setElemString
	sig.returnSourceType = m.sourceType
}

func (m containerMetadata) applyToValue(out *value) {
	if out == nil {
		return
	}
	out.listElemTyp = m.listElemTyp
	out.listElemString = m.listElemString
	out.mapKeyTyp = m.mapKeyTyp
	out.mapValueTyp = m.mapValueTyp
	out.mapKeyString = m.mapKeyString
	out.setElemTyp = m.setElemTyp
	out.setElemString = m.setElemString
	out.sourceType = m.sourceType
	out.gcManaged = valueNeedsManagedRoot(*out)
}

type enumInfo struct {
	name             string
	typ              string
	decl             *ast.EnumDecl
	hasPayload       bool
	payloadTyp       string
	payloadCount     int
	payloadSlotTypes []string
	isBoxed          bool
	variants         map[string]variantInfo
}

type variantInfo struct {
	name                string
	tag                 int
	payloads            []string
	payloadListElemTyp  string
	payloadListElemTyps []string
}

type enumVariantRef struct {
	enum    *enumInfo
	variant variantInfo
}

type enumPatternInfo struct {
	variant            variantInfo
	payloadName        string
	payloadType        string
	payloadListElemTyp string
	hasPayloadBinding  bool
	isBoxed            bool
	payloadBindings    []enumPayloadBinding
}

type enumPayloadBinding struct {
	name  string
	typ   string
	index int
}

type tupleTypeInfo struct {
	typ              string
	elems            []string
	elemListElemTyps []string
}

// interfaceInfo carries the collected shape of an `interface` declaration.
// Phase 6a: `methods` preserves the source-order signature list so the
// vtable emitter can lay out function-pointer slots in a deterministic
// order that matches `impls` tables. `impls` lists every struct/enum
// name whose method set structurally satisfies this interface, in
// source-declaration order.
type interfaceInfo struct {
	name    string
	decl    *ast.InterfaceDecl
	methods []interfaceMethodSig
	impls   []interfaceImpl
}

// interfaceMethodSig is the minimal surface the vtable emitter needs
// from each interface method: its source name and an ordinal slot.
type interfaceMethodSig struct {
	name string
	slot int
}

// interfaceImpl records a concrete type that satisfies an interface,
// together with the symbol its vtable emits.
type interfaceImpl struct {
	implName  string // source name of the struct/enum
	kind      int    // 0 = struct, 1 = enum
	vtableSym string // `@osty.vtable.<impl>__<iface>`
}

type typeAliasInfo struct {
	name string
	decl *ast.TypeAliasDecl
}

type globalLetInfo struct {
	name    string
	irName  string
	mutable bool
	decl    *ast.LetDecl
}

type constKind int

const (
	constKindOpaque constKind = iota
	constKindInt
	constKindFloat
	constKindBool
	constKindString
)

type constValue struct {
	typ         string
	init        string
	kind        constKind
	intValue    int64
	floatValue  float64
	boolValue   bool
	stringValue string
}

func (c constValue) typedInit() string {
	return fmt.Sprintf("%s %s", c.typ, c.init)
}

type builtinResultType struct {
	typ    string
	okTyp  string
	errTyp string
}

func builtinResultTypeFromAST(t ast.Type, env typeEnv) (builtinResultType, bool) {
	named, ok := t.(*ast.NamedType)
	if !ok || len(named.Path) != 1 || named.Path[0] != "Result" || len(named.Args) != 2 {
		return builtinResultType{}, false
	}
	okTyp, err := llvmType(named.Args[0], env)
	if err != nil {
		return builtinResultType{}, false
	}
	errTyp, err := llvmType(named.Args[1], env)
	if err != nil {
		return builtinResultType{}, false
	}
	return builtinResultType{
		typ:    llvmResultTypeName(okTyp, errTyp),
		okTyp:  okTyp,
		errTyp: errTyp,
	}, true
}

func collectDeclarations(file *ast.File) (*declarations, error) {
	out := &declarations{
		functionsByName:   map[string]*fnSig{},
		methodsByType:     map[string]map[string]*fnSig{},
		structsByName:     map[string]*structInfo{},
		structsByType:     map[string]*structInfo{},
		enumsByName:       map[string]*enumInfo{},
		enumsByType:       map[string]*enumInfo{},
		interfacesByName:  map[string]*interfaceInfo{},
		typeAliasesByName: map[string]*typeAliasInfo{},
		globalsByName:     map[string]*globalLetInfo{},
	}
	var enumDecls []*ast.EnumDecl
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.StructDecl:
			info, err := collectStructShell(d)
			if err != nil {
				return nil, err
			}
			if _, exists := out.structsByName[info.name]; exists {
				return nil, unsupportedf("source-layout", "duplicate struct %q", info.name)
			}
			out.structsOrdered = append(out.structsOrdered, info)
			out.structsByName[info.name] = info
			out.structsByType[info.typ] = info
		case *ast.EnumDecl:
			enumDecls = append(enumDecls, d)
		case *ast.InterfaceDecl:
			info, err := collectInterfaceShell(d)
			if err != nil {
				return nil, err
			}
			if _, exists := out.interfacesByName[info.name]; exists {
				return nil, unsupportedf("source-layout", "duplicate interface %q", info.name)
			}
			out.interfacesByName[info.name] = info
		case *ast.TypeAliasDecl:
			info, err := collectTypeAliasShell(d)
			if err != nil {
				return nil, err
			}
			if _, exists := out.typeAliasesByName[info.name]; exists {
				return nil, unsupportedf("source-layout", "duplicate type alias %q", info.name)
			}
			out.typeAliasesByName[info.name] = info
		case *ast.LetDecl:
			info, err := collectGlobalLetShell(d)
			if err != nil {
				return nil, err
			}
			if _, exists := out.globalsByName[info.name]; exists {
				return nil, unsupportedf("source-layout", "duplicate top-level let %q", info.name)
			}
			out.globalsOrdered = append(out.globalsOrdered, info)
			out.globalsByName[info.name] = info
		case *ast.FnDecl:
			// Function signatures are collected after struct shells so named
			// struct types can appear in parameters and returns.
		default:
			return nil, unsupportedf("source-layout", "top-level declaration %T", decl)
		}
	}
	env := typeEnv{
		structs:    out.structsByName,
		enums:      out.enumsByName,
		interfaces: out.interfacesByName,
		aliases:    out.typeAliasesByName,
	}
	for _, decl := range enumDecls {
		info, err := collectEnum(decl, env)
		if err != nil {
			return nil, err
		}
		if _, exists := out.enumsByName[info.name]; exists {
			return nil, unsupportedf("source-layout", "duplicate enum %q", info.name)
		}
		out.enumsOrdered = append(out.enumsOrdered, info)
		out.enumsByName[info.name] = info
		if info.hasPayload {
			out.enumsByType[info.typ] = info
		}
	}
	env.enums = out.enumsByName
	for _, info := range out.structsOrdered {
		if err := collectStructFields(info, env); err != nil {
			return nil, err
		}
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FnDecl)
		if !ok {
			continue
		}
		sig, err := signatureOf(fn, "", env)
		if err != nil {
			return nil, err
		}
		if _, exists := out.functionsByName[sig.name]; exists {
			return nil, unsupportedf("source-layout", "duplicate function %q", sig.name)
		}
		out.functionsOrdered = append(out.functionsOrdered, sig)
		out.functionsByName[sig.name] = sig
	}
	for _, info := range out.structsOrdered {
		if err := collectMethodDeclarations(out, info.name, info.typ, info.decl.Methods, env); err != nil {
			return nil, err
		}
	}
	for _, info := range out.enumsOrdered {
		if err := collectMethodDeclarations(out, info.name, info.typ, info.decl.Methods, env); err != nil {
			return nil, err
		}
	}
	// Phase 6a: once every struct/enum method has been registered in
	// methodsByType, scan for structural satisfaction so each interface
	// knows which concrete types need a vtable emitted at render time.
	discoverInterfaceImplementations(out)
	return out, nil
}

func collectMethodDeclarations(out *declarations, ownerName, ownerType string, methods []*ast.FnDecl, env typeEnv) error {
	if out == nil {
		return unsupported("source-layout", "nil declarations")
	}
	for _, fn := range methods {
		sig, err := signatureOf(fn, ownerName, env)
		if err != nil {
			return err
		}
		methodsByName := out.methodsByType[ownerType]
		if methodsByName == nil {
			methodsByName = map[string]*fnSig{}
			out.methodsByType[ownerType] = methodsByName
		}
		if _, exists := methodsByName[sig.name]; exists {
			return unsupportedf("source-layout", "duplicate method %q on %q", sig.name, ownerName)
		}
		out.functionsOrdered = append(out.functionsOrdered, sig)
		methodsByName[sig.name] = sig
	}
	return nil
}

func collectStdTestingAliases(file *ast.File) map[string]bool {
	out := map[string]bool{}
	if file == nil {
		return out
	}
	for _, use := range file.Uses {
		if use == nil || len(use.Path) != 2 || use.Path[0] != "std" || use.Path[1] != "testing" {
			continue
		}
		alias := use.Alias
		if alias == "" {
			alias = "testing"
		}
		if alias != "" {
			out[alias] = true
		}
	}
	return out
}

func collectBuiltinResultTypes(file *ast.File, env typeEnv) map[string]builtinResultType {
	out := map[string]builtinResultType{}
	if file == nil {
		return out
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			collectBuiltinResultTypesFromFn(out, d, env)
		case *ast.StructDecl:
			for _, field := range d.Fields {
				if field == nil {
					continue
				}
				collectBuiltinResultTypeFromAST(out, field.Type, env)
			}
			for _, method := range d.Methods {
				collectBuiltinResultTypesFromFn(out, method, env)
			}
		case *ast.EnumDecl:
			for _, variant := range d.Variants {
				if variant == nil {
					continue
				}
				for _, field := range variant.Fields {
					collectBuiltinResultTypeFromAST(out, field, env)
				}
			}
			for _, method := range d.Methods {
				collectBuiltinResultTypesFromFn(out, method, env)
			}
		case *ast.LetDecl:
			collectBuiltinResultTypeFromAST(out, d.Type, env)
		}
	}
	for _, stmt := range file.Stmts {
		collectBuiltinResultTypesFromStmt(out, stmt, env)
	}
	return out
}

func collectBuiltinResultTypesFromFn(out map[string]builtinResultType, fn *ast.FnDecl, env typeEnv) {
	if out == nil || fn == nil {
		return
	}
	for _, param := range fn.Params {
		if param == nil {
			continue
		}
		collectBuiltinResultTypeFromAST(out, param.Type, env)
	}
	collectBuiltinResultTypeFromAST(out, fn.ReturnType, env)
	collectBuiltinResultTypesFromBlock(out, fn.Body, env)
}

func collectBuiltinResultTypesFromBlock(out map[string]builtinResultType, block *ast.Block, env typeEnv) {
	if out == nil || block == nil {
		return
	}
	for _, stmt := range block.Stmts {
		collectBuiltinResultTypesFromStmt(out, stmt, env)
	}
}

func collectBuiltinResultTypesFromStmt(out map[string]builtinResultType, stmt ast.Stmt, env typeEnv) {
	if out == nil || stmt == nil {
		return
	}
	switch s := stmt.(type) {
	case *ast.Block:
		collectBuiltinResultTypesFromBlock(out, s, env)
	case *ast.LetStmt:
		collectBuiltinResultTypeFromAST(out, s.Type, env)
		collectBuiltinResultTypesFromExpr(out, s.Value, env)
	case *ast.ExprStmt:
		collectBuiltinResultTypesFromExpr(out, s.X, env)
	case *ast.AssignStmt:
		for _, target := range s.Targets {
			collectBuiltinResultTypesFromExpr(out, target, env)
		}
		collectBuiltinResultTypesFromExpr(out, s.Value, env)
	case *ast.ReturnStmt:
		collectBuiltinResultTypesFromExpr(out, s.Value, env)
	case *ast.ChanSendStmt:
		collectBuiltinResultTypesFromExpr(out, s.Channel, env)
		collectBuiltinResultTypesFromExpr(out, s.Value, env)
	case *ast.DeferStmt:
		collectBuiltinResultTypesFromExpr(out, s.X, env)
	case *ast.ForStmt:
		collectBuiltinResultTypesFromExpr(out, s.Iter, env)
		collectBuiltinResultTypesFromBlock(out, s.Body, env)
	}
}

func collectBuiltinResultTypesFromExpr(out map[string]builtinResultType, expr ast.Expr, env typeEnv) {
	if out == nil || expr == nil {
		return
	}
	switch e := expr.(type) {
	case *ast.Block:
		collectBuiltinResultTypesFromBlock(out, e, env)
	case *ast.ParenExpr:
		collectBuiltinResultTypesFromExpr(out, e.X, env)
	case *ast.UnaryExpr:
		collectBuiltinResultTypesFromExpr(out, e.X, env)
	case *ast.BinaryExpr:
		collectBuiltinResultTypesFromExpr(out, e.Left, env)
		collectBuiltinResultTypesFromExpr(out, e.Right, env)
	case *ast.QuestionExpr:
		collectBuiltinResultTypesFromExpr(out, e.X, env)
	case *ast.CallExpr:
		collectBuiltinResultTypesFromExpr(out, e.Fn, env)
		for _, arg := range e.Args {
			if arg != nil {
				collectBuiltinResultTypesFromExpr(out, arg.Value, env)
			}
		}
	case *ast.FieldExpr:
		collectBuiltinResultTypesFromExpr(out, e.X, env)
	case *ast.IndexExpr:
		collectBuiltinResultTypesFromExpr(out, e.X, env)
		collectBuiltinResultTypesFromExpr(out, e.Index, env)
	case *ast.TupleExpr:
		for _, elem := range e.Elems {
			collectBuiltinResultTypesFromExpr(out, elem, env)
		}
	case *ast.ListExpr:
		for _, elem := range e.Elems {
			collectBuiltinResultTypesFromExpr(out, elem, env)
		}
	case *ast.MapExpr:
		for _, entry := range e.Entries {
			if entry == nil {
				continue
			}
			collectBuiltinResultTypesFromExpr(out, entry.Key, env)
			collectBuiltinResultTypesFromExpr(out, entry.Value, env)
		}
	case *ast.StructLit:
		collectBuiltinResultTypesFromExpr(out, e.Type, env)
		for _, field := range e.Fields {
			if field != nil {
				collectBuiltinResultTypesFromExpr(out, field.Value, env)
			}
		}
		collectBuiltinResultTypesFromExpr(out, e.Spread, env)
	case *ast.IfExpr:
		collectBuiltinResultTypesFromExpr(out, e.Cond, env)
		collectBuiltinResultTypesFromBlock(out, e.Then, env)
		collectBuiltinResultTypesFromExpr(out, e.Else, env)
	case *ast.MatchExpr:
		collectBuiltinResultTypesFromExpr(out, e.Scrutinee, env)
		for _, arm := range e.Arms {
			if arm == nil {
				continue
			}
			collectBuiltinResultTypesFromExpr(out, arm.Guard, env)
			collectBuiltinResultTypesFromExpr(out, arm.Body, env)
		}
	case *ast.ClosureExpr:
		for _, param := range e.Params {
			if param != nil {
				collectBuiltinResultTypeFromAST(out, param.Type, env)
			}
		}
		collectBuiltinResultTypeFromAST(out, e.ReturnType, env)
		collectBuiltinResultTypesFromExpr(out, e.Body, env)
	case *ast.TurbofishExpr:
		collectBuiltinResultTypesFromExpr(out, e.Base, env)
		for _, arg := range e.Args {
			collectBuiltinResultTypeFromAST(out, arg, env)
		}
	case *ast.RangeExpr:
		collectBuiltinResultTypesFromExpr(out, e.Start, env)
		collectBuiltinResultTypesFromExpr(out, e.Stop, env)
	}
}

func collectTupleTypes(file *ast.File, env typeEnv) map[string]tupleTypeInfo {
	out := map[string]tupleTypeInfo{}
	if file == nil {
		return out
	}
	collectFn := func(fn *ast.FnDecl) {
		if fn == nil {
			return
		}
		for _, param := range fn.Params {
			if param == nil {
				continue
			}
			collectTupleTypeFromAST(out, param.Type, env)
		}
		collectTupleTypeFromAST(out, fn.ReturnType, env)
	}
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FnDecl:
			collectFn(d)
		case *ast.StructDecl:
			for _, field := range d.Fields {
				if field == nil {
					continue
				}
				collectTupleTypeFromAST(out, field.Type, env)
			}
			for _, method := range d.Methods {
				collectFn(method)
			}
		case *ast.EnumDecl:
			for _, variant := range d.Variants {
				if variant == nil {
					continue
				}
				for _, field := range variant.Fields {
					collectTupleTypeFromAST(out, field, env)
				}
			}
			for _, method := range d.Methods {
				collectFn(method)
			}
		case *ast.LetDecl:
			collectTupleTypeFromAST(out, d.Type, env)
		}
	}
	return out
}

func collectTupleTypeFromAST(out map[string]tupleTypeInfo, t ast.Type, env typeEnv) {
	if out == nil || t == nil {
		return
	}
	switch tt := t.(type) {
	case *ast.NamedType:
		for _, arg := range tt.Args {
			collectTupleTypeFromAST(out, arg, env)
		}
	case *ast.OptionalType:
		collectTupleTypeFromAST(out, tt.Inner, env)
	case *ast.TupleType:
		elemTypes := make([]string, 0, len(tt.Elems))
		elemListElemTyps := make([]string, 0, len(tt.Elems))
		for _, elem := range tt.Elems {
			collectTupleTypeFromAST(out, elem, env)
			elemTyp, err := llvmType(elem, env)
			if err != nil {
				return
			}
			elemTypes = append(elemTypes, elemTyp)
			if listElemTyp, ok, err := llvmListElementType(elem, env); err == nil && ok {
				elemListElemTyps = append(elemListElemTyps, listElemTyp)
			} else {
				elemListElemTyps = append(elemListElemTyps, "")
			}
		}
		info := tupleTypeInfo{
			typ:              llvmTupleTypeName(elemTypes),
			elems:            elemTypes,
			elemListElemTyps: elemListElemTyps,
		}
		out[info.typ] = info
	case *ast.FnType:
		for _, param := range tt.Params {
			collectTupleTypeFromAST(out, param, env)
		}
		collectTupleTypeFromAST(out, tt.ReturnType, env)
	}
}

func collectBuiltinResultTypeFromAST(out map[string]builtinResultType, t ast.Type, env typeEnv) {
	if out == nil || t == nil {
		return
	}
	switch tt := t.(type) {
	case *ast.NamedType:
		if info, ok := builtinResultTypeFromAST(tt, env); ok {
			out[info.typ] = info
		}
		for _, arg := range tt.Args {
			collectBuiltinResultTypeFromAST(out, arg, env)
		}
	case *ast.OptionalType:
		collectBuiltinResultTypeFromAST(out, tt.Inner, env)
	case *ast.TupleType:
		for _, elem := range tt.Elems {
			collectBuiltinResultTypeFromAST(out, elem, env)
		}
	case *ast.FnType:
		for _, param := range tt.Params {
			collectBuiltinResultTypeFromAST(out, param, env)
		}
		collectBuiltinResultTypeFromAST(out, tt.ReturnType, env)
	}
}

func collectInterfaceShell(decl *ast.InterfaceDecl) (*interfaceInfo, error) {
	if decl == nil {
		return nil, unsupported("source-layout", "nil interface")
	}
	if diag := llvmNominalDeclHeaderDiagnostic("interface", decl.Name, llvmIsIdent(decl.Name), len(decl.Generics), len(decl.Methods)); diag.kind != "" {
		return nil, unsupported(diag.kind, diag.message)
	}
	info := &interfaceInfo{name: decl.Name, decl: decl}
	// Phase 6a: capture method names in source order so the vtable
	// emitter can lay out function-pointer slots deterministically.
	for i, m := range decl.Methods {
		if m == nil || !llvmIsIdent(m.Name) {
			continue
		}
		info.methods = append(info.methods, interfaceMethodSig{name: m.Name, slot: i})
	}
	return info, nil
}

// discoverInterfaceImplementations walks every struct and enum looking
// for structural satisfaction of each collected interface: if all the
// interface's source-name methods appear in the type's method set,
// the type is registered as an implementer. The vtable symbol name
// follows `osty.vtable.<impl>__<iface>` so multiple implementers of
// the same interface never collide.
//
// Source-name matching is deliberate for Phase 6a: a more strict
// signature-level match (argument / return types) will be layered on
// once the dispatch path lands.
func discoverInterfaceImplementations(decls *declarations) {
	if decls == nil {
		return
	}
	for _, iface := range decls.interfacesByName {
		if iface == nil {
			continue
		}
		for _, sd := range decls.structsOrdered {
			if sd == nil {
				continue
			}
			if interfaceSatisfiedByMethods(iface, decls.methodsByType[sd.typ]) {
				iface.impls = append(iface.impls, interfaceImpl{
					implName:  sd.name,
					kind:      0,
					vtableSym: "@osty.vtable." + sd.name + "__" + iface.name,
				})
			}
		}
		for _, ed := range decls.enumsOrdered {
			if ed == nil {
				continue
			}
			if interfaceSatisfiedByMethods(iface, decls.methodsByType[ed.typ]) {
				iface.impls = append(iface.impls, interfaceImpl{
					implName:  ed.name,
					kind:      1,
					vtableSym: "@osty.vtable." + ed.name + "__" + iface.name,
				})
			}
		}
	}
}

func interfaceSatisfiedByMethods(iface *interfaceInfo, methods map[string]*fnSig) bool {
	if iface == nil || len(iface.methods) == 0 {
		return false
	}
	if methods == nil {
		return false
	}
	for _, need := range iface.methods {
		if _, ok := methods[need.name]; !ok {
			return false
		}
	}
	return true
}

func collectTypeAliasShell(decl *ast.TypeAliasDecl) (*typeAliasInfo, error) {
	if decl == nil {
		return nil, unsupported("source-layout", "nil type alias")
	}
	if !llvmIsIdent(decl.Name) {
		return nil, unsupported("name", fmt.Sprintf("type alias name %q", decl.Name))
	}
	return &typeAliasInfo{name: decl.Name, decl: decl}, nil
}

func collectGlobalLetShell(decl *ast.LetDecl) (*globalLetInfo, error) {
	if decl == nil {
		return nil, unsupported("source-layout", "nil top-level let")
	}
	if !llvmIsIdent(decl.Name) {
		return nil, unsupported("name", fmt.Sprintf("let name %q", decl.Name))
	}
	return &globalLetInfo{
		name:    decl.Name,
		irName:  llvmGlobalIRName(decl.Name),
		mutable: decl.Mut,
		decl:    decl,
	}, nil
}

func collectStructShell(decl *ast.StructDecl) (*structInfo, error) {
	if decl == nil {
		return nil, unsupported("source-layout", "nil struct")
	}
	if diag := llvmNominalDeclHeaderDiagnostic("struct", decl.Name, llvmIsIdent(decl.Name), len(decl.Generics), len(decl.Methods)); diag.kind != "" {
		return nil, unsupported(diag.kind, diag.message)
	}
	return &structInfo{
		name:   decl.Name,
		typ:    llvmStructTypeName(decl.Name),
		decl:   decl,
		byName: map[string]fieldInfo{},
	}, nil
}

func collectStructFields(info *structInfo, env typeEnv) error {
	if info == nil || info.decl == nil {
		return unsupported("source-layout", "nil struct")
	}
	for i, field := range info.decl.Fields {
		if field == nil {
			return unsupportedf("source-layout", "struct %q has nil field", info.name)
		}
		if diag := llvmStructFieldDiagnostic(info.name, field.Name, llvmIsIdent(field.Name), field.Default != nil, false, false, ""); diag.kind != "" {
			return unsupported(diag.kind, diag.message)
		}
		if _, exists := info.byName[field.Name]; exists {
			diag := llvmStructFieldDiagnostic(info.name, field.Name, true, false, true, false, "")
			return unsupported(diag.kind, diag.message)
		}
		typ, err := llvmType(field.Type, env)
		if err != nil {
			diag := llvmStructFieldDiagnostic(info.name, field.Name, true, false, false, false, unsupportedMessage(err))
			return unsupported(diag.kind, diag.message)
		}
		if typ == info.typ {
			diag := llvmStructFieldDiagnostic(info.name, field.Name, true, false, false, true, "")
			return unsupported(diag.kind, diag.message)
		}
		meta, err := containerMetadataFromSourceType(field.Type, env)
		if err != nil {
			diag := llvmStructFieldDiagnostic(info.name, field.Name, true, false, false, false, unsupportedMessage(err))
			return unsupported(diag.kind, diag.message)
		}
		fieldInfo := fieldInfo{name: field.Name, typ: typ, index: i}
		meta.applyToField(&fieldInfo)
		info.fields = append(info.fields, fieldInfo)
		info.byName[field.Name] = fieldInfo
	}
	return nil
}

func collectEnum(decl *ast.EnumDecl, env typeEnv) (*enumInfo, error) {
	if decl == nil {
		return nil, unsupported("source-layout", "nil enum")
	}
	if diag := llvmNominalDeclHeaderDiagnostic("enum", decl.Name, llvmIsIdent(decl.Name), len(decl.Generics), len(decl.Methods)); diag.kind != "" {
		return nil, unsupported(diag.kind, diag.message)
	}
	info := &enumInfo{
		name:     decl.Name,
		typ:      llvmEnumStorageType(decl.Name, false),
		decl:     decl,
		variants: map[string]variantInfo{},
	}
	for i, variant := range decl.Variants {
		if variant == nil {
			return nil, unsupportedf("source-layout", "enum %q has nil variant", decl.Name)
		}
		if !llvmIsIdent(variant.Name) {
			diag := llvmEnumVariantHeaderDiagnostic(decl.Name, variant.Name, false, len(variant.Fields), false)
			return nil, unsupported(diag.kind, diag.message)
		}
		payloads := make([]string, 0, len(variant.Fields))
		payloadListElemTyps := make([]string, 0, len(variant.Fields))
		payloadListElemTyp := ""
		for fi, field := range variant.Fields {
			typ, err := llvmEnumPayloadType(field, env)
			if err != nil {
				diag := llvmEnumPayloadDiagnostic(decl.Name, variant.Name, unsupportedMessage(err), "", "")
				return nil, unsupported(diag.kind, diag.message)
			}
			payloads = append(payloads, typ)
			if fi >= len(info.payloadSlotTypes) {
				info.payloadSlotTypes = append(info.payloadSlotTypes, typ)
			} else if info.payloadSlotTypes[fi] != typ {
				info.isBoxed = true
			}
			slotListElemTyp := ""
			if listElemTyp, ok, err := llvmListElementType(field, env); err != nil {
				diag := llvmEnumPayloadDiagnostic(decl.Name, variant.Name, unsupportedMessage(err), "", "")
				return nil, unsupported(diag.kind, diag.message)
			} else if ok {
				slotListElemTyp = listElemTyp
			}
			payloadListElemTyps = append(payloadListElemTyps, slotListElemTyp)
			if fi == 0 {
				payloadListElemTyp = slotListElemTyp
			}
			info.hasPayload = true
		}
		if len(variant.Fields) > info.payloadCount {
			info.payloadCount = len(variant.Fields)
		}
		if _, exists := info.variants[variant.Name]; exists {
			diag := llvmEnumVariantHeaderDiagnostic(decl.Name, variant.Name, true, len(variant.Fields), true)
			return nil, unsupported(diag.kind, diag.message)
		}
		info.variants[variant.Name] = variantInfo{
			name:                variant.Name,
			tag:                 i,
			payloads:            payloads,
			payloadListElemTyp:  payloadListElemTyp,
			payloadListElemTyps: payloadListElemTyps,
		}
	}
	if info.isBoxed {
		info.payloadSlotTypes = nil
		info.payloadTyp = ""
		for _, decl := range decl.Variants {
			if decl == nil {
				continue
			}
			v, ok := info.variants[decl.Name]
			if !ok {
				continue
			}
			if len(v.payloads) <= 1 {
				continue
			}
			for _, ptyp := range v.payloads {
				if ptyp == "ptr" {
					diag := llvmEnumBoxedMultiFieldDiagnostic(info.name, v.name, len(v.payloads))
					return nil, unsupported(diag.kind, diag.message)
				}
			}
		}
	} else if len(info.payloadSlotTypes) > 0 {
		info.payloadTyp = info.payloadSlotTypes[0]
		for _, t := range info.payloadSlotTypes {
			if t != info.payloadSlotTypes[0] {
				info.payloadTyp = ""
				break
			}
		}
	}
	info.typ = llvmEnumStorageType(info.name, info.hasPayload)
	return info, nil
}

func signatureOf(fn *ast.FnDecl, ownerName string, env typeEnv) (*fnSig, error) {
	if fn == nil {
		return nil, unsupported("source-layout", "nil function")
	}
	if diag := llvmFunctionHeaderDiagnostic(
		fn.Name,
		llvmIsIdent(fn.Name),
		false,
		len(fn.Generics),
		fn.Body != nil,
		fn.Name == "main",
		len(fn.Params),
		fn.ReturnType != nil,
	); diag.kind != "" {
		return nil, unsupported(diag.kind, diag.message)
	}
	sig := &fnSig{name: fn.Name, irName: fn.Name, decl: fn}
	if fn.Recv != nil {
		ownerType, ok := llvmMethodOwnerType(ownerName, env.structs, env.enums)
		if !ok {
			return nil, unsupportedf("type-system", "unknown method receiver owner %q", ownerName)
		}
		sig.irName = llvmMethodIRName(ownerName, fn.Name)
		sig.receiverType = ownerType
		sig.receiverMut = fn.Recv.Mut
		selfInfo := paramInfo{
			name:    "self",
			typ:     ownerType,
			irTyp:   llvmMethodReceiverIRType(ownerType, fn.Recv.Mut),
			mutable: fn.Recv.Mut,
			byRef:   fn.Recv.Mut,
		}
		// Option B Phase 2d: when the owner struct/enum is a
		// specialized stdlib built-in (Map / List / Set / Option /
		// Result), the `self` binding must carry the surface-level
		// map/list/set metadata so intrinsic dispatch inside the
		// method body (self.len(), self.get(k), self.insert(k, v),
		// …) recognizes the receiver and routes to the osty_rt_*
		// runtime helpers. Without this, those calls fall through
		// to user method dispatch on the mangled struct name and
		// fail because the intrinsic methods were stripped by the
		// Phase 2b AST bridge.
		if info, ok := specializedBuiltinMetaFor(ownerName); ok {
			selfInfo.sourceType = info.sourceType
			selfInfo.listElemTyp = info.listElemTyp
			selfInfo.listElemString = info.listElemString
			selfInfo.mapKeyTyp = info.mapKeyTyp
			selfInfo.mapValueTyp = info.mapValueTyp
			selfInfo.mapKeyString = info.mapKeyString
			selfInfo.setElemTyp = info.setElemTyp
			selfInfo.setElemString = info.setElemString
			// Specialized built-in containers are runtime aggregates
			// (opaque ptr to an osty_rt_map / list / set handle), not
			// LLVM struct values. Override the receiver's LLVM type
			// to "ptr" so intrinsic dispatch (which requires
			// baseInfo.typ == "ptr") fires for self.len(), self.get(k),
			// etc. The original %_ZTS… struct type would block that
			// check even though at the runtime ABI level the
			// container is already a ptr.
			selfInfo.typ = "ptr"
			selfInfo.irTyp = "ptr"
		}
		sig.params = append(sig.params, selfInfo)
	}
	if fn.Name == "main" {
		return sig, nil
	}
	if fn.ReturnType == nil {
		sig.ret = "void"
	} else {
		ret, err := llvmType(fn.ReturnType, env)
		if err != nil {
			diag := llvmFunctionReturnDiagnostic(fn.Name, unsupportedMessage(err))
			return nil, unsupported(diag.kind, diag.message)
		}
		sig.ret = ret
	}
	for _, p := range fn.Params {
		if diag := llvmFunctionParamDiagnostic(fn.Name, p.Name, p.Pattern != nil || p.Name == "", false, true, ""); diag.kind != "" {
			return nil, unsupported(diag.kind, diag.message)
		}
		if p.Default != nil {
			diag := llvmFunctionParamDiagnostic(fn.Name, p.Name, false, true, true, "")
			return nil, unsupported(diag.kind, diag.message)
		}
		if !llvmIsIdent(p.Name) {
			diag := llvmFunctionParamDiagnostic(fn.Name, p.Name, false, false, false, "")
			return nil, unsupported(diag.kind, diag.message)
		}
		typ, err := llvmType(p.Type, env)
		if err != nil {
			diag := llvmFunctionParamDiagnostic(fn.Name, p.Name, false, false, true, unsupportedMessage(err))
			return nil, unsupported(diag.kind, diag.message)
		}
		meta, err := containerMetadataFromSourceType(p.Type, env)
		if err != nil {
			diag := llvmFunctionParamDiagnostic(fn.Name, p.Name, false, false, true, unsupportedMessage(err))
			return nil, unsupported(diag.kind, diag.message)
		}
		info := paramInfo{name: p.Name, typ: typ}
		meta.applyToParam(&info)
		sig.params = append(sig.params, info)
	}
	retMeta, err := containerMetadataFromSourceType(fn.ReturnType, env)
	if err != nil {
		return nil, unsupportedf("type-system", "function %q return type: %s", fn.Name, unsupportedMessage(err))
	}
	retMeta.applyToReturn(sig)
	return sig, nil
}

func (g *generator) emitScriptMain(stmts []ast.Stmt) (string, error) {
	g.beginFunction()
	g.emitEnvArgsPrologue()
	if err := g.emitBlock(stmts); err != nil {
		return "", err
	}
	if g.currentReachable {
		if err := g.emitAllPendingDefers(); err != nil {
			return "", err
		}
	}
	if g.currentReachable {
		emitter := g.toOstyEmitter()
		g.releaseGCRoots(emitter)
		llvmReturnI32Zero(emitter)
		g.takeOstyEmitter(emitter)
	}
	return g.renderFunction("i32", "main", g.mainEntryParams()), nil
}

func (g *generator) emitMainFunction(sig *fnSig) (string, error) {
	g.beginFunction()
	g.emitEnvArgsPrologue()
	if err := g.emitBlock(sig.decl.Body.Stmts); err != nil {
		return "", err
	}
	if g.currentReachable {
		if err := g.emitAllPendingDefers(); err != nil {
			return "", err
		}
	}
	if g.currentReachable {
		emitter := g.toOstyEmitter()
		g.releaseGCRoots(emitter)
		llvmReturnI32Zero(emitter)
		g.takeOstyEmitter(emitter)
	}
	return g.renderFunction("i32", "main", g.mainEntryParams()), nil
}

const (
	ostyEnvArgsArgcParam = "osty_env_argc"
	ostyEnvArgsArgvParam = "osty_env_argv"
)

// mainEntryParams widens `main` to `(i32 argc, ptr argv)` when a
// package imports `std.env` so the prologue can hand raw argv to the
// env runtime. Without the import we keep the bare signature so
// smoke-test IR snapshots stay stable.
func (g *generator) mainEntryParams() []paramInfo {
	if len(g.stdEnvAliases) == 0 {
		return nil
	}
	return []paramInfo{
		{name: ostyEnvArgsArgcParam, typ: "i32", irTyp: "i32"},
		{name: ostyEnvArgsArgvParam, typ: "ptr", irTyp: "ptr"},
	}
}

// emitEnvArgsPrologue runs right after beginFunction so the
// osty_rt_env_args_init call precedes any user statement that could
// reach env.args(). The sext to i64 bridges C's `int argc` to the
// runtime ABI declared below.
func (g *generator) emitEnvArgsPrologue() {
	if len(g.stdEnvAliases) == 0 {
		return
	}
	g.declareRuntimeSymbol(ostyRtEnvArgsInitSymbol, "void", []paramInfo{
		{typ: "i64"}, {typ: "ptr"},
	})
	argcI64 := fmt.Sprintf("%%%s.i64", ostyEnvArgsArgcParam)
	g.body = append(g.body,
		fmt.Sprintf("  %s = sext i32 %%%s to i64", argcI64, ostyEnvArgsArgcParam),
		fmt.Sprintf("  call void @%s(i64 %s, ptr %%%s)", ostyRtEnvArgsInitSymbol, argcI64, ostyEnvArgsArgvParam),
	)
}

// fnHasAnnotation reports whether the function declaration carries an
// annotation with the given name. Safe on nil declarations — returns
// false.
func fnHasAnnotation(decl *ast.FnDecl, name string) bool {
	if decl == nil {
		return false
	}
	for _, a := range decl.Annotations {
		if a != nil && a.Name == name {
			return true
		}
	}
	return false
}

// fnFindAnnotation returns the first annotation with the given name, or
// nil if the declaration has no such annotation.
func fnFindAnnotation(decl *ast.FnDecl, name string) *ast.Annotation {
	if decl == nil {
		return nil
	}
	for _, a := range decl.Annotations {
		if a != nil && a.Name == name {
			return a
		}
	}
	return nil
}

// readLoopHints pulls the v0.6 loop-optimization hint set off the
// reified `*ast.FnDecl` into generator state so emitFor et al. can
// attach metadata without re-walking annotations on every back-edge.
// Called once per function body at emitUserFunction entry. The args
// on `#[vectorize(...)]` / `#[unroll(...)]` come already-validated
// from the resolver, so numeric parses here can assume well-formed
// shape and silently fall back to 0 on anything unexpected.
func (g *generator) readLoopHints(decl *ast.FnDecl) {
	// v0.6 A5.2: vectorize is default-on. Start with the hint enabled
	// and let `#[no_vectorize]` flip it off. The `#[vectorize(...)]`
	// annotation is still read for tuning args (width, scalable,
	// predicate) that refine the default-on behavior.
	g.vectorizeHint = true
	if fnHasAnnotation(decl, "no_vectorize") {
		g.vectorizeHint = false
	}
	if vec := fnFindAnnotation(decl, "vectorize"); vec != nil {
		for _, arg := range vec.Args {
			if arg == nil {
				continue
			}
			switch arg.Key {
			case "scalable":
				g.vectorizeScalable = true
			case "predicate":
				g.vectorizePredicate = true
			case "width":
				if lit, ok := arg.Value.(*ast.IntLit); ok {
					if v, err := strconv.Atoi(strings.ReplaceAll(lit.Text, "_", "")); err == nil && v > 0 {
						g.vectorizeWidth = v
					}
				}
			}
		}
	}
	if fnHasAnnotation(decl, "parallel") {
		g.parallelHint = true
		g.allocParallelAccessGroup()
	}
	if u := fnFindAnnotation(decl, "unroll"); u != nil {
		g.unrollHint = true
		for _, arg := range u.Args {
			if arg == nil || arg.Key != "count" {
				continue
			}
			if lit, ok := arg.Value.(*ast.IntLit); ok {
				if v, err := strconv.Atoi(strings.ReplaceAll(lit.Text, "_", "")); err == nil && v > 0 {
					g.unrollCount = v
				}
			}
		}
	}
}

func (g *generator) emitUserFunction(sig *fnSig) (string, error) {
	g.beginFunction()
	g.readLoopHints(sig.decl)
	g.returnType = sig.ret
	g.returnSourceType = sig.returnSourceType
	g.returnListElemTyp = sig.retListElemTyp
	g.returnListElemString = sig.retListString
	g.returnMapKeyTyp = sig.retMapKeyTyp
	g.returnMapValueTyp = sig.retMapValueTyp
	g.returnMapKeyString = sig.retMapKeyString
	g.returnSetElemTyp = sig.retSetElemTyp
	g.returnSetElemString = sig.retSetElemString
	for _, p := range sig.params {
		v := value{
			typ:            p.typ,
			ref:            "%" + p.name,
			listElemTyp:    p.listElemTyp,
			listElemString: p.listElemString,
			mapKeyTyp:      p.mapKeyTyp,
			mapValueTyp:    p.mapValueTyp,
			mapKeyString:   p.mapKeyString,
			setElemTyp:     p.setElemTyp,
			setElemString:  p.setElemString,
			sourceType:     p.sourceType,
		}
		// Phase 3: fn-typed parameter. The value arrives as a ptr to
		// a closure env (same uniform ABI as phase 1), so we tag the
		// binding with a synthesised *fnSig so subsequent `p(args)`
		// inside the body dispatches through emitIndirectUserCall.
		if fsig, ok, err := synthFnSigFromSourceType(p.sourceType, g.typeEnv()); err != nil {
			return "", err
		} else if ok {
			v.fnSigRef = fsig
		}
		v.gcManaged = valueNeedsManagedRoot(v)
		v.rootPaths = g.rootPathsForType(p.typ)
		if p.byRef {
			v.ptr = true
			v.mutable = p.mutable
			g.bindLocal(p.name, v)
			continue
		}
		g.bindNamedLocal(p.name, v, p.mutable)
	}
	if sig.ret == "void" {
		if err := g.emitBlock(sig.decl.Body.Stmts); err != nil {
			return "", err
		}
		if g.currentReachable {
			if err := g.emitAllPendingDefers(); err != nil {
				return "", err
			}
		}
		if g.currentReachable {
			emitter := g.toOstyEmitter()
			g.releaseGCRoots(emitter)
			emitter.body = append(emitter.body, "  ret void")
			g.takeOstyEmitter(emitter)
		}
	} else {
		if err := g.emitReturningBlock(sig.decl.Body.Stmts, sig.ret, sig.returnSourceType, sig.retListElemTyp, sig.retListString, sig.retMapKeyTyp, sig.retMapValueTyp, sig.retMapKeyString, sig.retSetElemTyp, sig.retSetElemString); err != nil {
			return "", err
		}
	}
	return g.renderFunction(sig.ret, sig.irName, sig.params), nil
}

func (g *generator) emitGlobalLets(globals []*globalLetInfo) error {
	for _, info := range globals {
		if info == nil || info.decl == nil {
			return unsupported("source-layout", "nil top-level let")
		}
		if info.decl.Value == nil {
			return unsupportedf("source-layout", "top-level let %q has no value", info.name)
		}
		cv, err := g.constExpr(info.decl.Value)
		if err != nil {
			return unsupportedf("source-layout", "top-level let %q initializer: %s", info.name, unsupportedMessage(err))
		}
		typ := cv.typ
		listElemTyp := ""
		if info.decl.Type != nil {
			declTyp, err := llvmType(info.decl.Type, g.typeEnv())
			if err != nil {
				return err
			}
			if declTyp != cv.typ {
				return unsupportedf("type-system", "top-level let %q type %s, value %s", info.name, declTyp, cv.typ)
			}
			typ = declTyp
			if elemTyp, ok, err := llvmListElementType(info.decl.Type, g.typeEnv()); err != nil {
				return err
			} else if ok {
				listElemTyp = elemTyp
			}
		}
		kind := "constant"
		if info.mutable {
			kind = "global"
		}
		g.globalDefs = append(g.globalDefs, fmt.Sprintf("%s = internal %s %s %s", info.irName, kind, typ, cv.init))
		g.globals[info.name] = value{
			typ:         typ,
			ref:         info.irName,
			ptr:         true,
			mutable:     info.mutable,
			listElemTyp: listElemTyp,
		}
		g.globalConsts[info.name] = cv
	}
	return nil
}

func (g *generator) constExpr(expr ast.Expr) (constValue, error) {
	switch e := expr.(type) {
	case *ast.IntLit:
		text := strings.ReplaceAll(e.Text, "_", "")
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return constValue{}, unsupportedf("expression", "invalid Int literal %q", e.Text)
		}
		return constValue{typ: "i64", init: strconv.FormatInt(n, 10), kind: constKindInt, intValue: n}, nil
	case *ast.FloatLit:
		text := strings.ReplaceAll(e.Text, "_", "")
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return constValue{}, unsupportedf("expression", "invalid Float literal %q", e.Text)
		}
		return constValue{typ: "double", init: llvmFloatConstLiteral(f), kind: constKindFloat, floatValue: f}, nil
	case *ast.BoolLit:
		if e.Value {
			return constValue{typ: "i1", init: "true", kind: constKindBool, boolValue: true}, nil
		}
		return constValue{typ: "i1", init: "false", kind: constKindBool, boolValue: false}, nil
	case *ast.StringLit:
		text, ok := plainStringLiteral(e)
		if !ok {
			return constValue{}, unsupported("expression", "interpolated String literals are not supported by LLVM")
		}
		if !llvmIsAsciiStringText(text) {
			return constValue{}, unsupported("type-system", "plain String literals currently require ASCII text with printable bytes or newline, tab, and carriage-return escapes")
		}
		emitter := g.toOstyEmitter()
		out := llvmStringLiteral(emitter, text)
		g.takeOstyEmitter(emitter)
		return constValue{typ: "ptr", init: out.name, kind: constKindString, stringValue: text}, nil
	case *ast.Ident:
		if cv, ok := g.globalConsts[e.Name]; ok {
			return cv, nil
		}
		if v, found, err := g.constEnumVariantIdent(e.Name); found || err != nil {
			return v, err
		}
		return constValue{}, unsupportedf("name", "unknown identifier %q", e.Name)
	case *ast.ParenExpr:
		return g.constExpr(e.X)
	case *ast.UnaryExpr:
		return g.constUnary(e)
	case *ast.BinaryExpr:
		return g.constBinary(e)
	case *ast.StructLit:
		return g.constStructLit(e)
	case *ast.FieldExpr:
		if e.IsOptional {
			return constValue{}, unsupported("expression", "optional field access is not supported")
		}
		ref, ok := g.enumVariantByField(e)
		if !ok {
			return constValue{}, unsupportedf("expression", "constant field expression %T", expr)
		}
		return g.constEnumVariant(ref.enum, ref.variant, nil)
	case *ast.CallExpr:
		ref, found, err := g.enumVariantCallTarget(e)
		if !found || err != nil {
			if err != nil {
				return constValue{}, err
			}
			return constValue{}, unsupportedf("expression", "constant expression %T", expr)
		}
		if len(ref.variant.payloads) == 0 {
			return g.constEnumVariant(ref.enum, ref.variant, nil)
		}
		if len(e.Args) != 1 || e.Args[0] == nil || e.Args[0].Name != "" || e.Args[0].Value == nil {
			return constValue{}, unsupportedf("call", "enum variant %q requires positional payload", ref.variant.name)
		}
		payload, err := g.constExpr(e.Args[0].Value)
		if err != nil {
			return constValue{}, err
		}
		return g.constEnumVariant(ref.enum, ref.variant, &payload)
	default:
		return constValue{}, unsupportedf("expression", "constant expression %T", expr)
	}
}

func (g *generator) constUnary(expr *ast.UnaryExpr) (constValue, error) {
	v, err := g.constExpr(expr.X)
	if err != nil {
		return constValue{}, err
	}
	switch expr.Op {
	case token.PLUS:
		if v.kind != constKindInt && v.kind != constKindFloat {
			return constValue{}, unsupportedf("type-system", "unary plus on %s", v.typ)
		}
		return v, nil
	case token.MINUS:
		switch v.kind {
		case constKindInt:
			n := -v.intValue
			return constValue{typ: "i64", init: strconv.FormatInt(n, 10), kind: constKindInt, intValue: n}, nil
		case constKindFloat:
			f := -v.floatValue
			return constValue{typ: "double", init: llvmFloatConstLiteral(f), kind: constKindFloat, floatValue: f}, nil
		default:
			return constValue{}, unsupportedf("type-system", "unary minus on %s", v.typ)
		}
	case token.NOT:
		if v.kind != constKindBool {
			return constValue{}, unsupportedf("type-system", "logical not on %s", v.typ)
		}
		return constValue{typ: "i1", init: strconv.FormatBool(!v.boolValue), kind: constKindBool, boolValue: !v.boolValue}, nil
	case token.BITNOT:
		if v.kind != constKindInt {
			return constValue{}, unsupportedf("type-system", "bitwise not on %s", v.typ)
		}
		n := ^v.intValue
		return constValue{typ: "i64", init: strconv.FormatInt(n, 10), kind: constKindInt, intValue: n}, nil
	default:
		return constValue{}, unsupportedf("expression", "unary operator %q", expr.Op)
	}
}

func (g *generator) constBinary(expr *ast.BinaryExpr) (constValue, error) {
	left, err := g.constExpr(expr.Left)
	if err != nil {
		return constValue{}, err
	}
	right, err := g.constExpr(expr.Right)
	if err != nil {
		return constValue{}, err
	}
	if llvmIsCompareOp(expr.Op.String()) {
		return constCompare(expr.Op, left, right)
	}
	if expr.Op == token.AND || expr.Op == token.OR {
		if left.kind != constKindBool || right.kind != constKindBool {
			return constValue{}, unsupportedf("type-system", "logical operator %q on %s/%s", expr.Op, left.typ, right.typ)
		}
		value := left.boolValue && right.boolValue
		if expr.Op == token.OR {
			value = left.boolValue || right.boolValue
		}
		return constValue{typ: "i1", init: strconv.FormatBool(value), kind: constKindBool, boolValue: value}, nil
	}
	if left.kind == constKindFloat && right.kind == constKindFloat {
		f, err := constFloatBinary(expr.Op, left.floatValue, right.floatValue)
		if err != nil {
			return constValue{}, err
		}
		return constValue{typ: "double", init: llvmFloatConstLiteral(f), kind: constKindFloat, floatValue: f}, nil
	}
	if left.kind != constKindInt || right.kind != constKindInt {
		return constValue{}, unsupportedf("type-system", "binary operator %q on %s/%s", expr.Op, left.typ, right.typ)
	}
	n, err := constIntBinary(expr.Op, left.intValue, right.intValue)
	if err != nil {
		return constValue{}, err
	}
	return constValue{typ: "i64", init: strconv.FormatInt(n, 10), kind: constKindInt, intValue: n}, nil
}

func (g *generator) constStructLit(lit *ast.StructLit) (constValue, error) {
	info, typeName, err := g.structInfoForExpr(lit.Type)
	if err != nil {
		return constValue{}, err
	}
	if lit.Spread != nil {
		return constValue{}, unsupportedf("expression", "struct %q spread literal", typeName)
	}
	fields := map[string]*ast.StructLitField{}
	for _, field := range lit.Fields {
		if field == nil {
			return constValue{}, unsupportedf("expression", "struct %q has nil literal field", typeName)
		}
		if !llvmIsIdent(field.Name) {
			return constValue{}, unsupportedf("name", "struct %q literal field name %q", typeName, field.Name)
		}
		if _, exists := fields[field.Name]; exists {
			return constValue{}, unsupportedf("expression", "struct %q duplicate literal field %q", typeName, field.Name)
		}
		if _, exists := info.byName[field.Name]; !exists {
			return constValue{}, unsupportedf("expression", "struct %q unknown literal field %q", typeName, field.Name)
		}
		fields[field.Name] = field
	}
	parts := make([]string, 0, len(info.fields))
	for _, field := range info.fields {
		litField := fields[field.name]
		if litField == nil {
			return constValue{}, unsupportedf("expression", "struct %q missing literal field %q", typeName, field.name)
		}
		var cv constValue
		if litField.Value == nil {
			var ok bool
			cv, ok = g.globalConsts[litField.Name]
			if !ok {
				return constValue{}, unsupportedf("name", "unknown identifier %q", litField.Name)
			}
		} else {
			cv, err = g.constExpr(litField.Value)
			if err != nil {
				return constValue{}, err
			}
		}
		if cv.typ != field.typ {
			return constValue{}, unsupportedf("type-system", "struct %q field %q type %s, value %s", typeName, field.name, field.typ, cv.typ)
		}
		parts = append(parts, cv.typedInit())
	}
	return constValue{typ: info.typ, init: fmt.Sprintf("{ %s }", strings.Join(parts, ", "))}, nil
}

func (g *generator) constEnumVariantIdent(name string) (constValue, bool, error) {
	found, count := g.findBareEnumVariant(name)
	if count == 0 {
		return constValue{}, false, nil
	}
	if count > 1 {
		return constValue{}, true, unsupportedf("name", "ambiguous enum variant %q", name)
	}
	out, err := g.constEnumVariant(found.enum, found.variant, nil)
	return out, true, err
}

func (g *generator) constEnumVariant(info *enumInfo, variant variantInfo, payload *constValue) (constValue, error) {
	if info.isBoxed {
		return constValue{}, unsupportedf("type-system", "enum %q has heterogeneous (boxed) payloads; variants cannot be used in a constant expression because construction requires a runtime GC allocation. Use a constructor function (e.g. `fn makeX() -> %s { ... }`) and call it at runtime.", info.name, info.name)
	}
	if info.hasPayload {
		payloadValue := constValue{typ: info.payloadTyp, init: llvmZeroLiteral(info.payloadTyp)}
		if payload != nil {
			if payload.typ != info.payloadTyp {
				return constValue{}, unsupportedf("type-system", "enum %q variant %q payload type %s, want %s", info.name, variant.name, payload.typ, info.payloadTyp)
			}
			payloadValue = *payload
		} else if len(variant.payloads) != 0 {
			return constValue{}, unsupportedf("expression", "enum variant %q requires a payload", variant.name)
		}
		return constValue{
			typ:  info.typ,
			init: fmt.Sprintf("{ i64 %d, %s }", variant.tag, payloadValue.typedInit()),
		}, nil
	}
	if payload != nil {
		return constValue{}, unsupportedf("expression", "enum %q has no payload layout", info.name)
	}
	return constValue{typ: "i64", init: strconv.Itoa(variant.tag), kind: constKindInt, intValue: int64(variant.tag)}, nil
}

func constCompare(op token.Kind, left, right constValue) (constValue, error) {
	if left.typ != right.typ {
		return constValue{}, unsupportedf("type-system", "compare type mismatch %s/%s", left.typ, right.typ)
	}
	switch left.kind {
	case constKindInt:
		return constBoolCompare(op, left.intValue, right.intValue)
	case constKindFloat:
		return constBoolCompare(op, left.floatValue, right.floatValue)
	case constKindBool:
		return constBoolCompare(op, left.boolValue, right.boolValue)
	case constKindString:
		if op != token.EQ && op != token.NEQ {
			return constValue{}, unsupportedf("type-system", "compare type %s", left.typ)
		}
		value := left.stringValue == right.stringValue
		if op == token.NEQ {
			value = !value
		}
		return constValue{typ: "i1", init: strconv.FormatBool(value), kind: constKindBool, boolValue: value}, nil
	default:
		return constValue{}, unsupportedf("type-system", "compare type %s", left.typ)
	}
}

func constBoolCompare[T comparable](op token.Kind, left, right T) (constValue, error) {
	var value bool
	switch any(left).(type) {
	case int64:
		l := any(left).(int64)
		r := any(right).(int64)
		switch op {
		case token.EQ:
			value = l == r
		case token.NEQ:
			value = l != r
		case token.LT:
			value = l < r
		case token.LEQ:
			value = l <= r
		case token.GT:
			value = l > r
		case token.GEQ:
			value = l >= r
		default:
			return constValue{}, unsupportedf("expression", "comparison operator %q", op)
		}
	case float64:
		l := any(left).(float64)
		r := any(right).(float64)
		switch op {
		case token.EQ:
			value = l == r
		case token.NEQ:
			value = l != r
		case token.LT:
			value = l < r
		case token.LEQ:
			value = l <= r
		case token.GT:
			value = l > r
		case token.GEQ:
			value = l >= r
		default:
			return constValue{}, unsupportedf("expression", "comparison operator %q", op)
		}
	case bool:
		l := any(left).(bool)
		r := any(right).(bool)
		switch op {
		case token.EQ:
			value = l == r
		case token.NEQ:
			value = l != r
		default:
			return constValue{}, unsupportedf("expression", "comparison operator %q", op)
		}
	default:
		return constValue{}, unsupportedf("expression", "comparison operator %q", op)
	}
	return constValue{typ: "i1", init: strconv.FormatBool(value), kind: constKindBool, boolValue: value}, nil
}

func constIntBinary(op token.Kind, left, right int64) (int64, error) {
	switch op {
	case token.PLUS:
		return left + right, nil
	case token.MINUS:
		return left - right, nil
	case token.STAR:
		return left * right, nil
	case token.SLASH:
		if right == 0 {
			return 0, unsupported("expression", "constant Int division by zero")
		}
		return left / right, nil
	case token.PERCENT:
		if right == 0 {
			return 0, unsupported("expression", "constant Int modulo by zero")
		}
		return left % right, nil
	case token.BITAND:
		return left & right, nil
	case token.BITOR:
		return left | right, nil
	case token.BITXOR:
		return left ^ right, nil
	case token.SHL:
		return left << uint(right), nil
	case token.SHR:
		return left >> uint(right), nil
	default:
		return 0, unsupportedf("expression", "binary operator %q", op)
	}
}

func constFloatBinary(op token.Kind, left, right float64) (float64, error) {
	switch op {
	case token.PLUS:
		return left + right, nil
	case token.MINUS:
		return left - right, nil
	case token.STAR:
		return left * right, nil
	case token.SLASH:
		switch {
		case right == 0 && left == 0:
			return math.NaN(), nil
		case right == 0 && left > 0:
			return math.Inf(1), nil
		case right == 0 && left < 0:
			return math.Inf(-1), nil
		default:
			return left / right, nil
		}
	default:
		return 0, unsupportedf("expression", "binary operator %q", op)
	}
}

func llvmFloatConstLiteral(value float64) string {
	switch {
	case math.IsNaN(value), math.IsInf(value, 0):
		return fmt.Sprintf("0x%016X", math.Float64bits(value))
	default:
		return strconv.FormatFloat(value, 'e', 16, 64)
	}
}

func llvmGlobalIRName(name string) string {
	return "@osty_global_" + sanitizeLLVMName(name)
}
