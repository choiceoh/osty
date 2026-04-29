package selfhost

func registerStdTestingPropertyAliasFns(env *CheckEnv, alias string) {
	tys := env.tys
	tString_ := tString(tys)
	tInt_ := tInt(tys)
	tBool_ := tBool(tys)
	tUnit_ := tUnit(tys)
	tT := tyNamed(tys, "T", make([]int, 0, 1))
	tGenT := tyNamed(tys, "Gen", []int{tT})
	tFnTBool := tyFn(tys, []int{tT}, tBool_)
	gT := []string{"T"}
	emptyBounds := make([]*CheckGenericBound, 0, 1)

	checkRegisterType(env, &CheckTypeSig{
		name:          "Gen",
		generics:      []string{"T"},
		genericBounds: emptyBounds,
		kind:          "struct",
	})
	checkRegisterFn(env, &CheckFnSig{
		name:          "property",
		owner:         alias,
		receiverTy:    -1,
		hasReceiver:   false,
		retTy:         tUnit_,
		paramNames:    []string{"name", "g", "pred"},
		paramTys:      []int{tString_, tGenT, tFnTBool},
		generics:      gT,
		genericBounds: emptyBounds,
	})
	checkRegisterFn(env, &CheckFnSig{
		name:          "propertyN",
		owner:         alias,
		receiverTy:    -1,
		hasReceiver:   false,
		retTy:         tUnit_,
		paramNames:    []string{"name", "g", "iterations", "pred"},
		paramTys:      []int{tString_, tGenT, tInt_, tFnTBool},
		generics:      gT,
		genericBounds: emptyBounds,
	})
	checkRegisterFn(env, &CheckFnSig{
		name:          "propertySeeded",
		owner:         alias,
		receiverTy:    -1,
		hasReceiver:   false,
		retTy:         tUnit_,
		paramNames:    []string{"name", "g", "seed", "pred"},
		paramTys:      []int{tString_, tGenT, tInt_, tFnTBool},
		generics:      gT,
		genericBounds: emptyBounds,
	})
}

