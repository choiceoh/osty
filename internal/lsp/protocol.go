// Package lsp implements a Language Server Protocol server for Osty.
//
// The server speaks LSP 3.17 over stdio with JSON-RPC 2.0 framing
// (Content-Length headers + JSON body). It reuses the compiler
// front-end (lexer, parser, resolver, checker) to compute diagnostics,
// hover info, go-to-definition targets, formatting, and document
// symbols.
//
// The public entry point is Server.Run, which drives a blocking
// read/dispatch/write loop until the client sends `exit` (or stdin
// hits EOF). Callers typically wire it into a `osty lsp` subcommand.
//
// This file defines the LSP wire types used by the server. Only the
// subset actually referenced by handlers is modelled — unused fields
// are elided to keep the schema small and auditable.
package lsp

import "encoding/json"

// ---- JSON-RPC envelope ----

// rpcRequest is an incoming JSON-RPC message. When ID is empty (absent
// from the wire) the message is a notification and must not be replied
// to; otherwise it is a request that expects a matching Response.
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification reports whether the message lacks an id (§LSP 3.17
// "Notification Message"). Notifications must never be answered.
func (r *rpcRequest) isNotification() bool {
	return len(r.ID) == 0 || string(r.ID) == "null"
}

// rpcResponse is the outgoing reply to a request. Exactly one of
// Result and Error is populated; both use json.RawMessage so the
// handler can marshal its concrete result first and we just splice
// the bytes in.
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcNotification is an outgoing notification (no reply expected).
type rpcNotification struct {
	JSONRPC string          `json:"jsonrpc"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// rpcError carries a structured error payload.
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// JSON-RPC / LSP reserved error codes. Only the ones the server can
// actually emit are listed.
const (
	errParseError     = -32700
	errInvalidRequest = -32600
	errMethodNotFound = -32601
	errInvalidParams  = -32602
	errInternalError  = -32603
	// LSP-specific.
	errServerNotInitialized = -32002
)

// ---- Core LSP types ----

// Position is a zero-based (line, character) location. Character is
// counted in UTF-16 code units by default — the server negotiates
// this with the client in Initialize and falls back to UTF-16 when
// the client doesn't send a PositionEncodingKind capability.
type Position struct {
	Line      uint32 `json:"line"`
	Character uint32 `json:"character"`
}

// Range is a half-open interval [Start, End) expressed in Positions.
type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

// Location pins a Range to a specific document URI.
type Location struct {
	URI   string `json:"uri"`
	Range Range  `json:"range"`
}

// TextDocumentIdentifier references a document by URI.
type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

// VersionedTextDocumentIdentifier is like TextDocumentIdentifier but
// carries the version the client expects us to see.
type VersionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int32  `json:"version"`
}

// TextDocumentItem is the full state sent on `textDocument/didOpen`.
type TextDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int32  `json:"version"`
	Text       string `json:"text"`
}

// ---- Initialize ----

// InitializeParams is the payload of the first request the client
// sends. We only pay attention to capabilities related to position
// encoding; everything else is ignored.
type InitializeParams struct {
	ProcessID    *int               `json:"processId"`
	RootURI      string             `json:"rootUri,omitempty"`
	Capabilities ClientCapabilities `json:"capabilities"`
	Trace        string             `json:"trace,omitempty"`
}

// ClientCapabilities models the subset we read.
type ClientCapabilities struct {
	General *GeneralClientCapabilities `json:"general,omitempty"`
}

// GeneralClientCapabilities carries the position-encoding list.
type GeneralClientCapabilities struct {
	PositionEncodings []string `json:"positionEncodings,omitempty"`
}

// InitializeResult is our response to Initialize.
type InitializeResult struct {
	Capabilities     ServerCapabilities `json:"capabilities"`
	ServerInfo       *ServerInfo        `json:"serverInfo,omitempty"`
	PositionEncoding string             `json:"positionEncoding,omitempty"`
}

// ServerInfo is a friendly name/version pair.
type ServerInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// ServerCapabilities advertises which methods the server handles.
type ServerCapabilities struct {
	TextDocumentSync           *TextDocumentSyncOptions `json:"textDocumentSync,omitempty"`
	HoverProvider              bool                     `json:"hoverProvider,omitempty"`
	DefinitionProvider         bool                     `json:"definitionProvider,omitempty"`
	DocumentFormattingProvider bool                     `json:"documentFormattingProvider,omitempty"`
	DocumentSymbolProvider     bool                     `json:"documentSymbolProvider,omitempty"`
	CompletionProvider         *CompletionOptions       `json:"completionProvider,omitempty"`
	ReferencesProvider         bool                     `json:"referencesProvider,omitempty"`
	RenameProvider             bool                     `json:"renameProvider,omitempty"`
	SignatureHelpProvider      *SignatureHelpOptions    `json:"signatureHelpProvider,omitempty"`
	WorkspaceSymbolProvider    bool                     `json:"workspaceSymbolProvider,omitempty"`
	InlayHintProvider          bool                     `json:"inlayHintProvider,omitempty"`
	SemanticTokensProvider     *SemanticTokensOptions   `json:"semanticTokensProvider,omitempty"`
	CodeActionProvider         *CodeActionOptions       `json:"codeActionProvider,omitempty"`
	PositionEncoding           string                   `json:"positionEncoding,omitempty"`
}

// SignatureHelpOptions tells the client which characters should
// proactively re-request signature help. `(` is the obvious one;
// `,` keeps the popup current as the user moves between arguments.
type SignatureHelpOptions struct {
	TriggerCharacters   []string `json:"triggerCharacters,omitempty"`
	RetriggerCharacters []string `json:"retriggerCharacters,omitempty"`
}

// CompletionOptions advertises which trigger characters (beyond the
// default `a..z`, `A..Z`, `_`) kick off completion. Osty surfaces
// member access via `.` so the client proactively asks for suggestions
// as soon as the user types a dot after a package alias or variable.
type CompletionOptions struct {
	TriggerCharacters []string `json:"triggerCharacters,omitempty"`
	ResolveProvider   bool     `json:"resolveProvider,omitempty"`
}

// TextDocumentSyncKind mirrors the LSP enum (0=None, 1=Full, 2=Incremental).
type TextDocumentSyncKind int

const (
	SyncNone        TextDocumentSyncKind = 0
	SyncFull        TextDocumentSyncKind = 1
	SyncIncremental TextDocumentSyncKind = 2
)

// TextDocumentSyncOptions advertises whether we want open/close
// notifications and which granularity of change events.
type TextDocumentSyncOptions struct {
	OpenClose bool                 `json:"openClose"`
	Change    TextDocumentSyncKind `json:"change"`
}

// ---- Text synchronization ----

// DidOpenTextDocumentParams arrives when the client opens a file.
type DidOpenTextDocumentParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

// DidChangeTextDocumentParams arrives on every edit. With SyncFull the
// ContentChanges slice has exactly one entry whose Text is the full
// new document.
type DidChangeTextDocumentParams struct {
	TextDocument   VersionedTextDocumentIdentifier  `json:"textDocument"`
	ContentChanges []TextDocumentContentChangeEvent `json:"contentChanges"`
}

// TextDocumentContentChangeEvent is either a ranged edit (when
// Range != nil) or a full-document replacement (when Range == nil).
// Full sync always uses the latter form.
type TextDocumentContentChangeEvent struct {
	Range *Range `json:"range,omitempty"`
	Text  string `json:"text"`
}

// DidCloseTextDocumentParams arrives when the client closes a file.
type DidCloseTextDocumentParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// ---- Diagnostics ----

// DiagnosticSeverity mirrors the LSP enum.
type DiagnosticSeverity int

const (
	SevError       DiagnosticSeverity = 1
	SevWarning     DiagnosticSeverity = 2
	SevInformation DiagnosticSeverity = 3
	SevHint        DiagnosticSeverity = 4
)

// LSPDiagnostic is the wire form of a diagnostic. Related information,
// code description and tags are omitted — the Osty compiler doesn't
// emit those yet.
type LSPDiagnostic struct {
	Range    Range              `json:"range"`
	Severity DiagnosticSeverity `json:"severity,omitempty"`
	Code     string             `json:"code,omitempty"`
	Source   string             `json:"source,omitempty"`
	Message  string             `json:"message"`
}

// PublishDiagnosticsParams is pushed to the client whenever we finish
// analyzing a document. Clearing the set (empty Diagnostics slice) is
// how we tell the client "this file has no problems anymore".
type PublishDiagnosticsParams struct {
	URI         string          `json:"uri"`
	Version     *int32          `json:"version,omitempty"`
	Diagnostics []LSPDiagnostic `json:"diagnostics"`
}

// ---- Hover / Definition ----

// TextDocumentPositionParams is the base shape used by hover,
// definition, and a handful of other point-style requests.
type TextDocumentPositionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// HoverParams is currently a simple alias of TextDocumentPositionParams.
type HoverParams = TextDocumentPositionParams

// DefinitionParams likewise — we don't yet read any cancel/progress
// tokens.
type DefinitionParams = TextDocumentPositionParams

// MarkupKind values for MarkupContent.Kind.
const (
	MarkupKindMarkdown  = "markdown"
	MarkupKindPlaintext = "plaintext"
)

// MarkupContent carries the hover body.
type MarkupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// Hover is the reply to `textDocument/hover`. Range is the span to
// highlight in the editor; nil means "the word under the cursor".
type Hover struct {
	Contents MarkupContent `json:"contents"`
	Range    *Range        `json:"range,omitempty"`
}

// ---- Formatting ----

// DocumentFormattingParams is the request payload for full-file
// formatting. FormattingOptions is ignored — `osty fmt` has no knobs.
type DocumentFormattingParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Options      FormattingOptions      `json:"options"`
}

// FormattingOptions is included for completeness.
type FormattingOptions struct {
	TabSize      uint32 `json:"tabSize"`
	InsertSpaces bool   `json:"insertSpaces"`
}

// TextEdit is one replacement in the formatting response. Formatting
// returns a single edit that rewrites the whole document.
type TextEdit struct {
	Range   Range  `json:"range"`
	NewText string `json:"newText"`
}

// ---- Document symbols ----

// DocumentSymbolParams is the request payload for the outline view.
type DocumentSymbolParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// SymbolKind mirrors the LSP enum. We only list the values that map
// cleanly to Osty declarations and that the outline handler actually
// emits.
type SymbolKind int

const (
	SymKindFunction   SymbolKind = 12
	SymKindVariable   SymbolKind = 13
	SymKindConstant   SymbolKind = 14
	SymKindClass      SymbolKind = 5
	SymKindInterface  SymbolKind = 11
	SymKindEnum       SymbolKind = 10
	SymKindEnumMember SymbolKind = 22
	SymKindStruct     SymbolKind = 23
	SymKindField      SymbolKind = 8
	SymKindMethod     SymbolKind = 6
)

// DocumentSymbol is one entry in the outline. `Range` encloses the
// whole declaration (including body); `SelectionRange` targets just
// the name — editors highlight the latter when the user clicks.
type DocumentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail,omitempty"`
	Kind           SymbolKind       `json:"kind"`
	Range          Range            `json:"range"`
	SelectionRange Range            `json:"selectionRange"`
	Children       []DocumentSymbol `json:"children,omitempty"`
}

// ---- Completion ----

// CompletionParams is the request body for `textDocument/completion`.
// The Context field (optional) tells us whether the client invoked
// completion manually or the IDE triggered it via a character.
type CompletionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      *CompletionContext     `json:"context,omitempty"`
}

// CompletionContext describes how completion was triggered.
type CompletionContext struct {
	TriggerKind      CompletionTriggerKind `json:"triggerKind"`
	TriggerCharacter string                `json:"triggerCharacter,omitempty"`
}

// CompletionTriggerKind mirrors the LSP enum.
type CompletionTriggerKind int

const (
	TriggerInvoked         CompletionTriggerKind = 1
	TriggerCharacter       CompletionTriggerKind = 2
	TriggerForIncompletion CompletionTriggerKind = 3
)

// CompletionItemKind mirrors the LSP enum — the subset we actually
// emit. Editor-specific icons hang off this number.
type CompletionItemKind int

const (
	CompletionItemFunction CompletionItemKind = 3
	CompletionItemVariable CompletionItemKind = 6
	CompletionItemField    CompletionItemKind = 5
	CompletionItemConstructor CompletionItemKind = 4
	CompletionItemModule   CompletionItemKind = 9
	CompletionItemStruct   CompletionItemKind = 22
	CompletionItemEnum     CompletionItemKind = 13
	CompletionItemInterface CompletionItemKind = 8
	CompletionItemEnumMember CompletionItemKind = 20
	CompletionItemTypeParameter CompletionItemKind = 25
	CompletionItemKeyword  CompletionItemKind = 14
	CompletionItemValue    CompletionItemKind = 12
)

// CompletionItem is one suggestion. `Detail` shows a compact
// type/signature inline; `Documentation` is the multi-line help card.
type CompletionItem struct {
	Label         string             `json:"label"`
	Kind          CompletionItemKind `json:"kind,omitempty"`
	Detail        string             `json:"detail,omitempty"`
	Documentation *MarkupContent     `json:"documentation,omitempty"`
	InsertText    string             `json:"insertText,omitempty"`
	SortText      string             `json:"sortText,omitempty"`
	FilterText    string             `json:"filterText,omitempty"`
}

// CompletionList is the response body. IsIncomplete = false tells the
// client "this is the full list, don't re-request on every keystroke."
type CompletionList struct {
	IsIncomplete bool             `json:"isIncomplete"`
	Items        []CompletionItem `json:"items"`
}

// ---- References / Rename ----

// ReferenceParams is the payload of `textDocument/references`.
type ReferenceParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	Context      ReferenceContext       `json:"context"`
}

