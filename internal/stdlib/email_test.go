package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestEmailModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["email"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.email not loaded")
	}
	for _, name := range []string{"address", "namedAddress", "parseAddress", "formatAddress", "attachment", "message", "withCc", "withBcc", "withHtml", "withHeader", "withAttachment", "recipients", "render", "envelope", "smtpData", "smtpCommands", "isEmail"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.email missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.email.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.email.%s not public", name)
		}
	}
	for _, name := range []string{"Address", "Attachment", "Message", "Envelope"} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.email missing type %q", name)
		}
		if sym.Kind != resolve.SymStruct {
			t.Fatalf("std.email.%s kind = %s, want struct", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.email.%s not public", name)
		}
	}
}

func TestEmailModuleSourcePinsMimeBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["email"]
	if mod == nil {
		t.Fatal("std.email module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn render(msg: Message) -> Result<String, Error>`,
		`Content-Type: multipart/mixed; boundary=`,
		`encoding.base64.encode(part.data)`,
		`pub fn smtpCommands(hostname: String, env: Envelope) -> List<String>`,
		`fn dotStuff(value: String) -> String`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.email source missing %q", want)
		}
	}
}
