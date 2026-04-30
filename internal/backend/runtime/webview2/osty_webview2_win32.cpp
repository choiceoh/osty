#if defined(_WIN32)

#include "osty_webview2.h"

#include <WebView2.h>
#include <windows.h>
#include <wrl.h>

#include <algorithm>
#include <cctype>
#include <cstdio>
#include <cstdint>
#include <filesystem>
#include <memory>
#include <mutex>
#include <string>
#include <system_error>
#include <unordered_map>
#include <utility>
#include <vector>

using Microsoft::WRL::Callback;
using Microsoft::WRL::ComPtr;

namespace {

constexpr uintptr_t kOstySSOTag = (uintptr_t{1} << 63);
constexpr uintptr_t kOstySSOLenShift = 56;
constexpr wchar_t kOstyLocalHost[] = L"osty.local";

thread_local bool t_failed = false;
thread_local std::string t_last_error;
thread_local std::string t_return;

struct Event {
    std::string name;
    std::string payload;
};

struct EntryTarget {
    std::wstring uri;
    std::wstring virtual_root;
};

struct App;

struct Window {
    int64_t id = 0;
    App *app = nullptr;
    HWND hwnd = nullptr;
    ComPtr<ICoreWebView2Controller> controller;
    ComPtr<ICoreWebView2> webview;
    std::wstring virtual_root;
    bool closed = false;
};

struct App {
    int64_t id = 0;
    std::wstring name;
    bool quit = false;
    std::vector<Event> events;
};

std::mutex g_mu;
int64_t g_next_app = 1;
int64_t g_next_window = 1;
std::unordered_map<int64_t, std::unique_ptr<App>> g_apps;
std::unordered_map<int64_t, std::unique_ptr<Window>> g_windows;
ATOM g_window_class = 0;

void clear_error() {
    t_failed = false;
    t_last_error.clear();
}

bool fail(const std::string &message) {
    t_failed = true;
    t_last_error = message;
    return false;
}

bool fail_hr(const char *context, HRESULT hr) {
    char buf[160];
    snprintf(buf, sizeof(buf), "%s failed: HRESULT 0x%08lx", context, static_cast<unsigned long>(hr));
    return fail(buf);
}

std::string osty_string_to_utf8(const char *value) {
    if (value == nullptr) {
        return "";
    }
    uintptr_t raw = reinterpret_cast<uintptr_t>(value);
    if ((raw & kOstySSOTag) != 0) {
        size_t len = (raw >> kOstySSOLenShift) & 0x7;
        std::string out;
        out.reserve(len);
        for (size_t i = 0; i < len; i++) {
            out.push_back(static_cast<char>((raw >> (i * 8)) & 0xff));
        }
        return out;
    }
    return std::string(value);
}

std::wstring utf8_to_wide(const std::string &s) {
    if (s.empty()) {
        return L"";
    }
    int needed = MultiByteToWideChar(CP_UTF8, MB_ERR_INVALID_CHARS, s.data(), static_cast<int>(s.size()), nullptr, 0);
    if (needed <= 0) {
        needed = MultiByteToWideChar(CP_UTF8, 0, s.data(), static_cast<int>(s.size()), nullptr, 0);
    }
    std::wstring out(static_cast<size_t>(needed), L'\0');
    MultiByteToWideChar(CP_UTF8, 0, s.data(), static_cast<int>(s.size()), out.data(), needed);
    return out;
}

std::string wide_to_utf8(const std::wstring &s) {
    if (s.empty()) {
        return "";
    }
    int needed = WideCharToMultiByte(CP_UTF8, 0, s.data(), static_cast<int>(s.size()), nullptr, 0, nullptr, nullptr);
    std::string out(static_cast<size_t>(needed), '\0');
    WideCharToMultiByte(CP_UTF8, 0, s.data(), static_cast<int>(s.size()), out.data(), needed, nullptr, nullptr);
    return out;
}

bool has_uri_scheme(const std::string &s) {
    auto pos = s.find(':');
    if (pos == std::string::npos || pos == 0) {
        return false;
    }
    if (pos == 1 && std::isalpha(static_cast<unsigned char>(s[0])) && s.size() > 2 && (s[2] == '\\' || s[2] == '/')) {
        return false;
    }
    for (size_t i = 0; i < pos; i++) {
        unsigned char c = static_cast<unsigned char>(s[i]);
        if (!std::isalnum(c) && c != '+' && c != '-' && c != '.') {
            return false;
        }
    }
    return true;
}

bool is_url_path_char(wchar_t c) {
    return (c >= L'a' && c <= L'z') ||
           (c >= L'A' && c <= L'Z') ||
           (c >= L'0' && c <= L'9') ||
           c == L'/' ||
           c == L'-' ||
           c == L'_' ||
           c == L'.' ||
           c == L'~';
}

std::wstring url_path_escape(std::wstring path) {
    const wchar_t *hex = L"0123456789ABCDEF";
    std::replace(path.begin(), path.end(), L'\\', L'/');
    std::wstring out;
    for (wchar_t c : path) {
        if (is_url_path_char(c)) {
            out.push_back(c);
            continue;
        }
        std::string utf8 = wide_to_utf8(std::wstring(1, c));
        for (unsigned char b : utf8) {
            out.push_back(L'%');
            out.push_back(hex[(b >> 4) & 0xf]);
            out.push_back(hex[b & 0xf]);
        }
    }
    return out;
}

bool path_is_within(const std::filesystem::path &root, const std::filesystem::path &child) {
    std::error_code ec;
    std::filesystem::path rel = std::filesystem::relative(child, root, ec);
    if (ec || rel.is_absolute()) {
        return false;
    }
    for (const auto &part : rel) {
        if (part == std::filesystem::path(L"..")) {
            return false;
        }
    }
    return true;
}

EntryTarget entry_target_for_entry(const char *entry) {
    std::string raw = osty_string_to_utf8(entry);
    if (has_uri_scheme(raw)) {
        return EntryTarget{utf8_to_wide(raw), L""};
    }
    if (raw.empty()) {
        raw = "ui/index.html";
    }

    std::error_code ec;
    std::filesystem::path root = std::filesystem::current_path(ec);
    if (ec) {
        root = std::filesystem::path(L".");
    }
    root = std::filesystem::absolute(root, ec).lexically_normal();
    if (ec) {
        root = std::filesystem::path(L".");
    }

    std::filesystem::path requested = std::filesystem::path(utf8_to_wide(raw));
    if (requested.is_relative()) {
        requested = root / requested;
    }
    requested = requested.lexically_normal();

    std::filesystem::path rel;
    if (path_is_within(root, requested)) {
        rel = std::filesystem::relative(requested, root, ec);
        if (ec) {
            rel = requested.filename();
            root = requested.parent_path();
        }
    } else {
        root = requested.parent_path();
        rel = requested.filename();
    }

    std::wstring url_path = url_path_escape(rel.generic_wstring());
    if (url_path.empty() || url_path[0] != L'/') {
        url_path = L"/" + url_path;
    }
    return EntryTarget{std::wstring(L"https://") + kOstyLocalHost + url_path, root.wstring()};
}

bool allowed_uri(const std::wstring &uri) {
    return uri.rfind(L"file:///", 0) == 0 ||
           uri.rfind(L"file://", 0) == 0 ||
           uri.rfind(L"https://osty.local/", 0) == 0;
}

std::wstring bridge_script() {
    return LR"JS((() => {
  if (window.osty && window.osty.__ostyWebView2Bridge) return;
  const stateHandlers = new Set();
  let lastState;
  let hasState = false;
  const post = (message) => {
    if (!window.chrome || !window.chrome.webview) return false;
    window.chrome.webview.postMessage(JSON.stringify(message));
    return true;
  };
  const consoleArg = (value) => {
    if (typeof value === 'string') return value;
    try {
      const encoded = JSON.stringify(value);
      return encoded === undefined ? String(value) : encoded;
    } catch (_) {
      return String(value);
    }
  };
  const emitConsole = (level, args) => {
    const rendered = Array.from(args, consoleArg);
    post({ type: 'event', name: `console.${level}`, payload: { level, message: rendered.join(' '), args: rendered } });
  };
  const api = {
    __ostyWebView2Bridge: true,
    platform: 'windows-webview2',
    version: '0.1',
    emit(name, payload = {}) {
      if (typeof name !== 'string' || name.length === 0) return false;
      return post({ type: 'event', name, payload });
    },
    onState(handler) {
      stateHandlers.add(handler);
      if (hasState) queueMicrotask(() => handler(lastState));
      return () => stateHandlers.delete(handler);
    }
  };
  Object.defineProperty(window, 'osty', { value: Object.freeze(api), configurable: false, writable: false });
  for (const level of ['debug', 'log', 'info', 'warn', 'error']) {
    const original = console[level] && console[level].bind(console);
    if (!original) continue;
    try {
      console[level] = (...args) => {
        original(...args);
        emitConsole(level, args);
      };
    } catch (_) {}
  }
  if (window.chrome && window.chrome.webview) {
    window.chrome.webview.addEventListener('message', (event) => {
      if (!event.data || event.data.type !== 'state') return;
      hasState = true;
      lastState = event.data.payload;
      for (const handler of stateHandlers) handler(event.data.payload);
    });
  }
})();)JS";
}

