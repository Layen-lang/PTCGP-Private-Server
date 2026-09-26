package updates

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The subprocess serves only a health endpoint. It never contacts an emulator,
// opens a real account database, or runs the production game server.
func TestMain(m *testing.M) {
	if os.Getenv("PTCGP_UPDATE_TEST_CHILD") == "1" && len(os.Args) > 1 && os.Args[1] == "serve-panel" {
		exe, _ := os.Executable()
		if os.Getenv("PTCGP_UPDATE_TEST_FAIL") == "1" && strings.Contains(filepath.ToSlash(exe), "/versions/") {
			_ = os.WriteFile(os.Getenv("PTCGP_UPDATE_TEST_DB"), []byte("failed migration"), 0600)
			os.Exit(7)
		}
		http.HandleFunc("/api/control/health", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"healthy": true, "pid": os.Getpid()})
		})
		if http.ListenAndServe(os.Getenv("PTCGP_UPDATE_TEST_ADDRESS"), nil) != nil {
			os.Exit(8)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestInstallerActivationAndRollback(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows installer")
	}
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "activate", true: "rollback"}[fail], func(t *testing.T) {
			root := t.TempDir()
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			address := listener.Addr().String()
			listener.Close()
			t.Setenv("PTCGP_UPDATE_TEST_CHILD", "1")
			t.Setenv("PTCGP_UPDATE_TEST_ADDRESS", address)
			if fail {
				t.Setenv("PTCGP_UPDATE_TEST_FAIL", "1")
			} else {
				t.Setenv("PTCGP_UPDATE_TEST_FAIL", "")
			}
			raw, e := os.ReadFile(filepath.Join("..", "..", "server.json"))
			if e != nil {
				t.Fatal(e)
			}
			var cfg map[string]any
			if e = json.Unmarshal(raw, &cfg); e != nil {
				t.Fatal(e)
			}
			cfg["runtime"].(map[string]any)["launcherAddress"] = address
			config, e := json.Marshal(cfg)
			if e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(root, "server.json"), config, 0600); e != nil {
				t.Fatal(e)
			}
			db := filepath.Join(root, "data", "ptcgp.db")
			if e = os.MkdirAll(filepath.Dir(db), 0700); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(db, []byte("original accounts"), 0600); e != nil {
				t.Fatal(e)
			}
			t.Setenv("PTCGP_UPDATE_TEST_DB", db)
			exe, e := os.Executable()
			if e != nil {
				t.Fatal(e)
			}
			binary, e := os.ReadFile(exe)
			if e != nil {
				t.Fatal(e)
			}
			payloads := map[string][]byte{"server.json": config, "VERSION": []byte("1.7.2.1"), "bin/ptcgp-launcher.exe": binary, "bin/ptcgp-server.exe": []byte("server"), "bin/ptcgp-importer.exe": []byte("importer")}
			payloads["bin/ptcgp-reader-x86_64"] = []byte("reader-x86_64")
			payloads["bin/ptcgp-reader-aarch64"] = []byte("reader-aarch64")
			payloads["profiles/images-1.7.2.json"] = []byte("{}")
			payloads["profiles/image-index-1.7.2.json"] = []byte("{}")
			manifest := Manifest{Version: "1.7.2.1", Games: []string{"1.7.2"}}
			for p, b := range payloads {
				manifest.Files = append(manifest.Files, File{Path: p, URL: "https://example.invalid/file", Size: int64(len(b)), SHA256: hash(b)})
			}
			envelope, public := signed(t, manifest)
			saved := PublicKey
			PublicKey = public
			defer func() { PublicKey = saved }()
			key := hash(envelope)
			stage := filepath.Join(root, "data", "updates", "versions", key)
			for p, b := range payloads {
				target := filepath.Join(stage, filepath.FromSlash(p))
				if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
					t.Fatal(e)
				}
				if e = os.WriteFile(target, b, 0700); e != nil {
					t.Fatal(e)
				}
			}
			if e = os.WriteFile(filepath.Join(stage, "update.json"), envelope, 0600); e != nil {
				t.Fatal(e)
			}
			if e = os.MkdirAll(filepath.Join(root, "bin"), 0700); e != nil {
				t.Fatal(e)
			}
			if e = Copy(exe, filepath.Join(root, "bin", "ptcgp-launcher.exe")); e != nil {
				t.Fatal(e)
			}
			err := Apply(root, key, 0)
			if fail && err == nil {
				t.Fatal("failed child was accepted")
			}
			if !fail && err != nil {
				t.Fatal(err)
			}
			client := &http.Client{Timeout: time.Second}
			var child int
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				response, e := client.Get("http://" + address + "/api/control/health")
				if e == nil {
					var health struct{ PID int }
					_ = json.NewDecoder(response.Body).Decode(&health)
					response.Body.Close()
					child = health.PID
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if child == 0 {
				t.Fatal("replacement or rollback panel did not start")
			}
			process, e := os.FindProcess(child)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = process.Kill(); _, _ = process.Wait() })
			_, e = os.Stat(filepath.Join(root, "data", "updates", "current.json"))
			if fail && !os.IsNotExist(e) {
				t.Fatal("failed release remained selected")
			}
			if !fail && e != nil {
				t.Fatal(e)
			}
			b, e := os.ReadFile(db)
			if e != nil || string(b) != "original accounts" {
				t.Fatalf("accounts not preserved: %q %v", b, e)
			}
		})
	}
}
