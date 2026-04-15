package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/osty/osty/internal/token"
)

// TestLineIndexOstyToLSP covers the rune → UTF-16 code-unit math for
// ASCII, multibyte UTF-8, and astral-plane runes (which need two
// code units).
func TestLineIndexOstyToLSP(t *testing.T) {
	src := []byte("fn main() {\n    let x = \"π is 3.14\"\n    let y = \"𝕊\" // astral\n}\n")
	li := newLineIndex(src)

	cases := []struct {
		name string
		in   token.Pos
		want Position
	}{
		{
			name: "line start",
			in:   token.Pos{Offset: 0, Line: 1, Column: 1},
			want: Position{Line: 0, Character: 0},
		},
		{
			name: "after ASCII",
			in:   token.Pos{Offset: 9, Line: 1, Column: 10}, // the `(` of `main(`
			want: Position{Line: 0, Character: 9},
		},
		{
			name: "after multi-byte π (2-byte UTF-8, 1 UTF-16 unit)",
			// src line 2: "    let x = \"π is 3.14\""
			// π is at byte offset 12+2+1=... let me just compute.
			// Line 2 starts at byte 12 ("\n" at 11). So:
			// "    let x = \"π" — 14 bytes (4 sp + "let x = \"" + 2-byte π) = col 15
			// rune col: 4 sp + "let x = " (8) + `"` (1) + π (1) = 14 runes, col 15
			in:   token.Pos{Offset: 12 + 14, Line: 2, Column: 15},
			want: Position{Line: 1, Character: 14},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := li.ostyToLSP(tc.in)
			if got != tc.want {
				t.Fatalf("ostyToLSP(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestLineIndexAstralRune verifies that a single astral-plane rune
// occupies two UTF-16 code units.
func TestLineIndexAstralRune(t *testing.T) {
	// "𝕊" is U+1D54A, encoded as 4 bytes in UTF-8, 2 UTF-16 units.
	src := []byte("a𝕊b")
	li := newLineIndex(src)
	// After "a𝕊" (5 bytes): LSP character should be 1 + 2 = 3.
	pos := li.ostyToLSP(token.Pos{Offset: 5, Line: 1, Column: 3})
	if pos.Character != 3 {
		t.Fatalf("astral offset UTF-16 units: got %d, want 3", pos.Character)
	}
}

// TestLineIndexRoundTrip verifies that ostyToLSP and lspToOsty are
// inverses on every legal column of a multiline buffer.
func TestLineIndexRoundTrip(t *testing.T) {
	src := []byte("abc\nπαβγ\nxyz")
	li := newLineIndex(src)
	// Walk every rune position and round-trip it.
	lines := [][]rune{
		[]rune("abc"),
		[]rune("παβγ"),
		[]rune("xyz"),
	}
	for lineIdx, runes := range lines {
		// column 1..len+1 (end-of-line)
		for col := 1; col <= len(runes)+1; col++ {
			byteOff := 0
			for i := 0; i < lineIdx; i++ {
				byteOff += len(string(lines[i])) + 1 // +1 for '\n'
			}
			byteOff += len(string(runes[:col-1]))
			start := token.Pos{Offset: byteOff, Line: lineIdx + 1, Column: col}
			lspPos := li.ostyToLSP(start)
			back := li.lspToOsty(lspPos)
			if back.Line != start.Line || back.Column != start.Column {
				t.Fatalf("round-trip line %d col %d: got %+v (lsp=%+v)", lineIdx+1, col, back, lspPos)
			}
		}
	}
}

// ---- JSON-RPC framing ----

// TestFramingRoundTrip sends two framed messages through a bytes.Buffer
// and verifies both come back intact.
func TestFramingRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	c := newConn(bytes.NewReader([]byte{}), &buf)

	msg1 := []byte(`{"jsonrpc":"2.0","method":"hi"}`)
	msg2 := []byte(`{"jsonrpc":"2.0","method":"bye"}`)
	if err := c.writeMessage(msg1); err != nil {
		t.Fatalf("write 1: %v", err)
	}
	if err := c.writeMessage(msg2); err != nil {
		t.Fatalf("write 2: %v", err)
	}
	// Now read them back.
	rc := newConn(bytes.NewReader(buf.Bytes()), io.Discard)
	got1, err := rc.readMessage()
	if err != nil {
		t.Fatalf("read 1: %v", err)
	}
	if !bytes.Equal(got1, msg1) {
		t.Fatalf("msg1 roundtrip: got %q want %q", got1, msg1)
	}
	got2, err := rc.readMessage()
	if err != nil {
		t.Fatalf("read 2: %v", err)
	}
	if !bytes.Equal(got2, msg2) {
		t.Fatalf("msg2 roundtrip: got %q want %q", got2, msg2)
	}
}

// TestFramingRejectsMissingLength confirms the framer bails out on a
// body-only message.
func TestFramingRejectsMissingLength(t *testing.T) {
	bad := strings.NewReader("Content-Type: text/plain\r\n\r\n{}")
	c := newConn(bad, io.Discard)
	if _, err := c.readMessage(); err == nil {
		t.Fatal("expected error for missing Content-Length")
	}
}

// ---- End-to-end session ----

// session drives the server over a pair of in-memory pipes. A
// single reader goroutine pumps every incoming frame into `frames`;
// test helpers pull from that channel instead of reading the pipe
// directly, which keeps all concurrency on one axis and avoids the
// races that come from spawning ad-hoc readers per helper call.
type session struct {
	t       *testing.T
	server  *Server
	clientR *io.PipeReader
	clientW *io.PipeWriter
	serverR *io.PipeReader
	serverW *io.PipeWriter
	done    chan struct{}
	// frames receives every server→client frame, in arrival order.
	// Closed when the reader goroutine hits EOF.
	frames chan []byte
	// Tests may need to peek back at frames read past a request they
	// were waiting for (e.g. a notification that arrived between the
	// request and the reply). pending buffers those for re-use.
	pending []rpcRequest
	pendMu  sync.Mutex
}

// startSession spins up the server and returns a session driver.
func startSession(t *testing.T) *session {
	t.Helper()
	// Pipe from client to server.
	serverR, clientW := io.Pipe()
	// Pipe from server to client.
	clientR, serverW := io.Pipe()
	s := NewServer(serverR, serverW, io.Discard)
	sess := &session{
		t:       t,
		server:  s,
		clientR: clientR,
		clientW: clientW,
		serverR: serverR,
		serverW: serverW,
		done:    make(chan struct{}),
		frames:  make(chan []byte, 64),
	}
	// Server loop.
	go func() {
		defer close(sess.done)
		_ = s.Run()
	}()
	// Reader pump: one goroutine reads the client→side pipe and
	// feeds frames into the channel until EOF.
	go func() {
		defer close(sess.frames)
		br := bufio.NewReader(clientR)
		for {
			body, err := readOneFrame(br)
			if err != nil {
				return
			}
			sess.frames <- body
		}
	}()
	return sess
}

// readOneFrame reads one Content-Length-framed message off br.
func readOneFrame(br *bufio.Reader) ([]byte, error) {
	length := -1
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "Content-Length:") {
			v := strings.TrimSpace(line[len("Content-Length:"):])
			_, _ = fmt.Sscanf(v, "%d", &length)
		}
	}
	if length < 0 {
		return nil, fmt.Errorf("missing Content-Length")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(br, body); err != nil {
		return nil, err
	}
	return body, nil
}

// initialize runs the LSP handshake: initialize request + its reply +
// the initialized notification. Tests that then exercise a handler
// usually pair this with openDoc.
func (s *session) initialize() {
	s.t.Helper()
	s.send("init", "initialize", InitializeParams{})
	resp := s.waitResponse("init")
	if resp.Error != nil {
		s.t.Fatalf("initialize error: %+v", resp.Error)
	}
	s.send("", "initialized", map[string]any{})
}

// openDoc sends didOpen for uri/text and drains the first
// publishDiagnostics so subsequent waitResponse calls won't pick it up.
func (s *session) openDoc(uri, text string) {
	s.t.Helper()
	s.send("", "textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: uri, LanguageID: "osty", Version: 1, Text: text},
	})
	_ = s.drainNotifications("textDocument/publishDiagnostics", 1*time.Second)
}

// stop closes the client side and waits for Run to return.
func (s *session) stop() {
	_ = s.clientW.Close()
	// Closing the server-write pipe unblocks our reader goroutine
	// if it's stuck on a read.
	_ = s.serverW.Close()
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
		s.t.Fatal("server did not stop in time")
	}
}

// send dispatches a request or notification. Notifications have id == "".
func (s *session) send(id, method string, params any) {
	s.t.Helper()
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			s.t.Fatalf("marshal params: %v", err)
		}
		raw = b
	}
	var body []byte
	var err error
	if id == "" {
		body, err = json.Marshal(rpcNotification{JSONRPC: "2.0", Method: method, Params: raw})
	} else {
		body, err = json.Marshal(struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      string          `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params,omitempty"`
		}{"2.0", id, method, raw})
	}
	if err != nil {
		s.t.Fatalf("marshal body: %v", err)
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	if _, err := s.clientW.Write([]byte(header)); err != nil {
		s.t.Fatalf("write header: %v", err)
	}
	if _, err := s.clientW.Write(body); err != nil {
		s.t.Fatalf("write body: %v", err)
	}
}

