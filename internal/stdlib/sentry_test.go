package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestSentryModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := requireSentryModule(t, reg)

	for _, tc := range []struct {
		name string
		kind resolve.SymbolKind
	}{
		{"Dsn", resolve.SymStruct},
		{"Client", resolve.SymStruct},
		{"User", resolve.SymStruct},
		{"Scope", resolve.SymStruct},
		{"CaptureOptions", resolve.SymStruct},
	} {
		sym := mod.Package.PkgScope.LookupLocal(tc.name)
		if sym == nil {
			t.Fatalf("std.sentry missing type %q", tc.name)
		}
		if sym.Kind != tc.kind {
			t.Fatalf("std.sentry.%s kind = %s, want %s", tc.name, sym.Kind, tc.kind)
		}
		if !sym.Pub {
			t.Fatalf("std.sentry.%s not public", tc.name)
		}
	}

	for _, name := range []string{
		"parseDsn", "client", "scope", "options", "user",
		"endpointUrl", "sentryTraceHeader", "traceHeaders", "authHeader", "headers",
		"captureMessageHttpRequest", "captureExceptionHttpRequest", "captureEventHttpRequest", "captureTransactionHttpRequest",
		"captureMessage", "captureException", "captureEvent", "captureTransaction",
		"eventPayload", "transactionPayload", "envelopeForEvent", "envelopeForTransaction",
		"envelope", "envelopeHeader", "envelopeItem", "eventHttpRequest", "transactionHttpRequest",
		"sentAtNow", "isSampled", "disabledClient", "normalizeEventId", "eventIdOrNew",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.sentry missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.sentry.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.sentry.%s not public", name)
		}
	}
}

func TestSentryModulePinsEnvelopeAndTraceBehavior(t *testing.T) {
	reg := LoadCached()
	mod := requireSentryModule(t, reg)
	src := string(mod.Source)
	for _, want := range []string{
		`"{self.baseUrl()}/api/{self.projectId}/envelope/"`,
		`out.insert("Content-Type", "application/x-sentry-envelope")`,
		`out.insert("X-Sentry-Auth", authHeader(client))`,
		`"Sentry {strings.join(parts, sep)}"`,
		`prop("dsn", quote(client.dsn.raw))`,
		`prop("type", quote(itemType))`,
		`prop("length", payload.bytes().len().toString())`,
		`prop("type", quote("transaction"))`,
		`prop("measurements", measurementsJson(event.metrics))`,
		`out.insert("sentry-trace", sentryTraceHeader(trace))`,
		`observability.attrsJson(mergedScope.extra)`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.sentry source missing %q", want)
		}
	}
}

func requireSentryModule(t *testing.T, reg *Registry) *Module {
	t.Helper()
	mod := reg.Modules["sentry"]
	if mod == nil || mod.Package == nil || mod.Package.PkgScope == nil {
		var details []string
		for _, d := range reg.Diags {
			if d != nil && (strings.Contains(d.File, "sentry") || strings.Contains(d.Message, "sentry")) {
				details = append(details, d.Error())
			}
		}
		t.Fatalf("std.sentry not fully loaded; diagnostics=%v", details)
	}
	return mod
}
