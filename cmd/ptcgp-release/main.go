// Command ptcgp-release prepares signed update assets for the release workflow.
// The Ed25519 seed is read only from the publisher's secret environment.
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/updates"
)

func run() error {
	if len(os.Args) != 4 {
		return fmt.Errorf("usage: ptcgp-release STAGING OUTPUT RELEASE_ASSET_URL")
	}
	seed, err := base64.StdEncoding.DecodeString(os.Getenv("PTCGP_UPDATE_SIGNING_SEED"))
	if err != nil || len(seed) != ed25519.SeedSize {
		return fmt.Errorf("PTCGP_UPDATE_SIGNING_SEED must contain a base64 Ed25519 seed")
	}
	key := ed25519.NewKeyFromSeed(seed)
	public := base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))
	if public != os.Getenv("PTCGP_UPDATE_PUBLIC_KEY") {
		return fmt.Errorf("release public key does not match signing seed")
	}
	root, out := os.Args[1], os.Args[2]
	if err = os.MkdirAll(out, 0700); err != nil {
		return err
	}
	version, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		return err
	}
	config, err := os.ReadFile(filepath.Join(root, "server.json"))
	if err != nil {
		return err
	}
	var cfg struct {
		Client struct {
			AppVersion string `json:"appVersion"`
		}
	}
	if err = json.Unmarshal(config, &cfg); err != nil {
		return err
	}
	manifest := updates.Manifest{Version: strings.TrimSpace(string(version)), Games: []string{cfg.Client.AppVersion}}
	for _, name := range []string{"bin", "profiles", "server.json", "VERSION"} {
		err = filepath.WalkDir(filepath.Join(root, name), func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if !d.Type().IsRegular() {
				return fmt.Errorf("release contains non-regular file")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			asset := strings.ReplaceAll(rel, "/", "__")
			h := sha256.Sum256(data)
			manifest.Files = append(manifest.Files, updates.File{Path: rel, URL: strings.TrimRight(os.Args[3], "/") + "/" + asset, SHA256: hex.EncodeToString(h[:]), Size: int64(len(data))})
			return os.WriteFile(filepath.Join(out, asset), data, 0600)
		})
		if err != nil {
			return err
		}
	}
	payload, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	envelope := updates.Envelope{Payload: base64.StdEncoding.EncodeToString(payload), Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload))}
	data, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	if _, err = updates.Verify(data, public); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "update.json"), data, 0600)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
