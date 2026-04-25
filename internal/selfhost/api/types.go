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
//
// JSON tags mirror the wire contract used by cmd/osty-native-checker and
// internal/check's host-boundary exec path so the same struct can travel
// both in-process and across the subprocess edge.
type CheckSummary struct {
	Assignments int `json:"assignments"`
	Accepted    int `json:"accepted"`
	Errors      int `json:"errors"`
	// ErrorsByContext buckets error-severity diagnostics by the native
	// checker's stable bucket key. For the typed checker this is usually
	// the diagnostic code (for example E0700); consumed by
	// `osty check --dump-native-diags`.
	ErrorsByContext map[string]int `json:"errorsByContext,omitempty"`
	// ErrorDetails optionally holds a second-level split under a given
	// bucket. For the typed checker this is the rendered diagnostic
	// message histogram underneath a code bucket.
	ErrorDetails map[string]map[string]int `json:"errorDetails,omitempty"`
}

// TypeRepr is a structured type representation that replaces the former
// string-based typeName round-trip between the Osty checker and the Go
// host bridge. The Osty side emits a TypeRepr for every typed node; the
// Go side converts it directly to types.Type without string parsing.
type TypeRepr struct {
	Kind   string     `json:"kind"`             // "primitive", "named", "tuple", "optional", "fn", "unit", "never", "typevar", "self", "error", "poison"
	Name   string     `json:"name,omitempty"`   // primitive/named/typevar name: "Int", "List", "T"
	Path   string     `json:"path,omitempty"`   // qualified path (reserved, currently empty)
	Args   []TypeRepr `json:"args,omitempty"`   // named generic args / tuple elems / fn params
	Return *TypeRepr  `json:"return,omitempty"` // fn return type / optional inner
}

// String renders a TypeRepr back to a human-readable type string matching the
// format produced by the Osty type printer (e.g. "List<Int>", "fn(T) -> T",
// "(Int, String)", "Int?"). This is used by diagnostic printers, CLI dump
// commands, and query helpers that still operate on rendered type names.
func (tr *TypeRepr) String() string {
	if tr == nil {
		return ""
	}
	switch tr.Kind {
	case "primitive", "named", "typevar", "error":
		if len(tr.Args) == 0 {
			if tr.Name == "" {
				switch tr.Kind {
				case "error":
					return "Invalid"
				default:
					return ""
				}
			}
			return tr.Name
		}
		parts := make([]string, 0, len(tr.Args))
		for i := range tr.Args {
			parts = append(parts, tr.Args[i].String())
		}
		return tr.Name + "<" + joinTypeReprStrings(parts, ", ") + ">"
	case "unit":
		return "()"
	case "never":
		return "Never"
	case "optional":
		if tr.Return != nil {
			return tr.Return.String() + "?"
		}
		return "()?"
	case "tuple":
		if len(tr.Args) == 0 {
			return "()"
		}
		if len(tr.Args) == 1 {
			return "(" + tr.Args[0].String() + ")"
		}
		parts := make([]string, 0, len(tr.Args))
		for i := range tr.Args {
			parts = append(parts, tr.Args[i].String())
		}
		return "(" + joinTypeReprStrings(parts, ", ") + ")"
	case "fn":
		parts := make([]string, 0, len(tr.Args))
		for i := range tr.Args {
			parts = append(parts, tr.Args[i].String())
		}
		s := "fn(" + joinTypeReprStrings(parts, ", ") + ")"
		if tr.Return != nil {
			s += " -> " + tr.Return.String()
		} else {
			s += " -> ()"
		}
		return s
	case "self":
		return "Self"
	case "poison":
		return "Poison"
	default:
		if tr.Name != "" {
			return tr.Name
		}
		return ""
	}
}

func joinTypeReprStrings(ss []string, sep string) string {
	if len(ss) == 0 {
		return ""
	}
	result := ss[0]
	for _, s := range ss[1:] {
		result += sep + s
	}
	return result
}

// CheckedNode records a checked expression node and its inferred type name.
type CheckedNode struct {
	Node  int       `json:"node"`
	Kind  string    `json:"kind"`
	Type  *TypeRepr `json:"type"`
	Start int       `json:"start"`
	End   int       `json:"end"`
}

// CheckedBinding records a local binding that the bootstrapped checker typed.
type CheckedBinding struct {
	Node    int       `json:"node"`
	Name    string    `json:"name"`
	Type    *TypeRepr `json:"type"`
	Mutable bool      `json:"mutable"`
	Start   int       `json:"start"`
	End     int       `json:"end"`
}

// CheckedSymbol records a declaration collected by the bootstrapped checker.
type CheckedSymbol struct {
	Node  int       `json:"node"`
	Kind  string    `json:"kind"`
	Name  string    `json:"name"`
	Owner string    `json:"owner"`
	Type  *TypeRepr `json:"type"`
	Start int       `json:"start"`
	End   int       `json:"end"`
}

// CheckInstantiation records a generic function or method instantiation.
type CheckInstantiation struct {
	Node       int        `json:"node"`
	Callee     string     `json:"callee"`
	TypeArgs   []TypeRepr `json:"typeArgs"`
	ResultType *TypeRepr  `json:"resultType,omitempty"`
	Start      int        `json:"start"`
	End        int        `json:"end"`
}

