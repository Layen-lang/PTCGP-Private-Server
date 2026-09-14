package localtls

import (
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// EnsureFiles renews the leaf for new release hosts while preserving the CA
// and both private keys. Only the certificate needs an atomic replacement.
func EnsureFiles(paths Paths, hosts []string) error {
	if len(hosts) == 0 {
		return fmt.Errorf("at least one certificate domain is required")
	}
	missing := 0
	seen := make(map[string]bool)
	for _, path := range []string{paths.CA, paths.CAKey, paths.Certificate, paths.PrivateKey} {
		key := filepath.Clean(path)
		if path == "" || seen[key] {
			return fmt.Errorf("certificate output paths must be nonempty and distinct")
		}
		seen[key] = true
		if _, err := os.Stat(path); os.IsNotExist(err) {
			missing++
		} else if err != nil {
			return err
		}
	}
	if missing == 4 {
		return GenerateFiles(paths, hosts)
	}
	if missing != 0 {
		return fmt.Errorf("incomplete certificate files; restore the existing CA and keys before renewal")
	}
	authority, err := tls.LoadX509KeyPair(paths.CA, paths.CAKey)
	if err != nil {
		return fmt.Errorf("load existing CA: %w", err)
	}
	ca, err := x509.ParseCertificate(authority.Certificate[0])
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if !ca.IsCA || now.Before(ca.NotBefore) || !now.Add(24*time.Hour).Before(ca.NotAfter) {
		return fmt.Errorf("local CA must be valid for at least another day")
	}
	leaf, err := tls.LoadX509KeyPair(paths.Certificate, paths.PrivateKey)
	if err != nil {
		return fmt.Errorf("load existing server keypair: %w", err)
	}
	current, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		return err
	}
	valid := current.CheckSignatureFrom(ca) == nil && !now.Before(current.NotBefore) && now.Add(24*time.Hour).Before(current.NotAfter)
	for _, host := range hosts {
		valid = valid && current.VerifyHostname(host) == nil
	}
	if valid {
		return nil
	}
	signer, ok := leaf.PrivateKey.(crypto.Signer)
	if !ok {
		return fmt.Errorf("server private key cannot sign")
	}
	end := now.AddDate(2, 0, 0)
	if ca.NotAfter.Before(end) {
		end = ca.NotAfter
	}
	template := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: hosts[0], Organization: []string{"PTCGP Local"}}, DNSNames: append([]string(nil), hosts...), NotBefore: now.Add(-time.Hour), NotAfter: end, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, signer.Public(), authority.PrivateKey)
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(paths.Certificate), ".renew-*.pem")
	if err != nil {
		return err
	}
	defer os.Remove(temporary.Name())
	if err := pem.Encode(temporary, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Chmod(temporary.Name(), 0644); err != nil {
		return err
	}
	return os.Rename(temporary.Name(), paths.Certificate)
}
