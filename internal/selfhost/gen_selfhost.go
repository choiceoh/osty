//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/selfhost/bundle"
	"github.com/osty/osty/internal/selfhost/genpatch"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	root, err := findRepoRoot()
	if err != nil {
		return err
	}
	outPath := filepath.Join(root, "internal/selfhost/generated.go")
	if upToDate, err := generatedSelfhostUpToDate(root, outPath); err == nil && upToDate {
		return nil
	} else if err != nil {
		return err
	}
	tmpDir, err := os.MkdirTemp("", "osty-bootstrap-gen-*")
	if err != nil {
		return fmt.Errorf("create selfhost temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	merged, err := bundle.MergeToolchainChecker(root)
	if err != nil {
		return err
	}
	// `text=auto` in .gitattributes checks `toolchain/*.osty` out with
	// CRLF on Windows. The Osty lexer treats `\r` inside a string as
	// an unterminated-string error, so a raw merged source breaks
	// bootstrap-gen's resolve pass with ~100 false positives. Strip
	// CR unconditionally — the in-tree files are canonical LF, and
	// this keeps the regen pipeline portable across hosts.
	merged = bytes.ReplaceAll(merged, []byte("\r\n"), []byte("\n"))
	mergedPath := filepath.Join(tmpDir, "selfhost_merged.osty")
	if err := os.WriteFile(mergedPath, merged, 0o644); err != nil {
		return fmt.Errorf("write merged selfhost source: %w", err)
	}
	tmpOutPath := filepath.Join(tmpDir, "generated.go")
	// bootstrap-gen links selfhost directly and forces the embedded
	// checker, so we no longer build osty-native-checker for regen.
	// That dropped ~15s of JSON + subprocess overhead on the checker
	// call. OSTY_NATIVE_CHECKER_BIN is still honoured only as an
	// explicit debug override.
	cmd := exec.Command(
		"go", "run", "./cmd/osty-bootstrap-gen",
		"--package", "selfhost",
		"-o", tmpOutPath,
		mergedPath,
	)
	cmd.Dir = root
	if override := strings.TrimSpace(os.Getenv("OSTY_NATIVE_CHECKER_BIN")); override != "" {
		cmd.Env = append(os.Environ(), "OSTY_NATIVE_CHECKER_BIN="+override)
	} else {
		cmd.Env = os.Environ()
	}
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generate selfhost parser: %w\n%s", err, bytes.TrimSpace(output))
	}
	if bootstrapGenMissingTypes(output) {
		return fmt.Errorf(
			"generate selfhost parser: bootstrap gen emitted untyped Go; refusing to overwrite %s\n%s",
			outPath,
			bytes.TrimSpace(output),
		)
	}
	if err := patchGenerated(tmpOutPath); err != nil {
		return err
	}
	data, err := os.ReadFile(tmpOutPath)
	if err != nil {
		return fmt.Errorf("read patched selfhost code: %w", err)
	}
	if dbg := os.Getenv("OSTY_BOOTSTRAP_DEBUG_DUMP"); dbg != "" {
		_ = os.WriteFile(dbg, data, 0o644)
	}
	return installWithBuildGate(root, outPath, data)
}

// installWithBuildGate atomically swaps generated.go for the freshly
// emitted content, verifies that the selfhost package still compiles,
// and rolls back on failure. Without this gate the regen pipeline
// silently replaced the generated bridge with compile-broken Go —
// any subsequent `go build` (including the next regen's native
// checker build) then dies inside the very package the regen was
// supposed to produce, and the only recovery was a hand-rolled
// `git checkout --`.
func installWithBuildGate(root, outPath string, data []byte) error {
	prev, readErr := os.ReadFile(outPath)
	if readErr != nil && !os.IsNotExist(readErr) {
		return fmt.Errorf("read previous selfhost code: %w", readErr)
	}
	havePrev := readErr == nil
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("install generated selfhost code: %w", err)
	}
	cmd := exec.Command("go", "build", "./internal/selfhost/...")
	cmd.Dir = root
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	// Preserve the rejected output alongside the restored file so
	// contributors can diff it against the known-good committed state
	// and locate the Osty-source shape that tripped bootstrap-gen
	// without re-running regen + scraping stderr. The broken dump is
	// under `.gitignore` (`*.broken`), so it never accidentally lands
	// in a commit.
	brokenPath := outPath + ".broken"
	_ = os.WriteFile(brokenPath, data, 0o644)
	if havePrev {
		if restoreErr := os.WriteFile(outPath, prev, 0o644); restoreErr != nil {
			return fmt.Errorf(
				"generated selfhost code does not compile and restore failed: build=%v (%s); restore=%w",
				err, bytes.TrimSpace(output), restoreErr,
			)
		}
	} else {
		_ = os.Remove(outPath)
	}
	return fmt.Errorf(
		"generated selfhost code does not compile; refused to install broken code at %s\nrejected output preserved at %s for diff\n%s",
		outPath,
		brokenPath,
		bytes.TrimSpace(output),
	)
}

