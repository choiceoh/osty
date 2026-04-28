package llvmgen

import (
	"fmt"
	"strconv"

	"github.com/osty/osty/internal/ast"
)

// emitDerivedToStringCall lowers `.toString()` when the receiver is a
// user-defined struct/enum or a container type that satisfies the
// §17 auto-derived ToString rules. Primitive receivers are handled by
// emitPrimitiveToStringCall; this dispatcher runs after that path
// returns false.
func (g *generator) emitDerivedToStringCall(call *ast.CallExpr) (value, bool, error) {
	field, ok := call.Fn.(*ast.FieldExpr)
	if !ok || field.IsOptional || field.Name != "toString" || len(call.Args) != 0 {
		return value{}, false, nil
	}
	base, err := g.emitExpr(field.X)
	if err != nil {
		return value{}, true, err
	}
	out, handled, err := g.emitValueToString(base)
	if err != nil {
		return value{}, true, err
	}
	if !handled {
		return value{}, false, nil
	}
	return out, true, nil
}

// emitValueToString routes any runtime value through the matching
// ToString lowering: primitives to their runtime helpers, strings to
// themselves (identity per §17), structs/enums to derived formatters,
// and user types with a declared `toString` method to the user body.
// Returns (result, handled, error); handled=false means no ToString
// lowering applies so callers can fall through to the next strategy.
func (g *generator) emitValueToString(v value) (value, bool, error) {
	switch v.typ {
	case "i64":
		out, err := g.emitRuntimeIntToString(v)
		return out, true, err
	case "double":
		out, err := g.emitRuntimeFloatToString(v)
		return out, true, err
	case "i1":
		out, err := g.emitRuntimeBoolToString(v)
		return out, true, err
	case "i32":
		out, err := g.emitRuntimeCharToString(v)
		return out, true, err
	case "i8":
		out, err := g.emitRuntimeByteToString(v)
		return out, true, err
	case "ptr":
		if isStringFieldValue(v) {
			return v, true, nil
		}
		if v.listElemTyp != "" {
			out, err := g.emitListToString(v)
			return out, true, err
		}
		if v.mapKeyTyp != "" || v.setElemTyp != "" {
			return value{}, false, nil
		}
		if opt, ok := optionalSourceType(v); ok {
			out, err := g.emitOptionToString(v, opt)
			return out, true, err
		}
	}
	if info := g.structsByType[v.typ]; info != nil {
		out, err := g.emitStructToString(v, info)
		return out, true, err
	}
	if info := g.enumsByType[v.typ]; info != nil {
		out, err := g.emitEnumToString(v, info)
		return out, true, err
	}
	if info, ok := builtinResultTypeFromAST(v.sourceType, g.typeEnv()); ok && info.typ == v.typ {
		out, err := g.emitResultToString(v, info)
		return out, true, err
	}
	if info, ok := g.tupleTypes[v.typ]; ok {
		out, err := g.emitTupleToString(v, info)
		return out, true, err
	}
	return value{}, false, nil
}

// optionalSourceType returns the inner AST type if v's static provenance
// is `Option<T>` / `T?`. Used to route Option-valued `.toString()` to
// the dedicated null/payload lowering, since Option values are always
// bare `ptr` at the LLVM level and can't be distinguished from String
// by `v.typ` alone.
func optionalSourceType(v value) (*ast.OptionalType, bool) {
	opt, ok := v.sourceType.(*ast.OptionalType)
	if !ok {
		return nil, false
	}
	return opt, true
}

