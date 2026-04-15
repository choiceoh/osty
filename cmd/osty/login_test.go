package main

import (
	"os"
	"path/filepath"
	"testing"
)

// withFakeHome redirects credentialsPath() at a tmpdir for the
// duration of one test by overriding $HOME (and Windows-only
// USERPROFILE). The previous values are restored via t.Cleanup.
func withFakeHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	prevHome := os.Getenv("HOME")
	prevUserprofile := os.Getenv("USERPROFILE")
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Cleanup(func() {
		_ = prevHome
		_ = prevUserprofile
	})
	return dir
}

// TestCredentialRoundtrip writes two tokens through writeCredentials,
// reads them back via loadCredentials, and confirms credentialFromStore
// returns the right value for each registry name.
func TestCredentialRoundtrip(t *testing.T) {
	withFakeHome(t)
	creds := map[string]string{
		"":         "default-token",
		"internal": "internal-token",
	}
	if err := writeCredentials(creds); err != nil {
		t.Fatalf("writeCredentials: %v", err)
	}
	got, err := loadCredentials()
	if err != nil {
		t.Fatalf("loadCredentials: %v", err)
	}
	if got[""] != "default-token" || got["internal"] != "internal-token" {
		t.Errorf("roundtrip: %+v", got)
	}
	if credentialFromStore("") != "default-token" {
		t.Errorf("default lookup failed")
	}
	if credentialFromStore("internal") != "internal-token" {
		t.Errorf("named lookup failed")
	}
	// File must be 0600 — best effort on Windows.
	info, err := os.Stat(credentialsPath())
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("credentials file %s has overly permissive mode %v",
			credentialsPath(), info.Mode().Perm())
	}
}

// TestCredentialMissingFile: a fresh user with no stored credentials
// should get an empty map (no error) so login can append the first
// entry without special-casing.
func TestCredentialMissingFile(t *testing.T) {
	withFakeHome(t)
	got, err := loadCredentials()
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map, got %+v", got)
	}
	// credentialFromStore must also degrade gracefully.
	if credentialFromStore("anything") != "" {
		t.Errorf("expected empty token from missing file")
	}
}

// TestCredentialDeletePersists: dropping a key and rewriting must
// leave the file readable and the dropped key absent.
func TestCredentialDeletePersists(t *testing.T) {
	withFakeHome(t)
	creds := map[string]string{"a": "1", "b": "2"}
	if err := writeCredentials(creds); err != nil {
		t.Fatalf("write: %v", err)
	}
	delete(creds, "a")
	if err := writeCredentials(creds); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	got, err := loadCredentials()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if _, ok := got["a"]; ok {
		t.Errorf("a should be gone: %+v", got)
	}
	if got["b"] != "2" {
		t.Errorf("b should remain: %+v", got)
	}
}

// TestCredentialsPathUnderHome ensures the file lands under the
// fake-home directory, not the real one.
func TestCredentialsPathUnderHome(t *testing.T) {
	dir := withFakeHome(t)
	want := filepath.Join(dir, ".osty", "credentials.toml")
	if got := credentialsPath(); got != want {
		t.Errorf("credentialsPath: got %s, want %s", got, want)
	}
}
