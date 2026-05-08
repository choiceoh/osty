### 10.37 Scanner Automation (`std.scan`)

- **Scope**: Osty stdlib spec — 10.37 Scanner Automation (`std.scan`)
- **Type**: Standard library specification
`std.scan` provides a typed automation layer for document scanners. Scanner
drivers remain host-specific, so the module plans and runs host commands rather
than embedding a driver stack. The built-in backend targets SANE `scanimage`;
other tools can be wrapped with `CustomCommand` and placeholder arguments.

Core surface:

```osty
scan.scanimageOptions(outputDir) -> Options
scan.adfOptions(outputDir) -> Options
scan.duplexAdfOptions(outputDir) -> Options
scan.customCommandOptions(command, outputDir) -> Options
scan.plan(options) -> Result<CommandPlan, Error>
scan.run(options) -> Result<ScanResult, Error>
scan.scanAndOcr(options, ocrOptions, maxChunkChars, reviewThreshold) -> Result<OcrScanResult, Error>
scan.parseScanimageDevices(text) -> List<Device>
scan.listDevices(command) -> Result<List<Device>, Error>
scan.listDefaultDevices() -> Result<List<Device>, Error>
scan.manifest(result) -> String
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
