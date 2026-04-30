#ifndef OSTY_QT_H
#define OSTY_QT_H

#include <stdbool.h>
#include <stdint.h>

#if defined(_WIN32)
#  if defined(OSTY_QT_BUILD_SHARED)
#    define OSTY_QT_API __declspec(dllexport)
#  elif defined(OSTY_QT_USE_SHARED)
#    define OSTY_QT_API __declspec(dllimport)
#  else
#    define OSTY_QT_API
#  endif
#else
#  define OSTY_QT_API __attribute__((visibility("default")))
#endif

#ifdef __cplusplus
extern "C" {
#endif

typedef int64_t osty_qt_handle;
typedef const char *osty_qt_string;

OSTY_QT_API osty_qt_string osty_qt_abi_version(void);
OSTY_QT_API bool osty_qt_has_qt(void);
OSTY_QT_API osty_qt_string osty_qt_last_error(void);

OSTY_QT_API osty_qt_handle osty_qt_app_new(osty_qt_string name);
OSTY_QT_API void osty_qt_app_free(osty_qt_handle app);
OSTY_QT_API int64_t osty_qt_app_run(osty_qt_handle app);
OSTY_QT_API bool osty_qt_app_poll(osty_qt_handle app);
OSTY_QT_API void osty_qt_app_quit(osty_qt_handle app);
OSTY_QT_API bool osty_qt_app_set_state_json(osty_qt_handle app, osty_qt_string state_json);
OSTY_QT_API bool osty_qt_app_add_import_path(osty_qt_handle app, osty_qt_string path);

OSTY_QT_API osty_qt_handle osty_qt_window_new(
    osty_qt_handle app,
    osty_qt_string title,
    int64_t width,
    int64_t height,
    osty_qt_string qml_path);
OSTY_QT_API bool osty_qt_window_show(osty_qt_handle window);
OSTY_QT_API void osty_qt_window_close(osty_qt_handle window);
OSTY_QT_API bool osty_qt_window_set_title(osty_qt_handle window, osty_qt_string title);
OSTY_QT_API bool osty_qt_window_set_state_json(osty_qt_handle window, osty_qt_string state_json);
OSTY_QT_API bool osty_qt_window_reload(osty_qt_handle window);

OSTY_QT_API int64_t osty_qt_event_count(osty_qt_handle app);
OSTY_QT_API osty_qt_string osty_qt_event_name(osty_qt_handle app, int64_t index);
OSTY_QT_API osty_qt_string osty_qt_event_payload(osty_qt_handle app, int64_t index);
OSTY_QT_API void osty_qt_event_clear(osty_qt_handle app);

#ifdef __cplusplus
}
#endif

#endif
