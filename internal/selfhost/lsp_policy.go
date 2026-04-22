package selfhost

import "sort"

type LSPSemanticToken struct {
	Line      int
	Column    int
	Length    int
	TokenType int
	Modifiers int
}

type LSPTextEdit struct {
	StartLine      int
	StartCharacter int
	EndLine        int
	EndCharacter   int
	NewText        string
}

type LSPLocation struct {
	URI            string
	StartLine      int
	StartCharacter int
	EndLine        int
	EndCharacter   int
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
	Severity int
	Message  string
}

type LSPPosition struct {
	Line      int
	Character int
}

type LSPRange struct {
	StartLine      int
	StartCharacter int
	EndLine        int
	EndCharacter   int
}

type LSPOstyPosition struct {
	Offset int
	Line   int
	Column int
}

func LSPSemanticTypeForTokenKind(kind, symbolKind string) int {
	return lspSemanticTypeForTokenKind(kind, symbolKind)
}

func LSPSemanticTypeForComment() int {
	return lspSemanticTypeComment()
}

func LSPCompletionKindForSymbolKind(kind string) int {
	return lspCompletionKindForSymbolKind(kind)
}

func LSPCompletionSortTextForSymbolKind(kind, label string) string {
	return lspCompletionSortTextForSymbolKind(kind, label)
}

func LSPCompletionDetail(kind, label, typeText string) string {
	return lspCompletionDetail(kind, label, typeText)
}

func LSPHoverSignatureLine(kind, name, typeText string) string {
	return lspHoverSignatureLine(kind, name, typeText)
}

func LSPPathToURI(path string) string {
	return lspPathToUri(path)
}

func LSPLineStarts(source string) []int {
	unitStarts := lspLineStarts(source)
	byteOffsets := stringUnitByteOffsets(source)
	lines := make([]int, 0, len(unitStarts))
	for _, unitStart := range unitStarts {
		lines = append(lines, unitOffsetToByteOffset(byteOffsets, unitStart))
	}
	return lines
}

func LSPUTF16UnitsInPrefix(source string) int {
	return lspUtf16UnitsInByteRange(source, 0, countStringUnits(source))
}

func LSPOstyPositionToLSP(source string, lineStarts []int, ostyLine, byteOffset int) LSPPosition {
	byteOffsets := stringUnitByteOffsets(source)
	pos := lspOstyPositionToLSP(
		source,
		byteLineStartsToUnitLineStarts(byteOffsets, lineStarts),
		ostyLine,
		byteOffsetToUnitOffset(byteOffsets, byteOffset),
	)
	return LSPPosition{
		Line:      pos.line,
		Character: pos.character,
	}
}

func LSPLSPPositionToOsty(source string, lineStarts []int, lspLine, character int) LSPOstyPosition {
	byteOffsets := stringUnitByteOffsets(source)
	pos := lspPositionToOsty(
		source,
		byteLineStartsToUnitLineStarts(byteOffsets, lineStarts),
		lspLine,
		character,
	)
	return LSPOstyPosition{
		Offset: unitOffsetToByteOffset(byteOffsets, pos.offset),
		Line:   pos.line,
		Column: pos.column,
	}
}

func LSPOffsetToPosition(source string, lineStarts []int, byteOffset int) LSPPosition {
	byteOffsets := stringUnitByteOffsets(source)
	pos := lspOffsetToPosition(
		source,
		byteLineStartsToUnitLineStarts(byteOffsets, lineStarts),
		byteOffsetToUnitOffset(byteOffsets, byteOffset),
	)
	return LSPPosition{
		Line:      pos.line,
		Character: pos.character,
	}
}

func LSPRangeFromOffsets(source string, lineStarts []int, start, end int) LSPRange {
	byteOffsets := stringUnitByteOffsets(source)
	rng := lspRangeFromOffsets(
		source,
		byteLineStartsToUnitLineStarts(byteOffsets, lineStarts),
		byteOffsetToUnitOffset(byteOffsets, start),
		byteOffsetToUnitOffset(byteOffsets, end),
	)
	return LSPRange{
		StartLine:      rng.startLine,
		StartCharacter: rng.startCharacter,
		EndLine:        rng.endLine,
		EndCharacter:   rng.endCharacter,
	}
}

func LSPRangeFromOstySpan(
	source string,
	lineStarts []int,
	startLine,
	startOffset,
	endLine,
	endOffset int,
) LSPRange {
	byteOffsets := stringUnitByteOffsets(source)
	rng := lspRangeFromOstySpan(
		source,
		byteLineStartsToUnitLineStarts(byteOffsets, lineStarts),
		startLine,
		byteOffsetToUnitOffset(byteOffsets, startOffset),
		endLine,
		byteOffsetToUnitOffset(byteOffsets, endOffset),
	)
	return LSPRange{
		StartLine:      rng.startLine,
		StartCharacter: rng.startCharacter,
		EndLine:        rng.endLine,
		EndCharacter:   rng.endCharacter,
	}
}

