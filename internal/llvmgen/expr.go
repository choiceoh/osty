// expr.go — value-position emission: emitExpr dispatcher, literals / idents /
// binary·unary·compare / Optional `?`, if-expr / if-let-expr / block-value /
// match-expr (tag + payload + guarded + select-safe variants), struct /
// field / tuple / index / list / map / set literals and methods, user calls
// (including optional receivers and builtin Result constructors), aggregate
// ABI helpers, enum-variant access, and testing value-returning calls.
//
// Also owns `ifLetCondition`, the shared helper used by both stmt (emitIfLetStmt)
// and expr (emitIfLetExprValue).
//
// NOTE(osty-migration): value emission is also AST-bound. After the
// IR-direct migration completes, this file's logic moves wholesale into
// toolchain/llvmgen.osty and consumes IrNode from toolchain/ir.osty.
package llvmgen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/token"
)

func (g *generator) ifLetCondition(pattern ast.Pattern, scrutinee value) (*LlvmValue, func() error, error) {
	if pattern == nil {
		return nil, nil, unsupported("control-flow", "if-let requires a pattern")
	}
	if _, ok := pattern.(*ast.WildcardPat); ok {
		return toOstyValue(value{typ: "i1", ref: "true"}), func() error { return nil }, nil
	}
	if info := g.enumsByType[scrutinee.typ]; info != nil && info.hasPayload {
		matched, ok, err := g.matchPayloadEnumPattern(info, pattern)
		if err != nil {
			return nil, nil, err
		}
		if !ok {
			return nil, nil, unsupported("control-flow", "if-let pattern must be an enum variant")
		}
		emitter := g.toOstyEmitter()
		tag := llvmExtractValue(emitter, toOstyValue(scrutinee), "i64", 0)
		cond := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: strconv.Itoa(matched.variant.tag)}))
		g.takeOstyEmitter(emitter)
		return cond, func() error {
			return g.bindPayloadEnumPattern(scrutinee, matched)
		}, nil
	}
	tag, ok, err := g.matchEnumTag(pattern)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return nil, nil, unsupported("control-flow", "if-let pattern must be an enum variant")
	}
	emitter := g.toOstyEmitter()
	cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(value{typ: "i64", ref: strconv.Itoa(tag)}))
	g.takeOstyEmitter(emitter)
	return cond, func() error { return nil }, nil
}

func (g *generator) emitStringLiteral(lit *ast.StringLit) (value, error) {
	if lit == nil {
		return value{}, unsupported("expression", "nil String literal")
	}
	allLit := true
	for _, part := range lit.Parts {
		if !part.IsLit {
			allLit = false
			break
		}
	}
	if allLit {
		var b strings.Builder
		for _, part := range lit.Parts {
			b.WriteString(part.Lit)
		}
		text := b.String()
		if !llvmIsAsciiStringText(text) {
			return value{}, unsupported("type-system", "plain String literals currently require ASCII text with printable bytes or newline, tab, and carriage-return escapes")
		}
		emitter := g.toOstyEmitter()
		out := llvmStringLiteral(emitter, text)
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), nil
	}
	return g.emitInterpolatedString(lit)
}

func (g *generator) emitInterpolatedString(lit *ast.StringLit) (value, error) {
	if len(lit.Parts) == 0 {
		emitter := g.toOstyEmitter()
		out := llvmStringLiteral(emitter, "")
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), nil
	}
	pieces := make([]value, 0, len(lit.Parts))
	for _, part := range lit.Parts {
		if part.IsLit {
			if !llvmIsAsciiStringText(part.Lit) {
				return value{}, unsupported("type-system", "plain String literals currently require ASCII text with printable bytes or newline, tab, and carriage-return escapes")
			}
			emitter := g.toOstyEmitter()
			out := llvmStringLiteral(emitter, part.Lit)
			g.takeOstyEmitter(emitter)
			pieces = append(pieces, fromOstyValue(out))
			continue
		}
		v, err := g.emitExpr(part.Expr)
		if err != nil {
			return value{}, err
		}
		if v.typ != "ptr" || v.listElemTyp != "" || v.mapKeyTyp != "" {
			return value{}, unsupportedf("type-system", "interpolation of %s value requires .toString() which the LLVM backend does not yet lower", v.typ)
		}
		pieces = append(pieces, v)
	}
	result := pieces[0]
	for i := 1; i < len(pieces); i++ {
		r, err := g.emitRuntimeStringConcat(result, pieces[i])
		if err != nil {
			return value{}, err
		}
		result = r
	}
	return result, nil
}

func (g *generator) emitExpr(expr ast.Expr) (value, error) {
	switch e := expr.(type) {
	case *ast.IntLit:
		text := strings.ReplaceAll(e.Text, "_", "")
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return value{}, unsupportedf("expression", "invalid Int literal %q", e.Text)
		}
		return value{typ: "i64", ref: strconv.FormatInt(n, 10)}, nil
	case *ast.FloatLit:
		text := strings.ReplaceAll(e.Text, "_", "")
		f, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return value{}, unsupportedf("expression", "invalid Float literal %q", e.Text)
		}
		out := llvmFloatLiteral(strconv.FormatFloat(f, 'e', 16, 64))
		return fromOstyValue(out), nil
	case *ast.BoolLit:
		if e.Value {
			return value{typ: "i1", ref: "true"}, nil
		}
		return value{typ: "i1", ref: "false"}, nil
	case *ast.StringLit:
		return g.emitStringLiteral(e)
	case *ast.Ident:
		return g.emitIdent(e.Name)
	case *ast.ParenExpr:
		return g.emitExpr(e.X)
	case *ast.QuestionExpr:
		return g.emitQuestionExpr(e)
	case *ast.UnaryExpr:
		return g.emitUnary(e)
	case *ast.BinaryExpr:
		return g.emitBinary(e)
	case *ast.CallExpr:
		return g.emitCall(e)
	case *ast.FieldExpr:
		return g.emitFieldExpr(e)
	case *ast.IndexExpr:
		return g.emitIndexExpr(e)
	case *ast.TupleExpr:
		return g.emitTupleExpr(e)
	case *ast.ListExpr:
		return g.emitListExprWithHint(e, "", false)
	case *ast.MapExpr:
		return g.emitMapExprWithHint(e, "", "", false)
	case *ast.StructLit:
		return g.emitStructLit(e)
	case *ast.IfExpr:
		return g.emitIfExprValue(e)
	case *ast.MatchExpr:
		return g.emitMatchExprValue(e)
	default:
		return value{}, unsupportedf("expression", "expression %T", expr)
	}
}

func (g *generator) emitIdent(name string) (value, error) {
	if v, ok := g.lookupBinding(name); ok {
		return g.loadIfPointer(v)
	}
	if v, found, err := g.enumVariantIdent(name); found || err != nil {
		return v, err
	}
	return value{}, unsupportedf("name", "unknown identifier %q", name)
}

func (g *generator) loadIfPointer(v value) (value, error) {
	if !v.ptr {
		return v, nil
	}
	emitter := g.toOstyEmitter()
	out := llvmLoad(emitter, toOstyValue(v))
	g.takeOstyEmitter(emitter)
	loaded := fromOstyValue(out)
	copyContainerMetadata(&loaded, v)
	loaded.rootPaths = cloneRootPaths(v.rootPaths)
	return loaded, nil
}

func (g *generator) loadTypedPointerValue(addr value, typ string) (value, error) {
	if addr.typ != "ptr" {
		return value{}, unsupportedf("type-system", "typed load from %s, want ptr", addr.typ)
	}
	emitter := g.toOstyEmitter()
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = load %s, ptr %s", tmp, typ, addr.ref))
	g.takeOstyEmitter(emitter)
	out := value{typ: typ, ref: tmp}
	out.rootPaths = g.rootPathsForType(typ)
	return out, nil
}

func (g *generator) emitOptionalPtrExpr(base value, then func() (value, error)) (value, error) {
	if base.typ != "ptr" {
		return value{}, unsupportedf("type-system", "optional receiver type %s, want ptr", base.typ)
	}
	emitter := g.toOstyEmitter()
	isNil := llvmCompare(emitter, "eq", toOstyValue(base), toOstyValue(value{typ: "ptr", ref: "null"}))
	thenLabel := llvmNextLabel(emitter, "optional.then")
	nilLabel := llvmNextLabel(emitter, "optional.nil")
	endLabel := llvmNextLabel(emitter, "optional.end")
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", isNil.name, nilLabel, thenLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", thenLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(thenLabel)

	thenValue, err := then()
	if err != nil {
		return value{}, err
	}
	if thenValue.typ != "ptr" {
		return value{}, unsupportedf("type-system", "optional branch type %s, want ptr", thenValue.typ)
	}
	thenPred := g.currentBlock
	if g.currentReachable {
		g.branchTo(endLabel)
	}

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", nilLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", endLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", endLabel))
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = phi ptr [ %s, %%%s ], [ null, %%%s ]", tmp, thenValue.ref, thenPred, nilLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)

	out := thenValue
	out.ref = tmp
	out.ptr = false
	out.mutable = false
	return out, nil
}