// waitResponse pulls frames until it sees one whose id matches; any
// intervening notifications are stashed in pending for later lookup.
func (s *session) waitResponse(id string) rpcResponse {
	s.t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case <-deadline:
			s.t.Fatalf("timeout waiting for response id=%s", id)
		case body, ok := <-s.frames:
			if !ok {
				s.t.Fatalf("frames channel closed while waiting for id=%s", id)
			}
			// Is this a response?
			var probe map[string]json.RawMessage
			if err := json.Unmarshal(body, &probe); err != nil {
				s.t.Fatalf("decode frame: %v", err)
			}
			if _, isResp := probe["id"]; isResp {
				var resp rpcResponse
				if err := json.Unmarshal(body, &resp); err != nil {
					s.t.Fatalf("decode response: %v", err)
				}
				if string(resp.ID) == `"`+id+`"` {
					return resp
				}
				continue
			}
			var n rpcRequest
			if err := json.Unmarshal(body, &n); err != nil {
				s.t.Fatalf("decode notification: %v", err)
			}
			s.pendMu.Lock()
			s.pending = append(s.pending, n)
			s.pendMu.Unlock()
		}
	}
}

// drainNotifications collects every stashed-or-incoming notification
// matching `method` within `timeout`. Useful for reading
// textDocument/publishDiagnostics that arrives asynchronously after
// didOpen / didChange.
func (s *session) drainNotifications(method string, timeout time.Duration) []rpcRequest {
	s.t.Helper()
	var out []rpcRequest
	// First, flush stashed notifications.
	s.pendMu.Lock()
	keep := s.pending[:0]
	for _, n := range s.pending {
		if n.Method == method {
			out = append(out, n)
		} else {
			keep = append(keep, n)
		}
	}
	s.pending = keep
	s.pendMu.Unlock()
	// Then read more until the timeout elapses with no new match.
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			return out
		case body, ok := <-s.frames:
			if !ok {
				return out
			}
			var probe map[string]json.RawMessage
			if err := json.Unmarshal(body, &probe); err != nil {
				continue
			}
			if _, isResp := probe["id"]; isResp {
				// Stray response; stash for completeness even
				// though no waitResponse is following us.
				continue
			}
			var n rpcRequest
			if err := json.Unmarshal(body, &n); err != nil {
				continue
			}
			if n.Method == method {
				out = append(out, n)
				// Give the server a tick to emit more of the
				// same kind of notification (e.g. a follow-up
				// publishDiagnostics after didChange).
				continue
			}
			s.pendMu.Lock()
			s.pending = append(s.pending, n)
			s.pendMu.Unlock()
		}
	}
}

