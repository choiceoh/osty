package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/osty/osty/internal/registry"
)

// runRegistry dispatches `osty registry <sub>` commands. The only
// subcommand today is `serve`, which runs a production-shaped
// registry backend against an on-disk storage root. Future
// subcommands (token management, index inspection) fit naturally
// under the same umbrella.
func runRegistry(args []string, _ cliFlags) {
	if len(args) == 0 {
		registryUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "serve":
		runRegistryServe(args[1:])
	case "add-token":
		// Kept as a thin helper so deployments can provision tokens
		// without hand-editing JSON.
		runRegistryAddToken(args[1:])
	default:
		registryUsage()
		os.Exit(2)
	}
}

func registryUsage() {
	fmt.Fprintln(os.Stderr, "usage: osty registry serve [--addr :8080] [--root DIR] [--tokens PATH] [--dev]")
	fmt.Fprintln(os.Stderr, "       osty registry add-token --tokens PATH --token VALUE [--subject NAME] [--all | --pkg NAME,...]")
}

// runRegistryServe stands up the HTTP backend. Flags:
//
//	--addr     listen address (default :8080)
//	--root     storage root (default ./registry-data)
//	--tokens   path to a TokenAuth JSON file
//	--dev      accept any non-empty bearer token (AllowAll). Convenient
//	           for local testing; refuses to start if --tokens is also
//	           set to avoid surprising operators.
//	--max-mb   upload cap in MiB (default 32, negative disables)
//
// Graceful shutdown: SIGINT / SIGTERM stop new connections and wait
// up to 10s for in-flight requests.
func runRegistryServe(args []string) {
	fs := flag.NewFlagSet("registry serve", flag.ExitOnError)
	var (
		addr    string
		root    string
		tokens  string
		devMode bool
		maxMB   int
	)
	fs.StringVar(&addr, "addr", ":8080", "listen address")
	fs.StringVar(&root, "root", "registry-data", "storage root directory")
	fs.StringVar(&tokens, "tokens", "", "path to tokens.json (required unless --dev)")
	fs.BoolVar(&devMode, "dev", false, "accept any non-empty bearer token")
	fs.IntVar(&maxMB, "max-mb", 32, "upload cap in MiB (negative disables)")
	_ = fs.Parse(args)

	if devMode && tokens != "" {
		fmt.Fprintln(os.Stderr, "osty registry serve: --dev and --tokens are mutually exclusive")
		os.Exit(2)
	}
	if !devMode && tokens == "" {
		fmt.Fprintln(os.Stderr, "osty registry serve: pass --tokens PATH (or --dev for local testing)")
		os.Exit(2)
	}

	store, err := registry.NewStorage(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty registry serve: storage: %v\n", err)
		os.Exit(1)
	}

	var auth registry.Authorizer
	if devMode {
		auth = registry.AllowAll{}
	} else {
		ta, err := registry.LoadTokenAuth(tokens)
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty registry serve: tokens: %v\n", err)
			os.Exit(1)
		}
		auth = ta
	}

	srv := &registry.Server{
		Storage:         store,
		Auth:            auth,
		MaxTarballBytes: int64(maxMB) << 20,
	}
	hs := &http.Server{
		Addr:              addr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Printf("osty registry listening on %s (root=%s, dev=%t)\n", addr, root, devMode)

	// Shutdown on signal.
	errCh := make(chan error, 1)
	go func() {
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		if err != nil {
			fmt.Fprintf(os.Stderr, "osty registry serve: %v\n", err)
			os.Exit(1)
		}
	case sig := <-sigs:
		fmt.Printf("received %s, shutting down...\n", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = hs.Shutdown(ctx)
	}
}

// runRegistryAddToken provisions a token in a TokenAuth JSON file,
// creating the file if missing. Appending (not replacing) keeps
// operator workflows simple: rerunning with a new --token adds
// another row.
func runRegistryAddToken(args []string) {
	fs := flag.NewFlagSet("registry add-token", flag.ExitOnError)
	var (
		path    string
		token   string
		subject string
		all     bool
		pkgs    string
	)
	fs.StringVar(&path, "tokens", "tokens.json", "path to the tokens.json file")
	fs.StringVar(&token, "token", "", "bearer token value (required)")
	fs.StringVar(&subject, "subject", "", "display name / identity for logs")
	fs.BoolVar(&all, "all", false, "grant permission to publish any package")
	fs.StringVar(&pkgs, "pkg", "", "comma-separated package names this token may publish")
	_ = fs.Parse(args)

	if token == "" {
		fmt.Fprintln(os.Stderr, "osty registry add-token: --token is required")
		os.Exit(2)
	}
	if !all && pkgs == "" {
		fmt.Fprintln(os.Stderr, "osty registry add-token: pass --all or --pkg NAME,...")
		os.Exit(2)
	}

	rec := registry.TokenRecord{Token: token, Subject: subject, AllowAll: all}
	if pkgs != "" {
		rec.Packages = splitCSV(pkgs)
	}

	// Load existing file (if any) and append the new record.
	ta, err := registry.LoadTokenAuth(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty registry add-token: %v\n", err)
		os.Exit(1)
	}
	ta.Add(rec)

	if err := registry.WriteTokenAuth(ta, path); err != nil {
		fmt.Fprintf(os.Stderr, "osty registry add-token: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("added token (subject=%q, all=%t, pkgs=%v) to %s\n",
		subject, all, rec.Packages, path)
}

// splitCSV breaks "a,b,c" into []string{"a","b","c"}, trimming
// empty entries so `--pkg a,,b` still gives [a b].
func splitCSV(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ',' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	return out
}
