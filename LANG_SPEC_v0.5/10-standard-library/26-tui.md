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
