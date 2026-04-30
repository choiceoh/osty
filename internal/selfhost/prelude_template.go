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

func cloneCheckNameIndexTable(src CheckNameIndexTable) CheckNameIndexTable {
	return CheckNameIndexTable{
		keys:   append([]string(nil), src.keys...),
		hashes: append([]int(nil), src.hashes...),
		values: append([]int(nil), src.values...),
		slots:  cloneCheckStringIntMap(src.slots),
	}
}

func cloneCheckNameSetTable(src CheckNameSetTable) CheckNameSetTable {
	return CheckNameSetTable{
		keys:   append([]string(nil), src.keys...),
		hashes: append([]int(nil), src.hashes...),
		slots:  cloneCheckStringIntMap(src.slots),
	}
}

func cloneCheckBindingStackIndex(src CheckBindingStackIndex) CheckBindingStackIndex {
	return CheckBindingStackIndex{
		keys:   append([]string(nil), src.keys...),
		hashes: append([]int(nil), src.hashes...),
		stacks: cloneCheckBindingStacks(src.stacks),
		slots:  cloneCheckStringIntMap(src.slots),
	}
}

func cloneCheckIntStackIndex(src CheckIntStackIndex) CheckIntStackIndex {
	return CheckIntStackIndex{
		keys:   append([]string(nil), src.keys...),
		hashes: append([]int(nil), src.hashes...),
		stacks: cloneCheckIntStacks(src.stacks),
		slots:  cloneCheckStringIntMap(src.slots),
	}
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
		fns:               append([]*CheckFnSig(nil), src.fns...),
		fnIndex:           cloneCheckNameIndexTable(src.fnIndex),
		fnBodyIndex:       cloneCheckNameSetTable(src.fnBodyIndex),
		fields:            append([]*CheckFieldSig(nil), src.fields...),
		fieldIndex:        cloneCheckNameIndexTable(src.fieldIndex),
		variants:          append([]*CheckVariantSig(nil), src.variants...),
		variantIndex:      cloneCheckNameIndexTable(src.variantIndex),
		variantOwnerIndex: cloneCheckNameIndexTable(src.variantOwnerIndex),
		aliases:           append([]*CheckAliasSig(nil), src.aliases...),
		aliasIndex:        cloneCheckNameIndexTable(src.aliasIndex),
		types:             append([]*CheckTypeSig(nil), src.types...),
		typeIndex:         cloneCheckNameIndexTable(src.typeIndex),
		interfaces:        append([]string(nil), src.interfaces...),
		interfaceIndex:    cloneCheckNameSetTable(src.interfaceIndex),
		interfaceExtends:  append([]*CheckInterfaceExt(nil), src.interfaceExtends...),
		importAliases:     append([]string(nil), src.importAliases...),
	}
}

func cloneCheckPreludeLocalEnv(src CheckLocalEnv, tys *TyArena) CheckLocalEnv {
	return CheckLocalEnv{
		bindings:          append([]*CheckBinding(nil), src.bindings...),
		bindingIndex:      cloneCheckBindingStackIndex(src.bindingIndex),
		genericBounds:     append([]*CheckGenericBound(nil), src.genericBounds...),
		genericBoundIndex: cloneCheckIntStackIndex(src.genericBoundIndex),
		returnTy:          tErr(tys),
		fnName:            "",
		inLoop:            false,
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
