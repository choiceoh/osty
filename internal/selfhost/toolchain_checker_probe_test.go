package selfhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/osty/osty/internal/selfhost/bundle"
	toolchainbin "github.com/osty/osty/internal/toolchain"
)

func TestToolchainCheckerFileBisect(t *testing.T) {
	if os.Getenv("OSTY_TOOLCHAIN_BISECT") == "" {
		t.Skip("set OSTY_TOOLCHAIN_BISECT=1 to run toolchain checker subset bisect")
	}

	files := bundle.ToolchainCheckerFiles()
	if raw := strings.TrimSpace(os.Getenv("OSTY_TOOLCHAIN_BISECT_FILES")); raw != "" {
		files = splitList(raw)
	}
	if len(files) == 0 {
		t.Fatal("no files selected for bisect")
	}

	timeout := toolchainBisectTimeout(t)
	root := filepath.Join("..", "..")
	checker := ensureNativeCheckerBinary(t, root)

	t.Logf("probing %d files with timeout %s", len(files), timeout)
	base := toolchainSubsetOutcome(t, root, checker, files, timeout)
	t.Logf("full-set outcome: timeout=%v duration=%s files=%v", base.timedOut, base.duration, files)
	if !base.timedOut {
		t.Fatalf("selected file set completed within timeout %s; nothing to bisect", timeout)
	}

	best := ddminToolchainFiles(t, root, checker, files, timeout)
	outcome := toolchainSubsetOutcome(t, root, checker, best, timeout)
	t.Logf("minimal timeout subset (%d files): %v", len(best), best)
	t.Logf("subset outcome: timeout=%v duration=%s", outcome.timedOut, outcome.duration)
}

type toolchainProbeOutcome struct {
	duration time.Duration
	timedOut bool
}

type toolchainDeclSubset struct {
	rel       string
	declStart int
	declEnd   int
}

func ddminToolchainFiles(t *testing.T, root, checker string, files []string, timeout time.Duration) []string {
	t.Helper()
	current := append([]string(nil), files...)
	n := 2
	for len(current) >= 2 {
		chunks := partitionStrings(current, n)
		reduced := false

		for _, chunk := range chunks {
			if len(chunk) == 0 {
				continue
			}
			outcome := toolchainSubsetOutcome(t, root, checker, chunk, timeout)
			t.Logf("probe chunk size=%d timeout=%v files=%v", len(chunk), outcome.timedOut, chunk)
			if outcome.timedOut {
				current = append([]string(nil), chunk...)
				n = 2
				reduced = true
				break
			}
		}
		if reduced {
			continue
		}

		for _, chunk := range chunks {
			if len(chunk) == 0 || len(chunk) == len(current) {
				continue
			}
			complement := subtractStrings(current, chunk)
			if len(complement) == 0 {
				continue
			}
			outcome := toolchainSubsetOutcome(t, root, checker, complement, timeout)
			t.Logf("probe complement size=%d timeout=%v files=%v", len(complement), outcome.timedOut, complement)
			if outcome.timedOut {
				current = complement
				if n > 2 {
					n--
				}
				reduced = true
				break
			}
		}
		if reduced {
			continue
		}

		if n >= len(current) {
			break
		}
		n *= 2
		if n > len(current) {
			n = len(current)
		}
	}
	return current
}

