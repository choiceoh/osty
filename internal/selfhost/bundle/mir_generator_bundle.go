package bundle

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// toolchainMIRGeneratorFiles enumerates the Osty-authored MIR→LLVM emitter
// sources that are ported from the hand-written Go implementation in
// `internal/llvmgen/mir_generator.go`. The port is phased — each section
// of mir_generator.go moves here as it's rewritten, so the list grows
// over time until the Go file is empty and can be deleted.
var toolchainMIRGeneratorFiles = []string{
	"toolchain/mir_generator.osty",
}

// mirGeneratorStringsPrelude mirrors the llvmgen bundle's Go-hosted strings
// surface so the bootstrap transpiler can compile uses of `.contains` /
// `.hasPrefix` from the Osty source against Go's stdlib at the native
// boundary. The postprocess pass in gen_mir_generator_snapshot rewrites
// the ostyStrings* shims back to direct llvmStrings.* calls after transpile.
const mirGeneratorStringsPrelude = `use go "strings" as llvmStrings {
    fn Contains(s: String, substr: String) -> Bool
    fn HasPrefix(s: String, prefix: String) -> Bool
    fn HasSuffix(s: String, suffix: String) -> Bool
    fn Join(elems: List<String>, sep: String) -> String
    fn Split(s: String, sep: String) -> List<String>
    fn TrimPrefix(s: String, prefix: String) -> String
    fn Index(s: String, substr: String) -> Int
}

fn ostyStringsContains(s: String, substr: String) -> Bool { llvmStrings.Contains(s, substr) }
fn ostyStringsHasPrefix(s: String, prefix: String) -> Bool { llvmStrings.HasPrefix(s, prefix) }
fn ostyStringsHasSuffix(s: String, suffix: String) -> Bool { llvmStrings.HasSuffix(s, suffix) }
`

// ToolchainMIRGeneratorFiles returns the Osty-authored MIR emitter sources
// that can travel through the bootstrap transpiler. Returns a defensive copy
// so callers can't mutate the canonical list.
func ToolchainMIRGeneratorFiles() []string {
	return append([]string(nil), toolchainMIRGeneratorFiles...)
}

// MergeToolchainMIRGenerator prepends the bootstrap-transpile strings prelude,
// then concatenates the MIR-emitter Osty sources into one standalone .osty
// file ready for `seedgen.Generate`. Mirrors `MergeToolchainLLVMGen` — we
// keep the two bundles separate so the support snapshot stays stable while
// the mir-emitter port progresses.
func MergeToolchainMIRGenerator(root string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(mirGeneratorStringsPrelude)
	b.WriteByte('\n')

	for _, rel := range toolchainMIRGeneratorFiles {
		path := filepath.Join(root, filepath.FromSlash(rel))
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		b.WriteString("// ---- ")
		b.WriteString(rel)
		b.WriteString(" ----\n")
		trimmed := stripLeadingLLVMGenStringsUse(string(data))
		trimmed = normalizeLLVMGenStdStringsCalls(trimmed)
		b.WriteString(trimmed)
		if !strings.HasSuffix(trimmed, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	return []byte(b.String()), nil
}
