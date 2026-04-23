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
	var req api.ResolveRequest
	if err := json.NewDecoder(stdin).Decode(&req); err != nil {
		return fmt.Errorf("decode resolver request: %w", err)
	}
	var (
		resolved api.ResolveResult
		err      error
	)
	if req.Package != nil {
		resolved, err = selfhost.ResolvePackageStructured(*req.Package)
		if err != nil {
			return fmt.Errorf("resolve package request: %w", err)
		}
	} else {
		resolved = selfhost.ResolveSourceStructured([]byte(req.Source))
	}
	return json.NewEncoder(stdout).Encode(resolved)
}