func (g *generator) emitOptionalPtrStmt(base value, then func() error) error {
	if base.typ != "ptr" {
		return unsupportedf("type-system", "optional receiver type %s, want ptr", base.typ)
	}
	emitter := g.toOstyEmitter()
	isNil := llvmCompare(emitter, "eq", toOstyValue(base), toOstyValue(value{typ: "ptr", ref: "null"}))
	thenLabel := llvmNextLabel(emitter, "optional.then")
	nilLabel := llvmNextLabel(emitter, "optional.nil")
	endLabel := llvmNextLabel(emitter, "optional.end")
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", isNil.name, nilLabel, thenLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", thenLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(thenLabel)
	if err := then(); err != nil {
		return err
	}
	if g.currentReachable {
		g.branchTo(endLabel)
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", nilLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", endLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", endLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)
	return nil
}

func (g *generator) registerTupleType(elemTypes []string, elemListElemTyps []string) tupleTypeInfo {
	info := tupleTypeInfo{
		typ:              llvmTupleTypeName(elemTypes),
		elems:            append([]string(nil), elemTypes...),
		elemListElemTyps: append([]string(nil), elemListElemTyps...),
	}
	if g.tupleTypes == nil {
		g.tupleTypes = map[string]tupleTypeInfo{}
	}
	if existing, ok := g.tupleTypes[info.typ]; ok {
		if len(existing.elemListElemTyps) == 0 && len(info.elemListElemTyps) != 0 {
			existing.elemListElemTyps = append([]string(nil), info.elemListElemTyps...)
			g.tupleTypes[info.typ] = existing
		}
		return existing
	}
	g.tupleTypes[info.typ] = info
	return info
}

func (g *generator) emitTupleExpr(expr *ast.TupleExpr) (value, error) {
	if expr == nil {
		return value{}, unsupported("expression", "nil tuple literal")
	}
	fields := make([]*LlvmValue, 0, len(expr.Elems))
	elemTypes := make([]string, 0, len(expr.Elems))
	elemListElemTyps := make([]string, 0, len(expr.Elems))
	for _, elem := range expr.Elems {
		v, err := g.emitExpr(elem)
		if err != nil {
			return value{}, err
		}
		fields = append(fields, toOstyValue(v))
		elemTypes = append(elemTypes, v.typ)
		elemListElemTyps = append(elemListElemTyps, v.listElemTyp)
	}
	info := g.registerTupleType(elemTypes, elemListElemTyps)
	emitter := g.toOstyEmitter()
	out := llvmStructLiteral(emitter, info.typ, fields)
	g.takeOstyEmitter(emitter)
	tupleValue := fromOstyValue(out)
	tupleValue.rootPaths = g.rootPathsForType(info.typ)
	return tupleValue, nil
}

func (g *generator) emitStructLit(lit *ast.StructLit) (value, error) {
	info, typeName, err := g.structInfoForExpr(lit.Type)
	if err != nil {
		return value{}, err
	}
	if lit.Spread != nil {
		return value{}, unsupportedf("expression", "struct %q spread literal", typeName)
	}
	fields := map[string]*ast.StructLitField{}
	for _, field := range lit.Fields {
		if field == nil {
			return value{}, unsupportedf("expression", "struct %q has nil literal field", typeName)
		}
		if !llvmIsIdent(field.Name) {
			return value{}, unsupportedf("name", "struct %q literal field name %q", typeName, field.Name)
		}
		if _, exists := fields[field.Name]; exists {
			return value{}, unsupportedf("expression", "struct %q duplicate literal field %q", typeName, field.Name)
		}
		if _, exists := info.byName[field.Name]; !exists {
			return value{}, unsupportedf("expression", "struct %q unknown literal field %q", typeName, field.Name)
		}
		fields[field.Name] = field
	}
	values := make([]*LlvmValue, 0, len(info.fields))
	for _, field := range info.fields {
		litField := fields[field.name]
		if litField == nil {
			return value{}, unsupportedf("expression", "struct %q missing literal field %q", typeName, field.name)
		}
		var v value
		if litField.Value == nil {
			v, err = g.emitIdent(litField.Name)
		} else {
			v, err = g.emitExprWithHintAndSourceType(litField.Value, field.sourceType, field.listElemTyp, field.listElemString, field.mapKeyTyp, field.mapValueTyp, field.mapKeyString, field.setElemTyp, field.setElemString)
		}
		if err != nil {
			return value{}, err
		}
		if v.typ != field.typ {
			return value{}, unsupportedf("type-system", "struct %q field %q type %s, value %s", typeName, field.name, field.typ, v.typ)
		}
		values = append(values, toOstyValue(v))
	}
	emitter := g.toOstyEmitter()
	out := llvmStructLiteral(emitter, info.typ, values)
	g.takeOstyEmitter(emitter)
	litValue := fromOstyValue(out)
	litValue.rootPaths = g.rootPathsForType(info.typ)
	return litValue, nil
}

func (g *generator) emitFieldExpr(expr *ast.FieldExpr) (value, error) {
	if expr.IsOptional {
		return g.emitOptionalFieldExpr(expr)
	}
	if v, found, err := g.enumVariantValue(expr); found || err != nil {
		return v, err
	}
	base, err := g.emitExpr(expr.X)
	if err != nil {
		return value{}, err
	}
	info := g.structsByType[base.typ]
	if info == nil {
		return value{}, unsupportedf("type-system", "field access on %s", base.typ)
	}
	field, ok := info.byName[expr.Name]
	if !ok {
		return value{}, unsupportedf("expression", "struct %q has no field %q", info.name, expr.Name)
	}
	emitter := g.toOstyEmitter()
	out := llvmExtractValue(emitter, toOstyValue(base), field.typ, field.index)
	g.takeOstyEmitter(emitter)
	loaded := fromOstyValue(out)
	loaded.listElemTyp = field.listElemTyp
	loaded.listElemString = field.listElemString
	loaded.mapKeyTyp = field.mapKeyTyp
	loaded.mapValueTyp = field.mapValueTyp
	loaded.mapKeyString = field.mapKeyString
	loaded.setElemTyp = field.setElemTyp
	loaded.setElemString = field.setElemString
	loaded.sourceType = field.sourceType
	loaded.gcManaged = valueNeedsManagedRoot(loaded)
	loaded.rootPaths = g.rootPathsForType(field.typ)
	return loaded, nil
}

func (g *generator) emitOptionalFieldExpr(expr *ast.FieldExpr) (value, error) {
	if expr == nil {
		return value{}, unsupported("expression", "nil optional field access")
	}
	baseSource, ok := g.staticExprSourceType(expr.X)
	if !ok {
		return value{}, unsupported("type-system", "optional field receiver type is unknown")
	}
	innerSource, ok := unwrapOptionalSourceType(baseSource)
	if !ok {
		return value{}, unsupported("type-system", "optional field receiver must have type T?")
	}
	innerTyp, err := llvmType(innerSource, g.typeEnv())
	if err != nil {
		return value{}, err
	}
	info := g.structsByType[innerTyp]
	if info == nil {
		return value{}, unsupportedf("type-system", "optional field access on %s", innerTyp)
	}
	field, ok := info.byName[expr.Name]
	if !ok {
		return value{}, unsupportedf("expression", "struct %q has no field %q", info.name, expr.Name)
	}
	if field.typ != "ptr" {
		return value{}, unsupportedf("type-system", "optional field %q.%s currently requires a ptr-backed field", info.name, field.name)
	}
	base, err := g.emitExpr(expr.X)
	if err != nil {
		return value{}, err
	}
	out, err := g.emitOptionalPtrExpr(base, func() (value, error) {
		loadedBase, err := g.loadTypedPointerValue(base, innerTyp)
		if err != nil {
			return value{}, err
		}
		emitter := g.toOstyEmitter()
		next := llvmExtractValue(emitter, toOstyValue(loadedBase), field.typ, field.index)
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(next)
		v.listElemTyp = field.listElemTyp
		v.listElemString = field.listElemString
		v.mapKeyTyp = field.mapKeyTyp
		v.mapValueTyp = field.mapValueTyp
		v.mapKeyString = field.mapKeyString
		v.setElemTyp = field.setElemTyp
		v.setElemString = field.setElemString
		v.sourceType = wrapOptionalSourceType(field.sourceType)
		v.gcManaged = valueNeedsManagedRoot(v)
		return v, nil
	})
	if err != nil {
		return value{}, err
	}
	out.listElemTyp = field.listElemTyp
	out.listElemString = field.listElemString
	out.mapKeyTyp = field.mapKeyTyp
	out.mapValueTyp = field.mapValueTyp
	out.mapKeyString = field.mapKeyString
	out.setElemTyp = field.setElemTyp
	out.setElemString = field.setElemString
	out.sourceType = wrapOptionalSourceType(field.sourceType)
	out.gcManaged = valueNeedsManagedRoot(out)
	return out, nil
}

func (g *generator) emitIndexExpr(expr *ast.IndexExpr) (value, error) {
	if expr == nil {
		return value{}, unsupported("expression", "nil index expression")
	}
	base, err := g.emitExpr(expr.X)
	if err != nil {
		return value{}, err
	}
	index, err := g.emitExpr(expr.Index)
	if err != nil {
		return value{}, err
	}
	switch {
	case base.listElemTyp != "":
		if index.typ != "i64" {
			return value{}, unsupportedf("type-system", "list index type %s, want i64", index.typ)
		}
		if listUsesTypedRuntime(base.listElemTyp) {
			symbol := listRuntimeGetSymbol(base.listElemTyp)
			g.declareRuntimeSymbol(symbol, base.listElemTyp, []paramInfo{{typ: "ptr"}, {typ: "i64"}})
			emitter := g.toOstyEmitter()
			out := llvmCall(emitter, base.listElemTyp, symbol, []*LlvmValue{toOstyValue(base), toOstyValue(index)})
			g.takeOstyEmitter(emitter)
			v := fromOstyValue(out)
			v.gcManaged = base.listElemTyp == "ptr"
			v.rootPaths = g.rootPathsForType(base.listElemTyp)
			return v, nil
		}
		traceSymbol := g.traceCallbackSymbol(base.listElemTyp, g.rootPathsForType(base.listElemTyp))
		emitter := g.toOstyEmitter()
		slot := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca %s", slot, base.listElemTyp))
		sizeValue := g.emitTypeSize(emitter, base.listElemTyp)
		g.declareRuntimeSymbol(listRuntimeGetBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  call void @%s(%s)",
			listRuntimeGetBytesSymbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(base), toOstyValue(index), {typ: "ptr", name: slot}, sizeValue, {typ: "ptr", name: llvmPointerOperand(traceSymbol)}}),
		))
		loaded := g.loadValueFromAddress(emitter, base.listElemTyp, slot)
		g.takeOstyEmitter(emitter)
		loaded.rootPaths = g.rootPathsForType(base.listElemTyp)
		return loaded, nil
	case base.mapKeyTyp != "":
		if index.typ != base.mapKeyTyp {
			return value{}, unsupportedf("type-system", "map index type %s, want %s", index.typ, base.mapKeyTyp)
		}
		loadedKey, err := g.loadIfPointer(index)
		if err != nil {
			return value{}, err
		}
		symbol := mapRuntimeGetOrAbortSymbol(base.mapKeyTyp, base.mapKeyString)
		g.declareRuntimeSymbol(symbol, "void", []paramInfo{{typ: "ptr"}, {typ: base.mapKeyTyp}, {typ: "ptr"}})
		emitter := g.toOstyEmitter()
		slot := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca %s", slot, base.mapValueTyp))
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  call void @%s(%s)",
			symbol,
			llvmCallArgs([]*LlvmValue{toOstyValue(base), toOstyValue(loadedKey), {typ: "ptr", name: slot}}),
		))
		out := g.loadValueFromAddress(emitter, base.mapValueTyp, slot)
		g.takeOstyEmitter(emitter)
		out.gcManaged = base.mapValueTyp == "ptr"
		out.rootPaths = g.rootPathsForType(base.mapValueTyp)
		return out, nil
	default:
		return value{}, unsupportedf("expression", "index expression on %s", base.typ)
	}
}

func (g *generator) enumVariantValue(expr *ast.FieldExpr) (value, bool, error) {
	ref, ok := g.enumVariantByField(expr)
	if !ok {
		return value{}, false, nil
	}
	out, err := g.enumVariantConstant(ref.enum, ref.variant)
	return out, true, err
}

func (g *generator) enumVariantIdent(name string) (value, bool, error) {
	found, count := g.findBareEnumVariant(name)
	if count == 0 {
		return value{}, false, nil
	}
	if count > 1 {
		return value{}, true, unsupportedf("name", "ambiguous enum variant %q", name)
	}
	out, err := g.enumVariantConstant(found.enum, found.variant)
	return out, true, err
}

func (g *generator) enumVariantConstant(info *enumInfo, variant variantInfo) (value, error) {
	if info.isBoxed {
		if len(variant.payloads) != 0 {
			return value{}, unsupportedf("expression", "enum variant %q requires a payload", variant.name)
		}
		emitter := g.toOstyEmitter()
		out := llvmEnumBoxedBareVariant(emitter, info.typ, variant.tag)
		g.takeOstyEmitter(emitter)
		enumValue := fromOstyValue(out)
		enumValue.rootPaths = g.rootPathsForType(info.typ)
		return enumValue, nil
	}
	if info.hasPayload {
		if len(variant.payloads) != 0 {
			return value{}, unsupportedf("expression", "enum variant %q requires a payload", variant.name)
		}
		return g.emitEnumPayloadVariant(info, variant, value{typ: info.payloadTyp, ref: llvmZeroLiteral(info.payloadTyp)})
	}
	out := llvmEnumVariant(info.name, variant.tag)
	return fromOstyValue(out), nil
}

func (g *generator) findBareEnumVariant(name string) (enumVariantRef, int) {
	var found enumVariantRef
	count := 0
	for _, info := range g.enums {
		if variant, ok := info.variants[name]; ok {
			found = enumVariantRef{enum: info, variant: variant}
			count++
		}
	}
	return found, count
}

func (g *generator) enumVariantByField(expr *ast.FieldExpr) (enumVariantRef, bool) {
	base, ok := expr.X.(*ast.Ident)
	if !ok {
		return enumVariantRef{}, false
	}
	info := g.enumInfoByName(base.Name)
	if info == nil {
		return enumVariantRef{}, false
	}
	variant, ok := info.variants[expr.Name]
	if !ok {
		return enumVariantRef{}, false
	}
	return enumVariantRef{enum: info, variant: variant}, true
}

func (g *generator) emitEnumPayloadVariant(info *enumInfo, variant variantInfo, payload value) (value, error) {
	if !info.hasPayload {
		return value{}, unsupportedf("expression", "enum %q has no payload layout", info.name)
	}
	if info.isBoxed {
		emitter := g.toOstyEmitter()
		site := "enum." + info.name + "." + variant.name
		out := llvmEnumBoxedPayloadVariant(emitter, info.typ, variant.tag, toOstyValue(payload), site)
		g.takeOstyEmitter(emitter)
		g.needsGCRuntime = true
		enumValue := fromOstyValue(out)
		enumValue.rootPaths = g.rootPathsForType(info.typ)
		return enumValue, nil
	}
	if payload.typ != info.payloadTyp {
		return value{}, unsupportedf("type-system", "enum %q variant %q payload type %s, want %s", info.name, variant.name, payload.typ, info.payloadTyp)
	}
	emitter := g.toOstyEmitter()
	out := llvmEnumPayloadVariant(emitter, info.typ, variant.tag, toOstyValue(payload))
	g.takeOstyEmitter(emitter)
	enumValue := fromOstyValue(out)
	enumValue.rootPaths = g.rootPathsForType(info.typ)
	return enumValue, nil
}

