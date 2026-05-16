package stage0

import (
	"github.com/osty/osty/internal/ir"
)

// stage0KnownStdlibStructLayout returns a fallback layout for stdlib
// structs whose definitions live in `internal/stdlib/modules/*.osty`
// but whose layout records don't reach the user's MIR module. Stage0's
// struct projection emitters key on `module.Layouts.Structs[name]`;
// when the lookup misses, the caller falls back to this registry so
// fields with `FieldProj.Type = *ir.ErrType` still resolve to a real
// scalar.
//
// Entries should be additive — adding a new struct here unlocks the
// matchers without touching the upstream MIR pipeline. Each entry
// covers a struct whose layout is observable from a `pub struct`
// declaration and stable enough that an in-tree change to the stdlib
// source would update both halves together.
func stage0KnownStdlibStructLayout(name string) ([]scalarType, []string, bool) {
	switch name {
	case "ExecOutput":
		// stdlib `pub struct ExecOutput { exitCode: Int, stdout: String, stderr: String, timedOut: Bool }`
		// (internal/stdlib/modules/os.osty:13-18).
		return []scalarType{scalarInt, scalarString, scalarString, scalarBool},
			[]string{"exitCode", "stdout", "stderr", "timedOut"},
			true
	case "Output":
		// stdlib `pub struct Output { exitCode: Int, stdout: String, stderr: String }`
		// (internal/stdlib/modules/os.osty Output type used by `output(...)`).
		return []scalarType{scalarInt, scalarString, scalarString},
			[]string{"exitCode", "stdout", "stderr"},
			true
	}
	return nil, nil, false
}

// stage0KnownStdlibFieldIRType returns the IR-level Type for a known
// stdlib struct field, used when the caller needs an `ir.Type` (not
// just a scalar) to drive downstream lowering decisions (e.g. nested
// struct walks, list projection). The mapping mirrors the source
// definition; entries should stay in sync with
// stage0KnownStdlibStructLayout above.
func stage0KnownStdlibFieldIRType(name string, idx int) ir.Type {
	switch name {
	case "ExecOutput":
		switch idx {
		case 0:
			return &ir.PrimType{Kind: ir.PrimInt}
		case 1, 2:
			return &ir.PrimType{Kind: ir.PrimString}
		case 3:
			return &ir.PrimType{Kind: ir.PrimBool}
		}
	case "Output":
		switch idx {
		case 0:
			return &ir.PrimType{Kind: ir.PrimInt}
		case 1, 2:
			return &ir.PrimType{Kind: ir.PrimString}
		}
	}
	return nil
}

// registerKnownStdlibStructDef ensures the fallback layout is emitted
// as an LLVM `%<name> = type { ... }` before the first GEP into the
// struct. Re-uses `emitStructDef` so duplicate registration is a no-op.
func (m *moduleCtx) registerKnownStdlibStructDef(name string, fields []scalarType, _ []string) {
	if m == nil || name == "" || len(fields) == 0 {
		return
	}
	m.emitStructDef(name, fields)
}
