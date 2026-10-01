// Package updates downloads signed immutable releases. Applying an update is a
// separate explicit action; discovery never interrupts a running game.
package updates

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/installation"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/provision"
)

// PublicKey is supplied by the release workflow. A source build cannot trust
// network-delivered executables until its publisher configures a signing key.
var PublicKey string
var ManifestURL = "https://github.com/Layen-lang/PTCGP-Private-Server/releases/latest/download/update.json"

type File struct {
	Path   string `json:"path"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Manifest struct {
	Version string   `json:"version"`
	Games   []string `json:"games"`
	Files   []File   `json:"files"`
}
type Envelope struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}
type State struct {
	Phase     string `json:"phase"`
	Version   string `json:"version,omitempty"`
	Message   string `json:"message"`
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
}
type Manager struct {
	work               sync.Mutex
	mu                 sync.RWMutex
	state              State
	root, program, key string
	client             *http.Client
}

func New(root, program string) *Manager {
	return &Manager{root: root, program: program, state: State{Phase: "idle", Message: "Recherche de mises à jour"}, client: &http.Client{Timeout: 2 * time.Minute}}
}
func (m *Manager) Status() State { m.mu.RLock(); defer m.mu.RUnlock(); return m.state }
func (m *Manager) set(s State)   { m.mu.Lock(); m.state = s; m.mu.Unlock() }
func (m *Manager) PreparedKey() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.state.Phase != "ready" {
		return ""
	}
	return m.key
}
func allowedPath(p string) bool {
	if !filepath.IsLocal(p) || strings.ContainsAny(p, ":\\") || filepath.ToSlash(filepath.Clean(p)) != p {
		return false
	}
	return p == "server.json" || p == "VERSION" || strings.HasPrefix(p, "profiles/") || strings.HasPrefix(p, "bin/")
}

func releaseVersion(value string) ([4]uint64, bool) {
	var version [4]uint64
	parts := strings.Split(value, ".")
	if len(parts) != len(version) {
		return version, false
	}
	for i, part := range parts {
		if part == "" || strings.Trim(part, "0123456789") != "" {
			return version, false
		}
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return version, false
		}
		version[i] = n
	}
	return version, true
}

func newerRelease(candidate, installed string) bool {
	next, valid := releaseVersion(candidate)
	if !valid {
		return false
	}
	current, valid := releaseVersion(installed)
	if !valid {
		return true // A development checkout may not have a release version.
	}
	for i := range next {
		if next[i] != current[i] {
			return next[i] > current[i]
		}
	}
	return false
}
func Verify(data []byte, public string) (Manifest, error) {
	var manifest Manifest
	var envelope Envelope
	if e := json.Unmarshal(data, &envelope); e != nil {
		return manifest, e
	}
	key, e := base64.StdEncoding.DecodeString(public)
	if e != nil || len(key) != ed25519.PublicKeySize {
		return manifest, fmt.Errorf("release signing key is not configured")
	}
	payload, e := base64.StdEncoding.DecodeString(envelope.Payload)
	if e != nil {
		return manifest, e
	}
	signature, e := base64.StdEncoding.DecodeString(envelope.Signature)
	if e != nil || !ed25519.Verify(key, payload, signature) {
		return manifest, fmt.Errorf("invalid update signature")
	}
	if e = json.Unmarshal(payload, &manifest); e != nil {
		return manifest, e
	}
	_, validVersion := releaseVersion(manifest.Version)
	if !validVersion || len(manifest.Games) == 0 || len(manifest.Files) == 0 || len(manifest.Files) > 256 {
		return manifest, fmt.Errorf("invalid release manifest")
	}
	seen := map[string]bool{}
	var total int64
	for _, file := range manifest.Files {
		u, e := url.Parse(file.URL)
		if e != nil || u.Scheme != "https" || u.Host == "" || !allowedPath(file.Path) || !installation.ValidKey(file.SHA256) || file.Size <= 0 || file.Size > 256<<20 {
			return manifest, fmt.Errorf("invalid update file %s", file.Path)
		}
		canonical := strings.ToLower(file.Path)
		if seen[canonical] {
			return manifest, fmt.Errorf("duplicate update path")
		}
		seen[canonical] = true
		total += file.Size
	}
	if total > 1<<30 {
		return manifest, fmt.Errorf("update exceeds size limit")
	}
	for _, required := range []string{"server.json", "VERSION", "bin/ptcgp-launcher.exe", "bin/ptcgp-server.exe", "bin/ptcgp-importer.exe", "bin/ptcgp-reader-x86_64", "bin/ptcgp-reader-aarch64"} {
		if !seen[strings.ToLower(required)] {
			return manifest, fmt.Errorf("update missing %s", required)
		}
	}
	for _, game := range manifest.Games {
		if _, valid := releaseVersion(game + ".0"); !valid {
			return manifest, fmt.Errorf("invalid supported game version")
		}
		for _, prefix := range []string{"profiles/images-", "profiles/image-index-"} {
			if !seen[prefix+game+".json"] {
				return manifest, fmt.Errorf("update missing extraction profile for %s", game)
			}
		}
	}
	return manifest, nil
}
func (m *Manager) fetch(ctx context.Context, address string, limit int64) ([]byte, error) {
	req, e := http.NewRequestWithContext(ctx, "GET", address, nil)
	if e != nil {
		return nil, e
	}
	response, e := m.client.Do(req)
	if e != nil {
		return nil, e
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("update server: HTTP %d", response.StatusCode)
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if e == nil && int64(len(data)) > limit {
		e = fmt.Errorf("download exceeds expected size")
	}
	return data, e
}
func hash(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func (m *Manager) Check(ctx context.Context, game string) {
	m.work.Lock()
	defer m.work.Unlock()
	if PublicKey == "" {
		m.set(State{Phase: "unconfigured", Message: "Les mises à jour signées seront disponibles dans une distribution officielle."})
		return
	}
	data, e := m.fetch(ctx, ManifestURL, 2<<20)
	if e != nil {
		m.set(State{Phase: "offline", Message: "Vérification des mises à jour indisponible ; installation locale conservée."})
		return
	}
	manifest, e := Verify(data, PublicKey)
	if e != nil {
		m.set(State{Phase: "error", Message: e.Error()})
		return
	}
	compatible := false
	for _, version := range manifest.Games {
		if version == game {
			compatible = true
		}
	}
	if !compatible {
		m.set(State{Phase: "incompatible", Version: manifest.Version, Message: "La dernière publication ne prend pas en charge cette version du jeu."})
		return
	}
	current, _ := os.ReadFile(filepath.Join(m.program, "VERSION"))
	if !newerRelease(manifest.Version, strings.TrimSpace(string(current))) {
		m.set(State{Phase: "current", Version: strings.TrimSpace(string(current)), Message: "Installation à jour"})
		return
	}
	key := hash(data)
	stage := filepath.Join(m.root, "data", "updates", "versions", key)
	if e = os.MkdirAll(stage, 0700); e != nil {
		m.set(State{Phase: "error", Message: e.Error()})
		return
	}
	for i, file := range manifest.Files {
		m.set(State{Phase: "downloading", Version: manifest.Version, Message: "Téléchargement de la mise à jour", Completed: i, Total: len(manifest.Files)})
		p := filepath.Join(stage, filepath.FromSlash(file.Path))
		bytes, _ := os.ReadFile(p)
		if int64(len(bytes)) != file.Size || hash(bytes) != file.SHA256 {
			bytes, _ = os.ReadFile(filepath.Join(m.program, filepath.FromSlash(file.Path)))
		}
		if int64(len(bytes)) != file.Size || hash(bytes) != file.SHA256 {
			bytes, e = m.fetch(ctx, file.URL, file.Size)
			if e != nil {
				m.set(State{Phase: "error", Message: e.Error()})
				return
			}
		}
		if int64(len(bytes)) != file.Size || hash(bytes) != file.SHA256 {
			m.set(State{Phase: "error", Message: "Le fichier téléchargé ne correspond pas à la publication signée."})
			return
		}
		if e = os.MkdirAll(filepath.Dir(p), 0700); e == nil {
			e = os.WriteFile(p+".partial", bytes, 0700)
		}
		if e == nil {
			e = os.Rename(p+".partial", p)
		}
		if e != nil {
			m.set(State{Phase: "error", Message: e.Error()})
			return
		}
	}
	if e = provision.WriteJSON(filepath.Join(stage, "update.json"), json.RawMessage(data)); e != nil {
		m.set(State{Phase: "error", Message: e.Error()})
		return
	}
	m.mu.Lock()
	m.key = key
	m.state = State{Phase: "ready", Version: manifest.Version, Message: "Mise à jour prête à installer", Completed: len(manifest.Files), Total: len(manifest.Files)}
	m.mu.Unlock()
}

// InstalledVersion identifies the program serving the panel, independently of discovery.
func (m *Manager) InstalledVersion() string {
	data, err := os.ReadFile(filepath.Join(m.program, "VERSION"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// InstallationError reports a failed installation after the old panel restarts.
func (m *Manager) InstallationError() string {
	data, err := os.ReadFile(filepath.Join(m.root, "data", "updates", "last-error.json"))
	if err != nil {
		return ""
	}
	var failure struct{ Error string }
	if json.Unmarshal(data, &failure) != nil {
		return ""
	}
	return failure.Error
}

func ValidatePrepared(root, key string) (Manifest, error) {
	var empty Manifest
	if !installation.ValidKey(key) {
		return empty, fmt.Errorf("invalid prepared release")
	}
	stage := filepath.Join(root, "data", "updates", "versions", key)
	data, e := os.ReadFile(filepath.Join(stage, "update.json"))
	if e != nil {
		return empty, e
	}
	if hash(data) != key {
		return empty, fmt.Errorf("prepared manifest changed")
	}
	manifest, e := Verify(data, PublicKey)
	if e != nil {
		return empty, e
	}
	for _, file := range manifest.Files {
		b, e := os.ReadFile(filepath.Join(stage, filepath.FromSlash(file.Path)))
		if e != nil {
			return empty, e
		}
		if int64(len(b)) != file.Size || hash(b) != file.SHA256 {
			return empty, fmt.Errorf("prepared file changed: %s", file.Path)
		}
	}
	return manifest, nil
}