func bootstrapGenMissingTypes(output []byte) bool {
	return bytes.Contains(output, []byte("osty-bootstrap-gen: warning: native type checking is unavailable"))
}

func generatedSelfhostUpToDate(root, outPath string) (bool, error) {
	outInfo, err := os.Stat(outPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("stat generated selfhost code: %w", err)
	}
	newest := outInfo.ModTime()
	checkPath := func(path string) error {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	}
	if err := checkPath(filepath.Join(root, "internal/selfhost/gen_selfhost.go")); err != nil {
		return false, fmt.Errorf("stat selfhost generator: %w", err)
	}
	// bootstrap-gen is the Osty→Go transpiler driver — its sources
	// directly shape generated.go, so a change here must trigger a
	// regen. Without this, editing `internal/bootstrap/gen/*.go` would
	// leave generated.go stale and every downstream test run would
	// silently reflect the pre-edit output.
	genDir := filepath.Join(root, "internal/bootstrap/gen")
	genEntries, err := os.ReadDir(genDir)
	if err != nil {
		return false, fmt.Errorf("read bootstrap/gen: %w", err)
	}
	for _, entry := range genEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		if err := checkPath(filepath.Join(genDir, entry.Name())); err != nil {
			return false, fmt.Errorf("stat bootstrap/gen/%s: %w", entry.Name(), err)
		}
	}
	for _, rel := range bundle.ToolchainCheckerFiles() {
		if err := checkPath(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return false, fmt.Errorf("stat %s: %w", rel, err)
		}
	}
	return !outInfo.ModTime().Before(newest), nil
}

func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find repository root from %s", dir)
		}
		dir = parent
	}
}

