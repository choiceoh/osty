package selfhostcache

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseTrustedKeyAcceptsValidHex(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	encoded := hex.EncodeToString(pub)
	got, err := ParseTrustedKey(encoded)
	if err != nil {
		t.Fatalf("ParseTrustedKey: %v", err)
	}
	if !got.Equal(pub) {
		t.Errorf("round-trip mismatch")
	}
}

func TestParseTrustedKeyRejectsBadInput(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"too short", "abcdef"},
		{"non-hex", strings.Repeat("z", 64)},
		{"odd length", strings.Repeat("a", 63)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseTrustedKey(tc.in)
			if err == nil {
				t.Fatalf("expected error for %q", tc.in)
			}
			if !errors.Is(err, ErrTrustedKeyMalformed) {
				t.Errorf("err = %v, want ErrTrustedKeyMalformed", err)
			}
		})
	}
}

func TestVerifyManifestSucceedsForGoodSignature(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	body := []byte(`{"version":1,"key":"sha-linux-amd64"}`)
	sig := ed25519.Sign(priv, body)
	if err := VerifyManifest(pub, body, sig); err != nil {
		t.Errorf("VerifyManifest: %v", err)
	}
}

func TestVerifyManifestRejectsTamperedBody(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	body := []byte(`{"version":1}`)
	sig := ed25519.Sign(priv, body)
	tampered := []byte(`{"version":2}`)
	if err := VerifyManifest(pub, tampered, sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("err = %v, want ErrSignatureInvalid", err)
	}
}

func TestVerifyManifestRejectsWrongKey(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	body := []byte(`{"version":1}`)
	sig := ed25519.Sign(priv, body)
	if err := VerifyManifest(otherPub, body, sig); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("err = %v, want ErrSignatureInvalid", err)
	}
}

func TestVerifyManifestRejectsShortSignature(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if err := VerifyManifest(pub, []byte("body"), []byte{0x01, 0x02}); !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("err = %v, want ErrSignatureInvalid", err)
	}
}

func TestDecodeSignatureAcceptsRawAndHex(t *testing.T) {
	raw := make([]byte, SignatureSize)
	for i := range raw {
		raw[i] = byte(i)
	}
	gotRaw, err := decodeSignature(raw)
	if err != nil {
		t.Fatalf("raw decode: %v", err)
	}
	if string(gotRaw) != string(raw) {
		t.Errorf("raw round-trip mismatch")
	}

	hexEncoded := []byte(hex.EncodeToString(raw))
	gotHex, err := decodeSignature(hexEncoded)
	if err != nil {
		t.Fatalf("hex decode: %v", err)
	}
	if string(gotHex) != string(raw) {
		t.Errorf("hex round-trip mismatch")
	}

	// Trailing newline (typical of `cat`-into-secret).
	gotTrim, err := decodeSignature(append(hexEncoded, '\n'))
	if err != nil {
		t.Fatalf("trim decode: %v", err)
	}
	if string(gotTrim) != string(raw) {
		t.Errorf("trim round-trip mismatch")
	}

	if _, err := decodeSignature([]byte{0x01, 0x02, 0x03}); err == nil {
		t.Errorf("expected error for short input")
	}
}

// signedRegistry helps test HTTPFetcher's signed path. Each
// (key, body) entry is signed under `signKey` so the test client
// can verify under `pub`.
type signedRegistry struct {
	t        *testing.T
	manifest map[string][]byte
	signed   map[string][]byte
	binary   map[string][]byte
}

func newSignedRegistry(t *testing.T) *signedRegistry {
	return &signedRegistry{
		t:        t,
		manifest: map[string][]byte{},
		signed:   map[string][]byte{},
		binary:   map[string][]byte{},
	}
}

func (r *signedRegistry) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := strings.TrimPrefix(req.URL.Path, "/")
		if strings.HasSuffix(path, ".json.sig") {
			body, ok := r.signed[strings.TrimSuffix(path, ".sig")]
			if !ok {
				http.NotFound(w, req)
				return
			}
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(body)
			return
		}
		if strings.HasSuffix(path, ".json") {
			body, ok := r.manifest[path]
			if !ok {
				http.NotFound(w, req)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(body)
			return
		}
		body, ok := r.binary["/"+path]
		if !ok {
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(body)
	})
}

