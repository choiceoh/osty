// Package api holds the pure data types that cross the selfhost
// boundary — return shapes for the bootstrapped Osty checker and
// resolver. They are factored out of `internal/selfhost` so that
// downstream consumers can depend on the types without pulling in
// the generated bootstrap core. The selfhost package itself re-exports
// every type here via Go type aliases, so existing callers continue to
// compile unchanged.
package api

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

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
	Kind       string     `json:"kind"`                  // "primitive", "named", "tuple", "optional", "fn", "unit", "never", "typevar", "self", "error", "poison"
	Name       string     `json:"name,omitempty"`        // primitive/named/typevar name: "Int", "List", "T"
	Path       string     `json:"path,omitempty"`        // qualified path (reserved, currently empty)
	Args       []TypeRepr `json:"args,omitempty"`        // named generic args / tuple elems / fn params
	Return     *TypeRepr  `json:"return,omitempty"`      // fn return type / optional inner
	ParamNames []string   `json:"param_names,omitempty"` // G20: fn-type parameter names; nil when unavailable
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
			// G20: include param name when available.
			if tr.ParamNames != nil && i < len(tr.ParamNames) && tr.ParamNames[i] != "" {
				parts = append(parts, tr.ParamNames[i]+": "+tr.Args[i].String())
			} else {
				parts = append(parts, tr.Args[i].String())
			}
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

// CheckedNode records a checked expression node and its inferred type.
type CheckedNode struct {
	ID      string    `json:"id,omitempty"`      // stable record identity
	NodeKey string    `json:"nodeKey,omitempty"` // stable node identity
	Node    int       `json:"node"`              // legacy alias for NodeID
	NodeID  int       `json:"nodeId"`            // selfhost arena id, stable only within one check run
	Kind    string    `json:"kind"`
	Type    *TypeRepr `json:"type"`
	TypeID  int       `json:"typeId"`            // legacy checker-arena type id
	TypeKey string    `json:"typeKey,omitempty"` // stable structured type identity
	Start   int       `json:"start"`
	End     int       `json:"end"`
}

// CheckedBinding records a local binding that the bootstrapped checker typed.
type CheckedBinding struct {
	ID        string    `json:"id,omitempty"`
	NodeKey   string    `json:"nodeKey,omitempty"`
	Node      int       `json:"node"` // legacy alias for NodeID
	NodeID    int       `json:"nodeId"`
	BindingID int       `json:"bindingId"` // legacy sequential id
	Name      string    `json:"name"`
	Type      *TypeRepr `json:"type"`
	TypeID    int       `json:"typeId"` // legacy checker-arena type id
	TypeKey   string    `json:"typeKey,omitempty"`
	Mutable   bool      `json:"mutable"`
	Start     int       `json:"start"`
	End       int       `json:"end"`
}

// CheckedSymbol records a declaration collected by the bootstrapped checker.
type CheckedSymbol struct {
	ID       string    `json:"id,omitempty"`
	NodeKey  string    `json:"nodeKey,omitempty"`
	Node     int       `json:"node"` // legacy alias for NodeID
	NodeID   int       `json:"nodeId"`
	SymbolID int       `json:"symbolId"` // legacy sequential id
	Kind     string    `json:"kind"`
	Name     string    `json:"name"`
	Owner    string    `json:"owner"`
	Type     *TypeRepr `json:"type"`
	TypeID   int       `json:"typeId"` // legacy checker-arena type id
	TypeKey  string    `json:"typeKey,omitempty"`
	Start    int       `json:"start"`
	End      int       `json:"end"`
}

// CheckInstantiation records a generic function or method instantiation.
type CheckInstantiation struct {
	ID              string     `json:"id,omitempty"`
	NodeKey         string     `json:"nodeKey,omitempty"`
	Node            int        `json:"node"` // legacy alias for NodeID
	NodeID          int        `json:"nodeId"`
	InstantiationID int        `json:"instantiationId"` // legacy sequential id
	Callee          string     `json:"callee"`
	TypeArgs        []TypeRepr `json:"typeArgs"`
	TypeArgIDs      []int      `json:"typeArgIds,omitempty"` // legacy checker-arena type ids
	TypeArgKeys     []string   `json:"typeArgKeys,omitempty"`
	ResultType      *TypeRepr  `json:"resultType,omitempty"`
	ResultTypeID    int        `json:"resultTypeId"` // legacy checker-arena type id
	ResultTypeKey   string     `json:"resultTypeKey,omitempty"`
	Start           int        `json:"start"`
	End             int        `json:"end"`
}

