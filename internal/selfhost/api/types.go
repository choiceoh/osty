// Package api holds the pure data types that cross the selfhost
// boundary — return shapes for the bootstrapped Osty checker and
// resolver. They are factored out of `internal/selfhost` so that
// downstream consumers can depend on the types without pulling in
// the generated bootstrap core. The selfhost package itself re-exports
// every type here via Go type aliases, so existing callers continue to
// compile unchanged.
package api

// CheckSummary is the exported Go shape for the bootstrapped Osty checker.
//
// The self-hosted checker is authoritative for mainstream checker diagnostics
// and supplies structured expression, binding, declaration-symbol, and
// instantiation facts to the Go check.Result bridge.
type CheckSummary struct {
	Assignments int
	Accepted    int
	Errors      int
	// ErrorsByContext buckets error-severity diagnostics by the native
	// checker's stable bucket key. For the typed checker this is usually
	// the diagnostic code (for example E0700); consumed by
	// `osty check --dump-native-diags`.
	ErrorsByContext map[string]int
	// ErrorDetails optionally holds a second-level split under a given
	// bucket. For the typed checker this is the rendered diagnostic
	// message histogram underneath a code bucket.
	ErrorDetails map[string]map[string]int
}

// CheckedNode records a checked expression node and its inferred type name.
type CheckedNode struct {
	Node     int
	Kind     string
	TypeName string
	Start    int
	End      int
}

// CheckedBinding records a local binding that the bootstrapped checker typed.
type CheckedBinding struct {
	Node     int
	Name     string
	TypeName string
	Mutable  bool
	Start    int
	End      int
}

// CheckedSymbol records a declaration collected by the bootstrapped checker.
type CheckedSymbol struct {
	Node     int
	Kind     string
	Name     string
	Owner    string
	TypeName string
	Start    int
	End      int
}

// CheckInstantiation records a generic function or method instantiation.
type CheckInstantiation struct {
	Node       int
	Callee     string
	TypeArgs   []string
	ResultType string
	Start      int
	End        int
}

// CheckDiagnosticRecord is a structured diagnostic produced by the
// bootstrapped Osty checker (see toolchain/check_diag.osty). The host
// bridge lifts each record into a `*diag.Diagnostic` so policy gates
// authored in Osty surface through the ordinary `check.Result.Diags`
// channel. Start/End are token indices; the Go bridge converts to byte
// offsets via the lex stream.
type CheckDiagnosticRecord struct {
	Code     string
	Severity string
	Message  string
	Start    int
	End      int
	File     string
	Notes    []string
}

// CheckResult is the structured Go-facing surface for the bootstrapped checker.
type CheckResult struct {
	Summary        CheckSummary
	TypedNodes     []CheckedNode
	Bindings       []CheckedBinding
	Symbols        []CheckedSymbol
	Instantiations []CheckInstantiation
	Diagnostics    []CheckDiagnosticRecord
}

// ResolveSummary is the exported Go summary for the bootstrapped Osty
// resolver.
type ResolveSummary struct {
	Symbols           int
	Refs              int
	TypeRefs          int
	Diagnostics       int
	Unresolved        int
	Duplicates        int
	SymbolsByKind     map[string]int
	DiagnosticsByCode map[string]int
}

// ResolvedSymbol records one symbol declared by the self-host resolver.
type ResolvedSymbol struct {
	Node     int
	Name     string
	Kind     string
	TypeName string
	Arity    int
	Depth    int
	Start    int
	End      int
	Public   bool
	File     string
}

// ResolvedRef records one value/name reference plus its resolved target span
// when available.
type ResolvedRef struct {
	Name        string
	Node        int
	Start       int
	End         int
	File        string
	TargetNode  int
	TargetStart int
	TargetEnd   int
	TargetFile  string
}

// ResolvedTypeRef records one resolved type-name reference.
type ResolvedTypeRef struct {
	Name  string
	Node  int
	Start int
	End   int
	File  string
}

// ResolveDiagnosticRecord is one structured diagnostic produced by the
// self-host resolver.
type ResolveDiagnosticRecord struct {
	Code    string
	Message string
	Name    string
	Hint    string
	Node    int
	Start   int
	End     int
	File    string
}

// ResolveResult is the structured Go-facing surface for the bootstrapped
// resolver.
type ResolveResult struct {
	Summary     ResolveSummary
	Symbols     []ResolvedSymbol
	Refs        []ResolvedRef
	TypeRefs    []ResolvedTypeRef
	Diagnostics []ResolveDiagnosticRecord
}