// emitOptionToString lowers `opt.toString()` for a ptr-backed
// `Option<T>`: emits a null comparison and phi-joins the `"None"` arm
// with a `"Some(<inner.toString()>)"` arm. The inner value is loaded
// from the heap box via the LLVM type derived from the source type —
// scalar T stored inline at *ptr, aggregate/ptr T also loaded as the
// stored shape, matching how `Some(x)` boxes it.
func (g *generator) emitOptionToString(base value, opt *ast.OptionalType) (value, error) {
	if opt == nil || opt.Inner == nil {
		return value{}, unsupported("type-system", "Option toString on nil inner type")
	}
	innerTy, err := llvmType(opt.Inner, g.typeEnv())
	if err != nil {
		return value{}, err
	}

	emitter := g.toOstyEmitter()
	cmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = icmp eq ptr %s, null", cmp, base.ref))
	noneLabel := llvmNextLabel(emitter, "tostring.none")
	someLabel := llvmNextLabel(emitter, "tostring.some")
	endLabel := llvmNextLabel(emitter, "tostring.optend")
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", cmp, noneLabel, someLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", noneLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(noneLabel)

	noneStr := g.emitStringLiteralValue("None")
	nonePred := g.currentBlock
	g.branchTo(endLabel)

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", someLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(someLabel)

	inner, err := g.loadOptionInner(base, innerTy, opt.Inner)
	if err != nil {
		return value{}, err
	}
	innerStr, err := g.emitFieldToString(inner)
	if err != nil {
		return value{}, err
	}
	open := g.emitStringLiteralValue("Some(")
	close := g.emitStringLiteralValue(")")
	someStr, err := g.emitRuntimeStringConcatN([]value{open, innerStr, close})
	if err != nil {
		return value{}, err
	}
	somePred := g.currentBlock
	g.branchTo(endLabel)

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", endLabel))
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", tmp, noneStr.ref, nonePred, someStr.ref, somePred))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)

	out := value{typ: "ptr", ref: tmp, gcManaged: true}
	out.sourceType = &ast.NamedType{Path: []string{"String"}}
	return out, nil
}

// loadOptionInner reads the payload out of a non-null `Option<T>` value.
// For ptr-backed inner types (String, List, Map, Set, struct-by-ptr)
// the `Some(x)` path returns x directly without heap-boxing — the
// Option value IS the inner ptr, so no deref. For scalars and
// aggregate structs, `Some(x)` stores x at a heap slot and the
// Option value is the box ptr — a typed load recovers x.
func (g *generator) loadOptionInner(base value, innerTy string, innerSource ast.Type) (value, error) {
	if innerTy == "ptr" {
		out := base
		out.sourceType = innerSource
		return out, nil
	}
	emitter := g.toOstyEmitter()
	loaded := llvmLoadFromSlot(emitter, toOstyValue(base), innerTy)
	g.takeOstyEmitter(emitter)
	out := fromOstyValue(loaded)
	out.sourceType = innerSource
	return out, nil
}

// emitStringLiteralValue materializes an LLVM string constant at the
// current emit point and wraps it in the `ptr`-typed value shape that
// the rest of the expression emitter expects for String values.
func (g *generator) emitStringLiteralValue(text string) value {
	emitter := g.toOstyEmitter()
	out := llvmStringLiteral(emitter, text)
	g.takeOstyEmitter(emitter)
	v := fromOstyValue(out)
	v.sourceType = &ast.NamedType{Path: []string{"String"}}
	return v
}

// emitStringQuoted wraps a String value in escaped double quotes, the
// form §17 requires for String fields inside a derived struct/enum
// rendering (e.g. `User { name: "alice", age: 30 }`). The quoting
// itself is a pair of one-byte literals concatenated around the body;
// the body is not escaped because the spec's example does not call
// for escape processing on interior bytes.
func (g *generator) emitStringQuoted(body value) (value, error) {
	quote := g.emitStringLiteralValue("\"")
	return g.emitRuntimeStringConcatN([]value{quote, body, quote})
}

// emitStructToString synthesizes the §17 default rendering:
// `StructName { field1: v1, field2: v2 }`. Field values are converted
// via emitValueToString; String fields are wrapped in quotes to match
// the spec example. The synthesized IR is inlined at each call site
// rather than emitted as a per-type function — struct shapes are
// compile-time fixed so there is no binary-size win from a shared
// helper, and inlining keeps string-concat coalescing effective.
func (g *generator) emitStructToString(base value, info *structInfo) (value, error) {
	if info == nil {
		return value{}, unsupported("type-system", "struct toString on nil info")
	}
	pieces := []value{g.emitStringLiteralValue(info.name + " { ")}
	for i, field := range info.fields {
		if i > 0 {
			pieces = append(pieces, g.emitStringLiteralValue(", "))
		}
		pieces = append(pieces, g.emitStringLiteralValue(field.name+": "))
		fv, err := g.extractStructField(base, info, field.name)
		if err != nil {
			return value{}, err
		}
		piece, err := g.emitFieldToString(fv)
		if err != nil {
			return value{}, err
		}
		pieces = append(pieces, piece)
	}
	pieces = append(pieces, g.emitStringLiteralValue(" }"))
	return g.emitRuntimeStringConcatN(pieces)
}

