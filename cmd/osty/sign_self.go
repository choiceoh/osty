package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/osty/osty/internal/toolchain/selfhostcache"
)

// runSignSelf handles `osty sign-self` — the producer-side
// counterpart to `osty manifest-self` + the `OSTY_SELF_TRUSTED_KEY`
// verification path on the resolver. Two modes:
//
//	osty sign-self genkey [--out PATH]
//	    Generate a fresh ed25519 keypair. Writes the hex-encoded
//	    private key to PATH (or stdout) and prints the matching
//	    public key to stdout (so CI can capture it via env / vars).
//
//	osty sign-self --manifest PATH --key PATH [--out PATH]
//	    Load a manifest written by `osty manifest-self`, sign its
//	    raw bytes (no re-marshal — the resolver verifies against the
//	    canonical on-the-wire encoding), and write the hex-encoded
//	    detached signature. Defaults --out to `<manifest>.sig`.
//
// The CLI never reads from network. CI workflows are responsible for
// publishing manifest + signature to the registry layout
// `<base>/<key>.json` + `<base>/<key>.json.sig`.
func runSignSelf(args []string, _ cliFlags) {
	if len(args) >= 1 && args[0] == "genkey" {
		runSignSelfGenkey(args[1:])
		return
	}

	fs := flag.NewFlagSet("sign-self", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, signSelfUsage())
		fs.PrintDefaults()
	}
	var (
		manifestPath string
		keyPath      string
		outPath      string
	)
	fs.StringVar(&manifestPath, "manifest", "", "path to the manifest JSON to sign")
	fs.StringVar(&keyPath, "key", "", "path to the hex-encoded ed25519 private key")
	fs.StringVar(&outPath, "out", "", "output path for the hex-encoded signature; defaults to <manifest>.sig")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if manifestPath == "" {
		fmt.Fprintln(os.Stderr, "osty sign-self: --manifest is required")
		fs.Usage()
		os.Exit(2)
	}
	if keyPath == "" {
		fmt.Fprintln(os.Stderr, "osty sign-self: --key is required")
		fs.Usage()
		os.Exit(2)
	}

	body, err := os.ReadFile(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty sign-self: read manifest: %v\n", err)
		os.Exit(1)
	}
	priv, err := loadPrivateKey(keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty sign-self: load key: %v\n", err)
		os.Exit(1)
	}

	sig := ed25519.Sign(priv, body)
	hexSig := hex.EncodeToString(sig) + "\n"

	target := outPath
	if target == "" {
		target = manifestPath + selfhostcache.SignatureSuffix
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "osty sign-self: mkdir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(target, []byte(hexSig), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "osty sign-self: write signature: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("signature:   %s\n", target)
	pub := priv.Public().(ed25519.PublicKey)
	fmt.Printf("public-key:  %s\n", hex.EncodeToString(pub))
}

// runSignSelfGenkey handles the `osty sign-self genkey` mode.
// Splitting the flag-set keeps `genkey` from polluting the main
// usage text with --manifest / --key when only --out applies.
func runSignSelfGenkey(args []string) {
	fs := flag.NewFlagSet("sign-self genkey", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty sign-self genkey [--out PATH]")
		fs.PrintDefaults()
	}
	var outPath string
	fs.StringVar(&outPath, "out", "", "write hex-encoded private key to PATH; defaults to stdout")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty sign-self genkey: %v\n", err)
		os.Exit(1)
	}
	pubHex := hex.EncodeToString(pub)
	privHex := hex.EncodeToString(priv) + "\n"

	if outPath == "" {
		// Private key on stdout for piping into a secret store —
		// public key on stderr so it doesn't pollute the captured
		// secret payload.
		fmt.Fprintf(os.Stderr, "public-key:  %s\n", pubHex)
		os.Stdout.Write([]byte(privHex))
		return
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "osty sign-self genkey: mkdir: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, []byte(privHex), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "osty sign-self genkey: write key: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("private-key: %s\n", outPath)
	fmt.Printf("public-key:  %s\n", pubHex)
}

// loadPrivateKey reads a hex-encoded ed25519 private key (128 hex
// chars / 64 bytes) from `path`. Trailing whitespace is tolerated
// so a file written by `genkey` (which appends a newline) round-
// trips without a manual trim.
func loadPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if len(trimmed) != ed25519.PrivateKeySize*2 {
		return nil, fmt.Errorf("private key not %d hex chars (got %d)", ed25519.PrivateKeySize*2, len(trimmed))
	}
	bytes, err := hex.DecodeString(trimmed)
	if err != nil {
		return nil, fmt.Errorf("hex decode: %w", err)
	}
	return ed25519.PrivateKey(bytes), nil
}

func signSelfUsage() string {
	return strings.TrimSpace(`
osty sign-self --manifest PATH --key PATH [--out PATH]
osty sign-self genkey [--out PATH]
    Sign a manifest produced by ` + "`osty manifest-self`" + ` with an
    ed25519 private key (default key encoding: 128-char hex). The
    detached signature is written next to the manifest as
    <manifest>.sig and verified by clients that have set
    OSTY_SELF_TRUSTED_KEY to the matching public key.
    Use ` + "`genkey`" + ` to mint a fresh keypair. The private half
    should be stored as a CI secret; the public half should be
    distributed to clients via OSTY_SELF_TRUSTED_KEY.
`)
}