func (g *generator) emitUnary(e *ast.UnaryExpr) (value, error) {
	v, err := g.emitExpr(e.X)
	if err != nil {
		return value{}, err
	}
	switch e.Op {
	case token.PLUS:
		if v.typ != "i64" && v.typ != "double" {
			return value{}, unsupportedf("type-system", "unary plus on %s", v.typ)
		}
		return v, nil
	case token.MINUS:
		emitter := g.toOstyEmitter()
		var out *LlvmValue
		switch v.typ {
		case "i64":
			out = llvmBinaryI64(emitter, "sub", llvmIntLiteral(0), toOstyValue(v))
		case "double":
			out = llvmBinaryF64(emitter, "fsub", llvmFloatLiteral("0.0"), toOstyValue(v))
		default:
			g.takeOstyEmitter(emitter)
			return value{}, unsupportedf("type-system", "unary minus on %s", v.typ)
		}
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), nil
	case token.NOT:
		if v.typ != "i1" {
			return value{}, unsupportedf("type-system", "logical not on %s", v.typ)
		}
		emitter := g.toOstyEmitter()
		out := llvmNotI1(emitter, toOstyValue(v))
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), nil
	case token.BITNOT:
		if v.typ != "i64" {
			return value{}, unsupportedf("type-system", "bitwise not on %s", v.typ)
		}
		emitter := g.toOstyEmitter()
		out := llvmBinaryI64(emitter, "xor", toOstyValue(v), llvmIntLiteral(-1))
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), nil
	default:
		return value{}, unsupportedf("expression", "unary operator %q", e.Op)
	}
}

func (g *generator) emitBinary(e *ast.BinaryExpr) (value, error) {
	left, err := g.emitExpr(e.Left)
	if err != nil {
		return value{}, err
	}
	right, err := g.emitExpr(e.Right)
	if err != nil {
		return value{}, err
	}
	if llvmIsCompareOp(e.Op.String()) {
		isString := g.staticExprIsString(e.Left) || g.staticExprIsString(e.Right)
		return g.emitCompare(e.Op, left, right, isString)
	}
	if e.Op == token.AND || e.Op == token.OR {
		return g.emitLogical(e.Op, left, right)
	}
	if left.typ == "double" && right.typ == "double" {
		op := llvmFloatBinaryInstruction(e.Op.String())
		if op == "" {
			return value{}, unsupportedf("expression", "binary operator %q", e.Op)
		}
		emitter := g.toOstyEmitter()
		out := llvmBinaryF64(emitter, op, toOstyValue(left), toOstyValue(right))
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), nil
	}
	if left.typ == "ptr" && right.typ == "ptr" && e.Op == token.PLUS {
		return g.emitRuntimeStringConcat(left, right)
	}
	if left.typ != "i64" || right.typ != "i64" {
		return value{}, unsupportedf("type-system", "binary operator %q on %s/%s", e.Op, left.typ, right.typ)
	}
	op := llvmIntBinaryInstruction(e.Op.String())
	if op == "" {
		return value{}, unsupportedf("expression", "binary operator %q", e.Op)
	}
	emitter := g.toOstyEmitter()
	out := llvmBinaryI64(emitter, op, toOstyValue(left), toOstyValue(right))
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitLogical(op token.Kind, left, right value) (value, error) {
	if left.typ != "i1" || right.typ != "i1" {
		return value{}, unsupportedf("type-system", "logical operator %q on %s/%s", op, left.typ, right.typ)
	}
	inst := llvmLogicalInstruction(op.String())
	if inst == "" {
		return value{}, unsupportedf("expression", "logical operator %q", op)
	}
	emitter := g.toOstyEmitter()
	out := llvmLogicalI1(emitter, inst, toOstyValue(left), toOstyValue(right))
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitCompare(op token.Kind, left, right value, isString bool) (value, error) {
	if left.typ != right.typ {
		return value{}, unsupportedf("type-system", "compare type mismatch %s/%s", left.typ, right.typ)
	}
	emitter := g.toOstyEmitter()
	var out *LlvmValue
	switch left.typ {
	case "i64", "i1":
		pred := llvmIntComparePredicate(op.String())
		if pred == "" {
			g.takeOstyEmitter(emitter)
			return value{}, unsupportedf("expression", "comparison operator %q", op)
		}
		out = llvmCompare(emitter, pred, toOstyValue(left), toOstyValue(right))
	case "double":
		pred := llvmFloatComparePredicate(op.String())
		if pred == "" {
			g.takeOstyEmitter(emitter)
			return value{}, unsupportedf("expression", "comparison operator %q", op)
		}
		out = llvmCompareF64(emitter, pred, toOstyValue(left), toOstyValue(right))
	case "ptr":
		g.takeOstyEmitter(emitter)
		if !isString {
			return value{}, unsupportedf("type-system", "comparison operator %q on non-String ptr values is not yet lowered", op)
		}
		return g.emitRuntimeStringCompare(op, left, right)
	default:
		g.takeOstyEmitter(emitter)
		return value{}, unsupportedf("type-system", "compare type %s", left.typ)
	}
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitQuestionExpr(expr *ast.QuestionExpr) (value, error) {
	if expr == nil || expr.X == nil {
		return value{}, unsupported("expression", "nil optional propagation")
	}
	sourceType, ok := g.staticExprSourceType(expr.X)
	if !ok {
		return value{}, unsupported("type-system", "optional propagation source type is unknown")
	}
	if info, ok := builtinResultTypeFromAST(sourceType, g.typeEnv()); ok {
		return g.emitQuestionExprResult(expr, info, sourceType)
	}
	innerSource, ok := unwrapOptionalSourceType(sourceType)
	if !ok {
		return value{}, unsupported("type-system", "optional propagation requires a T? or Result<T, E> value")
	}
	innerTyp, err := llvmType(innerSource, g.typeEnv())
	if err != nil {
		return value{}, err
	}
	if g.returnType != "ptr" {
		return value{}, unsupportedf("control-flow", "optional propagation currently requires a ptr-backed return type, got %s", g.returnType)
	}
	base, err := g.emitExpr(expr.X)
	if err != nil {
		return value{}, err
	}
	if base.typ != "ptr" {
		return value{}, unsupportedf("type-system", "optional propagation source type %s, want ptr", base.typ)
	}
	emitter := g.toOstyEmitter()
	isNil := llvmCompare(emitter, "eq", toOstyValue(base), toOstyValue(value{typ: "ptr", ref: "null"}))
	nilLabel := llvmNextLabel(emitter, "optional.return")
	contLabel := llvmNextLabel(emitter, "optional.cont")
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", isNil.name, nilLabel, contLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", nilLabel))
	g.releaseGCRoots(emitter)
	emitter.body = append(emitter.body, "  ret ptr null")
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", contLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(contLabel)
	if innerTyp == "ptr" {
		base.sourceType = innerSource
		base.gcManaged = valueNeedsManagedRoot(base)
		return base, nil
	}
	out, err := g.loadTypedPointerValue(base, innerTyp)
	if err != nil {
		return value{}, err
	}
	out.sourceType = innerSource
	return out, nil
}

// emitQuestionExprResult lowers `expr?` where `expr` evaluates to a
// `Result<T, E>` struct `{i64 tag, T ok, E err}` and the enclosing
// function returns `Result<T2, E>` (same error type). Ok: the `ok`
// field continues the enclosing expression. Err: the `err` field is
// re-packaged into the return Result with tag=1 and `ok` zeroed, then
// returned immediately.
func (g *generator) emitQuestionExprResult(expr *ast.QuestionExpr, info builtinResultType, sourceType ast.Type) (value, error) {
	returnInfo, ok := g.resultTypes[g.returnType]
	if !ok {
		return value{}, unsupported("control-flow", "? on Result<T, E> requires the enclosing function to return Result<_, E>")
	}
	if returnInfo.errTyp != info.errTyp {
		return value{}, unsupportedf("type-system", "? propagates err %s, function returns err %s", info.errTyp, returnInfo.errTyp)
	}
	base, err := g.emitExpr(expr.X)
	if err != nil {
		return value{}, err
	}
	if base.typ != info.typ {
		return value{}, unsupportedf("type-system", "? on Result type %s, want %s", base.typ, info.typ)
	}
	emitter := g.toOstyEmitter()
	tag := llvmExtractValue(emitter, toOstyValue(base), "i64", 0)
	isErr := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: "1"}))
	errLabel := llvmNextLabel(emitter, "result.err")
	okLabel := llvmNextLabel(emitter, "result.ok")
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", isErr.name, errLabel, okLabel))

	// Err branch: repackage into the enclosing function's Result<T2, E>
	// struct and return immediately. GC roots are released just before
	// `ret` to mirror the bare-return path.
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", errLabel))
	errSlot := llvmExtractValue(emitter, toOstyValue(base), info.errTyp, 2)
	retFields := []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(returnInfo.okTyp)),
		errSlot,
	}
	retStruct := llvmStructLiteral(emitter, returnInfo.typ, retFields)
	g.releaseGCRoots(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  ret %s %s", returnInfo.typ, retStruct.name))

	// Ok branch: extract the ok field and let the caller continue with it.
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", okLabel))
	okSlot := llvmExtractValue(emitter, toOstyValue(base), info.okTyp, 1)
	g.takeOstyEmitter(emitter)
	g.enterBlock(okLabel)

	out := fromOstyValue(okSlot)
	out.sourceType = builtinResultPayloadSourceType(sourceType, "Ok")
	out.rootPaths = g.rootPathsForType(info.okTyp)
	if info.okTyp == "ptr" {
		out.gcManaged = true
	}
	return out, nil
}

func (g *generator) emitRuntimeStringConcat(left, right value) (value, error) {
	g.declareRuntimeSymbol("osty_rt_strings_Concat", "ptr", []paramInfo{
		{typ: "ptr"},
		{typ: "ptr"},
	})
	emitter := g.toOstyEmitter()
	out := llvmStringConcat(emitter, toOstyValue(left), toOstyValue(right))
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitRuntimeStringCompare(op token.Kind, left, right value) (value, error) {
	if op != token.EQ && op != token.NEQ {
		return value{}, unsupportedf("type-system", "compare type %s", left.typ)
	}
	g.declareRuntimeSymbol("osty_rt_strings_Equal", "i1", []paramInfo{
		{typ: "ptr"},
		{typ: "ptr"},
	})
	emitter := g.toOstyEmitter()
	out := llvmStringCompare(emitter, op.String(), toOstyValue(left), toOstyValue(right))
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitIfExprValue(expr *ast.IfExpr) (value, error) {
	if expr.IsIfLet {
		return g.emitIfLetExprValue(expr)
	}
	if expr.Then == nil {
		return value{}, unsupported("control-flow", "if expression has no then block")
	}
	if expr.Else == nil {
		return value{}, unsupported("control-flow", "if expression has no else branch")
	}
	cond, err := g.emitExpr(expr.Cond)
	if err != nil {
		return value{}, err
	}
	if cond.typ != "i1" {
		return value{}, unsupportedf("type-system", "if condition type %s, want i1", cond.typ)
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfExprStart(emitter, toOstyValue(cond))
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	g.pushScope()
	thenValue, err := g.emitBlockValue(expr.Then)
	g.popScope()
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	elseValue, err := g.emitElseValue(expr.Else)
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "if expression branch types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitIfLetExprValue(expr *ast.IfExpr) (value, error) {
	if expr.Then == nil {
		return value{}, unsupported("control-flow", "if expression has no then block")
	}
	if expr.Else == nil {
		return value{}, unsupported("control-flow", "if expression has no else branch")
	}
	scrutinee, err := g.emitExpr(expr.Cond)
	if err != nil {
		return value{}, err
	}
	cond, bind, err := g.ifLetCondition(expr.Pattern, scrutinee)
	if err != nil {
		return value{}, err
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfExprStart(emitter, cond)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	g.pushScope()
	if bind != nil {
		if err := bind(); err != nil {
			g.popScope()
			return value{}, err
		}
	}
	thenValue, err := g.emitBlockValue(expr.Then)
	g.popScope()
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	elseValue, err := g.emitElseValue(expr.Else)
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "if expression branch types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitIfExprPhi(labels *LlvmIfLabels, thenPred, elsePred string, thenValue, elseValue value) (value, error) {
	if labels == nil {
		return value{}, unsupported("control-flow", "missing if-expression labels")
	}
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "if expression branch types %s/%s", thenValue.typ, elseValue.typ)
	}
	emitter := g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", labels.endLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", labels.endLabel))
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  %s = phi %s [ %s, %%%s ], [ %s, %%%s ]",
		tmp,
		thenValue.typ,
		thenValue.ref,
		thenPred,
		elseValue.ref,
		elsePred,
	))
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.endLabel
	out := value{typ: thenValue.typ, ref: tmp}
	mergeContainerMetadata(&out, thenValue, elseValue)
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, nil
}

