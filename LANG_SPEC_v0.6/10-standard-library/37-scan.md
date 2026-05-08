### 10.37 Scanner Automation (`std.scan`)

`std.scan` provides a typed automation layer for document scanners. Scanner
drivers remain host-specific, so the module plans and runs host commands rather
than embedding a driver stack. The built-in backend targets SANE `scanimage`;
other tools can be wrapped with `CustomCommand` and placeholder arguments.

> **v0.6 capability**: planning is pure (`scan.plan`,
> `scan.scanimageOptions`, `scan.parseScanimageDevices`); *execution*
> spawns a host process and writes files, so `scan.run` and
> `scan.scanAndOcr` require both `Process` and `Fs` capabilities
> (§20.9.5/6). The legacy zero-arg form desugars under
> `--legacy-globals` (v0.6.x).

Core surface:

```osty
// Pure — capability-free option builders + plan computation.
scan.scanimageOptions(outputDir) -> Options
scan.adfOptions(outputDir) -> Options
scan.duplexAdfOptions(outputDir) -> Options
scan.customCommandOptions(command, outputDir) -> Options
scan.plan(options) -> Result<CommandPlan, Error>
scan.parseScanimageDevices(text) -> List<Device>
scan.manifest(result) -> String

// Effectful — methods on `Process` + `Fs` capability set.
fn run(proc: Process, fs: Fs, options: Options) -> Result<ScanResult, Error>
fn scanAndOcr(
    proc: Process, fs: Fs,
    options: Options, ocrOptions: OcrOptions,
    maxChunkChars: Int, reviewThreshold: Float,
) -> Result<OcrScanResult, Error>
fn listDevices(proc: Process, command: String) -> Result<List<Device>, Error>
fn listDefaultDevices(proc: Process) -> Result<List<Device>, Error>
```

`Options` covers device id, source (`Flatbed`, `Adf`, `DuplexAdf`), color mode,
format (`Png`, `Jpeg`, `Tiff`, `Pnm`, `Pdf`), output directory, filename prefix,
page count, DPI, page size, timeout, and extra host arguments. `Scanimage`
rejects `Pdf` because `scanimage` emits image streams; use `CustomCommand` for
PDF-producing tools.

Custom command placeholders:

- `\{output\}`: first planned page path
- `\{outputs\}`: all planned page paths joined by spaces
- `\{outputDir\}`: output directory
- `\{batchPattern\}`: scanimage-style batch pattern
- `\{device\}`: configured device id
- `\{format\}`: lower-case format name
- `\{dpi\}`: configured resolution

The OCR handoff uses `std.ocr.run` for each scanned page, returns a scan-local
`OcrBatch`, and builds indexed documents with `ocr.indexDocument`.

#### Information flow

`Options.outputDir` is a path-shaped sink — passing a tainted
`String` (e.g. an HTTP form value) directly into `scanimageOptions`
without going through `std.path.normalize` is `E0902`. The
`outputDir` field carries `#[requires("path_safe")]` at the type
level, so the checker enforces sanitization at construction time
rather than at run.