std::string json_unescape(std::string s) {
    std::string out;
    out.reserve(s.size());
    for (size_t i = 0; i < s.size(); i++) {
        if (s[i] != '\\' || i + 1 >= s.size()) {
            out.push_back(s[i]);
            continue;
        }
        char n = s[++i];
        switch (n) {
        case '"': out.push_back('"'); break;
        case '\\': out.push_back('\\'); break;
        case '/': out.push_back('/'); break;
        case 'b': out.push_back('\b'); break;
        case 'f': out.push_back('\f'); break;
        case 'n': out.push_back('\n'); break;
        case 'r': out.push_back('\r'); break;
        case 't': out.push_back('\t'); break;
        default:
            out.push_back('\\');
            out.push_back(n);
            break;
        }
    }
    return out;
}

bool json_string_field(const std::string &json, const std::string &key, std::string *out) {
    std::string needle = "\"" + key + "\"";
    size_t pos = json.find(needle);
    if (pos == std::string::npos) {
        return false;
    }
    pos = json.find(':', pos + needle.size());
    if (pos == std::string::npos) {
        return false;
    }
    pos++;
    while (pos < json.size() && std::isspace(static_cast<unsigned char>(json[pos]))) {
        pos++;
    }
    if (pos >= json.size() || json[pos] != '"') {
        return false;
    }
    pos++;
    std::string value;
    bool esc = false;
    for (; pos < json.size(); pos++) {
        char c = json[pos];
        if (esc) {
            value.push_back('\\');
            value.push_back(c);
            esc = false;
            continue;
        }
        if (c == '\\') {
            esc = true;
            continue;
        }
        if (c == '"') {
            *out = json_unescape(value);
            return true;
        }
        value.push_back(c);
    }
    return false;
}

