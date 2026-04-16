package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunEmitsStructuredCheckResult(t *testing.T) {
	var stdout bytes.Buffer
	stdin := strings.NewReader(`{"source":"fn id<T>(value: T) -> T { value }\nfn main() { let answer = id::<Int>(1) }\n"}`)
	if err := run(stdin, &stdout); err != nil {
		t.Fatalf("run error: %v", err)
	}
	var resp checkResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Summary.Errors != 0 {
		t.Fatalf("summary errors = %d, want 0", resp.Summary.Errors)
	}
	if len(resp.TypedNodes) == 0 {
		t.Fatalf("typedNodes = %#v, want non-empty result", resp.TypedNodes)
	}
	if len(resp.Bindings) == 0 {
		t.Fatalf("bindings = %#v, want binding facts", resp.Bindings)
	}
	if len(resp.Symbols) == 0 {
		t.Fatalf("symbols = %#v, want symbol facts", resp.Symbols)
	}
	if len(resp.Instantiations) == 0 {
		t.Fatalf("instantiations = %#v, want generic instantiation facts", resp.Instantiations)
	}
}

func TestRunRejectsInvalidJSON(t *testing.T) {
	var stdout bytes.Buffer
	err := run(strings.NewReader("{"), &stdout)
	if err == nil || !strings.Contains(err.Error(), "decode checker request") {
		t.Fatalf("run error = %v, want decode error", err)
	}
}

func TestRunChecksGenericBoundsAndInterfaceExtends(t *testing.T) {
	var goodOut bytes.Buffer
	goodIn := strings.NewReader(`{"source":"interface Named { fn name(self) -> String }\ninterface Tagged { Named\n fn tag(self) -> String }\nstruct User { name: String fn name(self) -> String { self.name } fn tag(self) -> String { \"user\" } }\nfn display<T: Tagged>(value: T) -> String { value.name() }\nfn main() { let user: User = User { name: \"Ada\" } let label: String = display(user) }\n"}`)
	if err := run(goodIn, &goodOut); err != nil {
		t.Fatalf("run good error: %v", err)
	}
	var goodResp checkResponse
	if err := json.Unmarshal(goodOut.Bytes(), &goodResp); err != nil {
		t.Fatalf("decode good response: %v", err)
	}
	if goodResp.Summary.Errors != 0 {
		t.Fatalf("good summary errors = %d, want 0", goodResp.Summary.Errors)
	}
	if len(goodResp.Instantiations) == 0 {
		t.Fatalf("good instantiations = %#v, want generic call facts", goodResp.Instantiations)
	}

	var badOut bytes.Buffer
	badIn := strings.NewReader(`{"source":"interface Named { fn name(self) -> String }\nstruct User { age: Int }\nfn display<T: Named>(value: T) -> String { value.name() }\nfn main() { let user: User = User { age: 37 } let label: String = display(user) }\n"}`)
	if err := run(badIn, &badOut); err != nil {
		t.Fatalf("run bad error: %v", err)
	}
	var badResp checkResponse
	if err := json.Unmarshal(badOut.Bytes(), &badResp); err != nil {
		t.Fatalf("decode bad response: %v", err)
	}
	if badResp.Summary.Errors == 0 {
		t.Fatalf("bad summary errors = %d, want > 0", badResp.Summary.Errors)
	}
}
