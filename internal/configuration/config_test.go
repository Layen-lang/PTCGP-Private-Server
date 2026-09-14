package configuration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/binarypatch"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/contracts"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/testprofile"
)

func fixture(t *testing.T) []byte {
	t.Helper()
	profile := testprofile.Baseline()
	data, err := json.Marshal(Config{
		SchemaVersion: 1,
		Runtime:       Runtime{Address: "0.0.0.0:443", AdminAddress: "127.0.0.1:8081", LauncherAddress: "127.0.0.1:8080", Database: "data/ptcgp.db", Certificate: "certs/server.pem", PrivateKey: "certs/server-key.pem", CertificateAuthority: "certs/ca.pem", TrafficLog: "data/traffic.jsonl", RuntimeDirectory: "data/runtime"},
		Android:       Android{Package: "jp.pokemon.pokemontcgp", Activity: "UnityPlayerActivity", ServerAddress: "10.0.2.2", RedirectHosts: []string{"example.test"}},
		Data:          Data{MasterData: "game-data/master-data", Images: "game-data/images"},
		Client:        profile,
		Contracts:     contracts.Expectations{Fingerprint: contracts.DeclarationOrderV1, Services: 1, Methods: 1, DescriptorSHA256: strings.Repeat("a", 64)},
		Patch: binarypatch.Manifest{
			Name: "fixture", Version: profile.AppVersion, Library: "libfixture.so",
			SourceSHA256: strings.Repeat("b", 64), TargetSHA256: strings.Repeat("c", 64),
			Offset: 1, ExpectedHex: "00", ReplacementHex: "01", Rationale: "test",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestConfigResolvesRelativeToItsFileAndRejectsUnknownFields(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "server.json")
	data := fixture(t)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Runtime.Database != filepath.Join(root, "data", "ptcgp.db") ||
		c.Data.MasterData != filepath.Join(root, "game-data", "master-data") ||
		c.Data.Images != filepath.Join(root, "game-data", "images") {
		t.Fatalf("relative paths were not resolved from %s", root)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	value["typo"] = true
	data, err = json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("unknown key accepted")
	}
}

func TestConfigRejectsPatchForAnotherGameVersion(t *testing.T) {
	var value map[string]any
	if err := json.Unmarshal(fixture(t), &value); err != nil {
		t.Fatal(err)
	}
	value["patch"].(map[string]any)["version"] = "9.9.9"
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "server.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("patch for another game version accepted")
	}
}
