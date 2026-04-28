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
	if p, ok := pattern.(*ast.BindingPat); ok {
		cond, bind, err := g.ifLetCondition(p.Pattern, scrutinee)
		if err != nil {
			return nil, nil, err
		}
		return cond, func() error {
			if err := g.bindLetPattern(&ast.IdentPat{Name: p.Name}, scrutinee, false); err != nil {
				return err
			}
			if bind != nil {
				return bind()
			}
			return nil
		}, nil
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
	if litPat, ok := pattern.(*ast.LiteralPat); ok && litPat.Literal != nil && isPrimitiveLiteralMatchScrutineeType(scrutinee.typ) {
		litValue, err := g.emitExpr(litPat.Literal)
		if err != nil {
			return nil, nil, err
		}
		if litValue.typ != scrutinee.typ {
			return nil, nil, unsupportedf("type-system", "match literal type %s, want %s", litValue.typ, scrutinee.typ)
		}
		emitter := g.toOstyEmitter()
		cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(litValue))
		g.takeOstyEmitter(emitter)
		return cond, func() error { return nil }, nil
	}
	tag, ok, err := g.matchEnumTag(pattern)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		if _, ok := pattern.(*ast.IdentPat); ok {
			return toOstyValue(value{typ: "i1", ref: "true"}), func() error {
				return g.bindLetPattern(pattern, scrutinee, false)
			}, nil
		}
		return nil, nil, unsupported("control-flow", "if-let pattern must be an enum variant")
	}
	emitter := g.toOstyEmitter()
	cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(value{typ: "i64", ref: strconv.Itoa(tag)}))
	g.takeOstyEmitter(emitter)
	return cond, func() error { return nil }, nil
}

func (g *generator) matchNeedsGenericConditionPath(scrutinee value, arms []*ast.MatchArm) bool {
	if len(arms) == 0 {
		return false
	}
	payloadInfo := g.enumsByType[scrutinee.typ]
	if payloadInfo != nil && payloadInfo.hasPayload {
		for _, arm := range arms {
			if arm == nil || arm.Pattern == nil {
				continue
			}
			switch pat := arm.Pattern.(type) {
			case *ast.BindingPat:
				return true
			case *ast.IdentPat:
				if _, ok, err := g.matchPayloadEnumPattern(payloadInfo, pat); err == nil && !ok {
					return true
				}
			}
		}
		return false
	}
	if scrutinee.typ == "i64" || isPrimitiveLiteralMatchScrutineeType(scrutinee.typ) {
		literalSetOnly := isPrimitiveLiteralMatchArms(arms)
		for _, arm := range arms {
			if arm == nil || arm.Pattern == nil {
				continue
			}
			switch pat := arm.Pattern.(type) {
			case *ast.BindingPat:
				return true
			case *ast.IdentPat:
				if _, ok, err := g.matchEnumTag(pat); err == nil && !ok {
					return true
				}
			case *ast.LiteralPat:
				if !literalSetOnly {
					return true
				}
			}
		}
	}
	return false
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
		v := fromOstyValue(out)
		// Tag the literal with its source type so downstream binders
		// (`let mut line = ""`) propagate the String-ness forward —
		// without this, a subsequent `[line, otherString]` list literal
		// fails the isString parity check because the local's
		// sourceType is nil.
		v.sourceType = &ast.NamedType{Path: []string{"String"}}
		return v, nil
	}
	v, err := g.emitInterpolatedString(lit)
	if err != nil {
		return v, err
	}
	if v.sourceType == nil {
		v.sourceType = &ast.NamedType{Path: []string{"String"}}
	}
	return v, nil
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
		piece, err := g.emitInterpolationStringPiece(v)
		if err != nil {
			return value{}, err
		}
		pieces = append(pieces, piece)
	}
	if len(pieces) >= 3 {
		r, err := g.emitRuntimeStringConcatN(pieces)
		if err != nil {
			return value{}, err
		}
		r.sourceType = &ast.NamedType{Path: []string{"String"}}
		return r, nil
	}
	result := pieces[0]
	for i := 1; i < len(pieces); i++ {
		r, err := g.emitRuntimeStringConcat(result, pieces[i])
		if err != nil {
			return value{}, err
		}
		result = r
	}
	result.sourceType = &ast.NamedType{Path: []string{"String"}}
	return result, nil
}

// emitRuntimeStringConcatN lowers an N-way string concatenation as
// one call to `osty_rt_strings_ConcatN(count, parts_ptr)` instead of
// N-1 chained two-arg Concat calls. Saves N-2 intermediate allocations
// per interpolation site; measurable on hot paths that build keys
// inside loops (e.g. record_pipeline's `"{service}/{region}/{level}"`
// maps to one alloc per row instead of four).
//
// The array of parts is materialized in a stack alloca at the current
// block — free for the caller and automatically dead after the call.
func (g *generator) emitRuntimeStringConcatN(pieces []value) (value, error) {
	symbol := mirRtStringConcatNSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{
		{typ: "i64"},
		{typ: "ptr"},
	})
	emitter := g.toOstyEmitter()
	// Stack-allocate `[N x ptr]` and store each piece into the slot.
	arr := llvmNextTemp(emitter)
	piecesN := strconv.Itoa(len(pieces))
	emitter.body = append(emitter.body, mirAllocaArrayPtrText(arr, piecesN))
	for i, piece := range pieces {
		slot := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirGEPArrayElementText(slot, piecesN, arr, strconv.Itoa(i)))
		emitter.body = append(emitter.body, mirStorePtrText(piece.ref, slot))
	}
	out := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirCallRuntimePtrI64PtrText(out, symbol, piecesN, arr))
	g.takeOstyEmitter(emitter)
	joined := value{typ: "ptr", ref: out, gcManaged: true}
	return joined, nil
}

func (g *generator) emitInterpolationStringPiece(v value) (value, error) {
	switch v.typ {
	case "ptr":
		if v.listElemTyp == "" && v.mapKeyTyp == "" && v.setElemTyp == "" {
			return v, nil
		}
	case "i64":
		return g.emitRuntimeIntToString(v)
	case "double":
		return g.emitRuntimeFloatToString(v)
	case "i1":
		return g.emitRuntimeBoolToString(v)
	case "i32":
		return g.emitRuntimeCharToString(v)
	case "i8":
		return g.emitRuntimeByteToString(v)
	}
	return value{}, unsupportedf("type-system", "interpolation of %s value requires .toString() which the LLVM backend does not yet lower", v.typ)
}

func (g *generator) emitExpr(expr ast.Expr) (value, error) {
	switch e := expr.(type) {
	case *ast.IntLit:
		text := strings.ReplaceAll(e.Text, "_", "")
		// Base 0 so 0x / 0b / 0o prefixes parse correctly alongside
		// plain decimal literals. Osty grammar allows all four; the
		// previous base-10 pin silently rejected hex like 0x10000.
		n, err := strconv.ParseInt(text, 0, 64)
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
	case *ast.CharLit:
		return value{typ: "i32", ref: strconv.FormatInt(int64(e.Value), 10)}, nil
	case *ast.ByteLit:
		return value{typ: "i8", ref: strconv.FormatInt(int64(e.Value), 10)}, nil
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
		return g.emitListExprWithHint(e, nil, "", false)
	case *ast.MapExpr:
		return g.emitMapExprWithHint(e, nil, nil, "", "", false)
	case *ast.RangeExpr:
		return g.emitRangeExpr(e)
	case *ast.StructLit:
		return g.emitStructLit(e)
	case *ast.IfExpr:
		return g.emitIfExprValue(e)
	case *ast.MatchExpr:
		return g.emitMatchExprValue(e)
	default:
		return value{}, unsupportedf("expression", "expression %T %s", expr, exprPosLabel(expr))
	}
}

func (g *generator) emitRangeExpr(expr *ast.RangeExpr) (value, error) {
	if expr == nil {
		return value{}, unsupported("expression", "nil range literal")
	}
	if expr.Step != nil {
		return value{}, unsupported("expression", "range literal with step is not supported yet")
	}
	sourceType, _ := g.staticRangeExprSourceType(expr)
	info, ok := builtinRangeTypeFromAST(sourceType, g.typeEnv())
	if !ok {
		info = builtinRangeType{
			typ:     llvmRangeTypeName("i64"),
			elemTyp: "i64",
		}
		sourceType = &ast.NamedType{Path: []string{"Range"}, Args: []ast.Type{&ast.NamedType{Path: []string{"Int"}}}}
	}
	if g.rangeTypes == nil {
		g.rangeTypes = map[string]builtinRangeType{}
	}
	g.rangeTypes[info.typ] = info

	emitBound := func(bound ast.Expr) (value, bool, error) {
		if bound == nil {
			return value{typ: info.elemTyp, ref: llvmZeroLiteral(info.elemTyp)}, false, nil
		}
		v, err := g.emitExpr(bound)
		if err != nil {
			return value{}, false, err
		}
		if v.typ != info.elemTyp {
			return value{}, false, unsupportedf("type-system", "range bound type %s, want %s", v.typ, info.elemTyp)
		}
		return v, true, nil
	}

	startVal, hasStart, err := emitBound(expr.Start)
	if err != nil {
		return value{}, err
	}
	stopVal, hasStop, err := emitBound(expr.Stop)
	if err != nil {
		return value{}, err
	}

	emitter := g.toOstyEmitter()
	out := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(startVal),
		toOstyValue(stopVal),
		toOstyValue(value{typ: "i1", ref: strconv.FormatBool(hasStart)}),
		toOstyValue(value{typ: "i1", ref: strconv.FormatBool(hasStop)}),
		toOstyValue(value{typ: "i1", ref: strconv.FormatBool(expr.Inclusive)}),
	})
	g.takeOstyEmitter(emitter)
	loaded := fromOstyValue(out)
	loaded.sourceType = sourceType
	return loaded, nil
}

// exprPosLabel formats the AST node's source line/column and byte
// offset for LLVMXXX wall messages. The merged-toolchain probes
// synthesize paths like /tmp/toolchain_native_merged.osty, so file is
// usually not useful — but line:col plus byte-offset plus `%T` lets
// the investigator grep the merge buffer and pinpoint the offending
// expression immediately.
func exprPosLabel(node ast.Node) string {
	if node == nil {
		return ""
	}
	p := node.Pos()
	if p.Line == 0 && p.Column == 0 && p.Offset == 0 {
		return ""
	}
	return fmt.Sprintf("at %s (offset %d)", p, p.Offset)
}

func (g *generator) emitIdent(name string) (value, error) {
	if v, ok := g.lookupBinding(name); ok {
		return g.loadIfPointer(v)
	}
	if v, found, err := g.enumVariantIdent(name); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitBuiltinOptionNone(name); found || err != nil {
		return v, err
	}
	// Phase 1: bare top-level fn reference used in value position.
	// Materialise a 1-field closure env pointing at a synthesised
	// thunk so the uniform env-first-arg call ABI works for plain
	// fn refs without captures. See internal/llvmgen/fn_value.go.
	if sig, ok := g.functions[name]; ok && sig != nil && sig.receiverType == "" {
		return g.emitFnValueEnv(sig)
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
	emitter.body = append(emitter.body, mirLoadText(tmp, typ, addr.ref))
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
	emitter.body = append(emitter.body, mirBrCondText(isNil.name, nilLabel, thenLabel))
	emitter.body = append(emitter.body, mirLabelText(thenLabel))
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
	emitter.body = append(emitter.body, mirLabelText(nilLabel))
	emitter.body = append(emitter.body, mirBrUncondText(endLabel))
	emitter.body = append(emitter.body, mirLabelText(endLabel))
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirPhiPtrFromValueOrNullText(tmp, thenValue.ref, thenPred, nilLabel))
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
	emitter.body = append(emitter.body, mirBrCondText(isNil.name, nilLabel, thenLabel))
	emitter.body = append(emitter.body, mirLabelText(thenLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(thenLabel)
	if err := then(); err != nil {
		return err
	}
	if g.currentReachable {
		g.branchTo(endLabel)
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(nilLabel))
	emitter.body = append(emitter.body, mirBrUncondText(endLabel))
	emitter.body = append(emitter.body, mirLabelText(endLabel))
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
	rangeSource := base.sourceType
	if rangeSource == nil {
		rangeSource, _ = g.staticExprSourceType(expr.X)
	}
	if field, ok := g.builtinRangeFieldInfo(rangeSource, expr.Name); ok {
		emitter := g.toOstyEmitter()
		out := llvmExtractValue(emitter, toOstyValue(base), field.typ, field.index)
		g.takeOstyEmitter(emitter)
		loaded := fromOstyValue(out)
		loaded.sourceType = field.sourceType
		if err := g.decorateValueFromSourceType(&loaded, field.sourceType); err != nil {
			return value{}, err
		}
		loaded.gcManaged = valueNeedsManagedRoot(loaded)
		loaded.rootPaths = g.rootPathsForType(field.typ)
		return loaded, nil
	}
	info := g.structsByType[base.typ]
	if info == nil {
		return value{}, unsupportedf("type-system", "field %q access on %s %s", expr.Name, base.typ, exprPosLabel(expr))
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
	if err := g.decorateValueFromSourceType(&loaded, field.sourceType); err != nil {
		return value{}, err
	}
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
	if rng, ok := expr.Index.(*ast.RangeExpr); ok {
		return g.emitSliceIndex(expr, rng)
	}
	base, err := g.emitExpr(expr.X)
	if err != nil {
		return value{}, err
	}
	index, err := g.emitExpr(expr.Index)
	if err != nil {
		return value{}, err
	}
	if info, ok := g.rangeTypes[index.typ]; ok {
		return g.emitSliceIndexByRangeValue(expr, base, index, info)
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
			return g.decorateStaticValueFromSourceType(v, expr), nil
		}
		traceSymbol := g.traceCallbackSymbol(base.listElemTyp, g.rootPathsForType(base.listElemTyp))
		emitter := g.toOstyEmitter()
		slot := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirAllocaText(slot, base.listElemTyp))
		sizeValue := g.emitTypeSize(emitter, base.listElemTyp)
		g.declareRuntimeSymbol(listRuntimeGetBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			listRuntimeGetBytesSymbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(base), toOstyValue(index), {typ: "ptr", name: slot}, sizeValue, {typ: "ptr", name: llvmPointerOperand(traceSymbol)}}),
		))
		loaded := g.loadValueFromAddress(emitter, base.listElemTyp, slot)
		g.takeOstyEmitter(emitter)
		loaded.rootPaths = g.rootPathsForType(base.listElemTyp)
		return g.decorateStaticValueFromSourceType(loaded, expr), nil
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
		emitter.body = append(emitter.body, mirAllocaText(slot, base.mapValueTyp))
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			symbol,
			llvmCallArgs([]*LlvmValue{toOstyValue(base), toOstyValue(loadedKey), {typ: "ptr", name: slot}}),
		))
		out := g.loadValueFromAddress(emitter, base.mapValueTyp, slot)
		g.takeOstyEmitter(emitter)
		out.gcManaged = base.mapValueTyp == "ptr"
		out.rootPaths = g.rootPathsForType(base.mapValueTyp)
		return g.decorateStaticValueFromSourceType(out, expr), nil
	default:
		return value{}, unsupportedf("expression", "index expression on %s", base.typ)
	}
}

// emitSliceIndex lowers `base[a..b]` and `base[a..=b]` for both String
// and List<T> receivers. Dispatch is driven by the base's static source
// type — String routes to osty_rt_strings_Slice (byte-level), List
// routes to osty_rt_list_slice (elem_size-level memcpy). Non-slicable
// receivers surface a type-system diagnostic.
func (g *generator) emitSliceIndex(expr *ast.IndexExpr, rng *ast.RangeExpr) (value, error) {
	base, err := g.emitExpr(expr.X)
	if err != nil {
		return value{}, err
	}
	if base.listElemTyp != "" {
		return g.emitListSliceIndex(expr, rng, base)
	}
	if err := g.requireStringSliceBase(expr.X, base); err != nil {
		return value{}, err
	}
	base, err = g.loadIfPointer(base)
	if err != nil {
		return value{}, err
	}

	var startVal value
	if rng.Start == nil {
		startVal = value{typ: "i64", ref: "0"}
	} else {
		v, err := g.emitExpr(rng.Start)
		if err != nil {
			return value{}, err
		}
		if v.typ != "i64" {
			return value{}, unsupportedf("type-system", "slice start type %s, want Int", v.typ)
		}
		startVal = v
	}

	var endVal value
	if rng.Stop == nil {
		g.declareRuntimeSymbol(llvmStringRuntimeByteLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		lenVal := llvmCall(emitter, "i64", llvmStringRuntimeByteLenSymbol(), []*LlvmValue{toOstyValue(base)})
		g.takeOstyEmitter(emitter)
		endVal = fromOstyValue(lenVal)
	} else {
		v, err := g.emitExpr(rng.Stop)
		if err != nil {
			return value{}, err
		}
		if v.typ != "i64" {
			return value{}, unsupportedf("type-system", "slice end type %s, want Int", v.typ)
		}
		endVal = v
		if rng.Inclusive {
			emitter := g.toOstyEmitter()
			tmp := llvmNextTemp(emitter)
			emitter.body = append(emitter.body, mirAddI64OneText(tmp, endVal.ref))
			g.takeOstyEmitter(emitter)
			endVal = value{typ: "i64", ref: tmp}
		}
	}

	g.declareRuntimeSymbol(llvmStringRuntimeSliceSymbol(), "ptr", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	out := llvmStringRuntimeSlice(emitter, toOstyValue(base), toOstyValue(startVal), toOstyValue(endVal))
	g.takeOstyEmitter(emitter)
	sliced := fromOstyValue(out)
	sliced.gcManaged = true
	sliced.sourceType = &ast.NamedType{Path: []string{"String"}}
	return sliced, nil
}

// emitListSliceIndex lowers `list[a..b]` and `list[a..=b]` on a List<T>
// receiver to osty_rt_list_slice. Bounds default to 0 and list.len —
// runtime applies saturating clamps, matching String.slice semantics.
// Inclusive `..=` adds 1 to the end operand before the call.
func (g *generator) emitListSliceIndex(expr *ast.IndexExpr, rng *ast.RangeExpr, base value) (value, error) {
	baseLoaded, err := g.loadIfPointer(base)
	if err != nil {
		return value{}, err
	}

	var startVal value
	if rng.Start == nil {
		startVal = value{typ: "i64", ref: "0"}
	} else {
		v, err := g.emitExpr(rng.Start)
		if err != nil {
			return value{}, err
		}
		if v.typ != "i64" {
			return value{}, unsupportedf("type-system", "slice start type %s, want Int", v.typ)
		}
		startVal = v
	}

	var endVal value
	if rng.Stop == nil {
		g.declareRuntimeSymbol(listRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		lenVal := llvmCall(emitter, "i64", listRuntimeLenSymbol(), []*LlvmValue{toOstyValue(baseLoaded)})
		g.takeOstyEmitter(emitter)
		endVal = fromOstyValue(lenVal)
	} else {
		v, err := g.emitExpr(rng.Stop)
		if err != nil {
			return value{}, err
		}
		if v.typ != "i64" {
			return value{}, unsupportedf("type-system", "slice end type %s, want Int", v.typ)
		}
		endVal = v
		if rng.Inclusive {
			emitter := g.toOstyEmitter()
			tmp := llvmNextTemp(emitter)
			emitter.body = append(emitter.body, mirAddI64OneText(tmp, endVal.ref))
			g.takeOstyEmitter(emitter)
			endVal = value{typ: "i64", ref: tmp}
		}
	}

	g.declareRuntimeSymbol(listRuntimeSliceSymbol(), "ptr", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", listRuntimeSliceSymbol(), []*LlvmValue{toOstyValue(baseLoaded), toOstyValue(startVal), toOstyValue(endVal)})
	g.takeOstyEmitter(emitter)
	sliced := fromOstyValue(out)
	sliced.gcManaged = true
	sliced.listElemTyp = base.listElemTyp
	sliced.listElemString = base.listElemString
	sliced.sourceType = base.sourceType
	sliced.rootPaths = g.rootPathsForType(base.listElemTyp)
	return sliced, nil
}

// Range aggregate layout — kept in lockstep with the struct def emitted
// at generator.go (fields: elemTyp, elemTyp, i1, i1, i1). If the layout
// ever gains a field, update builtinRangeFieldInfo in type.go and the
// type def site together.
const (
	rangeFieldStart     = 0
	rangeFieldStop      = 1
	rangeFieldHasStart  = 2
	rangeFieldHasStop   = 3
	rangeFieldInclusive = 4
)

// emitSliceIndexByRangeValue lowers `base[r]` where `r` is a Range<T>
// value (param, let binding, or expression result) rather than an inline
// range literal. The Range aggregate is destructured at runtime and fed
// to the same osty_rt_list_slice / osty_rt_strings_Slice helpers as the
// literal path. Element type is restricted to i64 (Range<Int>) to match
// the literal slicer's bound constraints.
func (g *generator) emitSliceIndexByRangeValue(expr *ast.IndexExpr, base value, rangeVal value, info builtinRangeType) (value, error) {
	if info.elemTyp != "i64" {
		return value{}, unsupportedf("type-system", "slice range element type %s, want i64", info.elemTyp)
	}

	isList := base.listElemTyp != ""
	if !isList {
		if err := g.requireStringSliceBase(expr.X, base); err != nil {
			return value{}, err
		}
	}
	baseLoaded, err := g.loadIfPointer(base)
	if err != nil {
		return value{}, err
	}

	var lenSym, sliceSym string
	if isList {
		lenSym = listRuntimeLenSymbol()
		sliceSym = listRuntimeSliceSymbol()
	} else {
		lenSym = llvmStringRuntimeByteLenSymbol()
		sliceSym = llvmStringRuntimeSliceSymbol()
	}
	g.declareRuntimeSymbol(lenSym, "i64", []paramInfo{{typ: "ptr"}})
	g.declareRuntimeSymbol(sliceSym, "ptr", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "i64"}})

	emitter := g.toOstyEmitter()
	startRaw := llvmExtractValue(emitter, toOstyValue(rangeVal), "i64", rangeFieldStart)
	stopRaw := llvmExtractValue(emitter, toOstyValue(rangeVal), "i64", rangeFieldStop)
	hasStart := llvmExtractValue(emitter, toOstyValue(rangeVal), "i1", rangeFieldHasStart)
	hasStop := llvmExtractValue(emitter, toOstyValue(rangeVal), "i1", rangeFieldHasStop)
	inclusive := llvmExtractValue(emitter, toOstyValue(rangeVal), "i1", rangeFieldInclusive)

	startSel := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSelectI64LiteralRhsText(startSel, hasStart.name, startRaw.name, "0"))
	stopPlus := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAddI64OneText(stopPlus, stopRaw.name))
	stopIncl := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSelectI64Text(stopIncl, inclusive.name, stopPlus, stopRaw.name))

	lenVal := llvmCall(emitter, "i64", lenSym, []*LlvmValue{toOstyValue(baseLoaded)})
	endSel := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSelectI64Text(endSel, hasStop.name, stopIncl, lenVal.name))

	startVal := value{typ: "i64", ref: startSel}
	endVal := value{typ: "i64", ref: endSel}
	out := llvmCall(emitter, "ptr", sliceSym, []*LlvmValue{toOstyValue(baseLoaded), toOstyValue(startVal), toOstyValue(endVal)})
	g.takeOstyEmitter(emitter)

	sliced := fromOstyValue(out)
	sliced.gcManaged = true
	if isList {
		sliced.listElemTyp = base.listElemTyp
		sliced.listElemString = base.listElemString
		sliced.sourceType = base.sourceType
		sliced.rootPaths = g.rootPathsForType(base.listElemTyp)
	} else {
		sliced.sourceType = &ast.NamedType{Path: []string{"String"}}
	}
	return sliced, nil
}

