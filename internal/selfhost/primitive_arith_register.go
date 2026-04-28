package selfhost

// installPrimitiveArithMethods registers the placeholder methods that
// `internal/stdlib/primitives/int.osty` declares behind
// `#[intrinsic_methods(Int, Int8, …, UInt64, Byte)]`. The legacy
// stdlib loader copies these method names into a Primitives map but
// the self-hosted checker — generated.go — does not consult that
// map, so without this registration `Int.abs()` and friends surface
// as `E0703 no method on type Int` and never reach the LLVM lowering
// in `internal/llvmgen/mir_generator.go:emitPrimitiveMethodCall`.
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
	}
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
	checkRegisterFn(env, &CheckFnSig{
		name:          name,
		owner:         owner,
		receiverTy:    ty,
		hasReceiver:   true,
		retTy:         tyNamed(tys, "Result", []int{tInt(tys), tyNamed(tys, "Error", make([]int, 0, 1))}),
		paramNames:    make([]string, 0, 1),
		paramTys:      make([]int, 0, 1),
		generics:      make([]string, 0, 1),
		genericBounds: make([]*CheckGenericBound, 0, 1),
	})
}
