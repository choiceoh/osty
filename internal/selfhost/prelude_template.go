package selfhost

import "sync"

// The prelude is immutable after installation; each check materializes a fresh
// CheckEnv from this split template instead of rebuilding the same builtin
// signatures, lexical module aliases, and type arena.
var checkPreludeTemplateOnce sync.Once
var checkPreludeTemplate *checkPreludeTemplateSnapshot

type checkPreludeTemplateSnapshot struct {
	tys    *TyArena
	global CheckGlobalEnv
	local  CheckLocalEnv
}

func checkPreludeTemplateEnv() *checkPreludeTemplateSnapshot {
	checkPreludeTemplateOnce.Do(func() {
		tys := emptyTyArena()
		env := emptyCheckEnv(tys)
		checkInstallPrelude(env)
		checkPreludeTemplate = snapshotCheckPreludeTemplate(env)
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

func snapshotCheckPreludeTemplate(src *CheckEnv) *checkPreludeTemplateSnapshot {
	if src == nil {
		return nil
	}
	return &checkPreludeTemplateSnapshot{
		tys:    cloneTyArena(src.tys),
		global: cloneCheckGlobalEnv(src.global),
		local:  cloneCheckPreludeLocalEnv(src.local, src.tys),
	}
}

func cloneCheckGlobalEnv(src CheckGlobalEnv) CheckGlobalEnv {
	return CheckGlobalEnv{
		fns:                     append([]*CheckFnSig(nil), src.fns...),
		fnIndexKeys:             append([]string(nil), src.fnIndexKeys...),
		fnIndexHashes:           append([]int(nil), src.fnIndexHashes...),
		fnIndexValues:           append([]int(nil), src.fnIndexValues...),
		fnIndexSlots:            cloneCheckStringIntMap(src.fnIndexSlots),
		fnBodyKeys:              append([]string(nil), src.fnBodyKeys...),
		fnBodyHashes:            append([]int(nil), src.fnBodyHashes...),
		fields:                  append([]*CheckFieldSig(nil), src.fields...),
		fieldIndexKeys:          append([]string(nil), src.fieldIndexKeys...),
		fieldIndexHashes:        append([]int(nil), src.fieldIndexHashes...),
		fieldIndexValues:        append([]int(nil), src.fieldIndexValues...),
		variants:                append([]*CheckVariantSig(nil), src.variants...),
		variantIndexKeys:        append([]string(nil), src.variantIndexKeys...),
		variantIndexHashes:      append([]int(nil), src.variantIndexHashes...),
		variantIndexValues:      append([]int(nil), src.variantIndexValues...),
		variantOwnerIndexKeys:   append([]string(nil), src.variantOwnerIndexKeys...),
		variantOwnerIndexHashes: append([]int(nil), src.variantOwnerIndexHashes...),
		variantOwnerIndexValues: append([]int(nil), src.variantOwnerIndexValues...),
		aliases:                 append([]*CheckAliasSig(nil), src.aliases...),
		aliasIndexKeys:          append([]string(nil), src.aliasIndexKeys...),
		aliasIndexHashes:        append([]int(nil), src.aliasIndexHashes...),
		aliasIndexValues:        append([]int(nil), src.aliasIndexValues...),
		types:                   append([]*CheckTypeSig(nil), src.types...),
		typeIndexKeys:           append([]string(nil), src.typeIndexKeys...),
		typeIndexHashes:         append([]int(nil), src.typeIndexHashes...),
		typeIndexValues:         append([]int(nil), src.typeIndexValues...),
		interfaces:              append([]string(nil), src.interfaces...),
		interfaceIndexKeys:      append([]string(nil), src.interfaceIndexKeys...),
		interfaceIndexHashes:    append([]int(nil), src.interfaceIndexHashes...),
		interfaceExtends:        append([]*CheckInterfaceExt(nil), src.interfaceExtends...),
		importAliases:           append([]string(nil), src.importAliases...),
	}
}

func cloneCheckPreludeLocalEnv(src CheckLocalEnv, tys *TyArena) CheckLocalEnv {
	return CheckLocalEnv{
		bindings:                append([]*CheckBinding(nil), src.bindings...),
		bindingIndexNames:       append([]string(nil), src.bindingIndexNames...),
		bindingIndexHashes:      append([]int(nil), src.bindingIndexHashes...),
		bindingIndexStacks:      cloneCheckBindingStacks(src.bindingIndexStacks),
		genericBounds:           append([]*CheckGenericBound(nil), src.genericBounds...),
		genericBoundIndexNames:  append([]string(nil), src.genericBoundIndexNames...),
		genericBoundIndexHashes: append([]int(nil), src.genericBoundIndexHashes...),
		genericBoundIndexStacks: cloneCheckIntStacks(src.genericBoundIndexStacks),
		returnTy:                tErr(tys),
		fnName:                  "",
		inLoop:                  false,
	}
}

func clonePreludeCheckEnv(src *checkPreludeTemplateSnapshot) *CheckEnv {
	if src == nil {
		return emptyCheckEnv(emptyTyArena())
	}
	tys := cloneTyArena(src.tys)
	return &CheckEnv{
		tys:    tys,
		global: cloneCheckGlobalEnv(src.global),
		local:  cloneCheckPreludeLocalEnv(src.local, tys),
	}
}