// requireStringSliceBase rejects non-String ptr receivers at the slice
// index site. Shared between literal-range and range-value slice paths
// so the diagnostic phrasing stays identical.
func (g *generator) requireStringSliceBase(x ast.Expr, base value) error {
	if base.typ != "ptr" {
		return unsupportedf("type-system", "slice indexing on %s, want String (ptr) or List<T>", base.typ)
	}
	if sourceType, ok := g.staticExprSourceType(x); ok {
		if resolved, resErr := llvmResolveAliasType(sourceType, g.typeEnv(), map[string]bool{}); resErr == nil {
			if llvmNamedTypeIsString(resolved) {
				return nil
			}
		}
	}
	return unsupported("type-system", "slice indexing is only supported on String or List<T> receivers")
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
		return g.emitEnumPayloadVariant(info, variant, nil)
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

func (g *generator) emitEnumPayloadVariant(info *enumInfo, variant variantInfo, payloads []value) (value, error) {
	if !info.hasPayload {
		return value{}, unsupportedf("expression", "enum %q has no payload layout", info.name)
	}
	if info.isBoxed {
		if len(payloads) > 1 {
			for _, p := range payloads {
				if p.typ == "ptr" {
					return value{}, unsupportedf("type-system", "enum %q boxed multi-field payload with ptr field is not supported", info.name)
				}
			}
			emitter := g.toOstyEmitter()
			site := "enum." + info.name + "." + variant.name
			byteSize := len(payloads) * 8
			heapPtr := llvmGcAlloc(emitter, 1, byteSize, site)
			for i, p := range payloads {
				gep := llvmNextTemp(emitter)
				emitter.body = append(emitter.body, mirGEPInboundsI8Text(gep, heapPtr.name, strconv.Itoa(i*8)))
				emitter.body = append(emitter.body, mirStoreText(p.typ, p.ref, gep))
			}
			out := llvmStructLiteral(emitter, info.typ, []*LlvmValue{llvmEnumVariant(info.typ, variant.tag), heapPtr})
			g.takeOstyEmitter(emitter)
			g.needsGCRuntime = true
			enumValue := fromOstyValue(out)
			enumValue.rootPaths = g.rootPathsForType(info.typ)
			return enumValue, nil
		}
		var payload value
		if len(payloads) == 1 {
			payload = payloads[0]
		} else {
			payload = value{typ: "ptr", ref: "null"}
		}
		emitter := g.toOstyEmitter()
		site := "enum." + info.name + "." + variant.name
		out := llvmEnumBoxedPayloadVariant(emitter, info.typ, variant.tag, toOstyValue(payload), site)
		g.takeOstyEmitter(emitter)
		g.needsGCRuntime = true
		enumValue := fromOstyValue(out)
		enumValue.rootPaths = g.rootPathsForType(info.typ)
		return enumValue, nil
	}
	slotType := func(i int) string {
		if i < len(info.payloadSlotTypes) {
			return info.payloadSlotTypes[i]
		}
		if info.payloadTyp != "" {
			return info.payloadTyp
		}
		return "i64"
	}
	for i, p := range payloads {
		want := slotType(i)
		if p.typ != want {
			return value{}, unsupportedf("type-system", "enum %q variant %q payload slot %d type %s, want %s", info.name, variant.name, i, p.typ, want)
		}
	}
	slotCount := info.payloadCount
	if slotCount < 1 {
		slotCount = 1
	}
	fields := make([]*LlvmValue, 0, 1+slotCount)
	fields = append(fields, llvmEnumVariant(info.typ, variant.tag))
	for i := 0; i < slotCount; i++ {
		if i < len(payloads) {
			fields = append(fields, toOstyValue(payloads[i]))
			continue
		}
		t := slotType(i)
		fields = append(fields, &LlvmValue{typ: t, name: llvmZeroLiteral(t), pointer: false})
	}
	emitter := g.toOstyEmitter()
	out := llvmStructLiteral(emitter, info.typ, fields)
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
	// `??` lowers to a branch+phi rather than the usual eager binary
	// shape — the right side is lazy per spec §7.3 (Option fallback).
	// Intercept before emitExpr so the default is never evaluated when
	// the left is Some.
	if e.Op == token.QQ {
		return g.emitCoalesce(e)
	}
	left, err := g.emitExpr(e.Left)
	if err != nil {
		return value{}, err
	}
	right, err := g.emitExpr(e.Right)
	if err != nil {
		return value{}, err
	}
	isString := g.staticExprIsString(e.Left) || g.staticExprIsString(e.Right)
	return g.emitBinaryOpValues(e.Op, left, right, isString)
}

// emitBinaryOpValues applies a binary operator to already-evaluated operands.
// Shared with compound-assignment paths that can't safely re-evaluate the
// left expression (notably index targets like `xs[i] += v`, where re-reading
// would double-evaluate `i`). `isString` mirrors the flag emitBinary passes
// to emitCompare: true when either operand is known to be a String.
func (g *generator) emitBinaryOpValues(opTok token.Kind, left, right value, isString bool) (value, error) {
	if llvmIsCompareOp(opTok.String()) {
		return g.emitCompare(opTok, left, right, isString)
	}
	if opTok == token.AND || opTok == token.OR {
		return g.emitLogical(opTok, left, right)
	}
	if left.typ == "double" && right.typ == "double" {
		op := llvmFloatBinaryInstruction(opTok.String())
		if op == "" {
			return value{}, unsupportedf("expression", "binary operator %q", opTok)
		}
		emitter := g.toOstyEmitter()
		out := llvmBinaryF64(emitter, op, toOstyValue(left), toOstyValue(right))
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), nil
	}
	if left.typ == "ptr" && right.typ == "ptr" && opTok == token.PLUS {
		return g.emitRuntimeStringConcat(left, right)
	}
	if left.typ != "i64" || right.typ != "i64" {
		return value{}, unsupportedf("type-system", "binary operator %q on %s/%s", opTok, left.typ, right.typ)
	}
	op := llvmIntBinaryInstruction(opTok.String())
	if op == "" {
		return value{}, unsupportedf("expression", "binary operator %q", opTok)
	}
	emitter := g.toOstyEmitter()
	out := llvmBinaryI64(emitter, op, toOstyValue(left), toOstyValue(right))
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

// emitCoalesce lowers `left ?? right`, the Option-fallback operator.
//
// Semantics (LANG_SPEC_v0.5 §7.3): `left` has type `T?`, `right` has
// type `T`. If `left` is Some(v), the result is v; if None, the
// result is `right`. The right side is evaluated lazily — only when
// the left side is None.
//
// Lowering mirrors the null-check/phi shape used by `?` and `?.`:
//
//	 %is_nil = icmp eq ptr %left, null
//	 br i1 %is_nil, label %none, label %some
//	some:
//	  ; unwrap: inner-ptr types use %left directly; others load the
//	  ; boxed payload via `load innerTyp, ptr %left`.
//	  br label %end
//	none:
//	  ; evaluate %right (may be any side-effecting expression).
//	  br label %end
//	end:
//	  %out = phi innerTyp [ %unwrapped, %some.pred ], [ %right, %none.pred ]
//
// Nil-coalesce is intentionally right-associative at the surface so
// `a ?? b ?? c` parses as `a ?? (b ?? c)`; here we only see one
// operator at a time, and the recursive emitExpr on `e.Right` handles
// any chained `??` naturally.
func (g *generator) emitCoalesce(e *ast.BinaryExpr) (value, error) {
	leftSrc, ok := g.staticExprSourceType(e.Left)
	if !ok {
		return value{}, unsupported("type-system", "?? left source type unknown")
	}
	innerSrc, ok := unwrapOptionalSourceType(leftSrc)
	if !ok {
		return value{}, unsupported("type-system", "?? requires Option<T> on the left")
	}
	innerTyp, err := llvmType(innerSrc, g.typeEnv())
	if err != nil {
		return value{}, err
	}
	left, err := g.emitExpr(e.Left)
	if err != nil {
		return value{}, err
	}
	if left.typ != "ptr" {
		return value{}, unsupportedf("type-system", "?? left type %s, want ptr", left.typ)
	}

	emitter := g.toOstyEmitter()
	isNil := llvmCompare(emitter, "eq", toOstyValue(left), toOstyValue(value{typ: "ptr", ref: "null"}))
	someLabel := llvmNextLabel(emitter, "coalesce.some")
	noneLabel := llvmNextLabel(emitter, "coalesce.none")
	endLabel := llvmNextLabel(emitter, "coalesce.end")
	emitter.body = append(emitter.body, mirBrCondText(isNil.name, noneLabel, someLabel))
	emitter.body = append(emitter.body, mirLabelText(someLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(someLabel)

	var leftUnwrapped value
	if innerTyp == "ptr" {
		leftUnwrapped = left
		leftUnwrapped.sourceType = innerSrc
	} else {
		lv, err := g.loadTypedPointerValue(left, innerTyp)
		if err != nil {
			return value{}, err
		}
		lv.sourceType = innerSrc
		leftUnwrapped = lv
	}
	somePred := g.currentBlock
	g.branchTo(endLabel)

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(noneLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(noneLabel)

	right, err := g.emitExpr(e.Right)
	if err != nil {
		return value{}, err
	}
	if right.typ != innerTyp {
		return value{}, unsupportedf("type-system", "?? right type %s, want %s", right.typ, innerTyp)
	}
	nonePred := g.currentBlock
	g.branchTo(endLabel)

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(endLabel))
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirPhiTypedTwoEdgeText(tmp, innerTyp, leftUnwrapped.ref, somePred, right.ref, nonePred))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)

	out := value{typ: innerTyp, ref: tmp, sourceType: innerSrc}
	out.rootPaths = g.rootPathsForType(innerTyp)
	if innerTyp == "ptr" {
		mergeContainerMetadata(&out, leftUnwrapped, right)
		out.gcManaged = leftUnwrapped.gcManaged || right.gcManaged
	}
	return out, nil
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
	case "i32", "i8":
		// Char (i32) and Byte (i8) compare with unsigned predicates so
		// Byte values 128..255 compare above the low range rather than
		// below (signed ordering would flip them to negative).
		pred := llvmUnsignedIntComparePredicate(op.String())
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
			// `==` / `!=` between two opaque managed pointers fall back
			// to identity comparison — the same `icmp eq/ne ptr` shape
			// LLVM uses for null checks. This is the only sensible
			// semantics for non-String ptr values until structural
			// equality is wired in. Ordering ops stay rejected; they
			// have no meaning for identities.
			opStr := op.String()
			if opStr == "==" || opStr == "!=" {
				pred := "eq"
				if opStr == "!=" {
					pred = "ne"
				}
				emitter := g.toOstyEmitter()
				out := llvmCompare(emitter, pred, toOstyValue(left), toOstyValue(right))
				g.takeOstyEmitter(emitter)
				return fromOstyValue(out), nil
			}
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
	inBench := g.benchClosureDepth > 0
	if !inBench && g.returnType != "ptr" {
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
	emitter.body = append(emitter.body, mirBrCondText(isNil.name, nilLabel, contLabel))
	emitter.body = append(emitter.body, mirLabelText(nilLabel))
	if inBench {
		g.emitTestingAbortWithEmitter(emitter, g.benchQuestionFailMessage(expr), contLabel)
	} else {
		g.releaseGCRoots(emitter)
		emitter.body = append(emitter.body, mirRetPtrNullText())
		emitter.body = append(emitter.body, mirLabelText(contLabel))
	}
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
	inBench := g.benchClosureDepth > 0
	var returnInfo builtinResultType
	if !inBench {
		ri, ok := g.resultTypes[g.returnType]
		if !ok {
			return value{}, unsupported("control-flow", "? on Result<T, E> requires the enclosing function to return Result<_, E>")
		}
		if ri.errTyp != info.errTyp {
			return value{}, unsupportedf("type-system", "? propagates err %s, function returns err %s", info.errTyp, ri.errTyp)
		}
		returnInfo = ri
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
	emitter.body = append(emitter.body, mirBrCondText(isErr.name, errLabel, okLabel))

	emitter.body = append(emitter.body, mirLabelText(errLabel))
	if inBench {
		g.emitTestingAbortWithEmitter(emitter, g.benchQuestionFailMessage(expr), okLabel)
	} else {
		// Err branch: repackage into the enclosing function's Result<T2, E>
		// struct and return immediately. GC roots are released just before
		// `ret` to mirror the bare-return path.
		errSlot := llvmExtractValue(emitter, toOstyValue(base), info.errTyp, 2)
		retFields := []*LlvmValue{
			toOstyValue(value{typ: "i64", ref: "1"}),
			toOstyValue(llvmZeroValue(returnInfo.okTyp)),
			errSlot,
		}
		retStruct := llvmStructLiteral(emitter, returnInfo.typ, retFields)
		g.releaseGCRoots(emitter)
		emitter.body = append(emitter.body, mirRetText(returnInfo.typ, retStruct.name))
		emitter.body = append(emitter.body, mirLabelText(okLabel))
	}
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
	symbol := llvmStringRuntimeConcatSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{
		{typ: "ptr"},
		{typ: "ptr"},
	})
	emitter := g.toOstyEmitter()
	out := llvmStringConcat(emitter, toOstyValue(left), toOstyValue(right))
	g.takeOstyEmitter(emitter)
	joined := fromOstyValue(out)
	joined.gcManaged = true
	joined.sourceType = &ast.NamedType{Path: []string{"String"}}
	return joined, nil
}

func (g *generator) emitRuntimeIntToString(v value) (value, error) {
	symbol := llvmIntRuntimeToStringSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "i64"}})
	emitter := g.toOstyEmitter()
	out := llvmIntRuntimeToString(emitter, toOstyValue(v))
	g.takeOstyEmitter(emitter)
	text := fromOstyValue(out)
	text.gcManaged = true
	text.sourceType = &ast.NamedType{Path: []string{"String"}}
	return text, nil
}

func (g *generator) emitRuntimeFloatToString(v value) (value, error) {
	symbol := llvmFloatRuntimeToStringSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "double"}})
	emitter := g.toOstyEmitter()
	out := llvmFloatRuntimeToString(emitter, toOstyValue(v))
	g.takeOstyEmitter(emitter)
	text := fromOstyValue(out)
	text.gcManaged = true
	text.sourceType = &ast.NamedType{Path: []string{"String"}}
	return text, nil
}

func (g *generator) emitRuntimeBoolToString(v value) (value, error) {
	symbol := llvmBoolRuntimeToStringSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "i1"}})
	emitter := g.toOstyEmitter()
	out := llvmBoolRuntimeToString(emitter, toOstyValue(v))
	g.takeOstyEmitter(emitter)
	text := fromOstyValue(out)
	text.gcManaged = true
	text.sourceType = &ast.NamedType{Path: []string{"String"}}
	return text, nil
}

// emitRuntimeCharToString materialises a single-char UTF-8 String from a
// Char (i32) codepoint via the osty_rt_char_to_string runtime helper.
// The helper handles all four UTF-8 width classes plus the out-of-range
// replacement-char fallback — see internal/backend/runtime/osty_runtime.c.
func (g *generator) emitRuntimeCharToString(v value) (value, error) {
	symbol := mirRtCharToStringSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "i32"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(v)})
	g.takeOstyEmitter(emitter)
	text := fromOstyValue(out)
	text.gcManaged = true
	return text, nil
}

// emitRuntimeByteToString materialises a single-byte String from a Byte
// (i8) via osty_rt_byte_to_string. Treats the byte as a raw octet; useful
// when iterating over text.bytes() and rebuilding the original bytes.
func (g *generator) emitRuntimeByteToString(v value) (value, error) {
	symbol := mirRtByteToStringSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "i8"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(v)})
	g.takeOstyEmitter(emitter)
	text := fromOstyValue(out)
	text.gcManaged = true
	return text, nil
}

func (g *generator) emitRuntimeStringCompare(op token.Kind, left, right value) (value, error) {
	switch op {
	case token.EQ, token.NEQ:
		g.declareRuntimeSymbol(mirRtStringSymbol("Equal"), "i1", []paramInfo{
			{typ: "ptr"},
			{typ: "ptr"},
		})
	case token.LT, token.LEQ, token.GT, token.GEQ:
		g.declareRuntimeSymbol(llvmStringRuntimeCompareSymbol(), "i64", []paramInfo{
			{typ: "ptr"},
			{typ: "ptr"},
		})
	default:
		return value{}, unsupportedf("expression", "comparison operator %q", op)
	}
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
	thenValue, elseValue = coerceVoidArm(thenValue, elseValue)
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
	thenValue, elseValue = coerceVoidArm(thenValue, elseValue)
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "if expression branch types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitIfExprPhi(labels *LlvmIfLabels, thenPred, elsePred string, thenValue, elseValue value) (value, error) {
	if labels == nil {
		return value{}, unsupported("control-flow", "missing if-expression labels")
	}
	// Unreachable-arm coercion: when one branch ended in an abort
	// sequence (`__resultAbort`, testing-abort, unwrap-on-None), the
	// emitter leaves that predecessor with `unreachable` and reports
	// `typ == "void"`. The phi needs two entries of the same type
	// regardless — LLVM accepts `undef <ty>` from the unreachable
	// edge, so we project the void side onto the reachable side's
	// type rather than walling on the mismatch.
	thenValue, elseValue = coerceVoidArm(thenValue, elseValue)
	thenValue, elseValue = coerceVoidArm(thenValue, elseValue)
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "if expression branch types %s/%s", thenValue.typ, elseValue.typ)
	}
	emitter := g.toOstyEmitter()
	emitter.body = append(emitter.body, mirBrUncondText(labels.endLabel))
	emitter.body = append(emitter.body, mirLabelText(labels.endLabel))
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirPhiTypedTwoEdgeText(tmp, thenValue.typ, thenValue.ref, thenPred, elseValue.ref, elsePred))
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.endLabel
	out := value{typ: thenValue.typ, ref: tmp}
	mergeContainerMetadata(&out, thenValue, elseValue)
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, nil
}