func patchGenerated(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read generated selfhost code: %w", err)
	}
	src := string(data)
	src = normalizeGeneratedSourceComment(src)
	for _, fn := range []struct {
		name string
		body string
	}{
		{name: "frontPositionAt", body: frontPositionAtReplacement},
		{name: "frontUnitAt", body: frontUnitAtReplacement},
		{name: "frontTokenListCount", body: frontTokenListCountReplacement},
		{name: "frontTokenAtList", body: frontTokenAtListReplacement},
		{name: "astArenaNodeCount", body: astArenaNodeCountReplacement},
		{name: "astArenaNodeAt", body: astArenaNodeAtReplacement},
		{name: "coreArenaNodeCount", body: coreArenaNodeCountReplacement},
		{name: "coreArenaNodeAt", body: coreArenaNodeAtReplacement},
		{name: "opErrorCount", body: opErrorCountReplacement},
		{name: "checkIntListLenLocal", body: checkIntListLenLocalReplacement},
		{name: "checkIntListAtLocal", body: checkIntListAtLocalReplacement},
		{name: "checkIntListLenHelper", body: checkIntListLenHelperReplacement},
		{name: "checkIntListAt", body: checkIntListAtReplacement},
		{name: "checkStringListLenHelper", body: checkStringListLenHelperReplacement},
		{name: "checkVariantListLen", body: checkVariantListLenReplacement},
		{name: "pmWitnessesCount", body: pmWitnessesCountReplacement},
		{name: "checkFieldListLen", body: checkFieldListLenReplacement},
		{name: "checkListOfRowsLen", body: checkListOfRowsLenReplacement},
		{name: "checkCtorListLen", body: checkCtorListLenReplacement},
		{name: "frontCheckResultTypedNodeCount", body: frontCheckResultTypedNodeCountReplacement},
		{name: "frontCheckResultInstantiationCount", body: frontCheckResultInstantiationCountReplacement},
		{name: "selfResolveDiagnosticCount", body: selfResolveDiagnosticCountReplacement},
		{name: "srAstChildAt", body: srAstChildAtReplacement},
		{name: "srAstListCount", body: srAstListCountReplacement},
		{name: "srIntListAt", body: srIntListAtReplacement},
		{name: "srStringListAt", body: srStringListAtReplacement},
		{name: "frontLexTokenCount", body: frontLexTokenCountReplacement},
		{name: "frontLexDiagnosticCount", body: frontLexDiagnosticCountReplacement},
		{name: "frontCommentCount", body: frontCommentCountReplacement},
		{name: "frontStringPartCount", body: frontStringPartCountReplacement},
		{name: "frontInterpolationTokenCount", body: frontInterpolationTokenCountReplacement},
		{name: "frontLexTokenAt", body: frontLexTokenAtReplacement},
		{name: "frontLexDiagnosticAt", body: frontLexDiagnosticAtReplacement},
		{name: "frontCommentAt", body: frontCommentAtReplacement},
		{name: "frontStringPartAt", body: frontStringPartAtReplacement},
		{name: "frontInterpolationTokenAt", body: frontInterpolationTokenAtReplacement},
		{name: "stringUnitCount", body: stringUnitCountReplacement},
		{name: "ostyLexStringPartCount", body: ostyLexStringPartCountReplacement},
		{name: "ostyStringListCount", body: ostyStringListCountReplacement},
		{name: "ostyStringAt", body: ostyStringAtReplacement},
		{name: "ostyLexResultTokenCount", body: ostyLexResultTokenCountReplacement},
		{name: "ostyLexResultErrorCount", body: ostyLexResultErrorCountReplacement},
		{name: "ostyLexResultCommentCount", body: ostyLexResultCommentCountReplacement},
		{name: "ostyLexResultTokenAt", body: ostyLexResultTokenAtReplacement},
		{name: "astFileDeclCount", body: astFileDeclCountReplacement},
		{name: "astFileErrorCount", body: astFileErrorCountReplacement},
		{name: "astFileDeclAt", body: astFileDeclAtReplacement},
		{name: "astFileErrorAt", body: astFileErrorAtReplacement},
		{name: "checkDiagCount", body: checkDiagCountReplacement},
		{name: "coreIntLen", body: coreIntLenReplacement},
		{name: "coreDiagCount", body: coreDiagCountReplacement},
		{name: "solveIntLen", body: solveIntLenReplacement},
		{name: "selfLintDiagnosticCount", body: selfLintDiagnosticCountReplacement},
		{name: "selfLintFixCount", body: selfLintFixCountReplacement},
		{name: "selfLintEditListCount", body: selfLintEditListCountReplacement},
		{name: "selfLintEditAt", body: selfLintEditAtReplacement},
		{name: "astLowerTokenCount", body: astLowerTokenCountReplacement},
		{name: "astLowerIntListCount", body: astLowerIntListCountReplacement},
		{name: "astLowerIntListAt", body: astLowerIntListAtReplacement},
		{name: "pmParseIntLit", body: pmParseIntLitReplacement},
		{name: "containsInterpolation", body: containsInterpolationReplacement},
		{name: "astLowerDecodeEscapes", body: astLowerDecodeEscapesReplacement},
	} {
		var err error
		src, err = replaceGeneratedFunction(src, fn.name, fn.body)
		if err != nil {
			return err
		}
	}
	if strings.Contains(src, "sync.Mutex") && !strings.Contains(src, "\n\t\"sync\"\n") {
		src = strings.Replace(src, "\n\t\"strings\"\n", "\n\t\"strings\"\n\t\"sync\"\n", 1)
	}
	formatted, err := format.Source([]byte(src))
	if err != nil {
		return fmt.Errorf("format generated selfhost code: %w", err)
	}
	if err := os.WriteFile(path, formatted, 0o644); err != nil {
		return fmt.Errorf("write generated selfhost code: %w", err)
	}
	return nil
}

