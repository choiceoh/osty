package lsp

import (
	"encoding/json"

	"github.com/osty/osty/internal/selfhost"
)

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

type LSPReferenceFact struct {
	URI                  string
	StartLine            uint32
	StartCharacter       uint32
	EndLine              uint32
	EndCharacter         uint32
	TargetSymbolID       string
	TargetURI            string
	TargetStartLine      uint32
	TargetStartCharacter uint32
	TargetEndLine        uint32
	TargetEndCharacter   uint32
	Builtin              bool
}

type LSPSymbolFact struct {
	ID             string
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

type LSPOrganizeUseEntry struct {
	Group  int
	Key    string
	Alias  string
	Text   string
	Unused bool
}

type LSPSignatureParam struct {
	Name     string
	TypeName string
}

type LSPSignatureText struct {
	Label           string
	ParameterLabels []string
}

type LSPFunctionTypeParts struct {
	OK             bool
	ParameterTypes []string
	ReturnType     string
}

type LSPCompletionContext struct {
	Prefix   string
	AfterDot string
}

type LSPDiagnosticPayload struct {
	Severity uint32
	Message  string
}

type LSPDiagnosticView struct {
	StartLine      uint32
	StartCharacter uint32
	EndLine        uint32
	EndCharacter   uint32
	Severity       uint32
	Code           string
	Source         string
	Message        string
}

type LSPDiagnosticFileFact struct {
	DiagnosticFile      string
	PackageFile         string
	SpanSourceFileID    string
	PackageSourceFileID string
	PrimaryLine         int
	PrimaryOffset       int
	SourceLength        int
}

type LSPHeaderParseResult struct {
	OK            bool
	ContentLength int
	Error         string
}

type LSPFileURIPathRawResult struct {
	OK   bool
	Path string
}

type LSPCompletionItemData struct {
	Label         string
	Kind          uint32
	SortText      string
	Detail        string
	Documentation string
}

type LSPCompletionCandidateView struct {
	Name     string
	Kind     string
	TypeText string
	DocText  string
	Include  bool
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

type LSPFullDocumentRange struct {
	EndLine      uint32
	EndCharacter uint32
}

type LSPDispatchDecision struct {
	Action       string
	ErrorCode    int
	ErrorMessage string
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

func LSPServerName() string {
	return selfhost.LSPServerName()
}

func LSPServerVersion() string {
	return selfhost.LSPServerVersion()
}

func LSPPositionEncodingUTF16() string {
	return selfhost.LSPPositionEncodingUTF16()
}

func LSPJSONNull() string {
	return selfhost.LSPJSONNull()
}

func LSPCompletionTriggerDot() string {
	return selfhost.LSPCompletionTriggerDot()
}

func LSPSignatureTriggerOpenParen() string {
	return selfhost.LSPSignatureTriggerOpenParen()
}

func LSPSignatureTriggerComma() string {
	return selfhost.LSPSignatureTriggerComma()
}

func LSPSemanticTokenTypes() []string {
	return selfhost.LSPSemanticTokenTypes()
}

func LSPSemanticTokenModifiers() []string {
	return selfhost.LSPSemanticTokenModifiers()
}

func LSPDispatchActionInitialize() string { return selfhost.LSPDispatchActionInitialize() }
func LSPDispatchActionExit() string       { return selfhost.LSPDispatchActionExit() }
func LSPDispatchActionShutdown() string   { return selfhost.LSPDispatchActionShutdown() }
func LSPDispatchActionIgnore() string     { return selfhost.LSPDispatchActionIgnore() }
func LSPDispatchActionDispatch() string   { return selfhost.LSPDispatchActionDispatch() }
func LSPDispatchActionError() string      { return selfhost.LSPDispatchActionError() }

func LSPAsciiLowerText(text string) string {
	return selfhost.LSPAsciiLowerText(text)
}

func LSPNameMatchesPrefix(name, prefix string) bool {
	return selfhost.LSPNameMatchesPrefix(name, prefix)
}

func LSPNameMatchesQuery(name, query string) bool {
	return selfhost.LSPNameMatchesQuery(name, query)
}

func LSPURIForSourcePath(path string) string {
	return selfhost.LSPURIForSourcePath(path)
}

func LSPFileURIPathRaw(uri string) LSPFileURIPathRawResult {
	result := selfhost.LSPFileURIPathRaw(uri)
	return LSPFileURIPathRawResult{
		OK:   result.OK,
		Path: result.Path,
	}
}

func LSPPreferAIRepairFixAll(src []byte) bool {
	return selfhost.LSPPreferAIRepairFixAll(string(src))
}

func LSPTextChanged(original, replacement []byte) bool {
	return selfhost.LSPTextChanged(string(original), string(replacement))
}

func LSPParamsAreEmpty(params json.RawMessage) bool {
	return selfhost.LSPParamsAreEmpty(string(params))
}

func LSPRenameEmptyNameMessage() string {
	return selfhost.LSPRenameEmptyNameMessage()
}

func LSPCannotRenameBuiltinMessage() string {
	return selfhost.LSPCannotRenameBuiltinMessage()
}

func LSPCanRenameKind(kind string) bool {
	return selfhost.LSPCanRenameKind(kind)
}

func LSPRenameTitle(name string) string {
	return selfhost.LSPRenameTitle(name)
}

func LSPRemoveLineTitle() string {
	return selfhost.LSPRemoveLineTitle()
}

func LSPFixAllTitle() string {
	return selfhost.LSPFixAllTitle()
}

func LSPOrganizeImportsTitle() string {
	return selfhost.LSPOrganizeImportsTitle()
}

func LSPInlayTypeLabel(typeText string) string {
	return selfhost.LSPInlayTypeLabel(typeText)
}

func LSPMethodNotImplementedMessage(method string) string {
	return selfhost.LSPMethodNotImplementedMessage(method)
}

func LSPIsOstySourceFileName(name string) bool {
	return selfhost.LSPIsOstySourceFileName(name)
}

func LSPHasOstyFileExtension(name string) bool {
	return selfhost.LSPHasOstyFileExtension(name)
}

func LSPExitCode(shutdown bool) int {
	return selfhost.LSPExitCode(shutdown)
}

func LSPDispatchDecisionFor(method string, isNotification bool, initialized bool, shutdown bool) LSPDispatchDecision {
	decision := selfhost.LSPDispatchDecisionFor(method, isNotification, initialized, shutdown)
	return LSPDispatchDecision{
		Action:       decision.Action,
		ErrorCode:    decision.ErrorCode,
		ErrorMessage: decision.ErrorMessage,
	}
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

func LSPFullDocumentRangeFor(src []byte, lineStarts []int) LSPFullDocumentRange {
	rng := selfhost.LSPFullDocumentRange(string(src), lineStarts)
	return LSPFullDocumentRange{
		EndLine:      uint32(rng.EndLine),
		EndCharacter: uint32(rng.EndCharacter),
	}
}

func LSPFrameHeader(bodyLength int) string {
	return selfhost.LSPFrameHeader(bodyLength)
}

func LSPTrimHeaderLine(line string) string {
	return selfhost.LSPTrimHeaderLine(line)
}

func LSPParseHeaderLines(lines []string) LSPHeaderParseResult {
	parsed := selfhost.LSPParseHeaderLines(lines)
	return LSPHeaderParseResult{
		OK:            parsed.OK,
		ContentLength: parsed.ContentLength,
		Error:         parsed.Error,
	}
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

func LSPNamedTypeReferenceEndOffset(startOffset, sourceLength int, targetName string, firstPath string) int {
	firstPathMatchesTarget := firstPath != "" && firstPath == targetName
	return selfhost.LSPNamedTypeReferenceEndOffset(startOffset, sourceLength, len(targetName), firstPathMatchesTarget, len(firstPath))
}

func LSPDiagnosticPayloadFor(severity, message, hint string, notes []string) LSPDiagnosticPayload {
	payload := selfhost.LSPDiagnosticPayloadFor(severity, message, hint, notes)
	return LSPDiagnosticPayload{
		Severity: uint32(payload.Severity),
		Message:  payload.Message,
	}
}

func LSPDiagnosticsEqual(a, b []LSPDiagnosticView) bool {
	if len(a) != len(b) {
		return false
	}
	convertedA := make([]selfhost.LSPDiagnosticView, 0, len(a))
	for _, diag := range a {
		convertedA = append(convertedA, selfhost.LSPDiagnosticView{
			StartLine:      int(diag.StartLine),
			StartCharacter: int(diag.StartCharacter),
			EndLine:        int(diag.EndLine),
			EndCharacter:   int(diag.EndCharacter),
			Severity:       int(diag.Severity),
			Code:           diag.Code,
			Source:         diag.Source,
			Message:        diag.Message,
		})
	}
	convertedB := make([]selfhost.LSPDiagnosticView, 0, len(b))
	for _, diag := range b {
		convertedB = append(convertedB, selfhost.LSPDiagnosticView{
			StartLine:      int(diag.StartLine),
			StartCharacter: int(diag.StartCharacter),
			EndLine:        int(diag.EndLine),
			EndCharacter:   int(diag.EndCharacter),
			Severity:       int(diag.Severity),
			Code:           diag.Code,
			Source:         diag.Source,
			Message:        diag.Message,
		})
	}
	return selfhost.LSPDiagnosticsEqual(convertedA, convertedB)
}

func LSPDiagnosticBelongsToFile(fact LSPDiagnosticFileFact) bool {
	return selfhost.LSPDiagnosticBelongsToFile(selfhost.LSPDiagnosticFileFact{
		DiagnosticFile:      fact.DiagnosticFile,
		PackageFile:         fact.PackageFile,
		SpanSourceFileID:    fact.SpanSourceFileID,
		PackageSourceFileID: fact.PackageSourceFileID,
		PrimaryLine:         fact.PrimaryLine,
		PrimaryOffset:       fact.PrimaryOffset,
		SourceLength:        fact.SourceLength,
	})
}

func LSPLevenshteinBounded(a, b string, limit int) int {
	return selfhost.LSPLevenshteinBounded(a, b, limit)
}

func LSPRankNearbyNames(names []string, target string, maxDistance int) []string {
	return selfhost.LSPRankNearbyNames(names, target, maxDistance)
}

func LSPMergeNearbyNames(primary []string, fallback []string) []string {
	return selfhost.LSPMergeNearbyNames(primary, fallback)
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

func LSPLocationsForTarget(refs []LSPReferenceFact, symbols []LSPSymbolFact, targetID string, includeDecl bool) []LSPLocation {
	convertedRefs := make([]selfhost.LSPReferenceFact, 0, len(refs))
	for _, ref := range refs {
		convertedRefs = append(convertedRefs, selfhost.LSPReferenceFact{
			URI:                  ref.URI,
			StartLine:            int(ref.StartLine),
			StartCharacter:       int(ref.StartCharacter),
			EndLine:              int(ref.EndLine),
			EndCharacter:         int(ref.EndCharacter),
			TargetSymbolID:       ref.TargetSymbolID,
			TargetURI:            ref.TargetURI,
			TargetStartLine:      int(ref.TargetStartLine),
			TargetStartCharacter: int(ref.TargetStartCharacter),
			TargetEndLine:        int(ref.TargetEndLine),
			TargetEndCharacter:   int(ref.TargetEndCharacter),
			Builtin:              ref.Builtin,
		})
	}
	convertedSymbols := make([]selfhost.LSPSymbolFact, 0, len(symbols))
	for _, sym := range symbols {
		convertedSymbols = append(convertedSymbols, selfhost.LSPSymbolFact{
			ID:             sym.ID,
			URI:            sym.URI,
			StartLine:      int(sym.StartLine),
			StartCharacter: int(sym.StartCharacter),
			EndLine:        int(sym.EndLine),
			EndCharacter:   int(sym.EndCharacter),
		})
	}
	resolved := selfhost.LSPLocationsForTarget(convertedRefs, convertedSymbols, targetID, includeDecl)
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

func LSPCompletionItemForSymbolView(view selfhost.LSPSymbolView) LSPCompletionItemData {
	item := selfhost.LSPCompletionItemForSymbolView(view)
	return LSPCompletionItemData{
		Label:         item.Label,
		Kind:          uint32(item.Kind),
		SortText:      item.SortText,
		Detail:        item.Detail,
		Documentation: item.Documentation,
	}
}

func LSPCompletionItemsForCandidates(candidates []LSPCompletionCandidateView, prefix string) []LSPCompletionItemData {
	converted := make([]selfhost.LSPCompletionCandidateView, 0, len(candidates))
	for _, candidate := range candidates {
		converted = append(converted, selfhost.LSPCompletionCandidateView{
			Name:     candidate.Name,
			Kind:     candidate.Kind,
			TypeText: candidate.TypeText,
			DocText:  candidate.DocText,
			Include:  candidate.Include,
		})
	}
	items := selfhost.LSPCompletionItemsForCandidates(converted, prefix)
	out := make([]LSPCompletionItemData, 0, len(items))
	for _, item := range items {
		out = append(out, LSPCompletionItemData{
			Label:         item.Label,
			Kind:          uint32(item.Kind),
			SortText:      item.SortText,
			Detail:        item.Detail,
			Documentation: item.Documentation,
		})
	}
	return out
}

func LSPSemanticHoverKind(kind string) string {
	return selfhost.LSPSemanticHoverKind(kind)
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

func SortLSPStringIndexes(values []string) []int {
	return selfhost.SortLSPStringIndexes(values)
}

func SortLSPStrings(values []string) []string {
	if len(values) <= 1 {
		return values
	}
	indexes := SortLSPStringIndexes(values)
	out := make([]string, 0, len(indexes))
	for _, idx := range indexes {
		if idx < 0 || idx >= len(values) {
			continue
		}
		out = append(out, values[idx])
	}
	return out
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

func LSPOrganizedUseBlock(entries []LSPOrganizeUseEntry) string {
	converted := make([]selfhost.LSPOrganizeUseEntry, 0, len(entries))
	for _, entry := range entries {
		converted = append(converted, selfhost.LSPOrganizeUseEntry{
			Group:  entry.Group,
			Key:    entry.Key,
			Alias:  entry.Alias,
			Text:   entry.Text,
			Unused: entry.Unused,
		})
	}
	return selfhost.LSPOrganizedUseBlock(converted)
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

func LSPParseFunctionType(typeText string) LSPFunctionTypeParts {
	parsed := selfhost.LSPParseFunctionType(typeText)
	return LSPFunctionTypeParts{
		OK:             parsed.OK,
		ParameterTypes: append([]string(nil), parsed.ParameterTypes...),
		ReturnType:     parsed.ReturnType,
	}
}

func LSPFallbackParameterNames(count int) []string {
	return selfhost.LSPFallbackParameterNames(count)
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
