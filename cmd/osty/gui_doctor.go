package main

import (
	"bufio"
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/osty/osty/internal/manifest"
)

type guiDoctorStatus string

const (
	guiDoctorPass guiDoctorStatus = "pass"
	guiDoctorWarn guiDoctorStatus = "warn"
	guiDoctorFail guiDoctorStatus = "fail"
)

type guiDoctorCheck struct {
	Name   string
	Status guiDoctorStatus
	Detail string
	Hint   string
}

type guiDoctorReport struct {
	Backend string
	Root    string
	Checks  []guiDoctorCheck
}

type qtQuickDoctorOptions struct {
	Start      string
	BridgeRoot string
	RunCMake   bool
}

func runGui(args []string, flags cliFlags) {
	_ = flags
	if len(args) < 1 || args[0] != "doctor" {
		fmt.Fprintln(os.Stderr, "usage: osty gui doctor qtquick [--bridge-root DIR] [--no-cmake] [PATH]")
		os.Exit(2)
	}
	runGUIDoctor(args[1:])
}

func runGUIDoctor(args []string) {
	if len(args) < 1 || args[0] != "qtquick" {
		fmt.Fprintln(os.Stderr, "usage: osty gui doctor qtquick [--bridge-root DIR] [--no-cmake] [PATH]")
		os.Exit(2)
	}
	fs := flag.NewFlagSet("gui doctor qtquick", flag.ExitOnError)
	var bridgeRoot string
	var noCMake bool
	fs.StringVar(&bridgeRoot, "bridge-root", "", "path to libosty_qt")
	fs.BoolVar(&noCMake, "no-cmake", false, "skip Qt CMake configure probe")
	_ = fs.Parse(args[1:])
	start := "."
	if fs.NArg() == 1 {
		start = fs.Arg(0)
	} else if fs.NArg() > 1 {
		fs.Usage()
		os.Exit(2)
	}
	report := qtQuickDoctorReport(qtQuickDoctorOptions{
		Start:      start,
		BridgeRoot: bridgeRoot,
		RunCMake:   !noCMake,
	})
	printGUIDoctorReport(report)
	if reportHasFailure(report) {
		os.Exit(1)
	}
}

func qtQuickDoctorReport(opts qtQuickDoctorOptions) guiDoctorReport {
	if opts.Start == "" {
		opts.Start = "."
	}
	report := guiDoctorReport{Backend: "qtquick"}
	projectRoot, m, manifestErr := loadDoctorManifest(opts.Start)
	report.Root = projectRoot
	if manifestErr != nil {
		report.Checks = append(report.Checks, guiDoctorCheck{
			Name:   "manifest",
			Status: guiDoctorFail,
			Detail: manifestErr.Error(),
			Hint:   "Run this from an Osty GUI project root or pass the project path.",
		})
		return report
	}
	report.Checks = append(report.Checks, guiDoctorCheck{
		Name:   "manifest",
		Status: guiDoctorPass,
		Detail: filepath.Join(projectRoot, manifest.ManifestFile),
	})
	report.checkQtQuickManifest(projectRoot, m)
	report.checkQtQuickBridge(projectRoot, opts)
	return report
}