func (g *generator) emitBlockValue(block *ast.Block) (value, error) {
	if block == nil || len(block.Stmts) == 0 {
		return value{}, unsupported("expression", "block has no value")
	}
	for i, stmt := range block.Stmts {
		if i != len(block.Stmts)-1 {
			if err := g.emitStmt(stmt); err != nil {
				return value{}, err
			}
			continue
		}
		exprStmt, ok := stmt.(*ast.ExprStmt)
		if !ok {
			return value{}, unsupportedf("statement", "final block statement %T", stmt)
		}
		return g.emitExpr(exprStmt.X)
	}
	return value{}, unsupported("expression", "block has no value")
}

func (g *generator) emitElseValue(expr ast.Expr) (value, error) {
	switch e := expr.(type) {
	case *ast.Block:
		g.pushScope()
		defer g.popScope()
		return g.emitBlockValue(e)
	case *ast.IfExpr:
		return g.emitIfExprValue(e)
	default:
		return value{}, unsupportedf("control-flow", "else expression %T", expr)
	}
}

func (g *generator) emitMatchExprValue(expr *ast.MatchExpr) (value, error) {
	if expr == nil {
		return value{}, unsupported("expression", "nil match expression")
	}
	if len(expr.Arms) == 0 {
		return value{}, unsupported("expression", "match with no arms")
	}
	scrutinee, err := g.emitExpr(expr.Scrutinee)
	if err != nil {
		return value{}, err
	}
	hasGuard := false
	for _, arm := range expr.Arms {
		if arm == nil {
			return value{}, unsupported("expression", "nil match arm")
		}
		if arm.Guard != nil {
			hasGuard = true
		}
	}
	if hasGuard {
		return g.emitGuardedMatchExprValue(scrutinee, expr.Arms)
	}
	if scrutinee.typ == "i64" {
		return g.emitTagEnumMatchExprValue(scrutinee, expr.Arms)
	}
	if info := g.enumsByType[scrutinee.typ]; info != nil && info.hasPayload {
		return g.emitPayloadEnumMatchExprValue(scrutinee, info, expr.Arms)
	}
	if info, ok := g.resultTypes[scrutinee.typ]; ok {
		return g.emitResultMatchExprValue(scrutinee, info, expr.Arms)
	}
	return value{}, unsupportedf("type-system", "match scrutinee type %s, want enum tag", scrutinee.typ)
}

// resultPatternInfo describes a single arm of a Result match: which
// variant it targets, which struct field carries the payload, and
// whether the arm binds the payload to a local name.
type resultPatternInfo struct {
	tag         int    // 0 = Ok, 1 = Err
	fieldIndex  int    // 1 for Ok, 2 for Err
	payloadType string // info.okTyp or info.errTyp
	payloadName string
	hasBinding  bool
	isWildcard  bool // true if pattern is bare `_`
}

func (g *generator) matchResultPattern(info builtinResultType, pattern ast.Pattern) (resultPatternInfo, bool, error) {
	if _, ok := pattern.(*ast.WildcardPat); ok {
		return resultPatternInfo{isWildcard: true}, true, nil
	}
	vp, ok := pattern.(*ast.VariantPat)
	if !ok || len(vp.Path) != 1 {
		return resultPatternInfo{}, false, nil
	}
	out := resultPatternInfo{}
	switch vp.Path[0] {
	case "Ok":
		out.tag = 0
		out.fieldIndex = 1
		out.payloadType = info.okTyp
	case "Err":
		out.tag = 1
		out.fieldIndex = 2
		out.payloadType = info.errTyp
	default:
		return resultPatternInfo{}, false, nil
	}
	if len(vp.Args) != 1 {
		return resultPatternInfo{}, true, unsupportedf("expression", "Result variant pattern %q requires one argument", vp.Path[0])
	}
	switch arg := vp.Args[0].(type) {
	case *ast.IdentPat:
		if !llvmIsIdent(arg.Name) {
			return resultPatternInfo{}, true, unsupportedf("name", "Result payload binding name %q", arg.Name)
		}
		out.payloadName = arg.Name
		out.hasBinding = true
	case *ast.WildcardPat:
		// no binding
	default:
		return resultPatternInfo{}, true, unsupportedf("expression", "Result payload pattern %T", arg)
	}
	return out, true, nil
}

// emitResultMatchExprValue lowers a two-arm match on a Result<T, E>
// scrutinee (the `{i64 tag, T ok, E err}` builtin struct). The arms
// must cover Ok and Err in either order, optionally with a trailing
// wildcard. Payload bindings are extracted from field 1 (Ok) or
// field 2 (Err) per the existing Result ABI.
func (g *generator) emitResultMatchExprValue(scrutinee value, info builtinResultType, arms []*ast.MatchArm) (value, error) {
	if len(arms) != 2 {
		return value{}, unsupportedf("expression", "Result match requires exactly 2 arms, got %d", len(arms))
	}
	firstInfo, firstOk, err := g.matchResultPattern(info, arms[0].Pattern)
	if err != nil {
		return value{}, err
	}
	if !firstOk {
		return value{}, unsupported("expression", "Result match first arm must be Ok(...), Err(...), or _")
	}
	if firstInfo.isWildcard {
		// Wildcard first arm short-circuits — never consult the tag.
		return g.emitResultMatchArm(scrutinee, firstInfo, arms[0].Body)
	}
	secondInfo, secondOk, err := g.matchResultPattern(info, arms[1].Pattern)
	if err != nil {
		return value{}, err
	}
	if !secondOk {
		return value{}, unsupported("expression", "Result match second arm must be Ok(...), Err(...), or _")
	}
	emitter := g.toOstyEmitter()
	tag := llvmExtractValue(emitter, toOstyValue(scrutinee), "i64", 0)
	cond := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: strconv.Itoa(firstInfo.tag)}))
	labels := llvmIfExprStart(emitter, cond)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	thenValue, err := g.emitResultMatchArm(scrutinee, firstInfo, arms[0].Body)
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	elseValue, err := g.emitResultMatchArm(scrutinee, secondInfo, arms[1].Body)
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock

	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitResultMatchArm(scrutinee value, info resultPatternInfo, body ast.Expr) (value, error) {
	g.pushScope()
	defer g.popScope()
	if info.hasBinding && !info.isWildcard {
		emitter := g.toOstyEmitter()
		payload := llvmExtractValue(emitter, toOstyValue(scrutinee), info.payloadType, info.fieldIndex)
		g.takeOstyEmitter(emitter)
		payloadValue := fromOstyValue(payload)
		payloadValue.gcManaged = info.payloadType == "ptr"
		payloadValue.rootPaths = g.rootPathsForType(info.payloadType)
		g.bindNamedLocal(info.payloadName, payloadValue, false)
	}
	return g.emitMatchArmBodyValue(body)
}

func (g *generator) emitTagEnumMatchExprValue(scrutinee value, arms []*ast.MatchArm) (value, error) {
	if len(arms) == 2 {
		return g.emitTagEnumMatchIfExprValue(scrutinee, arms[0], arms[1])
	}
	selectSafe := true
	for _, arm := range arms {
		if !matchArmBodyIsSelectSafe(arm.Body) {
			selectSafe = false
			break
		}
	}
	if selectSafe {
		return g.emitTagEnumMatchSelectValue(scrutinee, arms)
	}
	return g.emitTagEnumMatchChainValue(scrutinee, arms)
}

func (g *generator) emitTagEnumMatchSelectValue(scrutinee value, arms []*ast.MatchArm) (value, error) {
	var current value
	haveCurrent := false
	for i := len(arms) - 1; i >= 0; i-- {
		arm := arms[i]
		if _, catchAll := arm.Pattern.(*ast.WildcardPat); catchAll {
			if i != len(arms)-1 {
				return value{}, unsupported("expression", "wildcard match arm must be last")
			}
			v, err := g.emitMatchArmBodyValue(arm.Body)
			if err != nil {
				return value{}, err
			}
			current = v
			haveCurrent = true
			continue
		}
		tag, ok, err := g.matchEnumTag(arm.Pattern)
		if err != nil {
			return value{}, err
		}
		if !ok {
			return value{}, unsupported("expression", "match arm must be a payload-free enum variant")
		}
		armValue, err := g.emitMatchArmBodyValue(arm.Body)
		if err != nil {
			return value{}, err
		}
		if !haveCurrent {
			current = armValue
			haveCurrent = true
			continue
		}
		if armValue.typ != current.typ {
			return value{}, unsupportedf("type-system", "match arm types %s/%s", armValue.typ, current.typ)
		}
		emitter := g.toOstyEmitter()
		cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(value{typ: "i64", ref: strconv.Itoa(tag)}))
		g.takeOstyEmitter(emitter)
		current, err = g.emitSelectValue(cond, armValue, current)
		if err != nil {
			return value{}, err
		}
	}
	if !haveCurrent {
		return value{}, unsupported("expression", "match with no arms")
	}
	return current, nil
}

