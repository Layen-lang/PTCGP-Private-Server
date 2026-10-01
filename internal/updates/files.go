package updates

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type savedFile struct {
	path    string
	existed bool
}

// replacement keeps original files until the new panel has started successfully.
type replacement struct {
	root, backup string
	files        []savedFile
}

func newReplacement(root, key string) (*replacement, error) {
	backup, err := os.MkdirTemp(filepath.Join(root, "data", "updates"), "backup-"+key[:12]+"-")
	if err != nil {
		return nil, err
	}
	return &replacement{root: root, backup: backup}, nil
}

func (r *replacement) save(path string) error {
	from := filepath.Join(r.root, filepath.FromSlash(path))
	info, err := os.Lstat(from)
	if errors.Is(err, os.ErrNotExist) {
		r.files = append(r.files, savedFile{path: path})
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("update target is not a regular file: %s", path)
	}
	to := filepath.Join(r.backup, filepath.FromSlash(path))
	if err = os.MkdirAll(filepath.Dir(to), 0700); err != nil {
		return err
	}
	if err = Copy(from, to); err != nil {
		return err
	}
	r.files = append(r.files, savedFile{path: path, existed: true})
	return nil
}

func (r *replacement) restore() error {
	var failures []error
	for i := len(r.files) - 1; i >= 0; i-- {
		file := r.files[i]
		target := filepath.Join(r.root, filepath.FromSlash(file.path))
		var err error
		if file.existed {
			err = replaceFile(filepath.Join(r.backup, filepath.FromSlash(file.path)), target)
		} else {
			err = os.Remove(target)
			if errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("restore %s: %w", file.path, err))
		}
	}
	return errors.Join(failures...)
}

// replaceFile waits for an older Windows entry point to release its executable.
// The destination is replaced atomically, so a locked file is never truncated.
func replaceFile(from, to string) error {
	if err := os.MkdirAll(filepath.Dir(to), 0700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(to), ".update-*.next.exe")
	if err != nil {
		return err
	}
	name := temporary.Name()
	if err = temporary.Close(); err != nil {
		return err
	}
	defer os.Remove(name)
	if err = Copy(from, name); err != nil {
		return err
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		err = os.Rename(name, to)
		if err == nil {
			return nil
		}
		if !fileBusy(err) || time.Now().After(deadline) {
			return fmt.Errorf("replace %s: %w", to, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// releaseConfig refreshes compatibility metadata while retaining local settings.
func releaseConfig(local, published string) ([]byte, error) {
	read := func(path string) (map[string]json.RawMessage, error) {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		var config map[string]json.RawMessage
		err = json.Unmarshal(data, &config)
		return config, err
	}
	config, err := read(local)
	if err != nil {
		return nil, err
	}
	release, err := read(published)
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"client", "contracts", "patch"} {
		config[field] = release[field]
	}
	var android, publishedAndroid map[string]json.RawMessage
	if err = json.Unmarshal(config["android"], &android); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(release["android"], &publishedAndroid); err != nil {
		return nil, err
	}
	android["redirectHosts"] = publishedAndroid["redirectHosts"]
	config["android"], err = json.Marshal(android)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(config, "", "  ")
}