func (r *guiDoctorReport) checkQtQuickManifest(root string, m *manifest.Manifest) {
	if m == nil || m.GUI == nil {
		r.Checks = append(r.Checks, guiDoctorCheck{
			Name:   "gui metadata",
			Status: guiDoctorFail,
			Detail: "missing [gui] section",
			Hint:   `Add [gui] backend = "qtquick" and entry = "ui/main.qml".`,
		})
		return
	}
	if m.GUI.Backend != "qtquick" {
		r.Checks = append(r.Checks, guiDoctorCheck{
			Name:   "gui metadata",
			Status: guiDoctorFail,
			Detail: fmt.Sprintf("backend = %q", m.GUI.Backend),
			Hint:   `Use backend = "qtquick" for Qt Quick apps.`,
		})
		return
	}
	r.Checks = append(r.Checks, guiDoctorCheck{Name: "gui metadata", Status: guiDoctorPass, Detail: `backend = "qtquick"`})

	entry := m.GUI.Entry
	if entry == "" {
		entry = "ui/main.qml"
	}
	entryPath := filepath.Join(root, entry)
	if _, err := os.Stat(entryPath); err != nil {
		r.Checks = append(r.Checks, guiDoctorCheck{
			Name:   "qml entry",
			Status: guiDoctorFail,
			Detail: fmt.Sprintf("%s: %v", entry, err),
			Hint:   "Create the QML entry file or update [gui].entry.",
		})
	} else {
		imports := scanQMLImports(entryPath)
		detail := entry
		if len(imports) > 0 {
			detail += " imports " + strings.Join(imports, ", ")
		}
		r.Checks = append(r.Checks, guiDoctorCheck{Name: "qml entry", Status: guiDoctorPass, Detail: detail})
	}

	binEntry := "main.osty"
	if m.Bin != nil && m.Bin.Path != "" {
		binEntry = m.Bin.Path
	}
	if _, err := os.Stat(filepath.Join(root, binEntry)); err != nil {
		r.Checks = append(r.Checks, guiDoctorCheck{
			Name:   "osty entry",
			Status: guiDoctorFail,
			Detail: fmt.Sprintf("%s: %v", binEntry, err),
			Hint:   "Create the Osty entry file or update [bin].path.",
		})
	} else {
		r.Checks = append(r.Checks, guiDoctorCheck{Name: "osty entry", Status: guiDoctorPass, Detail: binEntry})
	}

	host := runtime.GOARCH + "-" + runtime.GOOS
	for _, target := range m.Targets {
		if target == nil || target.Triple != host {
			continue
		}
		for _, lib := range target.Link {
			if lib == "osty_qt" {
				r.Checks = append(r.Checks, guiDoctorCheck{Name: "host link", Status: guiDoctorPass, Detail: host + ` links "osty_qt"`})
				return
			}
		}
		r.Checks = append(r.Checks, guiDoctorCheck{
			Name:   "host link",
			Status: guiDoctorFail,
			Detail: host + " target exists but does not link osty_qt",
			Hint:   `Add link = ["osty_qt"] under [target.` + host + `].`,
		})
		return
	}
	r.Checks = append(r.Checks, guiDoctorCheck{
		Name:   "host link",
		Status: guiDoctorWarn,
		Detail: "no [target." + host + "] entry",
		Hint:   `Add a host target with link = ["osty_qt"] for local native builds.`,
	})
}

func (r *guiDoctorReport) checkQtQuickBridge(projectRoot string, opts qtQuickDoctorOptions) {
	bridgeRoot := opts.BridgeRoot
	if bridgeRoot == "" {
		bridgeRoot = findQtBridgeRoot(projectRoot)
	}
	if bridgeRoot == "" {
		r.Checks = append(r.Checks, guiDoctorCheck{
			Name:   "libosty_qt",
			Status: guiDoctorFail,
			Detail: "libosty_qt directory not found",
			Hint:   "Run from the Osty repository checkout or pass --bridge-root /path/to/libosty_qt.",
		})
		return
	}
	r.Checks = append(r.Checks, guiDoctorCheck{Name: "libosty_qt", Status: guiDoctorPass, Detail: bridgeRoot})
	if !opts.RunCMake {
		r.Checks = append(r.Checks, guiDoctorCheck{Name: "qt configure", Status: guiDoctorWarn, Detail: "skipped by --no-cmake"})
		return
	}
	if _, err := exec.LookPath("cmake"); err != nil {
		r.Checks = append(r.Checks, guiDoctorCheck{
			Name:   "cmake",
			Status: guiDoctorFail,
			Detail: "cmake not found on PATH",
			Hint:   "Install CMake, then build libosty_qt before running the app.",
		})
		return
	}
	r.Checks = append(r.Checks, guiDoctorCheck{Name: "cmake", Status: guiDoctorPass, Detail: "available"})
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	buildDir, err := os.MkdirTemp("", "osty-qt-doctor-*")
	if err != nil {
		r.Checks = append(r.Checks, guiDoctorCheck{Name: "qt configure", Status: guiDoctorFail, Detail: err.Error()})
		return
	}
	defer os.RemoveAll(buildDir)
	cmd := exec.CommandContext(ctx, "cmake", "-S", bridgeRoot, "-B", buildDir, "-DOSTY_QT_ENABLE_QT=ON")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		r.Checks = append(r.Checks, guiDoctorCheck{
			Name:   "qt configure",
			Status: guiDoctorFail,
			Detail: "cmake probe timed out",
			Hint:   "Check that Qt and CMake are installed and responsive.",
		})
		return
	}
	if err != nil {
		r.Checks = append(r.Checks, guiDoctorCheck{
			Name:   "qt configure",
			Status: guiDoctorFail,
			Detail: firstOutputLines(out, 8),
			Hint:   "Install Qt Quick/QML development packages, or use -DOSTY_QT_ENABLE_QT=OFF only for diagnostic stubs.",
		})
		return
	}
	r.Checks = append(r.Checks, guiDoctorCheck{Name: "qt configure", Status: guiDoctorPass, Detail: "Qt Quick/QML found by CMake"})
}

