// Package localtls creates development-only certificates for local redirection.
package localtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/md5" // #nosec G501 -- Android's legacy certificate filename requires subject_hash_old.
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Generate writes a CA and leaf keypair. It refuses to overwrite any file.
func Generate(directory string, redirectHosts []string) error {
	return GenerateFiles(Paths{CA: filepath.Join(directory, "ca.pem"), CAKey: filepath.Join(directory, "ca-key.pem"), Certificate: filepath.Join(directory, "server.pem"), PrivateKey: filepath.Join(directory, "server-key.pem")}, redirectHosts)
}

type Paths struct{ CA, CAKey, Certificate, PrivateKey string }

// GenerateFiles honors configured filenames and domains, including separate directories.
func GenerateFiles(paths Paths, redirectHosts []string) error {
	if len(redirectHosts) == 0 {
		return fmt.Errorf("at least one certificate domain is required")
	}
	seen := make(map[string]bool)
	for _, path := range []string{paths.CA, paths.CAKey, paths.Certificate, paths.PrivateKey} {
		if path == "" || seen[filepath.Clean(path)] {
			return fmt.Errorf("certificate output paths must be nonempty and distinct")
		}
		seen[filepath.Clean(path)] = true
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("refusing to overwrite %s", path)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect %s: %w", path, err)
		}
	}
	now := time.Now().UTC()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate CA key: %w", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "PTCGP Local Development CA", Organization: []string{"PTCGP Local"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(5, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create CA certificate: %w", err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generate server key: %w", err)
	}
	leafTemplate := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: redirectHosts[0], Organization: []string{"PTCGP Local"}},
		DNSNames:     append([]string(nil), redirectHosts...),
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(2, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, caTemplate, &leafKey.PublicKey, caKey)
	if err != nil {
		return fmt.Errorf("create server certificate: %w", err)
	}
	caKeyDER, err := x509.MarshalPKCS8PrivateKey(caKey)
	if err != nil {
		return fmt.Errorf("marshal CA key: %w", err)
	}
	leafKeyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		return fmt.Errorf("marshal server key: %w", err)
	}
	files := []struct {
		name string
		mode os.FileMode
		data []byte
	}{
		{paths.CA, 0o644, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})},
		{paths.CAKey, 0o600, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: caKeyDER})},
		{paths.Certificate, 0o644, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})},
		{paths.PrivateKey, 0o600, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: leafKeyDER})},
	}
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(file.name), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(file.name, file.data, file.mode); err != nil {
			return fmt.Errorf("write %s: %w", file.name, err)
		}
	}
	return nil
}

// AndroidSystemName returns Android's legacy OpenSSL subject_hash_old filename.
func AndroidSystemName(certificatePath string) (string, error) {
	data, err := os.ReadFile(certificatePath)
	if err != nil {
		return "", fmt.Errorf("read CA certificate: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return "", fmt.Errorf("CA file does not contain a PEM certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("parse CA certificate: %w", err)
	}
	sum := md5.Sum(certificate.RawSubject) // #nosec G401 -- compatibility identifier, not security.
	hash := uint32(sum[0]) | uint32(sum[1])<<8 | uint32(sum[2])<<16 | uint32(sum[3])<<24
	return fmt.Sprintf("%08x.0", hash), nil
}

func serial() *big.Int {
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	n, err := rand.Int(rand.Reader, limit)
	if err != nil {
		panic(err)
	}
	return n
}
