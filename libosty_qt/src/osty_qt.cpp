#include "osty_qt.h"

#include <cstdint>
#include <cstring>
#include <deque>
#include <limits>
#include <memory>
#include <mutex>
#include <string>
#include <unordered_map>
#include <utility>
#include <vector>

#if defined(OSTY_QT_ENABLE_QT)
#  include <QCoreApplication>
#  include <QEventLoop>
#  include <QFileInfo>
#  include <QGuiApplication>
#  include <QJsonDocument>
#  include <QJsonObject>
#  include <QJsonParseError>
#  include <QJsonValue>
#  include <QQmlApplicationEngine>
#  include <QQmlContext>
#  include <QQmlEngine>
#  include <QQmlError>
#  include <QString>
#  include <QUrl>
#  include <QVariant>
#  include <QWindow>
#  ifdef emit
#    undef emit
#  endif
#endif

namespace {

constexpr const char *kABIVersion = "osty_qt/0.1";

thread_local std::string g_last_error;

void set_error(std::string message) {
    g_last_error = std::move(message);
}

void clear_error() {
    g_last_error.clear();
}

bool osty_string_is_inline(const char *value) {
    if (value == nullptr) {
        return false;
    }
    if (std::numeric_limits<uintptr_t>::digits < 64) {
        return false;
    }
    return (reinterpret_cast<uintptr_t>(value) & (uintptr_t{1} << 63)) != 0;
}

std::string osty_string_to_utf8(const char *value) {
    if (value == nullptr) {
        return {};
    }
    if (!osty_string_is_inline(value)) {
        return std::string(value);
    }
    uintptr_t packed = reinterpret_cast<uintptr_t>(value);
    size_t len = (packed >> 56) & 0x7;
    std::string out;
    out.resize(len);
    for (size_t i = 0; i < len; ++i) {
        out[i] = static_cast<char>((packed >> (i * 8)) & 0xff);
    }
    return out;
}

struct Event {
    std::string name;
    std::string payload;
};

struct App {
    std::string name;
    std::string state_json = "{}";
    bool quitting = false;
    std::vector<Event> events;
    std::vector<std::string> import_paths;
    std::vector<osty_qt_handle> windows;
    std::deque<std::string> exported_strings;
};

struct Window {
    osty_qt_handle app = 0;
    std::string title;
    int64_t width = 0;
    int64_t height = 0;
    std::string qml_path;
#if defined(OSTY_QT_ENABLE_QT)
    std::unique_ptr<QQmlApplicationEngine> engine;
    QObject *root = nullptr;
    QWindow *qwindow = nullptr;
    QObject *bridge = nullptr;
    std::string resolved_qml_url;
    std::vector<std::string> load_warnings;
#endif
};

std::mutex g_mu;
osty_qt_handle g_next_handle = 1;
std::unordered_map<osty_qt_handle, std::unique_ptr<App>> g_apps;
std::unordered_map<osty_qt_handle, std::unique_ptr<Window>> g_windows;

osty_qt_handle next_handle() {
    return g_next_handle++;
}

const char *export_app_string(App *app, std::string value) {
    if (app == nullptr) {
        return "";
    }
    app->exported_strings.push_back(std::move(value));
    return app->exported_strings.back().c_str();
}

App *lookup_app_locked(osty_qt_handle handle) {
    auto it = g_apps.find(handle);
    if (it == g_apps.end()) {
        set_error("osty_qt: invalid app handle");
        return nullptr;
    }
    return it->second.get();
}

Window *lookup_window_locked(osty_qt_handle handle) {
    auto it = g_windows.find(handle);
    if (it == g_windows.end()) {
        set_error("osty_qt: invalid window handle");
        return nullptr;
    }
    return it->second.get();
}

void enqueue_event(osty_qt_handle app_handle, std::string name, std::string payload) {
    std::lock_guard<std::mutex> lock(g_mu);
    App *app = lookup_app_locked(app_handle);
    if (app == nullptr) {
        return;
    }
    app->events.push_back(Event{std::move(name), std::move(payload)});
}

#if defined(OSTY_QT_ENABLE_QT)

int g_qt_argc = 1;
char g_qt_arg0[] = "osty";
char *g_qt_argv[] = {g_qt_arg0, nullptr};
std::unique_ptr<QGuiApplication> g_qt_app;

QGuiApplication *ensure_qt_app(const std::string &name) {
    if (QGuiApplication::instance() != nullptr) {
        QCoreApplication::setApplicationName(QString::fromUtf8(name.data(), int(name.size())));
        return qobject_cast<QGuiApplication *>(QGuiApplication::instance());
    }
    g_qt_app = std::make_unique<QGuiApplication>(g_qt_argc, g_qt_argv);
    QCoreApplication::setApplicationName(QString::fromUtf8(name.data(), int(name.size())));
    return g_qt_app.get();
}

QString to_qstring(const std::string &value) {
    return QString::fromUtf8(value.data(), int(value.size()));
}

QUrl qml_url(const std::string &path) {
    QString qpath = to_qstring(path);
    if (qpath.startsWith(QStringLiteral("qrc:")) ||
        qpath.startsWith(QStringLiteral(":/")) ||
        qpath.startsWith(QStringLiteral("file:"))) {
        return QUrl(qpath);
    }
    return QUrl::fromLocalFile(QFileInfo(qpath).absoluteFilePath());
}

std::string qurl_to_utf8(const QUrl &url) {
    QString text = url.isLocalFile() ? url.toLocalFile() : url.toString();
    return text.toUtf8().toStdString();
}

std::string join_lines(const std::vector<std::string> &items, const char *prefix) {
    std::string out;
    for (const auto &item : items) {
        if (item.empty()) {
            continue;
        }
        out += "\n";
        out += prefix;
        out += item;
    }
    return out;
}

std::string qml_failure_message(Window *window, const std::string &reason) {
    std::string msg = "osty_qt: " + reason + ": " + window->qml_path;
    if (!window->resolved_qml_url.empty()) {
        msg += "\n  resolved: " + window->resolved_qml_url;
    }
    if (window->engine != nullptr) {
        std::vector<std::string> import_paths;
        const QStringList paths = window->engine->importPathList();
        for (const auto &path : paths) {
            import_paths.push_back(path.toUtf8().toStdString());
        }
        if (!import_paths.empty()) {
            msg += "\n  QML import paths:";
            msg += join_lines(import_paths, "    - ");
        }
    }
    if (!window->load_warnings.empty()) {
        msg += "\n  QML diagnostics:";
        msg += join_lines(window->load_warnings, "    - ");
    }
    msg += "\n  hint: run `osty gui doctor qtquick` from the app root, and verify Qt Quick/QML modules and platform plugins are installed.";
    return msg;
}

QVariant json_to_variant(const std::string &json, bool *ok = nullptr) {
    QJsonParseError parse_error;
    QJsonDocument doc = QJsonDocument::fromJson(QByteArray(json.data(), int(json.size())), &parse_error);
    if (parse_error.error != QJsonParseError::NoError || doc.isNull()) {
        if (ok != nullptr) {
            *ok = false;
        }
        return QVariantMap{};
    }
    if (ok != nullptr) {
        *ok = true;
    }
    return doc.toVariant();
}

std::string variant_to_json(const QVariant &payload) {
    QJsonValue value = QJsonValue::fromVariant(payload);
    QJsonDocument doc;
    if (value.isObject()) {
        doc.setObject(value.toObject());
    } else if (value.isArray()) {
        doc.setArray(value.toArray());
    } else if (value.isUndefined() || value.isNull()) {
        doc.setObject(QJsonObject{});
    } else {
        doc.setObject(QJsonObject{{QStringLiteral("value"), value}});
    }
    QByteArray out = doc.toJson(QJsonDocument::Compact);
    return std::string(out.constData(), size_t(out.size()));
}

class OstyBridge : public QObject {
    Q_OBJECT
    Q_PROPERTY(QVariant appState READ appState NOTIFY appStateChanged)

public:
    OstyBridge(osty_qt_handle app_handle, QObject *parent = nullptr)
        : QObject(parent), app_handle_(app_handle) {}