// ---- End-to-end tests ----

const sampleSrc = `fn greet(name: String) -> String {
    "hello, " + name
}

fn main() {
    let msg = greet("world")
    println(msg)
}
`

const sampleURI = "file:///tmp/sample.osty"

func TestInitializeAndShutdown(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()

	sess.send("1", "initialize", InitializeParams{})
	resp := sess.waitResponse("1")
	if resp.Error != nil {
		t.Fatalf("initialize error: %+v", resp.Error)
	}
	var res InitializeResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("decode init result: %v", err)
	}
	if !res.Capabilities.HoverProvider {
		t.Error("expected hoverProvider=true")
	}
	if !res.Capabilities.DefinitionProvider {
		t.Error("expected definitionProvider=true")
	}
	if !res.Capabilities.DocumentFormattingProvider {
		t.Error("expected documentFormattingProvider=true")
	}
	if !res.Capabilities.DocumentSymbolProvider {
		t.Error("expected documentSymbolProvider=true")
	}

	sess.send("", "initialized", map[string]any{})
	sess.send("2", "shutdown", nil)
	resp = sess.waitResponse("2")
	if resp.Error != nil {
		t.Fatalf("shutdown error: %+v", resp.Error)
	}

	sess.send("", "exit", nil)
}

func TestDidOpenPublishesDiagnostics(t *testing.T) {
	// A file with an intentional undefined reference.
	bad := "fn main() { println(undeclared) }\n"
	sess := startSession(t)
	defer sess.stop()
	sess.send("1", "initialize", InitializeParams{})
	_ = sess.waitResponse("1")
	sess.send("", "initialized", map[string]any{})

	sess.send("", "textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{
			URI:        sampleURI,
			LanguageID: "osty",
			Version:    1,
			Text:       bad,
		},
	})

	// Wait for one publishDiagnostics notification and check that
	// it reports the undefined name.
	notifs := sess.drainNotifications("textDocument/publishDiagnostics", 2*time.Second)
	if len(notifs) == 0 {
		t.Fatal("no diagnostics published")
	}
	var pp PublishDiagnosticsParams
	if err := json.Unmarshal(notifs[0].Params, &pp); err != nil {
		t.Fatalf("decode publish: %v", err)
	}
	if pp.URI != sampleURI {
		t.Errorf("publish URI = %q want %q", pp.URI, sampleURI)
	}
	if len(pp.Diagnostics) == 0 {
		t.Fatal("expected at least one diagnostic for undefined name")
	}
	found := false
	for _, d := range pp.Diagnostics {
		if strings.Contains(d.Message, "undeclared") ||
			strings.Contains(d.Message, "not in scope") ||
			strings.Contains(strings.ToLower(d.Message), "undefined") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("no diagnostic mentions the undeclared name; got: %+v", pp.Diagnostics)
	}
}

