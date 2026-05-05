package selfhost

// installPrimitiveArithMethods registers the placeholder methods that
// `internal/stdlib/primitives/int.osty` declares behind
// `#[intrinsic_methods(Int, Int8, …, UInt64, Byte)]`. The legacy
// stdlib loader copies these method names into a Primitives map but
// the self-hosted checker — generated.go — does not consult that
// map, so without this registration `Int.abs()` and friends surface
// as `E0703 no method on type Int` and never reach the LLVM lowering
// at MIR primitive-method emission (now owned by the native LIR
// Proto subprocess in `cmd/osty-native-lirproto`).
//
// The integer methods are all `Self`-shaped, so the registration just
// substitutes each owner kind for `Self` in receiver / param / return
// types. Backend coverage decides which widths actually compile (i64,
// i32, i8 are wired today; i16 falls through to unsupported), but the
// checker accepts the call site uniformly so user code looks the same
// across widths.
//
// Mirrored in `toolchain/check_env.osty` so the future LLVM-compiled
// native checker registers the same surface.
func installPrimitiveArithMethods(env *CheckEnv) {
	tys := env.tys
	registerStdMathModule(env)
	kinds := []struct {
		owner string
		ty    int
	}{
		{"Int", tInt(tys)},
		{"Int8", tInt8(tys)},
		{"Int16", tInt16(tys)},
		{"Int32", tInt32(tys)},
		{"Int64", tInt64(tys)},
		{"UInt8", tUInt8(tys)},
		{"UInt16", tUInt16(tys)},
		{"UInt32", tUInt32(tys)},
		{"UInt64", tUInt64(tys)},
		{"Byte", tByte(tys)},
	}
	for _, k := range kinds {
		registerSelfShapedNullary(env, k.owner, k.ty, "abs")
		registerSelfShapedNullary(env, k.owner, k.ty, "signum")
		registerSelfShapedBinary(env, k.owner, k.ty, "min", "other")
		registerSelfShapedBinary(env, k.owner, k.ty, "max", "other")
		registerSelfShapedClamp(env, k.owner, k.ty)
		for _, name := range []string{"wrappingAdd", "wrappingSub", "wrappingMul", "wrappingDiv", "wrappingMod"} {
			registerSelfShapedBinary(env, k.owner, k.ty, name, "other")
		}
		registerSelfIntUnary(env, k.owner, k.ty, "wrappingShl", "b")
		registerSelfIntUnary(env, k.owner, k.ty, "wrappingShr", "b")
		registerSelfShapedNullary(env, k.owner, k.ty, "wrappingAbs")
		registerSelfShapedNullary(env, k.owner, k.ty, "wrappingNeg")
		for _, name := range []string{"checkedAdd", "checkedSub", "checkedMul", "checkedDiv", "checkedMod"} {
			registerOptionalSelfBinary(env, k.owner, k.ty, name, "other")
		}
		registerOptionalSelfIntUnary(env, k.owner, k.ty, "checkedShl", "b")
		registerOptionalSelfIntUnary(env, k.owner, k.ty, "checkedShr", "b")
		registerOptionalSelfNullary(env, k.owner, k.ty, "checkedAbs")
		registerOptionalSelfNullary(env, k.owner, k.ty, "checkedNeg")
		for _, name := range []string{"saturatingAdd", "saturatingSub", "saturatingMul", "saturatingDiv"} {
			registerSelfShapedBinary(env, k.owner, k.ty, name, "other")
		}
		registerSelfIntUnary(env, k.owner, k.ty, "pow", "exp")
		registerIntConversionMethods(env, k.owner, k.ty, tys)
		// toString — narrow widths share the i64 ABI on the LLVM side
		// and the dispatcher in `g.emitRuntimeIntToString` already
		// handles every Int kind by routing through `osty_rt_int_to_string`.
		// Register here so `let n: Int8 = 3; n.toString()` /
		// `let n: UInt32 = 3; n.toString()` resolve uniformly. The
		// `Int` registration in generated.go (paired with the
		// UntypedInt → "Int" promotion from #992) covered only the
		// canonical width — the other 9 surfaced as
		// `E0703 no method on type Int8` despite the lowering being
		// ready.
		registerToString(env, k.owner, k.ty)
		registerDurationConstructors(env, k.owner, k.ty)
	}
	registerSupplementalStdlibSurface(env)

	// Bytes primitive methods — mirrors the surface declared in
	// `internal/stdlib/primitives/bytes.osty` behind
	// `#[intrinsic_methods(Bytes)]`. The generic AST-based collector
	// `collectIntrinsicMethodsFromAst` handles this when invoked on
	// the bytes stub, but the prepopulated registration below ensures
	// the methods are available even when the AST collector hasn't
	// run yet (e.g. during early bootstrap).
	{
		bytesTy := tBytes(tys)
		bytesOwner := "Bytes"
		registerBytesIntrinsicMethods(env, bytesOwner, bytesTy, tys)
	}

	// Float family — same `#[intrinsic_methods(Float, Float32, Float64)]`
	// shape as the integer loop above. Register the full stdlib-declared
	// surface so selfhost checking matches the embedded primitive stub:
	// same-shaped numeric methods, predicates, checked conversions, format
	// helpers, legacy explicit conversions kept for compatibility, and
	// float-width resizes.
	floatKinds := []struct {
		owner string
		ty    int
	}{
		{"Float", tFloat(tys)},
		{"Float32", tFloat32(tys)},
		{"Float64", tFloat64(tys)},
	}
	for _, k := range floatKinds {
		for _, name := range []string{
			"abs", "signum", "floor", "ceil", "round", "trunc", "fract",
			"sqrt", "cbrt", "ln", "log2", "log10", "exp",
			"sin", "cos", "tan", "asin", "acos", "atan",
		} {
			registerSelfShapedNullary(env, k.owner, k.ty, name)
		}
		for _, name := range []string{"min", "max", "atan2", "pow"} {
			registerSelfShapedBinary(env, k.owner, k.ty, name, "other")
		}
		registerSelfShapedClamp(env, k.owner, k.ty)
		for _, name := range []string{"isNaN", "isInfinite", "isFinite"} {
			registerBoolNullary(env, k.owner, k.ty, name)
		}
		registerUInt64Nullary(env, k.owner, k.ty, "toBits")
		registerStringUnary(env, k.owner, k.ty, "toFixed", "n", tInt(tys))
		for _, name := range []string{"toIntTrunc", "toIntRound", "toIntFloor", "toIntCeil"} {
			registerResultIntErrorNullary(env, k.owner, k.ty, name, tys)
		}
		registerPlainReturnNullary(env, k.owner, k.ty, "toInt", tInt(tys))
		registerPlainReturnNullary(env, k.owner, k.ty, "toInt32", tInt32(tys))
		registerPlainReturnNullary(env, k.owner, k.ty, "toInt64", tInt64(tys))
		registerPlainReturnNullary(env, k.owner, k.ty, "toFloat", tFloat(tys))
		registerPlainReturnNullary(env, k.owner, k.ty, "toFloat32", tFloat32(tys))
		registerPlainReturnNullary(env, k.owner, k.ty, "toFloat64", tFloat64(tys))
		registerToString(env, k.owner, k.ty)
		registerDurationConstructors(env, k.owner, k.ty)
	}
}