// ReferenceContext asks whether the declaration should be included in
// the reference list along with the usages.
type ReferenceContext struct {
	IncludeDeclaration bool `json:"includeDeclaration"`
}

// RenameParams is the payload of `textDocument/rename`.
type RenameParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
	NewName      string                 `json:"newName"`
}

// WorkspaceEdit is the reply to rename (and other refactors). A flat
// map URI → edits captures all source changes the client should apply.
type WorkspaceEdit struct {
	Changes map[string][]TextEdit `json:"changes,omitempty"`
}

// ---- Signature help ----

// SignatureHelpParams is the payload of `textDocument/signatureHelp`.
type SignatureHelpParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Position     Position               `json:"position"`
}

// SignatureHelp is the reply. We always return one signature today
// because Osty has no overloading; the shape matches the spec so
// clients that expect a list still display correctly.
type SignatureHelp struct {
	Signatures      []SignatureInformation `json:"signatures"`
	ActiveSignature uint32                 `json:"activeSignature"`
	ActiveParameter uint32                 `json:"activeParameter"`
}

// SignatureInformation is one `fn(a: Int, b: String) -> Bool` line
// plus its per-parameter sub-ranges.
type SignatureInformation struct {
	Label         string                 `json:"label"`
	Documentation *MarkupContent         `json:"documentation,omitempty"`
	Parameters    []ParameterInformation `json:"parameters,omitempty"`
}

