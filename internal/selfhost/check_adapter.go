package selfhost

import (
	"strings"

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
	if lexed == nil {
		return CheckResult{}
	}
	file := selfhostSemanticAstFile(astParseLexedSource(lexed))
	if file == nil {
		return CheckResult{}
	}
	checked := frontendCheckAstStructured(file)
	if checked == nil {
		return CheckResult{}
	}
	result := adaptCheckResult(checked, lexed)
	selfhostAppendIntrinsicBodyGateForSource(&result, src)
	selfhostAppendPureGateForSource(&result, src)
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
	file := run.semanticAstFile()
	if file == nil {
		return CheckResult{}
	}
	checked := frontendCheckAstStructured(file)
	if checked == nil {
		return CheckResult{}
	}
	result := adaptCheckResultFromRuneStream(checked, run.rt, run.stream)
	selfhostAppendIntrinsicBodyGateForRun(&result, run)
	selfhostAppendPureGateForRun(&result, run)
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
			Node:  node.node,
			Kind:  node.kind,
			Type:  parseTypeRepr(node.typeName),
			Start: start,
			End:   end,
		})
	}
	for _, binding := range checked.bindings {
		if binding == nil {
			continue
		}
		start, end := checkNodeOffsets(rt, stream, binding.start, binding.end)
		result.Bindings = append(result.Bindings, CheckedBinding{
			Node:    binding.node,
			Name:    binding.name,
			Type:    parseTypeRepr(binding.typeName),
			Mutable: binding.mutable,
			Start:   start,
			End:     end,
		})
	}
	for _, symbol := range checked.symbols {
		if symbol == nil {
			continue
		}
		start, end := checkNodeOffsets(rt, stream, symbol.start, symbol.end)
		result.Symbols = append(result.Symbols, CheckedSymbol{
			Node:  symbol.node,
			Kind:  symbol.kind,
			Name:  symbol.name,
			Owner: symbol.owner,
			Type:  parseTypeRepr(symbol.typeName),
			Start: start,
			End:   end,
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
			TypeArgs:   parseTypeReprSlice(inst.typeArgs),
			ResultType: parseTypeRepr(inst.resultType),
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

// parseTypeRepr converts an Osty-rendered type string into a structured
// *api.TypeRepr.
//
// Transitional: internal/selfhost/generated.go still uses string-based
// typeName fields in FrontCheckedNode/FrontCheckedBinding/FrontCheckedSymbol
// and []string typeArgs in FrontCheckInstantiation because it was produced by
// the Osty→Go transpiler before the FrontTypeRepr struct landed in
// toolchain/check.osty. Once generated.go is regenerated with the new
// FrontTypeRepr-based fields, this function and its helpers (parseFnTypeRepr,
// splitGenericRepr, splitTypeReprList, matchingTypeReprParen,
// parseTypeReprSlice) can be deleted entirely — the adapters will read
// structured FrontTypeRepr values directly from the generated structs.
func parseTypeRepr(raw string) *api.TypeRepr {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil
	}
	switch text {
	case "Invalid", "Poison":
		return &api.TypeRepr{Kind: "error", Name: text}
	case "()", "Unit":
		return &api.TypeRepr{Kind: "unit"}
	case "Never":
		return &api.TypeRepr{Kind: "never", Name: "Never"}
	case "UntypedInt":
		return &api.TypeRepr{Kind: "primitive", Name: "UntypedInt"}
	case "UntypedFloat":
		return &api.TypeRepr{Kind: "primitive", Name: "UntypedFloat"}
	}
	// Optional suffix: "Int?"
	if strings.HasSuffix(text, "?") {
		inner := parseTypeRepr(strings.TrimSuffix(text, "?"))
		return &api.TypeRepr{Kind: "optional", Return: inner}
	}
	// Function type: "fn(...) -> ..."
	if strings.HasPrefix(text, "fn(") {
		return parseFnTypeRepr(text)
	}
	// Tuple: "(A, B, ...)" or "()" (already handled above)
	if strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") {
		inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(text, "("), ")"))
		if inner == "" {
			return &api.TypeRepr{Kind: "unit"}
		}
		parts := splitTypeReprList(inner)
		if len(parts) == 1 {
			return parseTypeRepr(parts[0])
		}
		args := make([]api.TypeRepr, 0, len(parts))
		for _, part := range parts {
			if tr := parseTypeRepr(part); tr != nil {
				args = append(args, *tr)
			}
		}
		return &api.TypeRepr{Kind: "tuple", Args: args}
	}
	// Named with generics: "List<Int>"
	head, argText, hasArgs := splitGenericRepr(text)
	if hasArgs {
		parts := splitTypeReprList(argText)
		typeArgs := make([]api.TypeRepr, 0, len(parts))
		for _, a := range parts {
			if tr := parseTypeRepr(a); tr != nil {
				typeArgs = append(typeArgs, *tr)
			}
		}
		return &api.TypeRepr{Kind: "named", Name: head, Args: typeArgs}
	}
	// Single uppercase letter → type variable
	if len(head) == 1 && head[0] >= 'A' && head[0] <= 'Z' {
		return &api.TypeRepr{Kind: "typevar", Name: head}
	}
	return &api.TypeRepr{Kind: "primitive", Name: head}
}

