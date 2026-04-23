package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/selfhost/api"
)

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(stdin io.Reader, stdout io.Writer) error {
	var req api.CheckRequest
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		return fmt.Errorf("decode checker request: %w", err)
	}
	var (
		checked api.CheckResult
		err     error
	)
	if req.Package != nil {
		checked, err = selfhost.CheckPackageStructured(*req.Package)
		if err != nil {
			return fmt.Errorf("check package request: %w", err)
		}
	} else {
		checked = selfhost.CheckSourceStructured([]byte(req.Source))
	}
	return json.NewEncoder(stdout).Encode(checked)
}