bool json_value_field(const std::string &json, const std::string &key, std::string *out) {
    std::string needle = "\"" + key + "\"";
    size_t pos = json.find(needle);
    if (pos == std::string::npos) {
        return false;
    }
    pos = json.find(':', pos + needle.size());
    if (pos == std::string::npos) {
        return false;
    }
    pos++;
    while (pos < json.size() && std::isspace(static_cast<unsigned char>(json[pos]))) {
        pos++;
    }
    size_t start = pos;
    int depth = 0;
    bool in_string = false;
    bool esc = false;
    for (; pos < json.size(); pos++) {
        char c = json[pos];
        if (in_string) {
            if (esc) {
                esc = false;
            } else if (c == '\\') {
                esc = true;
            } else if (c == '"') {
                in_string = false;
            }
            continue;
        }
        if (c == '"') {
            in_string = true;
            continue;
        }
        if (c == '{' || c == '[') {
            depth++;
            continue;
        }
        if (c == '}' || c == ']') {
            if (depth == 0) {
                break;
            }
            depth--;
            continue;
        }
        if (depth == 0 && c == ',') {
            break;
        }
    }
    size_t end = pos;
    while (end > start && std::isspace(static_cast<unsigned char>(json[end - 1]))) {
        end--;
    }
    *out = json.substr(start, end - start);
    if (out->empty()) {
        *out = "{}";
    }
    return true;
}