// emitFieldToString converts a struct/enum payload value to a String,
// quoting Strings per §17 and recursing for nested struct/enum. The
// quote rule applies only when the static type is String — not for
// Strings that appear inside a List, because §17 talks about "quotes
// string fields" (direct positional members), not every string byte.
func (g *generator) emitFieldToString(fv value) (value, error) {
	if isStringFieldValue(fv) {
		return g.emitStringQuoted(fv)
	}
	out, handled, err := g.emitValueToString(fv)
	if err != nil {
		return value{}, err
	}
	if !handled {
		return value{}, unsupportedf("type-system", "toString auto-derive for field type %s is not lowered yet", fv.typ)
	}
	return out, nil
}

// isStringFieldValue reports whether a value carries static String
// provenance (NamedType path ending in "String"). emitValueToString
// treats any bare `ptr` without container metadata as already a
// String — that identity case is fine for interpolation where the
// caller has already decided to splice the value in as-is — but
// struct field rendering needs the quoted form.
func isStringFieldValue(fv value) bool {
	if fv.typ != "ptr" {
		return false
	}
	if fv.listElemTyp != "" || fv.mapKeyTyp != "" || fv.setElemTyp != "" {
		return false
	}
	named, ok := fv.sourceType.(*ast.NamedType)
	if !ok || named == nil || len(named.Path) == 0 {
		return false
	}
	return named.Path[len(named.Path)-1] == "String"
}

// emitEnumToString lowers `enum.toString()` as a tag-indexed cascade:
// extract the i64 discriminant, then for each variant in declaration
// order compare and select the matching rendering. Non-payload
// variants render as bare `"Variant"`; payload variants render as
// `"Variant(p1, p2)"` with each payload recursively stringified.
// The phi at the join point unifies the selected string pointer; the
// final fall-through arm returns `"<?>"` to keep the IR type-correct
// for an unreachable tag rather than emitting `unreachable`, which
// would complicate surrounding defer/cleanup blocks.
func (g *generator) emitEnumToString(base value, info *enumInfo) (value, error) {
	if info == nil {
		return value{}, unsupported("type-system", "enum toString on nil info")
	}
	variants := orderedVariants(info)
	if len(variants) == 0 {
		return value{}, unsupportedf("type-system", "enum %s has no variants", info.name)
	}

	emitter := g.toOstyEmitter()
	var tag *LlvmValue
	var heapPtr *LlvmValue
	if info.isBoxed {
		tag = llvmExtractValue(emitter, toOstyValue(base), "i64", 0)
		heapPtr = llvmExtractValue(emitter, toOstyValue(base), "ptr", 1)
	} else if info.hasPayload {
		tag = llvmExtractValue(emitter, toOstyValue(base), "i64", 0)
	} else {
		tag = toOstyValue(value{typ: "i64", ref: base.ref})
	}
	g.takeOstyEmitter(emitter)

	endLabel := ""
	{
		emitter := g.toOstyEmitter()
		endLabel = llvmNextLabel(emitter, "tostring.end")
		g.takeOstyEmitter(emitter)
	}

	type armExit struct {
		pred string
		ref  string
	}
	var exits []armExit
	for i, variant := range variants {
		armLabel := ""
		nextLabel := ""
		{
			emitter := g.toOstyEmitter()
			armLabel = llvmNextLabel(emitter, fmt.Sprintf("tostring.v%d", i))
			nextLabel = llvmNextLabel(emitter, fmt.Sprintf("tostring.n%d", i))
			g.takeOstyEmitter(emitter)
		}
		emitter := g.toOstyEmitter()
		cond := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: strconv.Itoa(variant.tag)}))
		emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", cond.name, armLabel, nextLabel))
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", armLabel))
		g.takeOstyEmitter(emitter)
		g.enterBlock(armLabel)

		armVal, err := g.emitEnumVariantToString(base, info, variant, heapPtr)
		if err != nil {
			return value{}, err
		}
		exits = append(exits, armExit{pred: g.currentBlock, ref: armVal.ref})
		if g.currentReachable {
			g.branchTo(endLabel)
		}

		emitter = g.toOstyEmitter()
		emitter.body = append(emitter.body, fmt.Sprintf("%s:", nextLabel))
		g.takeOstyEmitter(emitter)
		g.enterBlock(nextLabel)
	}

	fallback := g.emitStringLiteralValue("<?>")
	exits = append(exits, armExit{pred: g.currentBlock, ref: fallback.ref})
	if g.currentReachable {
		g.branchTo(endLabel)
	}

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", endLabel))
	tmp := llvmNextTemp(emitter)
	phi := fmt.Sprintf("  %s = phi ptr ", tmp)
	for i, ex := range exits {
		if i > 0 {
			phi += ", "
		}
		phi += fmt.Sprintf("[ %s, %%%s ]", ex.ref, ex.pred)
	}
	emitter.body = append(emitter.body, phi)
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)

	out := value{typ: "ptr", ref: tmp, gcManaged: true}
	out.sourceType = &ast.NamedType{Path: []string{"String"}}
	return out, nil
}

