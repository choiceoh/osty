package stdlib

import (
	"strings"
	"testing"
)

func TestOcrModuleSurface(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["ocr"]
	if mod == nil || mod.Package == nil {
		t.Fatalf("std.ocr not loaded")
	}
	for _, name := range []string{
		"paddleV5Options", "paddleV5JsonOptions", "tesseractTsvOptions",
		"paddleV5FastJsonOptions", "paddleV5AccurateJsonOptions",
		"customCommandOptions", "withLanguage", "withDevice", "withSavePath",
		"withPaddleModels", "withPaddlexConfig", "withPaddlePreprocessing",
		"withMinConfidence", "withTextScoreThreshold", "withExtraArg",
		"withExtraArgs", "paddlePlan", "tesseractPlan", "customPlan", "plan",
		"commandLine", "run", "runAndReadJson", "runPaddleJson",
		"runPaddleJsonBatch", "paddleJsonPath", "parseOutput", "parsePaddleJson",
		"parsePaddleJsonWithOptions", "parseTesseractTsv",
		"parseTesseractTsvWithOptions", "documentFromText", "summary",
		"indexDocument", "indexBatch", "combine", "lowConfidenceWords",
		"reviewItems", "reviewMarkdown", "search", "keyValues", "valueForKey",
		"valueForAnyKey", "chunks", "toMarkdown", "toPlainText", "sortedLines",
	} {
		requirePublicFn(t, mod, "ocr", name)
	}
	for _, name := range []string{
		"Engine", "OutputFormat", "ReadingOrder", "Bounds", "Word", "Line",
		"Block", "Document", "ReviewItem", "IndexedDocument", "BatchItem",
		"BatchResult", "Options", "CommandPlan", "RunResult", "Summary",
		"TextChunk", "KeyValue",
	} {
		requirePublicType(t, mod, "ocr", name)
	}
}

func TestOcrModulePinsPaddleAndAnalysisBehavior(t *testing.T) {
	reg := LoadCached()
	mod := reg.Modules["ocr"]
	if mod == nil {
		t.Fatal("std.ocr module missing")
	}
	src := string(mod.Source)
	for _, want := range []string{
		`pub fn paddleV5Options(language: String) -> Options`,
		`pub fn paddleV5FastJsonOptions(language: String, savePath: String) -> Options`,
		`options.textDetectionModel = "PP-OCRv5_mobile_det"`,
		`ocrVersion: "PP-OCRv5"`,
		`args.push("--ocr_version")`,
		`args.push("--use_doc_orientation_classify")`,
		`args.push("--paddlex_config")`,
		`args.push("--text_detection_model_name")`,
		`args.push("--text_rec_score_thresh")`,
		`args.push("--text_recognition_model_name")`,
		`pub fn runAndReadJson(imagePath: String, jsonPath: String, options: Options) -> Result<RunResult, Error>`,
		`pub fn runPaddleJsonBatch(imagePaths: List<String>, savePath: String, options: Options) -> BatchResult`,
		`pub fn paddleJsonPath(imagePath: String, savePath: String) -> Result<String, Error>`,
		`parsePaddleJsonWithOptions(jsonText, configured)`,
		`let textItems = json.asArray(requiredField(obj, "rec_texts")?)?`,
		`let scoreItems = optionalArray(obj, "rec_scores")`,
		`let boxItems = optionalArray(obj, "rec_boxes")`,
		`pub fn parseTesseractTsvWithOptions(text: String, options: Options) -> Result<Document, Error>`,
		`let textValue = strings.trimSpace(strings.join(cols.drop(11), "\t"))`,
		`pub fn indexDocument(doc: Document, maxChunkChars: Int, reviewThreshold: Float) -> IndexedDocument`,
		`pub fn reviewItems(doc: Document, confidenceThreshold: Float) -> List<ReviewItem>`,
		`pub fn valueForAnyKey(doc: Document, keys: List<String>) -> String?`,
		`pub fn keyValues(doc: Document) -> List<KeyValue>`,
		`pub fn chunks(doc: Document, maxChars: Int) -> List<TextChunk>`,
		`fn sortLinesByReadingOrder(lines: List<Line>) -> List<Line>`,
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("std.ocr source missing %q", want)
		}
	}
}
