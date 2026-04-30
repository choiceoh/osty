package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckCLIHandlesStdUrlAndCharImports(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	source := `use std.char as ascii
use std.url as urls

fn main() {
    let _ = urls
    let _ = ascii
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	got := runOstyCLI(t, "check", "--no-airepair", path)
	if got.exit != 0 {
		t.Fatalf("osty check exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "code generation is not implemented yet") {
		t.Fatalf("stderr = %q, did not want backend-lowering failure during check", got.stderr)
	}
}

func TestCheckCLIHandlesStdGuiHtmlRenderer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.osty")
	source := `use std.gui as gui
use std.strings as strings

fn main() {
    let content = gui.column("content", [
        gui.label("title", "Dashboard"),
        gui.textInput("name", "Osty", "Name"),
        gui.checkbox("ready", "Ready", true),
        gui.button("save", "Save"),
    ], 8.0)
    let app = gui.root("app", [content])
    let viewport = gui.size(320.0, 240.0)
    let html = gui.browserHtmlDocument("Demo", app, viewport)
    let svg = gui.renderSvg(app, viewport)
    let event = gui.browserEventTarget("\{\"kind\":\"input\",\"id\":\"name\",\"value\":\"Ada\",\"checked\":false,\"x\":4,\"y\":5,\"button\":0,\"timestampMs\":7\}")
    let launcher = gui.macBrowserLauncher()
    let plan = gui.browserLaunchPlan("/tmp/osty-gui-demo.html", launcher)
    let written = gui.writeBrowserDocument("/tmp/osty-gui-demo.html", "Demo", app, viewport)
    let opened = gui.openBrowserDocument("/tmp/osty-gui-demo.html", gui.commandBrowserLauncher("open"))
    let launched = gui.launchBrowserDocument("/tmp/osty-gui-demo.html", "Demo", app, viewport, launcher)
    let _ = strings.contains(html, "data-gui-id")
    let _ = strings.contains(html, "osty-gui-event")
    let _ = strings.contains(svg, "<svg")
    let _ = event
    let _ = plan
    let _ = written
    let _ = opened
    let _ = launched
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatalf("write source: %v", err)
	}

	got := runOstyCLI(t, "check", "--no-airepair", path)
	if got.exit != 0 {
		t.Fatalf("osty check exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", got.exit, got.stdout, got.stderr)
	}
	if strings.Contains(got.stderr, "code generation is not implemented yet") {
		t.Fatalf("stderr = %q, did not want backend-lowering failure during check", got.stderr)
	}
}
