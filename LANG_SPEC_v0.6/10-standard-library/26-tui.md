### 10.26 Text UI (`std.tui`)

`std.tui` provides retained frame buffers for terminal UI code. A `Frame`
contains fixed-size `Cell` values, each with text and style. Rendering can
emit a whole frame or only the cells that changed compared with a previous
frame.

Core API:

```osty
tui.frame(size: grid.Size) -> Frame
tui.sized(width: Int, height: Int) -> Frame
tui.screen(size: grid.Size) -> Screen

Frame.put(point: grid.Point, text: String, style: Style) -> Bool
Frame.drawText(point: grid.Point, text: String, style: Style) -> Int
Frame.drawHLine(y: Int, x1: Int, x2: Int, text: String, style: Style) -> Int
Frame.drawVLine(x: Int, y1: Int, y2: Int, text: String, style: Style) -> Int
Frame.drawRect(rect: grid.Rect, glyph: String, style: Style)
Frame.renderAnsi() -> String
Frame.diffAnsi(previous: Frame?) -> String

Screen.next(frame: Frame) -> String
Screen.present(frame: Frame) -> Result<(), Error>
Screen.reset()
```

`std.tui` intentionally renders to strings first. That keeps frame diffing
testable and lets callers choose whether to write through `std.term`,
capture output, or compare snapshots.

#### Capability split

Frame composition (`tui.frame`, `Frame.put`, `Frame.drawText`,
`Frame.diffAnsi`) is *pure* — every method here is capability-free
and acceptable inside `#[reproducible(scope = "target")]` (§3.11).

Output (`Screen.present`) is the only effectful method; it consumes
a `Terminal` capability obtained from `Console.terminal()`
(§10.25 / §20.9.7). The split lets a `tui` test render a frame and
compare its `renderAnsi()` output against a `#[golden]` snapshot
(§11.5.2) without ever touching a host terminal:

```osty
#[golden("fixtures/tui_login_screen.snap", mode = "text")]
#[reproducible(scope = "target")]
fn testLoginScreen() {
    let frame = tui.sized(40, 12)
    frame.drawText(grid.point(2, 1), "login", styles.heading)
    frame.drawHLine(2, 1, 38, "─", styles.border)
    testing.assertGolden(frame.renderAnsi())
}
```