// coerceVoidArm pairs up two match-arm values when one of them
// terminated in an unreachable abort (type = "void"). LLVM phi
// instructions need uniform operand types; for the unreachable edge
// the value is never actually used, so project it onto the reachable
// arm's type with an `undef` operand. Both-void still goes through
// unchanged so the surrounding check reports the real "no arm
// produced a value" failure.
func coerceVoidArm(a, b value) (value, value) {
	if a.typ == "void" && b.typ != "void" && b.typ != "" {
		a = value{typ: b.typ, ref: "undef"}
	} else if b.typ == "void" && a.typ != "void" && a.typ != "" {
		b = value{typ: a.typ, ref: "undef"}
	}
	return a, b
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
		if ret, ok := stmt.(*ast.ReturnStmt); ok {
			if err := g.emitReturn(ret); err != nil {
				return value{}, err
			}
			return value{typ: "void", ref: "undef"}, nil
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
	// Pre-scan arms for a typed List<T> source so a sibling bare `[]` arm
	// body inherits the element type instead of walling on LLVM013. Push
	// once here and every sub-emitter (tag / payload / result / optional /
	// guarded) inherits the hint through g.matchArmListHints.
	if hint, ok := g.inferMatchArmsListHint(expr.Arms); ok {
		g.pushMatchArmListHint(hint)
		defer g.popMatchArmListHint()
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
	if g.matchNeedsGenericConditionPath(scrutinee, expr.Arms) {
		return g.emitGuardedMatchExprValue(scrutinee, expr.Arms)
	}
	if isPrimitiveLiteralMatchScrutineeType(scrutinee.typ) && isPrimitiveLiteralMatchArms(expr.Arms) {
		return g.emitPrimitiveLiteralMatchExprValue(scrutinee, expr.Arms)
	}
	if scrutinee.typ == "i64" {
		// `match n { 0 -> ..., 1 -> ..., _ -> ... }` on a plain Int
		// scrutinee falls into the same i64 dispatch as a payload-free
		// enum tag match — but the patterns are LiteralPat, not enum
		// variant idents, so emitTagEnumMatchExprValue's matchEnumTag
		// returns false on every arm. Detect the literal-pattern shape
		// up front and route to the dedicated primitive lowering.
		return g.emitTagEnumMatchExprValue(scrutinee, expr.Arms)
	}
	if info := g.enumsByType[scrutinee.typ]; info != nil && info.hasPayload {
		return g.emitPayloadEnumMatchExprValue(scrutinee, info, expr.Arms)
	}
	if info, ok := g.resultTypes[scrutinee.typ]; ok {
		return g.emitResultMatchExprValue(scrutinee, info, expr.Arms)
	}
	// Optional match-as-expression: `match opt { Some(x) -> a, None -> b }`
	// where opt has source type T?. The scrutinee LLVM type is "ptr"
	// (boxed Option ABI: null = None, non-null = Some(x)), which the
	// generic enum-tag fallback can't distinguish from a raw pointer.
	// Mirrors the optional-source-type detection used by emitMatchStmt.
	if scrutinee.typ == "ptr" {
		if sourceType, ok := g.staticExprSourceType(expr.Scrutinee); ok {
			resolved, resolveErr := llvmResolveAliasType(sourceType, g.typeEnv(), map[string]bool{})
			if resolveErr == nil {
				if opt, ok := resolved.(*ast.OptionalType); ok {
					return g.emitOptionalMatchExprValue(scrutinee, opt.Inner, expr.Arms)
				}
				// `match c.x { Ok(v) -> …, Err(_) -> … }` where
				// `c.x: Result<T, E>` is stored in a struct field or
				// loaded indirectly — the scrutinee LLVM type is a
				// ptr into that slot, not the aggregate value itself.
				// Load the Result aggregate through the ptr and
				// re-enter the dispatch so
				// emitResultMatchExprValue can consume it.
				if info, ok := builtinResultTypeFromAST(resolved, g.typeEnv()); ok {
					emitter := g.toOstyEmitter()
					loaded := llvmLoad(emitter, &LlvmValue{typ: info.typ, name: scrutinee.ref, pointer: true})
					g.takeOstyEmitter(emitter)
					aggregate := value{typ: info.typ, ref: loaded.name}
					return g.emitResultMatchExprValue(aggregate, info, expr.Arms)
				}
			}
		}
	}
	return value{}, unsupportedf("type-system", "match scrutinee type %s, want enum tag", scrutinee.typ)
}

// emitOptionalMatchExprValue lowers `match opt { Some(x) -> a, None -> b }`
// in value position. Mirrors emitOptionalMatchStmt's branch shape but
// uses emitIfExprPhi to merge the two arm values, so the match
// participates as an expression in let / fn-return / nested call
// position.
//
// Constraints (deliberately tight, matching the statement path):
//   - exactly two productive arms covering Some + None (a wildcard
//     fills in for either)
//   - no guards (those still wall — same as the statement path)
//   - both arms produce the same LLVM type (emitIfExprPhi enforces)
//
// The Some payload is bound via bindOptionalMatchPayload, which
// already handles ptr / scalar / aggregate via loadValueFromAddress,
// so this routine doesn't touch the boxing/unboxing surface.
func (g *generator) emitOptionalMatchExprValue(scrutinee value, innerSource ast.Type, arms []*ast.MatchArm) (value, error) {
	if len(arms) == 0 {
		return value{}, unsupported("expression", "optional match has no arms")
	}
	type optionalArmExpr struct {
		pat  optionalMatchPatternInfo
		body ast.Expr
	}
	parsed := make([]optionalArmExpr, 0, len(arms))
	for _, arm := range arms {
		if arm == nil {
			return value{}, unsupported("expression", "nil match arm")
		}
		if arm.Guard != nil {
			return value{}, unsupported("expression", "guarded optional match arms not yet supported in expression position")
		}
		pat, ok, err := matchOptionalPattern(arm.Pattern)
		if err != nil {
			return value{}, err
		}
		if !ok {
			return value{}, unsupportedf("expression", "optional match arm must be Some/None/wildcard, got %T", arm.Pattern)
		}
		parsed = append(parsed, optionalArmExpr{pat: pat, body: arm.Body})
	}
	// Resolve which parsed arm covers Some and which covers None, with
	// a wildcard filling in either slot. The first matching arm wins
	// (mirrors source-order arm preference).
	var someArm, noneArm, wildcardArm *optionalArmExpr
	for i := range parsed {
		switch {
		case parsed[i].pat.isSome && someArm == nil:
			someArm = &parsed[i]
		case parsed[i].pat.isNone && noneArm == nil:
			noneArm = &parsed[i]
		case parsed[i].pat.isWildcard && wildcardArm == nil:
			wildcardArm = &parsed[i]
		}
	}
	if someArm == nil {
		someArm = wildcardArm
	}
	if noneArm == nil {
		noneArm = wildcardArm
	}
	if someArm == nil || noneArm == nil {
		return value{}, unsupported("expression", "optional match must cover both Some and None (a wildcard counts for the missing side)")
	}

	emitter := g.toOstyEmitter()
	isNil := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpEqPtrNullText(isNil, scrutinee.ref))
	// `cond=true` selects the then-label per llvmIfExprStart; isNil=true
	// → the None arm runs in `then`, the Some arm in `else`.
	labels := llvmIfExprStart(emitter, &LlvmValue{typ: "i1", name: isNil})
	g.takeOstyEmitter(emitter)

	g.currentBlock = labels.thenLabel
	g.pushScope()
	noneValue, err := g.emitMatchArmBodyValue(noneArm.body)
	g.popScope()
	if err != nil {
		return value{}, err
	}
	nonePred := g.currentBlock

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel
	g.pushScope()
	if someArm.pat.isSome {
		if err := g.bindOptionalMatchPayload(scrutinee, innerSource, someArm.pat); err != nil {
			g.popScope()
			return value{}, err
		}
	}
	someValue, err := g.emitMatchArmBodyValue(someArm.body)
	g.popScope()
	if err != nil {
		return value{}, err
	}
	somePred := g.currentBlock

	return g.emitIfExprPhi(labels, nonePred, somePred, noneValue, someValue)
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

func resultVariantName(tag int) string {
	return mirResultVariantName(tag)
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

	thenValue, elseValue = coerceVoidArm(thenValue, elseValue)
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
		if err := g.decorateValueFromSourceType(&payloadValue, builtinResultPayloadSourceType(scrutinee.sourceType, resultVariantName(info.tag))); err != nil {
			return value{}, err
		}
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

// isPrimitiveLiteralMatchArms reports whether every arm of a match
// expression is either a literal pattern (`0`, `true`, `'a'`) or a
// wildcard, i.e. the shape `match n { 0 -> ..., 1 -> ..., _ -> ... }`.
// At least one arm must be a literal — bare `match _ { _ -> body }`
// is not what this routes to.
func isPrimitiveLiteralMatchArms(arms []*ast.MatchArm) bool {
	sawLiteral := false
	for _, arm := range arms {
		if arm == nil {
			return false
		}
		switch arm.Pattern.(type) {
		case *ast.WildcardPat:
		case *ast.LiteralPat:
			sawLiteral = true
		default:
			return false
		}
	}
	return sawLiteral
}

// isPrimitiveLiteralMatchScrutineeType reports whether a match
// scrutinee LLVM type can use the literal-dispatch fast path.
// Delegates to the Osty-sourced `mirIsPrimitiveLiteralMatchScrutineeType`
// (`toolchain/mir_generator.osty`).
func isPrimitiveLiteralMatchScrutineeType(typ string) bool {
	return mirIsPrimitiveLiteralMatchScrutineeType(typ)
}

// emitPrimitiveLiteralMatchExprValue lowers `match n { 0 -> A, 1 -> B,
// _ -> C }` for a primitive scalar scrutinee (the i64 dispatch slot).
// Mirrors emitTagEnumMatchExprValue for shape: select-safe arms
// collapse to a chain of `select` instructions, the rest fall back to
// nested if-expr phi.
func (g *generator) emitPrimitiveLiteralMatchExprValue(scrutinee value, arms []*ast.MatchArm) (value, error) {
	selectSafe := true
	for _, arm := range arms {
		if !matchArmBodyIsSelectSafe(arm.Body) {
			selectSafe = false
			break
		}
	}
	if selectSafe {
		return g.emitPrimitiveLiteralMatchSelectValue(scrutinee, arms)
	}
	return g.emitPrimitiveLiteralMatchChainValue(scrutinee, arms)
}

// emitPrimitiveLiteralMatchSelectValue builds the select chain back to
// front: each non-wildcard arm becomes
// `select (icmp eq scrutinee, lit), armValue, current`. Wildcard, when
// present, must be last (matches the corresponding rule in
// emitTagEnumMatchSelectValue) and seeds `current`.
func (g *generator) emitPrimitiveLiteralMatchSelectValue(scrutinee value, arms []*ast.MatchArm) (value, error) {
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
		litPat, ok := arm.Pattern.(*ast.LiteralPat)
		if !ok || litPat.Literal == nil {
			return value{}, unsupportedf("expression", "primitive literal match arm must be a literal pattern (got %T)", arm.Pattern)
		}
		litValue, err := g.emitExpr(litPat.Literal)
		if err != nil {
			return value{}, err
		}
		if litValue.typ != scrutinee.typ {
			return value{}, unsupportedf("type-system", "match literal type %s does not match scrutinee %s", litValue.typ, scrutinee.typ)
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
		armValue, current = coerceVoidArm(armValue, current)
		if armValue.typ != current.typ {
			return value{}, unsupportedf("type-system", "match arm types %s/%s", armValue.typ, current.typ)
		}
		emitter := g.toOstyEmitter()
		cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(litValue))
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

// emitPrimitiveLiteralMatchChainValue builds nested if-expr phi for
// non-select-safe arm bodies (block bodies with multiple statements,
// calls with side effects, etc.). Mirrors emitTagEnumMatchChainValue.
func (g *generator) emitPrimitiveLiteralMatchChainValue(scrutinee value, arms []*ast.MatchArm) (value, error) {
	if len(arms) == 0 {
		return value{}, unsupported("expression", "match with no arms")
	}
	arm := arms[0]
	if arm == nil {
		return value{}, unsupported("expression", "nil match arm")
	}
	if len(arms) == 1 {
		if _, catchAll := arm.Pattern.(*ast.WildcardPat); catchAll {
			return g.emitMatchArmBodyValue(arm.Body)
		}
		return value{}, unsupported("expression", "primitive literal match must end with a wildcard arm")
	}
	if _, catchAll := arm.Pattern.(*ast.WildcardPat); catchAll {
		return value{}, unsupported("expression", "wildcard match arm must be last")
	}
	litPat, ok := arm.Pattern.(*ast.LiteralPat)
	if !ok || litPat.Literal == nil {
		return value{}, unsupportedf("expression", "primitive literal match arm must be a literal pattern (got %T)", arm.Pattern)
	}
	litValue, err := g.emitExpr(litPat.Literal)
	if err != nil {
		return value{}, err
	}
	if litValue.typ != scrutinee.typ {
		return value{}, unsupportedf("type-system", "match literal type %s does not match scrutinee %s", litValue.typ, scrutinee.typ)
	}
	emitter := g.toOstyEmitter()
	cond := llvmCompare(emitter, "eq", toOstyValue(scrutinee), toOstyValue(litValue))
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

	elseValue, err := g.emitPrimitiveLiteralMatchChainValue(scrutinee, arms[1:])
	if err != nil {
		return value{}, err
	}
	elsePred := g.currentBlock
	thenValue, elseValue = coerceVoidArm(thenValue, elseValue)
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
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
			return value{}, unsupportedf("expression", "match arm must be a payload-free enum variant (got %s)", debugPatternShape(arm.Pattern))
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
		armValue, current = coerceVoidArm(armValue, current)
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
				return value{}, unsupportedf("expression", "match arm must be a payload-free enum variant (got %s)", debugPatternShape(arm.Pattern))
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
		return value{}, unsupportedf("expression", "match arm must be a payload-free enum variant (got %s)", debugPatternShape(arm.Pattern))
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
	thenValue, elseValue = coerceVoidArm(thenValue, elseValue)
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
	thenValue, elseValue = coerceVoidArm(thenValue, elseValue)
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
	thenValue, elseValue = coerceVoidArm(thenValue, elseValue)
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
		return value{}, unsupportedf("expression", "first match arm must be a payload-free enum variant (got %s)", debugPatternShape(first.Pattern))
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
	thenValue, elseValue = coerceVoidArm(thenValue, elseValue)
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) emitSelectValue(cond *LlvmValue, thenValue, elseValue value) (value, error) {
	if cond == nil || cond.typ != "i1" {
		return value{}, unsupported("type-system", "select condition must be Bool")
	}
	thenValue, elseValue = coerceVoidArm(thenValue, elseValue)
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "select branch types %s/%s", thenValue.typ, elseValue.typ)
	}
	emitter := g.toOstyEmitter()
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSelectTypedText(tmp, thenValue.typ, cond.name, thenValue.ref, elseValue.ref))
	g.takeOstyEmitter(emitter)
	out := value{typ: thenValue.typ, ref: tmp}
	mergeContainerMetadata(&out, thenValue, elseValue)
	out.rootPaths = g.rootPathsForType(out.typ)
	return out, nil
}

func (g *generator) emitExprWithHint(expr ast.Expr, listElemTyp string, listElemString bool, mapKeyTyp string, mapValueTyp string, mapKeyString bool, setElemTyp string, setElemString bool) (value, error) {
	if list, ok := expr.(*ast.ListExpr); ok {
		return g.emitListExprWithHint(list, nil, listElemTyp, listElemString)
	}
	if m, ok := expr.(*ast.MapExpr); ok {
		return g.emitMapExprWithHint(m, nil, nil, mapKeyTyp, mapValueTyp, mapKeyString)
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

func bytesToStringResultSourceType() ast.Type {
	return &ast.NamedType{
		Path: []string{"Result"},
		Args: []ast.Type{
			&ast.NamedType{Path: []string{"String"}},
			&ast.NamedType{Path: []string{"Error"}},
		},
	}
}

func stringToIntResultSourceType() ast.Type {
	return &ast.NamedType{
		Path: []string{"Result"},
		Args: []ast.Type{
			&ast.NamedType{Path: []string{"Int"}},
			&ast.NamedType{Path: []string{"Error"}},
		},
	}
}

func stringToFloatResultSourceType() ast.Type {
	return &ast.NamedType{
		Path: []string{"Result"},
		Args: []ast.Type{
			&ast.NamedType{Path: []string{"Float"}},
			&ast.NamedType{Path: []string{"Error"}},
		},
	}
}

func bytesListSourceType() ast.Type {
	return &ast.NamedType{
		Path: []string{"List"},
		Args: []ast.Type{
			&ast.NamedType{Path: []string{"Bytes"}},
		},
	}
}

func bytesFromHexResultSourceType() ast.Type {
	return &ast.NamedType{
		Path: []string{"Result"},
		Args: []ast.Type{
			&ast.NamedType{Path: []string{"Bytes"}},
			&ast.NamedType{Path: []string{"Error"}},
		},
	}
}

func (g *generator) emitExprWithHintAndSourceType(expr ast.Expr, sourceType ast.Type, listElemTyp string, listElemString bool, mapKeyTyp string, mapValueTyp string, mapKeyString bool, setElemTyp string, setElemString bool) (value, error) {
	if sourceType != nil {
		if elemTyp, elemString, ok, err := llvmListElementInfo(sourceType, g.typeEnv()); err != nil {
			return value{}, err
		} else if ok {
			if listElemTyp == "" {
				listElemTyp = elemTyp
				listElemString = elemString
			} else if listElemTyp == elemTyp && !listElemString {
				// Caller knew the elem IR type but not the String flag.
				// Pull the flag from the richer sourceType so list-literal
				// lowering doesn't trip `list_mixed_ptr` on List<String>
				// return shapes where the caller only wired retListElemTyp.
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
		} else if keyTyp, valueTyp, keyString, ok, err := llvmMapTypes(sourceType, g.typeEnv()); err == nil && ok {
			if keyTyp == mapKeyTyp && valueTyp == mapValueTyp && !mapKeyString {
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
		} else if elemTyp, elemString, ok, err := llvmSetElementType(sourceType, g.typeEnv()); err == nil && ok {
			if elemTyp == setElemTyp && !setElemString {
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
	if inner, ok := unwrapOptionalSourceType(sourceType); ok {
		g.optionContexts = append(g.optionContexts, builtinOptionContext{
			inner:      inner,
			sourceType: sourceType,
		})
		defer func() {
			g.optionContexts = g.optionContexts[:len(g.optionContexts)-1]
		}()
	}
	var (
		v   value
		err error
	)
	if list, ok := expr.(*ast.ListExpr); ok {
		elemSource, _ := llvmListElementSourceType(sourceType, g.typeEnv())
		v, err = g.emitListExprWithHint(list, elemSource, listElemTyp, listElemString)
	} else if m, ok := expr.(*ast.MapExpr); ok {
		keySource, valueSource, _ := llvmMapSourceTypes(sourceType, g.typeEnv())
		v, err = g.emitMapExprWithHint(m, keySource, valueSource, mapKeyTyp, mapValueTyp, mapKeyString)
	} else {
		v, err = g.emitExprWithHint(expr, listElemTyp, listElemString, mapKeyTyp, mapValueTyp, mapKeyString, setElemTyp, setElemString)
	}
	if err != nil {
		return value{}, err
	}
	decorateWith := sourceType
	if decorateWith == nil && v.sourceType == nil {
		if staticSource, ok := g.staticExprSourceType(expr); ok {
			decorateWith = staticSource
		}
	}
	if v.sourceType == nil && decorateWith != nil {
		if typ, err := llvmType(decorateWith, g.typeEnv()); err == nil && typ == v.typ {
			if err := g.decorateValueFromSourceType(&v, decorateWith); err != nil {
				return value{}, err
			}
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
	emitter.body = append(emitter.body, mirGEPSizeofText(sizePtr, typ))
	size := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirPtrToIntI64Text(size, sizePtr))
	return value{typ: "i64", ref: size}
}

func (g *generator) emitAggregateScratchSlot(emitter *LlvmEmitter, typ, initial string) value {
	slot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAllocaText(slot, typ))
	emitter.body = append(emitter.body, mirStoreText(typ, initial, slot))
	return value{typ: typ, ref: slot, ptr: true}
}

func (g *generator) emitAggregateRootOffsets(emitter *LlvmEmitter, typ string) (value, int, error) {
	paths := g.rootPathsForType(typ)
	if len(paths) == 0 {
		return value{typ: "ptr", ref: "null"}, 0, nil
	}
	arrayTyp := mirArrayI64TypeText(strconv.Itoa(len(paths)))
	arrayPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAllocaText(arrayPtr, arrayTyp))
	for i, path := range paths {
		offsetPtr := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirGEPInboundsNullRawIndicesText(offsetPtr, typ, llvmAggregatePathIndices(path)))
		offsetValue := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirPtrToIntI64Text(offsetValue, offsetPtr))
		slotPtr := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirGEPInboundsFieldText(slotPtr, arrayTyp, arrayPtr, strconv.Itoa(i)))
		emitter.body = append(emitter.body, mirStoreI64Text(offsetValue, slotPtr))
	}
	firstPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirGEPInboundsZeroText(firstPtr, arrayTyp, arrayPtr))
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
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			listRuntimePushBytesV1Symbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(listValue), toOstyValue(value{typ: "ptr", ref: slot.ref}), toOstyValue(size)}),
		))
	} else {
		g.declareRuntimeSymbol(listRuntimePushBytesRootsSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}})
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
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

// emitListAggregateInsert mirrors emitListAggregatePush but for an
// arbitrary index — the underlying runtime memmoves the tail right by
// one slot and copies the value bytes into slot `index`. Pointer-bearing
// struct fields go through the *_roots variant so GC sees the new edges.
func (g *generator) emitListAggregateInsert(listValue, idx, elem value) error {
	emitter := g.toOstyEmitter()
	slot := g.emitAggregateScratchSlot(emitter, elem.typ, elem.ref)
	size := g.emitAggregateByteSize(emitter, elem.typ)
	offsetsPtr, offsetCount, err := g.emitAggregateRootOffsets(emitter, elem.typ)
	if err != nil {
		return err
	}
	if offsetCount == 0 {
		g.declareRuntimeSymbol(listRuntimeInsertBytesV1Symbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}})
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			listRuntimeInsertBytesV1Symbol(),
			llvmCallArgs([]*LlvmValue{toOstyValue(listValue), toOstyValue(idx), toOstyValue(value{typ: "ptr", ref: slot.ref}), toOstyValue(size)}),
		))
	} else {
		g.declareRuntimeSymbol(listRuntimeInsertBytesRootsSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}, {typ: "i64"}})
		emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
			listRuntimeInsertBytesRootsSymbol(),
			llvmCallArgs([]*LlvmValue{
				toOstyValue(listValue),
				toOstyValue(idx),
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
	emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
		listRuntimeGetBytesV1Symbol(),
		llvmCallArgs([]*LlvmValue{toOstyValue(listValue), toOstyValue(index), toOstyValue(value{typ: "ptr", ref: slot.ref}), toOstyValue(size)}),
	))
	out := llvmLoad(emitter, toOstyValue(slot))
	g.takeOstyEmitter(emitter)
	loaded := fromOstyValue(out)
	loaded.rootPaths = g.rootPathsForType(elemTyp)
	return loaded, nil
}

func (g *generator) emitListExprWithHint(expr *ast.ListExpr, elemSource ast.Type, hintedElemTyp string, hintedElemString bool) (value, error) {
	if expr == nil {
		return value{}, unsupported("expression", "nil list literal")
	}
	g.pushScope()
	defer g.popScope()
	elemTyp := hintedElemTyp
	elemString := hintedElemString
	emittedElems := make([]value, 0, len(expr.Elems))
	for i, elem := range expr.Elems {
		var v value
		var err error
		if elemSource != nil {
			v, err = g.emitExprWithHintAndSourceType(elem, elemSource, "", false, "", "", false, "", false)
		} else {
			v, err = g.emitExpr(elem)
		}
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
		return value{}, unsupportedf("expression", "empty list literal %s requires an explicit List<T> type", exprPosLabel(expr))
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
			emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
				pushSymbol,
				llvmCallArgs([]*LlvmValue{toOstyValue(listValue), toOstyValue(loaded)}),
			))
		} else {
			addr := g.spillValueAddress(emitter, "list.elem", loaded)
			sizeValue := g.emitTypeSize(emitter, elemTyp)
			g.declareRuntimeSymbol(listRuntimePushBytesSymbol(), "void", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "i64"}, {typ: "ptr"}})
			emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
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

func (g *generator) emitMapExprWithHint(expr *ast.MapExpr, keySource, valueSource ast.Type, hintedKeyTyp, hintedValueTyp string, hintedKeyString bool) (value, error) {
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
		var key value
		var err error
		if keySource != nil {
			key, err = g.emitExprWithHintAndSourceType(entry.Key, keySource, "", false, "", "", false, "", false)
		} else {
			key, err = g.emitExpr(entry.Key)
		}
		if err != nil {
			return value{}, err
		}
		var val value
		if valueSource != nil {
			val, err = g.emitExprWithHintAndSourceType(entry.Value, valueSource, "", false, "", "", false, "", false)
		} else {
			val, err = g.emitExpr(entry.Value)
		}
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
		return value{}, unsupportedf("expression", "empty map literal %s requires an explicit Map<K, V> type", exprPosLabel(expr))
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
	emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
		insertSymbol,
		llvmCallArgs([]*LlvmValue{toOstyValue(base), toOstyValue(keyLoaded), {typ: "ptr", name: valAddr}}),
	))
	g.takeOstyEmitter(emitter)
	return nil
}

// emitCharByteConversionCall lowers the zero-arg width-only conversions
// between Char (i32), Byte (i8), and Int (i64) that the stdlib
// `primitives/{char,int}.osty` expose as `#[intrinsic_methods]`:
//
//   - `ch.toInt()`  on Char → zext i32 → i64
//   - `b.toInt()`   on Byte → zext i8  → i64
//   - `n.toChar()`  on Int  → trunc i64 → i32
//
// Other width-changing integer conversions (toInt8, toByte returning
// Result, etc.) still fall through to the unsupported-call path.
func (g *generator) emitCharByteConversionCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok || field.IsOptional {
		return value{}, false, nil
	}
	if len(call.Args) != 0 {
		return value{}, false, nil
	}
	baseInfo, ok := g.staticExprInfo(field.X)
	if !ok {
		return value{}, false, nil
	}
	switch {
	case field.Name == "toInt" && (baseInfo.typ == "i32" || baseInfo.typ == "i8"):
	case field.Name == "toChar" && baseInfo.typ == "i64":
	case field.Name == "toChar" && baseInfo.typ == "i8":
	case field.Name == "toByte" && baseInfo.typ == "i64":
	default:
		return value{}, false, nil
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	emitter := g.toOstyEmitter()
	tmp := llvmNextTemp(emitter)
	switch {
	case field.Name == "toInt" && base.typ == "i32":
		emitter.body = append(emitter.body, mirZExtI32ToI64Text(tmp, base.ref))
		g.takeOstyEmitter(emitter)
		return value{typ: "i64", ref: tmp}, true, nil
	case field.Name == "toInt" && base.typ == "i8":
		emitter.body = append(emitter.body, mirZExtI8ToI64Text(tmp, base.ref))
		g.takeOstyEmitter(emitter)
		return value{typ: "i64", ref: tmp}, true, nil
	case field.Name == "toChar" && base.typ == "i64":
		emitter.body = append(emitter.body, mirTruncI64ToI32Text(tmp, base.ref))
		g.takeOstyEmitter(emitter)
		return value{typ: "i32", ref: tmp}, true, nil
	case field.Name == "toByte" && base.typ == "i64":
		// Spec §2.2 narrows Int→Byte as `Result<Byte, Error>`, but the
		// toolchain uses it as an infallible truncation — the self-host
		// emitter (`toolchain/llvmgen.osty:532`) relies on this shape.
		// Lower as plain `trunc i64 to i8` so the comparisons that
		// follow (`b == '\\'.toInt().toByte()`) type-check against the
		// Byte receiver without an extra `.unwrap()` layer.
		emitter.body = append(emitter.body, mirTruncI64ToI8Text(tmp, base.ref))
		g.takeOstyEmitter(emitter)
		return value{typ: "i8", ref: tmp}, true, nil
	case field.Name == "toChar" && base.typ == "i8":
		// Byte → Char widens a u8 to a Char code point via zero extend.
		// `b.toChar().toString()` in the llvmgen C-string escape loop
		// materialises a one-byte UTF-8 string for printable ASCII.
		emitter.body = append(emitter.body, mirZExtI8ToI32Text(tmp, base.ref))
		g.takeOstyEmitter(emitter)
		return value{typ: "i32", ref: tmp}, true, nil
	}
	g.takeOstyEmitter(emitter)
	return value{}, false, nil
}