// ParameterInformation pins one argument in the signature label. The
// Label field may be a [start, end) byte-offset pair inside the
// signature, or the literal substring — we use the substring form.
type ParameterInformation struct {
	Label         string         `json:"label"`
	Documentation *MarkupContent `json:"documentation,omitempty"`
}

// ---- Workspace symbol ----

// WorkspaceSymbolParams carries the free-text query. Clients send "" on
// the first keystroke, then refine as the user types.
type WorkspaceSymbolParams struct {
	Query string `json:"query"`
}

// SymbolInformation is the legacy shape of the workspace-symbol reply.
// Kept because VS Code still accepts it; the newer WorkspaceSymbol is
// a future refinement.
type SymbolInformation struct {
	Name          string     `json:"name"`
	Kind          SymbolKind `json:"kind"`
	Location      Location   `json:"location"`
	ContainerName string     `json:"containerName,omitempty"`
}

// ---- Inlay hints ----

// InlayHintParams restricts hint production to a visible range so the
// server doesn't waste cycles computing hints for folded/unseen code.
type InlayHintParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
}

// InlayHintKind mirrors the LSP enum.
type InlayHintKind int

const (
	InlayHintKindType      InlayHintKind = 1
	InlayHintKindParameter InlayHintKind = 2
)

// InlayHint is the in-source ghost text. PaddingLeft/Right nudge
// whitespace around the label so it doesn't fuse with adjacent code.
type InlayHint struct {
	Position     Position      `json:"position"`
	Label        string        `json:"label"`
	Kind         InlayHintKind `json:"kind,omitempty"`
	PaddingLeft  bool          `json:"paddingLeft,omitempty"`
	PaddingRight bool          `json:"paddingRight,omitempty"`
}

