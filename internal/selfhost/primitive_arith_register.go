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
// The five methods are all `Self`-shaped, so the registration just
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
	}
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