func TestDidChangeRepublishes(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.send("1", "initialize", InitializeParams{})
	_ = sess.waitResponse("1")
	sess.send("", "initialized", map[string]any{})

	// Open a clean file.
	sess.send("", "textDocument/didOpen", DidOpenTextDocumentParams{
		TextDocument: TextDocumentItem{URI: sampleURI, LanguageID: "osty", Version: 1, Text: sampleSrc},
	})
	ns := sess.drainNotifications("textDocument/publishDiagnostics", 1*time.Second)
	if len(ns) == 0 {
		t.Fatal("no initial publish")
	}
	// Inject an error via didChange.
	broken := "fn main() { println(badname) }\n"
	sess.send("", "textDocument/didChange", DidChangeTextDocumentParams{
		TextDocument: VersionedTextDocumentIdentifier{URI: sampleURI, Version: 2},
		ContentChanges: []TextDocumentContentChangeEvent{
			{Text: broken},
		},
	})
	ns = sess.drainNotifications("textDocument/publishDiagnostics", 2*time.Second)
	if len(ns) == 0 {
		t.Fatal("no post-change publish")
	}
	var pp PublishDiagnosticsParams
	if err := json.Unmarshal(ns[len(ns)-1].Params, &pp); err != nil {
		t.Fatal(err)
	}
	if len(pp.Diagnostics) == 0 {
		t.Error("expected diagnostics after introducing error")
	}
}