// emitCharPredicateCall lowers the Char ASCII predicates and case
// conversion methods declared in primitives/char.osty as intrinsic
// placeholder bodies (`{ false }` / `{ '\0' }`). The injected stdlib
// bodies that bring these into play (e.g. `strings.toUpper(s)` walks
// each char with `c.toUpper()`) trip LLVM015 without a backend
// dispatcher because the placeholder body has no callable shape.
//
// Scope: ASCII fast path. Non-ASCII codepoints pass through
// unchanged from the case-conversion methods and report `false` from
// every is-predicate. Unicode case folding tables are a follow-up
// (RFC §2.1 leaves the wider behavior open). The methods covered
// here are the ones the stdlib bodies actually call:
//
//	c.isDigit() / c.isAlpha() / c.isAlphanumeric()
//	c.isWhitespace() / c.isUpper() / c.isLower()
//	c.toUpper() / c.toLower()
//
// All operate on the Char's i32 codepoint scalar; `c.toInt()` and
// `c.toString()` go through emitCharByteConversionCall and
// emitPrimitiveToStringCall respectively.
func (g *generator) emitCharPredicateCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok || field.IsOptional {
		return value{}, false, nil
	}
	if len(call.Args) != 0 {
		return value{}, false, nil
	}
	switch field.Name {
	case "isDigit", "isAlpha", "isAlphanumeric", "isWhitespace", "isUpper", "isLower", "toUpper", "toLower":
	default:
		return value{}, false, nil
	}
	baseInfo, ok := g.staticExprInfo(field.X)
	if !ok || baseInfo.typ != "i32" {
		return value{}, false, nil
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	if base.typ != "i32" {
		return value{}, true, unsupportedf("type-system", "Char.%s receiver type %s, want i32", field.Name, base.typ)
	}
	emitter := g.toOstyEmitter()
	defer g.takeOstyEmitter(emitter)
	switch field.Name {
	case "isDigit":
		// `c - '0' < 10` as unsigned comparison handles ASCII digits in
		// one branch-free check.
		out := llvmCharAsciiRangeCheck(emitter, base.ref, '0', 10)
		return value{typ: "i1", ref: out}, true, nil
	case "isUpper":
		out := llvmCharAsciiRangeCheck(emitter, base.ref, 'A', 26)
		return value{typ: "i1", ref: out}, true, nil
	case "isLower":
		out := llvmCharAsciiRangeCheck(emitter, base.ref, 'a', 26)
		return value{typ: "i1", ref: out}, true, nil
	case "isAlpha":
		// isUpper || isLower
		up := llvmCharAsciiRangeCheck(emitter, base.ref, 'A', 26)
		lo := llvmCharAsciiRangeCheck(emitter, base.ref, 'a', 26)
		out := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirOrI1Text(out, up, lo))
		return value{typ: "i1", ref: out}, true, nil
	case "isAlphanumeric":
		// isDigit || isUpper || isLower
		dg := llvmCharAsciiRangeCheck(emitter, base.ref, '0', 10)
		up := llvmCharAsciiRangeCheck(emitter, base.ref, 'A', 26)
		lo := llvmCharAsciiRangeCheck(emitter, base.ref, 'a', 26)
		al := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirOrI1Text(al, up, lo))
		out := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirOrI1Text(out, dg, al))
		return value{typ: "i1", ref: out}, true, nil
	case "isWhitespace":
		// Spec-relevant ASCII whitespace: SPACE / TAB / LF / CR.
		// (FF / VT are intentionally excluded — they don't appear in
		// any current stdlib body and matching libc isspace() too
		// closely is a Unicode follow-up concern.)
		eqSp := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirICmpEqI32LiteralText(eqSp, base.ref, "32"))
		eqTab := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirICmpEqI32LiteralText(eqTab, base.ref, "9"))
		eqLf := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirICmpEqI32LiteralText(eqLf, base.ref, "10"))
		eqCr := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirICmpEqI32LiteralText(eqCr, base.ref, "13"))
		o1 := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirOrI1Text(o1, eqSp, eqTab))
		o2 := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirOrI1Text(o2, eqLf, eqCr))
		out := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirOrI1Text(out, o1, o2))
		return value{typ: "i1", ref: out}, true, nil
	case "toLower":
		// if c is ASCII upper, c + 32; else c. select form keeps it
		// branch-free (downstream phi avoidance).
		isUp := llvmCharAsciiRangeCheck(emitter, base.ref, 'A', 26)
		shifted := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirAddI32LiteralText(shifted, base.ref, "32"))
		out := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirSelectI32Text(out, isUp, shifted, base.ref))
		return value{typ: "i32", ref: out}, true, nil
	case "toUpper":
		isLo := llvmCharAsciiRangeCheck(emitter, base.ref, 'a', 26)
		shifted := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirSubI32LiteralText(shifted, base.ref, "32"))
		out := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirSelectI32Text(out, isLo, shifted, base.ref))
		return value{typ: "i32", ref: out}, true, nil
	}
	return value{}, false, nil
}

// llvmCharAsciiRangeCheck emits `(c - low) <u count` — branch-free
// half-open range check `low <= c < low+count` via a single unsigned
// compare against the count. Returns the i1 temp name.
func llvmCharAsciiRangeCheck(emitter *LlvmEmitter, charRef string, low int, count int) string {
	shifted := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSubI32LiteralText(shifted, charRef, strconv.Itoa(low)))
	cmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpULTI32LiteralText(cmp, shifted, strconv.Itoa(count)))
	return cmp
}

// stdlib primitives/{int,float,bool}.osty declare toString as
// `#[intrinsic_methods]` placeholders with empty bodies, which would
// otherwise lower to dead IR; this dispatcher routes to the same
// runtime helpers that emitInterpolationStringPiece uses.
func (g *generator) emitPrimitiveToStringCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok || field.IsOptional || field.Name != "toString" {
		return value{}, false, nil
	}
	if len(call.Args) != 0 {
		return value{}, false, nil
	}
	baseInfo, ok := g.staticExprInfo(field.X)
	if !ok {
		return value{}, false, nil
	}
	switch baseInfo.typ {
	case "i64", "double", "i1", "i32", "i8":
	default:
		return value{}, false, nil
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	switch base.typ {
	case "i64":
		out, err := g.emitRuntimeIntToString(base)
		return out, true, err
	case "double":
		out, err := g.emitRuntimeFloatToString(base)
		return out, true, err
	case "i1":
		out, err := g.emitRuntimeBoolToString(base)
		return out, true, err
	case "i32":
		out, err := g.emitRuntimeCharToString(base)
		return out, true, err
	case "i8":
		out, err := g.emitRuntimeByteToString(base)
		return out, true, err
	}
	return value{}, false, nil
}

func (g *generator) emitStringMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.stringMethodInfo(call)
	if !ok {
		return value{}, false, nil
	}
	if field.X == nil {
		return value{}, true, unsupported("call", "String method receiver is missing")
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	base, err = g.loadIfPointer(base)
	if err != nil {
		return value{}, true, err
	}
	if base.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "String receiver type %s, want ptr", base.typ)
	}

	switch field.Name {
	case "len":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.len requires no arguments")
		}
		g.declareRuntimeSymbol(llvmStringRuntimeByteLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmStringByteLen(emitter, toOstyValue(base))
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "charCount":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.charCount requires no arguments")
		}
		g.declareRuntimeSymbol(llvmStringRuntimeCharsSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
		g.declareRuntimeSymbol(listRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		chars := llvmStringChars(emitter, toOstyValue(base))
		out := llvmCall(emitter, "i64", listRuntimeLenSymbol(), []*LlvmValue{chars})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "isEmpty":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.isEmpty requires no arguments")
		}
		g.declareRuntimeSymbol(llvmStringRuntimeByteLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		size := llvmStringByteLen(emitter, toOstyValue(base))
		out := llvmCompare(emitter, "eq", size, llvmIntLiteral(0))
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "startsWith":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "String.startsWith requires one positional argument")
		}
		prefix, err := g.emitStdStringsArg(call.Args[0], "startsWith", 0)
		if err != nil {
			return value{}, true, err
		}
		g.declareRuntimeSymbol(llvmStringRuntimeHasPrefixSymbol(), "i1", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmStringHasPrefix(emitter, toOstyValue(base), toOstyValue(prefix))
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "endsWith":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "String.endsWith requires one positional argument")
		}
		suffix, err := g.emitStdStringsArg(call.Args[0], "endsWith", 0)
		if err != nil {
			return value{}, true, err
		}
		g.declareRuntimeSymbol(llvmStringRuntimeHasSuffixSymbol(), "i1", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "i1", llvmStringRuntimeHasSuffixSymbol(), []*LlvmValue{toOstyValue(base), toOstyValue(suffix)})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "contains":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "String.contains requires one positional argument")
		}
		needle, err := g.emitStdStringsArg(call.Args[0], "contains", 0)
		if err != nil {
			return value{}, true, err
		}
		g.declareRuntimeSymbol(llvmStringRuntimeContainsSymbol(), "i1", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "i1", llvmStringRuntimeContainsSymbol(), []*LlvmValue{toOstyValue(base), toOstyValue(needle)})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "indexOf":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "String.indexOf requires one positional argument")
		}
		needle, err := g.emitStdStringsArg(call.Args[0], "indexOf", 0)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitStringIndexOfRuntime(base, needle)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "get":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "String.get requires one positional argument")
		}
		index, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		index, err = g.loadIfPointer(index)
		if err != nil {
			return value{}, true, err
		}
		if index.typ != "i64" {
			return value{}, true, unsupportedf("type-system", "String.get arg 1 type %s, want Int", index.typ)
		}
		g.declareRuntimeSymbol(llvmStringRuntimeBytesSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
		g.declareRuntimeSymbol(listRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
		getSym := listRuntimeGetSymbol("i8")
		g.declareRuntimeSymbol(getSym, "i8", []paramInfo{{typ: "ptr"}, {typ: "i64"}})
		emitter := g.toOstyEmitter()
		bytes := llvmStringBytes(emitter, toOstyValue(base))
		length := llvmCall(emitter, "i64", listRuntimeLenSymbol(), []*LlvmValue{bytes})
		nonNegative := llvmCompare(emitter, "sge", toOstyValue(index), llvmIntLiteral(0))
		beforeEnd := llvmCompare(emitter, "slt", toOstyValue(index), length)
		inRange := llvmLogicalI1(emitter, "and", nonNegative, beforeEnd)
		labels := llvmIfExprStart(emitter, inRange)
		g.takeOstyEmitter(emitter)
		g.currentBlock = labels.thenLabel

		emitter = g.toOstyEmitter()
		item := llvmCall(emitter, "i8", getSym, []*LlvmValue{bytes, toOstyValue(index)})
		box := llvmGcAlloc(emitter, 1, 1, "string.get.byte")
		emitter.body = append(emitter.body, mirStoreI8Text(item.name, box.name))
		g.takeOstyEmitter(emitter)
		g.needsGCRuntime = true
		someVal := value{typ: "ptr", ref: box.name, gcManaged: true}
		thenPred := g.currentBlock

		emitter = g.toOstyEmitter()
		llvmIfExprElse(emitter, labels)
		g.takeOstyEmitter(emitter)
		g.currentBlock = labels.elseLabel
		noneVal := value{typ: "ptr", ref: "null"}
		elsePred := g.currentBlock

		out, err := g.emitIfExprPhi(labels, thenPred, elsePred, someVal, noneVal)
		if err != nil {
			return value{}, true, err
		}
		out.gcManaged = true
		out.rootPaths = g.rootPathsForType("ptr")
		out.sourceType = &ast.OptionalType{Inner: &ast.NamedType{Path: []string{"Byte"}}}
		return out, true, nil
	case "trimPrefix":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "String.trimPrefix requires one positional argument")
		}
		prefix, err := g.emitStdStringsArg(call.Args[0], "trimPrefix", 0)
		if err != nil {
			return value{}, true, err
		}
		g.declareRuntimeSymbol(llvmStringRuntimeTrimPrefixSymbol(), "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "ptr", llvmStringRuntimeTrimPrefixSymbol(), []*LlvmValue{toOstyValue(base), toOstyValue(prefix)})
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.sourceType = &ast.NamedType{Path: []string{"String"}}
		return v, true, nil
	case "trimSuffix":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "String.trimSuffix requires one positional argument")
		}
		suffix, err := g.emitStdStringsArg(call.Args[0], "trimSuffix", 0)
		if err != nil {
			return value{}, true, err
		}
		g.declareRuntimeSymbol(llvmStringRuntimeTrimSuffixSymbol(), "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "ptr", llvmStringRuntimeTrimSuffixSymbol(), []*LlvmValue{toOstyValue(base), toOstyValue(suffix)})
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.sourceType = &ast.NamedType{Path: []string{"String"}}
		return v, true, nil
	case "split":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "String.split requires one positional argument")
		}
		sep, err := g.emitStdStringsArg(call.Args[0], "split", 0)
		if err != nil {
			return value{}, true, err
		}
		g.declareRuntimeSymbol(llvmStringRuntimeSplitSymbol(), "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmStringSplit(emitter, toOstyValue(base), toOstyValue(sep))
		g.takeOstyEmitter(emitter)
		parts := fromOstyValue(out)
		parts.gcManaged = true
		parts.listElemTyp = "ptr"
		parts.listElemString = true
		return parts, true, nil
	case "lines":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.lines requires no arguments")
		}
		g.declareRuntimeSymbol(llvmStringRuntimeSplitSymbol(), "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
		emitter := g.toOstyEmitter()
		sep := llvmStringLiteral(emitter, "\n")
		out := llvmStringSplit(emitter, toOstyValue(base), sep)
		g.takeOstyEmitter(emitter)
		parts := fromOstyValue(out)
		parts.gcManaged = true
		parts.listElemTyp = "ptr"
		parts.listElemString = true
		return parts, true, nil
	case "join":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "String.join requires one positional argument")
		}
		parts, err := g.emitExprWithHintAndSourceType(call.Args[0].Value, nil, "ptr", true, "", "", false, "", false)
		if err != nil {
			return value{}, true, err
		}
		parts, err = g.loadIfPointer(parts)
		if err != nil {
			return value{}, true, err
		}
		if parts.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "String.join arg 1 type %s, want List<String>", parts.typ)
		}
		g.declareRuntimeSymbol(llvmStringRuntimeJoinSymbol(), "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmStringRuntimeJoin(emitter, toOstyValue(parts), toOstyValue(base))
		g.takeOstyEmitter(emitter)
		joined := fromOstyValue(out)
		joined.gcManaged = true
		joined.sourceType = &ast.NamedType{Path: []string{"String"}}
		return joined, true, nil
	case "trim":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.trim requires no arguments")
		}
		g.declareRuntimeSymbol(llvmStringRuntimeTrimSpaceSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmStringRuntimeTrimSpace(emitter, toOstyValue(base))
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		return v, true, nil
	case "trimStart":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.trimStart requires no arguments")
		}
		g.declareRuntimeSymbol(llvmStringRuntimeTrimStartSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmStringRuntimeTrimStart(emitter, toOstyValue(base))
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.sourceType = &ast.NamedType{Path: []string{"String"}}
		return v, true, nil
	case "trimEnd":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.trimEnd requires no arguments")
		}
		g.declareRuntimeSymbol(llvmStringRuntimeTrimEndSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmStringRuntimeTrimEnd(emitter, toOstyValue(base))
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.sourceType = &ast.NamedType{Path: []string{"String"}}
		return v, true, nil
	case "replace":
		if len(call.Args) != 2 {
			return value{}, true, unsupported("call", "String.replace requires two positional arguments")
		}
		old, err := g.emitStdStringsArg(call.Args[0], "replace", 0)
		if err != nil {
			return value{}, true, err
		}
		newValue, err := g.emitStdStringsArg(call.Args[1], "replace", 1)
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitStringReplaceRuntime(base, old, newValue)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "repeat":
		if len(call.Args) != 1 {
			return value{}, true, unsupported("call", "String.repeat requires one positional argument")
		}
		n, err := g.emitStdStringsIntArg(call.Args[0], "repeat", 0)
		if err != nil {
			return value{}, true, err
		}
		g.declareRuntimeSymbol(llvmStringRuntimeRepeatSymbol(), "ptr", []paramInfo{{typ: "ptr"}, {typ: "i64"}})
		emitter := g.toOstyEmitter()
		out := llvmStringRuntimeRepeat(emitter, toOstyValue(base), toOstyValue(n))
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.sourceType = &ast.NamedType{Path: []string{"String"}}
		return v, true, nil
	case "substring", "slice":
		if len(call.Args) != 2 {
			return value{}, true, unsupportedf("call", "String.%s requires two positional arguments", field.Name)
		}
		start, err := g.emitStdStringsIntArg(call.Args[0], field.Name, 0)
		if err != nil {
			return value{}, true, err
		}
		end, err := g.emitStdStringsIntArg(call.Args[1], field.Name, 1)
		if err != nil {
			return value{}, true, err
		}
		g.declareRuntimeSymbol(llvmStringRuntimeSliceSymbol(), "ptr", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "i64"}})
		emitter := g.toOstyEmitter()
		out := llvmStringRuntimeSlice(emitter, toOstyValue(base), toOstyValue(start), toOstyValue(end))
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.sourceType = &ast.NamedType{Path: []string{"String"}}
		return v, true, nil
	case "toUpper":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.toUpper requires no arguments")
		}
		g.declareRuntimeSymbol(llvmStringRuntimeToUpperSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmStringRuntimeToUpper(emitter, toOstyValue(base))
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.sourceType = &ast.NamedType{Path: []string{"String"}}
		return v, true, nil
	case "toLower":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.toLower requires no arguments")
		}
		g.declareRuntimeSymbol(llvmStringRuntimeToLowerSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmStringRuntimeToLower(emitter, toOstyValue(base))
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.sourceType = &ast.NamedType{Path: []string{"String"}}
		return v, true, nil
	case "toInt":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.toInt requires no arguments")
		}
		out, err := g.emitStringToIntResult(base)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "toFloat":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.toFloat requires no arguments")
		}
		out, err := g.emitStringToFloatResult(base)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "toString":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.toString requires no arguments")
		}
		base.gcManaged = true
		return base, true, nil
	case "chars":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.chars requires no arguments")
		}
		g.declareRuntimeSymbol(llvmStringRuntimeCharsSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmStringChars(emitter, toOstyValue(base))
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.listElemTyp = "i32"
		return v, true, nil
	case "bytes":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.bytes requires no arguments")
		}
		g.declareRuntimeSymbol(llvmStringRuntimeBytesSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmStringBytes(emitter, toOstyValue(base))
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.listElemTyp = "i8"
		return v, true, nil
	case "toBytes":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "String.toBytes requires no arguments")
		}
		g.declareRuntimeSymbol(llvmStringRuntimeToBytesSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmStringToBytes(emitter, toOstyValue(base))
		g.takeOstyEmitter(emitter)
		v := fromOstyValue(out)
		v.gcManaged = true
		v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
		return v, true, nil
	default:
		return value{}, true, unsupportedf("call", "String.%s is not supported by legacy llvmgen yet", field.Name)
	}
}

func (g *generator) emitOptionalBoxedI64(index value, site string) (value, error) {
	if index.typ != "i64" {
		return value{}, unsupportedf("type-system", "boxed optional Int payload type %s, want i64", index.typ)
	}
	optInt := &ast.OptionalType{Inner: &ast.NamedType{Path: []string{"Int"}}}
	emitter := g.toOstyEmitter()
	present := llvmCompare(emitter, "sge", toOstyValue(index), llvmIntLiteral(0))
	labels := llvmIfExprStart(emitter, present)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	emitter = g.toOstyEmitter()
	box := llvmGcAlloc(emitter, 1, 8, site)
	emitter.body = append(emitter.body, mirStoreI64Text(index.ref, box.name))
	g.takeOstyEmitter(emitter)
	g.needsGCRuntime = true
	someVal := value{typ: "ptr", ref: box.name, gcManaged: true}
	thenPred := g.currentBlock

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel
	noneVal := value{typ: "ptr", ref: "null"}
	elsePred := g.currentBlock

	out, err := g.emitIfExprPhi(labels, thenPred, elsePred, someVal, noneVal)
	if err != nil {
		return value{}, err
	}
	out.gcManaged = true
	out.rootPaths = g.rootPathsForType("ptr")
	out.sourceType = optInt
	return out, nil
}

func (g *generator) emitStringIndexOfRuntime(base value, needle value) (value, error) {
	g.declareRuntimeSymbol(llvmStringRuntimeIndexOfSymbol(), "i64", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	index := llvmStringRuntimeIndexOf(emitter, toOstyValue(base), toOstyValue(needle))
	g.takeOstyEmitter(emitter)
	return g.emitOptionalBoxedI64(fromOstyValue(index), "string.index_of.int")
}

func (g *generator) emitStringReplaceRuntime(base value, old value, newValue value) (value, error) {
	g.declareRuntimeSymbol(llvmStringRuntimeReplaceSymbol(), "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmStringRuntimeReplace(emitter, toOstyValue(base), toOstyValue(old), toOstyValue(newValue))
	g.takeOstyEmitter(emitter)
	replaced := fromOstyValue(out)
	replaced.gcManaged = true
	replaced.sourceType = &ast.NamedType{Path: []string{"String"}}
	return replaced, nil
}

func (g *generator) emitBytesMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.bytesMethodInfo(call)
	if !ok {
		return value{}, false, nil
	}
	if field.X == nil {
		return value{}, true, unsupported("call", "Bytes method receiver is missing")
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	base, err = g.loadIfPointer(base)
	if err != nil {
		return value{}, true, err
	}
	if base.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "Bytes receiver type %s, want ptr", base.typ)
	}

	switch field.Name {
	case "len":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "Bytes.len requires no arguments")
		}
		symbol := mirRtBytesLenSymbolName()
		g.declareRuntimeSymbol(symbol, "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "i64", symbol, []*LlvmValue{toOstyValue(base)})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "isEmpty":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "Bytes.isEmpty requires no arguments")
		}
		symbol := mirRtBytesIsEmptySymbol()
		g.declareRuntimeSymbol(symbol, "i1", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "i1", symbol, []*LlvmValue{toOstyValue(base)})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "get":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.get requires one positional argument")
		}
		index, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		index, err = g.loadIfPointer(index)
		if err != nil {
			return value{}, true, err
		}
		if index.typ != "i64" {
			return value{}, true, unsupportedf("type-system", "Bytes.get arg 1 type %s, want Int", index.typ)
		}
		out, err := g.emitBytesGetRuntime(base, index)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "contains":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.contains requires one positional argument")
		}
		needle, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		needle, err = g.loadIfPointer(needle)
		if err != nil {
			return value{}, true, err
		}
		if needle.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.contains arg 1 type %s, want Bytes", needle.typ)
		}
		out, err := g.emitBytesContainsRuntime(base, needle)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "startsWith":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.startsWith requires one positional argument")
		}
		prefix, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		prefix, err = g.loadIfPointer(prefix)
		if err != nil {
			return value{}, true, err
		}
		if prefix.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.startsWith arg 1 type %s, want Bytes", prefix.typ)
		}
		out, err := g.emitBytesStartsWithRuntime(base, prefix)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "endsWith":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.endsWith requires one positional argument")
		}
		suffix, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		suffix, err = g.loadIfPointer(suffix)
		if err != nil {
			return value{}, true, err
		}
		if suffix.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.endsWith arg 1 type %s, want Bytes", suffix.typ)
		}
		out, err := g.emitBytesEndsWithRuntime(base, suffix)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "indexOf":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.indexOf requires one positional argument")
		}
		needle, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		needle, err = g.loadIfPointer(needle)
		if err != nil {
			return value{}, true, err
		}
		if needle.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.indexOf arg 1 type %s, want Bytes", needle.typ)
		}
		out, err := g.emitBytesIndexOfRuntime(base, needle)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "lastIndexOf":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.lastIndexOf requires one positional argument")
		}
		needle, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		needle, err = g.loadIfPointer(needle)
		if err != nil {
			return value{}, true, err
		}
		if needle.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.lastIndexOf arg 1 type %s, want Bytes", needle.typ)
		}
		out, err := g.emitBytesLastIndexOfRuntime(base, needle)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "split":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.split requires one positional argument")
		}
		sep, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		sep, err = g.loadIfPointer(sep)
		if err != nil {
			return value{}, true, err
		}
		if sep.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.split arg 1 type %s, want Bytes", sep.typ)
		}
		out, err := g.emitBytesSplitRuntime(base, sep)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "join":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.join requires one positional argument")
		}
		parts, err := g.emitBytesListOfBytesExpr(call.Args[0].Value, "Bytes.join arg 1")
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitBytesJoinRuntime(parts, base)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "concat":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.concat requires one positional argument")
		}
		other, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		other, err = g.loadIfPointer(other)
		if err != nil {
			return value{}, true, err
		}
		if other.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.concat arg 1 type %s, want Bytes", other.typ)
		}
		out, err := g.emitBytesConcatRuntime(base, other)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "repeat":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.repeat requires one positional argument")
		}
		n, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		n, err = g.loadIfPointer(n)
		if err != nil {
			return value{}, true, err
		}
		if n.typ != "i64" {
			return value{}, true, unsupportedf("type-system", "Bytes.repeat arg 1 type %s, want Int", n.typ)
		}
		out, err := g.emitBytesRepeatRuntime(base, n)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "replace":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.replace requires two positional arguments")
		}
		oldValue, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		oldValue, err = g.loadIfPointer(oldValue)
		if err != nil {
			return value{}, true, err
		}
		if oldValue.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.replace arg 1 type %s, want Bytes", oldValue.typ)
		}
		newValue, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		newValue, err = g.loadIfPointer(newValue)
		if err != nil {
			return value{}, true, err
		}
		if newValue.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.replace arg 2 type %s, want Bytes", newValue.typ)
		}
		out, err := g.emitBytesReplaceRuntime(base, oldValue, newValue)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "replaceAll":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.replaceAll requires two positional arguments")
		}
		oldValue, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		oldValue, err = g.loadIfPointer(oldValue)
		if err != nil {
			return value{}, true, err
		}
		if oldValue.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.replaceAll arg 1 type %s, want Bytes", oldValue.typ)
		}
		newValue, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		newValue, err = g.loadIfPointer(newValue)
		if err != nil {
			return value{}, true, err
		}
		if newValue.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.replaceAll arg 2 type %s, want Bytes", newValue.typ)
		}
		out, err := g.emitBytesReplaceAllRuntime(base, oldValue, newValue)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "trimLeft":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.trimLeft requires one positional argument")
		}
		strip, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		strip, err = g.loadIfPointer(strip)
		if err != nil {
			return value{}, true, err
		}
		if strip.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.trimLeft arg 1 type %s, want Bytes", strip.typ)
		}
		out, err := g.emitBytesTrimLeftRuntime(base, strip)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "trimRight":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.trimRight requires one positional argument")
		}
		strip, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		strip, err = g.loadIfPointer(strip)
		if err != nil {
			return value{}, true, err
		}
		if strip.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.trimRight arg 1 type %s, want Bytes", strip.typ)
		}
		out, err := g.emitBytesTrimRightRuntime(base, strip)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "trim":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.trim requires one positional argument")
		}
		strip, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		strip, err = g.loadIfPointer(strip)
		if err != nil {
			return value{}, true, err
		}
		if strip.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.trim arg 1 type %s, want Bytes", strip.typ)
		}
		out, err := g.emitBytesTrimRuntime(base, strip)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "trimSpace":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "Bytes.trimSpace requires no arguments")
		}
		out, err := g.emitBytesTrimSpaceRuntime(base)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "toUpper":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "Bytes.toUpper requires no arguments")
		}
		out, err := g.emitBytesToUpperRuntime(base)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "toLower":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "Bytes.toLower requires no arguments")
		}
		out, err := g.emitBytesToLowerRuntime(base)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "toHex":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "Bytes.toHex requires no arguments")
		}
		out, err := g.emitBytesToHexRuntime(base)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "slice":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.slice requires two positional arguments")
		}
		start, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		start, err = g.loadIfPointer(start)
		if err != nil {
			return value{}, true, err
		}
		if start.typ != "i64" {
			return value{}, true, unsupportedf("type-system", "Bytes.slice arg 1 type %s, want Int", start.typ)
		}
		end, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		end, err = g.loadIfPointer(end)
		if err != nil {
			return value{}, true, err
		}
		if end.typ != "i64" {
			return value{}, true, unsupportedf("type-system", "Bytes.slice arg 2 type %s, want Int", end.typ)
		}
		out, err := g.emitBytesSliceRuntime(base, start, end)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "toString":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "Bytes.toString requires no arguments")
		}
		out, err := g.emitBytesToStringResult(base)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	default:
		return value{}, true, unsupportedf("call", "Bytes.%s is not supported by legacy llvmgen yet", field.Name)
	}
}

