# WebView2 Runtime Shim

`std.gui.webview2` calls this shim through the `osty_webview2` C ABI. The
portable `osty_webview2_stub.c` keeps non-Windows builds linkable while
returning clear runtime diagnostics. The Windows implementation lives in
`osty_webview2_win32.cpp` and owns the Win32 window, WebView2 controller,
bridge injection, navigation allow-listing, and event queue.

Example Windows build shape:

```powershell
cl /std:c++17 /EHsc /LD osty_webview2_win32.cpp /I path\to\WebView2\include `
  WebView2LoaderStatic.lib user32.lib ole32.lib /Fe:osty_webview2.dll
```

Generated GUI projects include:

```toml
[target.amd64-windows]
cgo = true
link = ["osty_webview2", "WebView2Loader", "user32", "ole32"]
```

Keep the exported names in `osty_webview2.h` synchronized with
`internal/stdlib/modules/gui/webview2.osty`.
