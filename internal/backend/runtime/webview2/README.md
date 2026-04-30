# WebView2 Runtime Shim

`std.gui.webview2` calls this shim through the `osty_webview2` C ABI. The
portable `osty_webview2_stub.c` keeps non-Windows builds linkable while
returning clear runtime diagnostics. The Windows implementation lives in
`osty_webview2_win32.cpp` and owns the Win32 window, WebView2 controller,
bridge injection, navigation allow-listing, and event queue.

Local app assets are served through a WebView2 virtual host mapping at
`https://osty.local/...` rather than as raw `file://` navigations. This keeps
the HTML/CSS/JS bundle on one stable local origin, denies cross-origin resource
access by default, and lets the navigation allow-list block external websites
without breaking relative asset loads.

The injected JavaScript bridge also forwards `console.debug/log/info/warn/error`
as normal Osty events named `console.<level>`. App code can inspect those with
`std.gui.webview2.isConsoleEvent` and `consoleEventLevel` while keeping the
original browser console behavior intact.

Osty can send targeted UI commands back to JavaScript with `Window.send`.
Those arrive at `window.osty.onCommand((name, payload) => ...)`, giving apps an
imperative channel for focus, toast, reload, and animation requests without
overloading the state JSON stream.

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
