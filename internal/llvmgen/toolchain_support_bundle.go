package llvmgen

import (
	"github.com/osty/osty/internal/selfhost/bundle"
)

// ToolchainSupportFiles returns the Osty-authored llvmgen helper sources that
// can be transpiled through the bootstrap path today. The canonical file list
// now lives in internal/selfhost/bundle so the future selfhost llvmgen bridge
// and the current support snapshot regen share one merge contract.
func ToolchainSupportFiles() []string {
	return bundle.ToolchainLLVMGenFiles()
}

// MergeToolchainSupport prepends the minimal Go-hosted strings surface the
// bootstrap transpiler needs, then concatenates the llvmgen helper sources into
// one standalone .osty file.
func MergeToolchainSupport(root string) ([]byte, error) {
	return bundle.MergeToolchainLLVMGen(root)
}
