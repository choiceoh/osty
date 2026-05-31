package api

import (
	"encoding/json"
	"testing"
)

func TestPackageCheckFileJSONEncodesSourceAsRawText(t *testing.T) {
	src := "fn main() { println(\"hi\") }\n"
	payload, err := json.Marshal(CheckRequest{
		Package: &PackageCheckInput{
			Files: []PackageCheckFile{{
				Source:       []byte(src),
				Base:         7,
				Name:         "main.osty",
				Path:         "pkg/main.osty",
				SourceFileID: "src-1",
			}},
		},
	})
	if err != nil {
		t.Fatalf("marshal check request: %v", err)
	}

	var wire struct {
		Package struct {
			Files []struct {
				Source       string `json:"source"`
				Base         int    `json:"base"`
				Name         string `json:"name"`
				Path         string `json:"path"`
				SourceFileID string `json:"sourceFileId"`
			} `json:"files"`
		} `json:"package"`
	}
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatalf("decode marshaled request: %v\npayload: %s", err, payload)
	}
	if len(wire.Package.Files) != 1 {
		t.Fatalf("files = %#v, want one file", wire.Package.Files)
	}
	got := wire.Package.Files[0]
	if got.Source != src {
		t.Fatalf("source = %q, want raw source %q", got.Source, src)
	}
	if got.Base != 7 || got.Name != "main.osty" || got.Path != "pkg/main.osty" || got.SourceFileID != "src-1" {
		t.Fatalf("metadata = %#v, want fields preserved", got)
	}
}

func TestPackageCheckFileJSONDecodesRawSourceText(t *testing.T) {
	raw := []byte(`{"source":"fn helper() -> Int { 1 }\n","base":3,"name":"a.osty","path":"pkg/a.osty","sourceFileId":"src-2"}`)
	var file PackageCheckFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("unmarshal package file: %v", err)
	}
	if got, want := string(file.Source), "fn helper() -> Int { 1 }\n"; got != want {
		t.Fatalf("source = %q, want %q", got, want)
	}
	if file.Base != 3 || file.Name != "a.osty" || file.Path != "pkg/a.osty" || file.SourceFileID != "src-2" {
		t.Fatalf("metadata = %#v, want fields preserved", file)
	}
}
