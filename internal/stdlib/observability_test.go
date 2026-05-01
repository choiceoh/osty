package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/resolve"
)

func TestObservabilityModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := requireObservabilityModule(t, reg)

	for _, tc := range []struct {
		name string
		kind resolve.SymbolKind
	}{
		{"Severity", resolve.SymEnum},
		{"SignalKind", resolve.SymEnum},
		{"SpanStatus", resolve.SymEnum},
		{"Resource", resolve.SymStruct},
		{"TraceContext", resolve.SymStruct},
		{"Span", resolve.SymStruct},
		{"Breadcrumb", resolve.SymStruct},
		{"Metric", resolve.SymStruct},
		{"Event", resolve.SymStruct},
	} {
		sym := mod.Package.PkgScope.LookupLocal(tc.name)
		if sym == nil {
			t.Fatalf("std.observability missing type %q", tc.name)
		}
		if sym.Kind != tc.kind {
			t.Fatalf("std.observability.%s kind = %s, want %s", tc.name, sym.Kind, tc.kind)
		}
		if !sym.Pub {
			t.Fatalf("std.observability.%s not public", tc.name)
		}
	}

	for _, name := range []string{
		"resource", "defaultResource", "attributes", "tags",
		"attrString", "attrInt", "attrFloat", "attrBool",
		"traceContext", "childTrace", "traceparent", "baggage", "traceHeaders",
		"startSpan", "finishSpan", "breadcrumb", "metric", "metricFromSample", "counterMetrics",
		"messageEvent", "errorEvent", "spanEvent", "metricEvent",
		"logRecord", "eventJson", "spanJson", "traceJson", "attrsJson", "tagsJson",
	} {
		sym := mod.Package.PkgScope.LookupLocal(name)
		if sym == nil {
			t.Fatalf("std.observability missing export %q", name)
		}
		if sym.Kind != resolve.SymFn {
			t.Fatalf("std.observability.%s kind = %s, want fn", name, sym.Kind)
		}
		if !sym.Pub {
			t.Fatalf("std.observability.%s not public", name)
		}
	}
}

func TestObservabilityModulePinsOperationalBehavior(t *testing.T) {
	reg := LoadCached()
	mod := requireObservabilityModule(t, reg)
	src := string(mod.Source)
	for _, want := range []string{
		`"00-{strings.trimSpace(trace.traceId)}-{strings.trimSpace(trace.spanId)}-{sampled}"`,
		`out.insert("traceparent", traceparent(trace))`,
		`out.insert("baggage", bag)`,
		`metrics.snapshot(counter)`,
		`redact.redact(event.message)`,
		`log.Record`,
		`prop("duration_ms", span.durationMs.toString())`,
		`prop("error_chain", stringArrayJson(redactList(event.errorChain)))`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.observability source missing %q", want)
		}
	}
}

func requireObservabilityModule(t *testing.T, reg *Registry) *Module {
	t.Helper()
	mod := reg.Modules["observability"]
	if mod == nil || mod.Package == nil || mod.Package.PkgScope == nil {
		var details []string
		for _, d := range reg.Diags {
			if d != nil && (strings.Contains(d.File, "observability") || strings.Contains(d.Message, "observability")) {
				details = append(details, d.Error())
			}
		}
		t.Fatalf("std.observability not fully loaded; diagnostics=%v", details)
	}
	return mod
}
