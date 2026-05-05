package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestWsClientModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["ws_client"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.ws.client not loaded")
	}
	for _, name := range []string{
		"connect", "send", "sendText", "sendBinary",
		"receive", "receiveText",
		"close", "ping", "pong",
		"isConnected", "remoteAddr", "eventLoop",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.ws.client missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.ws.client.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.ws.client.%s not public", name)
		}
	}
	for _, name := range []string{
		"ConnectionState", "Connection", "MessageEvent", "ConnectionEvent",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.ws.client missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.ws.client.%s not public", name)
		}
	}
}

func TestWsClientModuleSourcePinsNonExecutingBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["ws_client"]
	if mod == nil {
		t.Fatal("std.ws.client module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn connect(url: String, headers: Headers = {:}) -> Result<Connection, Error>`,
		`pub fn send(conn: Connection, frame: websocket.Frame) -> Result<(), Error>`,
		`pub fn receive(conn: Connection) -> Result<websocket.Frame, Error>`,
		`pub fn close(conn: Connection, code: Int = 1000, reason: String = "") -> Result<(), Error>`,
		`pub fn ping(conn: Connection, data: bytes.Bytes) -> Result<(), Error>`,
		`pub fn eventLoop(conn: Connection) -> Result<ConnectionEvent, Error>`,
		`ConnectionEvent`,
		`MessageEvent`,
		`ConnectionState`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.ws.client source missing %q", want)
		}
	}
}
