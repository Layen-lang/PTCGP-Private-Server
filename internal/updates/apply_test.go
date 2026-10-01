package updates

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The subprocess serves only a health endpoint. It never contacts an emulator,
// opens a real account database, or runs the production game server.
func TestMain(m *testing.M) {
	if os.Getenv("PTCGP_UPDATE_TEST_CHILD") == "1" && len(os.Args) > 1 {
		PublicKey = os.Getenv("PTCGP_UPDATE_TEST_PUBLIC_KEY")
		if os.Args[1] == "request-update" {
			if err := StartInstaller(os.Args[2], os.Args[3]); err != nil {
				os.Exit(9)
			}
			os.Exit(0)
		}
		if os.Args[1] == "apply-update" {
			parent, _ := strconv.Atoi(os.Args[4])
			if err := Apply(os.Args[2], os.Args[3], parent); err != nil {
				os.Exit(10)
			}
			if err := os.WriteFile(filepath.Join(os.Args[2], "data", "updates", "installer-test.done"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
				os.Exit(11)
			}
			os.Exit(0)
		}
	}
	if os.Getenv("PTCGP_UPDATE_TEST_CHILD") == "1" && len(os.Args) > 1 && os.Args[1] == "serve-panel" {
		exe, _ := os.Executable()
		version, _ := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(exe)), "VERSION"))
		if os.Getenv("PTCGP_UPDATE_TEST_FAIL") == "1" && strings.TrimSpace(string(version)) == "1.7.2.1" {
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
	for _, test := range []struct {
		name                   string
		fail, legacy, detached bool
	}{
		{name: "activate"},
		{name: "rollback", fail: true},
		{name: "legacy-to-root", legacy: true},
		{name: "legacy-rollback", legacy: true, fail: true},
		{name: "detached-restart", detached: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fail := test.fail
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
			t.Setenv("PTCGP_UPDATE_TEST_PUBLIC_KEY", public)
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
			if e = os.WriteFile(filepath.Join(root, "VERSION"), []byte("1.7.2.0"), 0600); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(root, "bin", "ptcgp-server.exe"), []byte("previous server"), 0600); e != nil {
				t.Fatal(e)
			}
			var previousSelection []byte
			oldProgram := root
			if test.legacy {
				oldKey := strings.Repeat("a", 64)
				oldProgram = filepath.Join(root, "data", "updates", "versions", oldKey)
				for _, path := range []string{"bin/ptcgp-launcher.exe", "server.json", "VERSION"} {
					target := filepath.Join(oldProgram, filepath.FromSlash(path))
					if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
						t.Fatal(e)
					}
					if e = Copy(filepath.Join(root, filepath.FromSlash(path)), target); e != nil {
						t.Fatal(e)
					}
				}
				previousSelection = []byte(`{"key":"` + oldKey + `","version":"1.7.2.0"}`)
				if e = os.WriteFile(filepath.Join(root, "data", "updates", "current.json"), previousSelection, 0600); e != nil {
					t.Fatal(e)
				}
			}
			if test.detached {
				parent := exec.Command(filepath.Join(oldProgram, "bin", "ptcgp-launcher.exe"), "request-update", root, key)
				if e = parent.Run(); e != nil {
					t.Fatal(e)
				}
			} else {
				err := Apply(root, key, 0)
				if fail && err == nil {
					t.Fatal("failed child was accepted")
				}
				if !fail && err != nil {
					t.Fatal(err)
				}
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
			if test.detached {
				// Let the installer accept the child's health and exit before
				// cleanup stops that child; otherwise cleanup would trigger rollback.
				deadline := time.Now().Add(5 * time.Second)
				var installerPID int
				for time.Now().Before(deadline) {
					done, err := os.ReadFile(filepath.Join(root, "data", "updates", "installer-test.done"))
					if err == nil {
						installerPID, err = strconv.Atoi(string(done))
						if err != nil {
							t.Fatal(err)
						}
						break
					}
					time.Sleep(50 * time.Millisecond)
				}
				if installerPID == 0 {
					t.Fatal("detached installer did not finish")
				}
				if err := waitForExit(installerPID, 5*time.Second); err != nil {
					t.Fatal(err)
				}
			}
			process, e := os.FindProcess(child)
			if e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _ = process.Kill(); _, _ = process.Wait() })
			selection, e := os.ReadFile(filepath.Join(root, "data", "updates", "current.json"))
			if fail && test.legacy {
				if e != nil || string(selection) != string(previousSelection) {
					t.Fatalf("legacy selection not restored: %s %v", selection, e)
				}
			} else if !os.IsNotExist(e) {
				t.Fatal("root installation kept a version selector")
			}
			version, e := os.ReadFile(filepath.Join(root, "VERSION"))
			wantVersion := "1.7.2.1"
			wantServer := "server"
			if fail {
				wantVersion = "1.7.2.0"
				wantServer = "previous server"
			}
			if e != nil || string(version) != wantVersion {
				t.Fatalf("root version=%q, want %q: %v", version, wantVersion, e)
			}
			server, e := os.ReadFile(filepath.Join(root, "bin", "ptcgp-server.exe"))
			if e != nil || string(server) != wantServer {
				t.Fatalf("root server=%q, want %q: %v", server, wantServer, e)
			}
			_, e = os.Stat(filepath.Join(root, "profiles", "images-1.7.2.json"))
			if fail && !os.IsNotExist(e) || !fail && e != nil {
				t.Fatalf("root profiles were not installed or rolled back: %v", e)
			}
			b, e := os.ReadFile(db)
			if e != nil || string(b) != "original accounts" {
				t.Fatalf("accounts not preserved: %q %v", b, e)
			}
		})
	}
}
