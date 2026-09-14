// Package binarypatch applies hash- and byte-guarded reversible binary patches.
package binarypatch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// State describes whether a file is original or already patched.
type State string

const (
	// StateOriginal means source hash and expected bytes both match.
	StateOriginal State = "original"
	// StatePatched means the complete target hash matches.
	StatePatched State = "patched"
)

// Manifest pins a patch to one exact source and target binary.
type Manifest struct {
	Name           string `json:"name"`
	Version        string `json:"version"`
	Library        string `json:"library"`
	SourceSHA256   string `json:"source_sha256"`
	TargetSHA256   string `json:"target_sha256"`
	Offset         int64  `json:"offset"`
	ExpectedHex    string `json:"expected_hex"`
	ReplacementHex string `json:"replacement_hex"`
	Rationale      string `json:"rationale"`
}

var (
	versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
	libraryPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	hashPattern    = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

// Validate rejects incomplete manifests before they reach a target binary.
func (manifest Manifest) Validate() error {
	if strings.TrimSpace(manifest.Name) == "" {
		return fmt.Errorf("patch name is required")
	}
	if !versionPattern.MatchString(manifest.Version) {
		return fmt.Errorf("invalid patch version")
	}
	if !libraryPattern.MatchString(manifest.Library) || manifest.Library == "." || manifest.Library == ".." {
		return fmt.Errorf("invalid patch library")
	}
	if !hashPattern.MatchString(manifest.SourceSHA256) || !hashPattern.MatchString(manifest.TargetSHA256) {
		return fmt.Errorf("invalid patch SHA-256")
	}
	expected, err := hex.DecodeString(manifest.ExpectedHex)
	if err != nil {
		return fmt.Errorf("decode expected bytes: %w", err)
	}
	replacement, err := hex.DecodeString(manifest.ReplacementHex)
	if err != nil {
		return fmt.Errorf("decode replacement bytes: %w", err)
	}
	if len(expected) == 0 || len(expected) != len(replacement) || manifest.Offset < 0 {
		return fmt.Errorf("invalid patch span")
	}
	return nil
}

// LoadManifest reads and validates a JSON patch manifest.
func LoadManifest(path string) (Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read patch manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("parse patch manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// Verify checks a file without changing it.
func Verify(path string, manifest Manifest) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read target: %w", err)
	}
	hash := fileHash(data)
	if hash == manifest.TargetSHA256 {
		return StatePatched, nil
	}
	if hash != manifest.SourceSHA256 {
		return "", fmt.Errorf("unexpected source SHA-256 %s", hash)
	}
	expected, _ := hex.DecodeString(manifest.ExpectedHex)
	end := manifest.Offset + int64(len(expected))
	if end > int64(len(data)) {
		return "", fmt.Errorf("patch offset lies outside target")
	}
	if !equal(data[manifest.Offset:end], expected) {
		return "", fmt.Errorf("expected bytes do not match at offset 0x%x", manifest.Offset)
	}
	return StateOriginal, nil
}

// Apply creates an exclusive backup and applies the exact replacement bytes.
// Reapplying an already patched file succeeds without changing it.
func Apply(path, backupPath string, manifest Manifest) (State, error) {
	state, err := Verify(path, manifest)
	if err != nil || state == StatePatched {
		return state, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat target: %w", err)
	}
	backup, err := os.OpenFile(backupPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if errors.Is(err, os.ErrExist) {
		existing, readErr := os.ReadFile(backupPath)
		if readErr != nil {
			return "", fmt.Errorf("read existing backup: %w", readErr)
		}
		if got := fileHash(existing); got != manifest.SourceSHA256 {
			return "", fmt.Errorf("existing backup SHA-256 %s does not match source", got)
		}
	} else if err != nil {
		return "", fmt.Errorf("create exclusive backup: %w", err)
	} else {
		if _, err := backup.Write(data); err != nil {
			_ = backup.Close()
			return "", fmt.Errorf("write backup: %w", err)
		}
		if err := backup.Close(); err != nil {
			return "", fmt.Errorf("close backup: %w", err)
		}
	}
	replacement, _ := hex.DecodeString(manifest.ReplacementHex)
	copy(data[manifest.Offset:], replacement)
	if got := fileHash(data); got != manifest.TargetSHA256 {
		return "", fmt.Errorf("patched SHA-256 %s does not match manifest", got)
	}
	if err := os.WriteFile(path, data, info.Mode().Perm()); err != nil {
		return "", fmt.Errorf("write patched target: %w", err)
	}
	return StatePatched, nil
}

// Rollback restores a verified source backup over a verified patched target.
func Rollback(path, backupPath string, manifest Manifest) error {
	state, err := Verify(path, manifest)
	if err != nil {
		return err
	}
	if state != StatePatched {
		return errors.New("target is not patched")
	}
	backup, err := os.ReadFile(backupPath)
	if err != nil {
		return fmt.Errorf("read backup: %w", err)
	}
	if got := fileHash(backup); got != manifest.SourceSHA256 {
		return fmt.Errorf("backup SHA-256 %s does not match source", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat target: %w", err)
	}
	if err := os.WriteFile(path, backup, info.Mode().Perm()); err != nil {
		return fmt.Errorf("restore backup: %w", err)
	}
	return nil
}

func fileHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func equal(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for i := range left {
		different |= left[i] ^ right[i]
	}
	return different == 0
}
