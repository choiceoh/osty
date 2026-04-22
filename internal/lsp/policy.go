package lsp

import "github.com/osty/osty/internal/selfhost"

type LSPSemanticToken struct {
	Line      uint32
	Column    uint32
	Length    uint32
	TokenType uint32
	Modifiers uint32
}

type LSPTextEdit struct {
	StartLine      uint32
	StartCharacter uint32
	EndLine        uint32
	EndCharacter   uint32
	NewText        string
}

type LSPLocation struct {
	URI            string
	StartLine      uint32
	StartCharacter uint32
	EndLine        uint32
	EndCharacter   uint32
}

type LSPSymbolSortKey struct {
	Name string
	URI  string
}

type LSPImportSortKey struct {
	Group int
	Key   string
	Alias string
}

type LSPSignatureParam struct {
	Name     string
	TypeName string
}

type LSPSignatureText struct {
	Label           string
	ParameterLabels []string
}

type LSPCompletionContext struct {
	Prefix   string
	AfterDot string
}

type LSPDiagnosticPayload struct {
	Severity uint32
	Message  string
}

type LSPPosition struct {
	Line      uint32
	Character uint32
}

type LSPRange struct {
	Start LSPPosition
	End   LSPPosition
}

type LSPOstyPosition struct {
	Offset int
	Line   int
	Column int
}

func LSPSemanticTypeForTokenKind(kind, symbolKind string) (uint32, bool) {
	tokenType := selfhost.LSPSemanticTypeForTokenKind(kind, symbolKind)
	if tokenType < 0 {
		return 0, false
	}
	return uint32(tokenType), true
}

func LSPSemanticTypeForComment() uint32 {
	return uint32(selfhost.LSPSemanticTypeForComment())
}

func LSPCompletionKindForSymbolKind(kind string) uint32 {
	return uint32(selfhost.LSPCompletionKindForSymbolKind(kind))
}

func LSPCompletionSortTextForSymbolKind(kind, label string) string {
	return selfhost.LSPCompletionSortTextForSymbolKind(kind, label)
}

func LSPCompletionDetail(kind, label, typeText string) string {
	return selfhost.LSPCompletionDetail(kind, label, typeText)
}

func LSPHoverSignatureLine(kind, name, typeText string) string {
	return selfhost.LSPHoverSignatureLine(kind, name, typeText)
}

func LSPPathToURI(path string) string {
	return selfhost.LSPPathToURI(path)
}

func LSPLineStarts(src []byte) []int {
	return selfhost.LSPLineStarts(string(src))
}

func LSPUTF16UnitsInPrefix(src []byte) uint32 {
	return uint32(selfhost.LSPUTF16UnitsInPrefix(string(src)))
}

func LSPOstyPositionToLSP(src []byte, lineStarts []int, line, offset int) LSPPosition {
	pos := selfhost.LSPOstyPositionToLSP(string(src), lineStarts, line, offset)
	return LSPPosition{
		Line:      uint32(pos.Line),
		Character: uint32(pos.Character),
	}
}

func LSPLSPPositionToOsty(src []byte, lineStarts []int, lspLine, character uint32) LSPOstyPosition {
	pos := selfhost.LSPLSPPositionToOsty(string(src), lineStarts, int(lspLine), int(character))
	return LSPOstyPosition{
		Offset: pos.Offset,
		Line:   pos.Line,
		Column: pos.Column,
	}
}

func LSPOffsetToPosition(src []byte, lineStarts []int, off int) LSPPosition {
	pos := selfhost.LSPOffsetToPosition(string(src), lineStarts, off)
	return LSPPosition{
		Line:      uint32(pos.Line),
		Character: uint32(pos.Character),
	}
}

func LSPRangeFromOffsets(src []byte, lineStarts []int, start, end int) LSPRange {
	rng := selfhost.LSPRangeFromOffsets(string(src), lineStarts, start, end)
	return lspRangeFromSelfhost(rng)
}

