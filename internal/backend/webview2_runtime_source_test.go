package backend

import (
	"os"
	"strings"
	"testing"
)

func TestWebView2RuntimeUsesVirtualLocalOrigin(t *testing.T) {
	src := readWebView2RuntimeSource(t)
	for _, want := range []string{
		`constexpr wchar_t kOstyLocalHost[] = L"osty.local"`,
		"EntryTarget entry_target_for_entry",
		"(s[2] == '\\\\' || s[2] == '/')",
		`std::wstring(L"https://") + kOstyLocalHost`,
		"SetVirtualHostNameToFolderMapping",
		"COREWEBVIEW2_HOST_RESOURCE_ACCESS_KIND_DENY_CORS",
		"navigation blocked: local paths must stay under the WebView2 app asset root",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("WebView2 runtime source missing %q", want)
		}
	}
}

func TestWebView2RuntimeHardensBridgeAndLifetime(t *testing.T) {
	src := readWebView2RuntimeSource(t)
	for _, want := range []string{
		"Object.freeze(api)",
		"queueMicrotask(() => handler(lastState))",
		"typeof name !== 'string'",
		"w->controller.Reset()",
		"w->webview.Reset()",
		"g_windows.erase(id)",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("WebView2 runtime source missing %q", want)
		}
	}
}

func readWebView2RuntimeSource(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("runtime/webview2/osty_webview2_win32.cpp")
	if err != nil {
		t.Fatalf("read WebView2 runtime source: %v", err)
	}
	return string(raw)
}
