package profile

import (
	"slices"
	"testing"

	"github.com/osty/osty/internal/manifest"
)

func TestBuildConfigPreservesTargetLinkLibraries(t *testing.T) {
	m, err := manifest.Parse([]byte(`[package]
name = "desk"
version = "0.1.0"
edition = "0.5"

[target.amd64-windows]
cgo = true
link = ["osty_webview2", "WebView2Loader", "user32", "ole32"]
`))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	cfg, err := BuildConfig(m)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	target, ok := cfg.Target("amd64-windows")
	if !ok {
		t.Fatal("target amd64-windows not found")
	}
	want := []string{"osty_webview2", "WebView2Loader", "user32", "ole32"}
	if !slices.Equal(target.Link, want) {
		t.Fatalf("target link = %v, want %v", target.Link, want)
	}
}
