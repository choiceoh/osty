//go:build !selfhostgen

package selfhost

// LSPSemanticToken is the exported Go-facing shape for a semantic token before
// LSP delta encoding. The self-hosted implementation owns the token legend
// policy and the wire encoding; the Go LSP server owns source-position math.
type LSPSemanticToken struct {
	Line      uint32
	Column    uint32
	Length    uint32
	TokenType uint32
	Modifiers uint32
}

// LSPTextEdit is a pure-policy mirror of an LSP TextEdit. It is intentionally
// protocol-shaped but package-neutral so internal/lsp can convert without an
// import cycle.
type LSPTextEdit struct {
	StartLine      uint32
	StartCharacter uint32
	EndLine        uint32
	EndCharacter   uint32
	NewText        string
}

// LSPLocation is a minimal package-neutral Location used for self-hosted
// sorting and deduplication policy.
type LSPLocation struct {
	URI            string
	StartLine      uint32
	StartCharacter uint32
	EndLine        uint32
	EndCharacter   uint32
}

// LSPSymbolSortKey is the self-hosted workspace-symbol ordering key.
type LSPSymbolSortKey struct {
	Name string
	URI  string
}

// LSPImportSortKey is the self-hosted organize-import ordering key.
type LSPImportSortKey struct {
	Group int
	Key   string
	Alias string
}

// LSPSignatureParam is one rendered signature parameter.
type LSPSignatureParam struct {
	Name     string
	TypeName string
}

// LSPSignatureText is the rendered signature label plus parameter labels.
type LSPSignatureText struct {
	Label           string
	ParameterLabels []string
}

// LSPCompletionContext is the prefix/dot context immediately before a cursor.
type LSPCompletionContext struct {
	Prefix   string
	AfterDot string
}

// LSPDiagnosticPayload carries the self-hosted diagnostic severity/message
// projection, before Go attaches source ranges and diagnostic codes.
type LSPDiagnosticPayload struct {
	Severity uint32
	Message  string
}

// LSPPosition and LSPRange mirror the protocol's UTF-16 position/range shape.
type LSPPosition struct {
	Line      uint32
	Character uint32
}

type LSPRange struct {
	Start LSPPosition
	End   LSPPosition
}

// LSPOstyPosition mirrors token.Pos without importing token into callers that
// only need the self-hosted position conversion policy.
type LSPOstyPosition struct {
	Offset int
	Line   int
	Column int
}

// LSPSemanticTypeForTokenKind maps token.Kind.String() plus an optional
// resolve.SymbolKind.String() value to a semantic-token legend index.
func LSPSemanticTypeForTokenKind(kind, symbolKind string) (uint32, bool) {
	tokenType := lspSemanticTypeForTokenKind(kind, symbolKind)
	if tokenType < 0 {
		return 0, false
	}
	return uint32(tokenType), true
}

// LSPSemanticTypeForComment returns the legend index for comments.
func LSPSemanticTypeForComment() uint32 {
	return uint32(lspSemanticTypeComment())
}

// LSPCompletionKindForSymbolKind maps resolve.SymbolKind.String() to the LSP
// CompletionItemKind enum value.
func LSPCompletionKindForSymbolKind(kind string) uint32 {
	return uint32(lspCompletionKindForSymbolKind(kind))
}

// LSPCompletionSortTextForSymbolKind maps a resolver symbol kind and label to
// the deterministic sort bucket used by completion.
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

func LSPLineStarts(src []byte) []int {
	text := string(src)
	rt := newRuneTable(text)
	unitStarts := lspLineStarts(text)
	out := make([]int, 0, len(unitStarts))
	for _, unitStart := range unitStarts {
		out = append(out, rt.byteOffset(unitStart))
	}
	return out
}

func LSPUTF16UnitsInPrefix(src []byte) uint32 {
	text := string(src)
	return uint32(lspUtf16UnitsInByteRange(text, 0, len(newRuneTable(text).runes)))
}

func LSPOstyPositionToLSP(src []byte, lineStarts []int, line, offset int) LSPPosition {
	text := string(src)
	rt := newRuneTable(text)
	pos := lspOstyPositionToLSP(text, lspUnitLineStarts(rt, lineStarts), line, runeOffsetForByte(rt, offset))
	if pos == nil {
		return LSPPosition{}
	}
	return LSPPosition{Line: uint32(pos.line), Character: uint32(pos.character)}
}

