// Package provision publishes complete local data generations. Import details
// stay behind Prepare; consumers only resolve a validated generation.
package provision

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/android"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/configuration"
)

const converter = "rust-texture-v1"

type State struct {
	Phase     string `json:"phase"`
	Message   string `json:"message"`
	Ready     bool   `json:"ready"`
	Busy      bool   `json:"busy"`
	Completed int    `json:"completed"`
	Total     int    `json:"total"`
	Serial    string `json:"serial,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Manager struct {
	mu            sync.RWMutex
	state         State
	cfg           configuration.Config
	root, program string
}

func New(cfg configuration.Config, root, program string) *Manager {
	return &Manager{cfg: cfg, root: root, program: program, state: State{Phase: "checking", Message: "Vérification de l’installation"}}
}
func (m *Manager) Status() State { m.mu.RLock(); defer m.mu.RUnlock(); return m.state }

// Restore makes a complete local generation available without contacting ADB.
// Android compatibility is checked separately before local mode is enabled.
func (m *Manager) Restore() (configuration.Config, error) {
	m.mu.RLock()
	cfg, program := m.cfg, m.program
	m.mu.RUnlock()
	prepared, err := Resolve(cfg, program)
	if err != nil {
		return cfg, err
	}
	if cleanupErr := cleanupGeneration(filepath.Dir(filepath.Dir(prepared.Data.Images))); cleanupErr != nil {
		slog.Warn("completed import cleanup failed", "error", cleanupErr)
	}
	var receipt receipt
	if err := readJSON(filepath.Join(filepath.Dir(filepath.Dir(prepared.Data.Images)), "receipt.json"), &receipt); err != nil {
		return cfg, err
	}
	m.mu.Lock()
	m.state = State{Phase: "ready", Message: "Installation prête", Ready: true, Completed: len(receipt.Images), Total: len(receipt.Images), Serial: prepared.Android.Serial}
	m.mu.Unlock()
	return prepared, nil
}
func (m *Manager) set(phase, message string) {
	m.mu.Lock()
	m.state.Phase = phase
	m.state.Message = message
	m.mu.Unlock()
}

type fileRecord struct {
	Hash     string `json:"hash"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}
type receipt struct {
	MasterKey string                `json:"masterKey,omitempty"`
	Key       string                `json:"key"`
	Files     map[string]fileRecord `json:"files"`
	Images    map[string]string     `json:"images"`
}
type imageItem struct {
	Output   string   `json:"output"`
	Category string   `json:"category"`
	Source   string   `json:"source"`
	Blobs    []string `json:"blobs"`
}
type imagePlan struct {
	Outputs []imageItem                `json:"outputs"`
	Blobs   map[string]json.RawMessage `json:"blobs"`
}

func digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func readJSON(path string, target any) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	return json.Unmarshal(b, target)
}
func WriteJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".publish-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
func safePath(root, relative string) (string, error) {
	if !filepath.IsLocal(relative) || strings.ContainsAny(relative, ":\\") || filepath.ToSlash(filepath.Clean(relative)) != relative {
		return "", fmt.Errorf("unsafe manifest path %q", relative)
	}
	return filepath.Join(root, filepath.FromSlash(relative)), nil
}
func fileHash(path string) (fileRecord, error) {
	f, e := os.Open(path)
	if e != nil {
		return fileRecord{}, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return fileRecord{}, e
	}
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return fileRecord{}, e
	}
	return fileRecord{hex.EncodeToString(h.Sum(nil)), info.Size(), info.ModTime().UnixNano()}, nil
}
func validFile(path string, want fileRecord) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != want.Size {
		return false
	}
	if info.ModTime().UnixNano() == want.Modified {
		return true
	}
	got, err := fileHash(path)
	return err == nil && got.Hash == want.Hash
}

