// Command osty-registry runs a self-hosted osty package registry
// that speaks the HTTP protocol defined in internal/registry/client.go.
//
// Usage:
//
//	osty-registry --root ./data [--addr :8080] [--tokens ./tokens.json]
//
// Flags:
//
//	--root    Directory used for package storage. Created if missing.
//	          Layout is documented at internal/registry/server/storage.go.
//	--addr    TCP address to listen on. Default ":8080".
//	--tokens  Path to a JSON file containing publish tokens. Omit for
//	          a read-only registry (publishes always 401). The file
//	          is re-read on SIGHUP so tokens can be rotated without a
//	          restart.
//	--max-upload-mb  Cap on accepted publish body size, in MiB.
//	                 Default 16.
//
// tokens.json schema:
//
//	{
//	  "tokens": [
//	    {"token": "abc123", "owner": "alice", "scopes": ["publish:*"]},
//	    {"token": "ci-bot", "owner": "ci", "scopes": ["publish:hello"]}
//	  ]
//	}
//
// On a publish, the server:
//
//   - Verifies the Bearer token against tokens.json.
//   - Requires a scope of "publish:<name>" or "publish:*".
//   - Reads the upload body (bounded by --max-upload-mb), sha256s it,
//     and rejects if the client's Osty-Checksum disagrees.
//   - Extracts osty.toml from the tarball to populate the published
//     version's dependency list in the index.
//   - Refuses to overwrite an already-published (name, version) pair.
//
// The HTTP surface is intentionally small — three client endpoints,
// a /health probe for load balancers, and a JSON package listing at
// "/". See internal/registry/server/server.go for the handler code.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/osty/osty/internal/registry/server"
)

func main() {
	fs := flag.NewFlagSet("osty-registry", flag.ExitOnError)
	var (
		addr       = fs.String("addr", ":8080", "TCP address to listen on")
		root       = fs.String("root", "", "package storage directory (required)")
		tokensPath = fs.String("tokens", "", "path to tokens.json (optional; absent = read-only)")
		maxMB      = fs.Int64("max-upload-mb", 16, "max publish body size in MiB")
	)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty-registry --root DIR [--addr :8080] [--tokens FILE]")
		fs.PrintDefaults()
	}
	_ = fs.Parse(os.Args[1:])

	if *root == "" {
		fmt.Fprintln(os.Stderr, "osty-registry: --root is required")
		os.Exit(2)
	}
	absRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty-registry: %v\n", err)
		os.Exit(1)
	}

	storage, err := server.NewStorage(absRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty-registry: storage: %v\n", err)
		os.Exit(1)
	}
	tokens, err := loadTokens(*tokensPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty-registry: tokens: %v\n", err)
		os.Exit(1)
	}
	logger := log.New(os.Stderr, "osty-registry ", log.LstdFlags)
	srv := server.New(server.Config{
		Storage:         storage,
		Tokens:          tokens,
		MaxTarballBytes: *maxMB << 20,
		Logger:          logger,
	})

	// Wire SIGHUP to a tokens.json re-read so operators can add or
	// rotate tokens without dropping in-flight requests. SIGINT /
	// SIGTERM drive a graceful shutdown.
	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv,
		ReadHeaderTimeout: 15 * time.Second,
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
	go func() {
		for sig := range signals {
			switch sig {
			case syscall.SIGHUP:
				logger.Printf("SIGHUP: reloading tokens from %q", *tokensPath)
				reloaded, err := server.LoadTokenDB(*tokensPath)
				if err != nil {
					logger.Printf("SIGHUP: token reload failed: %v", err)
					continue
				}
				// Swap the token table in place — the Server still holds
				// the original *TokenDB, which internally picks up new
				// entries via Replace.
				tokensInPlace(tokens, reloaded)
				logger.Printf("SIGHUP: tokens reloaded")
			case syscall.SIGINT, syscall.SIGTERM:
				logger.Printf("received %s, shutting down", sig)
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				_ = httpSrv.Shutdown(ctx)
				return
			}
		}
	}()

	logger.Printf("listening on %s (root=%s, tokens=%s)", *addr, absRoot, displayPath(*tokensPath))
	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Fatalf("listen: %v", err)
	}
}

// loadTokens reads tokens.json if a path was given, or returns an
// empty DB otherwise. An empty DB means no publishes succeed — the
// server is effectively read-only, which is the right default when
// an operator hasn't configured auth.
func loadTokens(path string) (*server.TokenDB, error) {
	if path == "" {
		return server.NewTokenDB(nil), nil
	}
	return server.LoadTokenDB(path)
}

// tokensInPlace copies the fresh token list into the live DB so the
// Server keeps the same *TokenDB pointer across reloads. Replace
// takes a write lock internally, so in-flight authorizations see a
// consistent snapshot.
func tokensInPlace(live, fresh *server.TokenDB) {
	live.Replace(fresh.Snapshot())
}

// displayPath renders a config-file path for logs, showing "<none>"
// when the operator omitted the flag so the read-only mode is
// unambiguous in the startup banner.
func displayPath(p string) string {
	if p == "" {
		return "<none>"
	}
	return p
}
