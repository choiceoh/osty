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

// CheckSource runs the bootstrapped Osty checker over one source string.
// The returned Summary carries ErrorsByContext so lightweight telemetry
// callers do not need to materialise the full structured result.
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
	result := adaptCheckResult(checked, lexed)
	selfhostAppendIntrinsicBodyGateForSource(&result, src)
	return result
}

// CheckStructuredFromRun runs the bootstrapped Osty checker directly
// on an existing FrontendRun's parser arena. Callers that already
// hold a FrontendRun (for example through parser.ParseRun) should
// prefer this entry point over CheckSourceStructured to avoid the
// re-lex/re-parse pass that the source-based entry performs.
//
// Output matches CheckSourceStructured(src) byte-for-byte. The
// astbridge *ast.File lowering is still triggered exactly once per
// run by the intrinsic-body gate adapter (see
// selfhostAppendIntrinsicBodyGateForRun); porting that gate walker
// to AstArena is the follow-up that would make this path fully
// astbridge-free.
func CheckStructuredFromRun(run *FrontendRun) CheckResult {
	if run == nil {
		return CheckResult{}
	}
	file := run.astFile()
	if file == nil {
		return CheckResult{}
	}
	checked := frontendCheckAstStructured(file)
	if checked == nil {
		return CheckResult{}
	}
	result := adaptCheckResultFromRuneStream(checked, run.rt, run.stream)
	selfhostAppendIntrinsicBodyGateForRun(&result, run)
	return result
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

func adaptCheckSummaryWithContext(checked *FrontCheckResult, posLookup selfhostTokenPos) CheckSummary {
	if checked == nil {
		return CheckSummary{}
	}
	s := adaptCheckSummary(checked.summary)
	s.ErrorsByContext, s.ErrorDetails = selfhostDiagnosticTelemetry(checked.diagnostics, posLookup)
	return s
}

// selfhostStreamTokenPos returns a selfhostTokenPos that reads 1-based
// (line, column) directly off the lex stream's token positions. The
// filename is always empty — file mode already surfaces the path in the
// dump label above each telemetry block, so repeating it in every
// suffix would only add noise.
func selfhostStreamTokenPos(stream *FrontLexStream) selfhostTokenPos {
	if stream == nil {
		return nil
	}
	tokens := stream.tokens
	return func(tokenIdx int) (string, int, int, bool) {
		if tokenIdx < 0 || tokenIdx >= len(tokens) {
			return "", 0, 0, false
		}
		tok := tokens[tokenIdx]
		if tok == nil || tok.start == nil {
			return "", 0, 0, false
		}
		return "", tok.start.line, tok.start.column, true
	}
}

func adaptCheckResult(checked *FrontCheckResult, lexed *OstyLexedSource) CheckResult {
	rt := newRuneTable("")
	var stream *FrontLexStream
	if lexed != nil {
		rt = newRuneTable(lexed.source)
		stream = lexed.stream
	}
	return adaptCheckResultFromRuneStream(checked, rt, stream)
}

// adaptCheckResultFromRuneStream is the position-mapping core of
// adaptCheckResult. Factored out so callers that already hold a
// runeTable + FrontLexStream (for example the arena-direct
// CheckStructuredFromRun path, which reuses FrontendRun's own rt and
// stream) can skip constructing an OstyLexedSource.
func adaptCheckResultFromRuneStream(checked *FrontCheckResult, rt runeTable, stream *FrontLexStream) CheckResult {
	result := CheckResult{
		Summary:        adaptCheckSummaryWithContext(checked, selfhostStreamTokenPos(stream)),
		TypedNodes:     make([]CheckedNode, 0, len(checked.typedNodes)),
		Bindings:       make([]CheckedBinding, 0, len(checked.bindings)),
		Symbols:        make([]CheckedSymbol, 0, len(checked.symbols)),
		Instantiations: make([]CheckInstantiation, 0, len(checked.instantiations)),
		Diagnostics:    make([]CheckDiagnosticRecord, 0, len(checked.diagnostics)),
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
	for _, d := range checked.diagnostics {
		if d == nil {
			continue
		}
		start, end := checkNodeOffsets(rt, stream, d.start, d.end)
		result.Diagnostics = append(result.Diagnostics, CheckDiagnosticRecord{
			Code:     d.code,
			Severity: diagnosticSeverityName(d.severity),
			Message:  d.message,
			Start:    start,
			End:      end,
			File:     "",
			Notes:    append([]string(nil), d.notes...),
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