// reusedRecord avoids reading an unchanged hard link again. A copied file has
// a new modification time and is hashed before it can enter the receipt.
func reusedRecord(path string, previous fileRecord) (fileRecord, error) {
	info, err := os.Stat(path)
	if err == nil && info.Size() == previous.Size && info.ModTime().UnixNano() == previous.Modified {
		return previous, nil
	}
	return fileHash(path)
}
func generationRoot(cfg configuration.Config) string {
	return filepath.Join(filepath.Dir(cfg.Path), "data", "generations")
}
func loadReceipt(root, key string) (receipt, error) {
	var r receipt
	if len(key) != 64 || strings.Trim(key, "0123456789abcdef") != "" {
		return r, fmt.Errorf("invalid data generation")
	}
	e := readJSON(filepath.Join(root, key, "receipt.json"), &r)
	if e == nil && r.Key != key {
		e = fmt.Errorf("generation identity differs")
	}
	return r, e
}
func profileKey(cfg configuration.Config, program string) (string, []byte, []byte, error) {
	plan, e := os.ReadFile(filepath.Join(program, "profiles", "images-"+cfg.Client.AppVersion+".json"))
	if e != nil {
		return "", nil, nil, e
	}
	index, e := os.ReadFile(filepath.Join(program, "profiles", "image-index-"+cfg.Client.AppVersion+".json"))
	if e != nil {
		return "", nil, nil, e
	}
	identity, _ := json.Marshal(struct {
		Client    any
		Contract  any
		Converter string
	}{cfg.Client, cfg.Contracts, converter})
	return digest(append(append(identity, plan...), index...)), plan, index, nil
}

// Resolve requires a generation for this exact profile. Legacy data must be
// adopted by Prepare rather than silently being mixed with another release.
func Resolve(cfg configuration.Config, program string) (configuration.Config, error) {
	key, _, _, err := profileKey(cfg, program)
	if err != nil {
		return cfg, err
	}
	root := generationRoot(cfg)
	r, err := loadReceipt(root, key)
	if err != nil {
		return cfg, fmt.Errorf("data preparation required: %w", err)
	}
	if len(r.Files) == 0 {
		return cfg, fmt.Errorf("empty data generation")
	}
	for rel, want := range r.Files {
		p, e := safePath(filepath.Join(root, key), rel)
		if e != nil || !validFile(p, want) {
			return cfg, fmt.Errorf("data file needs repair: %s", rel)
		}
	}
	cfg.Data.MasterData = filepath.Join(root, key, "master", "output")
	cfg.Data.Images = filepath.Join(root, key, "images", "images")
	return cfg, nil
}

