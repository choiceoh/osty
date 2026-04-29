package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestSmtpModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["smtp"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.smtp not loaded")
	}
	for _, name := range []string{
		"noAuth", "plainAuth", "loginAuth", "config", "withSecurity", "withAuth",
		"defaultPort", "ehlo", "helo", "startTls", "mailFrom", "rcptTo",
		"dataCommand", "quit", "rset", "noop", "authPlain", "authLogin", "authCommands",
		"transaction", "commands", "renderCommands", "parseReply", "parseReplies",
		"isPositiveCompletion", "isPositiveIntermediate", "isTransientFailure", "isPermanentFailure",
		"capabilities", "hasCapability", "supportsAuth", "dotStuff", "dataBlock",
	} {
		requirePublicFn(t, mod, "smtp", name)
	}
	for _, name := range []string{"AuthKind", "Security", "Auth", "ClientConfig", "Reply", "Capability", "Transaction"} {
		requirePublicType(t, mod, "smtp", name)
	}
}

func TestZipModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["zip"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.zip not loaded")
	}
	for _, name := range []string{
		"file", "encode", "decode", "list", "extract", "contains", "isArchive", "crc32", "methodName",
	} {
		requirePublicFn(t, mod, "zip", name)
	}
	for _, name := range []string{"Method", "Entry"} {
		requirePublicType(t, mod, "zip", name)
	}
}

func TestImageModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["image"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.image not loaded")
	}
	for _, name := range []string{
		"identify", "formatName", "parse", "dimensions", "isImage",
		"isPng", "isJpeg", "isGif", "isBmp", "isWebp",
	} {
		requirePublicFn(t, mod, "image", name)
	}
	for _, name := range []string{"Format", "Size", "Metadata"} {
		requirePublicType(t, mod, "image", name)
	}
}

func TestBigGapModuleSourcePinsBehavior(t *testing.T) {
	reg := LoadCached()
	cases := map[string][]string{
		"smtp": {
			`pub fn authPlain(username: String, password: String) -> String`,
			`pub fn parseReply(line: String) -> Result<Reply, Error>`,
			`pub fn commands(tx: Transaction) -> Result<List<String>, Error>`,
			`pub fn dotStuff(data: String) -> String`,
			`pub fn dataBlock(data: String) -> String`,
			`out.push(ensureDataBlock(tx.envelope.data))`,
			`if !strings.endsWith(cmd, crlf())`,
			`fn ensureDataBlock(data: String) -> String`,
			`dataBlock(data)`,
			`strings.endsWith(data, "{crlf()}.{crlf()}")`,
		},
		"zip": {
			`pub fn encode(entries: List<Entry>) -> Result<Bytes, Error>`,
			`pub fn decode(archive: Bytes) -> Result<List<Entry>, Error>`,
			`pub fn crc32(data: Bytes) -> Int`,
			`strings.contains(clean, "\\")`,
			`fn isDriveLetterPath(name: String) -> Bool`,
		},
		"image": {
			`pub fn identify(data: Bytes) -> Format`,
			`fn parsePng(data: Bytes) -> Result<Metadata, Error>`,
			`fn parseJpeg(data: Bytes) -> Result<Metadata, Error>`,
			`if data.len() < 11`,
		},
	}
	for module, wants := range cases {
		mod := reg.Modules[module]
		if mod == nil {
			t.Fatalf("std.%s module missing", module)
		}
		src := string(mod.Source)
		for _, want := range wants {
			if !strings.Contains(src, want) {
				t.Fatalf("std.%s source missing %q", module, want)
			}
		}
	}
}

func requirePublicFn(t *testing.T, mod *Module, module, name string) {
	t.Helper()
	sym := mod.Package.PkgScope.LookupLocal(name)
	if sym == nil {
		t.Fatalf("std.%s missing export %q", module, name)
	}
	if sym.Kind != resolve.SymFn {
		t.Fatalf("std.%s.%s kind = %s, want fn", module, name, sym.Kind)
	}
	if !sym.Pub {
		t.Fatalf("std.%s.%s not public", module, name)
	}
}

func requirePublicType(t *testing.T, mod *Module, module, name string) {
	t.Helper()
	sym := mod.Package.PkgScope.LookupLocal(name)
	if sym == nil {
		t.Fatalf("std.%s missing type %q", module, name)
	}
	if !sym.Pub {
		t.Fatalf("std.%s.%s not public", module, name)
	}
}