func TestHoverReturnsType(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	sess.openDoc(sampleURI, sampleSrc)

	// Find the position of `msg` in the second `msg` reference:
	// "    println(msg)". Line index (0-based) = 6, char = 12.
	sess.send("2", "textDocument/hover", HoverParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Position:     Position{Line: 6, Character: 12},
	})
	resp := sess.waitResponse("2")
	if resp.Error != nil {
		t.Fatalf("hover error: %+v", resp.Error)
	}
	if string(resp.Result) == "null" {
		t.Fatal("hover returned null; expected content")
	}
	var h Hover
	if err := json.Unmarshal(resp.Result, &h); err != nil {
		t.Fatalf("decode hover: %v", err)
	}
	if !strings.Contains(h.Contents.Value, "msg") {
		t.Errorf("hover does not mention msg: %q", h.Contents.Value)
	}
}

func TestDefinitionJumpsToDeclaration(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	sess.openDoc(sampleURI, sampleSrc)

	// Click on `greet` inside `let msg = greet("world")` on line 6.
	// Line 6 (0-based 5) content: "    let msg = greet("world")".
	// `greet` starts at character 14.
	sess.send("2", "textDocument/definition", DefinitionParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Position:     Position{Line: 5, Character: 16},
	})
	resp := sess.waitResponse("2")
	if resp.Error != nil {
		t.Fatalf("definition error: %+v", resp.Error)
	}
	if string(resp.Result) == "null" {
		t.Fatal("definition returned null")
	}
	var loc Location
	if err := json.Unmarshal(resp.Result, &loc); err != nil {
		t.Fatalf("decode definition: %v", err)
	}
	if loc.URI != sampleURI {
		t.Errorf("definition URI = %q want %q", loc.URI, sampleURI)
	}
	if loc.Range.Start.Line != 0 {
		t.Errorf("expected definition on line 0, got %d", loc.Range.Start.Line)
	}
}

func TestDocumentSymbolListsDecls(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	sess.openDoc(sampleURI, sampleSrc)

	sess.send("2", "textDocument/documentSymbol", DocumentSymbolParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
	})
	resp := sess.waitResponse("2")
	if resp.Error != nil {
		t.Fatalf("documentSymbol error: %+v", resp.Error)
	}
	var syms []DocumentSymbol
	if err := json.Unmarshal(resp.Result, &syms); err != nil {
		t.Fatalf("decode symbols: %v", err)
	}
	names := map[string]bool{}
	for _, s := range syms {
		names[s.Name] = true
	}
	if !names["greet"] || !names["main"] {
		t.Errorf("expected greet and main in symbols, got %v", names)
	}
}

func TestFormattingProducesEdit(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	// Indentation should be 4 spaces per the formatter.
	sess.openDoc(sampleURI, "fn main() {\n  let x = 1\n  println(x)\n}\n")

	sess.send("2", "textDocument/formatting", DocumentFormattingParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Options:      FormattingOptions{TabSize: 4, InsertSpaces: true},
	})
	resp := sess.waitResponse("2")
	if resp.Error != nil {
		t.Fatalf("formatting error: %+v", resp.Error)
	}
	var edits []TextEdit
	if err := json.Unmarshal(resp.Result, &edits); err != nil {
		t.Fatalf("decode edits: %v", err)
	}
	if len(edits) != 1 {
		t.Fatalf("expected 1 edit, got %d", len(edits))
	}
	if !strings.Contains(edits[0].NewText, "    let x") {
		t.Errorf("formatting didn't canonicalize indentation: %q", edits[0].NewText)
	}
}

func TestMethodNotFound(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()

	sess.send("2", "textDocument/nonsense", nil)
	resp := sess.waitResponse("2")
	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != errMethodNotFound {
		t.Errorf("code = %d want %d", resp.Error.Code, errMethodNotFound)
	}
}