// emitEnumVariantToString renders a single enum arm, either as the
// bare variant name or `Variant(p1, p2, ...)` for payload-carrying
// cases. Payloads are extracted from the enum's payload slot (element
// 1 of the outer `{i64, T}` shape) by element index when the variant
// has multiple payload components. For boxed enums, heapPtr is the
// already-extracted slot-1 pointer and payload components are loaded
// from the heap block per `bindPayloadEnumPattern`'s layout rules.
func (g *generator) emitEnumVariantToString(base value, info *enumInfo, variant variantInfo, heapPtr *LlvmValue) (value, error) {
	if len(variant.payloads) == 0 {
		return g.emitStringLiteralValue(fmt.Sprintf("%s.%s", info.name, variant.name)), nil
	}
	pieces := []value{g.emitStringLiteralValue(fmt.Sprintf("%s.%s(", info.name, variant.name))}

	components, err := g.extractVariantPayloadComponents(base, info, variant, heapPtr)
	if err != nil {
		return value{}, err
	}

	for i, comp := range components {
		if i > 0 {
			pieces = append(pieces, g.emitStringLiteralValue(", "))
		}
		piece, err := g.emitFieldToString(comp)
		if err != nil {
			return value{}, err
		}
		pieces = append(pieces, piece)
	}
	pieces = append(pieces, g.emitStringLiteralValue(")"))
	return g.emitRuntimeStringConcatN(pieces)
}