func (g *generator) emitBytesGetRuntime(base, index value) (value, error) {
	lenSymbol := mirRtBytesLenSymbolName()
	getSymbol := mirRtBytesGetSymbol()
	g.declareRuntimeSymbol(lenSymbol, "i64", []paramInfo{{typ: "ptr"}})
	g.declareRuntimeSymbol(getSymbol, "i8", []paramInfo{{typ: "ptr"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	length := llvmCall(emitter, "i64", lenSymbol, []*LlvmValue{toOstyValue(base)})
	nonNegative := llvmCompare(emitter, "sge", toOstyValue(index), llvmIntLiteral(0))
	beforeEnd := llvmCompare(emitter, "slt", toOstyValue(index), length)
	inRange := llvmLogicalI1(emitter, "and", nonNegative, beforeEnd)
	labels := llvmIfExprStart(emitter, inRange)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	emitter = g.toOstyEmitter()
	item := llvmCall(emitter, "i8", getSymbol, []*LlvmValue{toOstyValue(base), toOstyValue(index)})
	box := llvmGcAlloc(emitter, 1, 1, "bytes.get.byte")
	emitter.body = append(emitter.body, mirStoreI8Text(item.name, box.name))
	g.takeOstyEmitter(emitter)
	g.needsGCRuntime = true
	someVal := value{typ: "ptr", ref: box.name, gcManaged: true}
	thenPred := g.currentBlock

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel
	noneVal := value{typ: "ptr", ref: "null"}
	elsePred := g.currentBlock

	out, err := g.emitIfExprPhi(labels, thenPred, elsePred, someVal, noneVal)
	if err != nil {
		return value{}, err
	}
	out.gcManaged = true
	out.rootPaths = g.rootPathsForType("ptr")
	out.sourceType = &ast.OptionalType{Inner: &ast.NamedType{Path: []string{"Byte"}}}
	return out, nil
}

func (g *generator) emitBytesLastIndexRaw(base, needle value) (value, error) {
	symbol := mirRtBytesLastIndexOfSymbol()
	g.declareRuntimeSymbol(symbol, "i64", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	index := llvmCall(emitter, "i64", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(needle)})
	g.takeOstyEmitter(emitter)
	return fromOstyValue(index), nil
}

func (g *generator) emitBytesIndexOfRuntime(base, needle value) (value, error) {
	symbol := mirRtBytesIndexOfSymbol()
	g.declareRuntimeSymbol(symbol, "i64", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	index := llvmCall(emitter, "i64", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(needle)})
	g.takeOstyEmitter(emitter)
	return g.emitOptionalBoxedI64(fromOstyValue(index), "bytes.index_of.int")
}

func (g *generator) emitBytesContainsRuntime(base, needle value) (value, error) {
	symbol := mirRtBytesIndexOfSymbol()
	g.declareRuntimeSymbol(symbol, "i64", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	index := llvmCall(emitter, "i64", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(needle)})
	out := llvmCompare(emitter, "sge", index, llvmIntLiteral(0))
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitBytesStartsWithRuntime(base, prefix value) (value, error) {
	symbol := mirRtBytesIndexOfSymbol()
	g.declareRuntimeSymbol(symbol, "i64", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	index := llvmCall(emitter, "i64", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(prefix)})
	out := llvmCompare(emitter, "eq", index, llvmIntLiteral(0))
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitBytesEndsWithRuntime(base, suffix value) (value, error) {
	lenSymbol := mirRtBytesLenSymbolName()
	g.declareRuntimeSymbol(lenSymbol, "i64", []paramInfo{{typ: "ptr"}})
	lastIndex, err := g.emitBytesLastIndexRaw(base, suffix)
	if err != nil {
		return value{}, err
	}
	emitter := g.toOstyEmitter()
	baseLen := llvmCall(emitter, "i64", lenSymbol, []*LlvmValue{toOstyValue(base)})
	suffixLen := llvmCall(emitter, "i64", lenSymbol, []*LlvmValue{toOstyValue(suffix)})
	expectedName := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirSubI64Text(expectedName, baseLen.name, suffixLen.name))
	found := llvmCompare(emitter, "sge", toOstyValue(lastIndex), llvmIntLiteral(0))
	atEnd := llvmCompare(emitter, "eq", toOstyValue(lastIndex), &LlvmValue{typ: "i64", name: expectedName})
	out := llvmLogicalI1(emitter, "and", found, atEnd)
	g.takeOstyEmitter(emitter)
	return fromOstyValue(out), nil
}

func (g *generator) emitBytesLastIndexOfRuntime(base, needle value) (value, error) {
	index, err := g.emitBytesLastIndexRaw(base, needle)
	if err != nil {
		return value{}, err
	}
	return g.emitOptionalBoxedI64(index, "bytes.last_index_of.int")
}

func (g *generator) emitBytesFromListRuntime(items value) (value, error) {
	symbol := mirRtBytesSymbol("from_list")
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(items)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitBytesListOfBytesExpr(expr ast.Expr, label string) (value, error) {
	if expr == nil {
		return value{}, unsupportedf("call", "%s requires List<Bytes>", label)
	}
	if !g.staticExprListElemIsBytes(expr) {
		return value{}, unsupportedf("type-system", "%s source type is not List<Bytes>", label)
	}
	v, err := g.emitExpr(expr)
	if err != nil {
		return value{}, err
	}
	loaded, err := g.loadIfPointer(v)
	if err != nil {
		return value{}, err
	}
	if loaded.typ != "ptr" {
		return value{}, unsupportedf("type-system", "%s type %s, want List<Bytes>", label, loaded.typ)
	}
	loaded.gcManaged = true
	loaded.listElemTyp = "ptr"
	loaded.listElemString = false
	loaded.sourceType = bytesListSourceType()
	return loaded, nil
}

func (g *generator) emitBytesSplitRuntime(base, sep value) (value, error) {
	symbol := mirRtBytesSplitSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(sep)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.listElemTyp = "ptr"
	v.listElemString = false
	v.sourceType = bytesListSourceType()
	return v, nil
}

func (g *generator) emitBytesJoinRuntime(parts, sep value) (value, error) {
	symbol := mirRtBytesJoinSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(parts), toOstyValue(sep)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitBytesConcatRuntime(left, right value) (value, error) {
	symbol := mirRtBytesConcatSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(left), toOstyValue(right)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitBytesRepeatRuntime(base, n value) (value, error) {
	symbol := mirRtBytesRepeatSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(n)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitBytesReplaceRuntime(base, oldValue, newValue value) (value, error) {
	symbol := mirRtBytesReplaceSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(oldValue), toOstyValue(newValue)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitBytesReplaceAllRuntime(base, oldValue, newValue value) (value, error) {
	symbol := mirRtBytesReplaceAllSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(oldValue), toOstyValue(newValue)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitBytesTrimLeftRuntime(base, strip value) (value, error) {
	symbol := mirRtBytesTrimLeftSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(strip)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitBytesTrimRightRuntime(base, strip value) (value, error) {
	symbol := mirRtBytesTrimRightSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(strip)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitBytesTrimRuntime(base, strip value) (value, error) {
	symbol := mirRtBytesTrimSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(strip)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitBytesTrimSpaceRuntime(base value) (value, error) {
	symbol := mirRtBytesTrimSpaceSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitBytesToUpperRuntime(base value) (value, error) {
	symbol := mirRtBytesToUpperSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitBytesToLowerRuntime(base value) (value, error) {
	symbol := mirRtBytesToLowerSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitBytesToHexRuntime(base value) (value, error) {
	symbol := mirRtBytesToHexSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"String"}}
	return v, nil
}

func (g *generator) emitBytesFromHexResult(text value) (value, error) {
	sourceType := bytesFromHexResultSourceType()
	info, ok := builtinResultTypeFromAST(sourceType, g.typeEnv())
	if !ok {
		return value{}, unsupported("type-system", "Bytes.fromHex Result<Bytes, Error> type is unavailable")
	}
	validateSymbol := mirRtBytesSymbol("is_valid_hex")
	fromHexSymbol := mirRtBytesSymbol("from_hex")
	g.declareRuntimeSymbol(validateSymbol, "i1", []paramInfo{{typ: "ptr"}})
	g.declareRuntimeSymbol(fromHexSymbol, "ptr", []paramInfo{{typ: "ptr"}})

	emitter := g.toOstyEmitter()
	valid := llvmCall(emitter, "i1", validateSymbol, []*LlvmValue{toOstyValue(text)})
	labels := llvmIfExprStart(emitter, valid)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	emitter = g.toOstyEmitter()
	decoded := llvmCall(emitter, "ptr", fromHexSymbol, []*LlvmValue{toOstyValue(text)})
	okResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		decoded,
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	g.takeOstyEmitter(emitter)
	thenValue := value{
		typ:        info.typ,
		ref:        okResult.name,
		sourceType: sourceType,
		rootPaths:  g.rootPathsForType(info.typ),
	}
	thenPred := g.currentBlock

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	emitter = g.toOstyEmitter()
	errResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	g.takeOstyEmitter(emitter)
	elseValue := value{
		typ:        info.typ,
		ref:        errResult.name,
		sourceType: sourceType,
		rootPaths:  g.rootPathsForType(info.typ),
	}
	elsePred := g.currentBlock

	out, err := g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
	if err != nil {
		return value{}, err
	}
	out.sourceType = sourceType
	out.rootPaths = g.rootPathsForType(info.typ)
	return out, nil
}

func (g *generator) emitBytesSliceRuntime(base, start, end value) (value, error) {
	symbol := mirRtBytesSliceSymbol()
	g.declareRuntimeSymbol(symbol, "ptr", []paramInfo{{typ: "ptr"}, {typ: "i64"}, {typ: "i64"}})
	emitter := g.toOstyEmitter()
	out := llvmCall(emitter, "ptr", symbol, []*LlvmValue{toOstyValue(base), toOstyValue(start), toOstyValue(end)})
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.gcManaged = true
	v.sourceType = &ast.NamedType{Path: []string{"Bytes"}}
	return v, nil
}

func (g *generator) emitBytesToStringResult(base value) (value, error) {
	sourceType := bytesToStringResultSourceType()
	info, ok := builtinResultTypeFromAST(sourceType, g.typeEnv())
	if !ok {
		return value{}, unsupported("type-system", "Bytes.toString Result<String, Error> type is unavailable")
	}
	validateSymbol := mirRtBytesSymbol("is_valid_utf8")
	toStringSymbol := mirRtBytesSymbol("to_string")
	g.declareRuntimeSymbol(validateSymbol, "i1", []paramInfo{{typ: "ptr"}})
	g.declareRuntimeSymbol(toStringSymbol, "ptr", []paramInfo{{typ: "ptr"}})

	emitter := g.toOstyEmitter()
	valid := llvmCall(emitter, "i1", validateSymbol, []*LlvmValue{toOstyValue(base)})
	labels := llvmIfExprStart(emitter, valid)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	emitter = g.toOstyEmitter()
	text := llvmCall(emitter, "ptr", toStringSymbol, []*LlvmValue{toOstyValue(base)})
	okResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		text,
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	g.takeOstyEmitter(emitter)
	thenValue := value{
		typ:        info.typ,
		ref:        okResult.name,
		sourceType: sourceType,
		rootPaths:  g.rootPathsForType(info.typ),
	}
	thenPred := g.currentBlock

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	emitter = g.toOstyEmitter()
	errResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	g.takeOstyEmitter(emitter)
	elseValue := value{
		typ:        info.typ,
		ref:        errResult.name,
		sourceType: sourceType,
		rootPaths:  g.rootPathsForType(info.typ),
	}
	elsePred := g.currentBlock

	out, err := g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
	if err != nil {
		return value{}, err
	}
	out.sourceType = sourceType
	out.rootPaths = g.rootPathsForType(info.typ)
	return out, nil
}

func (g *generator) emitStringParseResult(base value, sourceType ast.Type, validateSymbol, parseSymbol, okTyp string) (value, error) {
	info, ok := builtinResultTypeFromAST(sourceType, g.typeEnv())
	if !ok {
		return value{}, unsupported("type-system", "String parse Result type is unavailable")
	}
	g.declareRuntimeSymbol(validateSymbol, "i1", []paramInfo{{typ: "ptr"}})
	g.declareRuntimeSymbol(parseSymbol, okTyp, []paramInfo{{typ: "ptr"}})

	emitter := g.toOstyEmitter()
	valid := llvmCall(emitter, "i1", validateSymbol, []*LlvmValue{toOstyValue(base)})
	labels := llvmIfExprStart(emitter, valid)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	emitter = g.toOstyEmitter()
	parsed := llvmCall(emitter, okTyp, parseSymbol, []*LlvmValue{toOstyValue(base)})
	okResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "0"}),
		parsed,
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	g.takeOstyEmitter(emitter)
	thenValue := value{
		typ:        info.typ,
		ref:        okResult.name,
		sourceType: sourceType,
		rootPaths:  g.rootPathsForType(info.typ),
	}
	thenPred := g.currentBlock

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel

	emitter = g.toOstyEmitter()
	errResult := llvmStructLiteral(emitter, info.typ, []*LlvmValue{
		toOstyValue(value{typ: "i64", ref: "1"}),
		toOstyValue(llvmZeroValue(info.okTyp)),
		toOstyValue(llvmZeroValue(info.errTyp)),
	})
	g.takeOstyEmitter(emitter)
	elseValue := value{
		typ:        info.typ,
		ref:        errResult.name,
		sourceType: sourceType,
		rootPaths:  g.rootPathsForType(info.typ),
	}
	elsePred := g.currentBlock

	out, err := g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
	if err != nil {
		return value{}, err
	}
	out.sourceType = sourceType
	out.rootPaths = g.rootPathsForType(info.typ)
	return out, nil
}

func (g *generator) emitStringToIntResult(base value) (value, error) {
	return g.emitStringParseResult(base, stringToIntResultSourceType(), llvmStringRuntimeIsValidIntSymbol(), llvmStringRuntimeToIntSymbol(), "i64")
}

func (g *generator) emitStringToFloatResult(base value) (value, error) {
	return g.emitStringParseResult(base, stringToFloatResultSourceType(), llvmStringRuntimeIsValidFloatSymbol(), llvmStringRuntimeToFloatSymbol(), "double")
}

func (g *generator) emitBytesNamespaceCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := g.bytesNamespaceCallInfo(call)
	if !ok {
		return value{}, false, nil
	}
	switch field.Name {
	case "from":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.from requires one positional argument")
		}
		if !g.staticExprListElemIsByte(call.Args[0].Value) {
			return value{}, true, unsupported("type-system", "Bytes.from arg 1 must be List<Byte>")
		}
		items, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		items, err = g.loadIfPointer(items)
		if err != nil {
			return value{}, true, err
		}
		if items.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.from arg 1 type %s, want List<Byte>", items.typ)
		}
		out, err := g.emitBytesFromListRuntime(items)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "fromHex":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.fromHex requires one positional argument")
		}
		text, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		text, err = g.loadIfPointer(text)
		if err != nil {
			return value{}, true, err
		}
		if text.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.fromHex arg 1 type %s, want String", text.typ)
		}
		out, err := g.emitBytesFromHexResult(text)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "len":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.len requires one positional argument")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.len arg 1 type %s, want Bytes", base.typ)
		}
		symbol := mirRtBytesLenSymbolName()
		g.declareRuntimeSymbol(symbol, "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "i64", symbol, []*LlvmValue{toOstyValue(base)})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "isEmpty":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.isEmpty requires one positional argument")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.isEmpty arg 1 type %s, want Bytes", base.typ)
		}
		symbol := mirRtBytesIsEmptySymbol()
		g.declareRuntimeSymbol(symbol, "i1", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "i1", symbol, []*LlvmValue{toOstyValue(base)})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "get":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.get requires two positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.get arg 1 type %s, want Bytes", base.typ)
		}
		index, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		index, err = g.loadIfPointer(index)
		if err != nil {
			return value{}, true, err
		}
		if index.typ != "i64" {
			return value{}, true, unsupportedf("type-system", "Bytes.get arg 2 type %s, want Int", index.typ)
		}
		out, err := g.emitBytesGetRuntime(base, index)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "contains":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.contains requires two positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.contains arg 1 type %s, want Bytes", base.typ)
		}
		needle, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		needle, err = g.loadIfPointer(needle)
		if err != nil {
			return value{}, true, err
		}
		if needle.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.contains arg 2 type %s, want Bytes", needle.typ)
		}
		out, err := g.emitBytesContainsRuntime(base, needle)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "startsWith":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.startsWith requires two positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.startsWith arg 1 type %s, want Bytes", base.typ)
		}
		prefix, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		prefix, err = g.loadIfPointer(prefix)
		if err != nil {
			return value{}, true, err
		}
		if prefix.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.startsWith arg 2 type %s, want Bytes", prefix.typ)
		}
		out, err := g.emitBytesStartsWithRuntime(base, prefix)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "endsWith":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.endsWith requires two positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.endsWith arg 1 type %s, want Bytes", base.typ)
		}
		suffix, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		suffix, err = g.loadIfPointer(suffix)
		if err != nil {
			return value{}, true, err
		}
		if suffix.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.endsWith arg 2 type %s, want Bytes", suffix.typ)
		}
		out, err := g.emitBytesEndsWithRuntime(base, suffix)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "indexOf":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.indexOf requires two positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.indexOf arg 1 type %s, want Bytes", base.typ)
		}
		needle, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		needle, err = g.loadIfPointer(needle)
		if err != nil {
			return value{}, true, err
		}
		if needle.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.indexOf arg 2 type %s, want Bytes", needle.typ)
		}
		out, err := g.emitBytesIndexOfRuntime(base, needle)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "lastIndexOf":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.lastIndexOf requires two positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.lastIndexOf arg 1 type %s, want Bytes", base.typ)
		}
		needle, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		needle, err = g.loadIfPointer(needle)
		if err != nil {
			return value{}, true, err
		}
		if needle.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.lastIndexOf arg 2 type %s, want Bytes", needle.typ)
		}
		out, err := g.emitBytesLastIndexOfRuntime(base, needle)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "split":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.split requires two positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.split arg 1 type %s, want Bytes", base.typ)
		}
		sep, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		sep, err = g.loadIfPointer(sep)
		if err != nil {
			return value{}, true, err
		}
		if sep.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.split arg 2 type %s, want Bytes", sep.typ)
		}
		out, err := g.emitBytesSplitRuntime(base, sep)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "join":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.join requires two positional arguments")
		}
		sep, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		sep, err = g.loadIfPointer(sep)
		if err != nil {
			return value{}, true, err
		}
		if sep.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.join arg 1 type %s, want Bytes", sep.typ)
		}
		parts, err := g.emitBytesListOfBytesExpr(call.Args[1].Value, "Bytes.join arg 2")
		if err != nil {
			return value{}, true, err
		}
		out, err := g.emitBytesJoinRuntime(parts, sep)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "concat":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.concat requires two positional arguments")
		}
		left, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		left, err = g.loadIfPointer(left)
		if err != nil {
			return value{}, true, err
		}
		if left.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.concat arg 1 type %s, want Bytes", left.typ)
		}
		right, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		right, err = g.loadIfPointer(right)
		if err != nil {
			return value{}, true, err
		}
		if right.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.concat arg 2 type %s, want Bytes", right.typ)
		}
		out, err := g.emitBytesConcatRuntime(left, right)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "repeat":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.repeat requires two positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.repeat arg 1 type %s, want Bytes", base.typ)
		}
		n, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		n, err = g.loadIfPointer(n)
		if err != nil {
			return value{}, true, err
		}
		if n.typ != "i64" {
			return value{}, true, unsupportedf("type-system", "Bytes.repeat arg 2 type %s, want Int", n.typ)
		}
		out, err := g.emitBytesRepeatRuntime(base, n)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "replace":
		if len(call.Args) != 3 || call.Args[0] == nil || call.Args[1] == nil || call.Args[2] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[2].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil || call.Args[2].Value == nil {
			return value{}, true, unsupported("call", "Bytes.replace requires three positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.replace arg 1 type %s, want Bytes", base.typ)
		}
		oldValue, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		oldValue, err = g.loadIfPointer(oldValue)
		if err != nil {
			return value{}, true, err
		}
		if oldValue.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.replace arg 2 type %s, want Bytes", oldValue.typ)
		}
		newValue, err := g.emitExpr(call.Args[2].Value)
		if err != nil {
			return value{}, true, err
		}
		newValue, err = g.loadIfPointer(newValue)
		if err != nil {
			return value{}, true, err
		}
		if newValue.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.replace arg 3 type %s, want Bytes", newValue.typ)
		}
		out, err := g.emitBytesReplaceRuntime(base, oldValue, newValue)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "replaceAll":
		if len(call.Args) != 3 || call.Args[0] == nil || call.Args[1] == nil || call.Args[2] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[2].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil || call.Args[2].Value == nil {
			return value{}, true, unsupported("call", "Bytes.replaceAll requires three positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.replaceAll arg 1 type %s, want Bytes", base.typ)
		}
		oldValue, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		oldValue, err = g.loadIfPointer(oldValue)
		if err != nil {
			return value{}, true, err
		}
		if oldValue.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.replaceAll arg 2 type %s, want Bytes", oldValue.typ)
		}
		newValue, err := g.emitExpr(call.Args[2].Value)
		if err != nil {
			return value{}, true, err
		}
		newValue, err = g.loadIfPointer(newValue)
		if err != nil {
			return value{}, true, err
		}
		if newValue.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.replaceAll arg 3 type %s, want Bytes", newValue.typ)
		}
		out, err := g.emitBytesReplaceAllRuntime(base, oldValue, newValue)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "trimLeft":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.trimLeft requires two positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.trimLeft arg 1 type %s, want Bytes", base.typ)
		}
		strip, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		strip, err = g.loadIfPointer(strip)
		if err != nil {
			return value{}, true, err
		}
		if strip.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.trimLeft arg 2 type %s, want Bytes", strip.typ)
		}
		out, err := g.emitBytesTrimLeftRuntime(base, strip)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "trimRight":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.trimRight requires two positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.trimRight arg 1 type %s, want Bytes", base.typ)
		}
		strip, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		strip, err = g.loadIfPointer(strip)
		if err != nil {
			return value{}, true, err
		}
		if strip.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.trimRight arg 2 type %s, want Bytes", strip.typ)
		}
		out, err := g.emitBytesTrimRightRuntime(base, strip)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "trim":
		if len(call.Args) != 2 || call.Args[0] == nil || call.Args[1] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil {
			return value{}, true, unsupported("call", "Bytes.trim requires two positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.trim arg 1 type %s, want Bytes", base.typ)
		}
		strip, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		strip, err = g.loadIfPointer(strip)
		if err != nil {
			return value{}, true, err
		}
		if strip.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.trim arg 2 type %s, want Bytes", strip.typ)
		}
		out, err := g.emitBytesTrimRuntime(base, strip)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "trimSpace":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.trimSpace requires one positional argument")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.trimSpace arg 1 type %s, want Bytes", base.typ)
		}
		out, err := g.emitBytesTrimSpaceRuntime(base)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "toUpper":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.toUpper requires one positional argument")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.toUpper arg 1 type %s, want Bytes", base.typ)
		}
		out, err := g.emitBytesToUpperRuntime(base)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "toLower":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.toLower requires one positional argument")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.toLower arg 1 type %s, want Bytes", base.typ)
		}
		out, err := g.emitBytesToLowerRuntime(base)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "toHex":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.toHex requires one positional argument")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.toHex arg 1 type %s, want Bytes", base.typ)
		}
		out, err := g.emitBytesToHexRuntime(base)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "slice":
		if len(call.Args) != 3 || call.Args[0] == nil || call.Args[1] == nil || call.Args[2] == nil || call.Args[0].Name != "" || call.Args[1].Name != "" || call.Args[2].Name != "" || call.Args[0].Value == nil || call.Args[1].Value == nil || call.Args[2].Value == nil {
			return value{}, true, unsupported("call", "Bytes.slice requires three positional arguments")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.slice arg 1 type %s, want Bytes", base.typ)
		}
		start, err := g.emitExpr(call.Args[1].Value)
		if err != nil {
			return value{}, true, err
		}
		start, err = g.loadIfPointer(start)
		if err != nil {
			return value{}, true, err
		}
		if start.typ != "i64" {
			return value{}, true, unsupportedf("type-system", "Bytes.slice arg 2 type %s, want Int", start.typ)
		}
		end, err := g.emitExpr(call.Args[2].Value)
		if err != nil {
			return value{}, true, err
		}
		end, err = g.loadIfPointer(end)
		if err != nil {
			return value{}, true, err
		}
		if end.typ != "i64" {
			return value{}, true, unsupportedf("type-system", "Bytes.slice arg 3 type %s, want Int", end.typ)
		}
		out, err := g.emitBytesSliceRuntime(base, start, end)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	case "toString":
		if len(call.Args) != 1 || call.Args[0] == nil || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "Bytes.toString requires one positional argument")
		}
		base, err := g.emitExpr(call.Args[0].Value)
		if err != nil {
			return value{}, true, err
		}
		base, err = g.loadIfPointer(base)
		if err != nil {
			return value{}, true, err
		}
		if base.typ != "ptr" {
			return value{}, true, unsupportedf("type-system", "Bytes.toString arg 1 type %s, want Bytes", base.typ)
		}
		out, err := g.emitBytesToStringResult(base)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	default:
		return value{}, true, unsupportedf("call", "Bytes.%s is not supported by legacy llvmgen yet", field.Name)
	}
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
	case "isEmpty":
		// `list.isEmpty()` is the stdlib default `self.len() == 0`. We
		// inline it as `icmp eq i64 <len>, 0` instead of routing through
		// LLVM018 stdlib-body lowering so the toolchain probe can clear
		// this wall without the whole default-body lowering stack.
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "list.isEmpty requires no arguments")
		}
		g.declareRuntimeSymbol(listRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		lenVal := llvmCall(emitter, "i64", listRuntimeLenSymbol(), []*LlvmValue{toOstyValue(base)})
		cmp := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirICmpEqI64LiteralText(cmp, lenVal.name, "0"))
		g.takeOstyEmitter(emitter)
		return value{typ: "i1", ref: cmp}, true, nil
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
	case "get":
		out, err := g.emitListGetCall(call, base, elemTyp, elemString)
		return out, true, err
	default:
		return value{}, false, nil
	}
}

