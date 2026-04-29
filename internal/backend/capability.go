package backend

import (
	"fmt"
	"sort"

	"github.com/osty/osty/internal/ir"
	"github.com/osty/osty/internal/llvmgen"
)

// CapabilityID names one explicit backend coverage row. Rows are intentionally
// small and data-shaped so dispatch policy can be audited without reading the
// emitter control flow.
type CapabilityID string

const (
	CapabilityGoFFI         CapabilityID = "hir.use.go-ffi"
	CapabilityHIRNode       CapabilityID = "hir.node"
	CapabilityMIRLowerIssue CapabilityID = "mir.lower.issue"
	CapabilityMIREmit       CapabilityID = "mir.emit"
	CapabilityRuntimeFFI    CapabilityID = "hir.use.runtime-ffi"
	CapabilityNativeOwned   CapabilityID = "backend.llvm.native-owned"
	CapabilityMIRDirect     CapabilityID = "backend.llvm.mir-direct"
)

// CapabilityRow records whether one source/backend shape can cross each
// pipeline boundary. RuntimeABIKnown is meaningful only when
// RuntimeABIRequired is true.
type CapabilityRow struct {
	ID                 CapabilityID
	Subject            string
	HIRSupported       bool
	MIRLowerable       bool
	LLVMEmittable      bool
	RuntimeABIRequired bool
	RuntimeABIKnown    bool
	FallbackAllowed    bool
	DiagnosticKind     string
	DiagnosticDetail   string
	Diagnostic         llvmgen.UnsupportedDiagnostic
	Route              string
	Count              int
}

// CapabilityMatrix is the explicit HIR -> MIR -> LLVM coverage contract for
// one backend entry.
type CapabilityMatrix struct {
	rows []CapabilityRow
}

// NewLLVMCapabilityMatrix builds the coverage matrix used by LLVM backend
// dispatch. The matrix is deliberately derived from Entry rather than the AST:
// backend policy should be stated over the backend-neutral contract.
func NewLLVMCapabilityMatrix(entry Entry, opts llvmgen.Options) CapabilityMatrix {
	return newLLVMCapabilityMatrix(entry, opts, nil, "", false)
}

func newLLVMDispatchCapabilityMatrix(entry Entry, opts llvmgen.Options, features []string, emit EmitMode) CapabilityMatrix {
	return newLLVMCapabilityMatrix(entry, opts, features, emit, true)
}

func newLLVMCapabilityMatrix(entry Entry, opts llvmgen.Options, features []string, emit EmitMode, includeNativeRoute bool) CapabilityMatrix {
	rows := make([]CapabilityRow, 0, 16)
	rows = appendHIRNodeCapabilities(rows, entry)
	rows = appendIRUseCapabilities(rows, entry.IR)
	rows = appendMIRLoweringCapabilities(rows, entry)
	rows = appendLLVMRouteCapabilities(rows, entry, opts, features, emit, includeNativeRoute)
	rows = appendMIREmitCapabilities(rows, entry, opts)
	return CapabilityMatrix{rows: rows}
}

// Rows returns a stable snapshot of the matrix rows.
func (m CapabilityMatrix) Rows() []CapabilityRow {
	return append([]CapabilityRow(nil), m.rows...)
}

// BlockingDiagnostic returns the first row that cannot be emitted and cannot
// legally fall back to another backend path.
func (m CapabilityMatrix) BlockingDiagnostic() (llvmgen.UnsupportedDiagnostic, CapabilityRow, bool) {
	for _, row := range m.rows {
		if !rowHasDiagnostic(row) || row.LLVMEmittable || row.FallbackAllowed {
			continue
		}
		return rowDiagnostic(row), row, true
	}
	return llvmgen.UnsupportedDiagnostic{}, CapabilityRow{}, false
}

func (m CapabilityMatrix) PreflightBlockingDiagnostic() (llvmgen.UnsupportedDiagnostic, CapabilityRow, bool) {
	for _, row := range m.rows {
		if row.Route != "" || row.FallbackAllowed || row.LLVMEmittable {
			continue
		}
		if !rowHasDiagnostic(row) {
			continue
		}
		return rowDiagnostic(row), row, true
	}
	return llvmgen.UnsupportedDiagnostic{}, CapabilityRow{}, false
}

func (m CapabilityMatrix) RouteBlockingDiagnostic(route llvmDispatchRoute) (llvmgen.UnsupportedDiagnostic, CapabilityRow, bool) {
	for _, row := range m.rows {
		if row.Route != string(route) || row.FallbackAllowed || row.LLVMEmittable {
			continue
		}
		if !rowHasDiagnostic(row) {
			continue
		}
		return rowDiagnostic(row), row, true
	}
	return llvmgen.UnsupportedDiagnostic{}, CapabilityRow{}, false
}

func (m CapabilityMatrix) DispatchRoute() llvmDispatchRoute {
	for _, row := range m.rows {
		if row.Route == string(llvmDispatchMIRDirect) {
			return llvmDispatchMIRDirect
		}
	}
	for _, row := range m.rows {
		if row.Route == "" || !row.LLVMEmittable {
			continue
		}
		if row.Route == string(llvmDispatchNativeOwned) {
			continue
		}
		return llvmDispatchRoute(row.Route)
	}
	return llvmDispatchMIRDirect
}

func (m CapabilityMatrix) CanRoute(route llvmDispatchRoute) bool {
	for _, row := range m.rows {
		if row.Route == string(route) && row.LLVMEmittable {
			return true
		}
	}
	return false
}

func rowHasDiagnostic(row CapabilityRow) bool {
	return row.Diagnostic.Code != "" || row.Diagnostic.Kind != "" || row.Diagnostic.Message != "" || row.DiagnosticKind != ""
}

