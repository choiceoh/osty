package backend

import (
	"os/exec"
	"runtime"
)

func runtimeClangCommand(args ...string) *exec.Cmd {
	if runtime.GOOS != "windows" {
		args = append(args, "-lz", "-lm")
	}
	return exec.Command("clang", args...)
}
