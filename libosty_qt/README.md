# libosty_qt

`libosty_qt` is the first Qt Quick/QML bridge for Osty GUI experiments. It
keeps Qt's C++ object model behind a small C ABI so Osty code can use
`use c "osty_qt"` without exposing Qt pointers or callbacks.

## Build

```sh
cmake -S libosty_qt -B .osty/qt-build
cmake --build .osty/qt-build
```

For machines without Qt, the library can be built in diagnostic-only mode:

```sh
cmake -S libosty_qt -B .osty/qt-build -DOSTY_QT_ENABLE_QT=OFF
cmake --build .osty/qt-build
```

That stub build exports the same symbols, but `osty_qt_app_new` fails with a
clear error saying Qt support was not compiled in.

## String Boundary

The ABI receives Osty `String` values as the native runtime string pointer, not
as a generic C-owned `char *`. The shim decodes Osty's current inline
small-string tag before passing UTF-8 to Qt. Returned strings are stable
NUL-terminated bridge strings and stay valid until the app handle is freed.

This is good enough for the spike, but the durable runtime shape should expose a
small public string helper API so external shims do not duplicate runtime string
layout details.
