#include "osty_webview2.h"

static const char *osty_wv2_stub_error =
    "std.gui.webview2 is available only when linked with the Windows WebView2 shim";

int osty_wv2_failed(void) {
    return 1;
}

const char *osty_wv2_last_error(void) {
    return osty_wv2_stub_error;
}

int64_t osty_wv2_app_new(const char *name) {
    (void)name;
    return 0;
}

void osty_wv2_app_free(int64_t app) {
    (void)app;
}

int64_t osty_wv2_app_run(int64_t app) {
    (void)app;
    return -1;
}

int osty_wv2_app_poll(int64_t app) {
    (void)app;
    return 0;
}

void osty_wv2_app_quit(int64_t app) {
    (void)app;
}

int64_t osty_wv2_window_new(
    int64_t app,
    const char *title,
    int64_t width,
    int64_t height,
    const char *entry) {
    (void)app;
    (void)title;
    (void)width;
    (void)height;
    (void)entry;
    return 0;
}

int osty_wv2_window_show(int64_t window) {
    (void)window;
    return 0;
}

void osty_wv2_window_close(int64_t window) {
    (void)window;
}

int osty_wv2_window_set_title(int64_t window, const char *title) {
    (void)window;
    (void)title;
    return 0;
}

int osty_wv2_window_navigate(int64_t window, const char *url) {
    (void)window;
    (void)url;
    return 0;
}

int osty_wv2_window_post_state_json(int64_t window, const char *state) {
    (void)window;
    (void)state;
    return 0;
}

const char *osty_wv2_window_eval(int64_t window, const char *js) {
    (void)window;
    (void)js;
    return "";
}

int osty_wv2_window_open_devtools(int64_t window) {
    (void)window;
    return 0;
}

int64_t osty_wv2_event_count(int64_t app) {
    (void)app;
    return 0;
}

const char *osty_wv2_event_name(int64_t app, int64_t index) {
    (void)app;
    (void)index;
    return "";
}

const char *osty_wv2_event_payload(int64_t app, int64_t index) {
    (void)app;
    (void)index;
    return "{}";
}

void osty_wv2_event_clear(int64_t app) {
    (void)app;
}

int osty_wv2_runtime_available(void) {
    return 0;
}

const char *osty_wv2_runtime_version(void) {
    return "";
}