// registerDurationConstructors registers the Duration-producing nullary
// methods declared in `primitives/{int,float}.osty` (§10.20). The
// receiver is each integer / float kind; the return type is always the
// prelude `Duration` builtin so call sites see the same struct identity
// regardless of whether `std.time` is imported.
func registerDurationConstructors(env *CheckEnv, owner string, ty int) {
	tDuration := tyNamed(env.tys, "Duration", make([]int, 0, 1))
	for _, name := range []string{"ns", "us", "ms", "s", "minutes", "h", "days", "weeks"} {
		checkRegisterFn(env, &CheckFnSig{
			name:          name,
			owner:         owner,
			receiverTy:    ty,
			hasReceiver:   true,
			retTy:         tDuration,
			paramNames:    make([]string, 0, 1),
			paramTys:      make([]int, 0, 1),
			generics:      make([]string, 0, 1),
			genericBounds: make([]*CheckGenericBound, 0, 1),
		})
	}
}

func registerSupplementalStdlibSurface(env *CheckEnv) {
	tys := env.tys
	tString_ := tString(tys)
	tError := tyNamed(tys, "Error", make([]int, 0, 1))
	tListString := tyNamed(tys, "List", []int{tString_})

	checkRegisterFn(env, &CheckFnSig{
		name:          "join",
		owner:         "List",
		receiverTy:    tListString,
		hasReceiver:   true,
		retTy:         tString_,
		paramNames:    []string{"sep"},
		paramTys:      []int{tString_},
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
	checkRegisterFn(env, &CheckFnSig{
		name:          "new",
		owner:         "Error",
		receiverTy:    -1,
		hasReceiver:   false,
		retTy:         tError,
		paramNames:    []string{"message"},
		paramTys:      []int{tString_},
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
	checkMarkFnHasBody(env, "new", "Error")
	registerDurationMembers(env)
}

// registerDurationMembers fills in the methods + field that
// `internal/stdlib/modules/time.osty:12-35` declares on the `Duration`
// struct. Without this, prelude registers `Duration` as a builtin name
// (so `Int.s` / `Int.ms` etc. constructors return a real type), but
// the checker can't see any of the struct's methods or fields — every
// `d.toString()` / `d.nanoseconds` access surfaces as E0703 / E0702.
// Tracked as `duration-builtin-methods` in SPEC_GAPS until this fix.
func registerDurationMembers(env *CheckEnv) {
	tys := env.tys
	tDuration := tyNamed(tys, "Duration", make([]int, 0, 1))
	tInt_ := tInt(tys)
	tInt64_ := tInt64(tys)
	tString_ := tString(tys)

	for _, m := range []struct {
		name  string
		retTy int
	}{
		{"abs", tDuration},
		{"micros", tInt_},
		{"millis", tInt_},
		{"seconds", tInt_},
		{"toString", tString_},
	} {
		checkRegisterFn(env, &CheckFnSig{
			name:          m.name,
			owner:         "Duration",
			receiverTy:    tDuration,
			hasReceiver:   true,
			retTy:         m.retTy,
			paramNames:    make([]string, 0, 1),
			paramTys:      make([]int, 0, 1),
			generics:      make([]string, 0, 1),
			genericBounds: make([]*CheckGenericBound, 0, 1),
		})
	}
	checkRegisterField(env, &CheckFieldSig{
		owner:      "Duration",
		name:       "nanoseconds",
		ty:         tInt64_,
		exported:   true,
		hasDefault: false,
	})
}

func registerIntConversionMethods(env *CheckEnv, owner string, ty int, tys *TyArena) {
	registerPlainReturnNullary(env, owner, ty, "toInt", tInt(tys))
	registerResultReturnNullary(env, owner, ty, "toInt8", tInt8(tys), tys)
	registerResultReturnNullary(env, owner, ty, "toInt16", tInt16(tys), tys)
	registerResultReturnNullary(env, owner, ty, "toInt32", tInt32(tys), tys)
	registerPlainReturnNullary(env, owner, ty, "toInt64", tInt64(tys))
	registerResultReturnNullary(env, owner, ty, "toUInt8", tUInt8(tys), tys)
	registerPlainReturnNullary(env, owner, ty, "toByte", tByte(tys))
	registerResultReturnNullary(env, owner, ty, "toUInt16", tUInt16(tys), tys)
	registerResultReturnNullary(env, owner, ty, "toUInt32", tUInt32(tys), tys)
	registerResultReturnNullary(env, owner, ty, "toUInt64", tUInt64(tys), tys)
	registerPlainReturnNullary(env, owner, ty, "toFloat", tFloat(tys))
	registerPlainReturnNullary(env, owner, ty, "toFloat32", tFloat32(tys))
	registerPlainReturnNullary(env, owner, ty, "toFloat64", tFloat64(tys))
	registerPlainReturnNullary(env, owner, ty, "toChar", tChar(tys))
}

func registerStdMathModule(env *CheckEnv) {
	tys := env.tys
	tFloat_ := tFloat(tys)
	tMath := tyNamed(tys, "math", make([]int, 0, 1))
	checkBindSpan(env, "math", tMath, false, 0, 0)
	for _, name := range []string{"PI", "E", "TAU", "INFINITY", "NAN"} {
		checkRegisterField(env, &CheckFieldSig{
			owner:      "math",
			name:       name,
			ty:         tFloat_,
			exported:   true,
			hasDefault: false,
		})
	}
	for _, name := range []string{
		"sin", "cos", "tan",
		"asin", "acos", "atan",
		"sinh", "cosh", "tanh",
		"exp", "log2", "log10",
		"sqrt", "cbrt",
		"floor", "ceil", "round", "trunc",
		"abs",
	} {
		checkRegisterFn(env, &CheckFnSig{
			name:          name,
			owner:         "math",
			receiverTy:    -1,
			retTy:         tFloat_,
			paramNames:    []string{"x"},
			paramTys:      []int{tFloat_},
			generics:      make([]string, 0, 1),
			genericBounds: make([]*CheckGenericBound, 0, 1),
		})
	}
	for _, name := range []string{"min", "max", "hypot"} {
		checkRegisterFn(env, &CheckFnSig{
			name:          name,
			owner:         "math",
			receiverTy:    -1,
			retTy:         tFloat_,
			paramNames:    []string{"a", "b"},
			paramTys:      []int{tFloat_, tFloat_},
			generics:      make([]string, 0, 1),
			genericBounds: make([]*CheckGenericBound, 0, 1),
		})
	}
	checkRegisterFn(env, &CheckFnSig{
		name:          "atan2",
		owner:         "math",
		receiverTy:    -1,
		retTy:         tFloat_,
		paramNames:    []string{"y", "x"},
		paramTys:      []int{tFloat_, tFloat_},
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
	checkRegisterFn(env, &CheckFnSig{
		name:          "pow",
		owner:         "math",
		receiverTy:    -1,
		retTy:         tFloat_,
		paramNames:    []string{"x", "y"},
		paramTys:      []int{tFloat_, tFloat_},
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
	checkRegisterFn(env, &CheckFnSig{
		name:          "log",
		owner:         "math",
		receiverTy:    -1,
		retTy:         tFloat_,
		paramNames:    []string{"x", "base"},
		paramTys:      []int{tFloat_, tFloat_},
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

func registerToString(env *CheckEnv, owner string, ty int) {
	tys := env.tys
	checkRegisterFn(env, &CheckFnSig{
		name:          "toString",
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         tString(tys),
		paramNames:    make([]string, 0, 1),
		paramTys:      make([]int, 0, 1),
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

func registerSelfShapedNullary(env *CheckEnv, owner string, ty int, name string) {
	checkRegisterFn(env, &CheckFnSig{
		name:          name,
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         ty,
		paramNames:    make([]string, 0, 1),
		paramTys:      make([]int, 0, 1),
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

func registerSelfShapedBinary(env *CheckEnv, owner string, ty int, name, paramName string) {
	checkRegisterFn(env, &CheckFnSig{
		name:          name,
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         ty,
		paramNames:    []string{paramName},
		paramTys:      []int{ty},
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

func registerSelfIntUnary(env *CheckEnv, owner string, ty int, name, paramName string) {
	checkRegisterFn(env, &CheckFnSig{
		name:          name,
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         ty,
		paramNames:    []string{paramName},
		paramTys:      []int{tInt(env.tys)},
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

func registerSelfShapedClamp(env *CheckEnv, owner string, ty int) {
	checkRegisterFn(env, &CheckFnSig{
		name:          "clamp",
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         ty,
		paramNames:    []string{"lo", "hi"},
		paramTys:      []int{ty, ty},
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

func registerOptionalSelfNullary(env *CheckEnv, owner string, ty int, name string) {
	checkRegisterFn(env, &CheckFnSig{
		name:          name,
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         tyOptional(env.tys, ty),
		paramNames:    make([]string, 0, 1),
		paramTys:      make([]int, 0, 1),
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

func registerOptionalSelfBinary(env *CheckEnv, owner string, ty int, name, paramName string) {
	checkRegisterFn(env, &CheckFnSig{
		name:          name,
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         tyOptional(env.tys, ty),
		paramNames:    []string{paramName},
		paramTys:      []int{ty},
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

func registerOptionalSelfIntUnary(env *CheckEnv, owner string, ty int, name, paramName string) {
	checkRegisterFn(env, &CheckFnSig{
		name:          name,
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         tyOptional(env.tys, ty),
		paramNames:    []string{paramName},
		paramTys:      []int{tInt(env.tys)},
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

func registerBoolNullary(env *CheckEnv, owner string, ty int, name string) {
	checkRegisterFn(env, &CheckFnSig{
		name:          name,
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         tBool(env.tys),
		paramNames:    make([]string, 0, 1),
		paramTys:      make([]int, 0, 1),
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

func registerUInt64Nullary(env *CheckEnv, owner string, ty int, name string) {
	checkRegisterFn(env, &CheckFnSig{
		name:          name,
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         tUInt64(env.tys),
		paramNames:    make([]string, 0, 1),
		paramTys:      make([]int, 0, 1),
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

func registerStringUnary(env *CheckEnv, owner string, ty int, name, paramName string, paramTy int) {
	checkRegisterFn(env, &CheckFnSig{
		name:          name,
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         tString(env.tys),
		paramNames:    []string{paramName},
		paramTys:      []int{paramTy},
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

func registerPlainReturnNullary(env *CheckEnv, owner string, ty int, name string, retTy int) {
	checkRegisterFn(env, &CheckFnSig{
		name:          name,
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         retTy,
		paramNames:    make([]string, 0, 1),
		paramTys:      make([]int, 0, 1),
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

func registerResultIntErrorNullary(env *CheckEnv, owner string, ty int, name string, tys *TyArena) {
	registerResultReturnNullary(env, owner, ty, name, tInt(tys), tys)
}

func registerResultReturnNullary(env *CheckEnv, owner string, ty int, name string, retValueTy int, tys *TyArena) {
	checkRegisterFn(env, &CheckFnSig{
		name:          name,
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         tyNamed(tys, "Result", []int{retValueTy, tyNamed(tys, "Error", make([]int, 0, 1))}),
		paramNames:    make([]string, 0, 1),
		paramTys:      make([]int, 0, 1),
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}

// collectIntrinsicMethodsFromAst fans out methods from a
// `#[intrinsic_methods(Int, Int8, …)]`-annotated struct to the named
// primitive types so the self-hosted checker resolves `42.abs()` etc.
// without relying on the Go-side Primitives map.
//
// Mirrored in toolchain/check.osty so the future LLVM-compiled native
// checker processes the same surface.
func collectIntrinsicMethodsFromAst(cx *ElabCx, node *AstNode) {
	if cx == nil || node == nil {
		return
	}
	anns := srCollectAnnotationNodes(cx.ast, node.extra)
	for _, ann := range anns {
		if ann.text != "intrinsic_methods" {
			continue
		}
		for _, argIdx := range ann.children {
			arg := astArenaNodeAt(cx.ast.arena, argIdx)
			targetName := arg.text
			if targetName == "" {
				continue
			}
			targetTy := tyNamed(cx.env.tys, targetName, make([]int, 0, 1))
			if targetTy < 0 || tyIsBad(cx.env.tys, targetTy) {
				continue
			}
			for _, memberIdx := range node.children {
				member := astArenaNodeAt(cx.ast.arena, memberIdx)
				if _, ok := member.kind.(*AstNodeKind_AstNFnDecl); !ok {
					continue
				}
				registerIntrinsicMethodOn(cx, member, targetName, targetTy)
			}
		}
	}
}

// registerIntrinsicMethodOn builds a CheckFnSig for one method and
// registers it on targetName. Self references stay intact — they are
// resolved by checkSpecializeMethodSelf at the call site.
func registerIntrinsicMethodOn(cx *ElabCx, method *AstNode, targetName string, targetTy int) {
	fnName := method.text
	generics := collectGenericNames(cx, method.children2)
	bounds := collectGenericBounds(cx, method.children2)

	paramNames := make([]string, 0, len(method.children))
	paramTys := make([]int, 0, len(method.children))
	hasReceiver := false
	for _, paramIdx := range method.children {
		paramNode := astArenaNodeAt(cx.ast.arena, paramIdx)
		if _, ok := paramNode.kind.(*AstNodeKind_AstNParam); !ok {
			continue
		}
		if paramNode.text == "self" {
			hasReceiver = true
		} else {
			hasDefault := paramNode.left >= 0
			rawName := paramNode.text
			storedName := rawName
			if hasDefault {
				storedName = "?" + rawName
			}
			paramNames = append(paramNames, storedName)
			declared := astTypeToTyInCollect(cx, paramNode.right)
			if declared >= 0 {
				paramTys = append(paramTys, declared)
			} else {
				paramTys = append(paramTys, tErr(cx.env.tys))
			}
		}
	}

	retTy := astTypeToTyInCollect(cx, method.left)
	if retTy < 0 {
		retTy = tUnit(cx.env.tys)
	}

	checkRegisterFn(cx.env, &CheckFnSig{
		name:          fnName,
		owner:         targetName,
		receiverTy:    targetTy,
		hasReceiver:   hasReceiver,
		retTy:         retTy,
		paramNames:    paramNames,
		paramTys:      paramTys,
		generics:      generics,
		genericBounds: bounds,
	})
}

func registerBytesIntrinsicMethods(env *CheckEnv, owner string, ty int, tys *TyArena) {
	tInt_ := tInt(tys)
	tBool_ := tBool(tys)
	tString_ := tString(tys)
	tBytes_ := tBytes(tys)
	tOptByte := tyOptional(tys, tByte(tys))
	tOptInt := tyOptional(tys, tInt_)
	tListBytes := tyNamed(tys, "List", []int{tBytes_})
	tResultStringError := tyNamed(tys, "Result", []int{tString_, tyNamed(tys, "Error", make([]int, 0, 1))})

	type pm struct {
		name       string
		retTy      int
		paramNames []string
		paramTys   []int
	}
	methods := []pm{
		{"len", tInt_, nil, nil},
		{"isEmpty", tBool_, nil, nil},
		{"get", tOptByte, []string{"i"}, []int{tInt_}},
		{"contains", tBool_, []string{"sub"}, []int{tBytes_}},
		{"startsWith", tBool_, []string{"prefix"}, []int{tBytes_}},
		{"endsWith", tBool_, []string{"suffix"}, []int{tBytes_}},
		{"indexOf", tOptInt, []string{"sub"}, []int{tBytes_}},
		{"lastIndexOf", tOptInt, []string{"sub"}, []int{tBytes_}},
		{"split", tListBytes, []string{"sep"}, []int{tBytes_}},
		{"join", tBytes_, []string{"parts"}, []int{tListBytes}},
		{"concat", tBytes_, []string{"other"}, []int{tBytes_}},
		{"repeat", tBytes_, []string{"n"}, []int{tInt_}},
		{"replace", tBytes_, []string{"old", "new"}, []int{tBytes_, tBytes_}},
		{"replaceAll", tBytes_, []string{"old", "new"}, []int{tBytes_, tBytes_}},
		{"trimLeft", tBytes_, []string{"strip"}, []int{tBytes_}},
		{"trimRight", tBytes_, []string{"strip"}, []int{tBytes_}},
		{"trim", tBytes_, []string{"strip"}, []int{tBytes_}},
		{"trimSpace", tBytes_, nil, nil},
		{"toUpper", tBytes_, nil, nil},
		{"toLower", tBytes_, nil, nil},
		{"toHex", tString_, nil, nil},
		{"slice", tBytes_, []string{"start", "end"}, []int{tInt_, tInt_}},
		{"toString", tResultStringError, nil, nil},
	}
	for _, m := range methods {
		checkRegisterFn(env, &CheckFnSig{
			name:          m.name,
			owner:         owner,
			receiverTy:    ty,
			hasReceiver:   true,
			retTy:         m.retTy,
			paramNames:    m.paramNames,
			paramTys:      m.paramTys,
			generics:      make([]string, 0, 1),
			genericBounds: make([]*CheckGenericBound, 0, 1),
		})
	}
}