func toolchainSubsetOutcome(t *testing.T, root, checker string, files []string, timeout time.Duration) toolchainProbeOutcome {
	t.Helper()

	merged, err := bundle.MergeFiles(root, files)
	if err != nil {
		t.Fatalf("merge files %v: %v", files, err)
	}

	payload, err := json.Marshal(map[string]string{"source": string(merged)})
	if err != nil {
		t.Fatalf("marshal checker request: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, checker)
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err = cmd.Run()
	dur := time.Since(start)

	if ctx.Err() == context.DeadlineExceeded {
		return toolchainProbeOutcome{duration: dur, timedOut: true}
	}
	if err != nil {
		return toolchainProbeOutcome{duration: dur, timedOut: false}
	}
	return toolchainProbeOutcome{duration: dur, timedOut: false}
}

// ensureNativeCheckerBinary is probe-only instrumentation. Production checker
// selection now defaults to the embedded selfhost path; the toolchain bisect and
// timeout probes opt into the managed subprocess explicitly so they can keep
// measuring the exec-boundary behavior in isolation.
func ensureNativeCheckerBinary(t *testing.T, start string) string {
	t.Helper()
	path, err := toolchainbin.EnsureNativeChecker(start)
	if err != nil {
		t.Fatalf("ensure native checker: %v", err)
	}
	return path
}

func toolchainBisectTimeout(t *testing.T) time.Duration {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("OSTY_TOOLCHAIN_TIMEOUT_MS"))
	if raw == "" {
		return 3 * time.Second
	}
	ms, err := strconv.Atoi(raw)
	if err != nil || ms <= 0 {
		t.Fatalf("invalid OSTY_TOOLCHAIN_TIMEOUT_MS=%q", raw)
	}
	return time.Duration(ms) * time.Millisecond
}

func toolchainProbeLineWindow(t *testing.T, total int) (int, int) {
	t.Helper()
	startRaw := strings.TrimSpace(os.Getenv("OSTY_TOOLCHAIN_LINE_START"))
	endRaw := strings.TrimSpace(os.Getenv("OSTY_TOOLCHAIN_LINE_END"))
	start := 0
	end := total
	var err error
	if startRaw != "" {
		start, err = strconv.Atoi(startRaw)
		if err != nil || start < 0 || start > total {
			t.Fatalf("invalid OSTY_TOOLCHAIN_LINE_START=%q", startRaw)
		}
	}
	if endRaw != "" {
		end, err = strconv.Atoi(endRaw)
		if err != nil || end < 0 || end > total {
			t.Fatalf("invalid OSTY_TOOLCHAIN_LINE_END=%q", endRaw)
		}
	}
	if end < start {
		t.Fatalf("invalid line window start=%d end=%d", start, end)
	}
	return start, end
}

func toolchainDeclStarts(lines []string) []int {
	starts := make([]int, 0, 64)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		if line != trimmed {
			continue
		}
		if strings.HasPrefix(trimmed, "pub fn ") ||
			strings.HasPrefix(trimmed, "fn ") ||
			strings.HasPrefix(trimmed, "pub struct ") ||
			strings.HasPrefix(trimmed, "struct ") ||
			strings.HasPrefix(trimmed, "pub enum ") ||
			strings.HasPrefix(trimmed, "enum ") ||
			strings.HasPrefix(trimmed, "pub interface ") ||
			strings.HasPrefix(trimmed, "interface ") ||
			strings.HasPrefix(trimmed, "pub let ") ||
			strings.HasPrefix(trimmed, "let ") {
			starts = append(starts, i)
		}
	}
	return starts
}

func toolchainProbeDeclWindow(t *testing.T, total int) (int, int) {
	t.Helper()
	startRaw := strings.TrimSpace(os.Getenv("OSTY_TOOLCHAIN_DECL_START"))
	endRaw := strings.TrimSpace(os.Getenv("OSTY_TOOLCHAIN_DECL_END"))
	start := 0
	end := total
	var err error
	if startRaw != "" {
		start, err = strconv.Atoi(startRaw)
		if err != nil || start < 0 || start > total {
			t.Fatalf("invalid OSTY_TOOLCHAIN_DECL_START=%q", startRaw)
		}
	}
	if endRaw != "" {
		end, err = strconv.Atoi(endRaw)
		if err != nil || end < 0 || end > total {
			t.Fatalf("invalid OSTY_TOOLCHAIN_DECL_END=%q", endRaw)
		}
	}
	if end < start {
		t.Fatalf("invalid decl window start=%d end=%d", start, end)
	}
	return start, end
}

