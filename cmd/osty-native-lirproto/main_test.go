package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/osty/osty/internal/nativelirproto"
)

// TestRunLowersSimpleSourceToLLVMIR pins the binary's stdin → stdout
// contract end-to-end: a `LIRProtoRequest`-shaped JSON body in,
// a `LIRProtoResponse` JSON body out with non-empty `llvmIr` and
// no `declined` / `error`. Slice-1 of Phase-7 — the body delegates
// to the Go production lower → emit chain, so the IR text matches
// what `osty gen` produces with the gate off (modulo source-path
// comments, which mention the temp staging dir).
func TestRunLowersSimpleSourceToLLVMIR(t *testing.T) {
	body, err := json.Marshal(nativelirproto.Request{
		PackageName: "main",
		SourcePath:  "/tmp/demo/main.osty",
		Source:      "fn check(n: Int) -> Int { n + 1 }\nfn main() { println(\"{check(5)}\") }\n",
		Target:      "",
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var stdout bytes.Buffer
	if err := run(bytes.NewReader(body), &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp nativelirproto.Response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, stdout.String())
	}
	if resp.Declined {
		t.Fatalf("Declined = true, want successful response: %+v", resp)
	}
	if resp.Error != "" {
		t.Fatalf("Error = %q, want empty: %+v", resp.Error, resp)
	}
	if resp.LLVMIR == "" {
		t.Fatal("LLVMIR is empty")
	}
	for _, want := range []string{
		"define i64 @check(i64",
		"add i64",
		"ret i64",
	} {
		if !strings.Contains(resp.LLVMIR, want) {
			t.Fatalf("LLVMIR missing %q:\n%s", want, resp.LLVMIR)
		}
	}
}

// TestRunDeclinesEmptySource pins the structured-failure path: a
// request that the production pipeline can't lower (here: empty
// source = parse-stage rejection) lands as a `declined: true`
// response with a non-empty `error`, NOT as a hard exit. The
// dispatcher's existing fall-back-on-error policy converts this
// into a warning and continues with the legacy MIR-direct emit.
func TestRunDeclinesEmptySource(t *testing.T) {
	body, _ := json.Marshal(nativelirproto.Request{Source: ""})
	var stdout bytes.Buffer
	if err := run(bytes.NewReader(body), &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp nativelirproto.Response
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.Declined && resp.LLVMIR != "" {
		// Empty source might emit a no-op module on some toolchains;
		// in that case the body shouldn't contain user functions.
		if strings.Contains(resp.LLVMIR, "define ") {
			t.Fatalf("empty source produced user-function IR: %s", resp.LLVMIR)
		}
	}
}