func LSPLSPPositionToOsty(src []byte, lineStarts []int, line, character uint32) LSPOstyPosition {
	text := string(src)
	rt := newRuneTable(text)
	pos := lspPositionToOsty(text, lspUnitLineStarts(rt, lineStarts), int(line), int(character))
	if pos == nil {
		return LSPOstyPosition{Line: 1, Column: 1}
	}
	return LSPOstyPosition{Offset: rt.byteOffset(pos.offset), Line: pos.line, Column: pos.column}
}

func LSPOffsetToPosition(src []byte, lineStarts []int, offset int) LSPPosition {
	text := string(src)
	rt := newRuneTable(text)
	pos := lspOffsetToPosition(text, lspUnitLineStarts(rt, lineStarts), runeOffsetForByte(rt, clampByteOffset(offset, len(src))))
	if pos == nil {
		return LSPPosition{}
	}
	return LSPPosition{Line: uint32(pos.line), Character: uint32(pos.character)}
}

func LSPRangeFromOffsets(src []byte, lineStarts []int, start, end int) LSPRange {
	text := string(src)
	rt := newRuneTable(text)
	rng := lspRangeFromOffsets(text, lspUnitLineStarts(rt, lineStarts), runeOffsetForByte(rt, clampByteOffset(start, len(src))), runeOffsetForByte(rt, clampByteOffset(end, len(src))))
	return adaptLSPRange(rng)
}

func LSPRangeFromOstySpan(src []byte, lineStarts []int, startLine, startOffset, endLine, endOffset int) LSPRange {
	text := string(src)
	rt := newRuneTable(text)
	rng := lspRangeFromOstySpan(text, lspUnitLineStarts(rt, lineStarts), startLine, runeOffsetForByte(rt, clampByteOffset(startOffset, len(src))), endLine, runeOffsetForByte(rt, clampByteOffset(endOffset, len(src))))
	return adaptLSPRange(rng)
}

func adaptLSPRange(rng *LspRange) LSPRange {
	if rng == nil {
		return LSPRange{}
	}
	return LSPRange{
		Start: LSPPosition{Line: uint32(rng.startLine), Character: uint32(rng.startCharacter)},
		End:   LSPPosition{Line: uint32(rng.endLine), Character: uint32(rng.endCharacter)},
	}
}

func lspUnitLineStarts(rt runeTable, lineStarts []int) []int {
	out := make([]int, 0, len(lineStarts))
	for _, start := range lineStarts {
		out = append(out, runeOffsetForByte(rt, clampByteOffset(start, len(rt.src))))
	}
	if len(out) == 0 {
		out = append(out, 0)
	}
	return out
}

func LSPSymbolKindForDecl(kind string, mutable bool) uint32 {
	return uint32(lspSymbolKindForDecl(kind, mutable))
}

func LSPSymbolKindForMember(kind string) uint32 {
	return uint32(lspSymbolKindForMember(kind))
}

// LSPWantsCodeActionKind applies the LSP CodeActionKind prefix rule.
func LSPWantsCodeActionKind(only []string, kind string) bool {
	return lspWantsCodeActionKind(append([]string(nil), only...), kind)
}

func LSPPrefixUnderscoreName(name string) string {
	return lspPrefixUnderscoreName(name)
}

func LSPPrefixUnderscoreTitle(name string) string {
	return lspPrefixUnderscoreTitle(name)
}

// LSPFindNameOffset locates a declaration name through the self-hosted lexer.
// The public Go surface uses byte offsets, while lsp.osty works in lexer unit
// offsets, so this adapter maps both directions around the policy call.
func LSPFindNameOffset(src []byte, declStart, declEnd int, name string) int {
	if name == "" || declStart < 0 || declEnd > len(src) || declEnd <= declStart {
		return -1
	}
	text := string(src)
	rt := newRuneTable(text)
	startRune := runeOffsetForByte(rt, declStart)
	endRune := runeOffsetForByte(rt, declEnd)
	unitOff := lspFindNameOffset(text, startRune, endRune, name)
	if unitOff < 0 {
		return -1
	}
	return rt.byteOffset(unitOff)
}