func LSPSymbolKindForDecl(kind string, mutable bool) int {
	return lspSymbolKindForDecl(kind, mutable)
}

func LSPSymbolKindForMember(kind string) int {
	return lspSymbolKindForMember(kind)
}

func LSPWantsCodeActionKind(only []string, kind string) bool {
	return lspWantsCodeActionKind(only, kind)
}

func LSPPrefixUnderscoreName(name string) string {
	return lspPrefixUnderscoreName(name)
}

func LSPPrefixUnderscoreTitle(name string) string {
	return lspPrefixUnderscoreTitle(name)
}

func LSPFindNameOffset(source string, declStart, declEnd int, name string) int {
	byteOffsets := stringUnitByteOffsets(source)
	unitOffset := lspFindNameOffset(
		source,
		byteOffsetToUnitOffset(byteOffsets, declStart),
		byteOffsetToUnitOffset(byteOffsets, declEnd),
		name,
	)
	if unitOffset < 0 {
		return -1
	}
	return unitOffsetToByteOffset(byteOffsets, unitOffset)
}

func LSPPrecedingCompletionContext(source string, byteOffset int) LSPCompletionContext {
	byteOffsets := stringUnitByteOffsets(source)
	ctx := lspPrecedingCompletionContext(source, byteOffsetToUnitOffset(byteOffsets, byteOffset))
	return LSPCompletionContext{
		Prefix:   ctx.prefix,
		AfterDot: ctx.afterDot,
	}
}

func LSPIdentifierAt(source string, byteOffset int) string {
	return lspIdentifierAt(source, byteOffsetToUnitOffset(stringUnitByteOffsets(source), byteOffset))
}

func LSPContainsPosition(startLine, startColumn, endLine, endColumn, posLine, posColumn int) bool {
	return lspContainsPosition(startLine, startColumn, endLine, endColumn, posLine, posColumn)
}

func LSPSpanOverlaps(startOffset, endOffset, queryStart, queryEnd int) bool {
	return lspSpanOverlaps(startOffset, endOffset, queryStart, queryEnd)
}

func LSPDiagnosticPayloadFor(severity, message, hint string, notes []string) LSPDiagnosticPayload {
	payload := lspDiagnosticPayload(severity, message, hint, notes)
	return LSPDiagnosticPayload{
		Severity: payload.severity,
		Message:  payload.message,
	}
}

func SortDedupLSPLocations(locs []LSPLocation) []LSPLocation {
	tagged := make([]*LspLocation, 0, len(locs))
	for _, loc := range locs {
		tagged = append(tagged, &LspLocation{
			uri:            loc.URI,
			startLine:      loc.StartLine,
			startCharacter: loc.StartCharacter,
			endLine:        loc.EndLine,
			endCharacter:   loc.EndCharacter,
		})
	}
	sorted := lspSortDedupLocations(tagged)
	out := make([]LSPLocation, 0, len(sorted))
	for _, loc := range sorted {
		if loc == nil {
			continue
		}
		out = append(out, LSPLocation{
			URI:            loc.uri,
			StartLine:      loc.startLine,
			StartCharacter: loc.startCharacter,
			EndLine:        loc.endLine,
			EndCharacter:   loc.endCharacter,
		})
	}
	return out
}

func SortLSPSymbolIndexes(keys []LSPSymbolSortKey) []int {
	tagged := make([]*LspSymbolSortKey, 0, len(keys))
	for _, key := range keys {
		tagged = append(tagged, &LspSymbolSortKey{
			name: key.Name,
			uri:  key.URI,
		})
	}
	return lspSortSymbolIndexes(tagged)
}

func SortLSPCompletionIndexes(labels []string) []int {
	keys := make([]LSPSymbolSortKey, 0, len(labels))
	for _, label := range labels {
		keys = append(keys, LSPSymbolSortKey{Name: label})
	}
	return SortLSPSymbolIndexes(keys)
}

func SortLSPImportIndexes(keys []LSPImportSortKey) []int {
	tagged := make([]*LspImportSortKey, 0, len(keys))
	for _, key := range keys {
		tagged = append(tagged, &LspImportSortKey{
			group: key.Group,
			key:   key.Key,
			alias: key.Alias,
		})
	}
	return lspSortImportIndexes(tagged)
}

func LSPUseGroup(isGoFFI bool, path []string) int {
	return lspUseGroup(isGoFFI, path)
}

func LSPUseKey(isGoFFI bool, goPath, rawPath string, path []string) string {
	return lspUseKey(isGoFFI, goPath, rawPath, path)
}

