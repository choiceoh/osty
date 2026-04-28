package llvmgen

import (
	"strings"
	"testing"
)

func TestStdCompressGzipCallsRouteToRuntimeInAST(t *testing.T) {
	file := parseLLVMGenFile(t, `use std.compress as compress

fn roundTrip(data: Bytes) -> Result<Bytes, Error> {
    let encoded = compress.gzip.encode(data)
    compress.gzip.decode(encoded)
}
`)

	ir, err := generateFromAST(file, Options{
		PackageName: "main",
		SourcePath:  "/tmp/std_compress_gzip_ast.osty",
	})
	if err != nil {
		t.Fatalf("generateFromAST: %v", err)
	}
	got := string(ir)
	for _, want := range []string{
		"declare ptr @osty_rt_compress_gzip_encode(ptr)",
		"declare ptr @osty_rt_compress_gzip_decode(ptr)",
		"call ptr @osty_rt_compress_gzip_encode(",
		"call ptr @osty_rt_compress_gzip_decode(",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in IR:\n%s", want, got)
		}
	}
}