func (g *generator) emitTagEnumMatchChainValue(scrutinee value, arms []*ast.MatchArm) (value, error) {
	if len(arms) == 0 {
		return value{}, unsupported("expression", "match with no arms")
	}
	arm := arms[0]
	if arm == nil {
		return value{}, unsupported("expression", "nil match arm")
	}
	if len(arms) == 1 {
		if _, catchAll := arm.Pattern.(*ast.WildcardPat); !catchAll {
			if _, ok, err := g.matchEnumTag(arm.Pattern); err != nil {
				return value{}, err
			} else if !ok {
				return value{}, unsupported("expression", "match arm must be a payload-free enum variant")
			}
		}
		return g.emitMatchArmBodyValue(arm.Body)
	}
	if _, catchAll := arm.Pattern.(*ast.WildcardPat); catchAll {
		return value{}, unsupported("expression", "wildcard match arm must be last")
	}
	tag, ok, err := g.matchEnumTag(arm.Pattern)
	if err != nil {
		return value{}, err
	}
	if !ok {
		return value{}, unsupported("expression", "match arm must be a payload-free enum variant")
	}
	emitter := g.toOstyEmitter()
	cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(value{typ: "i64", ref: strconv.Itoa(tag)}))
	labels := llvmIfExprStart(emitter, cond)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	thenValue, err := g.emitMatchArmBodyValue(arm.Body)
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	elseValue, err := g.emitTagEnumMatchChainValue(scrutinee, arms[1:])
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitGuardedMatchExprValue(scrutinee value, arms []*ast.MatchArm) (value, error) {
	if len(arms) == 0 {
		return value{}, unsupported("expression", "match with no arms")
	}
	arm := arms[0]
	if arm == nil {
		return value{}, unsupported("expression", "nil match arm")
	}
	if len(arms) == 1 {
		return g.emitFinalMatchArmValue(scrutinee, arm)
	}
	if _, catchAll := arm.Pattern.(*ast.WildcardPat); catchAll && arm.Guard == nil {
		return value{}, unsupported("expression", "wildcard match arm must be last")
	}
	cond, bind, err := g.ifLetCondition(arm.Pattern, scrutinee)
	if err != nil {
		return value{}, err
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfExprStart(emitter, cond)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	thenValue, err := g.emitGuardedMatchArmThenValue(scrutinee, arm, arms[1:], bind)
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	elseValue, err := g.emitGuardedMatchExprValue(scrutinee, arms[1:])
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitGuardedMatchArmThenValue(scrutinee value, arm *ast.MatchArm, rest []*ast.MatchArm, bind func() error) (value, error) {
	g.pushScope()
	defer g.popScope()
	if bind != nil {
		if err := bind(); err != nil {
			return value{}, err
		}
	}
	if arm.Guard == nil {
		return g.emitMatchArmBodyValue(arm.Body)
	}
	guard, err := g.emitExpr(arm.Guard)
	if err != nil {
		return value{}, err
	}
	if guard.typ != "i1" {
		return value{}, unsupportedf("type-system", "match guard type %s, want i1", guard.typ)
	}
	if len(rest) == 0 {
		return value{}, unsupported("control-flow", "final guarded match arm requires an unguarded fallback arm")
	}
	emitter := g.toOstyEmitter()
	labels := llvmIfExprStart(emitter, toOstyValue(guard))
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	thenValue, err := g.emitMatchArmBodyValue(arm.Body)
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	elseValue, err := g.emitGuardedMatchExprValue(scrutinee, rest)
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitFinalMatchArmValue(scrutinee value, arm *ast.MatchArm) (value, error) {
	if arm == nil {
		return value{}, unsupported("expression", "nil match arm")
	}
	if arm.Guard != nil {
		return value{}, unsupported("control-flow", "final guarded match arm requires an unguarded fallback arm")
	}
	_, bind, err := g.ifLetCondition(arm.Pattern, scrutinee)
	if err != nil {
		return value{}, err
	}
	g.pushScope()
	defer g.popScope()
	if bind != nil {
		if err := bind(); err != nil {
			return value{}, err
		}
	}
	return g.emitMatchArmBodyValue(arm.Body)
}

func (g *generator) emitTagEnumMatchIfExprValue(scrutinee value, first, second *ast.MatchArm) (value, error) {
	tag, ok, err := g.matchEnumTag(first.Pattern)
	if err != nil {
		return value{}, err
	}
	if !ok {
		return value{}, unsupported("expression", "first match arm must be a payload-free enum variant")
	}
	if _, catchAll := second.Pattern.(*ast.WildcardPat); !catchAll {
		if _, _, err := g.matchEnumTag(second.Pattern); err != nil {
			return value{}, err
		}
	}
	emitter := g.toOstyEmitter()
	cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(value{typ: "i64", ref: strconv.Itoa(tag)}))
	labels := llvmIfExprStart(emitter, cond)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	thenValue, err := g.emitMatchArmBodyValue(first.Body)
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	elseValue, err := g.emitMatchArmBodyValue(second.Body)
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitSelectValue(cond *LlvmValue, thenValue, elseValue value) (value, error) {
	if cond == nil || cond.typ != "i1" {
		return value{}, unsupported("type-system", "select condition must be Bool")
	}
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "select branch types %s/%s", thenValue.typ, elseValue.typ)
	}
	emitter := g.toOstyEmitter()
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = select i1 %s, %s %s, %s %s", tmp, cond.name, thenValue.typ, thenValue.ref, elseValue.typ, elseValue.ref))
	g.takeOstyEmitter(emitter)
	out := value{typ: thenValue.typ, ref: tmp}
	mergeContainerMetadata(&out, thenValue, elseValue)
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, nil
}

func (g *generator) emitExprWithHint(expr ast.Expr, listElemTyp string, listElemString bool, mapKeyTyp string, mapValueTyp string, mapKeyString bool, setElemTyp string, setElemString bool) (value, error) {
	if list, ok := expr.(*ast.ListExpr); ok {
		return g.emitListExprWithHint(list, listElemTyp, listElemString)
	}
	if m, ok := expr.(*ast.MapExpr); ok {
		return g.emitMapExprWithHint(m, mapKeyTyp, mapValueTyp, mapKeyString)
	}
	return g.emitExpr(expr)
}

func builtinResultPayloadSourceType(sourceType ast.Type, constructor string) ast.Type {
	named, ok := sourceType.(*ast.NamedType)
	if !ok || len(named.Path) != 1 || named.Path[0] != "Result" || len(named.Args) != 2 {
		return nil
	}
	if constructor == "Err" {
		return named.Args[1]
	}
	return named.Args[0]
}

func (g *generator) emitExprWithHintAndSourceType(expr ast.Expr, sourceType ast.Type, listElemTyp string, listElemString bool, mapKeyTyp string, mapValueTyp string, mapKeyString bool, setElemTyp string, setElemString bool) (value, error) {
	if sourceType != nil {
		if listElemTyp == "" {
			if elemTyp, elemString, ok, err := llvmListElementInfo(sourceType, g.typeEnv()); err != nil {
				return value{}, err
			} else if ok {
				listElemTyp = elemTyp
				listElemString = elemString
			}
		}
		if mapKeyTyp == "" && mapValueTyp == "" {
			if keyTyp, valueTyp, keyString, ok, err := llvmMapTypes(sourceType, g.typeEnv()); err != nil {
				return value{}, err
			} else if ok {
				mapKeyTyp = keyTyp
				mapValueTyp = valueTyp
				mapKeyString = keyString
			}
		}
		if setElemTyp == "" {
			if elemTyp, elemString, ok, err := llvmSetElementType(sourceType, g.typeEnv()); err != nil {
				return value{}, err
			} else if ok {
				setElemTyp = elemTyp
				setElemString = elemString
			}
		}
	}
	if info, ok := builtinResultTypeFromAST(sourceType, g.typeEnv()); ok {
		g.resultContexts = append(g.resultContexts, builtinResultContext{
			info:       info,
			sourceType: sourceType,
		})
		defer func() {
			g.resultContexts = g.resultContexts[:len(g.resultContexts)-1]
		}()
	}
	v, err := g.emitExprWithHint(expr, listElemTyp, listElemString, mapKeyTyp, mapValueTyp, mapKeyString, setElemTyp, setElemString)
	if err != nil {
		return value{}, err
	}
	if v.sourceType == nil && sourceType != nil {
		if typ, err := llvmType(sourceType, g.typeEnv()); err == nil && typ == v.typ {
			v.sourceType = sourceType
		}
	}
	return v, nil
}

func (g *generator) usesAggregateListABI(elemTyp string) bool {
	switch elemTyp {
	case "", "i64", "i1", "double", "ptr":
		return false
	}
	return true
}

func (g *generator) emitAggregateByteSize(emitter *LlvmEmitter, typ string) value {
	sizePtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr %s, ptr null, i32 1", sizePtr, typ))
	size := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = ptrtoint ptr %s to i64", size, sizePtr))
	return value{typ: "i64", ref: size}
}

func (g *generator) emitAggregateScratchSlot(emitter *LlvmEmitter, typ, initial string) value {
	slot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca %s", slot, typ))
	emitter.body = append(emitter.body, fmt.Sprintf("  store %s %s, ptr %s", typ, initial, slot))
	return value{typ: typ, ref: slot, ptr: true}
}

func (g *generator) emitAggregateRootOffsets(emitter *LlvmEmitter, typ string) (value, int, error) {
	paths := g.rootPathsForType(typ)
	if len(paths) == 0 {
		return value{typ: "ptr", ref: "null"}, 0, nil
	}
	arrayTyp := fmt.Sprintf("[%d x i64]", len(paths))
	arrayPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = alloca %s", arrayPtr, arrayTyp))
	for i, path := range paths {
		offsetPtr := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  %s = getelementptr inbounds %s, ptr null, %s",
			offsetPtr,
			typ,
			llvmAggregatePathIndices(path),
		))
		offsetValue := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = ptrtoint ptr %s to i64", offsetValue, offsetPtr))
		slotPtr := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 %d", slotPtr, arrayTyp, arrayPtr, i))
		emitter.body = append(emitter.body, fmt.Sprintf("  store i64 %s, ptr %s", offsetValue, slotPtr))
	}
	firstPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr inbounds %s, ptr %s, i32 0, i32 0", firstPtr, arrayTyp, arrayPtr))
	return value{typ: "ptr", ref: firstPtr}, len(paths), nil
}

func (g *generator) emitListAggregatePush(listValue, elem value) error {
	emitter := g.toOstyEmitter()
	slot := g.emitAggregateScratchSlot(emitter, elem.typ, elem.ref)
	size := g.emitAggregateByteSize(emitter, elem.typ)
	offsetsPtr, offsetCount, err := g.emitAggregateRootOffsets(emitter, elem.typ)
	if err != nil {
		return err
	}
	if offsetCount == 0 {
		g.declareRuntimeSymbol(listRuntimePushBytesV1Symbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "i64"}})
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  call void @%s(%s)",
			listRuntimePushBytesV1Symbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(listValue), toOstyValue(value{typ: "ptr", ref: slot.ref}), toOstyValue(size)}),
		))
	} else {
		g.declareRuntimeSymbol(listRuntimePushBytesRootsSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}})
		emitter.body = append(emitter.body, fmt.Sprintf(
			"  call void @%s(%s)",
			listRuntimePushBytesRootsSymbol(),
			llvmCallArgs([]*LlvmValue{
				toOstyValue(listValue),
				toOstyValue(value{typ: "ptr", ref: slot.ref}),
				toOstyValue(size),
				toOstyValue(offsetsPtr),
				toOstyValue(value{typ: "i64", ref: strconv.Itoa(offsetCount)}),
			}),
		))
	}
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitListAggregateGet(listValue value, index value, elemTyp string) (value, error) {
	g.declareRuntimeSymbol(listRuntimeGetBytesV1Symbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	slot := g.emitAggregateScratchSlot(emitter, elemTyp, "zeroinitializer")
	size := g.emitAggregateByteSize(emitter, elemTyp)
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  call void @%s(%s)",
		listRuntimeGetBytesV1Symbol(),
		llvmCallArgs([]*LlvmValue{toOstyValue(listValue), toOstyValue(index), toOstyValue(value{typ: "ptr", ref: slot.ref}), toOstyValue(size)}),
	))
	out := llvmLoad(emitter, toOstyValue(slot))
	g.takeOstyEmitter(emitter)
	loaded := fromOstyValue(out)
	loaded.rootPaths = g.rootPathsForType(elemTyp)
	return loaded, nil
}

func (g *generator) emitListExprWithHint(expr *ast.ListExpr, hintedElemTyp string, hintedElemString bool) (value, error) {
	if expr == nil {
		return value{}, unsupported("expression", "nil list literal")
	}
	g.pushScope()
	defer g.popScope()
	elemTyp := hintedElemTyp
	elemString := hintedElemString
	emittedElems := make([]value, 0, len(expr.Elems))
	for i, elem := range expr.Elems {
		v, err := g.emitExpr(elem)
		if err != nil {
			return value{}, err
		}
		isStringElem := g.staticExprIsString(elem)
		if elemTyp == "" {
			elemTyp = v.typ
			elemString = isStringElem
		}
		if v.typ != elemTyp {
			return value{}, unsupportedf("type-system", "list literal element type %s, want %s", v.typ, elemTyp)
		}
		if i > 0 && isStringElem != elemString {
			return value{}, unsupported("type-system", "list literal mixes String and non-String ptr-backed values")
		}
		emittedElems = append(emittedElems, g.protectManagedTemporary("list.elem", v))
	}
	if elemTyp == "" {
		return value{}, unsupported("expression", "empty list literal requires an explicit List<T> type")
	}
	useAggregateABI := g.usesAggregateListABI(elemTyp)
	g.declareRuntimeSymbol(listRuntimeNewSymbol(), "ptr", nil)
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", listRuntimeNewSymbol(), nil)
	g.takeOstyEmitter(emitter)
	listValue := fromOstyValue(out)
	listValue.gcManaged = true
	listValue.listElemTyp = elemTyp
	listValue.listElemString = elemString
	if len(emittedElems) == 0 {
		return listValue, nil
	}
	pushSymbol := ""
	traceSymbol := ""
	if !useAggregateABI {
		if listUsesTypedRuntime(elemTyp) {
			pushSymbol = listRuntimePushSymbol(elemTyp)
			g.declareRuntimeSymbol(pushSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: elemTyp}})
		} else {
			traceSymbol = g.traceCallbackSymbol(elemTyp, g.rootPathsForType(elemTyp))
		}
	}
	for _, elem := range emittedElems {
		loaded, err := g.loadIfPointer(elem)
		if err != nil {
			return value{}, err
		}
		if useAggregateABI {
			if err := g.emitListAggregatePush(listValue, loaded); err != nil {
				return value{}, err
			}
			continue
		}
		emitter = g.toOstyEmitter()
		if listUsesTypedRuntime(elemTyp) {
			pushSymbol := listRuntimePushSymbol(elemTyp)
			g.declareRuntimeSymbol(pushSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: elemTyp}})
			emitter.body = append(emitter.body, fmt.Sprintf(
				"  call void @%s(%s)",
				pushSymbol,
				llvmCallArgs([]*LlvmValue{toOstyValue(listValue), toOstyValue(loaded)}),
			))
		} else {
			addr := g.spillValueAddress(emitter, "list.elem", loaded)
			sizeValue := g.emitTypeSize(emitter, elemTyp)
			g.declareRuntimeSymbol(listRuntimePushBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
			emitter.body = append(emitter.body, fmt.Sprintf(
				"  call void @%s(%s)",
				listRuntimePushBytesSymbol(),
				llvmCallArgs([]*LlvmValue{
					toOstyValue(listValue),
					{typ: "ptr", name: addr},
					sizeValue,
					{typ: "ptr", name: llvmPointerOperand(traceSymbol)},
				}),
			))
		}
		g.takeOstyEmitter(emitter)
	}
	return listValue, nil
}