func LSPKeyWithAlias(group int, key, alias string) string {
	return lspKeyWithAlias(group, key, alias)
}

func LSPUseSourceText(source string, start, end int) string {
	byteOffsets := stringUnitByteOffsets(source)
	return lspUseSourceText(
		source,
		byteOffsetToUnitOffset(byteOffsets, start),
		byteOffsetToUnitOffset(byteOffsets, end),
	)
}

func LSPEndOfLineOffset(source string, byteOffset int) int {
	byteOffsets := stringUnitByteOffsets(source)
	return unitOffsetToByteOffset(
		byteOffsets,
		lspEndOfLineOffset(source, byteOffsetToUnitOffset(byteOffsets, byteOffset)),
	)
}

func LSPHasTriviaBetweenOffsets(source string, start, end int) bool {
	byteOffsets := stringUnitByteOffsets(source)
	return lspHasTriviaBetweenOffsets(
		source,
		byteOffsetToUnitOffset(byteOffsets, start),
		byteOffsetToUnitOffset(byteOffsets, end),
	)
}

func LSPActiveParameter(argEndOffsets []int, cursorOffset int) int {
	return lspActiveParameter(argEndOffsets, cursorOffset)
}

func LSPBuildSignatureText(name string, params []LSPSignatureParam, returnType string) LSPSignatureText {
	tagged := make([]*LspSignatureParam, 0, len(params))
	for _, param := range params {
		tagged = append(tagged, &LspSignatureParam{
			name:     param.Name,
			typeName: param.TypeName,
		})
	}
	rendered := lspBuildSignatureText(name, tagged, returnType)
	if rendered == nil {
		return LSPSignatureText{}
	}
	return LSPSignatureText{
		Label:           rendered.label,
		ParameterLabels: append([]string(nil), rendered.parameterLabels...),
	}
}

func EncodeLSPSemanticTokens(tokens []LSPSemanticToken) []int {
	tagged := make([]*LspSemanticToken, 0, len(tokens))
	for _, token := range tokens {
		tagged = append(tagged, &LspSemanticToken{
			line:      token.Line,
			column:    token.Column,
			length:    token.Length,
			tokenType: token.TokenType,
			modifiers: token.Modifiers,
		})
	}
	return lspEncodeSortedSemanticTokens(tagged)
}

func ResolveOverlappingLSPTextEdits(edits []LSPTextEdit) []LSPTextEdit {
	tagged := make([]*LspTextEdit, 0, len(edits))
	for _, edit := range edits {
		tagged = append(tagged, &LspTextEdit{
			startLine:      edit.StartLine,
			startCharacter: edit.StartCharacter,
			endLine:        edit.EndLine,
			endCharacter:   edit.EndCharacter,
			newText:        edit.NewText,
		})
	}
	resolved := lspResolveOverlappingTextEdits(tagged)
	out := make([]LSPTextEdit, 0, len(resolved))
	for _, edit := range resolved {
		if edit == nil {
			continue
		}
		out = append(out, LSPTextEdit{
			StartLine:      edit.startLine,
			StartCharacter: edit.startCharacter,
			EndLine:        edit.endLine,
			EndCharacter:   edit.endCharacter,
			NewText:        edit.newText,
		})
	}
	return out
}

func stringUnitByteOffsets(source string) []int {
	units := splitStringUnits(source)
	offsets := make([]int, len(units)+1)
	off := 0
	for i, unit := range units {
		offsets[i] = off
		off += len(unit)
	}
	offsets[len(units)] = off
	return offsets
}

func byteLineStartsToUnitLineStarts(byteOffsets []int, lineStarts []int) []int {
	if len(lineStarts) == 0 {
		return nil
	}
	units := make([]int, 0, len(lineStarts))
	for _, lineStart := range lineStarts {
		units = append(units, byteOffsetToUnitOffset(byteOffsets, lineStart))
	}
	return units
}

func byteOffsetToUnitOffset(byteOffsets []int, byteOffset int) int {
	if len(byteOffsets) == 0 {
		return 0
	}
	if byteOffset <= 0 {
		return 0
	}
	limit := byteOffsets[len(byteOffsets)-1]
	if byteOffset >= limit {
		return len(byteOffsets) - 1
	}
	idx := sort.Search(len(byteOffsets), func(i int) bool {
		return byteOffsets[i] >= byteOffset
	})
	if idx < len(byteOffsets) && byteOffsets[idx] == byteOffset {
		return idx
	}
	if idx <= 0 {
		return 0
	}
	return idx - 1
}

func unitOffsetToByteOffset(byteOffsets []int, unitOffset int) int {
	if len(byteOffsets) == 0 || unitOffset <= 0 {
		return 0
	}
	last := len(byteOffsets) - 1
	if unitOffset >= last {
		return byteOffsets[last]
	}
	return byteOffsets[unitOffset]
}