void pump_messages_until(bool *done) {
    MSG msg;
    while (!*done) {
        while (PeekMessageW(&msg, nullptr, 0, 0, PM_REMOVE)) {
            TranslateMessage(&msg);
            DispatchMessageW(&msg);
        }
        if (!*done) {
            MsgWaitForMultipleObjects(0, nullptr, FALSE, 10, QS_ALLINPUT);
        }
    }
}

App *lookup_app(int64_t id) {
    auto it = g_apps.find(id);
    return it == g_apps.end() ? nullptr : it->second.get();
}

Window *lookup_window(int64_t id) {
    auto it = g_windows.find(id);
    return it == g_windows.end() ? nullptr : it->second.get();
}

void resize_controller(Window *w) {
    if (w == nullptr || !w->controller) {
        return;
    }
    RECT bounds;
    GetClientRect(w->hwnd, &bounds);
    w->controller->put_Bounds(bounds);
}

LRESULT CALLBACK wndproc(HWND hwnd, UINT msg, WPARAM wp, LPARAM lp) {
    Window *w = reinterpret_cast<Window *>(GetWindowLongPtrW(hwnd, GWLP_USERDATA));
    if (msg == WM_NCCREATE) {
        auto *cs = reinterpret_cast<CREATESTRUCTW *>(lp);
        w = reinterpret_cast<Window *>(cs->lpCreateParams);
        SetWindowLongPtrW(hwnd, GWLP_USERDATA, reinterpret_cast<LONG_PTR>(w));
    }
    switch (msg) {
    case WM_SIZE:
        resize_controller(w);
        return 0;
    case WM_CLOSE:
        if (w != nullptr) {
            w->closed = true;
            if (w->app != nullptr) {
                w->app->quit = true;
            }
        }
        DestroyWindow(hwnd);
        return 0;
    case WM_DESTROY:
        if (w != nullptr && w->hwnd == hwnd) {
            w->hwnd = nullptr;
            w->controller.Reset();
            w->webview.Reset();
        }
        return 0;
    default:
        return DefWindowProcW(hwnd, msg, wp, lp);
    }
}

bool ensure_window_class() {
    if (g_window_class != 0) {
        return true;
    }
    WNDCLASSW wc = {};
    wc.lpfnWndProc = wndproc;
    wc.hInstance = GetModuleHandleW(nullptr);
    wc.lpszClassName = L"OstyWebView2Window";
    wc.hCursor = LoadCursorW(nullptr, IDC_ARROW);
    g_window_class = RegisterClassW(&wc);
    if (g_window_class == 0) {
        return fail("RegisterClassW failed");
    }
    return true;
}