// LSPPrecedingCompletionContext extracts the partial identifier and optional
// dot receiver immediately before a byte offset.
func LSPPrecedingCompletionContext(src []byte, offset int) LSPCompletionContext {
	text := string(src)
	rt := newRuneTable(text)
	unitOffset := runeOffsetForByte(rt, clampByteOffset(offset, len(src)))
	ctx := lspPrecedingCompletionContext(text, unitOffset)
	if ctx == nil {
		return LSPCompletionContext{}
	}
	return LSPCompletionContext{Prefix: ctx.prefix, AfterDot: ctx.afterDot}
}

func LSPIdentifierAt(src []byte, offset int) string {
	if offset < 0 || offset >= len(src) {
		return ""
	}
	text := string(src)
	rt := newRuneTable(text)
	unitOffset := runeOffsetForByte(rt, offset)
	return lspIdentifierAt(text, unitOffset)
}

func LSPContainsPosition(startLine, startColumn, endLine, endColumn, posLine, posColumn int) bool {
	return lspContainsPosition(startLine, startColumn, endLine, endColumn, posLine, posColumn)
}

func LSPSpanOverlaps(startOffset, endOffset, queryStart, queryEnd int) bool {
	return lspSpanOverlaps(startOffset, endOffset, queryStart, queryEnd)
}

func LSPDiagnosticPayloadFor(severity, message, hint string, notes []string) LSPDiagnosticPayload {
	payload := lspDiagnosticPayload(severity, message, hint, append([]string(nil), notes...))
	if payload == nil {
		return LSPDiagnosticPayload{Severity: uint32(lspDiagnosticSeverityError()), Message: message}
	}
	return LSPDiagnosticPayload{Severity: uint32(payload.severity), Message: payload.message}
}

func SortDedupLSPLocations(locs []LSPLocation) []LSPLocation {
	ostyLocs := make([]*LspLocation, 0, len(locs))
	for _, loc := range locs {
		ostyLocs = append(ostyLocs, &LspLocation{
			uri:            loc.URI,
			startLine:      int(loc.StartLine),
			startCharacter: int(loc.StartCharacter),
			endLine:        int(loc.EndLine),
			endCharacter:   int(loc.EndCharacter),
		})
	}
	resolved := lspSortDedupLocations(ostyLocs)
	out := make([]LSPLocation, 0, len(resolved))
	for _, loc := range resolved {
		if loc == nil {
			continue
		}
		out = append(out, LSPLocation{
			URI:            loc.uri,
			StartLine:      uint32(loc.startLine),
			StartCharacter: uint32(loc.startCharacter),
			EndLine:        uint32(loc.endLine),
			EndCharacter:   uint32(loc.endCharacter),
		})
	}
	return out
}

func SortLSPSymbolIndexes(keys []LSPSymbolSortKey) []int {
	ostyKeys := make([]*LspSymbolSortKey, 0, len(keys))
	for _, key := range keys {
		ostyKeys = append(ostyKeys, &LspSymbolSortKey{name: key.Name, uri: key.URI})
	}
	indexes := lspSortSymbolIndexes(ostyKeys)
	out := make([]int, 0, len(indexes))
	for _, index := range indexes {
		out = append(out, index)
	}
	return out
}

func SortLSPCompletionIndexes(labels []string) []int {
	keys := make([]LSPSymbolSortKey, 0, len(labels))
	for _, label := range labels {
		keys = append(keys, LSPSymbolSortKey{Name: label})
	}
	return SortLSPSymbolIndexes(keys)
}

func SortLSPImportIndexes(keys []LSPImportSortKey) []int {
	ostyKeys := make([]*LspImportSortKey, 0, len(keys))
	for _, key := range keys {
		ostyKeys = append(ostyKeys, &LspImportSortKey{
			group: key.Group,
			key:   key.Key,
			alias: key.Alias,
		})
	}
	indexes := lspSortImportIndexes(ostyKeys)
	out := make([]int, 0, len(indexes))
	for _, index := range indexes {
		out = append(out, index)
	}
	return out
}

func LSPUseGroup(isGoFFI bool, path []string) int {
	return lspUseGroup(isGoFFI, append([]string(nil), path...))
}

func LSPUseKey(isGoFFI bool, goPath, rawPath string, path []string) string {
	return lspUseKey(isGoFFI, goPath, rawPath, append([]string(nil), path...))
}

func LSPKeyWithAlias(group int, key, alias string) string {
	return lspKeyWithAlias(group, key, alias)
}

