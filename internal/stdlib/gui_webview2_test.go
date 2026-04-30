package stdlib

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

func TestGuiWebView2ModuleSurface(t *testing.T) {
	reg := LoadCached()
	if reg.Modules["gui.webview2"] == nil {
		t.Fatal("stdlib gui.webview2 module missing")
	}

	for _, name := range []string{
		"app",
		"defaultWindowOptions",
		"runtimeDiagnostic",
		"bridgeScript",
		"lastErrorMessage",
	} {
		fn := reg.LookupFnDecl("gui.webview2", name)
		if fn == nil {
			t.Fatalf("LookupFnDecl(gui.webview2, %s) = nil", name)
		}
		if fn.Body == nil {
			t.Fatalf("gui.webview2.%s body = nil, want safe wrapper body", name)
		}
	}

	for _, tc := range []struct {
		typeName string
		method   string
	}{
		{"App", "window"},
		{"App", "poll"},
		{"App", "events"},
		{"App", "quit"},
		{"App", "run"},
		{"App", "close"},
		{"Window", "show"},
		{"Window", "navigate"},
		{"Window", "setTitle"},
		{"Window", "setState"},
		{"Window", "eval"},
		{"Window", "openDevTools"},
		{"Window", "close"},
	} {
		fn := reg.LookupMethodDecl("gui.webview2", tc.typeName, tc.method)
		if fn == nil {
			t.Fatalf("LookupMethodDecl(gui.webview2, %s, %s) = nil", tc.typeName, tc.method)
		}
		if fn.Body == nil {
			t.Fatalf("gui.webview2.%s.%s body = nil, want wrapper body", tc.typeName, tc.method)
		}
	}
}

func TestGuiWebView2SourcePinsBridgeAndCABI(t *testing.T) {
	src := guiWebView2ModuleSource(t)
	for _, want := range []string{
		`use c "osty_webview2" as wv`,
		"fn osty_wv2_window_post_state_json(window: Int, state: String) -> Bool",
		"fn osty_wv2_event_name(app: Int, index: Int) -> String",
		"fn osty_wv2_runtime_available() -> Bool",
		"pub struct WindowOptions",
		"pub struct Event",
		"pub fn bridgeScript() -> String",
		"__ostyWebView2Bridge: true",
		"chrome.webview.postMessage(JSON.stringify",
		"Object.freeze(api)",
		"queueMicrotask(() => handler(lastState))",
		"typeof name !== 'string'",
		"Object.defineProperty(window, 'osty'",
		"copyString(wv.osty_wv2_event_name",
		`strings.concat(s, "")`,
		"wv.osty_wv2_event_clear(self.handle)",
		`"WebView2 runtime is not available.`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.gui.webview2 source missing %q", want)
		}
	}
}

func TestGuiWebView2ImportResolvesMVPWorkflow(t *testing.T) {
	src := `
use std.gui.webview2 as webview2

pub fn demo() -> Result<(), Error> {
    let app = webview2.app("Inspector")?
    let mut options = webview2.defaultWindowOptions("Osty Inspector", "ui/index.html")
    options.devtools = true
    let window = app.window(options)?
    window.show()?
    window.setState("\{\"status\":\"ready\"\}")?
    for event in app.events() {
        if event.name == "run" {
            window.setState(event.payload)?
        }
    }
    app.quit()
    app.close()
    Ok(())
}
`
	file, parseDiags := parser.ParseDiagnostics([]byte(src))
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	res := resolve.ResolveFileSourceDefault([]byte(src), file, Load())
	for _, d := range res.Diags {
		if d == nil || d.Severity != diag.Error {
			continue
		}
		t.Errorf("resolver rejected std.gui.webview2 fixture: %s: %s", d.Code, d.Message)
	}
}

func guiWebView2ModuleSource(t *testing.T) string {
	t.Helper()
	reg := LoadCached()
	mod := reg.Modules["gui.webview2"]
	if mod == nil {
		t.Fatal("stdlib gui.webview2 module missing")
	}
	return string(mod.Source)
}