func parseFnTypeRepr(text string) *api.TypeRepr {
	open := strings.IndexByte(text, '(')
	if open < 0 {
		return &api.TypeRepr{Kind: "error", Name: text}
	}
	close := matchingTypeReprParen(text, open)
	if close < 0 {
		return &api.TypeRepr{Kind: "error", Name: text}
	}
	paramText := strings.TrimSpace(text[open+1 : close])
	var params []api.TypeRepr
	if paramText != "" {
		for _, part := range splitTypeReprList(paramText) {
			if tr := parseTypeRepr(part); tr != nil {
				params = append(params, *tr)
			}
		}
	}
	var ret *api.TypeRepr
	rest := strings.TrimSpace(text[close+1:])
	if strings.HasPrefix(rest, "->") {
		ret = parseTypeRepr(strings.TrimSpace(strings.TrimPrefix(rest, "->")))
	}
	if ret == nil {
		ret = &api.TypeRepr{Kind: "unit"}
	}
	return &api.TypeRepr{Kind: "fn", Args: params, Return: ret}
}

// splitGenericRepr splits "List<Int>" into ("List", "Int", true)
// and "Int" into ("Int", "", false).
func splitGenericRepr(text string) (head, args string, ok bool) {
	depth := 0
	start := -1
	for i, r := range text {
		switch r {
		case '<':
			if depth == 0 {
				start = i
			}
			depth++
		case '>':
			depth--
			if depth == 0 && i == len(text)-1 && start >= 0 {
				return strings.TrimSpace(text[:start]), strings.TrimSpace(text[start+1 : i]), true
			}
		}
	}
	return strings.TrimSpace(text), "", false
}

// splitTypeReprList splits "A, B, C" into ["A", "B", "C"] respecting
// angle brackets and parens.
func splitTypeReprList(text string) []string {
	var out []string
	start := 0
	angle := 0
	paren := 0
	for i, r := range text {
		switch r {
		case '<':
			angle++
		case '>':
			if angle > 0 {
				angle--
			}
		case '(':
			paren++
		case ')':
			if paren > 0 {
				paren--
			}
		case ',':
			if angle == 0 && paren == 0 {
				part := strings.TrimSpace(text[start:i])
				if part != "" {
					out = append(out, part)
				}
				start = i + 1
			}
		}
	}
	if part := strings.TrimSpace(text[start:]); part != "" {
		out = append(out, part)
	}
	return out
}

// matchingTypeReprParen finds the index of the closing paren matching the
// opening paren at position open.
func matchingTypeReprParen(text string, open int) int {
	depth := 0
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// parseTypeReprSlice converts a slice of Osty type strings to []api.TypeRepr.
func parseTypeReprSlice(raw []string) []api.TypeRepr {
	if len(raw) == 0 {
		return nil
	}
	out := make([]api.TypeRepr, 0, len(raw))
	for _, s := range raw {
		if tr := parseTypeRepr(s); tr != nil {
			out = append(out, *tr)
		}
	}
	return out
}