bool init_webview(Window *w, const EntryTarget &target) {
    bool done = false;
    bool ok = false;
    HRESULT hr = CreateCoreWebView2EnvironmentWithOptions(
        nullptr,
        nullptr,
        nullptr,
        Callback<ICoreWebView2CreateCoreWebView2EnvironmentCompletedHandler>(
            [w, &done, &ok, target](HRESULT result, ICoreWebView2Environment *env) -> HRESULT {
                if (FAILED(result) || env == nullptr) {
                    fail_hr("CreateCoreWebView2EnvironmentWithOptions", result);
                    done = true;
                    return S_OK;
                }
                env->CreateCoreWebView2Controller(
                    w->hwnd,
                    Callback<ICoreWebView2CreateCoreWebView2ControllerCompletedHandler>(
                        [w, &done, &ok, target](HRESULT result2, ICoreWebView2Controller *controller) -> HRESULT {
                            if (FAILED(result2) || controller == nullptr) {
                                fail_hr("CreateCoreWebView2Controller", result2);
                                done = true;
                                return S_OK;
                            }
                            w->controller = controller;
                            HRESULT hr2 = controller->get_CoreWebView2(&w->webview);
                            if (FAILED(hr2) || !w->webview) {
                                fail_hr("get_CoreWebView2", hr2);
                                done = true;
                                return S_OK;
                            }
                            resize_controller(w);
                            if (!target.virtual_root.empty()) {
                                ComPtr<ICoreWebView2_3> webview3;
                                HRESULT map_query = w->webview.As(&webview3);
                                if (FAILED(map_query) || !webview3) {
                                    fail_hr("ICoreWebView2_3", map_query);
                                    done = true;
                                    return S_OK;
                                }
                                HRESULT map_hr = webview3->SetVirtualHostNameToFolderMapping(
                                    kOstyLocalHost,
                                    target.virtual_root.c_str(),
                                    COREWEBVIEW2_HOST_RESOURCE_ACCESS_KIND_DENY_CORS);
                                if (FAILED(map_hr)) {
                                    fail_hr("SetVirtualHostNameToFolderMapping", map_hr);
                                    done = true;
                                    return S_OK;
                                }
                            }
                            w->webview->AddScriptToExecuteOnDocumentCreated(bridge_script().c_str(), nullptr);
                            EventRegistrationToken token{};
                            w->webview->add_WebMessageReceived(
                                Callback<ICoreWebView2WebMessageReceivedEventHandler>(
                                    [w](ICoreWebView2 *, ICoreWebView2WebMessageReceivedEventArgs *args) -> HRESULT {
                                        LPWSTR raw = nullptr;
                                        std::string json;
                                        if (SUCCEEDED(args->TryGetWebMessageAsString(&raw)) && raw != nullptr) {
                                            json = wide_to_utf8(raw);
                                            CoTaskMemFree(raw);
                                        } else if (SUCCEEDED(args->get_WebMessageAsJson(&raw)) && raw != nullptr) {
                                            json = wide_to_utf8(raw);
                                            CoTaskMemFree(raw);
                                        }
                                        std::string type;
                                        std::string name;
                                        std::string payload;
                                        if (!json_string_field(json, "type", &type) || type != "event") {
                                            return S_OK;
                                        }
                                        if (!json_string_field(json, "name", &name)) {
                                            name = "";
                                        }
                                        if (!json_value_field(json, "payload", &payload)) {
                                            payload = "{}";
                                        }
                                        if (w->app != nullptr) {
                                            w->app->events.push_back(Event{name, payload});
                                        }
                                        return S_OK;
                                    })
                                    .Get(),
                                &token);
                            w->webview->add_NavigationStarting(
                                Callback<ICoreWebView2NavigationStartingEventHandler>(
                                    [](ICoreWebView2 *, ICoreWebView2NavigationStartingEventArgs *args) -> HRESULT {
                                        LPWSTR uri = nullptr;
                                        if (SUCCEEDED(args->get_Uri(&uri)) && uri != nullptr) {
                                            std::wstring value(uri);
                                            CoTaskMemFree(uri);
                                            if (!allowed_uri(value)) {
                                                args->put_Cancel(TRUE);
                                            }
                                        }
                                        return S_OK;
                                    })
                                    .Get(),
                                &token);
                            w->webview->Navigate(target.uri.c_str());
                            ok = true;
                            clear_error();
                            done = true;
                            return S_OK;
                        })
                        .Get());
                return S_OK;
            })
            .Get());
    if (FAILED(hr)) {
        return fail_hr("CreateCoreWebView2EnvironmentWithOptions", hr);
    }
    pump_messages_until(&done);
    return ok;
}

} // namespace