// emitListGetCall lowers `list.get(i) -> T?` — the bounds-checking
// counterpart to raw `list[i]` indexing. The runtime's
// `osty_rt_list_get_<suffix>` aborts on out-of-range access, so the
// Option semantics are emitted in IR: check `0 <= i < len` first, take
// the runtime get only on the in-bounds branch, and let a phi node
// thread the result into the parent block.
//
// For ptr-backed elements the phi yields the element ptr directly
// (null = None). For scalar elements (i64, i1, double, i8, i32) the
// in-bounds branch GC-allocates a same-size heap box, stores the
// scalar into it, and phis the box ptr against null — Option<T> is
// always ptr-backed at the LLVM layer, so downstream `.isSome()` /
// `match` see a uniform nullable ptr regardless of T. Mirrors the
// Map.get scalar-V lowering established for `Map<K, Int>.get(k)`.
func (g *generator) emitListGetCall(call *ast.CallExpr, base value, elemTyp string, elemString bool) (value, error) {
	if len(call.Args) != 1 {
		return value{}, unsupportedf("call", "list.get expects 1 argument, got %d", len(call.Args))
	}
	arg := call.Args[0]
	if arg == nil || arg.Name != "" || arg.Value == nil {
		return value{}, unsupportedf("call", "list.get requires a positional Int argument")
	}
	byteSize, scalar, ok := listGetBoxByteSize(elemTyp)
	if !ok {
		return value{}, unsupportedf("type-system", "list.get on List<%s>: Option lowering not yet wired for this element type (supported: ptr / String / i64 / i1 / double / i8 / i32)", elemTyp)
	}
	idx, err := g.emitExpr(arg.Value)
	if err != nil {
		return value{}, err
	}
	idx, err = g.loadIfPointer(idx)
	if err != nil {
		return value{}, err
	}
	if idx.typ != "i64" {
		return value{}, unsupportedf("type-system", "list.get index type %s, want i64", idx.typ)
	}

	g.declareRuntimeSymbol(listRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
	runtimeRet := elemTyp
	if !scalar {
		runtimeRet = "ptr"
	}
	getSym := listRuntimeGetSymbol(runtimeRet)
	g.declareRuntimeSymbol(getSym, runtimeRet, []paramInfo{{typ: "ptr"}, {typ: "i64"}})

	emitter := g.toOstyEmitter()
	lenVal := llvmCall(emitter, "i64", listRuntimeLenSymbol(), []*LlvmValue{toOstyValue(base)})
	geq0 := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpSGEI64ZeroText(geq0, idx.ref))
	ltlen := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpSLTI64Text(ltlen, idx.ref, lenVal.name))
	inBounds := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAndI1Text(inBounds, geq0, ltlen))

	inLabel := llvmNextLabel(emitter, "list.get.in")
	outLabel := llvmNextLabel(emitter, "list.get.out")
	endLabel := llvmNextLabel(emitter, "list.get.end")
	emitter.body = append(emitter.body, mirBrCondText(inBounds, inLabel, outLabel))

	emitter.body = append(emitter.body, mirLabelText(inLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(inLabel)

	emitter = g.toOstyEmitter()
	var someRef string
	if scalar {
		// Scalar-V box: read i64/i1/double/…, GC-alloc byteSize,
		// store the scalar, yield the box ptr. Kind=1 (generic) to
		// match the Map.get path (no managed pointers inside).
		got := llvmCall(emitter, elemTyp, getSym, []*LlvmValue{toOstyValue(base), toOstyValue(idx)})
		site := "list.get.box." + elemTyp
		box := llvmGcAlloc(emitter, 1, byteSize, site)
		emitter.body = append(emitter.body, mirStoreText(elemTyp, got.name, box.name))
		someRef = box.name
		g.needsGCRuntime = true
	} else {
		// ptr-backed: the runtime already returns a ptr; None = null.
		got := llvmCall(emitter, "ptr", getSym, []*LlvmValue{toOstyValue(base), toOstyValue(idx)})
		someRef = got.name
	}
	inPred := g.currentBlock
	emitter.body = append(emitter.body, mirBrUncondText(endLabel))
	emitter.body = append(emitter.body, mirLabelText(outLabel))
	emitter.body = append(emitter.body, mirBrUncondText(endLabel))
	emitter.body = append(emitter.body, mirLabelText(endLabel))
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirPhiPtrFromValueOrNullText(tmp, someRef, inPred, outLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)

	out := value{typ: "ptr", ref: tmp, gcManaged: true}
	out.rootPaths = g.rootPathsForType("ptr")
	_ = elemString
	return out, nil
}

// listGetBoxByteSize reports the heap-box size for a scalar element
// type that list.get(i) must wrap into an Option ptr. The second
// return is `scalar=true` when the element needs boxing; when it's
// already ptr-backed (element = ptr), the caller routes to the
// direct null=None / non-null=Some phi instead. The third return is
// `ok=false` for element types the backend does not yet know how to
// box (anything outside i1 / i8 / i32 / i64 / double / ptr). Delegates
// to the Osty-sourced `mirListGetBoxByteSize`
// (`toolchain/mir_generator.osty`); the Osty side returns the size as a
// single int with a `-1` sentinel for the unsupported case so we can
// reconstruct `(size, scalar, ok)` from the integer alone.
func listGetBoxByteSize(elemTyp string) (int, bool, bool) {
	code := mirListGetBoxByteSize(elemTyp)
	if code < 0 {
		return 0, false, false
	}
	if code == 0 {
		return 0, false, true
	}
	return code, true, true
}

func (g *generator) emitMapMethodCall(call *ast.CallExpr) (value, bool, error) {
	field, keyTyp, _, keyString, found := g.mapMethodInfo(call)
	if !found {
		return value{}, false, nil
	}
	if preferSpecialized, err := g.specializedBuiltinUserMethodAvailable(call); err != nil {
		return value{}, true, err
	} else if preferSpecialized {
		return value{}, false, nil
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	switch field.Name {
	case "len":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "map.len requires no arguments")
		}
		g.declareRuntimeSymbol(mapRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		out := llvmCall(emitter, "i64", mapRuntimeLenSymbol(), []*LlvmValue{toOstyValue(base)})
		g.takeOstyEmitter(emitter)
		return fromOstyValue(out), true, nil
	case "isEmpty":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "map.isEmpty requires no arguments")
		}
		g.declareRuntimeSymbol(mapRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		lenVal := llvmCall(emitter, "i64", mapRuntimeLenSymbol(), []*LlvmValue{toOstyValue(base)})
		cmp := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirICmpEqI64LiteralText(cmp, lenVal.name, "0"))
		g.takeOstyEmitter(emitter)
		return value{typ: "i1", ref: cmp}, true, nil
	case "get":
		return g.emitMapGet(call, base, keyTyp, keyString)
	case "getOr":
		return g.emitMapGetOr(call, base, keyTyp, keyString)
	case "getOrInsert":
		return g.emitMapGetOrInsert(call, base, keyTyp, keyString)
	case "getOrInsertWith":
		return g.emitMapGetOrInsertWith(call, base, keyTyp, keyString)
	case "mergeWith":
		return g.emitMapMergeWith(call, base, keyTyp, keyString)
	case "mapValues":
		return g.emitMapMapValues(call, base, keyTyp, keyString)
	case "containsKey":
		if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupported("call", "map.containsKey requires one positional argument")
		}
		keySource, _, _ := g.iterableMapSourceTypes(field.X)
		key, err := g.emitExprWithSourceType(call.Args[0].Value, keySource)
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
		keySource, _, _ := g.iterableMapSourceTypes(field.X)
		key, err := g.emitExprWithSourceType(call.Args[0].Value, keySource)
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

// mapScalarValueByteSize returns the GC-alloc byte size for a scalar V
// box used by `Map.get` / `Map.getOr` when V is not already ptr. Only
// the set of V types supported by the map-key runtime macros is
// recognised; anything else routes to the unsupported diagnostic.
// Delegates to the Osty-sourced `mirMapScalarValueByteSize`
// (`toolchain/mir_generator.osty`); the Osty side returns `0` for
// unsupported types so the Go wrapper rebuilds the bool from `> 0`.
func mapScalarValueByteSize(valTyp string) (int, bool) {
	size := mirMapScalarValueByteSize(valTyp)
	return size, size > 0
}

// emitMapGet lowers `m.get(key) -> V?` as the real Option-returning
// intrinsic. This replaces the "contains + get_or_abort" special cases
// that every bodied helper used to reimplement, so once `get` lands,
// stdlib bodies like `self.get(key) ?? default` (getOr) and
// `self.get(key).isSome()` (containsKey) can compose naturally.
//
// ABI: the C runtime helper is
//
//	bool osty_rt_map_get_<K>(void *map, K key, void *out_slot)
//
// It returns true and memcpys V into *out_slot when the key is present;
// otherwise returns false and leaves *out_slot alone.
//
// Option<V> at the LLVM layer is always a ptr:
//   - V = ptr  →  the map value slot holds a pointer; we pre-zero the
//     slot and rely on `load ptr` producing null on miss and
//     the stored payload on hit. No branch needed.
//   - V = scalar (i64 / i1 / double) →  GC-alloc a box in the present
//     branch and phi ptr-to-box vs null. Matches the
//     boxed-Option ABI consumed by `??`.
func (g *generator) emitMapGet(call *ast.CallExpr, base value, keyTyp string, keyString bool) (value, bool, error) {
	if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, true, unsupported("call", "map.get requires one positional argument")
	}
	var keySource ast.Type
	if field, ok := fieldExprOfCallFn(call); ok {
		keySource, _, _ = g.iterableMapSourceTypes(field.X)
	}
	key, err := g.emitExprWithSourceType(call.Args[0].Value, keySource)
	if err != nil {
		return value{}, true, err
	}
	if key.typ != keyTyp {
		return value{}, true, unsupportedf("type-system", "map.get key type %s, want %s", key.typ, keyTyp)
	}
	loadedKey, err := g.loadIfPointer(key)
	if err != nil {
		return value{}, true, err
	}
	out, err := g.emitMapGetCore(base, loadedKey, keyTyp, keyString)
	if err != nil {
		return value{}, true, err
	}
	return out, true, nil
}

// emitMapGetCore is the pre-emitted-args variant of emitMapGet. Used
// by getOr/update which already lowered the base and key and need to
// reuse the same Option<V> materialisation without re-evaluating the
// receiver expression.
func (g *generator) emitMapGetCore(base value, loadedKey value, keyTyp string, keyString bool) (value, error) {
	getSym := mapRuntimeGetSymbol(keyTyp, keyString)
	g.declareRuntimeSymbol(getSym, "i1", []paramInfo{{typ: "ptr"}, {typ: keyTyp}, {typ: "ptr"}})

	valTyp := base.mapValueTyp
	if valTyp == "ptr" {
		// Fast path: V is already ptr. Pre-zero the slot so a miss
		// produces null (= None), a hit overwrites with the payload
		// ptr (= Some). No branch needed.
		emitter := g.toOstyEmitter()
		slot := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirAllocaPtrText(slot))
		emitter.body = append(emitter.body, mirStorePtrNullText(slot))
		emitter.body = append(emitter.body, mirCallI1FromArgsText(
			llvmNextTemp(emitter),
			getSym,
			llvmCallArgs([]*LlvmValue{toOstyValue(base), toOstyValue(loadedKey), {typ: "ptr", name: slot}}),
		))
		out := g.loadValueFromAddress(emitter, "ptr", slot)
		g.takeOstyEmitter(emitter)
		out.gcManaged = true
		out.rootPaths = g.rootPathsForType("ptr")
		return out, nil
	}

	byteSize, ok := mapScalarValueByteSize(valTyp)
	if !ok {
		return value{}, unsupportedf(
			"call",
			"map.get on Map<%s, %s>: Option<%s> lowering not yet wired for this V (scalar V supports i64/i1/double; V=ptr is direct)",
			keyTyp, valTyp, valTyp,
		)
	}

	// Scalar V: alloca temp slot, call helper, branch on present.
	//   present=true  → GC-alloc a V-sized box, copy slot→box, Some = box ptr
	//   present=false → null ptr (None)
	// Merged via phi at end; result is the boxed-Option ptr consumed
	// unchanged by ?? / match / .isSome().
	emitter := g.toOstyEmitter()
	slot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAllocaText(slot, valTyp))
	present := llvmCall(emitter, "i1", getSym, []*LlvmValue{
		toOstyValue(base),
		toOstyValue(loadedKey),
		{typ: "ptr", name: slot},
	})
	labels := llvmIfExprStart(emitter, present)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.thenLabel

	// Present branch: GC-alloc box + copy payload.
	emitter = g.toOstyEmitter()
	box := llvmGcAlloc(emitter, 1, byteSize, "map.get.box."+valTyp)
	payload := llvmLoad(emitter, &LlvmValue{typ: valTyp, name: slot, pointer: true})
	emitter.body = append(emitter.body, mirStoreText(valTyp, payload.name, box.name))
	g.takeOstyEmitter(emitter)
	g.needsGCRuntime = true
	someVal := value{typ: "ptr", ref: box.name, gcManaged: true}
	thenPred := g.currentBlock

	// Absent branch: null ptr.
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel
	noneVal := value{typ: "ptr", ref: "null"}
	elsePred := g.currentBlock

	out, err := g.emitIfExprPhi(labels, thenPred, elsePred, someVal, noneVal)
	if err != nil {
		return value{}, err
	}
	out.gcManaged = true
	out.rootPaths = g.rootPathsForType("ptr")
	return out, nil
}

// emitMapIterate emits a snapshot-based walk: it first calls
// `osty_rt_map_keys(m)` to freeze the K set at the moment of the call
// (that runtime helper copies all current keys into a fresh List<K>
// under the per-map lock), then iterates that list, calling
// `m.get(k)` per key to fetch the current V. If the key was removed
// by a concurrent mutator since the snapshot, get returns None and
// the body is skipped; if the value was replaced, body sees the new
// V. This is the weakly-consistent iteration semantics (a la Go's
// sync.Map.Range): no out-of-bounds panic under mutation, no
// guarantee of seeing entries added after the snapshot.
//
// `labelPrefix` keeps temp names distinct when callers compose
// multiple iterations back-to-back. body MUST handle the case where
// iterating over self while self is mutated (e.g. retainIf collects
// victim keys and defers removal to a second pass).
func (g *generator) emitMapIterate(mapVal value, keyTyp, valTyp string, keyString bool, labelPrefix string, body func(k, v value) error) error {
	// Snapshot keys under the per-map lock — this returns a
	// freshly-allocated List<K>.
	g.declareRuntimeSymbol(mapRuntimeKeysSymbol(), "ptr", []paramInfo{{typ: "ptr"}})
	mapLoaded, err := g.loadIfPointer(mapVal)
	if err != nil {
		return err
	}
	emitter := g.toOstyEmitter()
	keysList := llvmCall(emitter, "ptr", mapRuntimeKeysSymbol(), []*LlvmValue{toOstyValue(mapLoaded)})
	g.takeOstyEmitter(emitter)
	keysVal := fromOstyValue(keysList)
	keysVal.gcManaged = true
	keysVal.listElemTyp = keyTyp
	keysVal.listElemString = keyString
	keysVal = g.protectManagedTemporary(labelPrefix+".keys", keysVal)

	g.declareRuntimeSymbol(listRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
	keysLoaded, err := g.loadIfPointer(keysVal)
	if err != nil {
		return err
	}
	emitter = g.toOstyEmitter()
	keysLen := llvmCall(emitter, "i64", listRuntimeLenSymbol(), []*LlvmValue{toOstyValue(keysLoaded)})
	loop := llvmRangeStart(emitter, labelPrefix+"_i", llvmIntLiteral(0), keysLen, false)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.bodyLabel)
	cont := g.nextNamedLabel(labelPrefix + ".cont")
	g.pushLoop(loopContext{continueLabel: cont, breakLabel: loop.endLabel, scopeDepth: len(g.locals)})

	keysLoaded, err = g.loadIfPointer(keysVal)
	if err != nil {
		g.popLoop()
		return err
	}
	mapLoaded, err = g.loadIfPointer(mapVal)
	if err != nil {
		g.popLoop()
		return err
	}

	// k = keysList[i]
	listGetSym := listRuntimeGetSymbol(keyTyp)
	g.declareRuntimeSymbol(listGetSym, keyTyp, []paramInfo{{typ: "ptr"}, {typ: "i64"}})
	emitter = g.toOstyEmitter()
	kCall := llvmCall(emitter, keyTyp, listGetSym, []*LlvmValue{toOstyValue(keysLoaded), llvmI64(loop.current)})
	g.takeOstyEmitter(emitter)
	kVal := fromOstyValue(kCall)
	kVal.gcManaged = keyTyp == "ptr"
	kVal.rootPaths = g.rootPathsForType(keyTyp)

	// opt_v = map.get(k) — returns Option<V> (ptr). If null, the key
	// was removed between snapshot and here; skip the body.
	kLoaded, err := g.loadIfPointer(kVal)
	if err != nil {
		g.popLoop()
		return err
	}
	optV, err := g.emitMapGetCore(mapVal, kLoaded, keyTyp, keyString)
	if err != nil {
		g.popLoop()
		return err
	}
	// if opt is null → skip; else → unwrap and run body.
	emitter = g.toOstyEmitter()
	isNil := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpEqPtrNullText(isNil, optV.ref))
	labels := llvmIfExprStart(emitter, &LlvmValue{typ: "i1", name: isNil})
	g.takeOstyEmitter(emitter)
	// then = none (skip)
	g.currentBlock = labels.thenLabel
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	// else = some (body)
	g.currentBlock = labels.elseLabel
	var vUnwrapped value
	if valTyp == "ptr" {
		vUnwrapped = optV
	} else {
		emitter = g.toOstyEmitter()
		payload := llvmLoad(emitter, &LlvmValue{typ: valTyp, name: optV.ref, pointer: true})
		g.takeOstyEmitter(emitter)
		vUnwrapped = value{typ: valTyp, ref: payload.name}
	}
	vUnwrapped.gcManaged = valTyp == "ptr"
	vUnwrapped.rootPaths = g.rootPathsForType(valTyp)

	if err := body(kVal, vUnwrapped); err != nil {
		g.popLoop()
		return err
	}

	// Merge branches at labels.endLabel.
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirBrUncondText(labels.endLabel))
	emitter.body = append(emitter.body, mirLabelText(labels.endLabel))
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.endLabel

	g.popLoop()
	if g.currentReachable {
		g.branchTo(cont)
	}
	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, mirLabelText(cont))
	g.emitGCSafepointKind(emitter, safepointKindLoop)
	llvmRangeEnd(emitter, loop)
	g.takeOstyEmitter(emitter)
	g.enterBlock(loop.endLabel)
	return nil
}

