package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

func TestNetModuleSurface(t *testing.T) {
	reg := LoadCached()
	if reg.Modules["net"] == nil {
		t.Fatal("stdlib net module missing")
	}

	for _, name := range []string{
		"lookup",
		"lookupAddr",
		"parseIpv4",
		"parseIpv6",
		"parseIp",
		"parseAddr",
		"parseSocketAddr",
		"parseCidr",
		"splitHostPort",
		"resolve",
		"resolveAll",
		"tcpConnect",
		"tcpConnectTimeout",
		"tcpListen",
		"connect",
		"connectTimeout",
		"listen",
	} {
		fn := reg.LookupFnDecl("net", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(net, %s) = nil", name)
		}
		if fn.Body == nil {
			t.Fatalf("net.%s body = nil, want pure-Osty implementation", name)
		}
	}

	for _, name := range []string{
		"udpBind",
	} {
		fn := reg.LookupFnDecl("net", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(net, %s) = nil", name)
		}
		if fn.Body != nil {
			t.Fatalf("net.%s has a source body, want runtime bridge declaration", name)
		}
	}

	for _, tc := range []struct {
		typeName   string
		methodName string
	}{
		{"Ipv4Addr", "isGlobalUnicast"},
		{"Ipv6Addr", "isGlobalUnicast"},
		{"IpAddr", "isGlobalUnicast"},
		{"TcpStream", "send"},
		{"TcpStream", "recv"},
		{"TcpStream", "remoteAddr"},
	} {
		fn := reg.LookupMethodDecl("net", tc.typeName, tc.methodName)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(net, %s, %s) = nil", tc.typeName, tc.methodName)
		}
		if fn.Body == nil {
			t.Fatalf("net.%s.%s body = nil, want pure-Osty implementation", tc.typeName, tc.methodName)
		}
	}
}

func TestNetModuleSourcePinsAddressQualityGuards(t *testing.T) {
	src := netModuleSource(t)
	for _, want := range []string{
		"::ffff:{addr}",
		"self.a == 100 && self.b >= 64 && self.b <= 127",
		"self.a == 198 && (self.b == 18 || self.b == 19)",
		"embedded IPv4 must be last group in IPv6 address",
		"if countChar(hostport, ':') > 1",
		"IPv6 addresses with ports must use [host]:port",
		"if port.isEmpty()",
		"addr.isGlobalUnicast()",
		"pub struct Addr",
		"pub type TcpConn = TcpStream",
		"pub fn connect(addr: String)",
		"pub fn resolve(addr: String)",
		`use c "osty_runtime" as rt`,
		"rt.osty_rt_net_tcp_connect",
		"rt.osty_rt_net_lookup_text",
		"self.write(data)",
		"socketAddrToAddr(addr)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.net source missing %q", want)
		}
	}
}

func TestNetImportResolvesAddressHelpers(t *testing.T) {
	src := `
use std.net

pub fn demo() -> Result<String, Error> {
    let parsed = net.parseAddr("[::1]:443")?
    let resolved = net.resolve("127.0.0.1:443")?
    let all = net.resolveAll("127.0.0.1")?
    let v6 = net.parseIpv6("::ffff:192.0.2.1")?
    let ip = v6.toIp()
    let loopback = net.parseIpv4("127.0.0.1")?
    let (host, port) = net.splitHostPort("[::1]:443")?
    let sock = net.parseSocketAddr("[::1]:443")?
    let cidr = net.parseCidr("198.18.0.0/15")?
    let conn = net.connect("127.0.0.1:443")?
    let _ = conn.send("ping".toBytes())?
    let _ = conn.remoteAddr()?
    Ok("{parsed.toString()}:{resolved.toString()}:{all.len()}:{v6.toString()}:{ip.isGlobalUnicast()}:{host}:{port}:{sock.toString()}:{cidr.contains(loopback)}")
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	res := resolve.ResolveFileDefault(file, Load())
	for _, d := range res.Diags {
		if d == nil || d.Severity != diag.Error {
			continue
		}
		t.Errorf("resolver rejected std.net fixture: %s: %s", d.Code, d.Message)
	}
}

func netModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["net"]
	if mod == nil {
		t.Fatal("stdlib net module missing")
	}
	return string(mod.Source)
}
