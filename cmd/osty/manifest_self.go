package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/osty/osty/internal/toolchain/selfhostcache"
)

// runManifestSelf emits a JSON manifest describing a built osty-self
// binary so a CI pipeline can publish the (manifest, binary) pair to
// the artifact registry consumed by `selfhostcache.HTTPFetcher`.
//
// Inputs (flags):
//
//	--bin PATH          required; the freshly built osty-self binary.
//	--triple TRIPLE     defaults to the running host triple.
//	--osty-version VER  optional; embedded into the manifest as a
//	                    human-readable provenance hint.
//	--binary-url URL    required for upload; the resolver follows the
//	                    URL relative to OSTY_SELF_REGISTRY_URL.
//	--out PATH          where to write the manifest JSON; defaults to
//	                    stdout when unset.
//
// The CLI does not upload anything itself — that is the CI workflow's
// responsibility. The published manifest path inside the registry
// must follow the `<key>.json` layout that `HTTPFetcher.Fetch`
// derives from the Key.
func runManifestSelf(args []string, _ cliFlags) {
	fs := flag.NewFlagSet("manifest-self", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, manifestSelfUsage())
		fs.PrintDefaults()
	}
	var (
		binPath     string
		triple      string
		ostyVersion string
		binaryURL   string
		outPath     string
	)
	fs.StringVar(&binPath, "bin", "", "path to the osty-self binary to describe")
	fs.StringVar(&triple, "triple", selfhostcache.HostTriple(), "host triple stamped into the manifest Key")
	fs.StringVar(&ostyVersion, "osty-version", "", "optional human-readable provenance string")
	fs.StringVar(&binaryURL, "binary-url", "", "URL the resolver should GET to fetch the binary; relative URLs resolve against OSTY_SELF_REGISTRY_URL")
	fs.StringVar(&outPath, "out", "", "write manifest JSON to PATH; defaults to stdout")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if binPath == "" {
		fmt.Fprintln(os.Stderr, "osty manifest-self: --bin is required")
		fs.Usage()
		os.Exit(2)
	}
	if binaryURL == "" {
		fmt.Fprintln(os.Stderr, "osty manifest-self: --binary-url is required")
		fs.Usage()
		os.Exit(2)
	}

	root, err := selfhostcache.LocateProjectRoot(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty manifest-self: locate project root: %v\n", err)
		os.Exit(1)
	}
	key, err := selfhostcache.ComputeKey(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty manifest-self: compute key: %v\n", err)
		os.Exit(1)
	}
	// Override the auto-detected triple when the caller wants to
	// describe a cross-built artifact (Linux runner stamping a
	// darwin-arm64 manifest).
	if strings.TrimSpace(triple) != "" {
		key.Triple = triple
	}

	sum, err := hashFile(binPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty manifest-self: hash binary: %v\n", err)
		os.Exit(1)
	}

	manifest := selfhostcache.Manifest{
		Version:      selfhostcache.ManifestVersion,
		Key:          key.String(),
		BinarySHA256: sum,
		BinaryURL:    binaryURL,
		OstyVersion:  ostyVersion,
		CreatedAt:    time.Now().UTC().Truncate(time.Second),
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty manifest-self: encode manifest: %v\n", err)
		os.Exit(1)
	}
	body = append(body, '\n')

	if outPath == "" {
		_, _ = os.Stdout.Write(body)
		return
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "osty manifest-self: mkdir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, body, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "osty manifest-self: write: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("manifest:    %s\n", outPath)
	fmt.Printf("key:         %s\n", manifest.Key)
	fmt.Printf("sha256:      %s\n", manifest.BinarySHA256)
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func manifestSelfUsage() string {
	return strings.TrimSpace(fmt.Sprintf(`
osty manifest-self --bin PATH --binary-url URL [--triple TRIPLE] [--osty-version VER] [--out PATH]
    Emit a JSON manifest describing a built osty-self binary so a CI
    workflow can publish it to an OSTY_SELF_REGISTRY_URL backend
    consumed by selfhostcache.HTTPFetcher (manifest schema v%d).
    Defaults --triple to the running host (%s).
`, selfhostcache.ManifestVersion, runtime.GOOS+"-"+runtime.GOARCH))
}
