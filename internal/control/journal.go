package control

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// LogEvent is one timestamped operation or diagnostic message.
type LogEvent struct {
	Time      time.Time `json:"time"`
	Level     string    `json:"level"`
	Operation string    `json:"operation"`
	Message   string    `json:"message"`
}

// Journal persists control operations independently of the game server.
type Journal struct {
	mu   sync.Mutex
	path string
}

// NewJournal creates the directory used for the bounded operation log.
func NewJournal(directory string) (*Journal, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	return &Journal{path: filepath.Join(directory, "control.jsonl")}, nil
}

// Record appends one event. Logging failures are reported to the launcher log.
func (j *Journal) Record(operation, level, message string) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.append(LogEvent{Time: time.Now().UTC(), Level: level, Operation: operation, Message: message}); err != nil {
		slog.Error("write control journal", "error", err)
	}
}

func (j *Journal) append(event LogEvent) error {
	if info, err := os.Stat(j.path); err == nil && info.Size() > 4<<20 {
		if err := os.Remove(j.path + ".1"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(j.path, j.path+".1"); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(j.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(event)
}

// Recent returns the latest persisted events, including the previous rotation.
func (j *Journal) Recent() ([]LogEvent, error) {
	result := make([]LogEvent, 0)
	if j == nil {
		return result, nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, path := range []string{j.path + ".1", j.path} {
		lines, err := tailLines(path, 1<<20)
		if err != nil {
			return nil, err
		}
		for _, line := range lines {
			var event LogEvent
			if json.Unmarshal([]byte(line), &event) == nil {
				result = append(result, event)
			}
		}
	}
	if len(result) > 1000 {
		result = result[len(result)-1000:]
	}
	return result, nil
}

func tailLines(path string, limit int64) ([]string, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	skip := info.Size() > limit
	if skip {
		if _, err := f.Seek(-limit, io.SeekEnd); err != nil {
			return nil, err
		}
	}
	scanner := bufio.NewScanner(io.LimitReader(f, limit))
	scanner.Buffer(make([]byte, 4096), int(limit)+1)
	var lines []string
	for scanner.Scan() {
		if skip {
			skip = false
			continue
		}
		if line := strings.TrimSpace(scanner.Text()); line != "" {
			lines = append(lines, line)
		}
	}
	return lines, scanner.Err()
}

// Diagnostics reads only the two known server output files, never arbitrary paths.
func (j *Journal) Diagnostics() ([]string, error) {
	result := make([]string, 0)
	if j == nil {
		return result, nil
	}
	for _, name := range []string{"server.stdout.log", "server.stderr.log"} {
		lines, err := tailLines(filepath.Join(filepath.Dir(j.path), name), 256<<10)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", name, err)
		}
		for _, line := range lines {
			result = append(result, name+" · "+line)
		}
	}
	if len(result) > 500 {
		result = result[len(result)-500:]
	}
	return result, nil
}
