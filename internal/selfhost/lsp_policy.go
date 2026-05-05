package selfhost

import (
	"sort"
	"strconv"
	"strings"
)

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

type LSPReferenceFact struct {
	URI                  string
	StartLine            int
	StartCharacter       int
	EndLine              int
	EndCharacter         int
	TargetSymbolID       string
	TargetURI            string
	TargetStartLine      int
	TargetStartCharacter int
	TargetEndLine        int
	TargetEndCharacter   int
	Builtin              bool
}

type LSPSymbolFact struct {
	ID             string
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
	Severity int
	Message  string
}

type LSPDiagnosticView struct {
	StartLine      int
	StartCharacter int
	EndLine        int
	EndCharacter   int
	Severity       int
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
	Kind          int
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

// LSPSymbolView is the value-typed snapshot the LSP policy layer
// consumes when rendering hover / signature / completion text. The
// extractor at the LSP boundary (which still holds *resolve.Symbol)
// projects into this struct exactly once; every downstream formatter
// stays pointer-free, which keeps the policy a candidate for
// migration to toolchain/lsp.osty when the LLVM self-host LSP lands.
type LSPSymbolView struct {
	Name     string
	Kind     string
	TypeText string
	DocText  string
	HasSym   bool
}

// LSPUseDeclView is the value-typed projection of an *ast.UseDecl
// the refactor / organize-imports policy consumes. PosOffset and
// EndOffset cover the source range; the rest mirror the fields the
// existing selfhost helpers (LSPUseGroup / LSPUseKey / LSPUseSourceText)
// already accept as plain values.
type LSPUseDeclView struct {
	PosOffset int
	EndOffset int
	Path      []string
	RawPath   string
	Alias     string
	IsFFI     bool
	FFIPath   string
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

type LSPFullDocumentRangeResult struct {
	EndLine      int
	EndCharacter int
}

type LSPDispatchDecision struct {
	Action       string
	ErrorCode    int
	ErrorMessage string
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

// LSPHoverMarkdown renders the markdown body shown on hover from a
// pre-extracted symbol view. Wraps the signature line in an `osty`
// fenced block and appends the doc comment when present; the no-sym
// path emits the fallback identifier text alone.
func LSPHoverMarkdown(view LSPSymbolView) string {
	var b strings.Builder
	b.WriteString("```osty\n")
	if view.HasSym {
		b.WriteString(LSPHoverSignatureLine(view.Kind, view.Name, view.TypeText))
	} else {
		b.WriteString(view.Name)
	}
	b.WriteString("\n```")
	if view.HasSym && view.DocText != "" {
		b.WriteString("\n\n")
		b.WriteString(view.DocText)
	}
	return b.String()
}

func LSPPathToURI(path string) string {
	return lspPathToUri(path)
}

func LSPFileURIPathRaw(uri string) LSPFileURIPathRawResult {
	const prefix = "file://"
	if !strings.HasPrefix(uri, prefix) {
		return LSPFileURIPathRawResult{}
	}
	path := strings.TrimPrefix(uri, prefix)
	if len(path) >= 3 && path[0] == '/' && path[2] == ':' {
		path = path[1:]
	}
	return LSPFileURIPathRawResult{OK: true, Path: path}
}

func LSPServerName() string {
	return "osty-lsp"
}

func LSPServerVersion() string {
	return "0.1.0"
}

func LSPPositionEncodingUTF16() string {
	return "utf-16"
}

func LSPJSONNull() string {
	return "null"
}

func LSPCompletionTriggerDot() string {
	return "."
}

func LSPSignatureTriggerOpenParen() string {
	return "("
}

func LSPSignatureTriggerComma() string {
	return ","
}

func LSPSemanticTokenTypes() []string {
	return []string{
		"namespace",
		"type",
		"parameter",
		"variable",
		"property",
		"function",
		"keyword",
		"string",
		"number",
		"operator",
		"comment",
		"enumMember",
	}
}

func LSPSemanticTokenModifiers() []string {
	return []string{"declaration", "readonly"}
}

func LSPDispatchActionInitialize() string { return "initialize" }
func LSPDispatchActionExit() string       { return "exit" }
func LSPDispatchActionShutdown() string   { return "shutdown" }
func LSPDispatchActionIgnore() string     { return "ignore" }
func LSPDispatchActionDispatch() string   { return "dispatch" }
func LSPDispatchActionError() string      { return "error" }
func LSPErrInvalidRequest() int           { return -32600 }
func LSPErrMethodNotFound() int           { return -32601 }
func LSPErrServerNotInitialized() int     { return -32002 }

func LSPAsciiLowerText(text string) string {
	return strings.ToLower(text)
}

func LSPNameMatchesPrefix(name, prefix string) bool {
	return prefix == "" || strings.HasPrefix(name, prefix)
}

func LSPNameMatchesQuery(name, query string) bool {
	return query == "" || strings.Contains(strings.ToLower(name), query)
}

func LSPURIForSourcePath(path string) string {
	if strings.Contains(path, ":") && !strings.HasPrefix(path, "/") {
		return path
	}
	return LSPPathToURI(path)
}

func LSPPreferAIRepairFixAll(source string) bool {
	return strings.Contains(source, ".length")
}

func LSPTextChanged(original, replacement string) bool {
	return original != replacement
}

func LSPParamsAreEmpty(paramsText string) bool {
	return paramsText == "" || paramsText == "null"
}

func LSPRenameEmptyNameMessage() string {
	return "new name is empty"
}

func LSPCannotRenameBuiltinMessage() string {
	return "cannot rename a builtin"
}

func LSPCanRenameKind(kind string) bool {
	return kind != "builtin"
}

func LSPRenameTitle(name string) string {
	return "Rename to `" + name + "`"
}

func LSPRemoveLineTitle() string {
	return "Remove unused import"
}

func LSPFixAllTitle() string {
	return "Fix all auto-fixable problems"
}

func LSPOrganizeImportsTitle() string {
	return "Organize imports"
}

func LSPInlayTypeLabel(typeText string) string {
	return ": " + typeText
}

func LSPMethodNotImplementedMessage(method string) string {
	return "method not implemented: " + method
}

func LSPIsOstySourceFileName(name string) bool {
	return strings.HasSuffix(name, ".osty") && !strings.HasSuffix(name, "_test.osty")
}

func LSPHasOstyFileExtension(name string) bool {
	return strings.HasSuffix(name, ".osty")
}

func LSPExitCode(shutdown bool) int {
	if shutdown {
		return 0
	}
	return 1
}

func LSPDispatchDecisionFor(method string, isNotification bool, initialized bool, shutdown bool) LSPDispatchDecision {
	if method == "initialize" {
		return LSPDispatchDecision{Action: LSPDispatchActionInitialize()}
	}
	if method == "exit" {
		return LSPDispatchDecision{Action: LSPDispatchActionExit()}
	}
	if !initialized {
		if isNotification {
			return LSPDispatchDecision{Action: LSPDispatchActionIgnore()}
		}
		return LSPDispatchDecision{
			Action:       LSPDispatchActionError(),
			ErrorCode:    LSPErrServerNotInitialized(),
			ErrorMessage: "server has not been initialized",
		}
	}
	if shutdown {
		if isNotification {
			return LSPDispatchDecision{Action: LSPDispatchActionIgnore()}
		}
		return LSPDispatchDecision{
			Action:       LSPDispatchActionError(),
			ErrorCode:    LSPErrInvalidRequest(),
			ErrorMessage: "server is shutting down",
		}
	}
	if method == "initialized" {
		return LSPDispatchDecision{Action: LSPDispatchActionIgnore()}
	}
	if method == "shutdown" {
		return LSPDispatchDecision{Action: LSPDispatchActionShutdown()}
	}
	if lspMethodIsHandled(method) {
		return LSPDispatchDecision{Action: LSPDispatchActionDispatch()}
	}
	if isNotification {
		return LSPDispatchDecision{Action: LSPDispatchActionIgnore()}
	}
	return LSPDispatchDecision{
		Action:       LSPDispatchActionError(),
		ErrorCode:    LSPErrMethodNotFound(),
		ErrorMessage: LSPMethodNotImplementedMessage(method),
	}
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

func LSPFrameHeader(bodyLength int) string {
	return "Content-Length: " + strconv.Itoa(bodyLength) + "\r\n\r\n"
}

func LSPTrimHeaderLine(line string) string {
	return strings.TrimRight(line, "\r\n")
}

func LSPParseHeaderLines(lines []string) LSPHeaderParseResult {
	if len(lines) == 0 {
		return LSPHeaderParseResult{ContentLength: -1, Error: "lsp: empty header block"}
	}
	length := -1
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return LSPHeaderParseResult{ContentLength: -1, Error: "lsp: malformed header " + line}
		}
		if strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return LSPHeaderParseResult{ContentLength: -1, Error: "lsp: invalid Content-Length " + strings.TrimSpace(value)}
			}
			length = n
		}
	}
	return LSPHeaderParseResult{OK: true, ContentLength: length}
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

func LSPFullDocumentRange(source string, lineStarts []int) LSPFullDocumentRangeResult {
	endLine := 0
	lastLineStart := 0
	if len(lineStarts) > 0 {
		endLine = len(lineStarts) - 1
		lastLineStart = lineStarts[len(lineStarts)-1]
	}
	if lastLineStart < 0 {
		lastLineStart = 0
	}
	if lastLineStart > len(source) {
		lastLineStart = len(source)
	}
	return LSPFullDocumentRangeResult{
		EndLine:      endLine,
		EndCharacter: LSPUTF16UnitsInPrefix(source[lastLineStart:]),
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

func LSPNamedTypeReferenceEndOffset(startOffset, sourceLength, targetNameLength int, firstPathMatchesTarget bool, firstPathLength int) int {
	width := targetNameLength
	if firstPathMatchesTarget {
		width = firstPathLength
	}
	endOffset := startOffset + width
	if endOffset > sourceLength {
		endOffset = sourceLength
	}
	return endOffset
}

func LSPDiagnosticPayloadFor(severity, message, hint string, notes []string) LSPDiagnosticPayload {
	payload := lspDiagnosticPayload(severity, message, hint, notes)
	return LSPDiagnosticPayload{
		Severity: payload.severity,
		Message:  payload.message,
	}
}

func LSPDiagnosticsEqual(a, b []LSPDiagnosticView) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func LSPDiagnosticBelongsToFile(fact LSPDiagnosticFileFact) bool {
	if fact.DiagnosticFile != "" {
		return fact.DiagnosticFile == fact.PackageFile
	}
	if fact.SpanSourceFileID != "" && fact.PackageSourceFileID != "" {
		return fact.SpanSourceFileID == fact.PackageSourceFileID
	}
	if fact.PrimaryLine == 0 {
		return false
	}
	return fact.PrimaryOffset <= fact.SourceLength
}

func LSPLevenshteinBounded(a, b string, limit int) int {
	ar := []rune(a)
	br := []rune(b)
	if absInt(len(ar)-len(br)) > limit {
		return limit + 1
	}
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		rowMin := curr[0]
		for j := 1; j <= len(br); j++ {
			cost := 0
			if ar[i-1] != br[j-1] {
				cost = 1
			}
			curr[j] = minInt(
				prev[j]+1,
				curr[j-1]+1,
				prev[j-1]+cost,
			)
			if curr[j] < rowMin {
				rowMin = curr[j]
			}
		}
		if rowMin > limit {
			return limit + 1
		}
		prev, curr = curr, prev
	}
	if prev[len(br)] > limit {
		return limit + 1
	}
	return prev[len(br)]
}

func LSPRankNearbyNames(names []string, target string, maxDistance int) []string {
	type candidate struct {
		name string
		dist int
	}
	seen := map[string]bool{}
	candidates := make([]candidate, 0, len(names))
	for _, name := range names {
		if name == "" || seen[name] {
			continue
		}
		dist := LSPLevenshteinBounded(target, name, maxDistance)
		if dist > maxDistance {
			continue
		}
		seen[name] = true
		candidates = append(candidates, candidate{name: name, dist: dist})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].dist != candidates[j].dist {
			return candidates[i].dist < candidates[j].dist
		}
		return candidates[i].name < candidates[j].name
	})
	out := make([]string, 0, len(candidates))
	for _, c := range candidates {
		out = append(out, c.name)
	}
	return out
}