func LSPRangeFromOstySpan(src []byte, lineStarts []int, startLine, startOffset, endLine, endOffset int) LSPRange {
	rng := selfhost.LSPRangeFromOstySpan(string(src), lineStarts, startLine, startOffset, endLine, endOffset)
	return lspRangeFromSelfhost(rng)
}

func LSPSymbolKindForDecl(kind string, mutable bool) uint32 {
	return uint32(selfhost.LSPSymbolKindForDecl(kind, mutable))
}

func LSPSymbolKindForMember(kind string) uint32 {
	return uint32(selfhost.LSPSymbolKindForMember(kind))
}

func LSPWantsCodeActionKind(only []string, kind string) bool {
	return selfhost.LSPWantsCodeActionKind(only, kind)
}

func LSPPrefixUnderscoreName(name string) string {
	return selfhost.LSPPrefixUnderscoreName(name)
}

func LSPPrefixUnderscoreTitle(name string) string {
	return selfhost.LSPPrefixUnderscoreTitle(name)
}

func LSPFindNameOffset(src []byte, declStart, declEnd int, name string) int {
	return selfhost.LSPFindNameOffset(string(src), declStart, declEnd, name)
}

func LSPPrecedingCompletionContext(src []byte, offset int) LSPCompletionContext {
	ctx := selfhost.LSPPrecedingCompletionContext(string(src), offset)
	return LSPCompletionContext{
		Prefix:   ctx.Prefix,
		AfterDot: ctx.AfterDot,
	}
}

func LSPIdentifierAt(src []byte, offset int) string {
	return selfhost.LSPIdentifierAt(string(src), offset)
}

func LSPContainsPosition(startLine, startColumn, endLine, endColumn, posLine, posColumn int) bool {
	return selfhost.LSPContainsPosition(startLine, startColumn, endLine, endColumn, posLine, posColumn)
}

func LSPSpanOverlaps(startOffset, endOffset, queryStart, queryEnd int) bool {
	return selfhost.LSPSpanOverlaps(startOffset, endOffset, queryStart, queryEnd)
}

func LSPDiagnosticPayloadFor(severity, message, hint string, notes []string) LSPDiagnosticPayload {
	payload := selfhost.LSPDiagnosticPayloadFor(severity, message, hint, notes)
	return LSPDiagnosticPayload{
		Severity: uint32(payload.Severity),
		Message:  payload.Message,
	}
}

func SortDedupLSPLocations(locs []LSPLocation) []LSPLocation {
	converted := make([]selfhost.LSPLocation, 0, len(locs))
	for _, loc := range locs {
		converted = append(converted, selfhost.LSPLocation{
			URI:            loc.URI,
			StartLine:      int(loc.StartLine),
			StartCharacter: int(loc.StartCharacter),
			EndLine:        int(loc.EndLine),
			EndCharacter:   int(loc.EndCharacter),
		})
	}
	resolved := selfhost.SortDedupLSPLocations(converted)
	out := make([]LSPLocation, 0, len(resolved))
	for _, loc := range resolved {
		out = append(out, LSPLocation{
			URI:            loc.URI,
			StartLine:      uint32(loc.StartLine),
			StartCharacter: uint32(loc.StartCharacter),
			EndLine:        uint32(loc.EndLine),
			EndCharacter:   uint32(loc.EndCharacter),
		})
	}
	return out
}

func SortLSPSymbolIndexes(keys []LSPSymbolSortKey) []int {
	converted := make([]selfhost.LSPSymbolSortKey, 0, len(keys))
	for _, key := range keys {
		converted = append(converted, selfhost.LSPSymbolSortKey{
			Name: key.Name,
			URI:  key.URI,
		})
	}
	return selfhost.SortLSPSymbolIndexes(converted)
}

func SortLSPCompletionIndexes(labels []string) []int {
	return selfhost.SortLSPCompletionIndexes(labels)
}

