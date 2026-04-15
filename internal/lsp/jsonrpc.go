package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

// JSON-RPC 2.0 over stdio, framed with LSP-style headers:
//
//	Content-Length: 123\r\n
//	\r\n
//	{"jsonrpc":"2.0",...}
//
// Only Content-Length is required; Content-Type (when sent) is
// ignored. Readers must be tolerant of trailing whitespace but
// strict about the blank line separator that terminates the header
// block.

// conn wraps a bidirectional stream used by the LSP transport. Reads
// are buffered for line-oriented header parsing; writes are
// serialized through a mutex so concurrent handlers (future work)
// can emit notifications without interleaving bytes.
type conn struct {
	r *bufio.Reader
	w io.Writer

	writeMu sync.Mutex
}

// newConn builds a conn over the given reader/writer pair. The
// reader is wrapped in a bufio.Reader if it isn't one already.
func newConn(r io.Reader, w io.Writer) *conn {
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	return &conn{r: br, w: w}
}

// readMessage pulls one framed JSON-RPC message off the wire and
// returns its raw body. io.EOF is surfaced to the caller so the main
// loop can exit cleanly; any framing error becomes a descriptive
// error the caller can log to stderr.
func (c *conn) readMessage() ([]byte, error) {
	length, err := readHeaders(c.r)
	if err != nil {
		return nil, err
	}
	if length < 0 {
		return nil, fmt.Errorf("lsp: missing Content-Length header")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(c.r, body); err != nil {
		return nil, fmt.Errorf("lsp: short body read: %w", err)
	}
	return body, nil
}

// readHeaders consumes the header block terminated by a CRLF CRLF
// and returns the Content-Length. It returns io.EOF only when the
// stream ends cleanly before any header bytes were seen; a partial
// header block is a framing error.
func readHeaders(r *bufio.Reader) (int, error) {
	length := -1
	sawAnyHeader := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && !sawAnyHeader && line == "" {
				return 0, io.EOF
			}
			return 0, fmt.Errorf("lsp: header read: %w", err)
		}
		// Accept both CRLF (required by spec) and bare LF (tolerant
		// for pipes and test harnesses).
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if !sawAnyHeader {
				return 0, fmt.Errorf("lsp: empty header block")
			}
			return length, nil
		}
		sawAnyHeader = true
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			return 0, fmt.Errorf("lsp: malformed header %q", line)
		}
		name := strings.TrimSpace(line[:colon])
		value := strings.TrimSpace(line[colon+1:])
		if strings.EqualFold(name, "Content-Length") {
			n, err := strconv.Atoi(value)
			if err != nil || n < 0 {
				return 0, fmt.Errorf("lsp: invalid Content-Length %q", value)
			}
			length = n
		}
		// Other headers (Content-Type) are intentionally ignored.
	}
}

// writeMessage serializes body with a Content-Length header and
// pushes it to the writer atomically.
func (c *conn) writeMessage(body []byte) error {
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Content-Length: %d\r\n\r\n", len(body))
	buf.Write(body)

	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err := c.w.Write(buf.Bytes())
	return err
}

// writeResponse marshals a successful response with the given raw
// result and sends it.
func (c *conn) writeResponse(id json.RawMessage, result json.RawMessage) error {
	return c.writeMessage(mustMarshal(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  result,
	}))
}

// writeError sends a JSON-RPC error response.
func (c *conn) writeError(id json.RawMessage, code int, message string) error {
	if id == nil {
		// Preserve null ID so clients can correlate parse-time
		// failures. An empty RawMessage would be omitted.
		id = json.RawMessage("null")
	}
	return c.writeMessage(mustMarshal(rpcResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: message},
	}))
}

// writeNotification sends a notification (no reply).
func (c *conn) writeNotification(method string, params any) error {
	raw, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("lsp: marshal %s params: %w", method, err)
	}
	return c.writeMessage(mustMarshal(rpcNotification{
		JSONRPC: "2.0",
		Method:  method,
		Params:  raw,
	}))
}

// mustMarshal is the internal helper we use when the marshal target
// is a package-private struct whose shape we control. Any failure
// indicates a programmer error (e.g. a non-serializable field
// sneaking in), so we panic — there's no graceful recovery.
func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("lsp: marshal %T: %v", v, err))
	}
	return b
}