// normalizeGeneratedSourceComment rewrites both the top-level
// `// Osty source: …` header and the per-declaration
// `// Osty: …selfhost_merged.osty:LINE:COL` markers so the generated
// file is byte-stable across platforms. Without this pass the source
// paths inherit the host tmpdir — `/var/folders/…` on macOS,
// `C:\Users\…\AppData\Local\Temp\…` on Windows — producing a ~thousand-
// line diff on every cross-platform regen. The canonical placeholder is
// `/tmp/selfhost_merged.osty`; line/column suffixes are preserved.
func normalizeGeneratedSourceComment(src string) string {
	return genpatch.NormalizeGeneratedSourceComment(src)
}

func replaceGeneratedFunction(src, name, replacement string) (string, error) {
	return genpatch.ReplaceGeneratedFunction(src, name, replacement)
}

const frontPositionAtReplacement = `type frontPositionCacheState struct {
	units  []string
	target int
	offset int
	line   int
	column int
	skipLf bool
}

var frontPositionCacheMu sync.Mutex
var frontPositionCache frontPositionCacheState

func frontSameUnits(a []string, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 {
		return true
	}
	return &a[0] == &b[0]
}

func frontPositionAt(units []string, target int) *FrontPos {
	if target < 0 {
		target = 0
	}
	if target > len(units) {
		target = len(units)
	}

	frontPositionCacheMu.Lock()
	defer frontPositionCacheMu.Unlock()

	if !frontSameUnits(frontPositionCache.units, units) || target < frontPositionCache.target {
		frontPositionCache = frontPositionCacheState{
			units:  units,
			target: 0,
			offset: 0,
			line:   1,
			column: 1,
			skipLf: false,
		}
	}

	offset := frontPositionCache.offset
	line := frontPositionCache.line
	column := frontPositionCache.column
	skipLf := frontPositionCache.skipLf
	for idx := frontPositionCache.target; idx < target; idx++ {
		unit := units[idx]
		next := ""
		if idx+1 < len(units) {
			next = units[idx+1]
		}
		if skipLf {
			skipLf = false
			offset = offset + 1
		} else if unit == "\r" {
			line = line + 1
			column = 1
			offset = offset + 1
			if next == "\n" {
				skipLf = true
			}
		} else if unit == "\n" {
			line = line + 1
			column = 1
			offset = offset + 1
		} else {
			column = column + 1
			offset = offset + 1
		}
	}
	frontPositionCache.target = target
	frontPositionCache.offset = offset
	frontPositionCache.line = line
	frontPositionCache.column = column
	frontPositionCache.skipLf = skipLf
	return frontPos(offset, line, column)
}
`

const frontUnitAtReplacement = `func frontUnitAt(units []string, target int) string {
	if target < 0 || target >= len(units) {
		return ""
	}
	return units[target]
}
`

const frontTokenListCountReplacement = `func frontTokenListCount(tokens []*FrontToken) int {
	return len(tokens)
}
`

const frontTokenAtListReplacement = `func frontTokenAtList(tokens []*FrontToken, target int) *FrontToken {
	if target < 0 || target >= len(tokens) {
		return frontEOF()
	}
	return tokens[target]
}
`

const astArenaNodeCountReplacement = `func astArenaNodeCount(arena *AstArena) int {
	return len(arena.nodes)
}
`

const astArenaNodeAtReplacement = `func astArenaNodeAt(arena *AstArena, idx int) *AstNode {
	if idx < 0 || idx >= len(arena.nodes) {
		return emptyAstNode(AstNodeKind(&AstNodeKind_AstNError{}))
	}
	return arena.nodes[idx]
}
`

const coreArenaNodeCountReplacement = `func coreArenaNodeCount(arena *CoreArena) int {
	return len(arena.nodes)
}
`

const coreArenaNodeAtReplacement = `func coreArenaNodeAt(arena *CoreArena, idx int) *CoreNode {
	if idx < 0 || idx >= len(arena.nodes) {
		return emptyCoreNode(CoreKind(&CoreKind_CkErr{}))
	}
	return arena.nodes[idx]
}
`