// emitMapNewFor constructs an empty map with the given K/V types,
// wiring the ABI kind tags + value size + GC trace callback exactly
// like the `{:}` literal path.
func (g *generator) emitMapNewFor(keyTyp, valTyp string, keyString bool) (value, error) {
	traceSymbol := g.traceCallbackSymbol(valTyp, g.rootPathsForType(valTyp))
	g.declareRuntimeSymbol(mapRuntimeNewSymbol(), "ptr", []paramInfo{{typ: "i64"}, {typ: "i64"}, {typ: "i64"}, {typ: "ptr"}})
	emitter := g.toOstyEmitter()
	valueSize := g.emitTypeSize(emitter, valTyp)
	out := llvmCall(emitter, "ptr", mapRuntimeNewSymbol(), []*LlvmValue{
		llvmI64(strconv.Itoa(containerAbiKind(keyTyp, keyString))),
		llvmI64(strconv.Itoa(containerAbiKind(valTyp, false))),
		valueSize,
		{typ: "ptr", name: llvmPointerOperand(traceSymbol)},
	})
	g.takeOstyEmitter(emitter)
	mapValue := fromOstyValue(out)
	mapValue.gcManaged = true
	mapValue.mapKeyTyp = keyTyp
	mapValue.mapValueTyp = valTyp
	mapValue.mapKeyString = keyString
	mapValue.rootPaths = g.rootPathsForType("ptr")
	return mapValue, nil
}

// emitMapMergeWith lowers `m.mergeWith(other, combine) -> Map<K, V>`.
// Stdlib bodied form: `out = {:}; copy self; for other entries either
// insert or combine(existing, v) + insert`. All three operations
// compose on the intrinsic stack (map iter + map get + map insert +
// fn-value indirect call).
func (g *generator) emitMapMergeWith(call *ast.CallExpr, base value, keyTyp string, keyString bool) (value, bool, error) {
	if len(call.Args) != 2 ||
		call.Args[0].Name != "" || call.Args[1].Name != "" ||
		call.Args[0].Value == nil || call.Args[1].Value == nil {
		return value{}, true, unsupported("call", "map.mergeWith requires two positional arguments")
	}
	valTyp := base.mapValueTyp
	other, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return value{}, true, err
	}
	if other.typ != "ptr" || other.mapKeyTyp != keyTyp || other.mapValueTyp != valTyp {
		return value{}, true, unsupportedf("type-system", "map.mergeWith 'other' map types don't match receiver")
	}
	combine, err := g.emitExpr(call.Args[1].Value)
	if err != nil {
		return value{}, true, err
	}
	combineHeld, sig, err := g.protectFnValueCallback("mergewith.combine", combine, "map.mergeWith combine")
	if err != nil {
		return value{}, true, err
	}
	if len(sig.params) != 2 || sig.ret != valTyp {
		return value{}, true, unsupportedf("call", "map.mergeWith combine must be fn(V, V) -> V")
	}

	base = g.protectManagedTemporary("mergewith.self", base)
	other = g.protectManagedTemporary("mergewith.other", other)

	outMap, err := g.emitMapNewFor(keyTyp, valTyp, keyString)
	if err != nil {
		return value{}, true, err
	}
	outMap = g.protectManagedTemporary("mergewith.out", outMap)

	// Pass 1: copy every (k, v) from self into out.
	if err := g.emitMapIterate(base, keyTyp, valTyp, keyString, "mw1", func(k, v value) error {
		return g.emitMapInsert(outMap, k, v)
	}); err != nil {
		return value{}, true, err
	}

	// Pass 2: iterate other; if key exists in out call combine.
	err = g.emitMapIterate(other, keyTyp, valTyp, keyString, "mw2", func(k, v value) error {
		loadedKey, err := g.loadIfPointer(k)
		if err != nil {
			return err
		}
		existingOpt, err := g.emitMapGetCore(outMap, loadedKey, keyTyp, keyString)
		if err != nil {
			return err
		}
		// branch on opt null-ness
		emitter := g.toOstyEmitter()
		isNil := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirICmpEqPtrNullText(isNil, existingOpt.ref))
		labels := llvmIfExprStart(emitter, &LlvmValue{typ: "i1", name: isNil})
		g.takeOstyEmitter(emitter)

		// then = no existing: insert v directly
		g.currentBlock = labels.thenLabel
		if err := g.emitMapInsert(outMap, k, v); err != nil {
			return err
		}
		emitter = g.toOstyEmitter()
		llvmIfExprElse(emitter, labels)
		g.takeOstyEmitter(emitter)

		// else = existing: combine(existing, v) → insert
		g.currentBlock = labels.elseLabel
		var existing value
		if valTyp == "ptr" {
			existing = existingOpt
		} else {
			emitter = g.toOstyEmitter()
			payload := llvmLoad(emitter, &LlvmValue{typ: valTyp, name: existingOpt.ref, pointer: true})
			g.takeOstyEmitter(emitter)
			existing = value{typ: valTyp, ref: payload.name}
		}
		combined, err := g.emitProtectedFnValueCall(combineHeld, sig, []*LlvmValue{
			{typ: valTyp, name: existing.ref},
			{typ: valTyp, name: v.ref},
		})
		if err != nil {
			return err
		}
		if err := g.emitMapInsert(outMap, k, combined); err != nil {
			return err
		}
		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, mirBrUncondText(labels.endLabel))
		emitter.body = append(emitter.body, mirLabelText(labels.endLabel))
		g.takeOstyEmitter(emitter)
		g.currentBlock = labels.endLabel
		return nil
	})
	if err != nil {
		return value{}, true, err
	}

	outLoaded, err := g.loadIfPointer(outMap)
	if err != nil {
		return value{}, true, err
	}
	return outLoaded, true, nil
}

// emitMapMapValues lowers `m.mapValues(f) -> Map<K, R>` where
// R = f's return type. Builds a fresh Map<K, R> and inserts f(v) per
// entry. Simpler than mergeWith because there's no per-key combine —
// each entry goes through f once.
func (g *generator) emitMapMapValues(call *ast.CallExpr, base value, keyTyp string, keyString bool) (value, bool, error) {
	if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
		return value{}, true, unsupported("call", "map.mapValues requires one positional argument")
	}
	f, err := g.emitExpr(call.Args[0].Value)
	if err != nil {
		return value{}, true, err
	}
	fHeld, sig, err := g.protectFnValueCallback("mapvalues.f", f, "map.mapValues f")
	if err != nil {
		return value{}, true, err
	}
	if len(sig.params) != 1 || sig.params[0].typ != base.mapValueTyp {
		return value{}, true, unsupportedf("call", "map.mapValues f must take single V argument")
	}
	rTyp := sig.ret
	if rTyp == "" {
		return value{}, true, unsupportedf("call", "map.mapValues f must return a value")
	}

	base = g.protectManagedTemporary("mapvalues.self", base)

	outMap, err := g.emitMapNewFor(keyTyp, rTyp, keyString)
	if err != nil {
		return value{}, true, err
	}
	outMap = g.protectManagedTemporary("mapvalues.out", outMap)

	err = g.emitMapIterate(base, keyTyp, base.mapValueTyp, keyString, "mv", func(k, v value) error {
		rVal, err := g.emitProtectedFnValueCall(fHeld, sig, []*LlvmValue{{typ: v.typ, name: v.ref}})
		if err != nil {
			return err
		}
		return g.emitMapInsert(outMap, k, rVal)
	})
	if err != nil {
		return value{}, true, err
	}

	outLoaded, err := g.loadIfPointer(outMap)
	if err != nil {
		return value{}, true, err
	}
	return outLoaded, true, nil
}

// emitMapGetOr lowers `m.getOr(key, default) -> V` as the stdlib bodied
// form `self.get(key) ?? default`. It composes the real `get` intrinsic
// (Option<V>) with the ptr-backed coalesce shape: icmp eq ptr null →
// branch → phi. For scalar V the some branch also loads the payload
// out of a stack slot so hot getOr loops avoid allocating an Option box
// per successful lookup.
func (g *generator) emitMapGetOr(call *ast.CallExpr, base value, keyTyp string, keyString bool) (value, bool, error) {
	if len(call.Args) != 2 ||
		call.Args[0].Name != "" || call.Args[1].Name != "" ||
		call.Args[0].Value == nil || call.Args[1].Value == nil {
		return value{}, true, unsupported("call", "map.getOr requires two positional arguments")
	}
	var keySource, valueSource ast.Type
	if field, ok := fieldExprOfCallFn(call); ok {
		keySource, valueSource, _ = g.iterableMapSourceTypes(field.X)
	}
	key, err := g.emitExprWithSourceType(call.Args[0].Value, keySource)
	if err != nil {
		return value{}, true, err
	}
	if key.typ != keyTyp {
		return value{}, true, unsupportedf("type-system", "map.getOr key type %s, want %s", key.typ, keyTyp)
	}
	loadedKey, err := g.loadIfPointer(key)
	if err != nil {
		return value{}, true, err
	}

	def, err := g.emitExprWithSourceType(call.Args[1].Value, valueSource)
	if err != nil {
		return value{}, true, err
	}
	valTyp := base.mapValueTyp
	if def.typ != valTyp {
		return value{}, true, unsupportedf("type-system", "map.getOr default type %s, want %s", def.typ, valTyp)
	}
	if valTyp != "ptr" {
		getSym := mapRuntimeGetSymbol(keyTyp, keyString)
		g.declareRuntimeSymbol(getSym, "i1", []paramInfo{{typ: "ptr"}, {typ: keyTyp}, {typ: "ptr"}})

		emitter := g.toOstyEmitter()
		slot := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirAllocaText(slot, valTyp))
		present := llvmCall(emitter, "i1", getSym, []*LlvmValue{
			toOstyValue(base),
			toOstyValue(loadedKey),
			{typ: "ptr", name: slot},
		})
		labels := llvmIfExprStart(emitter, present)
		g.takeOstyEmitter(emitter)

		// then = hit: load the scalar payload from the temp slot.
		g.currentBlock = labels.thenLabel
		emitter = g.toOstyEmitter()
		payload := llvmLoad(emitter, &LlvmValue{typ: valTyp, name: slot, pointer: true})
		g.takeOstyEmitter(emitter)
		hitPred := g.currentBlock
		hitVal := value{typ: valTyp, ref: payload.name}

		// else = miss: fall back to the default.
		emitter = g.toOstyEmitter()
		llvmIfExprElse(emitter, labels)
		g.takeOstyEmitter(emitter)
		g.currentBlock = labels.elseLabel
		missPred := g.currentBlock
		missVal := def

		out, err := g.emitIfExprPhi(labels, hitPred, missPred, hitVal, missVal)
		if err != nil {
			return value{}, true, err
		}
		return out, true, nil
	}

	optVal, err := g.emitMapGetCore(base, loadedKey, keyTyp, keyString)
	if err != nil {
		return value{}, true, err
	}

	emitter := g.toOstyEmitter()
	isNil := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpEqPtrNullText(isNil, optVal.ref))
	labels := llvmIfExprStart(emitter, &LlvmValue{typ: "i1", name: isNil})
	g.takeOstyEmitter(emitter)

	// then = none: fall back to default.
	g.currentBlock = labels.thenLabel
	nonePred := g.currentBlock
	noneVal := def

	// else = some: for V=ptr the opt value IS the payload; for scalar
	// V we load it out of the GC box.
	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel
	var someVal value
	if valTyp == "ptr" {
		someVal = optVal
	} else {
		emitter = g.toOstyEmitter()
		payload := llvmLoad(emitter, &LlvmValue{typ: valTyp, name: optVal.ref, pointer: true})
		g.takeOstyEmitter(emitter)
		someVal = value{typ: valTyp, ref: payload.name}
	}
	somePred := g.currentBlock

	out, err := g.emitIfExprPhi(labels, nonePred, somePred, noneVal, someVal)
	if err != nil {
		return value{}, true, err
	}
	out.gcManaged = valTyp == "ptr"
	out.rootPaths = g.rootPathsForType(valTyp)
	return out, true, nil
}

// emitMapGetOrInsert lowers `m.getOrInsert(key, default) -> V`.
// The default operand follows normal call semantics: it is evaluated
// before the helper executes, even when the key is already present.
func (g *generator) emitMapGetOrInsert(call *ast.CallExpr, base value, keyTyp string, keyString bool) (value, bool, error) {
	if len(call.Args) != 2 ||
		call.Args[0].Name != "" || call.Args[1].Name != "" ||
		call.Args[0].Value == nil || call.Args[1].Value == nil {
		return value{}, true, unsupported("call", "map.getOrInsert requires two positional arguments")
	}
	var keySource, valueSource ast.Type
	if field, ok := fieldExprOfCallFn(call); ok {
		keySource, valueSource, _ = g.iterableMapSourceTypes(field.X)
	}
	key, err := g.emitExprWithSourceType(call.Args[0].Value, keySource)
	if err != nil {
		return value{}, true, err
	}
	if key.typ != keyTyp {
		return value{}, true, unsupportedf("type-system", "map.getOrInsert key type %s, want %s", key.typ, keyTyp)
	}
	loadedKey, err := g.loadIfPointer(key)
	if err != nil {
		return value{}, true, err
	}
	def, err := g.emitExprWithSourceType(call.Args[1].Value, valueSource)
	if err != nil {
		return value{}, true, err
	}
	valTyp := base.mapValueTyp
	if def.typ != valTyp {
		return value{}, true, unsupportedf("type-system", "map.getOrInsert default type %s, want %s", def.typ, valTyp)
	}
	return g.emitMapGetOrInsertValue(base, value{typ: keyTyp, ref: loadedKey.ref}, def, keyTyp, keyString)
}

// emitMapGetOrInsertWith lowers `m.getOrInsertWith(key, make) -> V`.
// The supplier is invoked only on misses and the lookup/insert sequence
// runs under the same per-map lock used by `Map.update`.
func (g *generator) emitMapGetOrInsertWith(call *ast.CallExpr, base value, keyTyp string, keyString bool) (value, bool, error) {
	if len(call.Args) != 2 ||
		call.Args[0].Name != "" || call.Args[1].Name != "" ||
		call.Args[0].Value == nil || call.Args[1].Value == nil {
		return value{}, true, unsupported("call", "map.getOrInsertWith requires two positional arguments")
	}
	var keySource ast.Type
	if field, ok := fieldExprOfCallFn(call); ok {
		keySource, _, _ = g.iterableMapSourceTypes(field.X)
	}
	key, err := g.emitExprWithSourceType(call.Args[0].Value, keySource)
	if err != nil {
		return value{}, true, err
	}
	if key.typ != keyTyp {
		return value{}, true, unsupportedf("type-system", "map.getOrInsertWith key type %s, want %s", key.typ, keyTyp)
	}
	loadedKey, err := g.loadIfPointer(key)
	if err != nil {
		return value{}, true, err
	}
	makeVal, err := g.emitExpr(call.Args[1].Value)
	if err != nil {
		return value{}, true, err
	}
	makeHeld, sig, err := g.protectFnValueCallback("getorinsertwith.make", makeVal, "map.getOrInsertWith callback")
	if err != nil {
		return value{}, true, err
	}
	if len(sig.params) != 0 || sig.ret != base.mapValueTyp {
		return value{}, true, unsupportedf("call", "map.getOrInsertWith make must be fn() -> V")
	}
	return g.emitMapGetOrInsertWithCallback(base, value{typ: keyTyp, ref: loadedKey.ref}, makeHeld, sig, keyTyp, keyString)
}

func (g *generator) emitMapGetOrInsertValue(base value, loadedKey value, missValue value, keyTyp string, keyString bool) (value, bool, error) {
	valTyp := base.mapValueTyp
	if err := g.emitMapLock(base); err != nil {
		return value{}, true, err
	}
	var out value
	var err error
	if valTyp != "ptr" {
		out, err = g.emitMapGetOrInsertScalar(base, loadedKey, missValue, keyTyp, keyString)
	} else {
		out, err = g.emitMapGetOrInsertPtr(base, loadedKey, func() (value, error) {
			return missValue, nil
		}, keyTyp, keyString)
	}
	if err != nil {
		return value{}, true, err
	}
	if err := g.emitMapUnlock(base); err != nil {
		return value{}, true, err
	}
	out.gcManaged = valTyp == "ptr"
	out.rootPaths = g.rootPathsForType(valTyp)
	return out, true, nil
}

func (g *generator) emitMapGetOrInsertWithCallback(base value, loadedKey value, makeHeld value, sig *fnSig, keyTyp string, keyString bool) (value, bool, error) {
	valTyp := base.mapValueTyp
	if err := g.emitMapLock(base); err != nil {
		return value{}, true, err
	}
	var out value
	var err error
	if valTyp != "ptr" {
		out, err = g.emitMapGetOrInsertScalarWithCallback(base, loadedKey, makeHeld, sig, keyTyp, keyString)
	} else {
		out, err = g.emitMapGetOrInsertPtr(base, loadedKey, func() (value, error) {
			return g.emitProtectedFnValueCall(makeHeld, sig, nil)
		}, keyTyp, keyString)
	}
	if err != nil {
		return value{}, true, err
	}
	if err := g.emitMapUnlock(base); err != nil {
		return value{}, true, err
	}
	out.gcManaged = valTyp == "ptr"
	out.rootPaths = g.rootPathsForType(valTyp)
	return out, true, nil
}

func (g *generator) emitMapGetOrInsertScalar(base value, loadedKey value, missValue value, keyTyp string, keyString bool) (value, error) {
	valTyp := base.mapValueTyp
	getSym := mapRuntimeGetSymbol(keyTyp, keyString)
	g.declareRuntimeSymbol(getSym, "i1", []paramInfo{{typ: "ptr"}, {typ: keyTyp}, {typ: "ptr"}})

	emitter := g.toOstyEmitter()
	slot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAllocaText(slot, valTyp))
	present := llvmCall(emitter, "i1", getSym, []*LlvmValue{
		toOstyValue(base),
		toOstyValue(loadedKey),
		{typ: "ptr", name: slot},
	})
	labels := llvmIfExprStart(emitter, present)
	g.takeOstyEmitter(emitter)

	g.currentBlock = labels.thenLabel
	emitter = g.toOstyEmitter()
	payload := llvmLoad(emitter, &LlvmValue{typ: valTyp, name: slot, pointer: true})
	g.takeOstyEmitter(emitter)
	hitPred := g.currentBlock
	hitVal := value{typ: valTyp, ref: payload.name}

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel
	if err := g.emitMapInsert(base, loadedKey, missValue); err != nil {
		return value{}, err
	}
	missPred := g.currentBlock
	return g.emitIfExprPhi(labels, hitPred, missPred, hitVal, missValue)
}

func (g *generator) emitMapGetOrInsertScalarWithCallback(base value, loadedKey value, makeHeld value, sig *fnSig, keyTyp string, keyString bool) (value, error) {
	valTyp := base.mapValueTyp
	getSym := mapRuntimeGetSymbol(keyTyp, keyString)
	g.declareRuntimeSymbol(getSym, "i1", []paramInfo{{typ: "ptr"}, {typ: keyTyp}, {typ: "ptr"}})

	emitter := g.toOstyEmitter()
	slot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirAllocaText(slot, valTyp))
	present := llvmCall(emitter, "i1", getSym, []*LlvmValue{
		toOstyValue(base),
		toOstyValue(loadedKey),
		{typ: "ptr", name: slot},
	})
	labels := llvmIfExprStart(emitter, present)
	g.takeOstyEmitter(emitter)

	g.currentBlock = labels.thenLabel
	emitter = g.toOstyEmitter()
	payload := llvmLoad(emitter, &LlvmValue{typ: valTyp, name: slot, pointer: true})
	g.takeOstyEmitter(emitter)
	hitPred := g.currentBlock
	hitVal := value{typ: valTyp, ref: payload.name}

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel
	missValue, err := g.emitProtectedFnValueCall(makeHeld, sig, nil)
	if err != nil {
		return value{}, err
	}
	if missValue.typ != valTyp {
		return value{}, unsupportedf("type-system", "map.getOrInsertWith make return %s, want %s", missValue.typ, valTyp)
	}
	if err := g.emitMapInsert(base, loadedKey, missValue); err != nil {
		return value{}, err
	}
	missPred := g.currentBlock
	return g.emitIfExprPhi(labels, hitPred, missPred, hitVal, missValue)
}

func (g *generator) emitMapGetOrInsertPtr(base value, loadedKey value, miss func() (value, error), keyTyp string, keyString bool) (value, error) {
	optVal, err := g.emitMapGetCore(base, loadedKey, keyTyp, keyString)
	if err != nil {
		return value{}, err
	}

	emitter := g.toOstyEmitter()
	isNil := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpEqPtrNullText(isNil, optVal.ref))
	labels := llvmIfExprStart(emitter, &LlvmValue{typ: "i1", name: isNil})
	g.takeOstyEmitter(emitter)

	g.currentBlock = labels.thenLabel
	missValue, err := miss()
	if err != nil {
		return value{}, err
	}
	if missValue.typ != base.mapValueTyp {
		return value{}, unsupportedf("type-system", "map.getOrInsert miss value %s, want %s", missValue.typ, base.mapValueTyp)
	}
	if err := g.emitMapInsert(base, loadedKey, missValue); err != nil {
		return value{}, err
	}
	missPred := g.currentBlock

	emitter = g.toOstyEmitter()
	llvmIfExprElse(emitter, labels)
	g.takeOstyEmitter(emitter)
	g.currentBlock = labels.elseLabel
	hitPred := g.currentBlock

	return g.emitIfExprPhi(labels, missPred, hitPred, missValue, optVal)
}

func (g *generator) emitMapLock(base value) error {
	g.declareRuntimeSymbol(mapRuntimeLockSymbol(), "void", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
		mapRuntimeLockSymbol(),
		llvmCallArgs([]*LlvmValue{toOstyValue(base)}),
	))
	g.takeOstyEmitter(emitter)
	return nil
}

func (g *generator) emitMapUnlock(base value) error {
	g.declareRuntimeSymbol(mapRuntimeUnlockSymbol(), "void", []paramInfo{{typ: "ptr"}})
	emitter := g.toOstyEmitter()
	emitter.body = append(emitter.body, mirCallRuntimeVoidOneArgText(
		mapRuntimeUnlockSymbol(),
		llvmCallArgs([]*LlvmValue{toOstyValue(base)}),
	))
	g.takeOstyEmitter(emitter)
	return nil
}