// ---- Semantic tokens ----

// SemanticTokensParams is the request for a full-file token pass.
type SemanticTokensParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}

// SemanticTokens is the reply. Data is the standard LSP five-tuple-
// per-token encoding: deltaLine, deltaStart, length, tokenType,
// tokenModifiers — all relative to the previous token.
type SemanticTokens struct {
	Data []uint32 `json:"data"`
}

// SemanticTokensLegend tells the client what each integer in `data`
// means. The client maps `tokenTypes[i]` to a theme color.
type SemanticTokensLegend struct {
	TokenTypes     []string `json:"tokenTypes"`
	TokenModifiers []string `json:"tokenModifiers"`
}

// SemanticTokensOptions is advertised in ServerCapabilities.
type SemanticTokensOptions struct {
	Legend SemanticTokensLegend `json:"legend"`
	Full   bool                 `json:"full"`
	Range  bool                 `json:"range,omitempty"`
}

// ---- Code action ----

// CodeActionOptions advertises which code-action kinds the server
// can produce. Clients that honor the list (VS Code in particular)
// filter their "Source Action…" menu and on-save runners by it, so
// advertising `source.organizeImports` here is what makes
// `editor.codeActionsOnSave` actually fire for Osty files.
type CodeActionOptions struct {
	CodeActionKinds []string `json:"codeActionKinds,omitempty"`
	ResolveProvider bool     `json:"resolveProvider,omitempty"`
}

// CodeActionParams is the payload of `textDocument/codeAction`. The
// Context.Diagnostics slice holds whatever problems the editor has
// flagged for the current cursor — that's where quick-fix candidates
// come from.
type CodeActionParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
	Range        Range                  `json:"range"`
	Context      CodeActionContext      `json:"context"`
}

// CodeActionContext is the "why we're asking" block.
type CodeActionContext struct {
	Diagnostics []LSPDiagnostic `json:"diagnostics"`
	Only        []string        `json:"only,omitempty"`
}

// CodeAction is one entry in the lightbulb menu.
type CodeAction struct {
	Title       string          `json:"title"`
	Kind        string          `json:"kind,omitempty"`
	Diagnostics []LSPDiagnostic `json:"diagnostics,omitempty"`
	Edit        *WorkspaceEdit  `json:"edit,omitempty"`
	IsPreferred bool            `json:"isPreferred,omitempty"`
}

// CodeActionKinds the spec lists; we only emit a handful.
const (
	CodeActionQuickFix = "quickfix"
	CodeActionRefactor = "refactor"
)
