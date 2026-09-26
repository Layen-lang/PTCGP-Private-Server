package updates

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func signed(t *testing.T, m Manifest) ([]byte, string) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(Envelope{base64.StdEncoding.EncodeToString(payload), base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))})
	if err != nil {
		t.Fatal(err)
	}
	return data, base64.StdEncoding.EncodeToString(public)
}
func fixtureManifest(base string) Manifest {
	m := Manifest{Version: "1.7.2.1", Games: []string{"1.7.2"}}
	for _, p := range []string{"server.json", "VERSION", "bin/ptcgp-launcher.exe", "bin/ptcgp-server.exe", "bin/ptcgp-importer.exe", "bin/ptcgp-reader-x86_64", "bin/ptcgp-reader-aarch64"} {
		m.Files = append(m.Files, File{Path: p, URL: base + "/file", SHA256: hash([]byte("payload")), Size: 7})
	}
	for _, p := range []string{"profiles/images-1.7.2.json", "profiles/image-index-1.7.2.json"} {
		m.Files = append(m.Files, File{Path: p, URL: base + "/file", SHA256: hash([]byte("payload")), Size: 7})
	}
	return m
}

func TestReleaseOrdering(t *testing.T) {
	for _, test := range []struct {
		candidate, installed string
		want                 bool
	}{
		{"1.7.2.10", "1.7.2.9", true},
		{"1.7.2.9", "1.7.2.10", false},
		{"1.7.2.1", "1.7.2.1", false},
		{"1.8.0.0", "1.7.9.9", true},
		{"1.7.2.1", "", true},
		{"invalid", "1.7.2.1", false},
	} {
		if got := newerRelease(test.candidate, test.installed); got != test.want {
			t.Errorf("newerRelease(%q, %q) = %v", test.candidate, test.installed, got)
		}
	}
}
func TestSignatureAndPaths(t *testing.T) {
	m := fixtureManifest("https://example.invalid")
	data, key := signed(t, m)
	if _, e := Verify(data, key); e != nil {
		t.Fatal(e)
	}
	var env Envelope
	_ = json.Unmarshal(data, &env)
	env.Payload = base64.StdEncoding.EncodeToString([]byte(`{"version":"attacker"}`))
	changed, _ := json.Marshal(env)
	if _, e := Verify(changed, key); e == nil {
		t.Fatal("accepted unsigned payload change")
	}
	for _, p := range []string{"../server.json", "bin/../../outside.exe", "bin/../server.json", "bin/x:stream", "/absolute", "bin\\file.exe"} {
		bad := fixtureManifest("https://example.invalid")
		bad.Files[0].Path = p
		b, k := signed(t, bad)
		if _, e := Verify(b, k); e == nil {
			t.Errorf("accepted unsafe path %q", p)
		}
	}
}
func TestDownloadsAreVerifiedAndNeverActivatedByCheck(t *testing.T) {
	var envelope []byte
	broken := false
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest" {
			_, _ = w.Write(envelope)
			return
		}
		requests++
		if broken {
			_, _ = w.Write([]byte("damaged"))
		} else {
			_, _ = w.Write([]byte("payload"))
		}
	}))
	defer server.Close()
	root := t.TempDir()
	envelope, key := signed(t, fixtureManifest(server.URL))
	oldKey, oldURL := PublicKey, ManifestURL
	PublicKey, ManifestURL = key, server.URL+"/manifest"
	t.Cleanup(func() { PublicKey, ManifestURL = oldKey, oldURL })
	m := New(root, root)
	m.client = server.Client()
	m.Check(context.Background(), "1.7.2")
	if m.Status().Phase != "ready" {
		t.Fatalf("%+v", m.Status())
	}
	if _, e := os.Stat(filepath.Join(root, "data", "updates", "current.json")); !os.IsNotExist(e) {
		t.Fatal("checking activated update")
	}
	if _, e := ValidatePrepared(root, m.PreparedKey()); e != nil {
		t.Fatal(e)
	}
	before := requests
	m.Check(context.Background(), "1.7.2")
	if requests != before {
		t.Fatal("unchanged files downloaded again")
	}
	path := filepath.Join(root, "data", "updates", "versions", m.PreparedKey(), "bin", "ptcgp-server.exe")
	if e := os.WriteFile(path, []byte("changed"), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := ValidatePrepared(root, m.PreparedKey()); e == nil {
		t.Fatal("accepted modified prepared executable")
	}
	broken = true
	m.Check(context.Background(), "1.7.2")
	if m.Status().Phase != "error" || m.PreparedKey() != "" {
		t.Fatal("damaged download remains installable")
	}
}
func TestUnknownGameDoesNotDownloadExecutables(t *testing.T) {
	var data []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/manifest" {
			t.Error("downloaded incompatible executable")
		}
		_, _ = w.Write(data)
	}))
	defer server.Close()
	var key string
	data, key = signed(t, fixtureManifest(server.URL))
	oldKey, oldURL := PublicKey, ManifestURL
	PublicKey, ManifestURL = key, server.URL+"/manifest"
	defer func() { PublicKey, ManifestURL = oldKey, oldURL }()
	m := New(t.TempDir(), t.TempDir())
	m.client = server.Client()
	m.Check(context.Background(), "9.9.9")
	if m.Status().Phase != "incompatible" {
		t.Fatal(m.Status())
	}
}