// Prepare is serialized and cancellable; a failed attempt never changes active.json.
func (m *Manager) Prepare(ctx context.Context, serial string) (cfg configuration.Config, err error) {
	m.mu.Lock()
	if m.state.Busy {
		m.mu.Unlock()
		return m.cfg, fmt.Errorf("preparation already running")
	}
	m.state = State{Phase: "checking", Message: "Vérification du jeu et de l’émulateur", Busy: true}
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.state.Busy = false
		if err != nil {
			m.state.Ready = false
			m.state.Phase = "blocked"
			m.state.Error = err.Error()
			m.state.Message = "Préparation interrompue"
		} else {
			m.state.Ready = true
			m.state.Completed = m.state.Total
			m.state.Phase = "ready"
			m.state.Message = "Installation prête"
		}
	}()
	cfg = m.cfg
	if serial != "" {
		cfg.Android.Serial = serial
	}
	source, err := android.Inspect(ctx, android.SystemRunner(), cfg)
	if err != nil {
		return cfg, err
	}
	cfg.Android.Serial = source.Serial
	m.mu.Lock()
	m.state.Serial = source.Serial
	m.mu.Unlock()
	if ready, e := Resolve(cfg, m.program); e == nil {
		if cleanupErr := cleanupGeneration(filepath.Dir(filepath.Dir(ready.Data.Images))); cleanupErr != nil {
			slog.Warn("completed import cleanup failed", "error", cleanupErr)
		}
		return ready, nil
	}
	key, planBytes, index, err := profileKey(cfg, m.program)
	if err != nil {
		return cfg, err
	}
	var plan imagePlan
	if err = json.Unmarshal(planBytes, &plan); err != nil {
		return cfg, err
	}
	root := generationRoot(cfg)
	stage := filepath.Join(root, key+".pending")
	final := filepath.Join(root, key)
	if err = os.MkdirAll(stage, 0700); err != nil {
		return cfg, err
	}
	var previous struct {
		Key string `json:"key"`
	}
	_ = readJSON(filepath.Join(root, "active.json"), &previous)
	old, _ := loadReceipt(root, previous.Key)
	newReceipt := receipt{Key: key, Files: map[string]fileRecord{}, Images: map[string]string{}}
	var checkpoint receipt
	_ = readJSON(filepath.Join(stage, "checkpoint.json"), &checkpoint)
	if checkpoint.Key != key {
		checkpoint = receipt{}
	}
	masterKey := digest([]byte("master-v1:" + cfg.Client.MasterMemoryAladdinHash + ":" + cfg.Client.AndroidAssetAladdinHash))
	masterReady := false
	for _, candidate := range []struct {
		root    string
		receipt receipt
	}{{stage, checkpoint}, {filepath.Join(root, previous.Key), old}} {
		if candidate.receipt.MasterKey != masterKey {
			continue
		}
		valid := true
		count := 0
		for rel, record := range candidate.receipt.Files {
			if !strings.HasPrefix(rel, "master/output/") {
				continue
			}
			p, e := safePath(candidate.root, rel)
			if e != nil || !validFile(p, record) {
				valid = false
				break
			}
			count++
		}
		if !valid || count == 0 {
			continue
		}
		for rel, record := range candidate.receipt.Files {
			if !strings.HasPrefix(rel, "master/output/") {
				continue
			}
			target, e := safePath(stage, rel)
			if e != nil {
				return cfg, e
			}
			if candidate.root != stage {
				if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
					return cfg, e
				}
				if _, e = os.Stat(target); e == nil {
					if e = os.Remove(target); e != nil {
						return cfg, e
					}
				}
				if e = copyFile(filepath.Join(candidate.root, filepath.FromSlash(rel)), target); e != nil {
					return cfg, e
				}
				record, e = reusedRecord(target, record)
				if e != nil {
					return cfg, e
				}
			}
			newReceipt.Files[rel] = record
		}
		masterReady = true
		newReceipt.MasterKey = masterKey
		break
	}
	delta := imagePlan{Blobs: map[string]json.RawMessage{}}
	for _, item := range plan.Outputs {
		target, e := safePath(filepath.Join(stage, "images", "images"), item.Output)
		if e != nil {
			return cfg, e
		}
		parts := []any{converter, item}
		for _, hash := range item.Blobs {
			row, ok := plan.Blobs[hash]
			if !ok {
				return cfg, fmt.Errorf("missing image dependency")
			}
			parts = append(parts, row)
		}
		b, _ := json.Marshal(parts)
		identity := digest(b)
		newReceipt.Images[item.Output] = identity
		rel := "images/images/" + item.Output
		// Resume only images whose completion and identity were recorded.
		if checkpoint.Images[item.Output] == identity && validFile(target, checkpoint.Files[rel]) {
			newReceipt.Files[rel] = checkpoint.Files[rel]
			continue
		}
		// Reuse only validated completed files, never a partial import output.
		if old.Images[item.Output] == identity && validFile(filepath.Join(root, previous.Key, filepath.FromSlash(rel)), old.Files[rel]) {
			if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
				return cfg, e
			}
			if _, e = os.Stat(target); e == nil {
				if e = os.Remove(target); e != nil {
					return cfg, e
				}
			}
			if e = copyFile(filepath.Join(root, previous.Key, filepath.FromSlash(rel)), target); e != nil {
				return cfg, e
			}
			newReceipt.Files[rel], err = reusedRecord(target, old.Files[rel])
			if err != nil {
				return cfg, err
			}
			continue
		}
		delta.Outputs = append(delta.Outputs, item)
		for _, hash := range item.Blobs {
			delta.Blobs[hash] = plan.Blobs[hash]
		}
	}
	m.mu.Lock()
	m.state.Total = len(plan.Outputs)
	m.state.Completed = len(plan.Outputs) - len(delta.Outputs)
	m.mu.Unlock()
	if err = WriteJSON(filepath.Join(stage, "plan.json"), delta); err != nil {
		return cfg, err
	}
	if err = WriteJSON(filepath.Join(stage, "config.json"), cfg); err != nil {
		return cfg, err
	}
	reader := filepath.Join(m.program, "bin", "ptcgp-reader-"+source.ABI)
	if _, err = os.Stat(reader); err != nil {
		return cfg, fmt.Errorf("reader for %s is unavailable: %w", source.ABI, err)
	}
	apks, _ := json.Marshal(source.APKs)
	useSU := "0"
	if source.UseSU {
		useSU = "1"
	}
	env := append(os.Environ(), "PTCGP_ADB_SERIAL="+source.Serial, "PTCGP_SOURCE_ROOT="+source.Root, "PTCGP_USE_SU="+useSU, "PTCGP_APKS="+string(apks))
	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	if workers < 1 {
		workers = 1
	}
	for _, job := range []string{"master", "images"} {
		if job == "master" && masterReady {
			continue
		}
		if job == "images" && len(delta.Outputs) == 0 {
			continue
		}
		m.set(job, map[string]string{"master": "Extraction des données et des neuf langues", "images": "Création des images"}[job])
		cmd := exec.CommandContext(ctx, filepath.Join(m.program, "bin", "ptcgp-importer.exe"), job, filepath.Join(stage, "plan.json"), filepath.Join(stage, "config.json"), filepath.Join(stage, job), reader, strconv.Itoa(workers))
		hideWindow(cmd)
		cmd.Env = env
		cmd.WaitDelay = 3 * time.Second
		out, e := cmd.StdoutPipe()
		if e != nil {
			return cfg, e
		}
		var diagnostics strings.Builder
		cmd.Stderr = &diagnostics
		if e = cmd.Start(); e != nil {
			return cfg, e
		}
		defer cleanupReader(source, cmd.Process.Pid)
		scanner := bufio.NewScanner(out)
		scanner.Buffer(make([]byte, 4096), 4<<20)
		for scanner.Scan() {
			line := scanner.Bytes()
			var fileEvent struct {
				Phase string
				Path  string
			}
			if json.Unmarshal(line, &fileEvent) == nil && fileEvent.Phase == "file" {
				rel := "images/images/" + fileEvent.Path
				target, e := safePath(stage, rel)
				if e != nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					return cfg, e
				}
				if _, known := newReceipt.Images[fileEvent.Path]; !known {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					return cfg, fmt.Errorf("unexpected imported file")
				}
				record, e := fileHash(target)
				if e != nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					return cfg, e
				}
				newReceipt.Files[rel] = record
				if len(newReceipt.Files)%100 == 0 {
					if e = WriteJSON(filepath.Join(stage, "checkpoint.json"), newReceipt); e != nil {
						_ = cmd.Process.Kill()
						_ = cmd.Wait()
						return cfg, e
					}
				}
			}
			var progress struct {
				Path      string
				Phase     string
				Completed int
				Total     int
			}
			if json.Unmarshal(scanner.Bytes(), &progress) == nil && progress.Phase == "images" {
				m.mu.Lock()
				m.state.Completed = len(plan.Outputs) - len(delta.Outputs) + progress.Completed
				m.mu.Unlock()
			}
		}
		if e = scanner.Err(); e != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return cfg, e
		}
		if saveErr := WriteJSON(filepath.Join(stage, "checkpoint.json"), newReceipt); saveErr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return cfg, saveErr
		}
		if e = cmd.Wait(); e != nil {
			return cfg, fmt.Errorf("%s import: %w: %s", job, e, diagnostics.String())
		}
		if job == "master" {
			if e = collectFiles(stage, "master/output", newReceipt.Files); e != nil {
				return cfg, e
			}
			newReceipt.MasterKey = masterKey
			if e = WriteJSON(filepath.Join(stage, "checkpoint.json"), newReceipt); e != nil {
				return cfg, e
			}
		}
	}
	m.set("validating", "Vérification des fichiers et du catalogue")
	if err = os.MkdirAll(filepath.Join(stage, "images", "images"), 0700); err != nil {
		return cfg, err
	}
	if err = os.WriteFile(filepath.Join(stage, "images", "images", "index.json"), index, 0600); err != nil {
		return cfg, err
	}
	for _, locale := range []string{"de_DE", "en_US", "es_ES", "fr_FR", "it_IT", "ja_JP", "ko_KR", "pt_BR", "zh_TW"} {
		if _, err = catalog.OpenLocale(filepath.Join(stage, "master", "output"), locale, "en_US"); err != nil {
			return cfg, err
		}
	}
	for _, item := range plan.Outputs {
		if _, err = os.Stat(filepath.Join(stage, "images", "images", filepath.FromSlash(item.Output))); err != nil {
			return cfg, err
		}
	}
	for _, sub := range []string{"master/output", "images/images"} {
		err = filepath.WalkDir(filepath.Join(stage, filepath.FromSlash(sub)), func(p string, d os.DirEntry, e error) error {
			if e != nil {
				return e
			}
			if d.IsDir() {
				return nil
			}
			rel, e := filepath.Rel(stage, p)
			if e != nil {
				return e
			}
			rel = filepath.ToSlash(rel)
			if record, ok := newReceipt.Files[rel]; ok && validFile(p, record) {
				return nil
			}
			r, e := fileHash(p)
			if e != nil {
				return e
			}
			newReceipt.Files[rel] = r
			return nil
		})
		if err != nil {
			return cfg, err
		}
	}
	// The source revision is checked again before publishing.
	if _, err = android.Inspect(ctx, android.SystemRunner(), cfg); err != nil {
		return cfg, err
	}
	if err = WriteJSON(filepath.Join(stage, "receipt.json"), newReceipt); err != nil {
		return cfg, err
	}
	damaged := ""
	if _, e := os.Stat(final); e == nil {
		damaged = final + ".damaged-" + strconv.FormatInt(time.Now().UnixNano(), 10)
		if err = os.Rename(final, damaged); err != nil {
			return cfg, err
		}
	}
	if err = os.Rename(stage, final); err != nil {
		if damaged != "" {
			if restoreErr := os.Rename(damaged, final); restoreErr != nil {
				return cfg, fmt.Errorf("publish: %v; restore previous generation: %w", err, restoreErr)
			}
		}
		return cfg, err
	}
	if err = WriteJSON(filepath.Join(root, "active.json"), map[string]string{"key": key}); err != nil {
		return cfg, err
	}
	if cleanupErr := cleanupGeneration(final); cleanupErr != nil {
		slog.Warn("completed import cleanup failed", "error", cleanupErr)
	}
	return Resolve(cfg, m.program)
}

