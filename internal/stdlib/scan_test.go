package stdlib

import (
	"strings"
	"testing"
)

func TestScanModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["scan"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.scan not loaded")
	}
	for _, name := range []string{
		"scanimageOptions", "adfOptions", "duplexAdfOptions",
		"customCommandOptions", "withCommand", "withDevice", "withSource",
		"withColorMode", "withFormat", "withOutput", "withPageCount",
		"withStartIndex", "withResolutionDpi", "withPageSize", "withDuplex",
		"withTimeoutMillis", "withExtraArg", "withExtraArgs", "plannedPaths",
		"pagePath", "plan", "scanimagePlan", "customPlan", "commandLine",
		"scan", "run", "scanAndOcr", "runAndOcr", "ocrPages", "indexOcrBatch",
		"scanimageListDevicesPlan", "scanimageDefaultListDevicesPlan",
		"listDevices", "listDefaultDevices", "parseScanimageDevices",
		"pagesFromPaths", "pageFromPath", "pagePaths", "manifest",
		"writeManifest", "formatName", "formatMime",
	} {
		requirePublicFn(t, mod, "scan", name)
	}
	for _, name := range []string{
		"Backend", "Source", "ColorMode", "ScanFormat", "Device",
		"Options", "CommandPlan", "ScannedPage", "ScanResult", "OcrPageItem",
		"OcrBatch", "OcrScanResult",
	} {
		requirePublicType(t, mod, "scan", name)
	}
}

func TestScanModulePinsScannerAndOcrBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["scan"]
	if mod == nil {
		t.Fatal("std.scan module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`use std.ocr`,
		`pub fn scanimageOptions(outputDir: String) -> Options`,
		`pub fn duplexAdfOptions(outputDir: String) -> Options`,
		`args.push("--device-name")`,
		`args.push("--format")`,
		`args.push("--resolution")`,
		`args.push("--source")`,
		`args.push("--batch={batch}")`,
		`args.push("--batch-count={options.pageCount}")`,
		`strings.replaceAll(out, "\{output\}", firstOutputPath(paths))`,
		`pub fn scanAndOcr(options: Options, ocrOptions: ocr.Options, maxChunkChars: Int, reviewThreshold: Float) -> Result<OcrScanResult, Error>`,
		`match ocr.run(page.path, options)`,
		`ocr.indexDocument(doc, maxChunkChars, reviewThreshold)`,
		`pub fn parseScanimageDevices(text: String) -> List<Device>`,
		`strings.startsWith(trimmed, "device ` + "`" + `")`,
		`image.parse(fs.read(path)?)`,
		`pub fn manifest(result: ScanResult) -> String`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.scan source missing %q", want)
		}
	}
}