// extractVariantPayloadComponents returns one value per positional
// payload component, handling both inline (unboxed) and heap (boxed)
// enum layouts.
//
// Unboxed single-payload: payload sits at slot 1 of the outer struct;
// the component IS the payload value.
// Unboxed multi-payload: slot 1 holds a sub-struct with one element
// per component.
// Boxed single-payload: slot 1 is a heap ptr whose target is the
// payload value; load via the component's LLVM type.
// Boxed multi-field: slot 1 is a heap ptr to a flat array of 8-byte
// slots; load each via `getelementptr i8, ptr heap, i64 i*8`.
func (g *generator) extractVariantPayloadComponents(base value, info *enumInfo, variant variantInfo, heapPtr *LlvmValue) ([]value, error) {
	if len(variant.payloads) == 0 {
		return nil, nil
	}
	emitter := g.toOstyEmitter()
	defer g.takeOstyEmitter(emitter)

	if info.isBoxed {
		if heapPtr == nil {
			return nil, unsupportedf("type-system", "boxed enum %s missing heap pointer for toString", info.name)
		}
		if len(variant.payloads) == 1 {
			loaded := llvmLoadFromSlot(emitter, heapPtr, variant.payloads[0])
			v := fromOstyValue(loaded)
			if len(variant.payloadListElemTyps) > 0 {
				v.listElemTyp = variant.payloadListElemTyps[0]
			}
			if i := 0; i < len(variant.payloadSourceTypes) {
				_ = g.decorateValueFromSourceType(&v, variant.payloadSourceTypes[i])
			}
			return []value{v}, nil
		}
		out := make([]value, 0, len(variant.payloads))
		for i, ptyp := range variant.payloads {
			gep := llvmNextTemp(emitter)
			emitter.body = append(emitter.body, fmt.Sprintf("  %s = getelementptr i8, ptr %s, i64 %d", gep, heapPtr.name, i*8))
			loadTmp := llvmNextTemp(emitter)
			emitter.body = append(emitter.body, fmt.Sprintf("  %s = load %s, ptr %s", loadTmp, ptyp, gep))
			v := value{typ: ptyp, ref: loadTmp}
			if i < len(variant.payloadSourceTypes) {
				_ = g.decorateValueFromSourceType(&v, variant.payloadSourceTypes[i])
			}
			out = append(out, v)
		}
		return out, nil
	}

	if len(variant.payloads) == 1 {
		payloadType := info.payloadTyp
		if payloadType == "" {
			payloadType = variant.payloads[0]
		}
		v := fromOstyValue(llvmExtractValue(emitter, toOstyValue(base), payloadType, 1))
		if len(variant.payloadListElemTyps) > 0 {
			v.listElemTyp = variant.payloadListElemTyps[0]
		}
		if 0 < len(variant.payloadSourceTypes) {
			_ = g.decorateValueFromSourceType(&v, variant.payloadSourceTypes[0])
		}
		return []value{v}, nil
	}
	out := make([]value, 0, len(variant.payloads))
	for i, ptyp := range variant.payloads {
		v := fromOstyValue(llvmExtractValue(emitter, toOstyValue(base), ptyp, i+1))
		if i < len(variant.payloadListElemTyps) {
			v.listElemTyp = variant.payloadListElemTyps[i]
		}
		if i < len(variant.payloadSourceTypes) {
			_ = g.decorateValueFromSourceType(&v, variant.payloadSourceTypes[i])
		}
		out = append(out, v)
	}
	return out, nil
}

// emitResultToString lowers `result.toString()` for a `Result<T, E>`
// value with layout `{i64 tag, T ok, E err}`. tag == 0 is Ok(T);
// tag == 1 is Err(E). Each arm extracts the matching payload field
// and wraps it in "Ok(...)" / "Err(...)" with field quoting for
// Strings per §17. Implementation mirrors the shape of
// emitResultMatchExprValue: tag compare, two branches, phi-join.
func (g *generator) emitResultToString(base value, info builtinResultType) (value, error) {
	named, ok := base.sourceType.(*ast.NamedType)
	if !ok || len(named.Args) != 2 {
		return value{}, unsupported("type-system", "Result toString missing source type arguments")
	}
	okSrc := named.Args[0]
	errSrc := named.Args[1]

	emitter := g.toOstyEmitter()
	tag := llvmExtractValue(emitter, toOstyValue(base), "i64", 0)
	cond := llvmCompare(emitter, "eq", tag, toOstyValue(value{typ: "i64", ref: "0"}))
	okLabel := llvmNextLabel(emitter, "tostring.ok")
	errLabel := llvmNextLabel(emitter, "tostring.err")
	endLabel := llvmNextLabel(emitter, "tostring.resend")
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", cond.name, okLabel, errLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", okLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(okLabel)

	okStr, err := g.emitResultArmToString(base, "Ok", info.okTyp, okSrc, 1)
	if err != nil {
		return value{}, err
	}
	okPred := g.currentBlock
	g.branchTo(endLabel)

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", errLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(errLabel)

	errStr, err := g.emitResultArmToString(base, "Err", info.errTyp, errSrc, 2)
	if err != nil {
		return value{}, err
	}
	errPred := g.currentBlock
	g.branchTo(endLabel)

	emitter = g.toOstyEmitter()
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", endLabel))
	tmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", tmp, okStr.ref, okPred, errStr.ref, errPred))
	g.takeOstyEmitter(emitter)
	g.enterBlock(endLabel)

	out := value{typ: "ptr", ref: tmp, gcManaged: true}
	out.sourceType = &ast.NamedType{Path: []string{"String"}}
	return out, nil
}

