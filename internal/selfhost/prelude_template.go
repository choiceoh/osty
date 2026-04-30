package selfhost

import "sync"

// The prelude is immutable after installation; each check clones this template
// instead of rebuilding the same builtin signatures and type arena.
var checkPreludeTemplateOnce sync.Once
var checkPreludeTemplate *CheckEnv

func checkPreludeTemplateEnv() *CheckEnv {
	checkPreludeTemplateOnce.Do(func() {
		tys := emptyTyArena()
		env := emptyCheckEnv(tys)
		checkInstallPrelude(env)
		checkPreludeTemplate = env
	})
	return checkPreludeTemplate
}

func cloneTyArena(src *TyArena) *TyArena {
	if src == nil {
		return nil
	}
	out := *src
	out.nodes = append([]*TyNode(nil), src.nodes...)
	out.internKeys = append([]string(nil), src.internKeys...)
	out.internHashes = append([]int(nil), src.internHashes...)
	out.internValues = append([]int(nil), src.internValues...)
	return &out
}

func cloneCheckIntStacks(in [][]int) [][]int {
	if len(in) == 0 {
		return nil
	}
	out := make([][]int, len(in))
	for i := range in {
		out[i] = append([]int(nil), in[i]...)
	}
	return out
}

func cloneCheckBindingStacks(in [][]*CheckBinding) [][]*CheckBinding {
	if len(in) == 0 {
		return nil
	}
	out := make([][]*CheckBinding, len(in))
	for i := range in {
		out[i] = append([]*CheckBinding(nil), in[i]...)
	}
	return out
}

func cloneCheckStringIntMap(in map[string]int) map[string]int {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func clonePreludeCheckEnv(src *CheckEnv) *CheckEnv {
	if src == nil {
		return emptyCheckEnv(emptyTyArena())
	}
	out := *src
	out.tys = cloneTyArena(src.tys)
	out.bindings = append([]*CheckBinding(nil), src.bindings...)
	out.bindingIndexNames = append([]string(nil), src.bindingIndexNames...)
	out.bindingIndexHashes = append([]int(nil), src.bindingIndexHashes...)
	out.bindingIndexStacks = cloneCheckBindingStacks(src.bindingIndexStacks)
	out.fns = append([]*CheckFnSig(nil), src.fns...)
	out.fnIndexKeys = append([]string(nil), src.fnIndexKeys...)
	out.fnIndexHashes = append([]int(nil), src.fnIndexHashes...)
	out.fnIndexValues = append([]int(nil), src.fnIndexValues...)
	out.fnIndexSlots = cloneCheckStringIntMap(src.fnIndexSlots)
	out.fnBodyKeys = append([]string(nil), src.fnBodyKeys...)
	out.fnBodyHashes = append([]int(nil), src.fnBodyHashes...)
	out.fields = append([]*CheckFieldSig(nil), src.fields...)
	out.fieldIndexKeys = append([]string(nil), src.fieldIndexKeys...)
	out.fieldIndexHashes = append([]int(nil), src.fieldIndexHashes...)
	out.fieldIndexValues = append([]int(nil), src.fieldIndexValues...)
	out.variants = append([]*CheckVariantSig(nil), src.variants...)
	out.variantIndexKeys = append([]string(nil), src.variantIndexKeys...)
	out.variantIndexHashes = append([]int(nil), src.variantIndexHashes...)
	out.variantIndexValues = append([]int(nil), src.variantIndexValues...)
	out.variantOwnerIndexKeys = append([]string(nil), src.variantOwnerIndexKeys...)
	out.variantOwnerIndexHashes = append([]int(nil), src.variantOwnerIndexHashes...)
	out.variantOwnerIndexValues = append([]int(nil), src.variantOwnerIndexValues...)
	out.aliases = append([]*CheckAliasSig(nil), src.aliases...)
	out.aliasIndexKeys = append([]string(nil), src.aliasIndexKeys...)
	out.aliasIndexHashes = append([]int(nil), src.aliasIndexHashes...)
	out.aliasIndexValues = append([]int(nil), src.aliasIndexValues...)
	out.types = append([]*CheckTypeSig(nil), src.types...)
	out.typeIndexKeys = append([]string(nil), src.typeIndexKeys...)
	out.typeIndexHashes = append([]int(nil), src.typeIndexHashes...)
	out.typeIndexValues = append([]int(nil), src.typeIndexValues...)
	out.interfaces = append([]string(nil), src.interfaces...)
	out.interfaceIndexKeys = append([]string(nil), src.interfaceIndexKeys...)
	out.interfaceIndexHashes = append([]int(nil), src.interfaceIndexHashes...)
	out.interfaceExtends = append([]*CheckInterfaceExt(nil), src.interfaceExtends...)
	out.importAliases = append([]string(nil), src.importAliases...)
	out.genericBounds = append([]*CheckGenericBound(nil), src.genericBounds...)
	out.genericBoundIndexNames = append([]string(nil), src.genericBoundIndexNames...)
	out.genericBoundIndexHashes = append([]int(nil), src.genericBoundIndexHashes...)
	out.genericBoundIndexStacks = cloneCheckIntStacks(src.genericBoundIndexStacks)
	out.aliasDeepCache = append([]int(nil), src.aliasDeepCache...)
	out.substCacheKeys = append([]string(nil), src.substCacheKeys...)
	out.substCacheHashes = append([]int(nil), src.substCacheHashes...)
	out.substCacheValues = append([]int(nil), src.substCacheValues...)
	out.diagnostics = append([]*CheckDiagnostic(nil), src.diagnostics...)
	out.bindingRecords = append([]*CheckBindingRecord(nil), src.bindingRecords...)
	out.symbolRecords = append([]*CheckSymbolRecord(nil), src.symbolRecords...)
	out.instantiations = append([]*CheckInstantiationRecord(nil), src.instantiations...)
	return &out
}
