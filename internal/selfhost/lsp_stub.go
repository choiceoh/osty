package selfhost

import "unicode/utf8"

// LSPSemanticToken is the generator-build mirror of the real adapter type.
// It keeps packages that import the LSP bridge buildable while generated.go is
// being rebuilt from lsp.osty.
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
	switch kind {
	case "IDENT":
		return lspSemanticTypeForSymbolKindStub(symbolKind), true
	case "INT", "FLOAT", "CHAR", "BYTE":
		return 8, true
	case "STRING", "RAWSTRING":
		return 7, true
	}
	if lspIsKeywordKindStub(kind) {
		return 6, true
	}
	if lspIsOperatorKindStub(kind) {
		return 9, true
	}
	return 0, false
}

func LSPSemanticTypeForComment() uint32 { return 10 }

func LSPCompletionKindForSymbolKind(kind string) uint32 {
	switch kind {
	case "function":
		return 3
	case "binding", "parameter":
		return 6
	case "struct", "type alias":
		return 22
	case "enum":
		return 13
	case "interface":
		return 8
	case "variant":
		return 20
	case "type parameter":
		return 25
	case "package":
		return 9
	case "builtin":
		return 14
	default:
		return 12
	}
}

func LSPCompletionSortTextForSymbolKind(kind, label string) string {
	switch kind {
	case "package":
		return "0_" + label
	case "binding", "parameter":
		return "1_" + label
	case "function", "variant":
		return "2_" + label
	default:
		return "3_" + label
	}
}

func LSPCompletionDetail(kind, label, typeText string) string {
	if typeText == "" {
		return ""
	}
	if kind == "function" {
		return "fn " + label + fnTypeTailStub(typeText)
	}
	return typeText
}

func LSPHoverSignatureLine(kind, name, typeText string) string {
	switch kind {
	case "function":
		if typeText != "" {
			return "fn " + name + fnTypeTailStub(typeText)
		}
		return "fn " + name
	case "binding":
		if typeText != "" {
			return "let " + name + ": " + typeText
		}
		return "let " + name
	case "parameter":
		if typeText != "" {
			return "(parameter) " + name + ": " + typeText
		}
		return "(parameter) " + name
	case "struct":
		return "struct " + name
	case "enum":
		return "enum " + name
	case "interface":
		return "interface " + name
	case "type alias":
		return "type " + name
	case "variant":
		return "variant " + name
	case "type parameter":
		return "type parameter " + name
	case "builtin":
		return "builtin " + name
	case "package":
		return "use " + name
	default:
		return name + " (" + kind + ")"
	}
}

func LSPPathToURI(path string) string {
	if path == "" {
		return "file://"
	}
	if hasPrefix(path, "/") {
		return "file://" + path
	}
	return "file:///" + path
}

func LSPLineStarts(src []byte) []int {
	lines := []int{0}
	for i := 0; i < len(src); i++ {
		switch src[i] {
		case '\n':
			lines = append(lines, i+1)
		case '\r':
			if i+1 < len(src) && src[i+1] == '\n' {
				i++
			}
			lines = append(lines, i+1)
		}
	}
	return lines
}

func LSPUTF16UnitsInPrefix(src []byte) uint32 {
	return utf16UnitsStub(src)
}

func LSPOstyPositionToLSP(src []byte, lineStarts []int, line, offset int) LSPPosition {
	if line <= 0 || len(lineStarts) == 0 {
		return LSPPosition{}
	}
	if line > len(lineStarts) {
		line = len(lineStarts)
	}
	start := lineStarts[line-1]
	end := offset
	if end < start {
		end = start
	}
	if end > len(src) {
		end = len(src)
	}
	return LSPPosition{Line: uint32(line - 1), Character: utf16UnitsStub(src[start:end])}
}