    QVariant appState() const {
        return state_;
    }

    void setState(QVariant state) {
        state_ = std::move(state);
        Q_EMIT appStateChanged();
    }

    Q_INVOKABLE void emit(const QString &name, const QVariant &payload = QVariant()) {
        enqueue_event(app_handle_, name.toStdString(), variant_to_json(payload));
    }

Q_SIGNALS:
    void appStateChanged();

private:
    osty_qt_handle app_handle_;
    QVariant state_;
};

bool apply_state(Window *window, const std::string &state_json) {
    if (window == nullptr || window->engine == nullptr || window->bridge == nullptr) {
        set_error("osty_qt: window is not loaded");
        return false;
    }
    bool ok = false;
    QVariant state = json_to_variant(state_json, &ok);
    if (!ok) {
        set_error("osty_qt: state is not valid JSON");
        return false;
    }
    auto *bridge = qobject_cast<OstyBridge *>(window->bridge);
    if (bridge != nullptr) {
        bridge->setState(state);
    }
    window->engine->rootContext()->setContextProperty(QStringLiteral("appState"), state);
    return true;
}

bool load_window(Window *window) {
    if (window == nullptr) {
        set_error("osty_qt: nil window");
        return false;
    }
    std::string state_json;
    std::vector<std::string> import_paths;
    {
        std::lock_guard<std::mutex> lock(g_mu);
        App *app = lookup_app_locked(window->app);
        if (app == nullptr) {
            return false;
        }
        state_json = app->state_json;
        import_paths = app->import_paths;
    }

    window->engine = std::make_unique<QQmlApplicationEngine>();
    window->root = nullptr;
    window->qwindow = nullptr;
    window->bridge = nullptr;
    window->resolved_qml_url.clear();
    window->load_warnings.clear();
    QObject::connect(window->engine.get(), &QQmlEngine::warnings,
                     [window](const QList<QQmlError> &warnings) {
                         for (const QQmlError &warning : warnings) {
                             window->load_warnings.push_back(warning.toString().toUtf8().toStdString());
                         }
                     });
    auto *bridge = new OstyBridge(window->app, window->engine.get());
    window->bridge = bridge;
    bool state_ok = false;
    QVariant state = json_to_variant(state_json, &state_ok);
    if (!state_ok) {
        set_error("osty_qt: app state is not valid JSON");
        return false;
    }
    bridge->setState(state);
    window->engine->rootContext()->setContextProperty(QStringLiteral("osty"), bridge);
    window->engine->rootContext()->setContextProperty(QStringLiteral("appState"), state);
    for (const auto &path : import_paths) {
        window->engine->addImportPath(to_qstring(path));
    }
    QUrl url = qml_url(window->qml_path);
    window->resolved_qml_url = qurl_to_utf8(url);
    if (url.isLocalFile() && !QFileInfo::exists(url.toLocalFile())) {
        set_error(qml_failure_message(window, "QML file not found"));
        return false;
    }
    window->engine->load(url);
    if (window->engine->rootObjects().isEmpty()) {
        set_error(qml_failure_message(window, "QML load failed"));
        return false;
    }
    window->root = window->engine->rootObjects().first();
    window->qwindow = qobject_cast<QWindow *>(window->root);
    if (window->qwindow != nullptr) {
        window->qwindow->setTitle(to_qstring(window->title));
        if (window->width > 0) {
            window->qwindow->setWidth(int(window->width));
        }
        if (window->height > 0) {
            window->qwindow->setHeight(int(window->height));
        }
    }
    return true;
}

#else

bool ensure_stub_error() {
    set_error("osty_qt: libosty_qt was built without Qt support; rebuild with OSTY_QT_ENABLE_QT=ON and Qt Quick/QML installed");
    return false;
}

#endif

} // namespace