extern "C" {

int osty_wv2_failed(void) {
    return t_failed ? 1 : 0;
}

const char *osty_wv2_last_error(void) {
    return t_last_error.empty() ? "" : t_last_error.c_str();
}

int64_t osty_wv2_app_new(const char *name) {
    HRESULT hr = CoInitializeEx(nullptr, COINIT_APARTMENTTHREADED);
    if (FAILED(hr) && hr != RPC_E_CHANGED_MODE) {
        fail_hr("CoInitializeEx", hr);
        return 0;
    }
    if (!ensure_window_class()) {
        return 0;
    }
    auto app = std::make_unique<App>();
    app->name = utf8_to_wide(osty_string_to_utf8(name));
    std::lock_guard<std::mutex> lock(g_mu);
    app->id = g_next_app++;
    int64_t id = app->id;
    g_apps[id] = std::move(app);
    clear_error();
    return id;
}

void osty_wv2_app_free(int64_t app) {
    std::lock_guard<std::mutex> lock(g_mu);
    App *a = lookup_app(app);
    if (a != nullptr) {
        std::vector<int64_t> windows;
        for (const auto &it : g_windows) {
            if (it.second && it.second->app == a) {
                windows.push_back(it.first);
            }
        }
        for (int64_t id : windows) {
            Window *w = lookup_window(id);
            if (w != nullptr) {
                HWND hwnd = w->hwnd;
                w->hwnd = nullptr;
                w->controller.Reset();
                w->webview.Reset();
                w->closed = true;
                if (hwnd != nullptr) {
                    DestroyWindow(hwnd);
                }
            }
            g_windows.erase(id);
        }
    }
    g_apps.erase(app);
    clear_error();
}

int64_t osty_wv2_app_run(int64_t app) {
    App *a = nullptr;
    {
        std::lock_guard<std::mutex> lock(g_mu);
        a = lookup_app(app);
    }
    if (a == nullptr) {
        fail("invalid WebView2 app handle");
        return -1;
    }
    while (!a->quit && osty_wv2_app_poll(app)) {
        MsgWaitForMultipleObjects(0, nullptr, FALSE, 16, QS_ALLINPUT);
    }
    clear_error();
    return 0;
}

int osty_wv2_app_poll(int64_t app) {
    App *a = nullptr;
    {
        std::lock_guard<std::mutex> lock(g_mu);
        a = lookup_app(app);
    }
    if (a == nullptr) {
        fail("invalid WebView2 app handle");
        return 0;
    }
    MSG msg;
    while (PeekMessageW(&msg, nullptr, 0, 0, PM_REMOVE)) {
        if (msg.message == WM_QUIT) {
            a->quit = true;
        }
        TranslateMessage(&msg);
        DispatchMessageW(&msg);
    }
    clear_error();
    return a->quit ? 0 : 1;
}

void osty_wv2_app_quit(int64_t app) {
    std::lock_guard<std::mutex> lock(g_mu);
    if (App *a = lookup_app(app)) {
        a->quit = true;
    }
    clear_error();
}

int64_t osty_wv2_window_new(int64_t app, const char *title, int64_t width, int64_t height, const char *entry) {
    App *a = nullptr;
    {
        std::lock_guard<std::mutex> lock(g_mu);
        a = lookup_app(app);
    }
    if (a == nullptr) {
        fail("invalid WebView2 app handle");
        return 0;
    }
    auto w = std::make_unique<Window>();
    w->app = a;
    std::wstring wide_title = utf8_to_wide(osty_string_to_utf8(title));
    if (wide_title.empty()) {
        wide_title = a->name.empty() ? L"Osty" : a->name;
    }
    if (width <= 0) {
        width = 1100;
    }
    if (height <= 0) {
        height = 720;
    }
    HWND hwnd = CreateWindowExW(
        0,
        L"OstyWebView2Window",
        wide_title.c_str(),
        WS_OVERLAPPEDWINDOW,
        CW_USEDEFAULT,
        CW_USEDEFAULT,
        static_cast<int>(width),
        static_cast<int>(height),
        nullptr,
        nullptr,
        GetModuleHandleW(nullptr),
        w.get());
    if (hwnd == nullptr) {
        fail("CreateWindowExW failed");
        return 0;
    }
    w->hwnd = hwnd;
    EntryTarget target = entry_target_for_entry(entry);
    if (!allowed_uri(target.uri)) {
        DestroyWindow(hwnd);
        fail("navigation blocked: only local app assets and https://osty.local/ are allowed");
        return 0;
    }
    w->virtual_root = target.virtual_root;
    if (!init_webview(w.get(), target)) {
        DestroyWindow(hwnd);
        return 0;
    }
    std::lock_guard<std::mutex> lock(g_mu);
    w->id = g_next_window++;
    int64_t id = w->id;
    g_windows[id] = std::move(w);
    clear_error();
    return id;
}

int osty_wv2_window_show(int64_t window) {
    std::lock_guard<std::mutex> lock(g_mu);
    Window *w = lookup_window(window);
    if (w == nullptr || w->hwnd == nullptr || w->closed) {
        fail("invalid WebView2 window handle");
        return 0;
    }
    ShowWindow(w->hwnd, SW_SHOW);
    UpdateWindow(w->hwnd);
    clear_error();
    return 1;
}

void osty_wv2_window_close(int64_t window) {
    std::lock_guard<std::mutex> lock(g_mu);
    Window *w = lookup_window(window);
    if (w != nullptr) {
        HWND hwnd = w->hwnd;
        w->hwnd = nullptr;
        w->controller.Reset();
        w->webview.Reset();
        w->closed = true;
        if (hwnd != nullptr) {
            DestroyWindow(hwnd);
        }
    }
    g_windows.erase(window);
    clear_error();
}

int osty_wv2_window_set_title(int64_t window, const char *title) {
    std::lock_guard<std::mutex> lock(g_mu);
    Window *w = lookup_window(window);
    if (w == nullptr || w->hwnd == nullptr || w->closed) {
        fail("invalid WebView2 window handle");
        return 0;
    }
    SetWindowTextW(w->hwnd, utf8_to_wide(osty_string_to_utf8(title)).c_str());
    clear_error();
    return 1;
}

int osty_wv2_window_navigate(int64_t window, const char *url) {
    Window *w = nullptr;
    {
        std::lock_guard<std::mutex> lock(g_mu);
        w = lookup_window(window);
    }
    if (w == nullptr || !w->webview) {
        fail("invalid WebView2 window handle");
        return 0;
    }
    EntryTarget target = entry_target_for_entry(url);
    if (!allowed_uri(target.uri)) {
        fail("navigation blocked: only local app assets and https://osty.local/ are allowed");
        return 0;
    }
    if (!target.virtual_root.empty() && target.virtual_root != w->virtual_root) {
        fail("navigation blocked: local paths must stay under the WebView2 app asset root");
        return 0;
    }
    HRESULT hr = w->webview->Navigate(target.uri.c_str());
    if (FAILED(hr)) {
        fail_hr("CoreWebView2.Navigate", hr);
        return 0;
    }
    clear_error();
    return 1;
}

int osty_wv2_window_post_state_json(int64_t window, const char *state) {
    Window *w = nullptr;
    {
        std::lock_guard<std::mutex> lock(g_mu);
        w = lookup_window(window);
    }
    if (w == nullptr || !w->webview) {
        fail("invalid WebView2 window handle");
        return 0;
    }
    std::string raw = osty_string_to_utf8(state);
    if (raw.empty()) {
        raw = "null";
    }
    std::wstring msg = utf8_to_wide(std::string("{\"type\":\"state\",\"payload\":") + raw + "}");
    HRESULT hr = w->webview->PostWebMessageAsJson(msg.c_str());
    if (FAILED(hr)) {
        fail_hr("CoreWebView2.PostWebMessageAsJson", hr);
        return 0;
    }
    clear_error();
    return 1;
}

const char *osty_wv2_window_eval(int64_t window, const char *js) {
    Window *w = nullptr;
    {
        std::lock_guard<std::mutex> lock(g_mu);
        w = lookup_window(window);
    }
    if (w == nullptr || !w->webview) {
        fail("invalid WebView2 window handle");
        return "";
    }
    bool done = false;
    HRESULT hr = w->webview->ExecuteScript(
        utf8_to_wide(osty_string_to_utf8(js)).c_str(),
        Callback<ICoreWebView2ExecuteScriptCompletedHandler>(
            [&done](HRESULT result, LPCWSTR value) -> HRESULT {
                if (FAILED(result)) {
                    fail_hr("CoreWebView2.ExecuteScript", result);
                } else {
                    t_return = wide_to_utf8(value == nullptr ? L"" : value);
                    clear_error();
                }
                done = true;
                return S_OK;
            })
            .Get());
    if (FAILED(hr)) {
        fail_hr("CoreWebView2.ExecuteScript", hr);
        return "";
    }
    pump_messages_until(&done);
    return t_return.c_str();
}

int osty_wv2_window_open_devtools(int64_t window) {
    std::lock_guard<std::mutex> lock(g_mu);
    Window *w = lookup_window(window);
    if (w == nullptr || !w->webview) {
        fail("invalid WebView2 window handle");
        return 0;
    }
    HRESULT hr = w->webview->OpenDevToolsWindow();
    if (FAILED(hr)) {
        fail_hr("CoreWebView2.OpenDevToolsWindow", hr);
        return 0;
    }
    clear_error();
    return 1;
}

int64_t osty_wv2_event_count(int64_t app) {
    std::lock_guard<std::mutex> lock(g_mu);
    App *a = lookup_app(app);
    if (a == nullptr) {
        fail("invalid WebView2 app handle");
        return 0;
    }
    clear_error();
    return static_cast<int64_t>(a->events.size());
}

const char *osty_wv2_event_name(int64_t app, int64_t index) {
    std::lock_guard<std::mutex> lock(g_mu);
    App *a = lookup_app(app);
    if (a == nullptr || index < 0 || static_cast<size_t>(index) >= a->events.size()) {
        fail("invalid WebView2 event index");
        return "";
    }
    clear_error();
    return a->events[static_cast<size_t>(index)].name.c_str();
}

const char *osty_wv2_event_payload(int64_t app, int64_t index) {
    std::lock_guard<std::mutex> lock(g_mu);
    App *a = lookup_app(app);
    if (a == nullptr || index < 0 || static_cast<size_t>(index) >= a->events.size()) {
        fail("invalid WebView2 event index");
        return "{}";
    }
    clear_error();
    return a->events[static_cast<size_t>(index)].payload.c_str();
}

void osty_wv2_event_clear(int64_t app) {
    std::lock_guard<std::mutex> lock(g_mu);
    if (App *a = lookup_app(app)) {
        a->events.clear();
    }
    clear_error();
}

int osty_wv2_runtime_available(void) {
    LPWSTR version = nullptr;
    HRESULT hr = GetAvailableCoreWebView2BrowserVersionString(nullptr, &version);
    if (SUCCEEDED(hr) && version != nullptr) {
        CoTaskMemFree(version);
        clear_error();
        return 1;
    }
    fail_hr("GetAvailableCoreWebView2BrowserVersionString", hr);
    return 0;
}

const char *osty_wv2_runtime_version(void) {
    LPWSTR version = nullptr;
    HRESULT hr = GetAvailableCoreWebView2BrowserVersionString(nullptr, &version);
    if (FAILED(hr) || version == nullptr) {
        fail_hr("GetAvailableCoreWebView2BrowserVersionString", hr);
        return "";
    }
    t_return = wide_to_utf8(version);
    CoTaskMemFree(version);
    clear_error();
    return t_return.c_str();
}

} // extern "C"

#endif
