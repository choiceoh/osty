package selfhost

// CheckSummary is the exported Go shape for the bootstrapped Osty checker.
//
// The self-hosted checker is authoritative for mainstream checker diagnostics
// and supplies structured expression, binding, declaration-symbol, and
// instantiation facts to the Go check.Result bridge.
type CheckSummary struct {
	Assignments int
	Accepted    int
	Errors      int
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

// CheckResult is the structured Go-facing surface for the bootstrapped checker.
type CheckResult struct {
	Summary        CheckSummary
	TypedNodes     []CheckedNode
	Bindings       []CheckedBinding
	Symbols        []CheckedSymbol
	Instantiations []CheckInstantiation
}

// CheckSource runs the bootstrapped Osty checker over one source string.
func CheckSource(src []byte) CheckSummary {
	return CheckSourceStructured(src).Summary
}

// CheckSourceStructured runs the bootstrapped Osty checker and returns the
// structured result consumed by the Go check.Result bridge.
func CheckSourceStructured(src []byte) CheckResult {
	lexed := ostyLexSource(string(src))
	checked := frontendCheckLexedSourceStructured(lexed)
	if checked == nil {
		return CheckResult{}
	}
	return adaptCheckResult(checked, lexed)
}

func adaptCheckSummary(checked *FrontCheckSummary) CheckSummary {
	if checked == nil {
		return CheckSummary{}
	}
	return CheckSummary{
		Assignments: checked.assignments,
		Accepted:    checked.accepted,
		Errors:      checked.errors,
	}
}

func adaptCheckResult(checked *FrontCheckResult, lexed *OstyLexedSource) CheckResult {
	result := CheckResult{
		Summary:        adaptCheckSummary(checked.summary),
		TypedNodes:     make([]CheckedNode, 0, len(checked.typedNodes)),
		Bindings:       make([]CheckedBinding, 0, len(checked.bindings)),
		Symbols:        make([]CheckedSymbol, 0, len(checked.symbols)),
		Instantiations: make([]CheckInstantiation, 0, len(checked.instantiations)),
	}
	rt := newRuneTable("")
	var stream *FrontLexStream
	if lexed != nil {
		rt = newRuneTable(lexed.source)
		stream = lexed.stream
	}
	for _, node := range checked.typedNodes {
		if node == nil {
			continue
		}
		start, end := checkNodeOffsets(rt, stream, node.start, node.end)
		result.TypedNodes = append(result.TypedNodes, CheckedNode{
			Node:     node.node,
			Kind:     node.kind,
			TypeName: node.typeName,
			Start:    start,
			End:      end,
		})
	}
	for _, binding := range checked.bindings {
		if binding == nil {
			continue
		}
		start, end := checkNodeOffsets(rt, stream, binding.start, binding.end)
		result.Bindings = append(result.Bindings, CheckedBinding{
			Node:     binding.node,
			Name:     binding.name,
			TypeName: binding.typeName,
			Mutable:  binding.mutable,
			Start:    start,
			End:      end,
		})
	}
	for _, symbol := range checked.symbols {
		if symbol == nil {
			continue
		}
		start, end := checkNodeOffsets(rt, stream, symbol.start, symbol.end)
		result.Symbols = append(result.Symbols, CheckedSymbol{
			Node:     symbol.node,
			Kind:     symbol.kind,
			Name:     symbol.name,
			Owner:    symbol.owner,
			TypeName: symbol.typeName,
			Start:    start,
			End:      end,
		})
	}
	for _, inst := range checked.instantiations {
		if inst == nil {
			continue
		}
		start, end := checkNodeOffsets(rt, stream, inst.start, inst.end)
		result.Instantiations = append(result.Instantiations, CheckInstantiation{
			Node:       inst.node,
			Callee:     inst.callee,
			TypeArgs:   append([]string(nil), inst.typeArgs...),
			ResultType: inst.resultType,
			Start:      start,
			End:        end,
		})
	}
	return result
}

func checkNodeOffsets(rt runeTable, stream *FrontLexStream, startToken, endToken int) (int, int) {
	if stream == nil || len(stream.tokens) == 0 {
		return 0, 0
	}
	if startToken < 0 {
		startToken = 0
	}
	if startToken >= len(stream.tokens) {
		startToken = len(stream.tokens) - 1
	}
	endIndex := endToken - 1
	if endIndex < startToken {
		endIndex = startToken
	}
	if endIndex >= len(stream.tokens) {
		endIndex = len(stream.tokens) - 1
	}
	start := rt.byteOffset(stream.tokens[startToken].start.offset)
	end := rt.byteOffset(stream.tokens[endIndex].end.offset)
	if end < start {
		end = start
	}
	return start, end
}
