package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/ast"
	"github.com/osty/osty/internal/backend"
	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/diag"
	"github.com/osty/osty/internal/resolve"
)

type nativeTestCase struct {
	Name string
	Path string
}

func runTest(args []string, flags cliFlags) {
	os.Exit(runTestMain(args, flags, os.Stdout, os.Stderr))
}

func runTestMain(args []string, flags cliFlags, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: osty test [--offline | --locked | --frozen] [--backend NAME] [--emit MODE] [--airepair] [--airepair-mode MODE] [PATH|FILTER...]")
	}
	var offline, locked, frozen bool
	fs.BoolVar(&offline, "offline", false, "do not fetch dependencies; fail if caches are missing")
	fs.BoolVar(&locked, "locked", false, "fail if osty.lock would change")
	fs.BoolVar(&frozen, "frozen", false, "imply --locked --offline; require an existing osty.lock")
	var aiRepairModeName string
	registerAIRepairCommandFlags(fs, &flags.aiRepair, &aiRepairModeName)
	var backendName string
	var emitName string
	fs.StringVar(&backendName, "backend", defaultBackendName(), "code generation backend (llvm)")
	fs.StringVar(&emitName, "emit", "", "artifact mode to execute (binary)")
	var pf profileFlags
	pf.register(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	mode, ok := parseAIRepairMode(aiRepairModeName)
	if !ok {
		fmt.Fprintf(stderr, "osty test: unknown airepair mode %q (want rewrite, parse, or frontend)\n", aiRepairModeName)
		return 2
	}
	flags.aiMode = mode
	_ = offline
	_ = locked
	_ = frozen
	_ = pf

	backendID, emitMode := resolveBackendAndEmitFlags("test", backendName, emitName)
	pkgDir, filters, err := resolveTestTarget(fs.Args())
	if err != nil {
		fmt.Fprintf(stderr, "osty test: %v\n", err)
		return 2
	}

	pkg, err := resolve.LoadPackageWithTests(pkgDir)
	if err != nil {
		fmt.Fprintf(stderr, "osty test: %v\n", err)
		return 1
	}
	applyAIRepairToPackage(pkg, "osty test --airepair", stderr, flags)
	parseDiags := packageParseDiags(pkg)
	if hasError(parseDiags) {
		printPackageDiags(pkg, parseDiags, flags)
		fmt.Fprintf(stderr, "osty test: parse errors in %s\n", pkgDir)
		return 1
	}

	tests, err := discoverNativeTests(pkg, filters)
	if err != nil {
		fmt.Fprintf(stderr, "osty test: %v\n", err)
		return 1
	}
	if len(tests) == 0 {
		fmt.Fprintln(stdout, "running 0 tests")
		return 0
	}

	b, err := backend.New(backendID)
	if err != nil {
		fmt.Fprintf(stderr, "osty test: %v\n", err)
		return 2
	}
	tmpRoot, err := os.MkdirTemp("", "osty-test-*")
	if err != nil {
		fmt.Fprintf(stderr, "osty test: %v\n", err)
		return 1
	}
	defer os.RemoveAll(tmpRoot)

	fmt.Fprintf(stdout, "running %d tests\n", len(tests))
	failures := 0
	for _, tc := range tests {
		run, err := compileAndRunNativeTest(context.Background(), b, emitMode, tmpRoot, pkg, tc)
		if err != nil {
			failures++
			fmt.Fprintf(stdout, "FAIL\t%s\n", tc.Name)
			fmt.Fprintf(stderr, "osty test: %s: %v\n", tc.Name, err)
			if strings.TrimSpace(run.Stdout) != "" {
				fmt.Fprintf(stdout, "%s", run.Stdout)
				if !strings.HasSuffix(run.Stdout, "\n") {
					fmt.Fprintln(stdout)
				}
			}
			if strings.TrimSpace(run.Stderr) != "" {
				fmt.Fprintf(stderr, "%s", run.Stderr)
				if !strings.HasSuffix(run.Stderr, "\n") {
					fmt.Fprintln(stderr)
				}
			}
			continue
		}
		fmt.Fprintf(stdout, "ok\t%s\n", tc.Name)
	}
	if failures > 0 {
		fmt.Fprintf(stdout, "FAIL\t%d/%d tests failed\n", failures, len(tests))
		return 1
	}
	fmt.Fprintf(stdout, "ok\t%d tests passed\n", len(tests))
	return 0
}

func resolveTestTarget(args []string) (string, []string, error) {
	if len(args) == 0 {
		return ".", nil, nil
	}
	first := args[0]
	if info, err := os.Stat(first); err == nil {
		if info.IsDir() {
			return first, args[1:], nil
		}
		return filepath.Dir(first), args[1:], nil
	}
	return ".", args, nil
}