func SortLSPImportIndexes(keys []LSPImportSortKey) []int {
	converted := make([]selfhost.LSPImportSortKey, 0, len(keys))
	for _, key := range keys {
		converted = append(converted, selfhost.LSPImportSortKey{
			Group: key.Group,
			Key:   key.Key,
			Alias: key.Alias,
		})
	}
	return selfhost.SortLSPImportIndexes(converted)
}

func LSPUseGroup(isGoFFI bool, path []string) int {
	return selfhost.LSPUseGroup(isGoFFI, path)
}

func LSPUseKey(isGoFFI bool, goPath, rawPath string, path []string) string {
	return selfhost.LSPUseKey(isGoFFI, goPath, rawPath, path)
}

func LSPKeyWithAlias(group int, key, alias string) string {
	return selfhost.LSPKeyWithAlias(group, key, alias)
}

func LSPUseSourceText(src []byte, start, end int) string {
	return selfhost.LSPUseSourceText(string(src), start, end)
}

func LSPEndOfLineOffset(src []byte, off int) int {
	return selfhost.LSPEndOfLineOffset(string(src), off)
}

func LSPHasTriviaBetweenOffsets(src []byte, start, end int) bool {
	return selfhost.LSPHasTriviaBetweenOffsets(string(src), start, end)
}

func LSPActiveParameter(argEndOffsets []int, cursorOffset int) uint32 {
	return uint32(selfhost.LSPActiveParameter(argEndOffsets, cursorOffset))
}

func LSPBuildSignatureText(name string, params []LSPSignatureParam, returnType string) LSPSignatureText {
	converted := make([]selfhost.LSPSignatureParam, 0, len(params))
	for _, param := range params {
		converted = append(converted, selfhost.LSPSignatureParam{
			Name:     param.Name,
			TypeName: param.TypeName,
		})
	}
	rendered := selfhost.LSPBuildSignatureText(name, converted, returnType)
	return LSPSignatureText{
		Label:           rendered.Label,
		ParameterLabels: append([]string(nil), rendered.ParameterLabels...),
	}
}

func EncodeLSPSemanticTokens(tokens []LSPSemanticToken) []uint32 {
	converted := make([]selfhost.LSPSemanticToken, 0, len(tokens))
	for _, token := range tokens {
		converted = append(converted, selfhost.LSPSemanticToken{
			Line:      int(token.Line),
			Column:    int(token.Column),
			Length:    int(token.Length),
			TokenType: int(token.TokenType),
			Modifiers: int(token.Modifiers),
		})
	}
	encoded := selfhost.EncodeLSPSemanticTokens(converted)
	out := make([]uint32, 0, len(encoded))
	for _, value := range encoded {
		out = append(out, uint32(value))
	}
	return out
}

func ResolveOverlappingLSPTextEdits(edits []LSPTextEdit) []LSPTextEdit {
	converted := make([]selfhost.LSPTextEdit, 0, len(edits))
	for _, edit := range edits {
		converted = append(converted, selfhost.LSPTextEdit{
			StartLine:      int(edit.StartLine),
			StartCharacter: int(edit.StartCharacter),
			EndLine:        int(edit.EndLine),
			EndCharacter:   int(edit.EndCharacter),
			NewText:        edit.NewText,
		})
	}
	resolved := selfhost.ResolveOverlappingLSPTextEdits(converted)
	out := make([]LSPTextEdit, 0, len(resolved))
	for _, edit := range resolved {
		out = append(out, LSPTextEdit{
			StartLine:      uint32(edit.StartLine),
			StartCharacter: uint32(edit.StartCharacter),
			EndLine:        uint32(edit.EndLine),
			EndCharacter:   uint32(edit.EndCharacter),
			NewText:        edit.NewText,
		})
	}
	return out
}

func lspRangeFromSelfhost(rng selfhost.LSPRange) LSPRange {
	return LSPRange{
		Start: LSPPosition{
			Line:      uint32(rng.StartLine),
			Character: uint32(rng.StartCharacter),
		},
		End: LSPPosition{
			Line:      uint32(rng.EndLine),
			Character: uint32(rng.EndCharacter),
		},
	}
}