func loadDoctorManifest(start string) (string, *manifest.Manifest, error) {
	info, err := os.Stat(start)
	if err == nil && !info.IsDir() {
		start = filepath.Dir(start)
	}
	root, err := manifest.FindRoot(start)
	if err != nil {
		return "", nil, err
	}
	m, diags, err := manifest.Load(filepath.Join(root, manifest.ManifestFile))
	if err != nil {
		return root, nil, err
	}
	for _, d := range diags {
		if d != nil && d.Severity.String() == "error" {
			return root, nil, fmt.Errorf("%s", d.Message)
		}
	}
	return root, m, nil
}

func findQtBridgeRoot(projectRoot string) string {
	candidates := []string{}
	for _, env := range []string{"OSTY_QT_ROOT", "OSTY_QT_BRIDGE_ROOT"} {
		if value := os.Getenv(env); value != "" {
			candidates = append(candidates, value)
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "libosty_qt"))
		candidates = append(candidates, walkUpBridgeCandidates(cwd)...)
	}
	candidates = append(candidates, filepath.Join(projectRoot, "libosty_qt"))
	candidates = append(candidates, walkUpBridgeCandidates(projectRoot)...)
	for _, c := range candidates {
		c = filepath.Clean(c)
		if isQtBridgeRoot(c) {
			return c
		}
	}
	return ""
}

func walkUpBridgeCandidates(start string) []string {
	var out []string
	dir, err := filepath.Abs(start)
	if err != nil {
		return out
	}
	for {
		out = append(out, filepath.Join(dir, "libosty_qt"))
		parent := filepath.Dir(dir)
		if parent == dir {
			return out
		}
		dir = parent
	}
}

func isQtBridgeRoot(dir string) bool {
	for _, rel := range []string{"CMakeLists.txt", "include/osty_qt.h", "src/osty_qt.cpp"} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			return false
		}
	}
	return true
}

func scanQMLImports(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "import ") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := fields[1]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func firstOutputLines(out []byte, limit int) string {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return "cmake failed without output"
	}
	lines := strings.Split(text, "\n")
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return strings.Join(lines, "\n")
}

func printGUIDoctorReport(report guiDoctorReport) {
	fmt.Printf("Qt Quick GUI doctor\n")
	if report.Root != "" {
		fmt.Printf("project: %s\n", report.Root)
	}
	for _, check := range report.Checks {
		fmt.Printf("[%s] %s: %s\n", check.Status, check.Name, check.Detail)
		if check.Hint != "" {
			fmt.Printf("      hint: %s\n", check.Hint)
		}
	}
}

func reportHasFailure(report guiDoctorReport) bool {
	for _, check := range report.Checks {
		if check.Status == guiDoctorFail {
			return true
		}
	}
	return false
}