// CheckDiagnosticRecord is a structured diagnostic produced by the
// bootstrapped Osty checker (see toolchain/check_diag.osty). The host
// bridge lifts each record into a `*diag.Diagnostic` so policy gates
// authored in Osty surface through the ordinary `check.Result.Diags`
// channel. Start/End are token indices; the Go bridge converts to byte
// offsets via the lex stream.
type CheckDiagnosticRecord struct {
	Code     string   `json:"code"`
	Severity string   `json:"severity"`
	Message  string   `json:"message"`
	Start    int      `json:"start"`
	End      int      `json:"end"`
	File     string   `json:"file,omitempty"`
	Notes    []string `json:"notes,omitempty"`
}

// CheckResult is the structured Go-facing surface for the bootstrapped checker.
type CheckResult struct {
	Summary        CheckSummary            `json:"summary"`
	TypedNodes     []CheckedNode           `json:"typedNodes"`
	Bindings       []CheckedBinding        `json:"bindings"`
	Symbols        []CheckedSymbol         `json:"symbols"`
	Instantiations []CheckInstantiation    `json:"instantiations"`
	Diagnostics    []CheckDiagnosticRecord `json:"diagnostics,omitempty"`
}

// CheckRequest is the wire shape consumed by the cmd/osty-native-checker
// subprocess entry point. Exactly one of Source / Package should be set.
// Included in api so host callers and the native-checker binary share the
// same struct declaration.
type CheckRequest struct {
	Source  string             `json:"source,omitempty"`
	Package *PackageCheckInput `json:"package,omitempty"`
}

// ResolveSummary is the exported Go summary for the bootstrapped Osty
// resolver.
//
// JSON tags mirror the cmd/osty-native-resolver wire format so the
// struct travels both in-process and across the subprocess edge
// without a translation layer.
type ResolveSummary struct {
	Symbols           int            `json:"symbols"`
	Refs              int            `json:"refs"`
	TypeRefs          int            `json:"typeRefs"`
	Diagnostics       int            `json:"diagnostics"`
	Unresolved        int            `json:"unresolved"`
	Duplicates        int            `json:"duplicates"`
	SymbolsByKind     map[string]int `json:"symbolsByKind,omitempty"`
	DiagnosticsByCode map[string]int `json:"diagnosticsByCode,omitempty"`
}

// ResolvedSymbol records one symbol declared by the self-host resolver.
type ResolvedSymbol struct {
	Node   int       `json:"node"`
	Name   string    `json:"name"`
	Kind   string    `json:"kind"`
	Type   *TypeRepr `json:"type"`
	Arity  int       `json:"arity"`
	Depth  int       `json:"depth"`
	Start  int       `json:"start"`
	End    int       `json:"end"`
	Public bool      `json:"public"`
	File   string    `json:"file,omitempty"`
}

// ResolvedRef records one value/name reference plus its resolved target span
// when available.
type ResolvedRef struct {
	Name        string `json:"name"`
	Node        int    `json:"node"`
	Start       int    `json:"start"`
	End         int    `json:"end"`
	File        string `json:"file,omitempty"`
	TargetNode  int    `json:"targetNode"`
	TargetStart int    `json:"targetStart"`
	TargetEnd   int    `json:"targetEnd"`
	TargetFile  string `json:"targetFile,omitempty"`
}

// ResolvedTypeRef records one resolved type-name reference.
type ResolvedTypeRef struct {
	Name  string `json:"name"`
	Node  int    `json:"node"`
	Start int    `json:"start"`
	End   int    `json:"end"`
	File  string `json:"file,omitempty"`
}

// ResolveDiagnosticRecord is one structured diagnostic produced by the
// self-host resolver.
type ResolveDiagnosticRecord struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Name    string `json:"name,omitempty"`
	Hint    string `json:"hint,omitempty"`
	Node    int    `json:"node"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
	File    string `json:"file,omitempty"`
}

// ResolveResult is the structured Go-facing surface for the bootstrapped
// resolver.
type ResolveResult struct {
	Summary     ResolveSummary            `json:"summary"`
	Symbols     []ResolvedSymbol          `json:"symbols"`
	Refs        []ResolvedRef             `json:"refs"`
	TypeRefs    []ResolvedTypeRef         `json:"typeRefs"`
	Diagnostics []ResolveDiagnosticRecord `json:"diagnostics,omitempty"`
}

// ResolveRequest is the wire shape consumed by the
// cmd/osty-native-resolver subprocess entry point. Exactly one of
// Source / Package should be set.
type ResolveRequest struct {
	Source  string               `json:"source,omitempty"`
	Package *PackageResolveInput `json:"package,omitempty"`
}

// InspectRecord is the exported shape of one inspector observation
// produced by the self-hosted inspect pass (toolchain/inspect.osty).
//
// Unlike internal/check.InspectRecord — which carries structured
// types.Type values and token.Pos spans — this record stays in the
// self-host's pre-lift representation: raw byte offsets and rendered
// type strings. Callers that need structured types should continue to
// use the Go-side Inspect until the self-host surfaces a typed form.
type InspectRecord struct {
	Start    int       `json:"start"`
	End      int       `json:"end"`
	NodeKind string    `json:"nodeKind"`
	Rule     string    `json:"rule"`
	Type     *TypeRepr `json:"type,omitempty"`
	HintName string    `json:"hintName,omitempty"`
	Notes    []string  `json:"notes,omitempty"`
}