func parseToolchainDeclSubsets(t *testing.T, raw string) []toolchainDeclSubset {
	t.Helper()
	parts := strings.Split(raw, ";")
	out := make([]toolchainDeclSubset, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		fields := strings.Split(part, ":")
		if len(fields) != 3 {
			t.Fatalf("invalid decl subset spec %q, want path:start:end", part)
		}
		start, err := strconv.Atoi(strings.TrimSpace(fields[1]))
		if err != nil || start < 0 {
			t.Fatalf("invalid decl subset start in %q", part)
		}
		end, err := strconv.Atoi(strings.TrimSpace(fields[2]))
		if err != nil || end < start {
			t.Fatalf("invalid decl subset end in %q", part)
		}
		out = append(out, toolchainDeclSubset{
			rel:       strings.TrimSpace(fields[0]),
			declStart: start,
			declEnd:   end,
		})
	}
	if len(out) == 0 {
		t.Fatal("no decl subsets parsed")
	}
	return out
}

func writeDeclSubsetFile(t *testing.T, srcRoot, dstRoot string, spec toolchainDeclSubset) {
	t.Helper()
	path := filepath.Join(srcRoot, filepath.FromSlash(spec.rel))
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", spec.rel, err)
	}
	lines := strings.Split(string(src), "\n")
	starts := toolchainDeclStarts(lines)
	if spec.declEnd > len(starts) {
		t.Fatalf("decl subset %s end=%d beyond %d decls", spec.rel, spec.declEnd, len(starts))
	}
	if spec.declStart == spec.declEnd {
		writeProbeFile(t, dstRoot, spec.rel, "")
		return
	}
	declEnd := append([]int(nil), starts[1:]...)
	declEnd = append(declEnd, len(lines))
	selected := make([]string, 0, spec.declEnd-spec.declStart)
	for idx := spec.declStart; idx < spec.declEnd; idx++ {
		selected = append(selected, strings.Join(lines[starts[idx]:declEnd[idx]], "\n"))
	}
	writeProbeFile(t, dstRoot, spec.rel, strings.Join(selected, "\n"))
}

