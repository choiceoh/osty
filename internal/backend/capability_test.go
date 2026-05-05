package backend

import (
	"errors"
	"testing"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/llvmabi"
	"github.com/osty/osty/internal/mir"
)

func TestLLVMCapabilityMatrixReportsGoFFIBlocker(t *testing.T) {
	t.Parallel()

	entry := Entry{
		IR: &ir.Module{
			Decls: []ir.Decl{
				&ir.UseDecl{IsGoFFI: true, GoPath: "strings"},
			},
		},
		MIR: &mir.Module{},
	}
	matrix := NewLLVMCapabilityMatrix(entry, llvmabi.Options{UseMIR: true, EmitGC: true})

	row, ok := capabilityRow(matrix, CapabilityGoFFI)
	if !ok {
		t.Fatal("go FFI capability row missing")
	}
	if !row.HIRSupported {
		t.Fatal("go FFI HIRSupported = false, want true")
	}
	if row.MIRLowerable {
		t.Fatal("go FFI MIRLowerable = true, want false")
	}
	if row.LLVMEmittable {
		t.Fatal("go FFI LLVMEmittable = true, want false")
	}
	if row.RuntimeABIRequired {
		t.Fatal("go FFI RuntimeABIRequired = true, want false")
	}
	if row.FallbackAllowed {
		t.Fatal("go FFI FallbackAllowed = true, want false")
	}
	diag, blocker, ok := matrix.BlockingDiagnostic()
	if !ok {
		t.Fatal("BlockingDiagnostic not reported")
	}
	if got, want := blocker.DiagnosticKind, "go-ffi"; got != want {
		t.Fatalf("blocker.DiagnosticKind = %q, want %q", got, want)
	}
	if got, want := diag.Kind, "foreign-ffi"; got != want {
		t.Fatalf("diag.Kind = %q, want %q", got, want)
	}
}

func TestLLVMCapabilityMatrixRecordsRuntimeABIKnownness(t *testing.T) {
	t.Parallel()

	entry := Entry{
		IR: &ir.Module{
			Decls: []ir.Decl{
				&ir.UseDecl{IsRuntimeFFI: true, RuntimePath: "runtime.strings"},
				&ir.UseDecl{IsRuntimeFFI: true, RuntimePath: "runtime.unknown"},
			},
		},
		MIR: &mir.Module{},
	}
	matrix := NewLLVMCapabilityMatrix(entry, llvmabi.Options{UseMIR: true, EmitGC: true})

	rows := rowsByID(matrix, CapabilityRuntimeFFI)
	if got, want := len(rows), 2; got != want {
		t.Fatalf("runtime FFI row count = %d, want %d", got, want)
	}
	known := rowBySubject(rows, "runtime.strings")
	if known == nil {
		t.Fatal("runtime.strings capability row missing")
	}
	if !known.RuntimeABIRequired || !known.RuntimeABIKnown || !known.LLVMEmittable {
		t.Fatalf("runtime.strings row = %+v, want known ABI and LLVM-emittable", *known)
	}
	unknown := rowBySubject(rows, "runtime.unknown")
	if unknown == nil {
		t.Fatal("runtime.unknown capability row missing")
	}
	if !unknown.RuntimeABIRequired || unknown.RuntimeABIKnown || unknown.LLVMEmittable {
		t.Fatalf("runtime.unknown row = %+v, want required-but-unknown ABI blocker", *unknown)
	}
	diag, _, ok := matrix.BlockingDiagnostic()
	if !ok {
		t.Fatal("BlockingDiagnostic not reported")
	}
	if got, want := diag.Kind, "runtime-ffi"; got != want {
		t.Fatalf("diag.Kind = %q, want %q", got, want)
	}
}

func TestLLVMCapabilityMatrixRecordsHIRNodeCoverage(t *testing.T) {
	t.Parallel()

	entry := Entry{
		IR: &ir.Module{
			Decls: []ir.Decl{
				&ir.FnDecl{
					Name:   "answer",
					Return: ir.TInt,
					Body: &ir.Block{
						Result: &ir.IntLit{Text: "1", T: ir.TInt},
					},
				},
			},
		},
		MIR: &mir.Module{},
	}
	matrix := NewLLVMCapabilityMatrix(entry, llvmabi.Options{UseMIR: true, EmitGC: true})

	rows := rowsByID(matrix, CapabilityHIRNode)
	fn := rowBySubject(rows, "*ir.FnDecl")
	if fn == nil {
		t.Fatalf("HIR capability rows missing *ir.FnDecl: %+v", rows)
	}
	if got, want := fn.Count, 1; got != want {
		t.Fatalf("*ir.FnDecl Count = %d, want %d", got, want)
	}
	if !fn.HIRSupported || !fn.MIRLowerable || !fn.LLVMEmittable {
		t.Fatalf("*ir.FnDecl row = %+v, want supported through MIR and LLVM", *fn)
	}

	entry.MIRIssues = []error{errors.New("synthetic MIR lowering gap")}
	matrix = NewLLVMCapabilityMatrix(entry, llvmabi.Options{UseMIR: true, EmitGC: true})
	fn = rowBySubject(rowsByID(matrix, CapabilityHIRNode), "*ir.FnDecl")
	if fn == nil {
		t.Fatal("HIR capability row missing after MIR issue")
	}
	if !fn.HIRSupported || fn.MIRLowerable || fn.LLVMEmittable {
		t.Fatalf("*ir.FnDecl row with MIR issue = %+v, want HIR-only coverage", *fn)
	}
	issue, ok := capabilityRow(matrix, CapabilityMIRLowerIssue)
	if !ok {
		t.Fatal("MIR lowering issue capability row missing")
	}
	if !issue.FallbackAllowed {
		t.Fatalf("MIR lowering issue row = %+v, want fallback allowed", issue)
	}
	if _, _, ok := matrix.PreflightBlockingDiagnostic(); ok {
		t.Fatal("MIR lowering issue should not be a preflight blocker while fallback is allowed")
	}
}

