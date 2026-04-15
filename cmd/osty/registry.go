package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/osty/osty/internal/registry"
)

func runRegistry(args []string, cliF cliFlags) {
	if len(args) == 0 || args[0] != "serve" {
		fmt.Fprintln(os.Stderr, "usage: osty registry serve [--addr HOST:PORT] [--root DIR] [--token T]")
		os.Exit(2)
	}
	runRegistryServe(args[1:], cliF)
}

func runRegistryServe(args []string, cliF cliFlags) {
	fs := flag.NewFlagSet("registry serve", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty registry serve [--addr HOST:PORT] [--root DIR] [--token T] [--allow-anonymous-writes]")
	}
	var (
		addr       string
		root       string
		token      string
		allowAnon  bool
		maxUploadM int
	)
	fs.StringVar(&addr, "addr", "127.0.0.1:7878", "address to listen on")
	fs.StringVar(&root, "root", filepath.Join(".osty", "registry"), "registry data directory")
	fs.StringVar(&token, "token", os.Getenv("OSTY_REGISTRY_TOKEN"), "bearer token for publish/yank (defaults to $OSTY_REGISTRY_TOKEN)")
	fs.BoolVar(&allowAnon, "allow-anonymous-writes", false, "accept publish/yank without checking bearer tokens")
	fs.IntVar(&maxUploadM, "max-upload-mb", int(registry.DefaultMaxUploadBytes>>20), "maximum package upload size in MiB")
	_ = fs.Parse(args)
	if fs.NArg() != 0 {
		fs.Usage()
		os.Exit(2)
	}
	if token == "" && !allowAnon {
		fmt.Fprintln(os.Stderr, "osty registry serve: pass --token, set $OSTY_REGISTRY_TOKEN, or use --allow-anonymous-writes for local-only testing")
		os.Exit(2)
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty registry serve: %v\n", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(absRoot, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "osty registry serve: %v\n", err)
		os.Exit(1)
	}
	handler := registry.NewServer(registry.NewFileStore(absRoot))
	if !allowAnon {
		handler.Authorize = registry.BearerTokenAuth(token)
	}
	if maxUploadM > 0 {
		handler.MaxUploadBytes = int64(maxUploadM) << 20
	}
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	fmt.Printf("Serving osty registry at %s\n", displayRegistryURL(addr))
	fmt.Printf("Data root: %s\n", absRoot)
	if allowAnon {
		fmt.Println("Writes: anonymous")
	} else {
		fmt.Println("Writes: bearer token required")
	}
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "osty registry serve: %v\n", err)
		os.Exit(1)
	}
	_ = cliF
}

func displayRegistryURL(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") {
			return "http://127.0.0.1" + addr
		}
		return "http://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
}