// cleanupGeneration removes import-only files after a generation is complete.
// A pending generation keeps its checkpoint until publication succeeds.
func cleanupGeneration(directory string) error {
	paths := []string{
		"checkpoint.json", "plan.json", "config.json",
		filepath.Join("images", "stream-processing.json"),
		filepath.Join("images", "stream-report.json"),
		filepath.Join("master", "master-report.json"),
	}
	for i := 0; i < 8; i++ {
		paths = append(paths, filepath.Join("images", fmt.Sprintf("stream-manifest-%d.json", i)))
	}
	for _, relative := range paths {
		if err := os.Remove(filepath.Join(directory, relative)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove completed import file %s: %w", relative, err)
		}
	}
	return nil
}

func cleanupReader(source android.Source, pid int) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	prefix := "/data/local/tmp/ptcgp-stream-benchmark-" + strconv.Itoa(pid)
	paths := []string{prefix + ".bin", "/data/local/tmp/ptcgp-master-" + strconv.Itoa(pid) + ".bin"}
	for i := 0; i < 8; i++ {
		paths = append(paths, fmt.Sprintf("%s-%d.json", prefix, i), fmt.Sprintf("%s-%d.log", prefix, i))
	}
	for i := range paths {
		paths[i] = android.ShellQuote(paths[i])
	}
	command := "rm -f " + strings.Join(paths, " ")
	if source.UseSU {
		command = "su -c " + android.ShellQuote(command)
	}
	// Best effort after disconnect: these uniquely named temporary files contain
	// no key material or user data and a later import uses a different process ID.
	_, _ = android.SystemRunner().Run(ctx, "adb", "-s", source.Serial, "shell", command)
}

func copyFile(from, to string) error {
	if e := os.Link(from, to); e == nil {
		return nil
	}
	in, e := os.Open(from)
	if e != nil {
		return e
	}
	defer in.Close()
	out, e := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	_, e = io.Copy(out, in)
	closeErr := out.Close()
	if e != nil {
		return e
	}
	return closeErr
}

func (m *Manager) Fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Ready = false
	m.state.Busy = false
	m.state.Phase = "blocked"
	m.state.Error = err.Error()
}

// Configure selects a validated profile while no import is running.
func (m *Manager) Configure(cfg configuration.Config, program string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Busy {
		return fmt.Errorf("import already running")
	}
	m.cfg = cfg
	m.program = program
	return nil
}

func collectFiles(root, sub string, files map[string]fileRecord) error {
	return filepath.WalkDir(filepath.Join(root, filepath.FromSlash(sub)), func(p string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() {
			return nil
		}
		record, e := fileHash(p)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(root, p)
		if e != nil {
			return e
		}
		files[filepath.ToSlash(rel)] = record
		return nil
	})
}
