### 10.25 Terminal (`std.term`)

- **Scope**: Osty stdlib spec — 10.25 Terminal (`std.term`)
- **Type**: Standard library specification
`std.term` is the low-level terminal boundary used by text UIs and simple
terminal games.

Host-backed primitives:

```osty
term.open() -> Result<Terminal, Error>
term.isTerminal() -> Bool
term.size() -> Result<Size, Error>
term.setRawMode(enabled: Bool) -> Result<(), Error>
term.readKey() -> Result<Key, Error>
term.pollKey(timeoutMillis: Int) -> Result<Key?, Error>
term.readEvent() -> Result<Event, Error>
term.write(text: String) -> Result<(), Error>
term.flush() -> Result<(), Error>
```

Pure ANSI helpers:

```osty
term.clearScreenSeq() -> String
term.clearLineSeq() -> String
term.moveToSeq(Position { x, y }) -> String
term.hideCursorSeq() -> String
term.showCursorSeq() -> String
term.enterAltScreenSeq() -> String
term.exitAltScreenSeq() -> String
term.styleSeq(style: Style) -> String
term.styled(text: String, style: Style) -> String
```

Coordinates are zero-based in the Osty API. `moveToSeq` converts to the
one-based row/column convention used by ANSI terminals.

`Terminal.restore()` is the canonical cleanup point for programs that enter
raw mode, hide the cursor, or switch to the alternate screen.

Current LLVM backend coverage includes `isTerminal`, `size`, `write`, `flush`,
and `setRawMode`. Key and resize event decoding is part of the next host-backed
terminal increment.
