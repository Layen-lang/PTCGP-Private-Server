// Package installation resolves immutable program versions without moving user data.
package installation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Selection struct {
	Key      string `json:"key"`
	Previous string `json:"previous,omitempty"`
	Version  string `json:"version"`
}

func ValidKey(s string) bool { return len(s) == 64 && strings.Trim(s, "0123456789abcdef") == "" }
func Program(root string) (string, error) {
	b, e := os.ReadFile(filepath.Join(root, "data", "updates", "current.json"))
	if os.IsNotExist(e) {
		return root, nil
	}
	if e != nil {
		return "", e
	}
	var selected Selection
	if e = json.Unmarshal(b, &selected); e != nil {
		return "", e
	}
	if !ValidKey(selected.Key) {
		return "", fmt.Errorf("invalid installed version")
	}
	return filepath.Join(root, "data", "updates", "versions", selected.Key), nil
}
