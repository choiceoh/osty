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

#### 10.26.1 Frame composition patterns

The pure-rendering / capability-presenting split lets TUI tests
follow this layered shape:

1. **Pure layer** — functions that take *application state* and
   return a `Frame`. No capability, no I/O. `#[reproducible]`-
   eligible.

   ```osty
   #[reproducible(scope = "target")]
   fn renderState(state: AppState) -> Frame {
       let frame = tui.frame(state.size)
       frame.drawText(state.cursorPos, ">", styles.cursor)
       frame.drawText(grid.point(0, 0), state.title, styles.heading)
       frame
   }
   ```

2. **Effectful layer** — receives `Terminal` and presents the
   frame. This is the only place that consults capabilities.

   ```osty
   fn redraw(term: Terminal, screen: Screen, state: AppState) -> Result<(), Error> {
       let frame = renderState(state)
       screen.present(frame)
   }
   ```

The split makes the pure layer testable in isolation against
`#[golden]` snapshots; only the effectful layer needs `Terminal`-
shaped fakes.

#### 10.26.2 Diff-based rendering

`Frame.diffAnsi(previous)` produces an ANSI sequence that updates
the terminal from `previous` to the current frame. This is the
primary pattern for low-flicker TUIs:

```osty
fn renderLoop(term: Terminal, screen: Screen) -> Result<(), Error> {
    let mut prev: Frame? = None
    for {
        let state = readNextState()?
        let curr = renderState(state)
        let ansi = curr.diffAnsi(prev)
        term.write(ansi)?
        term.flush()?
        prev = Some(curr)
        thread.checkCancelled()?
    }
}
```

Diff output is deterministic given two frames — the sequence is
always the same for the same `(prev, curr)` pair. This matters
for `#[golden]` tests of diff output.

#### 10.26.3 Frame and information flow

Cell text in a `Frame` carries the flow tag set of its source
string. Tainted text rendered into a frame produces a tainted
ANSI string when serialized; writing that string to a terminal
goes through the `Terminal` capability (not a registered sink),
so no flow check is required at the present site.

Authors who want sanitization before rendering apply it in the
pure layer:

```osty
fn renderUserInput(input: #[taint("user_input")] String) -> Frame {
    let safe = std.term.escapeAnsi(input)   // strips control sequences
    let frame = tui.frame(grid.size(80, 1))
    frame.drawText(grid.point(0, 0), safe, styles.normal)
    frame
}
```

`std.term.escapeAnsi` strips embedded ANSI control sequences from
user input — preventing terminal-injection attacks where untrusted
strings could move the cursor, change colors, or set the window
title.
