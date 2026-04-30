package main

import (
	"strings"
	"testing"

	"github.com/osty/osty/internal/scaffold"
)

func TestPickScaffoldKindWebView2GUI(t *testing.T) {
	kind, usageErr := pickScaffoldKind(false, false, false, false, false, "webview2")
	if usageErr != "" {
		t.Fatalf("usageErr = %q, want none", usageErr)
	}
	if kind != scaffold.KindGUIWebView2 {
		t.Fatalf("kind = %v, want KindGUIWebView2", kind)
	}
	if got := kindLabel(kind); got != "WebView2 GUI app project" {
		t.Fatalf("kindLabel = %q", got)
	}
}

func TestPickScaffoldKindQtQuickGUI(t *testing.T) {
	kind, usageErr := pickScaffoldKind(false, false, false, false, false, "qtquick")
	if usageErr != "" {
		t.Fatalf("usageErr = %q, want none", usageErr)
	}
	if kind != scaffold.KindGUIQtQuick {
		t.Fatalf("kind = %v, want KindGUIQtQuick", kind)
	}
	if got := kindLabel(kind); got != "Qt Quick GUI app project" {
		t.Fatalf("kindLabel = %q", got)
	}
}

func TestPickScaffoldKindRejectsUnknownGUIBackend(t *testing.T) {
	_, usageErr := pickScaffoldKind(false, false, false, false, false, "webkit")
	if !strings.Contains(usageErr, "unknown --gui backend") {
		t.Fatalf("usageErr = %q, want unknown --gui backend", usageErr)
	}
}

func TestPickScaffoldKindRejectsGUIWithOtherKind(t *testing.T) {
	_, usageErr := pickScaffoldKind(false, true, false, false, false, "webview2")
	if !strings.Contains(usageErr, "--gui") || !strings.Contains(usageErr, "mutually exclusive") {
		t.Fatalf("usageErr = %q, want mutually exclusive --gui error", usageErr)
	}
}
