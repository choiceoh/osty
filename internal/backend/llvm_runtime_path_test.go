package backend

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBundledRuntimeFilepathBaseAndExt(t *testing.T) {
	parallelClangBackendTest(t)

	dir := t.TempDir()
	runtimePath := filepath.Join(dir, bundledRuntimeSourceName)
	harnessPath := filepath.Join(dir, "runtime_filepath_harness.c")
	binaryPath := filepath.Join(dir, "runtime_filepath_harness")
	if err := os.WriteFile(runtimePath, []byte(bundledRuntimeSource), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", runtimePath, err)
	}

	harness := `#include <stdio.h>

const char *osty_rt_path_filepath_Base(const char *path);
const char *osty_rt_path_filepath_Ext(const char *path);

static void dump(const char *label, const char *path) {
    const char *base = osty_rt_path_filepath_Base(path);
    const char *ext = osty_rt_path_filepath_Ext(base);
    printf("%s base=%s ext=%s\n", label, base, ext);
}

int main(void) {
    dump("unix-file", "/tmp/src/module.osty");
    dump("unix-dir", "/tmp/src/");
    dump("empty", "");
    dump("no-ext", "Makefile");
    dump("multi-ext", "archive.tar.gz");
    dump("win-file", "C:\\tmp\\main.osty");
    return 0;
}
`
	if err := os.WriteFile(harnessPath, []byte(harness), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", harnessPath, err)
	}

	buildOutput, err := runtimeClangCommand("-std=c11", runtimePath, harnessPath, "-o", binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("clang failed: %v\n%s", err, buildOutput)
	}
	runOutput, err := exec.Command(binaryPath).CombinedOutput()
	if err != nil {
		t.Fatalf("running %q failed: %v\n%s", binaryPath, err, runOutput)
	}

	want := "" +
		"unix-file base=module.osty ext=.osty\n" +
		"unix-dir base=src ext=\n" +
		"empty base=. ext=.\n" +
		"no-ext base=Makefile ext=\n" +
		"multi-ext base=archive.tar.gz ext=.gz\n" +
		"win-file base=main.osty ext=.osty\n"
	if got := string(runOutput); got != want {
		t.Fatalf("runtime filepath stdout = %q, want %q", got, want)
	}
}