const opErrorCountReplacement = `func opErrorCount(p *OstyParser) int {
	return len(p.arena.errors)
}
`

const checkIntListLenLocalReplacement = `func checkIntListLenLocal(xs []int) int {
	return len(xs)
}
`

const checkIntListAtLocalReplacement = `func checkIntListAtLocal(xs []int, idx int) int {
	if idx < 0 || idx >= len(xs) {
		return -1
	}
	return xs[idx]
}
`

const checkIntListLenHelperReplacement = `func checkIntListLenHelper(xs []int) int {
	return len(xs)
}
`

const checkIntListAtReplacement = `func checkIntListAt(xs []int, idx int) int {
	if idx < 0 || idx >= len(xs) {
		return -1
	}
	return xs[idx]
}
`

const checkStringListLenHelperReplacement = `func checkStringListLenHelper(xs []string) int {
	return len(xs)
}
`

const checkVariantListLenReplacement = `func checkVariantListLen(xs []*CheckVariantSig) int {
	return len(xs)
}
`

const pmWitnessesCountReplacement = `func pmWitnessesCount(xs []string) int {
	return len(xs)
}
`

const checkFieldListLenReplacement = `func checkFieldListLen(xs []*CheckFieldSig) int {
	return len(xs)
}
`

const checkListOfRowsLenReplacement = `func checkListOfRowsLen(xs [][]int) int {
	return len(xs)
}
`

const checkCtorListLenReplacement = `func checkCtorListLen(xs []*PmCtor) int {
	return len(xs)
}
`

const frontCheckResultTypedNodeCountReplacement = `func frontCheckResultTypedNodeCount(result *FrontCheckResult) int {
	return len(result.typedNodes)
}
`

const frontCheckResultInstantiationCountReplacement = `func frontCheckResultInstantiationCount(result *FrontCheckResult) int {
	return len(result.instantiations)
}
`

const selfResolveDiagnosticCountReplacement = `func selfResolveDiagnosticCount(result *SelfResolveResult) int {
	return len(result.diagnostics)
}
`

const srAstChildAtReplacement = `func srAstChildAt(children []int, target int) int {
	if target < 0 || target >= len(children) {
		return -1
	}
	return children[target]
}
`

const srAstListCountReplacement = `func srAstListCount(items []int) int {
	return len(items)
}
`

const srIntListAtReplacement = `func srIntListAt(items []int, target int) int {
	if target < 0 || target >= len(items) {
		return -1
	}
	return items[target]
}
`

const srStringListAtReplacement = `func srStringListAt(items []string, target int) string {
	if target < 0 || target >= len(items) {
		return ""
	}
	return items[target]
}
`

const frontLexTokenCountReplacement = `func frontLexTokenCount(stream *FrontLexStream) int {
	return len(stream.tokens)
}
`

const frontLexDiagnosticCountReplacement = `func frontLexDiagnosticCount(stream *FrontLexStream) int {
	return len(stream.diagnostics)
}
`

const frontCommentCountReplacement = `func frontCommentCount(stream *FrontLexStream) int {
	return len(stream.comments)
}
`

const frontStringPartCountReplacement = `func frontStringPartCount(stream *FrontLexStream) int {
	return len(stream.stringParts)
}
`

const frontInterpolationTokenCountReplacement = `func frontInterpolationTokenCount(stream *FrontLexStream) int {
	return len(stream.interpolationTokens)
}
`

const frontLexTokenAtReplacement = `func frontLexTokenAt(stream *FrontLexStream, target int) *FrontLexToken {
	if target < 0 || target >= len(stream.tokens) {
		return emptyFrontLexToken()
	}
	return stream.tokens[target]
}
`

const frontLexDiagnosticAtReplacement = `func frontLexDiagnosticAt(stream *FrontLexStream, target int) *FrontLexDiagnostic {
	if target < 0 || target >= len(stream.diagnostics) {
		return emptyFrontLexDiagnostic()
	}
	return stream.diagnostics[target]
}
`

