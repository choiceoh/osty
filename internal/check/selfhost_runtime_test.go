package check

import (
	"bytes"
	"testing"

	"github.com/osty/osty/internal/parser"
	"github.com/osty/osty/internal/selfhost"
	"github.com/osty/osty/internal/stdlib"
)

func TestSelfhostFileSourcePreservesStdlibGenericFunctions(t *testing.T) {
	src := []byte(`use std.testing

fn selfhostRuntimeUsesGenericStdlibStubs() {
    testing.assertEq(1, 1)
    testing.assertEq("left", "left")
}
`)
	file, parseDiags := parser.ParseDiagnostics(src)
	if len(parseDiags) != 0 {
		t.Fatalf("parse diagnostics: %v", parseDiags)
	}
	checkedSrc := selfhostFileSource(file, nil, src, stdlib.LoadCached())
	if !bytes.Contains(checkedSrc.source, []byte("use testing {")) {
		t.Fatalf("synthetic stdlib import missing:\n%s", checkedSrc.source)
	}
	if !bytes.Contains(checkedSrc.source, []byte("fn assertEq<T>(")) {
		t.Fatalf("generic stdlib function was not preserved:\n%s", checkedSrc.source)
	}
	checked := selfhost.CheckSourceStructured(checkedSrc.source)
	if checked.Summary.Errors != 0 {
		t.Fatalf(
			"selfhost checker summary = errors:%d accepted:%d assignments:%d",
			checked.Summary.Errors,
			checked.Summary.Accepted,
			checked.Summary.Assignments,
		)
	}
}