func splitList(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func partitionStrings(items []string, n int) [][]string {
	if n <= 0 {
		return nil
	}
	out := make([][]string, 0, n)
	for i := 0; i < n; i++ {
		start := len(items) * i / n
		end := len(items) * (i + 1) / n
		out = append(out, append([]string(nil), items[start:end]...))
	}
	return out
}

func partitionInts(items []int, n int) [][]int {
	if n <= 0 {
		return nil
	}
	out := make([][]int, 0, n)
	for i := 0; i < n; i++ {
		start := len(items) * i / n
		end := len(items) * (i + 1) / n
		out = append(out, append([]int(nil), items[start:end]...))
	}
	return out
}

func subtractStrings(items, remove []string) []string {
	skip := make(map[string]int, len(remove))
	for _, item := range remove {
		skip[item]++
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if skip[item] > 0 {
			skip[item]--
			continue
		}
		out = append(out, item)
	}
	return out
}

func subtractInts(items, remove []int) []int {
	skip := make(map[int]int, len(remove))
	for _, item := range remove {
		skip[item]++
	}
	out := make([]int, 0, len(items))
	for _, item := range items {
		if skip[item] > 0 {
			skip[item]--
			continue
		}
		out = append(out, item)
	}
	return out
}

func TestToolchainCheckerSubsetProbe(t *testing.T) {
	if os.Getenv("OSTY_TOOLCHAIN_PROBE") == "" {
		t.Skip("set OSTY_TOOLCHAIN_PROBE=file1,file2 to probe one subset")
	}
	files := splitList(os.Getenv("OSTY_TOOLCHAIN_PROBE"))
	if len(files) == 0 {
		t.Fatal("OSTY_TOOLCHAIN_PROBE must list at least one file")
	}
	timeout := toolchainBisectTimeout(t)
	root := filepath.Join("..", "..")
	checker := ensureNativeCheckerBinary(t, root)
	outcome := toolchainSubsetOutcome(t, root, checker, files, timeout)
	t.Logf("subset=%v timeout=%v duration=%s threshold=%s", files, outcome.timedOut, outcome.duration, timeout)
}

func TestToolchainCheckerDeclBisect(t *testing.T) {
	rel := strings.TrimSpace(os.Getenv("OSTY_TOOLCHAIN_DECL_BISECT_FILE"))
	if rel == "" {
		t.Skip("set OSTY_TOOLCHAIN_DECL_BISECT_FILE=path to bisect one file's declarations")
	}
	root := filepath.Join("..", "..")
	src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	lines := strings.Split(string(src), "\n")
	starts := toolchainDeclStarts(lines)
	if len(starts) == 0 {
		t.Fatalf("no top-level declarations found in %s", rel)
	}
	windowStart, windowEnd := toolchainProbeDeclWindow(t, len(starts))
	current := make([]int, 0, windowEnd-windowStart)
	for idx := windowStart; idx < windowEnd; idx++ {
		current = append(current, idx)
	}
	if len(current) == 0 {
		t.Fatal("no decls selected for bisect")
	}

	supportRaw := strings.TrimSpace(os.Getenv("OSTY_TOOLCHAIN_DECL_BISECT_WITH_SUBSETS"))
	if supportRaw == "" {
		t.Fatal("set OSTY_TOOLCHAIN_DECL_BISECT_WITH_SUBSETS=path:start:end;... for fixed support subsets")
	}
	support := parseToolchainDeclSubsets(t, supportRaw)
	timeout := toolchainBisectTimeout(t)
	checker := ensureNativeCheckerBinary(t, root)
	base := toolchainDeclOutcome(t, root, checker, rel, current, support, timeout)
	t.Logf("decl-bisect base file=%s decls=%d support=%v timeout=%v duration=%s", rel, len(current), support, base.timedOut, base.duration)
	if !base.timedOut {
		t.Fatalf("selected declaration set completed within timeout %s; nothing to bisect", timeout)
	}

	n := 2
	for len(current) >= 2 {
		chunks := partitionInts(current, n)
		reduced := false

		for _, chunk := range chunks {
			if len(chunk) == 0 {
				continue
			}
			outcome := toolchainDeclOutcome(t, root, checker, rel, chunk, support, timeout)
			t.Logf("probe chunk size=%d decls=%v timeout=%v duration=%s", len(chunk), chunk, outcome.timedOut, outcome.duration)
			if outcome.timedOut {
				current = append([]int(nil), chunk...)
				n = 2
				reduced = true
				break
			}
		}
		if reduced {
			continue
		}

		for _, chunk := range chunks {
			if len(chunk) == 0 || len(chunk) == len(current) {
				continue
			}
			complement := subtractInts(current, chunk)
			if len(complement) == 0 {
				continue
			}
			outcome := toolchainDeclOutcome(t, root, checker, rel, complement, support, timeout)
			t.Logf("probe complement size=%d decls=%v timeout=%v duration=%s", len(complement), complement, outcome.timedOut, outcome.duration)
			if outcome.timedOut {
				current = complement
				if n > 2 {
					n--
				}
				reduced = true
				break
			}
		}
		if reduced {
			continue
		}

		if n >= len(current) {
			break
		}
		n *= 2
		if n > len(current) {
			n = len(current)
		}
	}

	final := toolchainDeclOutcome(t, root, checker, rel, current, support, timeout)
	t.Logf("minimal timeout decl subset file=%s decls=%v timeout=%v duration=%s", rel, current, final.timedOut, final.duration)
}

func TestToolchainCheckerFileHalves(t *testing.T) {
	if os.Getenv("OSTY_TOOLCHAIN_HALVE_FILE") == "" {
		t.Skip("set OSTY_TOOLCHAIN_HALVE_FILE=path to probe one file's line halves")
	}
	rel := strings.TrimSpace(os.Getenv("OSTY_TOOLCHAIN_HALVE_FILE"))
	support := splitList(os.Getenv("OSTY_TOOLCHAIN_HALVE_WITH"))
	root := filepath.Join("..", "..")
	path := filepath.Join(root, filepath.FromSlash(rel))
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	lines := strings.Split(string(src), "\n")
	if len(lines) < 2 {
		t.Fatalf("file too small to halve: %s", rel)
	}
	windowStart, windowEnd := toolchainProbeLineWindow(t, len(lines))
	lines = lines[windowStart:windowEnd]
	if len(lines) < 2 {
		t.Fatalf("selected line window too small to halve: %s [%d:%d]", rel, windowStart, windowEnd)
	}
	timeout := toolchainBisectTimeout(t)
	checker := ensureNativeCheckerBinary(t, root)

	for _, half := range [][2]int{
		{0, len(lines) / 2},
		{len(lines) / 2, len(lines)},
	} {
		tmpDir := t.TempDir()
		targetRel := filepath.ToSlash(rel)
		for _, relPath := range support {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
			if err != nil {
				t.Fatalf("read support %s: %v", relPath, err)
			}
			writeProbeFile(t, tmpDir, relPath, string(data))
		}
		writeProbeFile(t, tmpDir, targetRel, strings.Join(lines[half[0]:half[1]], "\n"))
		files := append([]string(nil), support...)
		files = append(files, targetRel)
		merged, err := bundle.MergeFiles(tmpDir, files)
		if err != nil {
			t.Fatalf("merge half bundle: %v", err)
		}
		payload, err := json.Marshal(map[string]string{"source": string(merged)})
		if err != nil {
			t.Fatalf("marshal chunk: %v", err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, checker)
		cmd.Stdin = bytes.NewReader(payload)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		start := time.Now()
		err = cmd.Run()
		dur := time.Since(start)
		cancel()
		timedOut := ctx.Err() == context.DeadlineExceeded
		t.Logf("%s lines[%d:%d] support=%v timeout=%v duration=%s err=%v", rel, windowStart+half[0], windowStart+half[1], support, timedOut, dur, err)
	}
}

func TestToolchainCheckerDeclHalves(t *testing.T) {
	if os.Getenv("OSTY_TOOLCHAIN_DECL_FILE") == "" {
		t.Skip("set OSTY_TOOLCHAIN_DECL_FILE=path to probe one file's declaration halves")
	}
	rel := strings.TrimSpace(os.Getenv("OSTY_TOOLCHAIN_DECL_FILE"))
	support := splitList(os.Getenv("OSTY_TOOLCHAIN_DECL_WITH"))
	root := filepath.Join("..", "..")
	path := filepath.Join(root, filepath.FromSlash(rel))
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	lines := strings.Split(string(src), "\n")
	starts := toolchainDeclStarts(lines)
	if len(starts) < 2 {
		t.Fatalf("need at least two top-level declarations in %s, found %d", rel, len(starts))
	}
	windowStart, windowEnd := toolchainProbeDeclWindow(t, len(starts))
	starts = starts[windowStart:windowEnd]
	if len(starts) < 2 {
		t.Fatalf("selected declaration window too small: %s [%d:%d]", rel, windowStart, windowEnd)
	}
	timeout := toolchainBisectTimeout(t)
	checker := ensureNativeCheckerBinary(t, root)
	declEnd := append([]int(nil), starts[1:]...)
	declEnd = append(declEnd, len(lines))

	for _, half := range [][2]int{
		{0, len(starts) / 2},
		{len(starts) / 2, len(starts)},
	} {
		tmpDir := t.TempDir()
		targetRel := filepath.ToSlash(rel)
		for _, relPath := range support {
			data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relPath)))
			if err != nil {
				t.Fatalf("read support %s: %v", relPath, err)
			}
			writeProbeFile(t, tmpDir, relPath, string(data))
		}

		selected := make([]string, 0, half[1]-half[0])
		for idx := half[0]; idx < half[1]; idx++ {
			selected = append(selected, strings.Join(lines[starts[idx]:declEnd[idx]], "\n"))
		}
		writeProbeFile(t, tmpDir, targetRel, strings.Join(selected, "\n"))

		files := append([]string(nil), support...)
		files = append(files, targetRel)
		merged, err := bundle.MergeFiles(tmpDir, files)
		if err != nil {
			t.Fatalf("merge decl bundle: %v", err)
		}
		payload, err := json.Marshal(map[string]string{"source": string(merged)})
		if err != nil {
			t.Fatalf("marshal decl chunk: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		cmd := exec.CommandContext(ctx, checker)
		cmd.Stdin = bytes.NewReader(payload)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		start := time.Now()
		err = cmd.Run()
		dur := time.Since(start)
		cancel()
		timedOut := ctx.Err() == context.DeadlineExceeded
		t.Logf("%s decls[%d:%d] lines[%d:%d] support=%v timeout=%v duration=%s err=%v", rel, windowStart+half[0], windowStart+half[1], starts[half[0]], declEnd[half[1]-1], support, timedOut, dur, err)
	}
}

func TestToolchainCheckerDeclSubsetProbe(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("OSTY_TOOLCHAIN_DECL_SUBSETS"))
	if raw == "" {
		t.Skip("set OSTY_TOOLCHAIN_DECL_SUBSETS=path:start:end;path:start:end to probe declaration subsets")
	}
	root := filepath.Join("..", "..")
	tmpDir := t.TempDir()
	subsets := parseToolchainDeclSubsets(t, raw)
	files := make([]string, 0, len(subsets))
	for _, spec := range subsets {
		writeDeclSubsetFile(t, root, tmpDir, spec)
		files = append(files, spec.rel)
	}
	timeout := toolchainBisectTimeout(t)
	checker := ensureNativeCheckerBinary(t, root)
	outcome := toolchainSubsetOutcome(t, tmpDir, checker, files, timeout)
	t.Logf("decl-subsets=%v timeout=%v duration=%s threshold=%s", subsets, outcome.timedOut, outcome.duration, timeout)
}