func (g *generator) emitMapExprWithHint(expr *ast.MapExpr, hintedKeyTyp, hintedValueTyp string, hintedKeyString bool) (value, error) {
	if expr == nil {
		return value{}, unsupported("expression", "nil map literal")
	}
	keyTyp := hintedKeyTyp
	valueTyp := hintedValueTyp
	keyIsString := hintedKeyString
	g.pushScope()
	defer g.popScope()
	type entryPair struct {
		key   value
		value value
	}
	entries := make([]entryPair, 0, len(expr.Entries))
	for _, entry := range expr.Entries {
		if entry == nil {
			return value{}, unsupported("expression", "nil map entry")
		}
		key, err := g.emitExpr(entry.Key)
		if err != nil {
			return value{}, err
		}
		val, err := g.emitExpr(entry.Value)
		if err != nil {
			return value{}, err
		}
		if keyTyp == "" {
			keyTyp = key.typ
			keyIsString = key.typ == "ptr"
		}
		if valueTyp == "" {
			valueTyp = val.typ
		}
		if key.typ != keyTyp || val.typ != valueTyp {
			return value{}, unsupportedf("type-system", "map literal entry types %s/%s, want %s/%s", key.typ, val.typ, keyTyp, valueTyp)
		}
		entries = append(entries, entryPair{
			key:   g.protectManagedTemporary("map.key", key),
			value: g.protectManagedTemporary("map.value", val),
		})
	}
	if keyTyp == "" || valueTyp == "" {
		return value{}, unsupported("expression", "empty map literal requires an explicit Map<K, V> type")
	}
	traceSymbol := g.traceCallbackSymbol(valueTyp, g.rootPathsForType(valueTyp))
	g.declareRuntimeSymbol(mapRuntimeNewSymbol(), "ptr", []paramInfo{{typ: "i64"}, {typ: "i64"}, {typ: "i64"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	valueSize := g.emitTypeSize(emitter, valueTyp)
	out := llvmCall(emitter, "ptr", mapRuntimeNewSymbol(), []*LlvmValue{
		llvmI64(strconv.Itoa(containerAbiKind(keyTyp, keyIsString))),
		llvmI64(strconv.Itoa(containerAbiKind(valueTyp, false))),
		valueSize,
		{typ: "ptr", name: llvmPointerOperand(traceSymbol)},
	})
	g.takeOstyEmitter(emitter)
	mapValue := fromOstyValue(out)
	mapValue.gcManaged = true
	mapValue.mapKeyTyp = keyTyp
	mapValue.mapValueTyp = valueTyp
	mapValue.mapKeyString = keyIsString
	for _, entry := range entries {
		if err := g.emitMapInsert(mapValue, entry.key, entry.value); err != nil {
			return value{}, err
		}
	}
	return mapValue, nil
}

func (g *generator) emitMapInsert(base, key, val value) error {
	insertSymbol := mapRuntimeInsertSymbol(base.mapKeyTyp, base.mapKeyString)
	keyLoaded, err := g.loadIfPointer(key)
	if err != nil {
		return err
	}
	valLoaded, err := g.loadIfPointer(val)
	if err != nil {
		return err
	}
	emitter := g.toOstyEmitter()
	valAddr := g.spillValueAddress(emitter, "map.insert.value", valLoaded)
	g.declareRuntimeSymbol(insertSymbol, "void", []paramInfo{{typ: "ptr"}, {typ: base.mapKeyTyp}, {typ: "ptr"}})
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  call void @%s(%s)",
		insertSymbol,
		llvmCallArgs([]*LlvmValue{toOstyValue(base), toOstyValue(keyLoaded), {typ: "ptr", name: valAddr}}),
	))
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitListMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, elemTyp, elemString, found := g.listMethodInfo(call)
	if !found {
		return value{}, false, nil
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	if base.typ != "ptr" || elemTyp == "" {
		return value{}, true, unsupportedf("type-system", "list receiver type %s", base.typ)
	}
	switch field.Name {
	case "len":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "list.len requires no arguments")
		}
		g.declareRuntimeSymbol(listRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "i64", listRuntimeLenSymbol(), []*LlvmValue{toOstyValue(base)})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "sorted":
		symbol := listRuntimeSortedSymbol(elemTyp, elemString)
		if len(call.Args) != 0 || symbol == "" {
			return value{}, true, unsupported("call", "list.sorted currently supports List<Int>, List<Bool>, List<Float>, or List<String> with no arguments")
		}
		g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base)})
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.listElemTyp = elemTyp
		v.listElemString = elemString
		return v, true, nil
	case "toSet":
		if elemTyp == "ptr" && !elemString && g.staticExprListElemIsBytes(field.X) {
			return value{}, true, unsupported("call", "list.toSet currently supports List<Int>, List<Bool>, List<Float>, List<String>, or ptr-backed lists except List<Bytes>, with no arguments")
		}
		symbol := listRuntimeToSetSymbol(elemTyp, elemString)
		if len(call.Args) != 0 || symbol == "" {
			return value{}, true, unsupported("call", "list.toSet currently supports List<Int>, List<Bool>, List<Float>, List<String>, or ptr-backed lists except List<Bytes>, with no arguments")
		}
		g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base)})
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.setElemTyp = elemTyp
		v.setElemString = elemString
		return v, true, nil
	default:
		return value{}, false, nil
	}
}

func (g *generator) emitMapMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, keyTyp, _, keyString, found := g.mapMethodInfo(call)
	if !found {
		return value{}, false, nil
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	switch field.Name {
	case "containsKey":
		if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "map.containsKey requires one positional argument")
		}
		key, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		if key.typ != keyTyp {
			return value{}, true, unsupportedf("type-system", "map.containsKey key type %s, want %s", key.typ, keyTyp)
		}
		loaded, err := g.loadIfPointer(key)
		if err != nil {
			return value{}, true, err
		}
		symbol := mapRuntimeContainsSymbol(keyTyp, keyString)
		g.declareRuntimeSymbol(symbol, "i1", []paramInfo{{typ: "ptr"}, {typ: keyTyp}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "i1", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(loaded)})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "keys":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "map.keys requires no arguments")
		}
		g.declareRuntimeSymbol(mapRuntimeKeysSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "ptr", mapRuntimeKeysSymbol(), []*LlvmValue{toOstyValue(base)})
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.listElemTyp = keyTyp
		v.listElemString = keyString
		return v, true, nil
	case "remove":
		if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "map.remove requires one positional argument")
		}
		key, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		if key.typ != keyTyp {
			return value{}, true, unsupportedf("type-system", "map.remove key type %s, want %s", key.typ, keyTyp)
		}
		loaded, err := g.loadIfPointer(key)
		if err != nil {
			return value{}, true, err
		}
		symbol := mapRuntimeRemoveSymbol(keyTyp, keyString)
		g.declareRuntimeSymbol(symbol, "i1", []paramInfo{{typ: "ptr"}, {typ: keyTyp}})
		emitter := g.toOstyEmitter()
		llvmCall(emitter, "i1", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(loaded)})
		g.takeOstyEmitter(emitter)
		return value{typ: "ptr", ref: "null"}, true, nil
	default:
		return value{}, false, nil
	}
}

func (g *generator) emitSetMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, elemTyp, elemString, found := g.setMethodInfo(call)
	if !found {
		return value{}, false, nil
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	switch field.Name {
	case "len":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "set.len requires no arguments")
		}
		g.declareRuntimeSymbol(setRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "i64", setRuntimeLenSymbol(), []*LlvmValue{toOstyValue(base)})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "contains", "remove":
		if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupportedf("call", "set.%s requires one positional argument", field.Name)
		}
		item, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		if item.typ != elemTyp {
			return value{}, true, unsupportedf("type-system", "set.%s item type %s, want %s", field.Name, item.typ, elemTyp)
		}
		loaded, err := g.loadIfPointer(item)
		if err != nil {
			return value{}, true, err
		}
		symbol := setRuntimeContainsSymbol(elemTyp, elemString)
		if field.Name == "remove" {
			symbol = setRuntimeRemoveSymbol(elemTyp, elemString)
		}
		g.declareRuntimeSymbol(symbol, "i1", []paramInfo{{typ: "ptr"}, {typ: elemTyp}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "i1", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(loaded)})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "toList":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "set.toList requires no arguments")
		}
		g.declareRuntimeSymbol(setRuntimeToListSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "ptr", setRuntimeToListSymbol(), []*LlvmValue{toOstyValue(base)})
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.listElemTyp = elemTyp
		v.listElemString = elemString
		return v, true, nil
	default:
		return value{}, false, nil
	}
}

func matchArmBodyIsSelectSafe(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.IntLit, *ast.FloatLit, *ast.BoolLit, *ast.StringLit, *ast.Ident, *ast.FieldExpr:
		return true
	case *ast.Block:
		if e == nil || len(e.Stmts) != 1 {
			return false
		}
		stmt, ok := e.Stmts[0].(*ast.ExprStmt)
		return ok && matchArmBodyIsSelectSafe(stmt.X)
	default:
		return false
	}
}

func (g *generator) emitPayloadEnumMatchExprValue(scrutinee value, info *enumInfo, arms []*ast.MatchArm) (value, error) {
	if len(arms) == 0 {
		return value{}, unsupported("expression", "match with no arms")
	}
	first := arms[0]
	firstPattern, ok, err := g.matchPayloadEnumPattern(info, first.Pattern)
	if err != nil {
		return value{}, err
	}
	if !ok {
		return value{}, unsupportedf("expression", "first match arm must be an enum %q variant", info.name)
	}
	if len(arms) == 1 {
		g.pushScope()
		defer g.popScope()
		if err := g.bindPayloadEnumPattern(scrutinee, firstPattern); err != nil {
			return value{}, err
		}
		return g.emitMatchArmBodyValue(first.Body)
	}
	second := arms[1]
	var elseValue value
	var elsePred string
	var secondPattern enumPatternInfo
	secondHasPattern := false
	if _, catchAll := second.Pattern.(*ast.WildcardPat); !catchAll {
		secondPattern, secondHasPattern, err = g.matchPayloadEnumPattern(info, second.Pattern)
		if err != nil {
			return value{}, err
		}
		if !secondHasPattern {
			return value{}, unsupportedf("expression", "second match arm must be an enum %q variant or wildcard", info.name)
		}
	}

	emitter := g.toOstyEmitter()
	tag := llvmExtractValue(emitter, toOstyValue(scrutinee), "i64", 0)
	cond := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: strconv.Itoa(firstPattern.variant.tag)}))
	labels := llvmIfExprStart(emitter, cond)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	g.pushScope()
	if err := g.bindPayloadEnumPattern(scrutinee, firstPattern); err != nil {
		g.popScope()
		return value{}, err
	}
	thenValue, err := g.emitMatchArmBodyValue(first.Body)
	g.popScope()
	if err != nil {
		return value{}, err
	}
	thenPred := g.currentBlock
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	if len(arms) > 2 {
		elseValue, err = g.emitPayloadEnumMatchExprValue(scrutinee, info, arms[1:])
		if err != nil {
			return value{}, err
		}
		elsePred = g.currentBlock
	} else {
		g.pushScope()
		if secondHasPattern {
			if err := g.bindPayloadEnumPattern(scrutinee, secondPattern); err != nil {
				g.popScope()
				return value{}, err
			}
		}
		elseValue, err = g.emitMatchArmBodyValue(second.Body)
		g.popScope()
		if err != nil {
			return value{}, err
		}
		elsePred = g.currentBlock
	}
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) bindPayloadEnumPattern(scrutinee value, pattern enumPatternInfo) error {
	if !pattern.hasPayloadBinding {
		return nil
	}
	emitter := g.toOstyEmitter()
	var payload *LlvmValue
	if pattern.isBoxed {
		heapPtr := llvmExtractValue(emitter, toOstyValue(scrutinee), "ptr", 1)
		payload = llvmLoadFromSlot(emitter, heapPtr, pattern.payloadType)
	} else {
		payload = llvmExtractValue(emitter, toOstyValue(scrutinee), pattern.payloadType, 1)
	}
	g.takeOstyEmitter(emitter)
	payloadValue := fromOstyValue(payload)
	payloadValue.listElemTyp = pattern.payloadListElemTyp
	payloadValue.gcManaged = pattern.payloadType == "ptr" || pattern.payloadListElemTyp != ""
	payloadValue.rootPaths = g.rootPathsForType(pattern.payloadType)
	g.bindNamedLocal(pattern.payloadName, payloadValue, false)
	return nil
}