func packageParseDiags(pkg *resolve.Package) []*diag.Diagnostic {
	var out []*diag.Diagnostic
	if pkg == nil {
		return out
	}
	for _, pf := range pkg.Files {
		if pf == nil {
			continue
		}
		out = append(out, pf.ParseDiags...)
	}
	return out
}

func discoverNativeTests(pkg *resolve.Package, filters []string) ([]nativeTestCase, error) {
	if pkg == nil {
		return nil, fmt.Errorf("missing package for test discovery")
	}
	seen := map[string]string{}
	var tests []nativeTestCase
	for _, pf := range pkg.Files {
		if pf == nil || pf.File == nil {
			continue
		}
		for _, decl := range pf.File.Decls {
			fn, ok := decl.(*ast.FnDecl)
			if !ok || fn == nil {
				continue
			}
			if fn.Recv != nil || len(fn.Generics) != 0 {
				continue
			}
			if fn.Name == "main" {
				return nil, fmt.Errorf("package %s already defines main; native test runner currently requires a library-style package", pkg.Dir)
			}
			if !strings.HasPrefix(fn.Name, "test") || fn.Name == "testing" {
				continue
			}
			if len(fn.Params) != 0 || fn.ReturnType != nil || fn.Body == nil {
				continue
			}
			if !matchesTestFilters(fn.Name, filters) {
				continue
			}
			if prev, exists := seen[fn.Name]; exists {
				return nil, fmt.Errorf("duplicate test function %q in %s and %s", fn.Name, prev, pf.Path)
			}
			seen[fn.Name] = pf.Path
			tests = append(tests, nativeTestCase{Name: fn.Name, Path: pf.Path})
		}
	}
	sort.Slice(tests, func(i, j int) bool {
		if tests[i].Path != tests[j].Path {
			return tests[i].Path < tests[j].Path
		}
		return tests[i].Name < tests[j].Name
	})
	return tests, nil
}

func matchesTestFilters(name string, filters []string) bool {
	if len(filters) == 0 {
		return true
	}
	for _, filter := range filters {
		if filter == "" {
			continue
		}
		if strings.Contains(name, filter) {
			return true
		}
	}
	return false
}

type nativeTestRun struct {
	Stdout string
	Stderr string
}

func compileAndRunNativeTest(ctx context.Context, b backend.Backend, emitMode backend.EmitMode, tmpRoot string, pkg *resolve.Package, tc nativeTestCase) (nativeTestRun, error) {
	file, src, sourcePath, err := parseNativeTestEntry(pkg, tc)
	if err != nil {
		return nativeTestRun{}, err
	}
	name := sanitizeNativeTestName(tc.Name)
	layoutRoot := filepath.Join(tmpRoot, name)
	res := resolveFile(file)
	chk := check.File(file, res, checkOptsForSource(src))
	entry, err := backend.PrepareEntry("main", sourcePath, file, res, chk)
	if err != nil {
		return nativeTestRun{}, err
	}
	req := backend.Request{
		Layout: backend.Layout{
			Root:    layoutRoot,
			Profile: "test",
		},
		Emit:       emitMode,
		Entry:      entry,
		BinaryName: name,
	}
	result, err := b.Emit(ctx, req)
	if err != nil {
		return nativeTestRun{}, err
	}
	return runNativeTestBinary(result.Artifacts.Binary)
}

func parseNativeTestEntry(pkg *resolve.Package, tc nativeTestCase) (*ast.File, []byte, string, error) {
	if pkg == nil {
		return nil, nil, "", fmt.Errorf("missing package")
	}
	runnerPath := filepath.Join(pkg.Dir, "__osty_test_runner__.osty")
	runner := []byte(fmt.Sprintf("fn main() {\n    %s()\n}\n", tc.Name))
	testPkg := &resolve.Package{
		Dir:  pkg.Dir,
		Name: pkg.Name,
	}
	testPkg.Files = append(testPkg.Files, pkg.Files...)
	testPkg.Files = append(testPkg.Files, &resolve.PackageFile{
		Path:   runnerPath,
		Source: runner,
	})
	file, src, err := parseGenEmitFile(testPkg)
	if err != nil {
		return nil, nil, "", err
	}
	sourcePath := tc.Path
	if sourcePath == "" {
		sourcePath = runnerPath
	}
	return file, src, sourcePath, nil
}

func sanitizeNativeTestName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "osty_test"
	}
	return b.String()
}

func runNativeTestBinary(binPath string) (nativeTestRun, error) {
	if binPath == "" {
		return nativeTestRun{}, fmt.Errorf("native backend did not produce a binary")
	}
	absBin, err := filepath.Abs(binPath)
	if err != nil {
		return nativeTestRun{}, err
	}
	cmd := exec.Command(absBin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	run := nativeTestRun{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if err == nil {
		return run, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return run, fmt.Errorf("exit status %d", exitErr.ExitCode())
	}
	return run, err
}