extern "C" {

osty_qt_string osty_qt_abi_version(void) {
    return kABIVersion;
}

bool osty_qt_has_qt(void) {
#if defined(OSTY_QT_ENABLE_QT)
    return true;
#else
    return false;
#endif
}

osty_qt_string osty_qt_last_error(void) {
    return g_last_error.empty() ? "" : g_last_error.c_str();
}

osty_qt_handle osty_qt_app_new(osty_qt_string name) {
    clear_error();
    std::string app_name = osty_string_to_utf8(name);
    if (app_name.empty()) {
        app_name = "Osty";
    }
#if defined(OSTY_QT_ENABLE_QT)
    if (ensure_qt_app(app_name) == nullptr) {
        set_error("osty_qt: failed to initialize QGuiApplication");
        return 0;
    }
    std::lock_guard<std::mutex> lock(g_mu);
    osty_qt_handle handle = next_handle();
    auto app = std::make_unique<App>();
    app->name = app_name;
    g_apps.emplace(handle, std::move(app));
    return handle;
#else
    (void)app_name;
    ensure_stub_error();
    return 0;
#endif
}

void osty_qt_app_free(osty_qt_handle app) {
    std::lock_guard<std::mutex> lock(g_mu);
    auto it = g_apps.find(app);
    if (it == g_apps.end()) {
        return;
    }
    for (osty_qt_handle window_handle : it->second->windows) {
        g_windows.erase(window_handle);
    }
    g_apps.erase(it);
}

int64_t osty_qt_app_run(osty_qt_handle app) {
    clear_error();
#if defined(OSTY_QT_ENABLE_QT)
    {
        std::lock_guard<std::mutex> lock(g_mu);
        if (lookup_app_locked(app) == nullptr) {
            return -1;
        }
    }
    return QCoreApplication::exec();
#else
    (void)app;
    ensure_stub_error();
    return -1;
#endif
}

bool osty_qt_app_poll(osty_qt_handle app) {
    clear_error();
#if defined(OSTY_QT_ENABLE_QT)
    {
        std::lock_guard<std::mutex> lock(g_mu);
        App *found = lookup_app_locked(app);
        if (found == nullptr) {
            return false;
        }
        if (found->quitting) {
            return false;
        }
    }
    QCoreApplication::processEvents(QEventLoop::AllEvents, 0);
    std::lock_guard<std::mutex> lock(g_mu);
    App *found = lookup_app_locked(app);
    return found != nullptr && !found->quitting;
#else
    (void)app;
    ensure_stub_error();
    return false;
#endif
}

void osty_qt_app_quit(osty_qt_handle app) {
    clear_error();
    {
        std::lock_guard<std::mutex> lock(g_mu);
        App *found = lookup_app_locked(app);
        if (found == nullptr) {
            return;
        }
        found->quitting = true;
    }
#if defined(OSTY_QT_ENABLE_QT)
    QCoreApplication::quit();
#endif
}

bool osty_qt_app_set_state_json(osty_qt_handle app, osty_qt_string state_json) {
    clear_error();
    std::string state = osty_string_to_utf8(state_json);
    std::vector<osty_qt_handle> windows;
    {
        std::lock_guard<std::mutex> lock(g_mu);
        App *found = lookup_app_locked(app);
        if (found == nullptr) {
            return false;
        }
        found->state_json = state;
        windows = found->windows;
    }
#if defined(OSTY_QT_ENABLE_QT)
    for (osty_qt_handle window_handle : windows) {
        Window *window = nullptr;
        {
            std::lock_guard<std::mutex> lock(g_mu);
            window = lookup_window_locked(window_handle);
        }
        if (window != nullptr && !apply_state(window, state)) {
            return false;
        }
    }
    return true;
#else
    return ensure_stub_error();
#endif
}

bool osty_qt_app_add_import_path(osty_qt_handle app, osty_qt_string path) {
    clear_error();
    std::string import_path = osty_string_to_utf8(path);
    if (import_path.empty()) {
        set_error("osty_qt: import path is empty");
        return false;
    }
    std::lock_guard<std::mutex> lock(g_mu);
    App *found = lookup_app_locked(app);
    if (found == nullptr) {
        return false;
    }
    found->import_paths.push_back(std::move(import_path));
    return true;
}

osty_qt_handle osty_qt_window_new(
    osty_qt_handle app,
    osty_qt_string title,
    int64_t width,
    int64_t height,
    osty_qt_string qml_path) {
    clear_error();
    if (width <= 0 || height <= 0) {
        set_error("osty_qt: window size must be positive");
        return 0;
    }
    std::string title_utf8 = osty_string_to_utf8(title);
    std::string qml_utf8 = osty_string_to_utf8(qml_path);
    if (qml_utf8.empty()) {
        set_error("osty_qt: QML path is empty");
        return 0;
    }
#if defined(OSTY_QT_ENABLE_QT)
    auto window = std::make_unique<Window>();
    window->app = app;
    window->title = title_utf8;
    window->width = width;
    window->height = height;
    window->qml_path = qml_utf8;
    {
        std::lock_guard<std::mutex> lock(g_mu);
        if (lookup_app_locked(app) == nullptr) {
            return 0;
        }
    }
    if (!load_window(window.get())) {
        return 0;
    }
    std::lock_guard<std::mutex> lock(g_mu);
    App *found = lookup_app_locked(app);
    if (found == nullptr) {
        return 0;
    }
    osty_qt_handle handle = next_handle();
    found->windows.push_back(handle);
    g_windows.emplace(handle, std::move(window));
    return handle;
#else
    (void)app;
    (void)title_utf8;
    (void)qml_utf8;
    return ensure_stub_error() ? 0 : 0;
#endif
}

bool osty_qt_window_show(osty_qt_handle window) {
    clear_error();
#if defined(OSTY_QT_ENABLE_QT)
    std::lock_guard<std::mutex> lock(g_mu);
    Window *found = lookup_window_locked(window);
    if (found == nullptr) {
        return false;
    }
    if (found->qwindow != nullptr) {
        found->qwindow->show();
        return true;
    }
    if (found->root != nullptr) {
        found->root->setProperty("visible", true);
        return true;
    }
    set_error("osty_qt: QML root object is not showable");
    return false;
#else
    (void)window;
    return ensure_stub_error();
#endif
}

void osty_qt_window_close(osty_qt_handle window) {
    clear_error();
#if defined(OSTY_QT_ENABLE_QT)
    std::lock_guard<std::mutex> lock(g_mu);
    Window *found = lookup_window_locked(window);
    if (found != nullptr && found->qwindow != nullptr) {
        found->qwindow->close();
    }
#else
    (void)window;
#endif
}

bool osty_qt_window_set_title(osty_qt_handle window, osty_qt_string title) {
    clear_error();
    std::string title_utf8 = osty_string_to_utf8(title);
#if defined(OSTY_QT_ENABLE_QT)
    std::lock_guard<std::mutex> lock(g_mu);
    Window *found = lookup_window_locked(window);
    if (found == nullptr) {
        return false;
    }
    found->title = title_utf8;
    if (found->qwindow != nullptr) {
        found->qwindow->setTitle(to_qstring(title_utf8));
    } else if (found->root != nullptr) {
        found->root->setProperty("title", to_qstring(title_utf8));
    }
    return true;
#else
    (void)window;
    (void)title_utf8;
    return ensure_stub_error();
#endif
}

bool osty_qt_window_set_state_json(osty_qt_handle window, osty_qt_string state_json) {
    clear_error();
    std::string state = osty_string_to_utf8(state_json);
#if defined(OSTY_QT_ENABLE_QT)
    Window *found = nullptr;
    {
        std::lock_guard<std::mutex> lock(g_mu);
        found = lookup_window_locked(window);
    }
    return found != nullptr && apply_state(found, state);
#else
    (void)window;
    (void)state;
    return ensure_stub_error();
#endif
}

bool osty_qt_window_reload(osty_qt_handle window) {
    clear_error();
#if defined(OSTY_QT_ENABLE_QT)
    Window *found = nullptr;
    {
        std::lock_guard<std::mutex> lock(g_mu);
        found = lookup_window_locked(window);
    }
    if (found == nullptr) {
        return false;
    }
    return load_window(found);
#else
    (void)window;
    return ensure_stub_error();
#endif
}

int64_t osty_qt_event_count(osty_qt_handle app) {
    clear_error();
    std::lock_guard<std::mutex> lock(g_mu);
    App *found = lookup_app_locked(app);
    if (found == nullptr) {
        return 0;
    }
    return int64_t(found->events.size());
}

osty_qt_string osty_qt_event_name(osty_qt_handle app, int64_t index) {
    clear_error();
    std::lock_guard<std::mutex> lock(g_mu);
    App *found = lookup_app_locked(app);
    if (found == nullptr) {
        return "";
    }
    if (index < 0 || index >= int64_t(found->events.size())) {
        set_error("osty_qt: event index out of range");
        return "";
    }
    return export_app_string(found, found->events[size_t(index)].name);
}

osty_qt_string osty_qt_event_payload(osty_qt_handle app, int64_t index) {
    clear_error();
    std::lock_guard<std::mutex> lock(g_mu);
    App *found = lookup_app_locked(app);
    if (found == nullptr) {
        return "";
    }
    if (index < 0 || index >= int64_t(found->events.size())) {
        set_error("osty_qt: event index out of range");
        return "";
    }
    return export_app_string(found, found->events[size_t(index)].payload);
}

void osty_qt_event_clear(osty_qt_handle app) {
    clear_error();
    std::lock_guard<std::mutex> lock(g_mu);
    App *found = lookup_app_locked(app);
    if (found != nullptr) {
        found->events.clear();
    }
}

}

#if defined(OSTY_QT_ENABLE_QT)
#  include "osty_qt.moc"
#endif