const frontCommentAtReplacement = `func frontCommentAt(stream *FrontLexStream, target int) *FrontComment {
	if target < 0 || target >= len(stream.comments) {
		return emptyFrontComment()
	}
	return stream.comments[target]
}
`

const frontStringPartAtReplacement = `func frontStringPartAt(stream *FrontLexStream, target int) *FrontStringPart {
	if target < 0 || target >= len(stream.stringParts) {
		return emptyFrontStringPart()
	}
	return stream.stringParts[target]
}
`

const frontInterpolationTokenAtReplacement = `func frontInterpolationTokenAt(stream *FrontLexStream, target int) *FrontInterpolationToken {
	if target < 0 || target >= len(stream.interpolationTokens) {
		return emptyFrontInterpolationToken()
	}
	return stream.interpolationTokens[target]
}
`

const stringUnitCountReplacement = `func stringUnitCount(text string) int {
	return len(strings.Split(text, ""))
}
`

const ostyLexStringPartCountReplacement = `func ostyLexStringPartCount(parts []*OstyLexStringPart) int {
	return len(parts)
}
`

const ostyStringListCountReplacement = `func ostyStringListCount(items []string) int {
	return len(items)
}
`

const ostyStringAtReplacement = `func ostyStringAt(items []string, target int) string {
	if target < 0 || target >= len(items) {
		return ""
	}
	return items[target]
}
`

const ostyLexResultTokenCountReplacement = `func ostyLexResultTokenCount(result *OstyLexResult) int {
	return len(result.tokens)
}
`

const ostyLexResultErrorCountReplacement = `func ostyLexResultErrorCount(result *OstyLexResult) int {
	return len(result.errors)
}
`

const ostyLexResultCommentCountReplacement = `func ostyLexResultCommentCount(result *OstyLexResult) int {
	return len(result.comments)
}
`

const ostyLexResultTokenAtReplacement = `func ostyLexResultTokenAt(result *OstyLexResult, idx int) *OstyRichToken {
	if idx < 0 || idx >= len(result.tokens) {
		return &OstyRichToken{kind: FrontTokenKind(&FrontTokenKind_FrontEOF{}), text: "", startOffset: 0, startLine: 0, startCol: 0, endOffset: 0, endLine: 0, endCol: 0, leadingDoc: "", triple: false, partCount: 0}
	}
	return result.tokens[idx]
}
`

const astFileDeclCountReplacement = `func astFileDeclCount(file *AstFile) int {
	return len(file.arena.decls)
}
`

const astFileErrorCountReplacement = `func astFileErrorCount(file *AstFile) int {
	return len(file.arena.errors)
}
`

const astFileDeclAtReplacement = `func astFileDeclAt(file *AstFile, idx int) *AstNode {
	if idx < 0 || idx >= len(file.arena.decls) {
		return emptyAstNode(AstNodeKind(&AstNodeKind_AstNError{}))
	}
	return astArenaNodeAt(file.arena, file.arena.decls[idx])
}
`

const astFileErrorAtReplacement = `func astFileErrorAt(file *AstFile, idx int) *AstParseError {
	if idx < 0 || idx >= len(file.arena.errors) {
		return &AstParseError{message: "", tokenIndex: 0, hint: "", note: "", code: ""}
	}
	return file.arena.errors[idx]
}
`

const checkDiagCountReplacement = `func checkDiagCount(xs []*CheckDiagnostic) int {
	return len(xs)
}
`

const coreIntLenReplacement = `func coreIntLen(xs []int) int {
	return len(xs)
}
`

const coreDiagCountReplacement = `func coreDiagCount(xs []*CheckDiagnostic) int {
	return len(xs)
}
`

const solveIntLenReplacement = `func solveIntLen(xs []int) int {
	return len(xs)
}
`

const selfLintDiagnosticCountReplacement = `func selfLintDiagnosticCount(report *SelfLintReport) int {
	return len(report.diagnostics)
}
`

const selfLintFixCountReplacement = `func selfLintFixCount(diag *SelfLintDiagnostic) int {
	return len(diag.fixes)
}
`

const selfLintEditListCountReplacement = `func selfLintEditListCount(edits []*SelfLintEdit) int {
	return len(edits)
}
`

