package llvmgen

import (
	"strings"

	"github.com/osty/osty/internal/selfhost/bundle"
)

// ToolchainSupportFiles returns the Osty-authored llvmgen helper sources that
// can be transpiled through the bootstrap path today. The canonical file list
// now lives in internal/selfhost/bundle so the future selfhost llvmgen bridge
// and the current support snapshot regen share one merge contract.
func ToolchainSupportFiles() []string {
	return bundle.ToolchainLLVMGenFiles()
}

// MergeToolchainSupport reuses the shared llvmgen support merger, then strips
// the native-owned entry slice so the transpiled support bundle does not
// redeclare helpers that already live in native_entry_snapshot.go.
func MergeToolchainSupport(root string) ([]byte, error) {
	merged, err := bundle.MergeToolchainLLVMGen(root)
	if err != nil {
		return nil, err
	}
	return []byte(stripLlvmNativeEntrySlice(string(merged))), nil
}

func stripLlvmNativeEntrySlice(src string) string {
	const startMarker = "pub enum LlvmNativeExprKind {"
	const endMarker = "/// Phase A4 fn-value thunk template."

	start := strings.Index(src, startMarker)
	if start < 0 {
		return src
	}
	end := strings.Index(src[start:], endMarker)
	if end < 0 {
		return src
	}
	end += start
	return src[:start] + src[end:]
}