func toolchainDeclOutcome(t *testing.T, srcRoot, checker, targetRel string, targetDecls []int, support []toolchainDeclSubset, timeout time.Duration) toolchainProbeOutcome {
	t.Helper()
	if len(targetDecls) == 0 {
		t.Fatal("toolchainDeclOutcome requires at least one target decl")
	}
	tmpDir := t.TempDir()
	for _, spec := range support {
		writeDeclSubsetFile(t, srcRoot, tmpDir, spec)
	}
	path := filepath.Join(srcRoot, filepath.FromSlash(targetRel))
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", targetRel, err)
	}
	lines := strings.Split(string(src), "\n")
	starts := toolchainDeclStarts(lines)
	declEnd := append([]int(nil), starts[1:]...)
	declEnd = append(declEnd, len(lines))
	selected := make([]string, 0, len(targetDecls))
	for _, idx := range targetDecls {
		selected = append(selected, strings.Join(lines[starts[idx]:declEnd[idx]], "\n"))
	}
	writeProbeFile(t, tmpDir, targetRel, strings.Join(selected, "\n"))
	files := make([]string, 0, len(support)+1)
	for _, spec := range support {
		files = append(files, spec.rel)
	}
	files = append(files, targetRel)
	return toolchainSubsetOutcome(t, tmpDir, checker, files, timeout)
}

func writeProbeFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestToolchainCheckerProbeHelpersStayDeterministic(t *testing.T) {
	if got, want := partitionStrings([]string{"a", "b", "c", "d"}, 2), [][]string{{"a", "b"}, {"c", "d"}}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("partitionStrings() = %v, want %v", got, want)
	}
	if got, want := subtractStrings([]string{"a", "b", "c"}, []string{"b"}), []string{"a", "c"}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("subtractStrings() = %v, want %v", got, want)
	}
	if got, want := partitionInts([]int{0, 1, 2, 3}, 2), [][]int{{0, 1}, {2, 3}}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("partitionInts() = %v, want %v", got, want)
	}
	if got, want := subtractInts([]int{0, 1, 2}, []int{1}), []int{0, 2}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("subtractInts() = %v, want %v", got, want)
	}
	if got, want := toolchainDeclStarts([]string{
		"pub fn a() -> Int {",
		"  1",
		"}",
		"pub struct B {",
		"}",
	}), []int{0, 3}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("toolchainDeclStarts() = %v, want %v", got, want)
	}
}