func LSPLSPPositionToOsty(src []byte, lineStarts []int, lspLine, character uint32) LSPOstyPosition {
	line := int(lspLine) + 1
	if len(lineStarts) == 0 {
		return LSPOstyPosition{Line: 1, Column: 1}
	}
	if line > len(lineStarts) {
		line = len(lineStarts)
	}
	if line < 1 {
		line = 1
	}
	start := lineStarts[line-1]
	want := int(character)
	col := 1
	off := start
	units := 0
	for off < len(src) {
		b := src[off]
		if b == '\n' || b == '\r' {
			break
		}
		r, sz := utf8.DecodeRune(src[off:])
		ru := 1
		if r >= 0x10000 {
			ru = 2
		}
		if units+ru > want {
			break
		}
		units += ru
		off += sz
		col++
	}
	return LSPOstyPosition{Offset: off, Line: line, Column: col}
}

func LSPOffsetToPosition(src []byte, lineStarts []int, off int) LSPPosition {
	if len(lineStarts) == 0 {
		return LSPPosition{}
	}
	if off < 0 {
		off = 0
	}
	if off > len(src) {
		off = len(src)
	}
	lo, hi := 0, len(lineStarts)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if lineStarts[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	start := lineStarts[lo]
	return LSPPosition{Line: uint32(lo), Character: utf16UnitsStub(src[start:off])}
}

func LSPRangeFromOffsets(src []byte, lineStarts []int, start, end int) LSPRange {
	return LSPRange{Start: LSPOffsetToPosition(src, lineStarts, start), End: LSPOffsetToPosition(src, lineStarts, end)}
}

func LSPRangeFromOstySpan(src []byte, lineStarts []int, startLine, startOffset, endLine, endOffset int) LSPRange {
	start := LSPOstyPositionToLSP(src, lineStarts, startLine, startOffset)
	if endLine == 0 {
		endLine = startLine
		endOffset = startOffset
	}
	end := LSPOstyPositionToLSP(src, lineStarts, endLine, endOffset)
	if end.Line == start.Line && end.Character <= start.Character {
		end.Character = start.Character + 1
	}
	return LSPRange{Start: start, End: end}
}

func LSPSymbolKindForDecl(kind string, mutable bool) uint32 {
	switch kind {
	case "fn":
		return 12
	case "struct":
		return 23
	case "enum":
		return 10
	case "interface":
		return 11
	case "typeAlias":
		return 5
	case "let":
		if mutable {
			return 13
		}
		return 14
	default:
		return 13
	}
}

func LSPSymbolKindForMember(kind string) uint32 {
	switch kind {
	case "field":
		return 8
	case "variant":
		return 22
	case "method":
		return 6
	default:
		return 13
	}
}

func LSPWantsCodeActionKind(only []string, kind string) bool {
	if len(only) == 0 {
		return true
	}
	for _, requested := range only {
		if requested == kind || hasPrefix(kind, requested+".") {
			return true
		}
	}
	return false
}

func LSPPrefixUnderscoreName(name string) string {
	if name == "" {
		return "_"
	}
	return "_" + name
}

func LSPPrefixUnderscoreTitle(name string) string {
	return "Prefix `" + name + "` with `_` to silence"
}

func LSPFindNameOffset(src []byte, declStart, declEnd int, name string) int {
	if name == "" || declStart < 0 || declEnd > len(src) || declEnd <= declStart {
		return -1
	}
	for off := declStart; off < declEnd; {
		if !isIdentStart(src[off]) {
			off++
			continue
		}
		start := off
		off++
		for off < declEnd && isIdentCont(src[off]) {
			off++
		}
		if string(src[start:off]) == name {
			return start
		}
	}
	return -1
}

func LSPPrecedingCompletionContext(src []byte, offset int) LSPCompletionContext {
	if offset > len(src) {
		offset = len(src)
	}
	if offset < 0 {
		offset = 0
	}
	start := offset
	for start > 0 && isIdentCont(src[start-1]) {
		start--
	}
	ctx := LSPCompletionContext{Prefix: string(src[start:offset])}
	if start > 0 && src[start-1] == '.' {
		recvEnd := start - 1
		recvStart := recvEnd
		for recvStart > 0 && isIdentCont(src[recvStart-1]) {
			recvStart--
		}
		ctx.AfterDot = string(src[recvStart:recvEnd])
	}
	return ctx
}

func LSPIdentifierAt(src []byte, offset int) string {
	if offset < 0 || offset >= len(src) || !isIdentCont(src[offset]) {
		return ""
	}
	end := offset
	for end < len(src) && isIdentCont(src[end]) {
		end++
	}
	return string(src[offset:end])
}

func LSPContainsPosition(startLine, startColumn, endLine, endColumn, posLine, posColumn int) bool {
	if posLine < startLine || posLine > endLine {
		return false
	}
	if posLine == startLine && posColumn < startColumn {
		return false
	}
	if posLine == endLine && posColumn > endColumn {
		return false
	}
	return true
}

func LSPSpanOverlaps(startOffset, endOffset, queryStart, queryEnd int) bool {
	return !(endOffset < queryStart || startOffset > queryEnd)
}

func LSPDiagnosticPayloadFor(severity, message, hint string, notes []string) LSPDiagnosticPayload {
	code := uint32(1)
	switch severity {
	case "warning":
		code = 2
	case "note":
		code = 3
	}
	out := message
	if hint != "" {
		out += "\nhelp: " + hint
	}
	for _, note := range notes {
		out += "\nnote: " + note
	}
	return LSPDiagnosticPayload{Severity: code, Message: out}
}

func SortDedupLSPLocations(locs []LSPLocation) []LSPLocation {
	if len(locs) <= 1 {
		return locs
	}
	tagged := make([]struct {
		loc LSPLocation
		idx int
	}, 0, len(locs))
	for i, loc := range locs {
		tagged = append(tagged, struct {
			loc LSPLocation
			idx int
		}{loc: loc, idx: i})
	}
	for i := 1; i < len(tagged); i++ {
		cur := tagged[i]
		j := i - 1
		for j >= 0 && indexedLocationLess(cur.loc, cur.idx, tagged[j].loc, tagged[j].idx) {
			tagged[j+1] = tagged[j]
			j--
		}
		tagged[j+1] = cur
	}
	out := make([]LSPLocation, 0, len(tagged))
	for _, item := range tagged {
		if len(out) > 0 && sameLocation(item.loc, out[len(out)-1]) {
			continue
		}
		out = append(out, item.loc)
	}
	return out
}

func SortLSPSymbolIndexes(keys []LSPSymbolSortKey) []int {
	tagged := make([]struct {
		key LSPSymbolSortKey
		idx int
	}, 0, len(keys))
	for i, key := range keys {
		tagged = append(tagged, struct {
			key LSPSymbolSortKey
			idx int
		}{key: key, idx: i})
	}
	for i := 1; i < len(tagged); i++ {
		cur := tagged[i]
		j := i - 1
		for j >= 0 && indexedSymbolLess(cur.key, cur.idx, tagged[j].key, tagged[j].idx) {
			tagged[j+1] = tagged[j]
			j--
		}
		tagged[j+1] = cur
	}
	out := make([]int, 0, len(tagged))
	for _, item := range tagged {
		out = append(out, item.idx)
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
	tagged := make([]struct {
		key LSPImportSortKey
		idx int
	}, 0, len(keys))
	for i, key := range keys {
		tagged = append(tagged, struct {
			key LSPImportSortKey
			idx int
		}{key: key, idx: i})
	}
	for i := 1; i < len(tagged); i++ {
		cur := tagged[i]
		j := i - 1
		for j >= 0 && indexedImportLess(cur.key, cur.idx, tagged[j].key, tagged[j].idx) {
			tagged[j+1] = tagged[j]
			j--
		}
		tagged[j+1] = cur
	}
	out := make([]int, 0, len(tagged))
	for _, item := range tagged {
		out = append(out, item.idx)
	}
	return out
}

func LSPUseGroup(isGoFFI bool, path []string) int {
	if isGoFFI {
		return 2
	}
	if len(path) > 0 && path[0] == "std" {
		return 0
	}
	return 1
}

func LSPUseKey(isGoFFI bool, goPath, rawPath string, path []string) string {
	if isGoFFI {
		return goPath
	}
	if rawPath != "" {
		return rawPath
	}
	out := ""
	for i, part := range path {
		if i > 0 {
			out += "."
		}
		out += part
	}
	return out
}

func LSPKeyWithAlias(group int, key, alias string) string {
	return string(rune('0'+group)) + "|" + key + "|" + alias
}

func LSPUseSourceText(src []byte, start, end int) string {
	if start < 0 || end > len(src) || start >= end {
		return ""
	}
	for end > start {
		switch src[end-1] {
		case ' ', '\t', '\r', '\n':
			end--
		default:
			return string(src[start:end])
		}
	}
	return string(src[start:end])
}

func LSPEndOfLineOffset(src []byte, off int) int {
	if off < 0 {
		off = 0
	}
	if off > len(src) {
		off = len(src)
	}
	for off < len(src) && (src[off] == ' ' || src[off] == '\t') {
		off++
	}
	if off < len(src) && src[off] == '\r' {
		off++
	}
	if off < len(src) && src[off] == '\n' {
		off++
	}
	return off
}

func LSPHasTriviaBetweenOffsets(src []byte, start, end int) bool {
	if start < 0 || end > len(src) || start >= end {
		return false
	}
	for off := start; off < end; off++ {
		switch src[off] {
		case ' ', '\t', '\n', '\r':
		default:
			return true
		}
	}
	return false
}

func LSPActiveParameter(argEndOffsets []int, cursorOffset int) uint32 {
	var active uint32
	for _, endOffset := range argEndOffsets {
		if cursorOffset <= endOffset {
			return active
		}
		active++
	}
	return active
}

func LSPBuildSignatureText(name string, params []LSPSignatureParam, returnType string) LSPSignatureText {
	label := "fn " + name + "("
	labels := make([]string, 0, len(params))
	for i, param := range params {
		if i > 0 {
			label += ", "
		}
		paramLabel := param.Name + ": " + param.TypeName
		label += paramLabel
		labels = append(labels, paramLabel)
	}
	label += ")"
	if returnType != "" {
		label += " -> " + returnType
	}
	return LSPSignatureText{Label: label, ParameterLabels: labels}
}

func EncodeLSPSemanticTokens(tokens []LSPSemanticToken) []uint32 {
	if len(tokens) > 1 {
		tokens = append([]LSPSemanticToken(nil), tokens...)
		for i := 1; i < len(tokens); i++ {
			cur := tokens[i]
			j := i - 1
			for j >= 0 && semanticTokenLess(cur, tokens[j]) {
				tokens[j+1] = tokens[j]
				j--
			}
			tokens[j+1] = cur
		}
	}
	data := make([]uint32, 0, 5*len(tokens))
	var prevLine, prevCol uint32
	for i, token := range tokens {
		deltaLine := token.Line - prevLine
		deltaCol := token.Column
		if i != 0 && deltaLine == 0 {
			deltaCol = token.Column - prevCol
		}
		data = append(data, deltaLine, deltaCol, token.Length, token.TokenType, token.Modifiers)
		prevLine = token.Line
		prevCol = token.Column
	}
	return data
}

func ResolveOverlappingLSPTextEdits(edits []LSPTextEdit) []LSPTextEdit {
	if len(edits) <= 1 {
		return edits
	}
	tagged := make([]struct {
		edit LSPTextEdit
		idx  int
	}, 0, len(edits))
	for i, edit := range edits {
		tagged = append(tagged, struct {
			edit LSPTextEdit
			idx  int
		}{edit: edit, idx: i})
	}
	for i := 1; i < len(tagged); i++ {
		cur := tagged[i]
		j := i - 1
		for j >= 0 && indexedTextEditLess(cur.edit, cur.idx, tagged[j].edit, tagged[j].idx) {
			tagged[j+1] = tagged[j]
			j--
		}
		tagged[j+1] = cur
	}
	out := make([]LSPTextEdit, 0, len(tagged))
	var lastStartLine, lastStartChar, lastEndLine, lastEndChar uint32
	have := false
	for _, item := range tagged {
		edit := item.edit
		if have {
			if posBefore(edit.StartLine, edit.StartCharacter, lastEndLine, lastEndChar) {
				continue
			}
			if posEqual(edit.StartLine, edit.StartCharacter, lastEndLine, lastEndChar) &&
				posEqual(edit.StartLine, edit.StartCharacter, edit.EndLine, edit.EndCharacter) &&
				posEqual(lastStartLine, lastStartChar, lastEndLine, lastEndChar) {
				continue
			}
		}
		out = append(out, edit)
		lastStartLine, lastStartChar = edit.StartLine, edit.StartCharacter
		lastEndLine, lastEndChar = edit.EndLine, edit.EndCharacter
		have = true
	}
	return out
}

func lspSemanticTypeForSymbolKindStub(kind string) uint32 {
	switch kind {
	case "function":
		return 5
	case "struct", "enum", "interface", "type alias", "builtin", "type parameter":
		return 1
	case "parameter":
		return 2
	case "variant":
		return 11
	case "package":
		return 0
	default:
		return 3
	}
}

func lspIsKeywordKindStub(kind string) bool {
	switch kind {
	case "fn", "struct", "enum", "interface", "type", "let", "mut", "pub",
		"if", "else", "match", "for", "break", "continue", "return", "use", "defer":
		return true
	default:
		return false
	}
}

func lspIsOperatorKindStub(kind string) bool {
	switch kind {
	case "+", "-", "*", "/", "%", "==", "!=", "<", ">", "<=", ">=", "&&", "||", "!",
		"&", "|", "^", "~", "<<", ">>", "=", "+=", "-=", "*=", "/=", "%=", "&=", "|=",
		"^=", "<<=", ">>=", "?", "?.", "??", "..", "..=", "->", "<-":
		return true
	default:
		return false
	}
}

func hasPrefix(text, prefix string) bool {
	if len(prefix) > len(text) {
		return false
	}
	return text[:len(prefix)] == prefix
}

func fnTypeTailStub(typeText string) string {
	if !hasPrefix(typeText, "fn") {
		return ""
	}
	return typeText[len("fn"):]
}

func utf16UnitsStub(src []byte) uint32 {
	var n uint32
	for len(src) > 0 {
		r, sz := utf8.DecodeRune(src)
		if r == utf8.RuneError && sz == 1 {
			n++
			src = src[1:]
			continue
		}
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
		src = src[sz:]
	}
	return n
}

func indexedTextEditLess(a LSPTextEdit, ai int, b LSPTextEdit, bi int) bool {
	if a.StartLine != b.StartLine {
		return a.StartLine < b.StartLine
	}
	if a.StartCharacter != b.StartCharacter {
		return a.StartCharacter < b.StartCharacter
	}
	return ai < bi
}

func indexedLocationLess(a LSPLocation, ai int, b LSPLocation, bi int) bool {
	if a.URI != b.URI {
		return a.URI < b.URI
	}
	if a.StartLine != b.StartLine {
		return a.StartLine < b.StartLine
	}
	if a.StartCharacter != b.StartCharacter {
		return a.StartCharacter < b.StartCharacter
	}
	return ai < bi
}

func indexedSymbolLess(a LSPSymbolSortKey, ai int, b LSPSymbolSortKey, bi int) bool {
	if a.Name != b.Name {
		return a.Name < b.Name
	}
	if a.URI != b.URI {
		return a.URI < b.URI
	}
	return ai < bi
}

func indexedImportLess(a LSPImportSortKey, ai int, b LSPImportSortKey, bi int) bool {
	if a.Group != b.Group {
		return a.Group < b.Group
	}
	if a.Key != b.Key {
		return a.Key < b.Key
	}
	if a.Alias != b.Alias {
		return a.Alias < b.Alias
	}
	return ai < bi
}

func semanticTokenLess(a, b LSPSemanticToken) bool {
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Column < b.Column
}

func sameLocation(a, b LSPLocation) bool {
	return a.URI == b.URI &&
		a.StartLine == b.StartLine &&
		a.StartCharacter == b.StartCharacter &&
		a.EndLine == b.EndLine &&
		a.EndCharacter == b.EndCharacter
}

func posBefore(line, char, otherLine, otherChar uint32) bool {
	if line != otherLine {
		return line < otherLine
	}
	return char < otherChar
}

func posEqual(line, char, otherLine, otherChar uint32) bool {
	return line == otherLine && char == otherChar
}

func isIdentStart(b byte) bool {
	return b == '_' || b >= 0x80 || ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}

func isIdentCont(b byte) bool {
	return isIdentStart(b) || ('0' <= b && b <= '9')
}
