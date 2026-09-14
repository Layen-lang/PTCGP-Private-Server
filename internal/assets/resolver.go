// Package assets exposes an allowlisted, read-only view of datamined images.
package assets

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

var ErrUnavailable = errors.New("image index unavailable")

// Resolver maps master-data identifiers to immutable image files.
type Resolver struct {
	canonical map[string]string
	root      string
	allowed   map[string]struct{}
	mu        sync.RWMutex
	cache     map[string]string
	thumbMu   sync.Mutex
	thumbs    map[string][]byte
	order     []string
}

// Open loads the image allowlist. An empty root returns ErrUnavailable.
func Open(root string) (*Resolver, error) {
	if strings.TrimSpace(root) == "" {
		return nil, ErrUnavailable
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve image root: %w", err)
	}
	data, err := os.ReadFile(filepath.Join(abs, "index.json"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var raw map[string]string
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse image index: %w", err)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: index contains no images", ErrUnavailable)
	}
	canonical := make(map[string]string, len(raw))
	allowed := make(map[string]struct{}, len(raw))
	for identifier, output := range raw {
		identifier = strings.ToLower(strings.TrimSpace(identifier))
		if identifier == "" {
			return nil, fmt.Errorf("image index contains an empty identifier")
		}
		rel, ok := cleanRelative(output)
		if !ok {
			return nil, fmt.Errorf("image index contains unsafe output %q", output)
		}
		if prefix := filepath.Base(abs) + "/"; strings.HasPrefix(rel, prefix) {
			rel = strings.TrimPrefix(rel, prefix)
		}
		canonical[identifier] = rel
		allowed[rel] = struct{}{}
	}
	return &Resolver{canonical: canonical, root: abs, allowed: allowed, cache: make(map[string]string), thumbs: make(map[string][]byte)}, nil
}

// ThumbnailURL returns a cached, resized variant suitable for catalogue grids.
func (r *Resolver) ThumbnailURL(identifier string, width int) string {
	value := r.URL(identifier)
	if value == "" {
		return ""
	}
	if width < 48 {
		width = 48
	}
	if width > 480 {
		width = 480
	}
	return value + "?w=" + strconv.Itoa(width)
}

// URL returns the local admin URL for the best image matching identifier.
func (r *Resolver) URL(identifier string) string {
	if r == nil {
		return ""
	}
	identifier = strings.ToLower(strings.TrimSpace(identifier))
	if identifier == "" {
		return ""
	}
	r.mu.RLock()
	if value, ok := r.cache[identifier]; ok {
		r.mu.RUnlock()
		return value
	}
	r.mu.RUnlock()

	value := ""
	if output, ok := r.canonical[identifier]; ok {
		value = "/assets/image/" + base64.RawURLEncoding.EncodeToString([]byte(output))
	}
	r.mu.Lock()
	r.cache[identifier] = value
	r.mu.Unlock()
	return value
}

// ServeHTTP serves only files listed in index.json.
func (r *Resolver) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	if r == nil || request.Method != http.MethodGet {
		http.NotFound(w, request)
		return
	}
	token := strings.TrimPrefix(request.URL.Path, "/assets/image/")
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		http.NotFound(w, request)
		return
	}
	rel, ok := cleanRelative(string(decoded))
	if !ok {
		http.NotFound(w, request)
		return
	}
	if _, ok := r.allowed[rel]; !ok {
		http.NotFound(w, request)
		return
	}
	w.Header().Set("Cache-Control", "private, max-age=86400, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if width, err := strconv.Atoi(request.URL.Query().Get("w")); err == nil && width >= 48 && width <= 480 {
		if data, err := r.thumbnail(rel, width); err == nil {
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Length", strconv.Itoa(len(data)))
			_, _ = w.Write(data)
			return
		}
	}
	http.ServeFile(w, request, filepath.Join(r.root, filepath.FromSlash(rel)))
}

func (r *Resolver) thumbnail(rel string, width int) ([]byte, error) {
	key := rel + "@" + strconv.Itoa(width)
	r.thumbMu.Lock()
	if value, ok := r.thumbs[key]; ok {
		r.thumbMu.Unlock()
		return value, nil
	}
	r.thumbMu.Unlock()

	file, err := os.Open(filepath.Join(r.root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	source, _, err := image.Decode(file)
	_ = file.Close()
	if err != nil {
		return nil, err
	}
	bounds := source.Bounds()
	if bounds.Dx() <= 0 || bounds.Dy() <= 0 {
		return nil, fmt.Errorf("invalid image dimensions")
	}
	height := max(1, bounds.Dy()*width/bounds.Dx())
	destination := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		sy := bounds.Min.Y + y*bounds.Dy()/height
		for x := 0; x < width; x++ {
			sx := bounds.Min.X + x*bounds.Dx()/width
			destination.SetNRGBA(x, y, color.NRGBAModel.Convert(source.At(sx, sy)).(color.NRGBA))
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, destination); err != nil {
		return nil, err
	}
	data := buffer.Bytes()
	r.thumbMu.Lock()
	if len(r.order) >= 256 {
		delete(r.thumbs, r.order[0])
		r.order = r.order[1:]
	}
	r.thumbs[key] = data
	r.order = append(r.order, key)
	r.thumbMu.Unlock()
	return data, nil
}

func cleanRelative(value string) (string, bool) {
	value = filepath.ToSlash(strings.TrimSpace(value))
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if cleaned == "." || strings.HasPrefix(cleaned, "../") || filepath.IsAbs(cleaned) || strings.Contains(cleaned, ":") {
		return "", false
	}
	return cleaned, true
}
