package localtls

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateConfiguredPathsAndDomainsWithoutOverwriting(t *testing.T) {
	root := t.TempDir()
	paths := Paths{CA: filepath.Join(root, "authority", "custom.crt"), CAKey: filepath.Join(root, "authority", "private.key"), Certificate: filepath.Join(root, "tls", "leaf.crt"), PrivateKey: filepath.Join(root, "tls", "leaf.key")}
	if err := GenerateFiles(paths, []string{"configured.example"}); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(paths.Certificate)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(original)
	if block == nil {
		t.Fatal("missing certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.VerifyHostname("configured.example"); err != nil {
		t.Fatal(err)
	}
	if err := GenerateFiles(paths, []string{"changed.example"}); err == nil {
		t.Fatal("overwrote existing certificates")
	}
	after, err := os.ReadFile(paths.Certificate)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(original, after) {
		t.Fatal("existing certificate changed")
	}
}

func TestEnsureRenewsNewDomainsAndKeepsAuthorityAndKeys(t *testing.T) {
	root := t.TempDir()
	paths := Paths{CA: filepath.Join(root, "ca.pem"), CAKey: filepath.Join(root, "ca-key.pem"), Certificate: filepath.Join(root, "leaf.pem"), PrivateKey: filepath.Join(root, "leaf-key.pem")}
	if err := EnsureFiles(paths, []string{"old.example"}); err != nil {
		t.Fatal(err)
	}
	originals := map[string][]byte{}
	for _, path := range []string{paths.CA, paths.CAKey, paths.PrivateKey} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		originals[path] = data
	}
	if err := EnsureFiles(paths, []string{"new.example"}); err != nil {
		t.Fatal(err)
	}
	for path, want := range originals {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("changed authority/key %s", path)
		}
	}
	before, err := os.ReadFile(paths.Certificate)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(before)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.VerifyHostname("new.example"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureFiles(paths, []string{"new.example"}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(paths.Certificate)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("renewed an already valid certificate")
	}
}