// CheckDiagnosticRecord is a structured diagnostic produced by the
// bootstrapped Osty checker (see toolchain/check_diag.osty). The host
// bridge lifts each record into a `*diag.Diagnostic` so policy gates
// authored in Osty surface through the ordinary `check.Result.Diags`
// channel. Start/End are byte offsets in the source shape the native checker
// consumed. StartLine/StartColumn/EndLine/EndColumn, when present, are the
// checker-owned display positions for that span.
type CheckDiagnosticRecord struct {
	Code         string                 `json:"code"`
	Severity     string                 `json:"severity"`
	Message      string                 `json:"message"`
	Start        int                    `json:"start"`
	End          int                    `json:"end"`
	StartLine    int                    `json:"startLine,omitempty"`
	StartColumn  int                    `json:"startColumn,omitempty"`
	EndLine      int                    `json:"endLine,omitempty"`
	EndColumn    int                    `json:"endColumn,omitempty"`
	File         string                 `json:"file,omitempty"`
	Notes        []string               `json:"notes,omitempty"`
	SourceFileID string                 `json:"sourceFileId,omitempty"`
	SpanID       string                 `json:"spanId,omitempty"`
	Provenance   []SpanProvenanceRecord `json:"provenance,omitempty"`
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

// CheckResultIndex is a stable-id lookup table over CheckResult. It is not
// serialized; callers build it from an authoritative CheckResult when they
// want to consume checker facts without span/name rematching.
type CheckResultIndex struct {
	TypedNodesByStableID     map[string]*CheckedNode
	TypedNodesByNodeID       map[int]*CheckedNode
	TypedNodesByNodeKey      map[string]*CheckedNode
	BindingsByStableID       map[string]*CheckedBinding
	BindingsByID             map[int]*CheckedBinding
	BindingsByNodeID         map[int][]*CheckedBinding
	BindingsByNodeKey        map[string][]*CheckedBinding
	SymbolsByStableID        map[string]*CheckedSymbol
	SymbolsByID              map[int]*CheckedSymbol
	SymbolsByNodeID          map[int][]*CheckedSymbol
	SymbolsByNodeKey         map[string][]*CheckedSymbol
	InstantiationsByStableID map[string]*CheckInstantiation
	InstantiationsByID       map[int]*CheckInstantiation
	InstantiationsByNodeID   map[int][]*CheckInstantiation
	InstantiationsByNodeKey  map[string][]*CheckInstantiation
}

// EnsureStableIDs fills the content-derived identities for every checker
// record. The integer NodeID / TypeID / BindingID / SymbolID fields remain for
// legacy consumers, but new consumers should prefer the string IDs and keys:
// they are derived from source span, record kind, names, and structured type
// shapes instead of checker arena allocation order.
func (r *CheckResult) EnsureStableIDs() {
	if r == nil {
		return
	}
	for i := range r.TypedNodes {
		rec := &r.TypedNodes[i]
		rec.NodeKey = stableNodeKey(rec.NodeKey, rec.Kind, rec.Start, rec.End)
		rec.TypeKey = stableTypeKeyOrExisting(rec.TypeKey, rec.Type)
		rec.ID = stableRecordID(rec.ID, "typed-node", rec.NodeKey, rec.Kind, rec.TypeKey)
	}
	for i := range r.Bindings {
		rec := &r.Bindings[i]
		rec.NodeKey = stableNodeKey(rec.NodeKey, "binding:"+rec.Name, rec.Start, rec.End)
		rec.TypeKey = stableTypeKeyOrExisting(rec.TypeKey, rec.Type)
		rec.ID = stableRecordID(rec.ID, "binding", rec.NodeKey, rec.Name, strconv.FormatBool(rec.Mutable), rec.TypeKey)
	}
	for i := range r.Symbols {
		rec := &r.Symbols[i]
		rec.NodeKey = stableNodeKey(rec.NodeKey, "symbol:"+rec.Kind+":"+rec.Owner+":"+rec.Name, rec.Start, rec.End)
		rec.TypeKey = stableTypeKeyOrExisting(rec.TypeKey, rec.Type)
		rec.ID = stableRecordID(rec.ID, "symbol", rec.NodeKey, rec.Kind, rec.Owner, rec.Name, rec.TypeKey)
	}
	for i := range r.Instantiations {
		rec := &r.Instantiations[i]
		rec.NodeKey = stableNodeKey(rec.NodeKey, "instantiation:"+rec.Callee, rec.Start, rec.End)
		if len(rec.TypeArgKeys) != len(rec.TypeArgs) {
			rec.TypeArgKeys = make([]string, len(rec.TypeArgs))
		}
		for j := range rec.TypeArgs {
			rec.TypeArgKeys[j] = stableTypeKeyOrExisting(rec.TypeArgKeys[j], &rec.TypeArgs[j])
		}
		rec.ResultTypeKey = stableTypeKeyOrExisting(rec.ResultTypeKey, rec.ResultType)
		parts := []string{rec.NodeKey, rec.Callee, rec.ResultTypeKey}
		parts = append(parts, rec.TypeArgKeys...)
		rec.ID = stableRecordID(rec.ID, "instantiation", parts...)
	}
}

// Index builds a stable-id lookup table for r. Pointer values refer to records
// inside r, so callers should treat r as immutable while using the index.
func (r *CheckResult) Index() CheckResultIndex {
	if r == nil {
		return CheckResultIndex{
			TypedNodesByStableID:     map[string]*CheckedNode{},
			TypedNodesByNodeID:       map[int]*CheckedNode{},
			TypedNodesByNodeKey:      map[string]*CheckedNode{},
			BindingsByStableID:       map[string]*CheckedBinding{},
			BindingsByID:             map[int]*CheckedBinding{},
			BindingsByNodeID:         map[int][]*CheckedBinding{},
			BindingsByNodeKey:        map[string][]*CheckedBinding{},
			SymbolsByStableID:        map[string]*CheckedSymbol{},
			SymbolsByID:              map[int]*CheckedSymbol{},
			SymbolsByNodeID:          map[int][]*CheckedSymbol{},
			SymbolsByNodeKey:         map[string][]*CheckedSymbol{},
			InstantiationsByStableID: map[string]*CheckInstantiation{},
			InstantiationsByID:       map[int]*CheckInstantiation{},
			InstantiationsByNodeID:   map[int][]*CheckInstantiation{},
			InstantiationsByNodeKey:  map[string][]*CheckInstantiation{},
		}
	}
	idx := CheckResultIndex{
		TypedNodesByStableID:     make(map[string]*CheckedNode, len(r.TypedNodes)),
		TypedNodesByNodeID:       make(map[int]*CheckedNode, len(r.TypedNodes)),
		TypedNodesByNodeKey:      make(map[string]*CheckedNode, len(r.TypedNodes)),
		BindingsByStableID:       make(map[string]*CheckedBinding, len(r.Bindings)),
		BindingsByID:             make(map[int]*CheckedBinding, len(r.Bindings)),
		BindingsByNodeID:         make(map[int][]*CheckedBinding),
		BindingsByNodeKey:        make(map[string][]*CheckedBinding),
		SymbolsByStableID:        make(map[string]*CheckedSymbol, len(r.Symbols)),
		SymbolsByID:              make(map[int]*CheckedSymbol, len(r.Symbols)),
		SymbolsByNodeID:          make(map[int][]*CheckedSymbol),
		SymbolsByNodeKey:         make(map[string][]*CheckedSymbol),
		InstantiationsByStableID: make(map[string]*CheckInstantiation, len(r.Instantiations)),
		InstantiationsByID:       make(map[int]*CheckInstantiation, len(r.Instantiations)),
		InstantiationsByNodeID:   make(map[int][]*CheckInstantiation),
		InstantiationsByNodeKey:  make(map[string][]*CheckInstantiation),
	}
	for i := range r.TypedNodes {
		rec := &r.TypedNodes[i]
		if id := stableCheckedNodeID(rec); id != "" {
			idx.TypedNodesByStableID[id] = rec
		}
		if key := stableCheckedNodeKey(rec); key != "" {
			idx.TypedNodesByNodeKey[key] = rec
		}
		idx.TypedNodesByNodeID[checkRecordNodeID(rec.NodeID, rec.Node)] = rec
	}
	for i := range r.Bindings {
		rec := &r.Bindings[i]
		nodeID := checkRecordNodeID(rec.NodeID, rec.Node)
		if id := stableCheckedBindingID(rec); id != "" {
			idx.BindingsByStableID[id] = rec
		}
		if key := stableCheckedBindingNodeKey(rec); key != "" {
			idx.BindingsByNodeKey[key] = append(idx.BindingsByNodeKey[key], rec)
		}
		idx.BindingsByID[rec.BindingID] = rec
		idx.BindingsByNodeID[nodeID] = append(idx.BindingsByNodeID[nodeID], rec)
	}
	for i := range r.Symbols {
		rec := &r.Symbols[i]
		nodeID := checkRecordNodeID(rec.NodeID, rec.Node)
		if id := stableCheckedSymbolID(rec); id != "" {
			idx.SymbolsByStableID[id] = rec
		}
		if key := stableCheckedSymbolNodeKey(rec); key != "" {
			idx.SymbolsByNodeKey[key] = append(idx.SymbolsByNodeKey[key], rec)
		}
		idx.SymbolsByID[rec.SymbolID] = rec
		idx.SymbolsByNodeID[nodeID] = append(idx.SymbolsByNodeID[nodeID], rec)
	}
	for i := range r.Instantiations {
		rec := &r.Instantiations[i]
		nodeID := checkRecordNodeID(rec.NodeID, rec.Node)
		if id := stableCheckInstantiationID(rec); id != "" {
			idx.InstantiationsByStableID[id] = rec
		}
		if key := stableCheckInstantiationNodeKey(rec); key != "" {
			idx.InstantiationsByNodeKey[key] = append(idx.InstantiationsByNodeKey[key], rec)
		}
		idx.InstantiationsByID[rec.InstantiationID] = rec
		idx.InstantiationsByNodeID[nodeID] = append(idx.InstantiationsByNodeID[nodeID], rec)
	}
	return idx
}

func checkRecordNodeID(nodeID, legacyNode int) int {
	if nodeID != 0 {
		return nodeID
	}
	return legacyNode
}

func stableCheckedNodeID(rec *CheckedNode) string {
	if rec == nil {
		return ""
	}
	return stableRecordID(rec.ID, "typed-node", stableCheckedNodeKey(rec), rec.Kind, stableTypeKeyOrExisting(rec.TypeKey, rec.Type))
}

func stableCheckedNodeKey(rec *CheckedNode) string {
	if rec == nil {
		return ""
	}
	return stableNodeKey(rec.NodeKey, rec.Kind, rec.Start, rec.End)
}

func stableCheckedBindingID(rec *CheckedBinding) string {
	if rec == nil {
		return ""
	}
	return stableRecordID(rec.ID, "binding", stableCheckedBindingNodeKey(rec), rec.Name, strconv.FormatBool(rec.Mutable), stableTypeKeyOrExisting(rec.TypeKey, rec.Type))
}

func stableCheckedBindingNodeKey(rec *CheckedBinding) string {
	if rec == nil {
		return ""
	}
	return stableNodeKey(rec.NodeKey, "binding:"+rec.Name, rec.Start, rec.End)
}

func stableCheckedSymbolID(rec *CheckedSymbol) string {
	if rec == nil {
		return ""
	}
	return stableRecordID(rec.ID, "symbol", stableCheckedSymbolNodeKey(rec), rec.Kind, rec.Owner, rec.Name, stableTypeKeyOrExisting(rec.TypeKey, rec.Type))
}

func stableCheckedSymbolNodeKey(rec *CheckedSymbol) string {
	if rec == nil {
		return ""
	}
	return stableNodeKey(rec.NodeKey, "symbol:"+rec.Kind+":"+rec.Owner+":"+rec.Name, rec.Start, rec.End)
}

func stableCheckInstantiationID(rec *CheckInstantiation) string {
	if rec == nil {
		return ""
	}
	typeArgKeys := stableTypeKeys(rec.TypeArgKeys, rec.TypeArgs)
	parts := []string{stableCheckInstantiationNodeKey(rec), rec.Callee, stableTypeKeyOrExisting(rec.ResultTypeKey, rec.ResultType)}
	parts = append(parts, typeArgKeys...)
	return stableRecordID(rec.ID, "instantiation", parts...)
}

func stableCheckInstantiationNodeKey(rec *CheckInstantiation) string {
	if rec == nil {
		return ""
	}
	return stableNodeKey(rec.NodeKey, "instantiation:"+rec.Callee, rec.Start, rec.End)
}

func stableTypeKeys(existing []string, reprs []TypeRepr) []string {
	if len(existing) == len(reprs) {
		out := append([]string(nil), existing...)
		for i := range out {
			out[i] = stableTypeKeyOrExisting(out[i], &reprs[i])
		}
		return out
	}
	out := make([]string, len(reprs))
	for i := range reprs {
		out[i] = stableTypeKeyOrExisting("", &reprs[i])
	}
	return out
}

func stableNodeKey(existing, kind string, start, end int) string {
	if existing != "" {
		return existing
	}
	return stableID("node", kind, strconv.Itoa(start), strconv.Itoa(end))
}

func stableTypeKeyOrExisting(existing string, tr *TypeRepr) string {
	if existing != "" {
		return existing
	}
	return StableTypeKey(tr)
}

// StableTypeKey returns the content-derived identity for a structured type.
// It ignores transient checker arena TypeID integers.
func StableTypeKey(tr *TypeRepr) string {
	if tr == nil {
		return ""
	}
	parts := []string{tr.Kind, tr.Name, tr.Path}
	for i := range tr.Args {
		parts = append(parts, StableTypeKey(&tr.Args[i]))
	}
	if tr.Return != nil {
		parts = append(parts, "return", StableTypeKey(tr.Return))
	}
	return stableID("type", parts...)
}

func stableRecordID(existing, prefix string, parts ...string) string {
	if existing != "" {
		return existing
	}
	return stableID(prefix, parts...)
}

func stableID(prefix string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(prefix))
	h.Write([]byte{0})
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	sum := h.Sum(nil)
	return prefix + ":" + hex.EncodeToString(sum[:12])
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
	ID        string    `json:"symbolId,omitempty"`
	PackageID string    `json:"packageId,omitempty"`
	DeclID    string    `json:"declId,omitempty"`
	Node      int       `json:"node"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Type      *TypeRepr `json:"type"`
	Arity     int       `json:"arity"`
	Depth     int       `json:"depth"`
	Start     int       `json:"start"`
	End       int       `json:"end"`
	Public    bool      `json:"public"`
	File      string    `json:"file,omitempty"`
}

// ResolvedRef records one value/name reference plus its resolved target span
// when available.
type ResolvedRef struct {
	ID             string `json:"refId,omitempty"`
	PackageID      string `json:"packageId,omitempty"`
	BindingID      string `json:"bindingId,omitempty"`
	TargetSymbolID string `json:"targetSymbolId,omitempty"`
	Name           string `json:"name"`
	Node           int    `json:"node"`
	Start          int    `json:"start"`
	End            int    `json:"end"`
	File           string `json:"file,omitempty"`
	TargetNode     int    `json:"targetNode"`
	TargetStart    int    `json:"targetStart"`
	TargetEnd      int    `json:"targetEnd"`
	TargetFile     string `json:"targetFile,omitempty"`
}

// ResolvedTypeRef records one resolved type-name reference.
type ResolvedTypeRef struct {
	ID             string `json:"typeRefId,omitempty"`
	PackageID      string `json:"packageId,omitempty"`
	TargetSymbolID string `json:"targetSymbolId,omitempty"`
	Name           string `json:"name"`
	Node           int    `json:"node"`
	Start          int    `json:"start"`
	End            int    `json:"end"`
	File           string `json:"file,omitempty"`
	TargetNode     int    `json:"targetNode"`
	TargetStart    int    `json:"targetStart"`
	TargetEnd      int    `json:"targetEnd"`
	TargetFile     string `json:"targetFile,omitempty"`
}

// ResolveDiagnosticRecord is one structured diagnostic produced by the
// self-host resolver.
type ResolveDiagnosticRecord struct {
	ID           string                 `json:"diagnosticId,omitempty"`
	PackageID    string                 `json:"packageId,omitempty"`
	Code         string                 `json:"code"`
	Message      string                 `json:"message"`
	Name         string                 `json:"name,omitempty"`
	Hint         string                 `json:"hint,omitempty"`
	Node         int                    `json:"node"`
	Start        int                    `json:"start"`
	End          int                    `json:"end"`
	File         string                 `json:"file,omitempty"`
	SourceFileID string                 `json:"sourceFileId,omitempty"`
	SpanID       string                 `json:"spanId,omitempty"`
	Provenance   []SpanProvenanceRecord `json:"provenance,omitempty"`
}

// SpanProvenanceRecord is the JSON-stable form of span provenance emitted by
// selfhost adapters and consumed by host diagnostics.
type SpanProvenanceRecord struct {
	Kind         string `json:"kind,omitempty"`
	SourceFileID string `json:"sourceFileId,omitempty"`
	SpanID       string `json:"spanId,omitempty"`
	Detail       string `json:"detail,omitempty"`
}

// ResolveResult is the structured Go-facing surface for the bootstrapped
// resolver.
type ResolveResult struct {
	PackageID   string                    `json:"packageId,omitempty"`
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
// Unlike internal/check.InspectRecord — which carries types.Type values and
// token.Pos spans — this record stays in the self-host's pre-lift span
// representation while carrying the same structured TypeRepr as CheckResult.
type InspectRecord struct {
	Start    int       `json:"start"`
	End      int       `json:"end"`
	NodeKind string    `json:"nodeKind"`
	Rule     string    `json:"rule"`
	Type     *TypeRepr `json:"type,omitempty"`
	HintName string    `json:"hintName,omitempty"`
	Notes    []string  `json:"notes,omitempty"`
}