// emitResultArmToString extracts one Result arm's payload, recursively
// stringifies it, and wraps it in the variant-name envelope. `index`
// is the element index of the payload inside the Result aggregate
// (1 for Ok, 2 for Err).
func (g *generator) emitResultArmToString(base value, variant string, payloadTy string, payloadSrc ast.Type, index int) (value, error) {
	emitter := g.toOstyEmitter()
	payload := llvmExtractValue(emitter, toOstyValue(base), payloadTy, index)
	g.takeOstyEmitter(emitter)
	pv := fromOstyValue(payload)
	pv.sourceType = payloadSrc
	if payloadTy == "ptr" {
		pv.gcManaged = true
	}
	inner, err := g.emitFieldToString(pv)
	if err != nil {
		return value{}, err
	}
	open := g.emitStringLiteralValue(variant + "(")
	close := g.emitStringLiteralValue(")")
	return g.emitRuntimeStringConcatN([]value{open, inner, close})
}

// emitListToString lowers `list.toString()` as `[e1, e2, e3]` with
// each element recursively stringified. Implementation: accumulate
// per-element `String` values into a scratch `List<String>` via
// `osty_rt_list_push_ptr`, then `osty_rt_strings_Join(parts, ", ")`
// and wrap in `[`…`]`. Uses a classic header/body/latch loop over
// the list length returned by `osty_rt_list_len`.
//
// Element access routes through the typed getter that matches
// `v.listElemTyp` (i64/i1/f64/ptr). Non-primitive element types
// (char/byte/aggregates stored inline) are not yet supported and
// fall through to an unsupported diagnostic so the caller surfaces
// a clean error instead of emitting wrong IR.
func (g *generator) emitListToString(base value) (value, error) {
	elemTy := base.listElemTyp
	var elemSrc ast.Type
	if named, ok := base.sourceType.(*ast.NamedType); ok && len(named.Args) == 1 {
		elemSrc = named.Args[0]
	}

	getSymbol := ""
	getRet := elemTy
	switch elemTy {
	case "i64", "i1", "ptr":
		getSymbol = "osty_rt_list_get_" + elemTy
	case "double":
		getSymbol = "osty_rt_list_get_f64"
	default:
		return value{}, unsupportedf("type-system", "toString auto-derive for List element type %s is not lowered yet", elemTy)
	}

	g.declareRuntimeSymbol("osty_rt_list_new", "ptr", nil)
	g.declareRuntimeSymbol("osty_rt_list_len", "i64", []paramInfo{{typ: "ptr"}})
	g.declareRuntimeSymbol(getSymbol, getRet, []paramInfo{{typ: "ptr"}, {typ: "i64"}})
	g.declareRuntimeSymbol("osty_rt_list_push_ptr", "void", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})
	g.declareRuntimeSymbol("osty_rt_strings_Join", "ptr", []paramInfo{{typ: "ptr"}, {typ: "ptr"}})

	emitter := g.toOstyEmitter()
	partsTmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = call ptr @osty_rt_list_new()", partsTmp))
	lenTmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = call i64 @osty_rt_list_len(ptr %s)", lenTmp, base.ref))
	headerLabel := llvmNextLabel(emitter, "tostring.list.h")
	bodyLabel := llvmNextLabel(emitter, "tostring.list.b")
	exitLabel := llvmNextLabel(emitter, "tostring.list.x")
	entryPred := g.currentBlock
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", headerLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", headerLabel))
	idxTmp := llvmNextTemp(emitter)
	phiIdx := len(emitter.body)
	emitter.body = append(emitter.body, "")
	condTmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = icmp slt i64 %s, %s", condTmp, idxTmp, lenTmp))
	emitter.body = append(emitter.body, fmt.Sprintf("  br i1 %s, label %%%s, label %%%s", condTmp, bodyLabel, exitLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", bodyLabel))
	g.takeOstyEmitter(emitter)
	g.enterBlock(bodyLabel)

	emitter = g.toOstyEmitter()
	elemTmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = call %s @%s(ptr %s, i64 %s)", elemTmp, getRet, getSymbol, base.ref, idxTmp))
	g.takeOstyEmitter(emitter)

	elemVal := value{typ: getRet, ref: elemTmp}
	if elemSrc != nil {
		elemVal.sourceType = elemSrc
		if err := g.decorateValueFromSourceType(&elemVal, elemSrc); err != nil {
			return value{}, err
		}
	}
	if getRet == "ptr" {
		elemVal.gcManaged = true
	}
	elemStr, err := g.emitFieldToString(elemVal)
	if err != nil {
		return value{}, err
	}

	latchPred := g.currentBlock
	emitter = g.toOstyEmitter()
	nextTmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  call void @osty_rt_list_push_ptr(ptr %s, ptr %s)", partsTmp, elemStr.ref))
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = add nuw i64 %s, 1", nextTmp, idxTmp))
	emitter.body = append(emitter.body, fmt.Sprintf("  br label %%%s", headerLabel))
	emitter.body = append(emitter.body, fmt.Sprintf("%s:", exitLabel))
	emitter.body[phiIdx] = fmt.Sprintf("  %s = phi i64 [ 0, %%%s ], [ %s, %%%s ]", idxTmp, entryPred, nextTmp, latchPred)
	g.takeOstyEmitter(emitter)
	g.enterBlock(exitLabel)

	sep := g.emitStringLiteralValue(", ")
	emitter = g.toOstyEmitter()
	joinedTmp := llvmNextTemp(emitter)
	emitter.body = append(emitter.body, fmt.Sprintf("  %s = call ptr @osty_rt_strings_Join(ptr %s, ptr %s)", joinedTmp, partsTmp, sep.ref))
	g.takeOstyEmitter(emitter)
	joined := value{typ: "ptr", ref: joinedTmp, gcManaged: true}
	joined.sourceType = &ast.NamedType{Path: []string{"String"}}

	open := g.emitStringLiteralValue("[")
	close := g.emitStringLiteralValue("]")
	return g.emitRuntimeStringConcatN([]value{open, joined, close})
}

// emitTupleToString lowers `tuple.toString()` as `(e1, e2, e3)` with
// each element recursively stringified. 1-tuples render as `(e1,)` to
// mirror the source syntax. Element source types (for quoting String
// fields, recognising Option/List provenance, etc.) are recovered via
// `extractTupleElement` which already decorates the element value.
func (g *generator) emitTupleToString(base value, info tupleTypeInfo) (value, error) {
	if len(info.elems) == 0 {
		return g.emitStringLiteralValue("()"), nil
	}
	pieces := []value{g.emitStringLiteralValue("(")}
	for i := range info.elems {
		if i > 0 {
			pieces = append(pieces, g.emitStringLiteralValue(", "))
		}
		elem, err := g.extractTupleElement(base, info, i)
		if err != nil {
			return value{}, err
		}
		piece, err := g.emitFieldToString(elem)
		if err != nil {
			return value{}, err
		}
		pieces = append(pieces, piece)
	}
	if len(info.elems) == 1 {
		pieces = append(pieces, g.emitStringLiteralValue(",)"))
	} else {
		pieces = append(pieces, g.emitStringLiteralValue(")"))
	}
	return g.emitRuntimeStringConcatN(pieces)
}

// orderedVariants returns an enum's variants in declaration order.
// enumInfo.variants is a map, so we reconstruct order from the AST.
func orderedVariants(info *enumInfo) []variantInfo {
	if info == nil || info.decl == nil {
		return nil
	}
	out := make([]variantInfo, 0, len(info.decl.Variants))
	for _, decl := range info.decl.Variants {
		if decl == nil {
			continue
		}
		if v, ok := info.variants[decl.Name]; ok {
			out = append(out, v)
		}
	}
	return out
}