func registerStdTestingGenAliasFns(env *CheckEnv, alias string) {
	tys := env.tys
	tInt_ := tInt(tys)
	tBool_ := tBool(tys)
	tString_ := tString(tys)
	tT := tyNamed(tys, "T", make([]int, 0, 1))
	tU := tyNamed(tys, "U", make([]int, 0, 1))
	tA := tyNamed(tys, "A", make([]int, 0, 1))
	tB := tyNamed(tys, "B", make([]int, 0, 1))
	tC := tyNamed(tys, "C", make([]int, 0, 1))
	tE := tyNamed(tys, "E", make([]int, 0, 1))
	tGenT := tyNamed(tys, "Gen", []int{tT})
	tGenU := tyNamed(tys, "Gen", []int{tU})
	tGenA := tyNamed(tys, "Gen", []int{tA})
	tGenB := tyNamed(tys, "Gen", []int{tB})
	tGenC := tyNamed(tys, "Gen", []int{tC})
	tGenE := tyNamed(tys, "Gen", []int{tE})
	emptyGenerics := make([]string, 0, 1)
	emptyBounds := make([]*CheckGenericBound, 0, 1)
	gT := []string{"T"}

	checkRegisterType(env, &CheckTypeSig{
		name:          "Gen",
		generics:      gT,
		genericBounds: emptyBounds,
		kind:          "struct",
	})
	for _, sig := range []*CheckFnSig{
		{name: "int", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tyNamed(tys, "Gen", []int{tInt_}), paramNames: make([]string, 0, 1), paramTys: make([]int, 0, 1), generics: emptyGenerics, genericBounds: emptyBounds},
		{name: "intRange", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tyNamed(tys, "Gen", []int{tInt_}), paramNames: []string{"lo", "hi"}, paramTys: []int{tInt_, tInt_}, generics: emptyGenerics, genericBounds: emptyBounds},
		{name: "bool", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tyNamed(tys, "Gen", []int{tBool_}), paramNames: make([]string, 0, 1), paramTys: make([]int, 0, 1), generics: emptyGenerics, genericBounds: emptyBounds},
		{name: "float", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tyNamed(tys, "Gen", []int{tFloat64(tys)}), paramNames: make([]string, 0, 1), paramTys: make([]int, 0, 1), generics: emptyGenerics, genericBounds: emptyBounds},
		{name: "char", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tyNamed(tys, "Gen", []int{tChar(tys)}), paramNames: make([]string, 0, 1), paramTys: make([]int, 0, 1), generics: emptyGenerics, genericBounds: emptyBounds},
		{name: "byte", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tyNamed(tys, "Gen", []int{tByte(tys)}), paramNames: make([]string, 0, 1), paramTys: make([]int, 0, 1), generics: emptyGenerics, genericBounds: emptyBounds},
		{name: "asciiString", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tyNamed(tys, "Gen", []int{tString_}), paramNames: []string{"maxLen"}, paramTys: []int{tInt_}, generics: emptyGenerics, genericBounds: emptyBounds},
	} {
		checkRegisterFn(env, sig)
	}
	checkRegisterFn(env, &CheckFnSig{name: "oneOf", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tGenT, paramNames: []string{"choices"}, paramTys: []int{tyNamed(tys, "List", []int{tT})}, generics: gT, genericBounds: emptyBounds})
	checkRegisterFn(env, &CheckFnSig{name: "map", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tGenU, paramNames: []string{"g", "f"}, paramTys: []int{tGenT, tyFn(tys, []int{tT}, tU)}, generics: []string{"T", "U"}, genericBounds: emptyBounds})
	checkRegisterFn(env, &CheckFnSig{name: "filter", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tGenT, paramNames: []string{"g", "pred"}, paramTys: []int{tGenT, tyFn(tys, []int{tT}, tBool_)}, generics: gT, genericBounds: emptyBounds})
	checkRegisterFn(env, &CheckFnSig{name: "pair", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tyNamed(tys, "Gen", []int{tyTuple(tys, []int{tA, tB})}), paramNames: []string{"a", "b"}, paramTys: []int{tGenA, tGenB}, generics: []string{"A", "B"}, genericBounds: emptyBounds})
	checkRegisterFn(env, &CheckFnSig{name: "triple", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tyNamed(tys, "Gen", []int{tyTuple(tys, []int{tA, tB, tC})}), paramNames: []string{"a", "b", "c"}, paramTys: []int{tGenA, tGenB, tGenC}, generics: []string{"A", "B", "C"}, genericBounds: emptyBounds})
	checkRegisterFn(env, &CheckFnSig{name: "list", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tyNamed(tys, "Gen", []int{tyNamed(tys, "List", []int{tT})}), paramNames: []string{"item", "maxLen"}, paramTys: []int{tGenT, tInt_}, generics: gT, genericBounds: emptyBounds})
	checkRegisterFn(env, &CheckFnSig{name: "listOfSize", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tyNamed(tys, "Gen", []int{tyNamed(tys, "List", []int{tT})}), paramNames: []string{"item", "size"}, paramTys: []int{tGenT, tInt_}, generics: gT, genericBounds: emptyBounds})
	checkRegisterFn(env, &CheckFnSig{name: "option", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tyNamed(tys, "Gen", []int{tyOptional(tys, tT)}), paramNames: []string{"item"}, paramTys: []int{tGenT}, generics: gT, genericBounds: emptyBounds})
	checkRegisterFn(env, &CheckFnSig{name: "result", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tyNamed(tys, "Gen", []int{tyNamed(tys, "Result", []int{tT, tE})}), paramNames: []string{"ok", "err"}, paramTys: []int{tGenT, tGenE}, generics: []string{"T", "E"}, genericBounds: emptyBounds})
	checkRegisterFn(env, &CheckFnSig{name: "constant", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tGenT, paramNames: []string{"value"}, paramTys: []int{tT}, generics: gT, genericBounds: emptyBounds})
	checkRegisterFn(env, &CheckFnSig{name: "oneOfGens", owner: alias, receiverTy: -1, hasReceiver: false, retTy: tGenT, paramNames: []string{"gens"}, paramTys: []int{tyNamed(tys, "List", []int{tGenT})}, generics: gT, genericBounds: emptyBounds})
}