// emitOptionMethodCall lowers `opt.isSome()` and `opt.isNone()` as a
// ptr-null check when `opt` statically resolves to an Option<T>
// value (i.e. the receiver's source type is `*ast.OptionalType`, or
// the expression returns one — e.g. `self.get(k).isSome()` inside
// a specialized Map method body, where `self.get(k)` advertises
// `V?` as its source type via staticMapMethodSourceType).
//
// This is the Phase 2f intrinsic that lets `Map.containsKey`'s
// stdlib body compose through the specialized stack without needing
// a monomorphized Option<V> enum with its own isSome method
// dispatch. At the LLVM layer, Option<T> for every T is already
// represented as `ptr` (null = None, non-null = Some), so the
// check is uniformly a null-comparison regardless of V.
func (g *generator) emitOptionMethodCall(call *ast.CallExpr) (value, bool, error) {
	if call == nil {
		return value{}, false, nil
	}
	field, ok := fieldExprOfCallFn(call)
	if !ok || field.IsOptional {
		return value{}, false, nil
	}
	if field.Name != "isSome" && field.Name != "isNone" && field.Name != "unwrap" {
		return value{}, false, nil
	}
	if len(call.Args) != 0 {
		return value{}, false, nil
	}
	baseSrc, ok := g.staticExprSourceType(field.X)
	if !ok {
		return value{}, false, nil
	}
	optType, isOpt := baseSrc.(*ast.OptionalType)
	if !isOpt {
		return value{}, false, nil
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	if base.typ != "ptr" {
		return value{}, true, unsupportedf("type-system", "Option.%s receiver type %s, want ptr", field.Name, base.typ)
	}
	if field.Name == "unwrap" {
		return g.emitOptionUnwrap(base, optType, call)
	}
	emitter := g.toOstyEmitter()
	cmp := llvmNextTemp(emitter)
	op := "ne"
	if field.Name == "isNone" {
		op = "eq"
	}
	emitter.body = append(emitter.body, mirICmpEqPtrText(cmp, op, base.ref))
	g.takeOstyEmitter(emitter)
	return value{typ: "i1", ref: cmp}, true, nil
}

// emitResultAbortCall lowers the stdlib `__resultAbort(msg)` helper
// as a print + exit(1) sequence. The Osty body is a self-recursive
// stub — the backend's job is to terminate the program with the
// caller-supplied diagnostic. Mirrors the testing-abort contract so
// the message hits stderr on the way out.
//
// Emits `call void @exit(i32 1)` without closing the current block:
// the caller (match arm emission, if/else phi, …) still owns the
// block's terminator (`br label %end`). Returned value uses `"void"`
// to signal that the branch is effectively never-returning — downstream
// phi builders pair it with coerceVoidArm so the phi entry becomes an
// `undef <ty>` that LLVM accepts regardless of the sibling arm's type.
// The runtime never actually reads that phi operand because exit(1)
// already terminated the process.
func (g *generator) emitResultAbortCall(call *ast.CallExpr) (value, error) {
	msg := "called unwrap on Err"
	emitter := g.toOstyEmitter()
	emitted := false
	if len(call.Args) >= 1 && call.Args[0] != nil && call.Args[0].Value != nil {
		g.takeOstyEmitter(emitter)
		m, err := g.emitExpr(call.Args[0].Value)
		if err == nil {
			m, err = g.loadIfPointer(m)
		}
		emitter = g.toOstyEmitter()
		if err == nil && m.typ == "ptr" {
			llvmPrintlnString(emitter, &LlvmValue{typ: "ptr", name: m.ref})
			emitted = true
		}
	}
	if !emitted {
		msgPtr := llvmStringLiteral(emitter, msg)
		llvmPrintlnString(emitter, msgPtr)
	}
	g.declareRuntimeSymbol("exit", "void", []paramInfo{{typ: "i32"}})
	emitter.body = append(emitter.body, mirCallExitOneText())
	g.takeOstyEmitter(emitter)
	return value{typ: "void", ref: "undef"}, nil
}

// emitOptionUnwrap lowers `opt.unwrap()` for a ptr-backed `Option<T>`:
// if the receiver is non-null the payload ptr is the result; null
// aborts with a "called unwrap on None" message (matching the spec's
// panic shape). Uses the same print+exit+unreachable contract as
// `emitTestingAbortWithEmitter` so the abort path is visible in
// stderr without pulling in the full testing harness.
//
// Scalar Option (Option<Int>, Option<Bool>, …) still routes through
// the caller's generic fallback — ptr-backed is where the injection
// pipeline (`List<T>.first(self) -> T?`, `self.indexOf(k).isSome()`
// chains, …) needs unwrap first.
func (g *generator) emitOptionUnwrap(base value, optType *ast.OptionalType, call *ast.CallExpr) (value, bool, error) {
	emitter := g.toOstyEmitter()
	isNone := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirICmpEqPtrNullText(isNone, base.ref))
	someLabel := llvmNextLabel(emitter, "unwrap.some")
	noneLabel := llvmNextLabel(emitter, "unwrap.none")
	emitter.body = append(emitter.body, mirBrCondText(isNone, noneLabel, someLabel))
	emitter.body = append(emitter.body, mirLabelText(noneLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(noneLabel)

	emitter = g.toOstyEmitter()
	msg := mirOptionUnwrapNoneMessage(g.sourceLineLabel(call.Pos().Line, "<unwrap>"))
	msgPtr := llvmStringLiteral(emitter, msg)
	llvmPrintlnString(emitter, msgPtr)
	g.declareRuntimeSymbol("exit", "void", []paramInfo{{typ: "i32"}})
	emitter.body = append(emitter.body, mirCallExitOneText())
	emitter.body = append(emitter.body, mirUnreachableText())
	emitter.body = append(emitter.body, mirLabelText(someLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(someLabel)

	// Scalar Option<T> (T = Int / Bool / Float / Char / Byte) is
	// heap-boxed by `Some(x)` / `list.get` / `map.get` to keep
	// Option<T> uniformly ptr-backed at the LLVM layer. Unwrap has to
	// dereference the box so the callsite binds to the underlying
	// scalar (e.g. `let n: Int = opt.unwrap()` expects i64). Ptr-
	// backed payloads (String, List<T>, struct) pass through the
	// base ptr unchanged because Option<T> and T share `ptr` there.
	inner := optType.Inner
	if innerLLVM, ok := scalarLLVMTypeForOptionInner(inner); ok {
		emitter = g.toOstyEmitter()
		loaded := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirLoadText(loaded, innerLLVM, base.ref))
		g.takeOstyEmitter(emitter)
		return value{typ: innerLLVM, ref: loaded, sourceType: inner}, true, nil
	}

	out := base
	out.sourceType = inner
	return out, true, nil
}

// scalarLLVMTypeForOptionInner maps an Option<T>'s inner AST type to
// its LLVM scalar representation when T is a primitive that
// Some(x) / list.get / map.get box on the heap. Returns (llvmType,
// true) for Int / Bool / Float / Char / Byte, (zero, false) for
// ptr-backed or aggregate payloads which don't need a load at
// unwrap time.
func scalarLLVMTypeForOptionInner(t ast.Type) (string, bool) {
	named, ok := t.(*ast.NamedType)
	if !ok || len(named.Path) != 1 || len(named.Args) != 0 {
		return "", false
	}
	switch named.Path[0] {
	case "Int":
		return "i64", true
	case "Bool":
		return "i1", true
	case "Float":
		return "double", true
	case "Char":
		return "i32", true
	case "Byte":
		return "i8", true
	}
	return "", false
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
	case "isEmpty":
		if len(call.Args) != 0 {
			return value{}, true, unsupported("call", "set.isEmpty requires no arguments")
		}
		g.declareRuntimeSymbol(setRuntimeLenSymbol(), "i64", []paramInfo{{typ: "ptr"}})
		emitter := g.toOstyEmitter()
		lenVal := llvmCall(emitter, "i64", setRuntimeLenSymbol(), []*LlvmValue{toOstyValue(base)})
		cmp := llvmNextTemp(emitter)
		emitter.body = append(emitter.body, mirICmpEqI64LiteralText(cmp, lenVal.name, "0"))
		g.takeOstyEmitter(emitter)
		return value{typ: "i1", ref: cmp}, true, nil
	case "contains", "remove":
		if len(call.Args) != 1 || call.Args[0].Name != "" || call.Args[0].Value == nil {
			return value{}, true, unsupportedf("call", "set.%s requires one positional argument", field.Name)
		}
		itemSource, _ := g.setElemSourceType(field.X)
		item, err := g.emitExprWithSourceType(call.Args[0].Value, itemSource)
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
	thenValue, elseValue = coerceVoidArm(thenValue, elseValue)
	if thenValue.typ != elseValue.typ {
		return value{}, unsupportedf("type-system", "match arm types %s/%s", thenValue.typ, elseValue.typ)
	}
	return g.emitIfExprPhi(labels, thenPred, elsePred, thenValue, elseValue)
}

func (g *generator) bindPayloadEnumPattern(scrutinee value, pattern enumPatternInfo) error {
	if !pattern.hasPayloadBinding {
		return nil
	}
	if len(pattern.payloadBindings) == 0 {
		return nil
	}
	decoratePayload := func(payloadIndex int, payloadValue *value) error {
		if payloadIndex < 0 || payloadValue == nil {
			return nil
		}
		if payloadIndex >= len(pattern.variant.payloadSourceTypes) {
			return nil
		}
		return g.decorateValueFromSourceType(payloadValue, pattern.variant.payloadSourceTypes[payloadIndex])
	}
	if pattern.isBoxed {
		emitter := g.toOstyEmitter()
		heapPtr := llvmExtractValue(emitter, toOstyValue(scrutinee), "ptr", 1)
		if len(pattern.payloadBindings) > 1 {
			for i, b := range pattern.payloadBindings {
				if b.name == "" {
					continue
				}
				gep := llvmNextTemp(emitter)
				emitter.body = append(emitter.body, mirGEPInboundsI8Text(gep, heapPtr.name, strconv.Itoa(i*8)))
				loadTmp := llvmNextTemp(emitter)
				emitter.body = append(emitter.body, mirLoadText(loadTmp, b.typ, gep))
				payloadValue := value{typ: b.typ, ref: loadTmp}
				payloadValue.rootPaths = g.rootPathsForType(b.typ)
				if err := decoratePayload(i, &payloadValue); err != nil {
					g.takeOstyEmitter(emitter)
					return err
				}
				g.bindNamedLocal(b.name, payloadValue, false)
			}
			g.takeOstyEmitter(emitter)
			return nil
		}
		payload := llvmLoadFromSlot(emitter, heapPtr, pattern.payloadType)
		g.takeOstyEmitter(emitter)
		b := pattern.payloadBindings[0]
		payloadValue := fromOstyValue(payload)
		payloadValue.listElemTyp = pattern.payloadListElemTyp
		payloadValue.gcManaged = b.typ == "ptr" || pattern.payloadListElemTyp != ""
		payloadValue.rootPaths = g.rootPathsForType(b.typ)
		if err := decoratePayload(0, &payloadValue); err != nil {
			return err
		}
		g.bindNamedLocal(b.name, payloadValue, false)
		return nil
	}
	for i, b := range pattern.payloadBindings {
		if b.name == "" {
			continue
		}
		emitter := g.toOstyEmitter()
		payload := llvmExtractValue(emitter, toOstyValue(scrutinee), b.typ, b.index)
		g.takeOstyEmitter(emitter)
		payloadValue := fromOstyValue(payload)
		if i < len(pattern.variant.payloadListElemTyps) {
			payloadValue.listElemTyp = pattern.variant.payloadListElemTyps[i]
		} else if b.index == 1 {
			payloadValue.listElemTyp = pattern.payloadListElemTyp
		}
		payloadValue.gcManaged = b.typ == "ptr" || payloadValue.listElemTyp != ""
		payloadValue.rootPaths = g.rootPathsForType(b.typ)
		if err := decoratePayload(i, &payloadValue); err != nil {
			return err
		}
		g.bindNamedLocal(b.name, payloadValue, false)
	}
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
		if info.isBoxed && len(p.Args) > 1 {
			for _, pt := range variant.payloads {
				if pt == "ptr" {
					return enumPatternInfo{}, true, unsupportedf("expression", "enum variant pattern %q boxed multi-field payload with ptr field is not supported", name)
				}
			}
		}
		for idx, arg := range p.Args {
			binding := enumPayloadBinding{typ: variant.payloads[idx], index: idx + 1}
			switch a := arg.(type) {
			case *ast.IdentPat:
				if !llvmIsIdent(a.Name) {
					return enumPatternInfo{}, true, unsupportedf("name", "enum payload binding name %q", a.Name)
				}
				binding.name = a.Name
				out.hasPayloadBinding = true
				if idx == 0 {
					out.payloadName = a.Name
				}
			case *ast.WildcardPat:
			default:
				return enumPatternInfo{}, true, unsupportedf("expression", "enum variant payload pattern %T", a)
			}
			out.payloadBindings = append(out.payloadBindings, binding)
		}
		return out, true, nil
	default:
		return enumPatternInfo{}, false, nil
	}
}

// listElemHint carries the LLVM-level element type + String-pointer flag
// for a List<T> shape inferred from surrounding context. Pushed on
// g.matchArmListHints during match-expression arm emission so a bare
// empty-list arm body can pick up its element type from a sibling arm
// (or from an outer expected-type hint), instead of walling on LLVM013
// "empty list literal requires explicit List<T>".
type listElemHint struct {
	elemTyp    string
	elemString bool
}

func (g *generator) pushMatchArmListHint(hint listElemHint) {
	g.matchArmListHints = append(g.matchArmListHints, hint)
}

func (g *generator) popMatchArmListHint() {
	if n := len(g.matchArmListHints); n > 0 {
		g.matchArmListHints = g.matchArmListHints[:n-1]
	}
}

func (g *generator) currentMatchArmListHint() (listElemHint, bool) {
	if n := len(g.matchArmListHints); n > 0 {
		h := g.matchArmListHints[n-1]
		if h.elemTyp == "" {
			return listElemHint{}, false
		}
		return h, true
	}
	return listElemHint{}, false
}

// inferMatchArmsListHint scans arm bodies for a ListExpr whose source
// type resolves to `List<T>` with a known element IR type. Returns the
// first hint found; siblings with bare `[]` can then adopt it via
// pushMatchArmListHint on the surrounding match-expr emitter.
func (g *generator) inferMatchArmsListHint(arms []*ast.MatchArm) (listElemHint, bool) {
	for _, arm := range arms {
		if arm == nil {
			continue
		}
		body := arm.Body
		if block, ok := body.(*ast.Block); ok && len(block.Stmts) > 0 {
			if tail, ok := block.Stmts[len(block.Stmts)-1].(*ast.ExprStmt); ok && tail != nil {
				body = tail.X
			}
		}
		src, ok := g.staticExprSourceType(body)
		if !ok || src == nil {
			continue
		}
		if elemTyp, elemString, ok, err := llvmListElementInfo(src, g.typeEnv()); err == nil && ok && elemTyp != "" {
			return listElemHint{elemTyp: elemTyp, elemString: elemString}, true
		}
	}
	return listElemHint{}, false
}

func (g *generator) emitMatchArmBodyValue(expr ast.Expr) (value, error) {
	switch e := expr.(type) {
	case *ast.Block:
		g.pushScope()
		defer g.popScope()
		return g.emitBlockValue(e)
	default:
		// Empty-list arm body: adopt the sibling hint (if a push has been
		// done by the enclosing match-expr emitter) so the LLVM013 wall
		// on `_ -> []` against a typed List<T> arm stays closed.
		if list, ok := expr.(*ast.ListExpr); ok && len(list.Elems) == 0 {
			if hint, ok := g.currentMatchArmListHint(); ok {
				return g.emitListExprWithHint(list, nil, hint.elemTyp, hint.elemString)
			}
		}
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
	if v, found, err := g.emitBuiltinOptionSomeCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitEnumVariantCall(call); found || err != nil {
		return v, err
	}
	// Alias-qualified dispatchers (std.bytes / std.strings / runtime.*) — run before
	// emitInterfaceMethodCall and the *MethodCall family, which eagerly
	// lower the receiver via emitExpr and would otherwise fail with
	// LLVM016 when the receiver is a module alias rather than a binding.
	if v, found, err := g.emitStdBytesCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitStdCompressCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitBytesNamespaceCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitStdStringsCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitStdEnvCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitStdNetCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitStdCryptoCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitStdIoCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitPtrBackedErrorCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitRuntimeFFICall(call); found || err != nil {
		return v, err
	}
	// Interface downcast: `recv.downcast::<T>()` — compares the runtime
	// vtable pointer embedded in the receiver's `%osty.iface` value
	// against the vtable symbol known for `T`. Placed before the
	// generic interface-method-call path so the turbofish shape isn't
	// mistaken for a regular method call on a non-existent method
	// named `downcast`. See iface_downcast.go for the lowering.
	if v, found, err := g.emitInterfaceDowncastCall(call); found || err != nil {
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
	if v, found, err := g.emitOptionMethodCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitBytesMethodCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitPrimitiveToStringCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitCharByteConversionCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitCharPredicateCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitStringMethodCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitOptionalUserCall(call); found || err != nil {
		return v, err
	}
	if v, found, err := g.emitClosureMakerCall(call); found || err != nil {
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
		// stdlib `__resultAbort(msg) -> Never` from result.osty is a
		// `Never`-returning helper the Result<T, E> combinators call
		// on `unwrap`/`expect` error paths. The Osty body stub is a
		// self-recursive placeholder; the backend is expected to
		// intercept with a print + exit + unreachable sequence. Match
		// the testing-abort contract so the failure mode is
		// stderr-visible and the LLVM IR terminates the current block
		// cleanly. The call still gets queued into the specialized
		// `Result<T, E>.unwrap` body even when user code never calls
		// unwrap, because monomorphize emits every non-generic method
		// of a specialized owner.
		if id, ok := call.Fn.(*ast.Ident); ok && id.Name == "__resultAbort" {
			return g.emitResultAbortCall(call)
		}
		// Phase 1: indirect call through a first-class fn value held in a
		// local/global binding. `f` was bound earlier (e.g. `let f =
		// someFunc`) and carries fnSigRef so we recover the original
		// signature for the call-site type string.
		if v, ok, ierr := g.emitIndirectUserCall(call); ok || ierr != nil {
			return v, ierr
		}
		if field, ok := call.Fn.(*ast.FieldExpr); ok {
			return value{}, unsupportedf("call", "call target %T (%s)", call.Fn, debugFieldCallTarget(field))
		}
		if id, ok := call.Fn.(*ast.Ident); ok {
			return value{}, unsupportedf("call", "call target *ast.Ident (%s) — not a known fn, fn-value binding, or recognized intrinsic", id.Name)
		}
		return value{}, unsupportedf("call", "call target %T", call.Fn)
	}
	if sig.ret == "" {
		return value{}, unsupportedf("call", "function %q has no return value", sig.name)
	}
	if sig.ret == "void" {
		return value{}, unsupportedf("call", "function %q has no return value", sig.name)
	}
	// The direct-call poll runs before arg evaluation, so when the
	// current frame has no visible GC roots it can only emit an empty
	// runtime call. Skip that case to cheapen scalar-heavy recursion.
	if g.hasVisibleSafepointRoots() {
		emitter := g.toOstyEmitter()
		g.emitGCSafepointKind(emitter, safepointKindCall)
		g.takeOstyEmitter(emitter)
	}
	g.pushScope()
	args, err := g.userCallArgs(sig, receiverExpr, call)
	if err != nil {
		g.popScope()
		return value{}, err
	}
	emitter := g.toOstyEmitter()
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

func debugFieldCallTarget(field *ast.FieldExpr) string {
	if field == nil {
		return "<nil>"
	}
	return debugFieldBase(field.X) + "." + field.Name
}

// debugPatternShape returns a short human label for a match pattern so
// "match arm must be a payload-free enum variant" diagnostics can name
// the offending arm. The shape is intentionally compact: the most useful
// signal is the variant or ident name (so the reader can grep), not a
// faithful pretty-printing of the whole AST node.
func debugPatternShape(pattern ast.Pattern) string {
	switch p := pattern.(type) {
	case nil:
		return "<nil>"
	case *ast.IdentPat:
		return mirDebugIdentText(strconv.Quote(p.Name))
	case *ast.VariantPat:
		path := strings.Join(p.Path, ".")
		if len(p.Args) == 0 {
			return mirDebugVariantNoArgsText(path)
		}
		return mirDebugVariantWithArgsText(path, strconv.Itoa(len(p.Args)))
	case *ast.WildcardPat:
		return "wildcard"
	case *ast.LiteralPat:
		return "literal"
	case *ast.RangePat:
		return "range"
	case *ast.OrPat:
		return "or-pattern"
	case *ast.BindingPat:
		return "binding"
	case *ast.TuplePat:
		return mirDebugTupleText(strconv.Itoa(len(p.Elems)))
	case *ast.StructPat:
		return mirDebugStructText(strings.Join(p.Type, "."))
	default:
		return fmt.Sprintf("%T", pattern)
	}
}

func debugFieldBase(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.FieldExpr:
		return debugFieldCallTarget(e)
	default:
		return fmt.Sprintf("%T", expr)
	}
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
		if g.hasVisibleSafepointRoots() {
			emitter := g.toOstyEmitter()
			g.emitGCSafepointKind(emitter, safepointKindCall)
			g.takeOstyEmitter(emitter)
		}
		args, err := g.optionalUserCallArgs(sig, innerSource, baseValue, call)
		if err != nil {
			return value{}, err
		}
		emitter := g.toOstyEmitter()
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
			// Phase 2g: built-in container method dispatch.
			// Surface-level receivers (Map<K, V>, List<T>, …)
			// carry baseInfo.typ == "ptr" because Phase 2c
			// re-associates the mangled specialization back to the
			// surface form for intrinsic dispatch. That leaves the
			// plain `methodsByType[baseInfo.typ]` lookup missing the
			// specialized struct's bodied methods (forEach, getOr,
			// update, …). Try the specialized owner type next.
			if baseSrc, srcOk := g.staticExprSourceType(fn.X); srcOk {
				if mangledTyp, ok := specializedBuiltinMangledForSurface(baseSrc); ok {
					methods = g.methods[mangledTyp]
				}
			}
			if methods == nil {
				return nil, nil, false, nil
			}
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

// specializedBuiltinUserMethodAvailable reports whether `call` targets a
// specialized stdlib built-in method body registered under the mangled
// owner type (Map<String, Int> -> %_ZTS...). Intrinsic interceptors use
// this as an escape hatch so monomorphized helper bodies can win over
// legacy hand-emit paths at user callsites, while the plain AST path
// continues to fall back to the runtime-backed helpers.
func (g *generator) specializedBuiltinUserMethodAvailable(call *ast.CallExpr) (bool, error) {
	field, ok := fieldExprOfCallFn(call)
	if !ok || field == nil || field.IsOptional || field.X == nil {
		return false, nil
	}
	baseSource, ok := g.staticExprSourceType(field.X)
	if !ok {
		return false, nil
	}
	if _, ok := specializedBuiltinMangledForSurface(baseSource); !ok {
		return false, nil
	}
	sig, _, found, err := g.userCallTarget(call)
	if err != nil {
		return false, err
	}
	return found && sig != nil, nil
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
	payloads := make([]value, 0, len(call.Args))
	for i, arg := range call.Args {
		if arg.Name != "" || arg.Value == nil {
			return value{}, true, unsupportedf("call", "enum variant %q requires positional payload", ref.variant.name)
		}
		var payloadSource ast.Type
		if i < len(ref.variant.payloadSourceTypes) {
			payloadSource = ref.variant.payloadSourceTypes[i]
		}
		payload, err := g.emitExprWithHintAndSourceType(arg.Value, payloadSource, "", false, "", "", false, "", false)
		if err != nil {
			return value{}, true, err
		}
		if payload.typ != ref.variant.payloads[i] {
			return value{}, true, unsupportedf("type-system", "enum variant %q payload type %s, want %s", ref.variant.name, payload.typ, ref.variant.payloads[i])
		}
		payloads = append(payloads, payload)
	}
	out, err := g.emitEnumPayloadVariant(ref.enum, ref.variant, payloads)
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
	emitter.body = append(emitter.body, mirExtractValueIfacePtrText(dataPtr, recv.ref))
	vtable := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirExtractValueIfaceVtableText(vtable, recv.ref))
	fnPtrSlot := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirGEPArrayElementText(fnPtrSlot, strconv.Itoa(len(iface.methods)), vtable, strconv.Itoa(slot)))
	fnPtr := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirLoadPtrText(fnPtr, fnPtrSlot))
	callArgList := mirCallArgsPtrPrefix(dataPtr)
	for _, p := range argPairs {
		callArgList = mirAppendArgWithComma(callArgList, p[0], p[1])
	}
	ret := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, mirCallTypedFnPtrText(ret, retTyp, fnPtr, callArgList))
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