func LSPMergeNearbyNames(primary []string, fallback []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(primary)+len(fallback))
	for _, group := range [][]string{primary, fallback} {
		for _, name := range group {
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
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

func LSPLocationsForTarget(refs []LSPReferenceFact, symbols []LSPSymbolFact, targetID string, includeDecl bool) []LSPLocation {
	if targetID == "" {
		return nil
	}
	out := make([]LSPLocation, 0, len(refs)+1)
	var fallbackDecl *LSPLocation
	for _, ref := range refs {
		if ref.TargetSymbolID != targetID {
			continue
		}
		out = append(out, LSPLocation{
			URI:            ref.URI,
			StartLine:      ref.StartLine,
			StartCharacter: ref.StartCharacter,
			EndLine:        ref.EndLine,
			EndCharacter:   ref.EndCharacter,
		})
		if fallbackDecl == nil && ref.TargetURI != "" && !ref.Builtin {
			fallbackDecl = &LSPLocation{
				URI:            ref.TargetURI,
				StartLine:      ref.TargetStartLine,
				StartCharacter: ref.TargetStartCharacter,
				EndLine:        ref.TargetEndLine,
				EndCharacter:   ref.TargetEndCharacter,
			}
		}
	}
	if includeDecl {
		haveSymbolDecl := false
		for _, sym := range symbols {
			if sym.ID != targetID {
				continue
			}
			out = append(out, LSPLocation{
				URI:            sym.URI,
				StartLine:      sym.StartLine,
				StartCharacter: sym.StartCharacter,
				EndLine:        sym.EndLine,
				EndCharacter:   sym.EndCharacter,
			})
			haveSymbolDecl = true
			break
		}
		if !haveSymbolDecl && fallbackDecl != nil {
			out = append(out, *fallbackDecl)
		}
	}
	return SortDedupLSPLocations(out)
}

func LSPCompletionItemForSymbolView(view LSPSymbolView) LSPCompletionItemData {
	detail := ""
	if view.TypeText != "" {
		detail = LSPCompletionDetail(view.Kind, view.Name, view.TypeText)
	}
	return LSPCompletionItemData{
		Label:         view.Name,
		Kind:          LSPCompletionKindForSymbolKind(view.Kind),
		SortText:      LSPCompletionSortTextForSymbolKind(view.Kind, view.Name),
		Detail:        detail,
		Documentation: view.DocText,
	}
}

func LSPCompletionItemsForCandidates(candidates []LSPCompletionCandidateView, prefix string) []LSPCompletionItemData {
	seen := map[string]bool{}
	items := make([]LSPCompletionItemData, 0, len(candidates))
	for _, candidate := range candidates {
		if !candidate.Include || candidate.Name == "" {
			continue
		}
		if seen[candidate.Name] {
			continue
		}
		if !LSPNameMatchesPrefix(candidate.Name, prefix) {
			continue
		}
		seen[candidate.Name] = true
		items = append(items, LSPCompletionItemForSymbolView(LSPSymbolView{
			Name:     candidate.Name,
			Kind:     candidate.Kind,
			TypeText: candidate.TypeText,
			DocText:  candidate.DocText,
			HasSym:   true,
		}))
	}
	labels := make([]string, 0, len(items))
	for _, item := range items {
		labels = append(labels, item.Label)
	}
	order := SortLSPStringIndexes(labels)
	out := make([]LSPCompletionItemData, 0, len(order))
	for _, idx := range order {
		if idx < 0 || idx >= len(items) {
			continue
		}
		out = append(out, items[idx])
	}
	return out
}

func LSPSemanticHoverKind(kind string) string {
	switch kind {
	case "fn":
		return "function"
	case "value":
		return "binding"
	case "generic":
		return "type parameter"
	default:
		return kind
	}
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

func SortLSPStringIndexes(values []string) []int {
	type indexedString struct {
		value string
		index int
	}
	tagged := make([]indexedString, 0, len(values))
	for idx, value := range values {
		tagged = append(tagged, indexedString{value: value, index: idx})
	}
	sort.Slice(tagged, func(i, j int) bool {
		if tagged[i].value != tagged[j].value {
			return tagged[i].value < tagged[j].value
		}
		return tagged[i].index < tagged[j].index
	})
	out := make([]int, 0, len(tagged))
	for _, item := range tagged {
		out = append(out, item.index)
	}
	return out
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

func LSPOrganizedUseBlock(entries []LSPOrganizeUseEntry) string {
	kept := make([]LSPOrganizeUseEntry, 0, len(entries))
	seen := map[string]bool{}
	for _, entry := range entries {
		if entry.Unused || entry.Text == "" {
			continue
		}
		dedupKey := LSPKeyWithAlias(entry.Group, entry.Key, entry.Alias)
		if seen[dedupKey] {
			continue
		}
		seen[dedupKey] = true
		kept = append(kept, entry)
	}
	keys := make([]LSPImportSortKey, 0, len(kept))
	for _, entry := range kept {
		keys = append(keys, LSPImportSortKey{
			Group: entry.Group,
			Key:   entry.Key,
			Alias: entry.Alias,
		})
	}
	order := SortLSPImportIndexes(keys)
	var b strings.Builder
	prevGroup := -1
	emitted := 0
	for _, idx := range order {
		if idx < 0 || idx >= len(kept) {
			continue
		}
		entry := kept[idx]
		if emitted > 0 && entry.Group != prevGroup {
			b.WriteByte('\n')
		}
		b.WriteString(entry.Text)
		b.WriteByte('\n')
		prevGroup = entry.Group
		emitted++
	}
	return b.String()
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

func LSPParseFunctionType(typeText string) LSPFunctionTypeParts {
	rest := strings.TrimSpace(typeText)
	if !strings.HasPrefix(rest, "fn") {
		return LSPFunctionTypeParts{}
	}
	rest = strings.TrimSpace(strings.TrimPrefix(rest, "fn"))
	if !strings.HasPrefix(rest, "(") {
		return LSPFunctionTypeParts{}
	}
	closeIdx := matchingCloseParen(rest)
	if closeIdx < 0 {
		return LSPFunctionTypeParts{}
	}
	paramsText := strings.TrimSpace(rest[1:closeIdx])
	var params []string
	if paramsText != "" {
		params = splitTopLevelComma(paramsText)
	}
	returnType := ""
	tail := strings.TrimSpace(rest[closeIdx+1:])
	if strings.HasPrefix(tail, "->") {
		returnType = strings.TrimSpace(strings.TrimPrefix(tail, "->"))
	}
	return LSPFunctionTypeParts{
		OK:             true,
		ParameterTypes: params,
		ReturnType:     returnType,
	}
}

func LSPFallbackParameterNames(count int) []string {
	names := make([]string, count)
	for i := range names {
		names[i] = "arg" + strconv.Itoa(i+1)
	}
	return names
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

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func minInt(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		return c
	}
	return a
}

func matchingCloseParen(s string) int {
	depth := 0
	for i, r := range s {
		switch r {
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

func splitTopLevelComma(s string) []string {
	var out []string
	start := 0
	depth := 0
	for i, r := range s {
		switch r {
		case '(', '[', '<':
			depth++
		case ')', ']', '>':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				if part := strings.TrimSpace(s[start:i]); part != "" {
					out = append(out, part)
				}
				start = i + len(string(r))
			}
		}
	}
	if part := strings.TrimSpace(s[start:]); part != "" {
		out = append(out, part)
	}
	return out
}

func lspMethodIsHandled(method string) bool {
	switch method {
	case "textDocument/didOpen",
		"textDocument/didChange",
		"textDocument/didClose",
		"textDocument/hover",
		"textDocument/definition",
		"textDocument/formatting",
		"textDocument/documentSymbol",
		"textDocument/completion",
		"textDocument/references",
		"textDocument/rename",
		"textDocument/signatureHelp",
		"workspace/symbol",
		"textDocument/inlayHint",
		"textDocument/semanticTokens/full",
		"textDocument/codeAction":
		return true
	default:
		return false
	}
}
