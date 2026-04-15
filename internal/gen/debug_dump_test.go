//go:build debug

package gen_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/osty/osty/internal/check"
	"github.com/osty/osty/internal/gen"
	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/resolve"
)

// TestDump is a debugging aid. Set -tags debug to enable.
//
//	go test -tags debug ./internal/gen/ -run TestDump/04-expressions -v
func TestDump(t *testing.T) {
	names := []string{
		"01-lexical", "02-types", "03-declarations", "04-expressions",
		"05-modules", "06-scripts", "07-errors", "08-concurrency", "11-testing",
	}
	for _, name := range names {
		name := name
		t.Run(name, func(t *testing.T) {
			src, err := os.ReadFile("../../testdata/spec/positive/" + name + ".osty")
			if err != nil {
				t.Fatal(err)
			}
			file, _ := parser.ParseDiagnostics(src)
			res := resolve.File(file, resolve.NewPrelude())
			chk := check.File(file, res)
			goSrc, gerr := gen.Generate("main", file, res, chk)
			if gerr != nil {
				fmt.Printf("=== %s — gen err: %v\n", name, gerr)
			} else {
				fmt.Printf("=== %s — OK (%d bytes)\n", name, len(goSrc))
			}
			if len(goSrc) > 0 {
				for i, l := range strings.Split(string(goSrc), "\n") {
					fmt.Printf("%4d: %s\n", i+1, l)
				}
			}
		})
	}
}