func LSPUseSourceText(src []byte, start, end int) string {
	if start < 0 || end > len(src) || start >= end {
		return ""
	}
	text := string(src)
	rt := newRuneTable(text)
	startRune := runeOffsetForByte(rt, start)
	endRune := runeOffsetForByte(rt, end)
	return lspUseSourceText(text, startRune, endRune)
}

func LSPEndOfLineOffset(src []byte, offset int) int {
	text := string(src)
	rt := newRuneTable(text)
	unitOffset := runeOffsetForByte(rt, clampByteOffset(offset, len(src)))
	return rt.byteOffset(lspEndOfLineOffset(text, unitOffset))
}

func LSPHasTriviaBetweenOffsets(src []byte, start, end int) bool {
	if start < 0 || end > len(src) || start >= end {
		return false
	}
	text := string(src)
	rt := newRuneTable(text)
	startRune := runeOffsetForByte(rt, start)
	endRune := runeOffsetForByte(rt, end)
	return lspHasTriviaBetweenOffsets(text, startRune, endRune)
}

func LSPActiveParameter(argEndOffsets []int, cursorOffset int) uint32 {
	return uint32(lspActiveParameter(append([]int(nil), argEndOffsets...), cursorOffset))
}

// LSPBuildSignatureText renders the stable signature label and parameter
// labels from already-computed names/types.
func LSPBuildSignatureText(name string, params []LSPSignatureParam, returnType string) LSPSignatureText {
	ostyParams := make([]*LspSignatureParam, 0, len(params))
	for _, param := range params {
		ostyParams = append(ostyParams, &LspSignatureParam{name: param.Name, typeName: param.TypeName})
	}
	rendered := lspBuildSignatureText(name, ostyParams, returnType)
	if rendered == nil {
		return LSPSignatureText{}
	}
	return LSPSignatureText{
		Label:           rendered.label,
		ParameterLabels: append([]string(nil), rendered.parameterLabels...),
	}
}

// EncodeLSPSemanticTokens sorts and delta-encodes absolute semantic token
// positions using the self-hosted LSP policy implementation.
func EncodeLSPSemanticTokens(tokens []LSPSemanticToken) []uint32 {
	ostyTokens := make([]*LspSemanticToken, 0, len(tokens))
	for _, token := range tokens {
		ostyTokens = append(ostyTokens, &LspSemanticToken{
			line:      int(token.Line),
			column:    int(token.Column),
			length:    int(token.Length),
			tokenType: int(token.TokenType),
			modifiers: int(token.Modifiers),
		})
	}
	encoded := lspEncodeSortedSemanticTokens(ostyTokens)
	out := make([]uint32, 0, len(encoded))
	for _, value := range encoded {
		if value < 0 {
			value = 0
		}
		out = append(out, uint32(value))
	}
	return out
}

func clampByteOffset(off, max int) int {
	if off < 0 {
		return 0
	}
	if off > max {
		return max
	}
	return off
}

func runeOffsetForByte(rt runeTable, off int) int {
	if off <= 0 {
		return 0
	}
	if off >= len(rt.src) {
		return len(rt.runes)
	}
	lo, hi := 0, len(rt.byteStart)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if rt.byteStart[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// ResolveOverlappingLSPTextEdits sorts edits and drops overlapping edits using
// the self-hosted fix-all policy.
func ResolveOverlappingLSPTextEdits(edits []LSPTextEdit) []LSPTextEdit {
	ostyEdits := make([]*LspTextEdit, 0, len(edits))
	for _, edit := range edits {
		ostyEdits = append(ostyEdits, &LspTextEdit{
			startLine:      int(edit.StartLine),
			startCharacter: int(edit.StartCharacter),
			endLine:        int(edit.EndLine),
			endCharacter:   int(edit.EndCharacter),
			newText:        edit.NewText,
		})
	}
	resolved := lspResolveOverlappingTextEdits(ostyEdits)
	out := make([]LSPTextEdit, 0, len(resolved))
	for _, edit := range resolved {
		if edit == nil {
			continue
		}
		out = append(out, LSPTextEdit{
			StartLine:      uint32(edit.startLine),
			StartCharacter: uint32(edit.startCharacter),
			EndLine:        uint32(edit.endLine),
			EndCharacter:   uint32(edit.endCharacter),
			NewText:        edit.newText,
		})
	}
	return out
}