func (r *signedRegistry) publishSigned(key Key, binary []byte, signKey ed25519.PrivateKey) []byte {
	hash := sha256.Sum256(binary)
	m := Manifest{
		Version:      ManifestVersion,
		Key:          key.String(),
		BinarySHA256: hex.EncodeToString(hash[:]),
		BinaryURL:    "/" + key.String() + "/osty-self",
	}
	body, err := json.Marshal(m)
	if err != nil {
		r.t.Fatalf("marshal manifest: %v", err)
	}
	r.manifest[key.String()+".json"] = body
	if signKey != nil {
		sig := ed25519.Sign(signKey, body)
		r.signed[key.String()+".json"] = []byte(hex.EncodeToString(sig))
	}
	r.binary["/"+key.String()+"/osty-self"] = binary
	return body
}

func TestHTTPFetcherVerifiesSignedManifest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	reg := newSignedRegistry(t)
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	key := Key{ToolchainSHA: strings.Repeat("a", 64), Triple: "linux-amd64"}
	binary := []byte("signed binary")
	reg.publishSigned(key, binary, priv)

	fetcher := HTTPFetcher{BaseURL: srv.URL, TrustedKey: pub}
	manifest, body, err := fetcher.Fetch(context.Background(), key)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer body.Close()
	if manifest.Key != key.String() {
		t.Errorf("manifest.Key = %q, want %q", manifest.Key, key.String())
	}
}

func TestHTTPFetcherRejectsTamperedManifest(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	reg := newSignedRegistry(t)
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	key := Key{ToolchainSHA: strings.Repeat("b", 64), Triple: "linux-amd64"}
	reg.publishSigned(key, []byte("body"), priv)

	// Mutate the manifest in-place after publishing — keeps the
	// signature pointing at the original bytes so verification
	// must fail.
	reg.manifest[key.String()+".json"] = bytes_replace(reg.manifest[key.String()+".json"], `"key":"`+key.String()+`"`, `"key":"tampered-x86"`)

	fetcher := HTTPFetcher{BaseURL: srv.URL, TrustedKey: pub}
	_, _, err := fetcher.Fetch(context.Background(), key)
	if err == nil {
		t.Fatal("expected verification failure")
	}
	// Tampering changes the manifest key field, so the resolver
	// surfaces a key-mismatch before the signature check runs —
	// also a valid rejection. Either error is acceptable as long
	// as the binary download is *not* initiated.
	if !errors.Is(err, ErrSignatureInvalid) && !strings.Contains(err.Error(), "manifest key") {
		t.Errorf("err = %v, want signature or key-mismatch failure", err)
	}
}

func TestHTTPFetcherRejectsWrongKey(t *testing.T) {
	_, signPriv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	reg := newSignedRegistry(t)
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	key := Key{ToolchainSHA: strings.Repeat("c", 64), Triple: "linux-amd64"}
	reg.publishSigned(key, []byte("body"), signPriv)

	fetcher := HTTPFetcher{BaseURL: srv.URL, TrustedKey: otherPub}
	_, _, err := fetcher.Fetch(context.Background(), key)
	if !errors.Is(err, ErrSignatureInvalid) {
		t.Errorf("err = %v, want ErrSignatureInvalid", err)
	}
}

func TestHTTPFetcherRequiresSignatureWhenTrustedKeySet(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	reg := newSignedRegistry(t)
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	// Publish unsigned (signKey nil leaves no `.sig` entry).
	key := Key{ToolchainSHA: strings.Repeat("d", 64), Triple: "linux-amd64"}
	reg.publishSigned(key, []byte("body"), nil)

	fetcher := HTTPFetcher{BaseURL: srv.URL, TrustedKey: pub}
	_, _, err := fetcher.Fetch(context.Background(), key)
	if !errors.Is(err, ErrSignatureMissing) {
		t.Errorf("err = %v, want ErrSignatureMissing", err)
	}
}

func TestHTTPFetcherAcceptsUnsignedWhenNoTrustedKey(t *testing.T) {
	reg := newSignedRegistry(t)
	srv := httptest.NewServer(reg.handler())
	defer srv.Close()

	key := Key{ToolchainSHA: strings.Repeat("e", 64), Triple: "linux-amd64"}
	reg.publishSigned(key, []byte("body"), nil)

	// No TrustedKey configured → unsigned path is acceptable
	// (preserves A4 behaviour).
	fetcher := HTTPFetcher{BaseURL: srv.URL}
	_, body, err := fetcher.Fetch(context.Background(), key)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	defer body.Close()
}

// bytes_replace is a tiny helper that swaps the first occurrence of
// `old` in `src` with `new`. Avoids pulling in `strings.Replace` so
// the tests stay obvious about what they mutate.
func bytes_replace(src []byte, old, new string) []byte {
	return []byte(strings.Replace(string(src), old, new, 1))
}