func rowDiagnostic(row CapabilityRow) llvmgen.UnsupportedDiagnostic {
	if row.Diagnostic.Code != "" || row.Diagnostic.Kind != "" || row.Diagnostic.Message != "" {
		return row.Diagnostic
	}
	return llvmgen.UnsupportedDiagnosticFor(row.DiagnosticKind, row.DiagnosticDetail)
}

func appendHIRNodeCapabilities(rows []CapabilityRow, entry Entry) []CapabilityRow {
	if entry.IR == nil {
		return rows
	}
	counts := map[string]int{}
	ir.Walk(ir.VisitorFunc(func(n ir.Node) bool {
		if n == nil {
			return true
		}
		counts[fmt.Sprintf("%T", n)]++
		return true
	}), entry.IR)
	keys := make([]string, 0, len(counts))
	for key := range counts {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	mirReady := entry.MIR != nil && !hasMIRLoweringIssues(entry)
	for _, key := range keys {
		rows = append(rows, CapabilityRow{
			ID:            CapabilityHIRNode,
			Subject:       key,
			HIRSupported:  true,
			MIRLowerable:  mirReady,
			LLVMEmittable: mirReady,
			Count:         counts[key],
		})
	}
	return rows
}

func hasMIRLoweringIssues(entry Entry) bool {
	for _, issue := range entry.MIRIssues {
		if issue != nil {
			return true
		}
	}
	return false
}

func appendIRUseCapabilities(rows []CapabilityRow, mod *ir.Module) []CapabilityRow {
	if mod == nil {
		return rows
	}
	for _, decl := range mod.Decls {
		use, ok := decl.(*ir.UseDecl)
		if !ok || use == nil {
			continue
		}
		if use.IsGoFFI {
			rows = append(rows, CapabilityRow{
				ID:               CapabilityGoFFI,
				Subject:          use.GoPath,
				HIRSupported:     true,
				MIRLowerable:     false,
				LLVMEmittable:    false,
				FallbackAllowed:  false,
				DiagnosticKind:   "go-ffi",
				DiagnosticDetail: use.GoPath,
			})
			continue
		}
		if use.IsRuntimeFFI {
			known := llvmgen.IsKnownRuntimeFFIPath(use.RuntimePath)
			row := CapabilityRow{
				ID:                 CapabilityRuntimeFFI,
				Subject:            use.RuntimePath,
				HIRSupported:       true,
				MIRLowerable:       known,
				LLVMEmittable:      known,
				RuntimeABIRequired: true,
				RuntimeABIKnown:    known,
				FallbackAllowed:    false,
			}
			if !known {
				row.DiagnosticKind = "runtime-ffi"
				row.DiagnosticDetail = use.RuntimePath
			}
			rows = append(rows, row)
		}
	}
	return rows
}

func appendMIRLoweringCapabilities(rows []CapabilityRow, entry Entry) []CapabilityRow {
	for i, issue := range entry.MIRIssues {
		if issue == nil {
			continue
		}
		rows = append(rows, CapabilityRow{
			ID:               CapabilityMIRLowerIssue,
			Subject:          fmt.Sprintf("mir.lower.issue:%d", i),
			HIRSupported:     entry.IR != nil,
			MIRLowerable:     false,
			LLVMEmittable:    false,
			FallbackAllowed:  true,
			DiagnosticKind:   "unsupported-source",
			DiagnosticDetail: issue.Error(),
			Count:            1,
		})
	}
	return rows
}

func appendLLVMRouteCapabilities(rows []CapabilityRow, entry Entry, opts llvmgen.Options, features []string, emit EmitMode, includeNativeRoute bool) []CapabilityRow {
	hirReady := entry.IR != nil
	if includeNativeRoute {
		nativeReady := hirReady && useNativeOwnedLLVMIR(features, emit) && !hasInjectedStdlibBodies(entry.IR)
		rows = append(rows, CapabilityRow{
			ID:              CapabilityNativeOwned,
			Subject:         string(llvmDispatchNativeOwned),
			HIRSupported:    hirReady,
			MIRLowerable:    false,
			LLVMEmittable:   nativeReady,
			FallbackAllowed: true,
			Route:           string(llvmDispatchNativeOwned),
		})
	}
	mirReady := entry.MIR != nil
	rows = append(rows, CapabilityRow{
		ID:                 CapabilityMIRDirect,
		Subject:            string(llvmDispatchMIRDirect),
		HIRSupported:       hirReady,
		MIRLowerable:       mirReady,
		LLVMEmittable:      hirReady && mirReady,
		RuntimeABIRequired: opts.EmitGC,
		RuntimeABIKnown:    opts.EmitGC,
		FallbackAllowed:    false,
		Route:              string(llvmDispatchMIRDirect),
	})
	return rows
}

func appendMIREmitCapabilities(rows []CapabilityRow, entry Entry, opts llvmgen.Options) []CapabilityRow {
	if entry.MIR == nil && !opts.UseMIR {
		return rows
	}
	for _, reportRow := range llvmgen.MIRCapabilityReport(entry.MIR, opts) {
		row := CapabilityRow{
			ID:                 CapabilityMIREmit,
			Subject:            reportRow.Subject,
			HIRSupported:       entry.IR != nil,
			MIRLowerable:       entry.MIR != nil,
			LLVMEmittable:      reportRow.LLVMEmittable,
			RuntimeABIRequired: reportRow.RuntimeABIRequired,
			RuntimeABIKnown:    reportRow.RuntimeABIKnown,
			Diagnostic:         reportRow.Diagnostic,
			Route:              string(llvmDispatchMIRDirect),
			Count:              1,
		}
		rows = append(rows, row)
	}
	return rows
}