func TestRequestBeforeInitialize(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.send("1", "textDocument/hover", HoverParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
	})
	resp := sess.waitResponse("1")
	if resp.Error == nil {
		t.Fatal("expected error for request before initialize")
	}
	if resp.Error.Code != errServerNotInitialized {
		t.Errorf("code = %d want %d", resp.Error.Code, errServerNotInitialized)
	}
}

// hoverAt is a small wrapper used by the edge-case tests below so
// the per-test boilerplate stays focused on positions and expected
// outcomes.
func (s *session) hoverAt(id string, uri string, line, char uint32) rpcResponse {
	s.t.Helper()
	s.send(id, "textDocument/hover", HoverParams{
		TextDocument: TextDocumentIdentifier{URI: uri},
		Position:     Position{Line: line, Character: char},
	})
	return s.waitResponse(id)
}

// TestHoverOnWhitespace asserts that hovering on a position where no
// identifier lives returns null per the LSP spec.
func TestHoverOnWhitespace(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	sess.openDoc(sampleURI, sampleSrc)

	// Line 0 col 0 sits on the `f` of `fn` — that's a keyword, not an
	// Ident the resolver tracks, so hover should be null.
	resp := sess.hoverAt("h", sampleURI, 0, 0)
	if resp.Error != nil {
		t.Fatalf("hover error: %+v", resp.Error)
	}
	if string(resp.Result) != "null" {
		t.Errorf("expected null hover for keyword position, got %s", resp.Result)
	}
}

// TestHoverOnBuiltin exercises the SymBuiltin branch of
// writeSymSignature by hovering the prelude `println` call.
func TestHoverOnBuiltin(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	sess.openDoc(sampleURI, sampleSrc)

	// `println` in `    println(msg)` — line 6, starts at col 4.
	resp := sess.hoverAt("h", sampleURI, 6, 6)
	if resp.Error != nil {
		t.Fatalf("hover error: %+v", resp.Error)
	}
	if string(resp.Result) == "null" {
		t.Fatal("expected hover content for builtin println")
	}
	var h Hover
	if err := json.Unmarshal(resp.Result, &h); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.Contents.Value, "println") {
		t.Errorf("hover missing println: %q", h.Contents.Value)
	}
}

// TestDefinitionOnBuiltinReturnsNull confirms we don't try to point
// the editor at a builtin (there's no source location to jump to).
func TestDefinitionOnBuiltinReturnsNull(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	sess.openDoc(sampleURI, sampleSrc)

	sess.send("d", "textDocument/definition", DefinitionParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
		Position:     Position{Line: 6, Character: 6}, // println
	})
	resp := sess.waitResponse("d")
	if resp.Error != nil {
		t.Fatalf("definition error: %+v", resp.Error)
	}
	if string(resp.Result) != "null" {
		t.Errorf("expected null for builtin definition, got %s", resp.Result)
	}
}

// TestDidCloseClearsDiagnostics verifies that closing a file pushes
// an empty diagnostic list so stale squigglies vanish.
func TestDidCloseClearsDiagnostics(t *testing.T) {
	sess := startSession(t)
	defer sess.stop()
	sess.initialize()
	// Open a file with an error so the first publish carries entries.
	sess.openDoc(sampleURI, "fn main() { println(unknown) }\n")

	sess.send("", "textDocument/didClose", DidCloseTextDocumentParams{
		TextDocument: TextDocumentIdentifier{URI: sampleURI},
	})
	notifs := sess.drainNotifications("textDocument/publishDiagnostics", 1*time.Second)
	if len(notifs) == 0 {
		t.Fatal("expected a clearing publishDiagnostics after didClose")
	}
	var pp PublishDiagnosticsParams
	if err := json.Unmarshal(notifs[len(notifs)-1].Params, &pp); err != nil {
		t.Fatal(err)
	}
	if len(pp.Diagnostics) != 0 {
		t.Errorf("expected empty diagnostics on close, got %d", len(pp.Diagnostics))
	}
}
