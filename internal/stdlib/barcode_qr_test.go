package stdlib

import (
	"strings"
	"testing"
)

func TestBarcodeModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["barcode"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.barcode not loaded")
	}
	for _, name := range []string{
		"code39Options", "ean13Options", "upcAOptions",
		"zbarimgOptions", "zxingOptions", "customScanOptions",
		"withModuleWidth", "withBarHeight", "withQuietZone", "withText",
		"withColors", "withChecksum", "withSymbology", "withExtraArg",
		"encode", "encodeCode39", "encodeEan13", "encodeUpcA",
		"toSvg", "renderSvg", "toAscii", "symbologyName",
		"parseSymbology", "detectSymbology", "scanPlan", "commandLine",
		"scan", "parseScanLines", "parseZbarOutput", "parseZxingOutput",
		"ean13CheckDigit", "upcACheckDigit", "isValidEan13", "isValidUpcA",
		"inventoryLabel", "shipmentLabel", "accessLabel",
	} {
		requirePublicFn(t, mod, "barcode", name)
	}
	for _, name := range []string{
		"Symbology", "ChecksumPolicy", "ScannerEngine", "Options",
		"LinearCode", "ScanOptions", "ScanResult", "CommandPlan",
	} {
		requirePublicType(t, mod, "barcode", name)
	}
}

func TestQrModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["qr"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.qr not loaded")
	}
	for _, name := range []string{
		"options", "low", "medium", "high",
		"withErrorCorrection", "withModuleSize", "withQuietZone",
		"withColors", "withCommand", "withExtraArg",
		"zbarimgOptions", "zxingOptions", "customRecognitionOptions",
		"encode", "toSvg", "renderSvg", "toAscii", "matrixFromRows",
		"qrencodePlan", "generate", "recognitionPlan", "recognize",
		"parseRecognitionOutput", "commandLine", "urlPayload",
		"wifiPayload", "mailtoPayload", "vCardPayload", "paymentPayload",
		"accessPayload", "shipmentPayload",
	} {
		requirePublicFn(t, mod, "qr", name)
	}
	for _, name := range []string{
		"ErrorCorrection", "QrEngine", "OutputFormat", "Options",
		"Matrix", "CommandPlan", "RecognitionOptions", "RecognitionResult",
	} {
		requirePublicType(t, mod, "qr", name)
	}
}

func TestBarcodeQrModulesPinPracticalBehavior(t *testing.T) {
	reg := LoadCached()
	cases := map[string][]string{
		"barcode": {
			`pub fn encodeCode39(text: String, checksum: ChecksumPolicy) -> Result<LinearCode, Error>`,
			`fn code39CharacterBits(c: Char) -> String`,
			`fn ean13Pattern(full: String) -> Result<String, Error>`,
			`pub fn scanPlan(imagePath: String, options: ScanOptions) -> Result<CommandPlan, Error>`,
			`pub fn parseZxingOutput(text: String, source: String) -> List<ScanResult>`,
			`pub fn shipmentLabel(carrier: String, tracking: String) -> String`,
			`"--possibleFormats"`,
		},
		"qr": {
			`pub fn qrencodePlan(text: String, outputPath: String, format: OutputFormat, options: Options) -> Result<CommandPlan, Error>`,
			`pub fn recognize(imagePath: String, options: RecognitionOptions) -> Result<List<RecognitionResult>, Error>`,
			`pub fn wifiPayload(ssid: String, password: String, auth: String, hidden: Bool) -> String`,
			`fn drawFinder(matrix: Matrix, ox: Int, oy: Int) -> Matrix`,
			`fn fillPayloadModules(matrix: Matrix, text: String, level: ErrorCorrection) -> Matrix`,
			`pub fn parseRecognitionOutput(text: String, source: String) -> List<RecognitionResult>`,
			`"qrencode"`,
		},
	}
	for module, wants := range cases {
		mod := reg.Modules[module]
		if mod == nil {
			t.Fatalf("std.%s module missing", module)
		}
		src := string(mod.Source)
		for _, want := range wants {
			if !strings.Contains(src, want) {
				t.Fatalf("std.%s source missing %q", module, want)
			}
		}
	}
}
