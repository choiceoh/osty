package bundle

// Bundles for the Osty-authored HIR / MIR tiers of the toolchain. These
// mirror `MergeToolchainLLVMGen` (see llvmgen_bundle.go) but cover the
// AST → HIR lowerer and the HIR → MIR lowerer that live in
// `toolchain/hir*.osty` and `toolchain/mir*.osty`.
//
// Both tiers share the checker bundle's `MergeFiles` plumbing (full
// stdlib-strings prelude + std-call normalization + while→for loop
// sugar), so they currently transpile under the same bootstrap rules
// as the checker. The smoke tests under `internal/selfhost/hir_gen_test.go`
// and `mir_gen_test.go` exercise these bundles end-to-end through
// `seedgen.Generate` → `go test`.

// hirExtensionFiles are the Osty-authored files layered on top of the
// checker bundle to make the HIR tier transpile cleanly. `hir.osty`
// defines the HIR node algebra; `hir_clone.osty` supplies deep-clone
// helpers; `monomorph_pass.osty` provides the `hirCloneType` /
// `hirCloneTypeList` primitives the clone layer calls; `pmcompile.osty`
// provides the match decision-tree compiler used by `hir_lower.osty`;
// `hir_lower.osty` is the AST → HIR lowerer plus annotation extractors.
//
// `ty.osty` (the arena-based type surface HIR lowering consumes) is
// already in the checker bundle, so we don't re-declare it here — the
// HIR bundle starts from `ToolchainCheckerFiles()` and appends.
var hirExtensionFiles = []string{
	"toolchain/hir.osty",
	"toolchain/hir_clone.osty",
	"toolchain/monomorph.osty",
	"toolchain/monomorph_rewrite.osty",
	"toolchain/monomorph_pass.osty",
	"toolchain/pmcompile.osty",
	"toolchain/hir_lower.osty",
}

// mirExtensionFiles layers the MIR tier on top of the HIR bundle. MIR
// lowering consumes HIR as input, so `hir*` must precede these files
// in the merge order.
var mirExtensionFiles = []string{
	"toolchain/mir.osty",
	"toolchain/mir_lower.osty",
	"toolchain/mir_optimize.osty",
	"toolchain/mir_validator.osty",
}

func toolchainHirFiles() []string {
	// Drop `ast_lower.osty` from the checker bundle — HIR lowering
	// does not consume the Osty-side AST bridge (it works off the
	// elab `TyArena`), and removing it means the merged source
	// transpiles to Go that does not import `astbridge`, letting the
	// smoke test compile with a fresh `go.mod`.
	base := ToolchainCheckerFiles()
	filtered := make([]string, 0, len(base)+len(hirExtensionFiles))
	for _, f := range base {
		if f == "internal/selfhost/ast_lower.osty" {
			continue
		}
		filtered = append(filtered, f)
	}
	return append(filtered, hirExtensionFiles...)
}

func toolchainMirFiles() []string {
	base := toolchainHirFiles()
	return append(base, mirExtensionFiles...)
}

// ToolchainHirFiles returns the HIR bundle members in dependency order:
// the checker bundle followed by the HIR-extension files.
func ToolchainHirFiles() []string {
	return toolchainHirFiles()
}

// ToolchainMirFiles returns the MIR bundle members in dependency order:
// the HIR bundle followed by the MIR-extension files.
func ToolchainMirFiles() []string {
	return toolchainMirFiles()
}

// MergeToolchainHir concatenates the HIR bundle through the shared
// `MergeFiles` path so it inherits the checker bundle's stdlib-strings
// prelude + call normalizer + while→for sugar lowering. Returns the
// merged source as a `[]byte` ready to be handed to `seedgen.Generate`.
func MergeToolchainHir(root string) ([]byte, error) {
	return MergeFiles(root, toolchainHirFiles())
}

// MergeToolchainMir concatenates the MIR bundle (HIR surface + MIR
// lowerer + passes) through `MergeFiles`. The resulting merged source
// can transpile with the bootstrap seedgen as long as every pattern
// exercised is already supported upstream — the companion smoke test
// pins that guarantee.
func MergeToolchainMir(root string) ([]byte, error) {
	return MergeFiles(root, toolchainMirFiles())
}
