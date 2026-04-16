package pkgmgr

import (
	"reflect"
	"testing"

	"github.com/osty/osty/internal/lockfile"
)

func TestSelfhostMarshalLockMatchesGoLockfile(t *testing.T) {
	lock := &lockfile.Lock{
		Version: lockfile.SchemaVersion,
		Packages: []lockfile.Package{
			{
				Name:     "zed",
				Version:  "1.0.0",
				Source:   "registry+default",
				Checksum: "sha256:zed",
				Dependencies: []lockfile.Dependency{
					{Name: "beta", Version: "0.2.0"},
					{Name: "alpha", Version: "0.1.0", Source: "registry+default"},
				},
			},
			{Name: "alpha", Version: "0.1.0", Source: "path+../a"},
		},
	}

	got, err := SelfhostMarshalLock(lock)
	if err != nil {
		t.Fatal(err)
	}
	want := lockfile.Marshal(lock)
	if string(got) != string(want) {
		t.Fatalf("selfhost marshal mismatch\nwant:\n%s\ngot:\n%s", want, got)
	}
}

func TestSelfhostDiffLockMatchesGoDiff(t *testing.T) {
	old := &lockfile.Lock{
		Version: lockfile.SchemaVersion,
		Packages: []lockfile.Package{
			{Name: "alpha", Version: "1.0.0", Source: "registry+default", Checksum: "sha256:aaaaaaaaaaaaaaaa"},
			{Name: "beta", Version: "1.0.0", Source: "registry+old", Checksum: "sha256:bbbbbbbbbbbbbbbb"},
			{Name: "gamma", Version: "1.0.0", Source: "registry+default", Checksum: "sha256:cccccccccccccccc"},
			{Name: "zed", Version: "1.0.0", Source: "registry+default", Checksum: "sha256:dddddddddddddddd"},
		},
	}
	new := &lockfile.Lock{
		Version: lockfile.SchemaVersion,
		Packages: []lockfile.Package{
			{Name: "alpha", Version: "1.0.0", Source: "registry+default", Checksum: "sha256:eeeeeeeeeeeeeeee"},
			{Name: "beta", Version: "1.0.0", Source: "registry+new", Checksum: "sha256:bbbbbbbbbbbbbbbb"},
			{Name: "delta", Version: "0.1.0", Source: "registry+default", Checksum: "sha256:ffffffffffffffff"},
			{Name: "zed", Version: "1.1.0", Source: "registry+default", Checksum: "sha256:dddddddddddddddd"},
		},
	}

	got, err := SelfhostDiffLock(old, new)
	if err != nil {
		t.Fatal(err)
	}
	want := diffLockGo(old, new)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selfhost diff mismatch\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestSelfhostSelectRegistryCandidateHonorsLock(t *testing.T) {
	locked := &lockfile.Lock{
		Version: lockfile.SchemaVersion,
		Packages: []lockfile.Package{
			{Name: "json", Version: "1.4.0", Source: "registry+default", Checksum: "sha256:locked"},
		},
	}
	candidates := []SelfhostRegistryCandidate{
		{PackageName: "json", Version: "1.5.0", Checksum: "sha256:newer"},
		{PackageName: "json", Version: "1.4.0", Checksum: "sha256:locked"},
	}

	got, err := SelfhostSelectRegistryCandidate("json", "", "", "^1.0.0", candidates, locked)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Found || !got.FromLock || got.Version != "1.4.0" || got.Checksum != "sha256:locked" {
		t.Fatalf("unexpected decision: %#v", got)
	}
}

func TestSelfhostRegistryRequestAdapters(t *testing.T) {
	versions := SelfhostRegistryVersionsRequest("https://registry.example/", "json", "")
	if versions.Method != "GET" || versions.URL != "https://registry.example/v1/crates/json" {
		t.Fatalf("bad versions request: %#v", versions)
	}

	publish := SelfhostRegistryPublishRequest("https://registry.example/", "json", "1.2.3", "tok", "sha256:abc", "meta")
	if publish.Method != "PUT" ||
		publish.URL != "https://registry.example/v1/crates/json/1.2.3" ||
		publish.Authorization != "Bearer tok" ||
		publish.ContentType != "application/x-tar+gzip" ||
		publish.Checksum != "sha256:abc" ||
		publish.Metadata != "meta" {
		t.Fatalf("bad publish request: %#v", publish)
	}

	yank := SelfhostRegistryYankRequest("https://registry.example", "json", "1.2.3", "tok", true)
	unyank := SelfhostRegistryYankRequest("https://registry.example", "json", "1.2.3", "tok", false)
	if yank.Method != "DELETE" || yank.URL != "https://registry.example/v1/crates/json/1.2.3/yank" {
		t.Fatalf("bad yank request: %#v", yank)
	}
	if unyank.Method != "PUT" || unyank.URL != "https://registry.example/v1/crates/json/1.2.3/unyank" {
		t.Fatalf("bad unyank request: %#v", unyank)
	}
}

func TestSelfhostRankRegistryCandidates(t *testing.T) {
	candidates := []SelfhostRegistryCandidate{
		{PackageName: "json", Version: "0.9.0", Checksum: "sha256:old"},
		{PackageName: "json", Version: "1.2.0", Checksum: "sha256:new"},
		{PackageName: "json", Version: "1.1.0", Checksum: "sha256:mid"},
		{PackageName: "json", Version: "2.0.0", Checksum: "sha256:next"},
	}
	got, err := SelfhostRankRegistryCandidates("json", "json", "", "^1.0.0", candidates)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Version != "1.2.0" || got[1].Version != "1.1.0" {
		t.Fatalf("unexpected ranking: %#v", got)
	}
}

func TestSelfhostSourceHelpers(t *testing.T) {
	if got := SelfhostPathSourceURI("../lib"); got != "path+../lib" {
		t.Fatalf("bad path URI: %q", got)
	}
	if got := SelfhostRegistrySourceURI(""); got != "registry+default" {
		t.Fatalf("bad default registry URI: %q", got)
	}
	if got := SelfhostRegistrySourceURI("corp"); got != "registry+corp" {
		t.Fatalf("bad named registry URI: %q", got)
	}
	if got := SelfhostGitSourceURI("https://example.com/repo.git", "v1.0.0", "main", "abc"); got != "git+https://example.com/repo.git?tag=v1.0.0&branch=main&rev=abc" {
		t.Fatalf("bad git URI: %q", got)
	}
	if got := SelfhostGitCheckoutRef("", "v1.0.0", "main", ""); got != "refs/tags/v1.0.0" {
		t.Fatalf("bad checkout ref: %q", got)
	}
	if got := SelfhostSanitizeURL("https://example.com/a b"); got != "https___example.com_a_b" {
		t.Fatalf("bad sanitized URL: %q", got)
	}
}

func TestSelfhostVerifyChecksumMessagesMatchGoPolicy(t *testing.T) {
	if err := SelfhostVerifyChecksum("", "anything"); err != nil {
		t.Fatal(err)
	}
	err := SelfhostVerifyChecksum("sha256:abc", "sha256:def")
	if err == nil || err.Error() != "checksum mismatch:\n  want sha256:abc\n  got  sha256:def" {
		t.Fatalf("bad mismatch error: %v", err)
	}
	err = SelfhostVerifyChecksum("md5:abc", "sha256:abc")
	if err == nil || err.Error() != `checksum mismatch: want "md5:abc", got "sha256:abc" (unsupported algorithm)` {
		t.Fatalf("bad algorithm error: %v", err)
	}
}

func TestSelfhostLookupDependency(t *testing.T) {
	graph := []SelfhostDepLookupItem{
		{Name: "fastjson", GitURL: "https://github.com/acme/fastjson.git"},
		{Name: "leaf"},
	}
	manifest := []SelfhostDepLookupItem{
		{Name: "http", GitURL: "git@github.com:acme/http.git"},
	}

	if got := SelfhostNormalizeGitURL("git@github.com:acme/http.git/"); got != "github.com/acme/http" {
		t.Fatalf("bad normalized git URL: %q", got)
	}
	cases := map[string]string{
		"fastjson":                         "fastjson",
		"github.com/acme/fastjson":         "fastjson",
		"https://github.com/acme/http":     "http",
		"github.com/acme/leaf":             "leaf",
		"https://github.com/acme/http.git": "http",
	}
	for rawPath, want := range cases {
		got := SelfhostLookupDependency(rawPath, graph, manifest)
		if !got.Found || got.Name != want {
			t.Fatalf("lookup %q: want %q, got %#v", rawPath, want, got)
		}
	}
	if got := SelfhostLookupDependency("missing", graph, manifest); got.Found {
		t.Fatalf("unexpected lookup hit: %#v", got)
	}
}

func TestSelfhostTopoOrderMatchesGoOrder(t *testing.T) {
	graph := &Graph{Nodes: map[string]*ResolvedNode{
		"app":  {Name: "app", Deps: []string{"util"}},
		"util": {Name: "util", Deps: []string{"leaf"}},
		"leaf": {Name: "leaf"},
	}}
	want := (&resolver{graph: graph}).topoOrderGo()
	got := SelfhostTopoOrder(graph)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selfhost topo mismatch\nwant: %#v\ngot:  %#v", want, got)
	}
}

func TestSelfhostLockFromGraphMatchesGoProjection(t *testing.T) {
	graph := &Graph{Nodes: map[string]*ResolvedNode{
		"app": {
			Name:   "app",
			Source: &pathSource{path: "../app"},
			Fetched: &FetchedPackage{
				Version:  "1.0.0",
				Checksum: "sha256:app",
			},
			Deps: []string{"util"},
		},
		"util": {
			Name:   "util",
			Source: &pathSource{path: "../util"},
			Fetched: &FetchedPackage{
				Version:  "0.2.0",
				Checksum: "sha256:util",
			},
		},
	}}
	graph.Order = (&resolver{graph: graph}).topoOrderGo()

	got, err := SelfhostLockFromGraph(graph)
	if err != nil {
		t.Fatal(err)
	}
	want := lockFromGraphGo(graph)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selfhost lock projection mismatch\nwant: %#v\ngot:  %#v", want, got)
	}
	if projected := LockFromGraph(graph); !reflect.DeepEqual(projected, want) {
		t.Fatalf("public lock projection mismatch\nwant: %#v\ngot:  %#v", want, projected)
	}
}
