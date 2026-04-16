//go:build selfhostgen

package backend

import (
	"context"
	"fmt"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/resolve"
)

// Name is the stable user/tooling identifier for a code-generation backend.
// It is also the backend artifact directory name under .osty/out/<key>/.
type Name string

const (
	NameLLVM Name = "llvm"
)

// ParseName converts a CLI/config backend name into a Name.
func ParseName(s string) (Name, error) {
	switch Name(s) {
	case NameGo:
		return NameGo, nil
	case NameLLVM:
		return NameLLVM, nil
	default:
		return "", fmt.Errorf("unknown backend %q (want go or llvm)", s)
	}
}

func (n Name) String() string { return string(n) }

// Valid reports whether n is a known backend name.
func (n Name) Valid() bool {
	switch n {
	case NameGo, NameLLVM:
		return true
	default:
		return false
	}
}

// EmitMode describes how far a backend should drive code generation.
type EmitMode string

const (
	EmitLLVMIR EmitMode = "llvm-ir"
	EmitObject EmitMode = "object"
	EmitBinary EmitMode = "binary"
)

// ParseEmitMode converts a CLI/config emit mode into an EmitMode.
func ParseEmitMode(s string) (EmitMode, error) {
	switch EmitMode(s) {
	case EmitGoSource:
		return EmitGoSource, nil
	case EmitLLVMIR:
		return EmitLLVMIR, nil
	case EmitObject:
		return EmitObject, nil
	case EmitBinary:
		return EmitBinary, nil
	default:
		return "", fmt.Errorf("unknown emit mode %q (want go, llvm-ir, object, or binary)", s)
	}
}

func (m EmitMode) String() string { return string(m) }

// Valid reports whether m is a known emit mode.
func (m EmitMode) Valid() bool {
	switch m {
	case EmitGoSource, EmitLLVMIR, EmitObject, EmitBinary:
		return true
	default:
		return false
	}
}

// ValidFor reports whether backend n can produce emit mode m.
func (m EmitMode) ValidFor(n Name) bool {
	switch n {
	case NameGo:
		return m == EmitGoSource || m == EmitBinary
	case NameLLVM:
		return m == EmitLLVMIR || m == EmitObject || m == EmitBinary
	default:
		return false
	}
}

// ValidateEmit returns an error when emit mode m is not meaningful for backend
// n. CLI plumbing should call this before dispatching to a concrete backend.
func ValidateEmit(n Name, m EmitMode) error {
	if !n.Valid() {
		return fmt.Errorf("unknown backend %q", n)
	}
	if !m.Valid() {
		return fmt.Errorf("unknown emit mode %q", m)
	}
	if !m.ValidFor(n) {
		return fmt.Errorf("backend %q cannot emit %q", n, m)
	}
	return nil
}

// Backend is implemented by concrete code-generation backends.
type Backend interface {
	Name() Name
	Emit(context.Context, Request) (*Result, error)
}

// Entry is the type-checked source unit a backend should emit. It keeps the
// front-end products grouped under one backend-neutral contract.
type Entry struct {
	PackageName string
	SourcePath  string
	File        *ast.File
	Resolve     *resolve.Result
	Check       *check.Result
}

// Request is the backend-neutral build request shared by gen/build/run/test
// orchestration.
type Request struct {
	Layout     Layout
	Emit       EmitMode
	Entry      Entry
	BinaryName string
	Features   []string
}

// Artifacts returns the conventional artifact paths for backend n.
func (r Request) Artifacts(n Name) Artifacts {
	return r.Layout.Artifacts(n, r.BinaryName)
}

// Result is the backend-neutral execution result returned by concrete
// backends. Warnings are non-fatal lowering/runtime/toolchain observations.
type Result struct {
	Backend   Name
	Emit      EmitMode
	Artifacts Artifacts
	Warnings  []error
}
