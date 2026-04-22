package selfhost

import (
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/selfhost/api"
)

// Type aliases re-export the cross-boundary shapes from
// internal/selfhost/api so existing `selfhost.CheckResult` callers
// continue to compile. Future work can switch consumers (cmd/osty,
// internal/check) to `api.CheckResult` directly.
type (
	CheckSummary          = api.CheckSummary
	CheckedNode           = api.CheckedNode
	CheckedBinding        = api.CheckedBinding
	CheckedSymbol         = api.CheckedSymbol
	CheckInstantiation    = api.CheckInstantiation
	CheckDiagnosticRecord = api.CheckDiagnosticRecord
	CheckResult           = api.CheckResult
)

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

// CheckFromSource parses src, runs the bootstrapped Osty checker, and
// returns parse-level diagnostics plus the structured check result in
// one pass. Callers that previously threaded a *FrontendRun through
// their CLI layer should prefer this entry point: it keeps the
// FrontendRun internal so cmd/osty does not need the selfhost type to
// cross its call boundary.
func CheckFromSource(src []byte) ([]*diag.Diagnostic, CheckResult) {
	run := Run(src)
	if run == nil {
		return nil, CheckResult{}
	}
	return run.Diagnostics(), CheckStructuredFromRun(run)
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
