### 10.38 Print (`std.print`)

- **Scope**: Osty stdlib spec — 10.38 Print (`std.print`)
- **Type**: Standard library specification
`std.print` sends existing PDF and image files to the host print spooler. It
does not implement page rendering in the language runtime; applications should
produce a PDF or image first, then ask the host printer stack to print it.

Core types:

```osty
print.DocumentKind
print.Backend
print.Orientation
print.Duplex
print.ColorMode
print.Document
print.Options
print.Printer
print.CommandPlan
print.Job
```

Core API:

```osty
print.options() -> Options
print.file(path) -> Document
print.pdf(path) -> Document
print.image(path) -> Document
print.pageRange(from, to) -> Result<PageRange, Error>
print.plan(doc, opts) -> Result<CommandPlan, Error>
print.print(doc, opts) -> Result<Job, Error>
print.printFile(path) -> Result<Job, Error>
print.printPdf(path) -> Result<Job, Error>
print.printImage(path) -> Result<Job, Error>
print.defaultPrinterName() -> Result<String, Error>
print.printers() -> Result<List<Printer>, Error>
```

Behavior:

- `AutoBackend` uses CUPS `lp`, which is available on macOS and common Linux
  desktops. `Lpr`, `WindowsShell`, and `CustomCommand` expose explicit
  fallback paths.
- `plan` validates the requested document kind, copies, page ranges, and
  command backend without requiring the file to exist.
- `print` additionally checks that the file exists, runs the command plan, and
  returns the command output plus a best-effort spooler job id.
- PDF and image printing are path-based. Byte-to-file rendering, PDF creation,
  and image pixel conversion remain responsibilities of `std.fs`, `std.gui`,
  `std.report`, or future codec/runtime layers.