func (g *generator) matchPayloadEnumPattern(info *enumInfo, pattern ast.Pattern) (enumPatternInfo, bool, error) {
	switch p := pattern.(type) {
	case *ast.IdentPat:
		variant, ok := info.variants[p.Name]
		if !ok {
			return enumPatternInfo{}, false, nil
		}
		if len(variant.payloads) != 0 {
			return enumPatternInfo{}, true, unsupportedf("expression", "enum variant pattern %q must bind its payload", p.Name)
		}
		return enumPatternInfo{variant: variant, isBoxed: info.isBoxed}, true, nil
	case *ast.VariantPat:
		if len(p.Path) == 0 || len(p.Path) > 2 {
			return enumPatternInfo{}, false, nil
		}
		if len(p.Path) == 2 && p.Path[0] != info.name {
			return enumPatternInfo{}, false, nil
		}
		name := p.Path[len(p.Path)-1]
		variant, ok := info.variants[name]
		if !ok {
			return enumPatternInfo{}, false, nil
		}
		if len(p.Args) != len(variant.payloads) {
			return enumPatternInfo{}, true, unsupportedf("expression", "enum variant pattern %q payload count", name)
		}
		out := enumPatternInfo{variant: variant, isBoxed: info.isBoxed}
		if len(variant.payloads) == 0 {
			return out, true, nil
		}
		out.payloadType = variant.payloads[0]
		out.payloadListElemTyp = variant.payloadListElemTyp
		switch arg := p.Args[0].(type) {
		case *ast.IdentPat:
			if !llvmIsIdent(arg.Name) {
				return enumPatternInfo{}, true, unsupportedf("name", "enum payload binding name %q", arg.Name)
			}
			out.payloadName = arg.Name
			out.hasPayloadBinding = true
		case *ast.WildcardPat:
		default:
			return enumPatternInfo{}, true, unsupportedf("expression", "enum variant payload pattern %T", arg)
		}
		return out, true, nil
	default:
		return enumPatternInfo{}, false, nil
	}
}

func (g *generator) emitMatchArmBodyValue(expr ast.Expr) (value, error) {
	switch e := expr.(type) {
	case *ast.Block:
		g.pushScope()
		defer g.popScope()
		return g.emitBlockValue(e)
	default:
		return g.emitExpr(expr)
	}
}

func (g *generator) matchEnumTag(pattern ast.Pattern) (int, bool, error) {
	switch p := pattern.(type) {
	case *ast.IdentPat:
		var found variantInfo
		count := 0
		for _, info := range g.enums {
			if info.hasPayload {
				continue
			}
			if variant, ok := info.variants[p.Name]; ok {
				found = variant
				count++
			}
		}
		if count == 0 {
			return 0, false, nil
		}
		if count > 1 {
			return 0, true, unsupportedf("name", "ambiguous enum variant pattern %q", p.Name)
		}
		return found.tag, true, nil
	case *ast.VariantPat:
		if len(p.Args) != 0 || len(p.Path) == 0 {
			return 0, false, nil
		}
		name := p.Path[len(p.Path)-1]
		if len(p.Path) == 2 {
			info := g.enumsByName[p.Path[0]]
			if info == nil || info.hasPayload {
				return 0, false, nil
			}
			variant, ok := info.variants[name]
			if !ok {
				return 0, false, nil
			}
			return variant.tag, true, nil
		}
		return g.matchEnumTag(&ast.IdentPat{Name: name})
	default:
		return 0, false, nil
	}
}

func (g *generator) emitCall(call *ast.CallExpr) (value, error) {
	if v, found, err := g.emitTestingValueCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitBuiltinResultConstructor(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitEnumVariantCall(call); found || err != nil {
		return v, err
	}
	// Alias-qualified dispatchers (std.strings / runtime.*) — run before
	// emitInterfaceMethodCall and the *MethodCall family, which eagerly
	// lower the receiver via emitExpr and would otherwise fail with
	// LLVM016 when the receiver is a module alias rather than a binding.
	if v, found, err := g.emitStdStringsCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitRuntimeFFICall(call); found || err != nil {
		return v, err
	}
	// Phase 6b: interface value method dispatch via `%osty.iface` vtable.
	if v, found, err := g.emitInterfaceMethodCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitListMethodCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitMapMethodCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitSetMethodCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitOptionalUserCall(call); found || err != nil {
		return v, err
	}
	sig, receiverExpr, found, err := g.userCallTarget(call)
	if err != nil {
		return value{}, err
	}
	if !found {
		if id, ok := call.Fn.(*ast.Ident); ok && id.Name == "println" {
			return value{}, unsupported("call", "println is only supported as a statement")
		}
		return value{}, unsupportedf("call", "call target %T", call.Fn)
	}
	if sig.ret == "" {
		return value{}, unsupportedf("call", "function %q has no return value", sig.name)
	}
	if sig.ret == "void" {
		return value{}, unsupportedf("call", "function %q has no return value", sig.name)
	}
	emitter := g.toOstyEmitter()
	g.emitGCSafepoint(emitter)
	g.takeOstyEmitter(emitter)
	g.pushScope()
	args, err := g.userCallArgs(sig, receiverExpr, call)
	if err != nil {
		g.popScope()
		return value{}, err
	}
	emitter = g.toOstyEmitter()
	out := llvmCall(emitter, sig.ret, sig.irName, args)
	g.takeOstyEmitter(emitter)
	g.popScope()
	ret := fromOstyValue(out)
	ret.listElemTyp = sig.retListElemTyp
	ret.listElemString = sig.retListString
	ret.mapKeyTyp = sig.retMapKeyTyp
	ret.mapValueTyp = sig.retMapValueTyp
	ret.mapKeyString = sig.retMapKeyString
	ret.setElemTyp = sig.retSetElemTyp
	ret.setElemString = sig.retSetElemString
	ret.sourceType = sig.returnSourceType
	ret.gcManaged = valueNeedsManagedRoot(ret)
	ret.rootPaths = g.rootPathsForType(sig.ret)
	return ret, nil
}

func (g *generator) emitTestingValueCall(call *ast.CallExpr) (value, bool, error) {
	method, ok := g.testingCallMethod(call)
	if !ok {
		return value{}, false, nil
	}
	switch method {
	case "expectOk":
		v, err := g.emitTestingExpect(call, false)
		return v, true, err
	case "expectError":
		v, err := g.emitTestingExpect(call, true)
		return v, true, err
	default:
		return value{}, false, nil
	}
}

func builtinResultConstructorName(expr ast.Expr) (string, bool) {
	switch fn := expr.(type) {
	case *ast.Ident:
		if fn.Name == "Ok" || fn.Name == "Err" {
			return fn.Name, true
		}
	case *ast.FieldExpr:
		base, ok := fn.X.(*ast.Ident)
		if ok && base.Name == "Result" && (fn.Name == "Ok" || fn.Name == "Err") {
			return fn.Name, true
		}
	case *ast.TurbofishExpr:
		return builtinResultConstructorName(fn.Base)
	}
	return "", false
}

func (g *generator) emitBuiltinResultConstructor(call *ast.CallExpr) (value, bool, error) {
	name, ok := builtinResultConstructorName(call.Fn)
	if !ok {
		return value{}, false, nil
	}
	ctx, ok := g.currentBuiltinResultContext()
	if !ok {
		return value{}, true, unsupportedf("call", "%s requires a concrete Result<T, E> context", name)
	}
	if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, true, unsupportedf("call", "%s requires one positional argument", name)
	}
	payloadIndex := 1
	payloadType := ctx.info.okTyp
	tag := "0"
	if name == "Err" {
		payloadIndex = 2
		payloadType = ctx.info.errTyp
		tag = "1"
	}
	payload, err := g.emitExprWithHintAndSourceType(call.Args[0].Value, builtinResultPayloadSourceType(ctx.sourceType, name), "", false, "", "", false, "", false)
	if err != nil {
		return value{}, true, err
	}
	if payload.typ != payloadType {
		return value{}, true, unsupportedf("type-system", "%s payload type %s, want %s", name, payload.typ, payloadType)
	}
	emitter := g.toOstyEmitter()
	fields := []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: tag}),
		toOstyValue(llvmZeroValue(ctx.info.okTyp)),
		toOstyValue(llvmZeroValue(ctx.info.errTyp)),
	}
	fields[payloadIndex] = toOstyValue(payload)
	out := llvmStructLiteral(emitter, ctx.info.typ, fields)
	g.takeOstyEmitter(emitter)
	result := fromOstyValue(out)
	result.sourceType = ctx.sourceType
	result.rootPaths = g.rootPathsForType(result.typ)
	return result, true, nil
}

func (g *generator) currentBuiltinResultContext() (builtinResultContext, bool) {
	if n := len(g.resultContexts); n != 0 {
		return g.resultContexts[n-1], true
	}
	if info, ok := g.resultTypes[g.returnType]; ok {
		return builtinResultContext{info: info, sourceType: g.returnSourceType}, true
	}
	if len(g.resultTypes) == 1 {
		for _, info := range g.resultTypes {
			return builtinResultContext{info: info}, true
		}
	}
	return builtinResultContext{}, false
}

func (g *generator) optionalUserCallTarget(call *ast.CallExpr) (*fnSig, ast.Type, bool, error) {
	if call == nil {
		return nil, nil, false, nil
	}
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok || !field.IsOptional {
		return nil, nil, false, nil
	}
	baseSource, ok := g.staticExprSourceType(field.X)
	if !ok {
		return nil, nil, true, unsupported("type-system", "optional method receiver type is unknown")
	}
	innerSource, ok := unwrapOptionalSourceType(baseSource)
	if !ok {
		return nil, nil, true, unsupported("type-system", "optional method receiver must have type T?")
	}
	innerTyp, err := llvmType(innerSource, g.typeEnv())
	if err != nil {
		return nil, nil, true, err
	}
	methods := g.methods[innerTyp]
	if methods == nil {
		return nil, nil, false, nil
	}
	sig := methods[field.Name]
	if sig == nil {
		return nil, nil, false, nil
	}
	return sig, innerSource, true, nil
}

func (g *generator) optionalUserReceiverArg(sig *fnSig, innerSource ast.Type, base value) (*LlvmValue, error) {
	if sig == nil || len(sig.params) == 0 {
		return nil, unsupported("call", "optional method receiver signature is invalid")
	}
	innerTyp, err := llvmType(innerSource, g.typeEnv())
	if err != nil {
		return nil, err
	}
	param := sig.params[0]
	if param.byRef {
		if innerTyp == "ptr" {
			return nil, unsupportedf("call", "optional mut receiver for %q is not supported when the inner value is ptr-backed", sig.name)
		}
		if innerTyp != param.typ {
			return nil, unsupportedf("type-system", "receiver for %q type %s, want %s", sig.name, innerTyp, param.typ)
		}
		return &LlvmValue{typ: "ptr", name: base.ref}, nil
	}
	if innerTyp == "ptr" {
		if param.typ != "ptr" {
			return nil, unsupportedf("type-system", "receiver for %q type ptr, want %s", sig.name, param.typ)
		}
		return toOstyValue(base), nil
	}
	if innerTyp != param.typ {
		return nil, unsupportedf("type-system", "receiver for %q type %s, want %s", sig.name, innerTyp, param.typ)
	}
	loaded, err := g.loadTypedPointerValue(base, innerTyp)
	if err != nil {
		return nil, err
	}
	return toOstyValue(loaded), nil
}

func (g *generator) optionalUserCallArgs(sig *fnSig, innerSource ast.Type, base value, call *ast.CallExpr) ([]*LlvmValue, error) {
	if len(sig.params) == 0 {
		return nil, unsupportedf("call", "function %q receiver metadata is missing", sig.name)
	}
	if len(call.Args) != len(sig.params)-1 {
		return nil, unsupportedf("call", "function %q argument count", sig.name)
	}
	receiver, err := g.optionalUserReceiverArg(sig, innerSource, base)
	if err != nil {
		return nil, err
	}
	args := []*LlvmValue{receiver}
	values := make([]value, 0, len(call.Args))
	for i, arg := range call.Args {
		if arg == nil || arg.Name != "" || arg.Value == nil {
			return nil, unsupportedf("call", "function %q requires positional arguments", sig.name)
		}
		param := sig.params[i+1]
		v, err := g.emitExprWithHintAndSourceType(arg.Value, param.sourceType, param.listElemTyp, param.listElemString, param.mapKeyTyp, param.mapValueTyp, param.mapKeyString, param.setElemTyp, param.setElemString)
		if err != nil {
			return nil, err
		}
		if v.typ != param.typ {
			return nil, unsupportedf("type-system", "function %q arg %d type %s, want %s", sig.name, i+1, v.typ, param.typ)
		}
		values = append(values, g.protectManagedTemporary(sig.name+".arg", v))
	}
	for _, v := range values {
		loaded, err := g.loadIfPointer(v)
		if err != nil {
			return nil, err
		}
		args = append(args, toOstyValue(loaded))
	}
	return args, nil
}