func TestLLVMCapabilityMatrixSelectsDispatchRoute(t *testing.T) {
	t.Parallel()

	entry := Entry{
		IR:  &ir.Module{},
		MIR: &mir.Module{},
	}
	matrix := NewLLVMCapabilityMatrix(entry, llvmabi.Options{UseMIR: true, EmitGC: true})
	if got, want := matrix.DispatchRoute(), llvmDispatchMIRDirect; got != want {
		t.Fatalf("DispatchRoute = %q, want %q", got, want)
	}
	row, ok := capabilityRow(matrix, CapabilityMIRDirect)
	if !ok {
		t.Fatal("MIR route row missing")
	}
	if !row.HIRSupported || !row.MIRLowerable || !row.LLVMEmittable {
		t.Fatalf("MIR route row = %+v, want all pipeline stages ready", row)
	}
	if !row.RuntimeABIRequired || !row.RuntimeABIKnown {
		t.Fatalf("MIR route runtime ABI row = %+v, want required and known", row)
	}

	entry.MIR = nil
	matrix = NewLLVMCapabilityMatrix(entry, llvmabi.Options{UseMIR: true, EmitGC: true})
	if got, want := matrix.DispatchRoute(), llvmDispatchMIRDirect; got != want {
		t.Fatalf("DispatchRoute without MIR = %q, want %q", got, want)
	}
	diag, row, ok := matrix.RouteBlockingDiagnostic(llvmDispatchMIRDirect)
	if !ok {
		t.Fatal("missing-MIR route blocker not reported")
	}
	if row.ID != CapabilityMIREmit || row.Subject != "mir.module" {
		t.Fatalf("missing-MIR route blocker = %+v, want MIR module emit blocker", row)
	}
	if got, want := diag.Kind, "source-layout"; got != want {
		t.Fatalf("diag.Kind = %q, want %q", got, want)
	}
}

func TestLLVMCapabilityMatrixRecordsNativeOwnedRoute(t *testing.T) {
	t.Parallel()

	entry := Entry{
		IR:  &ir.Module{},
		MIR: &mir.Module{},
	}
	matrix := newLLVMDispatchCapabilityMatrix(entry, llvmabi.Options{UseMIR: true, EmitGC: true}, nil, EmitLLVMIR)
	row, ok := capabilityRow(matrix, CapabilityNativeOwned)
	if !ok {
		t.Fatal("native-owned row missing")
	}
	if !row.HIRSupported || !row.MIRLowerable || !row.LLVMEmittable || !row.FallbackAllowed {
		t.Fatalf("native-owned row = %+v, want HIR+MIR route with fallback allowed", row)
	}
	if !matrix.CanRoute(llvmDispatchNativeOwned) {
		t.Fatal("CanRoute(native-owned) = false, want true")
	}

	matrix = newLLVMDispatchCapabilityMatrix(entry, llvmabi.Options{UseMIR: true, EmitGC: true}, []string{"mir-backend"}, EmitLLVMIR)
	row, ok = capabilityRow(matrix, CapabilityNativeOwned)
	if !ok {
		t.Fatal("native-owned row missing when feature disables it")
	}
	if row.LLVMEmittable {
		t.Fatalf("native-owned row = %+v, want LLVMEmittable=false under mir-backend feature", row)
	}
	if matrix.CanRoute(llvmDispatchNativeOwned) {
		t.Fatal("CanRoute(native-owned) = true under mir-backend feature, want false")
	}
	if got, want := matrix.DispatchRoute(), llvmDispatchMIRDirect; got != want {
		t.Fatalf("DispatchRoute = %q, want %q", got, want)
	}
}


func capabilityRow(matrix CapabilityMatrix, id CapabilityID) (CapabilityRow, bool) {
	for _, row := range matrix.Rows() {
		if row.ID == id {
			return row, true
		}
	}
	return CapabilityRow{}, false
}

func rowsByID(matrix CapabilityMatrix, id CapabilityID) []CapabilityRow {
	var rows []CapabilityRow
	for _, row := range matrix.Rows() {
		if row.ID == id {
			rows = append(rows, row)
		}
	}
	return rows
}

func rowBySubject(rows []CapabilityRow, subject string) *CapabilityRow {
	for i := range rows {
		if rows[i].Subject == subject {
			return &rows[i]
		}
	}
	return nil
}