const selfLintEditAtReplacement = `func selfLintEditAt(edits []*SelfLintEdit, target int) *SelfLintEdit {
	if target < 0 || target >= len(edits) {
		return selfLintInvalidEdit()
	}
	return edits[target]
}
`

const astLowerTokenCountReplacement = `func astLowerTokenCount(toks []astbridge.Token) int {
	return len(toks)
}
`

const astLowerIntListCountReplacement = `func astLowerIntListCount(xs []int) int {
	return len(xs)
}
`

const astLowerIntListAtReplacement = `func astLowerIntListAt(xs []int, target int) int {
	if target < 0 || target >= len(xs) {
		return -1
	}
	return xs[target]
}
`

const pmParseIntLitReplacement = `func pmParseIntLit(text string) *PmIntParse {
	units := strings.Split(text, "")
	n := len(units)
	if n == 0 {
		return &PmIntParse{ok: false, value: 0}
	}
	negative := false
	i := 0
	if units[0] == "-" {
		negative = true
		i = 1
	}
	if i >= n {
		return &PmIntParse{ok: false, value: 0}
	}
	base := 10
	if i+1 < n {
		lead := units[i] + units[i+1]
		if lead == "0x" || lead == "0X" {
			base = 16
			i += 2
		} else if lead == "0b" || lead == "0B" {
			base = 2
			i += 2
		} else if lead == "0o" || lead == "0O" {
			base = 8
			i += 2
		}
	}
	if i >= n {
		return &PmIntParse{ok: false, value: 0}
	}
	value := 0
	digits := 0
	for i < n {
		ch := units[i]
		if ch == "_" {
			i++
			continue
		}
		if base == 10 && (ch == "." || ch == "e" || ch == "E") {
			return &PmIntParse{ok: false, value: 0}
		}
		d := pmDigitValueIn(ch, base)
		if d < 0 {
			return &PmIntParse{ok: false, value: 0}
		}
		if value > (math.MaxInt-d)/base {
			panic("integer overflow")
		}
		value = value*base + d
		digits++
		i++
	}
	if digits == 0 {
		return &PmIntParse{ok: false, value: 0}
	}
	if negative {
		value = -value
	}
	return &PmIntParse{ok: true, value: value}
}
`

const containsInterpolationReplacement = `func containsInterpolation(text string) bool {
	units := strings.Split(text, "")
	for i, unit := range units {
		if unit != "{" {
			continue
		}
		prev := ""
		if i > 0 {
			prev = units[i-1]
		}
		if prev != "\\" {
			return true
		}
	}
	return false
}
`

const astLowerDecodeEscapesReplacement = `func astLowerDecodeEscapes(s string) string {
	if strings.Count(s, "\\") == 0 {
		return s
	}
	units := strings.Split(s, "")
	n := len(units)
	parts := make([]string, 0, n)
	for i := 0; i < n; {
		unit := units[i]
		if unit != "\\" {
			parts = append(parts, unit)
			i++
			continue
		}
		if i+1 >= n {
			parts = append(parts, "\\")
			i++
			continue
		}
		next := units[i+1]
		if next == "n" {
			parts = append(parts, "\n")
			i += 2
			continue
		}
		if next == "r" {
			parts = append(parts, "\r")
			i += 2
			continue
		}
		if next == "t" {
			parts = append(parts, "\t")
			i += 2
			continue
		}
		if next == "0" {
			parts = append(parts, astbridge.RuneString(0))
			i += 2
			continue
		}
		if next == "x" && i+3 < n {
			high := frontHexValue(units[i+2])
			low := frontHexValue(units[i+3])
			if high >= 0 && low >= 0 {
				value := high*16 + low
				parts = append(parts, astbridge.RuneString(value))
				i += 4
				continue
			}
		}
		if next == "u" && i+2 < n && units[i+2] == "{" {
			parsed := astLowerDecodeUnicodeEscape(units, i+3)
			if parsed.consumed > 0 {
				parts = append(parts, astbridge.RuneString(parsed.value))
				i += 3 + parsed.consumed
				continue
			}
		}
		parts = append(parts, next)
		i += 2
	}
	return strings.Join(parts, "")
}
`
