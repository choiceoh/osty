package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osty/osty/internal/tomlparse"
)

// runLogin implements `osty login [--registry NAME]` — store an API
// token for a registry so subsequent `osty publish` / `osty yank`
// calls don't require --token or $OSTY_PUBLISH_TOKEN. Tokens are
// written to ~/.osty/credentials.toml with 0600 perms (best effort
// on Windows).
//
// Token sources, in order:
//   - --token T (explicit)
//   - $OSTY_PUBLISH_TOKEN
//   - read from stdin (so `echo $TOKEN | osty login` works)
//
// runLogout removes a stored token. With --all it clears every
// stored credential.
//
// Exit codes: 0 success, 1 I/O failure, 2 missing token.
func runLogin(args []string, cliF cliFlags) {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty login [--registry NAME] [--token T]")
		fmt.Fprintln(os.Stderr, "       (with no --token, reads from $OSTY_PUBLISH_TOKEN or stdin)")
	}
	var (
		regName string
		token   string
	)
	fs.StringVar(&regName, "registry", "", "registry name (defaults to the built-in default)")
	fs.StringVar(&token, "token", "", "API token to store")
	_ = fs.Parse(args)

	if token == "" {
		token = os.Getenv("OSTY_PUBLISH_TOKEN")
	}
	if token == "" {
		// Read a single line from stdin. Detect the no-input case
		// (terminal with nothing typed) so we don't silently store
		// an empty token.
		fmt.Fprint(os.Stderr, "token: ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && err != io.EOF {
			fmt.Fprintf(os.Stderr, "osty login: %v\n", err)
			os.Exit(1)
		}
		token = strings.TrimSpace(line)
	}
	if token == "" {
		fmt.Fprintln(os.Stderr, "osty login: no token provided")
		os.Exit(2)
	}

	creds, err := loadCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty login: %v\n", err)
		os.Exit(1)
	}
	creds[regName] = token
	if err := writeCredentials(creds); err != nil {
		fmt.Fprintf(os.Stderr, "osty login: %v\n", err)
		os.Exit(1)
	}
	label := regName
	if label == "" {
		label = "(default)"
	}
	fmt.Printf("Token stored for registry %s in %s\n", label, credentialsPath())
	_ = cliF
}

// runLogout deletes a stored token.
func runLogout(args []string, cliF cliFlags) {
	fs := flag.NewFlagSet("logout", flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "usage: osty logout [--registry NAME] [--all]")
	}
	var (
		regName string
		all     bool
	)
	fs.StringVar(&regName, "registry", "", "registry name (defaults to the built-in default)")
	fs.BoolVar(&all, "all", false, "remove every stored token")
	_ = fs.Parse(args)

	if all {
		if err := os.Remove(credentialsPath()); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "osty logout: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("All tokens removed")
		return
	}
	creds, err := loadCredentials()
	if err != nil {
		fmt.Fprintf(os.Stderr, "osty logout: %v\n", err)
		os.Exit(1)
	}
	if _, ok := creds[regName]; !ok {
		label := regName
		if label == "" {
			label = "(default)"
		}
		fmt.Printf("No token stored for registry %s\n", label)
		return
	}
	delete(creds, regName)
	if err := writeCredentials(creds); err != nil {
		fmt.Fprintf(os.Stderr, "osty logout: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Token removed")
	_ = cliF
}

// credentialsPath returns the on-disk location of the credentials
// file. The path mirrors the cache layout used by pkgmgr (~/.osty/...)
// so users have one directory to back up / clean.
func credentialsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to the cwd; better than failing — the user can
		// still operate, the file just lands in an unusual spot.
		return filepath.Join(".", ".osty-credentials.toml")
	}
	return filepath.Join(home, ".osty", "credentials.toml")
}

// loadCredentials reads the credentials file. A missing file returns
// an empty map; malformed contents surface as an error so we don't
// silently overwrite a user's tokens.
func loadCredentials() (map[string]string, error) {
	data, err := os.ReadFile(credentialsPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	root, err := tomlparse.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", credentialsPath(), err)
	}
	out := map[string]string{}
	regsV, ok := root.Get("registries")
	if !ok || regsV.Tbl == nil {
		return out, nil
	}
	for _, k := range regsV.Tbl.Keys {
		v, _ := regsV.Tbl.Get(k)
		if v.Tbl == nil {
			continue
		}
		tokV, ok := v.Tbl.Get("token")
		if !ok || tokV.Str == nil {
			continue
		}
		// "default" in the on-disk file maps back to the empty key so
		// callers can pass --registry "" naturally.
		key := k
		if key == "default" {
			key = ""
		}
		out[key] = *tokV.Str
	}
	return out, nil
}

// writeCredentials writes creds atomically (best effort: a temp file
// in the same dir + rename). The file is chmod 0600 to discourage
// accidental disclosure on shared machines.
func writeCredentials(creds map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(credentialsPath()), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# osty credential store. Edit by hand at your own risk.\n")
	b.WriteString("# Per-registry tokens; \"default\" maps to the unnamed default registry.\n\n")
	keys := make([]string, 0, len(creds))
	for k := range creds {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		display := k
		if display == "" {
			display = "default"
		}
		fmt.Fprintf(&b, "[registries.%s]\n", display)
		fmt.Fprintf(&b, "token = %q\n\n", creds[k])
	}
	tmp := credentialsPath() + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, credentialsPath())
}

// credentialFromStore returns the stored token for `name`, or "" when
// none is configured / the file can't be read. Used by `osty publish`
// and `osty yank` as a fallback below env + manifest sources.
func credentialFromStore(name string) string {
	creds, err := loadCredentials()
	if err != nil {
		return ""
	}
	return creds[name]
}
