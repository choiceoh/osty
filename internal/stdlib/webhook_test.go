package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestWebhookModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["webhook"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.webhook not loaded")
	}
	for _, name := range []string{
		"stripe", "github", "supabase", "slack", "discord",
		"hmacSha256Hex", "hmacSha256Base64", "unsigned",
		"verify", "verifyRequest", "eventFromRequest", "dispatch",
		"emptyStore", "ack", "retryLater", "reject",
		"idempotencyKey", "wasProcessed", "isInFlight",
		"markInFlight", "markProcessed", "clearInFlight",
		"header", "bodyFingerprint",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.webhook missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.webhook.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.webhook.%s not public", name)
		}
	}
	for _, name := range []string{
		"Provider", "SignatureScheme", "VerificationCode", "DispatchStatus",
		"Policy", "Verification", "Event", "Store",
		"DispatchDecision", "DispatchOutcome",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.webhook missing type %q", name)
		}
		if !sym.Pub {
			t.Fatalf("std.webhook.%s not public", name)
		}
	}
}

func TestWebhookModuleSourcePinsSecurityAndDispatchBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["webhook"]
	if mod == nil {
		t.Fatal("std.webhook module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`signatureHeader: "Stripe-Signature"`,
		`signatureHeader: "X-Hub-Signature-256"`,
		`timestampHeader: "X-Slack-Request-Timestamp"`,
		`signatureHeader: "x-supabase-signature"`,
		`signatureHeader: "X-Signature-Ed25519"`,
		`Discord Ed25519 verification requires a host crypto verifier`,
		`crypto.hmac.sha256(bytes.fromString(secret), payload)`,
		`crypto.constantTimeEq(bytes.fromString(a), bytes.fromString(b))`,
		`bytes.concat(bytes.fromString("{timestampText}."), body)`,
		`bytes.concat(bytes.fromString("v0:{timestampText}:"), body)`,
		`ReplayWindowExceeded`,
		`pub struct Store {`,
		`pub processed: Map<String, Bool> = {:}`,
		`pub inFlight: Map<String, Bool> = {:}`,
		`return DispatchOutcome {`,
		`status: Duplicate`,
		`status: InFlight`,
		`status: RetryLater`,
		`markProcessed(store, key)`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.webhook source missing %q", want)
		}
	}
}
