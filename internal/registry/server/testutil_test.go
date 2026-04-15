package server

import (
	"io"
	"log"
	"testing"
)

// nopLogger returns a *log.Logger that discards output. Used in
// tests so publish/download traces don't pollute `go test -v` output.
func nopLogger(t *testing.T) *log.Logger {
	t.Helper()
	return log.New(io.Discard, "", 0)
}