func (g *generator) emitOptionalUserCall(call *ast.CallExpr) (value, bool, error) {
	sig, innerSource, found, err := g.optionalUserCallTarget(call)
	if !found || err != nil {
		return value{}, found, err
	}
	if sig.ret == "" || sig.ret == "void" {
		return value{}, true, unsupportedf("call", "function %q has no return value", sig.name)
	}
	if sig.ret != "ptr" {
		return value{}, true, unsupportedf("type-system", "optional method %q currently requires a ptr-backed return type", sig.name)
	}
	field := call.Fn.(*ast.FieldExpr)
	g.pushScope()
	defer g.popScope()
	base, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	base = g.protectManagedTemporary(sig.name+".optional.self", base)
	baseValue, err := g.loadIfPointer(base)
	if err != nil {
		return value{}, true, err
	}
	out, err := g.emitOptionalPtrExpr(baseValue, func() (value, error) {
		emitter := g.toOstyEmitter()
		g.emitGCSafepoint(emitter)
		g.takeOstyEmitter(emitter)
		args, err := g.optionalUserCallArgs(sig, innerSource, baseValue, call)
		if err != nil {
			return value{}, err
		}
		emitter = g.toOstyEmitter()
		next := llvmCall(emitter, sig.ret, sig.irName, args)
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(next)
		v.listElemTyp = sig.retListElemTyp
		v.listElemString = sig.retListString
		v.mapKeyTyp = sig.retMapKeyTyp
		v.mapValueTyp = sig.retMapValueTyp
		v.mapKeyString = sig.retMapKeyString
		v.setElemTyp = sig.retSetElemTyp
		v.setElemString = sig.retSetElemString
		v.sourceType = wrapOptionalSourceType(sig.returnSourceType)
		v.gcManaged = valueNeedsManagedRoot(v)
		return v, nil
	})
	if err != nil {
		return value{}, true, err
	}
	out.listElemTyp = sig.retListElemTyp
	out.listElemString = sig.retListString
	out.mapKeyTyp = sig.retMapKeyTyp
	out.mapValueTyp = sig.retMapValueTyp
	out.mapKeyString = sig.retMapKeyString
	out.setElemTyp = sig.retSetElemTyp
	out.setElemString = sig.retSetElemString
	out.sourceType = wrapOptionalSourceType(sig.returnSourceType)
	out.gcManaged = valueNeedsManagedRoot(out)
	out.rootPaths = g.rootPathsForType(sig.ret)
	return out, true, nil
}

func (g *generator) userCallArgs(sig *fnSig, receiverExpr ast.Expr, call *ast.CallExpr) ([]*LlvmValue, error) {
	expectedArgs := len(sig.params)
	if receiverExpr != nil {
		expectedArgs--
	}
	if len(call.Args) != expectedArgs {
		return nil, unsupportedf("call", "function %q argument count", sig.name)
	}
	args := make([]*LlvmValue, 0, len(sig.params))
	paramIndex := 0
	if receiverExpr != nil {
		receiver, err := g.userCallReceiverArg(sig, sig.params[0], receiverExpr)
		if err != nil {
			return nil, err
		}
		args = append(args, receiver)
		paramIndex = 1
	}
	values := make([]value, 0, len(call.Args))
	for i, arg := range call.Args {
		if arg.Name != "" || arg.Value == nil {
			return nil, unsupportedf("call", "function %q requires positional arguments", sig.name)
		}
		param := sig.params[paramIndex+i]
		v, err := g.emitExprWithHintAndSourceType(arg.Value, param.sourceType, param.listElemTyp, param.listElemString, param.mapKeyTyp, param.mapValueTyp, param.mapKeyString, param.setElemTyp, param.setElemString)
		if err != nil {
			return nil, err
		}
		// Phase 6d: auto-box a concrete value when the callee expects
		// an interface. Mirrors the `let s: Sized = v` and return-value
		// paths so call-site conversion lands consistently.
		if param.typ == "%osty.iface" && v.typ != "%osty.iface" && param.sourceType != nil {
			boxed, boxErr := g.boxInterfaceValue(param.sourceType, v)
			if boxErr != nil {
				return nil, boxErr
			}
			v = boxed
		}
		if v.typ != param.typ {
			return nil, unsupportedf("type-system", "function %q arg %d type %s, want %s", sig.name, i+1, v.typ, param.typ)
		}
		values = append(values, g.protectManagedTemporary(sig.name+".arg", v))
	}
	for _, v := range values {
		loaded, err := g.loadIfPointer(v)
		if err != nil {
			return nil, err
		}
		args = append(args, toOstyValue(loaded))
	}
	return args, nil
}

func (g *generator) userCallReceiverArg(sig *fnSig, param paramInfo, receiverExpr ast.Expr) (*LlvmValue, error) {
	if param.byRef {
		id, ok := receiverExpr.(*ast.Ident)
		if !ok {
			return nil, unsupportedf("call", "mut receiver for %q must be a local binding", sig.name)
		}
		slot, ok := g.lookupBinding(id.Name)
		if !ok {
			return nil, unsupportedf("name", "unknown receiver binding %q", id.Name)
		}
		if !slot.ptr || slot.typ != param.typ {
			return nil, unsupportedf("type-system", "receiver for %q must be mutable %s", sig.name, param.typ)
		}
		return &LlvmValue{typ: "ptr", name: slot.ref}, nil
	}
	v, err := g.emitExpr(receiverExpr)
	if err != nil {
		return nil, err
	}
	if v.typ != param.typ {
		return nil, unsupportedf("type-system", "receiver for %q type %s, want %s", sig.name, v.typ, param.typ)
	}
	protected := g.protectManagedTemporary(sig.name+".self", v)
	loaded, err := g.loadIfPointer(protected)
	if err != nil {
		return nil, err
	}
	return toOstyValue(loaded), nil
}

func (g *generator) userCallTarget(call *ast.CallExpr) (*fnSig, ast.Expr, bool, error) {
	if call == nil {
		return nil, nil, false, nil
	}
	switch fn := call.Fn.(type) {
	case *ast.Ident:
		sig := g.functions[fn.Name]
		if sig == nil {
			return nil, nil, false, nil
		}
		return sig, nil, true, nil
	case *ast.FieldExpr:
		if fn.IsOptional {
			return nil, nil, false, nil
		}
		baseInfo, ok := g.staticExprInfo(fn.X)
		if !ok {
			return nil, nil, false, nil
		}
		methods := g.methods[baseInfo.typ]
		if methods == nil {
			return nil, nil, false, nil
		}
		sig := methods[fn.Name]
		if sig == nil {
			return nil, nil, false, nil
		}
		return sig, fn.X, true, nil
	default:
		return nil, nil, false, nil
	}
}

func (g *generator) emitEnumVariantCall(call *ast.CallExpr) (value, bool, error) {
	ref, found, err := g.enumVariantCallTarget(call)
	if !found || err != nil {
		return value{}, found, err
	}
	if len(call.Args) != len(ref.variant.payloads) {
		return value{}, true, unsupportedf("call", "enum variant %q argument count", ref.variant.name)
	}
	if len(ref.variant.payloads) == 0 {
		out, err := g.enumVariantConstant(ref.enum, ref.variant)
		return out, true, err
	}
	if !ref.enum.hasPayload {
		return value{}, true, unsupportedf("expression", "enum %q has no payload layout", ref.enum.name)
	}
	arg := call.Args[0]
	if arg.Name != "" || arg.Value == nil {
		return value{}, true, unsupportedf("call", "enum variant %q requires positional payload", ref.variant.name)
	}
	payload, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, true, err
	}
	if payload.typ != ref.variant.payloads[0] {
		return value{}, true, unsupportedf("type-system", "enum variant %q payload type %s, want %s", ref.variant.name, payload.typ, ref.variant.payloads[0])
	}
	out, err := g.emitEnumPayloadVariant(ref.enum, ref.variant, payload)
	return out, true, err
}

func (g *generator) enumVariantCallTarget(call *ast.CallExpr) (enumVariantRef, bool, error) {
	switch fn := call.Fn.(type) {
	case *ast.Ident:
		found, count := g.findBareEnumVariant(fn.Name)
		if count == 0 {
			return enumVariantRef{}, false, nil
		}
		if count > 1 {
			return enumVariantRef{}, true, unsupportedf("name", "ambiguous enum variant %q", fn.Name)
		}
		return found, true, nil
	case *ast.FieldExpr:
		found, ok := g.enumVariantByField(fn)
		return found, ok, nil
	default:
		return enumVariantRef{}, false, nil
	}
}

// emitInterfaceMethodCall dispatches a method call whose receiver is
// an interface value (`%osty.iface` fat pointer). The fat pointer is
// split into (data, vtable), the correct function-pointer slot is
// loaded from the vtable array, and the call is emitted as an
// `indirect call` with the data pointer threaded in as `self`.
//
// Phase 6b scope: zero non-self arguments only. Methods carrying
// extra parameters return `found=true` with an `unsupported`
// diagnostic so callers know the dispatch path was recognised.
func (g *generator) emitInterfaceMethodCall(call *ast.CallExpr) (value, bool, error) {
	fx, ok := call.Fn.(*ast.FieldExpr)
	if !ok || fx == nil {
		return value{}, false, nil
	}
	recvInfo, ok := g.staticExprInfo(fx.X)
	if !ok || recvInfo.typ != "%osty.iface" {
		return value{}, false, nil
	}
	recv, err := g.emitExpr(fx.X)
	if err != nil {
		return value{}, true, err
	}
	iface, slot := g.findInterfaceMethod(fx.Name)
	if iface == nil {
		return value{}, true, unsupportedf("call", "no interface declares method %q", fx.Name)
	}
	if slot < 0 || slot >= len(iface.decl.Methods) {
		return value{}, true, unsupportedf("call", "interface %q has no slot for %q", iface.name, fx.Name)
	}
	methodDecl := iface.decl.Methods[slot]
	if methodDecl == nil || methodDecl.ReturnType == nil {
		return value{}, true, unsupportedf("call", "interface method %q has no return type", fx.Name)
	}
	// Phase 6c: interface method params are lowered individually and
	// threaded into the indirect call as extra LLVM arguments. The
	// receiver slot is carried by `FnDecl.Recv` (not `Params`), so
	// `methodDecl.Params` already lists the non-self user-facing args
	// 1:1 with `call.Args`.
	nonSelfParams := methodDecl.Params
	if len(nonSelfParams) != len(call.Args) {
		return value{}, true, unsupportedf("call",
			"interface method %q expects %d non-self args, got %d",
			fx.Name, len(nonSelfParams), len(call.Args))
	}
	argPairs := make([][2]string, 0, len(nonSelfParams))
	for i, p := range nonSelfParams {
		if p == nil || p.Type == nil {
			return value{}, true, unsupportedf("call", "interface method %q arg %d has no type", fx.Name, i)
		}
		paramTyp, err := llvmType(p.Type, g.typeEnv())
		if err != nil {
			return value{}, true, err
		}
		av, err := g.emitExpr(call.Args[i].Value)
		if err != nil {
			return value{}, true, err
		}
		if av.typ != paramTyp {
			return value{}, true, unsupportedf("call",
				"interface method %q arg %d: expected %s, got %s",
				fx.Name, i, paramTyp, av.typ)
		}
		argPairs = append(argPairs, [2]string{paramTyp, av.ref})
	}
	retTyp, err := llvmType(methodDecl.ReturnType, g.typeEnv())
	if err != nil {
		return value{}, true, err
	}
	emitter := g.toOstyEmitter()
	dataPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = extractvalue %%osty.iface %s, 0", dataPtr, recv.ref))
	vtable := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = extractvalue %%osty.iface %s, 1", vtable, recv.ref))
	fnPtrSlot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  %s = getelementptr [%d x ptr], ptr %s, i64 0, i64 %d",
		fnPtrSlot, len(iface.methods), vtable, slot,
	))
	fnPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = load ptr, ptr %s", fnPtr, fnPtrSlot))
	callArgList := fmt.Sprintf("ptr %s", dataPtr)
	for _, p := range argPairs {
		callArgList += fmt.Sprintf(", %s %s", p[0], p[1])
	}
	ret := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf(
		"  %s = call %s %s(%s)",
		ret, retTyp, fnPtr, callArgList,
	))
	g.takeOstyEmitter(emitter)
	return value{typ: retTyp, ref: ret}, true, nil
}

// findInterfaceMethod locates the (interface, slot) pair for a method
// name across every known interface. Returns (nil, -1) when no
// interface declares the name. Ambiguity — two different interfaces
// declaring the same method name — picks the first match; a stricter
// resolution (requiring the receiver's static interface type) is a
// Phase 6c refinement.
func (g *generator) findInterfaceMethod(name string) (*interfaceInfo, int) {
	for _, iface := range g.interfacesByName {
		if iface == nil {
			continue
		}
		for i, m := range iface.methods {
			if m.name == name {
				return iface, i
			}
		}
	}
	return nil, -1
}
