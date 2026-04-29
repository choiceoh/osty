package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestWebsocketModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["websocket"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.websocket not loaded")
	}
	for _, name := range []string{"acceptKey", "handshakeHeaders", "isHandshake", "text", "binary", "ping", "pong", "close", "frame", "masked", "encode", "decode", "textPayload"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.websocket missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.websocket.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.websocket.%s not public", name)
		}
	}
	for _, name := range []string{"Opcode", "Frame", "DecodedFrame"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.websocket missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.websocket.%s not public", name)
		}
	}
}

func TestWebsocketModuleSourcePinsHandshakeAndFrameBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["websocket"]
	if mod == nil {
		t.Fatal("std.websocket module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`encoding.base64.encode(crypto.sha1(seed))`,
		`out.insert("Sec-WebSocket-Accept", acceptKey(clientKey))`,
		`pub fn encode(frame: Frame) -> Result<Bytes, Error>`,
		`pub fn decode(data: Bytes) -> Result<DecodedFrame, Error>`,
		`strings.toLower(strings.trimSpace(header(headers, "Upgrade") ?? ""))`,
		`fn hasConnectionToken(value: String, token: String) -> Bool`,
		`strings.split(value, ",")`,
		`fn isValidClientKey(key: String) -> Bool`,
		`decoded.len() == 16`,
		`fn xorByte(a: Int, b: Int) -> Int`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.websocket source missing %q", want)
		}
	}
}
