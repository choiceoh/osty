package llvmgen

import (
	"github.com/osty/osty/internal/selfhost/bundle"
)

// ToolchainMIRGeneratorFiles returns the Osty-authored MIR emitter sources
// that can be transpiled through the bootstrap path today. The canonical
// file list lives in internal/selfhost/bundle for parity with the llvmgen
// support bundle.
func ToolchainMIRGeneratorFiles() []string {
	return bundle.ToolchainMIRGeneratorFiles()
}

// MergeToolchainMIRGenerator delegates to the shared mir-generator merger.
// Kept as a thin facade so llvmgen callers can import a single package for
// both merge entry points.
func MergeToolchainMIRGenerator(root string) ([]byte, error) {
	return bundle.MergeToolchainMIRGenerator(root)
}
