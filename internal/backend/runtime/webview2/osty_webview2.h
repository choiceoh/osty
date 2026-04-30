#ifndef OSTY_WEBVIEW2_H
#define OSTY_WEBVIEW2_H

#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

#if defined(_WIN32)
#define OSTY_WV2_EXPORT __declspec(dllexport)
#else
#define OSTY_WV2_EXPORT
#endif

OSTY_WV2_EXPORT int osty_wv2_failed(void);
OSTY_WV2_EXPORT const char *osty_wv2_last_error(void);

OSTY_WV2_EXPORT int64_t osty_wv2_app_new(const char *name);
OSTY_WV2_EXPORT void osty_wv2_app_free(int64_t app);
OSTY_WV2_EXPORT int64_t osty_wv2_app_run(int64_t app);
OSTY_WV2_EXPORT int osty_wv2_app_poll(int64_t app);
OSTY_WV2_EXPORT void osty_wv2_app_quit(int64_t app);

OSTY_WV2_EXPORT int64_t osty_wv2_window_new(
    int64_t app,
    const char *title,
    int64_t width,
    int64_t height,
    const char *entry);
OSTY_WV2_EXPORT int osty_wv2_window_show(int64_t window);
OSTY_WV2_EXPORT void osty_wv2_window_close(int64_t window);
OSTY_WV2_EXPORT int osty_wv2_window_set_title(int64_t window, const char *title);
OSTY_WV2_EXPORT int osty_wv2_window_navigate(int64_t window, const char *url);
OSTY_WV2_EXPORT int osty_wv2_window_post_state_json(int64_t window, const char *state);
OSTY_WV2_EXPORT int osty_wv2_window_post_command_json(int64_t window, const char *name, const char *payload);
OSTY_WV2_EXPORT const char *osty_wv2_window_eval(int64_t window, const char *js);
OSTY_WV2_EXPORT int osty_wv2_window_open_devtools(int64_t window);

OSTY_WV2_EXPORT int64_t osty_wv2_event_count(int64_t app);
OSTY_WV2_EXPORT const char *osty_wv2_event_name(int64_t app, int64_t index);
OSTY_WV2_EXPORT const char *osty_wv2_event_payload(int64_t app, int64_t index);
OSTY_WV2_EXPORT void osty_wv2_event_clear(int64_t app);

OSTY_WV2_EXPORT int osty_wv2_runtime_available(void);
OSTY_WV2_EXPORT const char *osty_wv2_runtime_version(void);

#ifdef __cplusplus
}
#endif

#endif
