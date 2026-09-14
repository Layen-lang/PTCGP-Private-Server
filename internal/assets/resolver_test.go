package assets

import (
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolverCreatesAndCachesBoundedThumbnail(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cards"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), []byte(`{"pk_thumb_test":"cards/card.png"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(filepath.Join(root, "cards", "card.png"))
	if err != nil {
		t.Fatal(err)
	}
	source := image.NewNRGBA(image.Rect(0, 0, 400, 600))
	for index := range source.Pix {
		source.Pix[index] = color.NRGBA{R: 23, G: 122, B: 101, A: 255}.R
	}
	if err := png.Encode(file, source); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	resolver, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	resolver.ServeHTTP(response, httptest.NewRequest(http.MethodGet, resolver.ThumbnailURL("PK_THUMB_TEST", 120), nil))
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("thumbnail status=%d type=%s", response.Code, response.Header().Get("Content-Type"))
	}
	decoded, err := png.Decode(strings.NewReader(response.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Bounds().Dx() != 120 || decoded.Bounds().Dy() != 180 {
		t.Fatalf("thumbnail bounds=%v", decoded.Bounds())
	}
}

func TestResolverOnlyServesAllowlistedImages(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "cards"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), []byte(`{"pk_10_000010_00":"cards/card.png"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cards", "card.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := resolver.URL("PK_10_000010_00"); !strings.HasPrefix(got, "/assets/image/") {
		t.Fatalf("URL() = %q", got)
	}

	allowed := httptest.NewRecorder()
	resolver.ServeHTTP(allowed, httptest.NewRequest(http.MethodGet, resolver.URL("PK_10_000010_00"), nil))
	if allowed.Code != http.StatusOK {
		t.Fatalf("allowed status = %d", allowed.Code)
	}

	unsafeToken := base64.RawURLEncoding.EncodeToString([]byte("../index.json"))
	blocked := httptest.NewRecorder()
	resolver.ServeHTTP(blocked, httptest.NewRequest(http.MethodGet, "/assets/image/"+unsafeToken, nil))
	if blocked.Code != http.StatusNotFound {
		t.Fatalf("blocked status = %d", blocked.Code)
	}
}

func TestResolverAcceptsIndexOutputsPrefixedWithImagesRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "images")
	if err := os.MkdirAll(filepath.Join(root, "cards"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), []byte(`{"pack":"images/cards/pack.png"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "cards", "pack.png"), []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	resolver.ServeHTTP(response, httptest.NewRequest(http.MethodGet, resolver.URL("PACK"), nil))
	if response.Code != http.StatusOK {
		t.Fatalf("prefixed output status=%d", response.Code)
	}
}

func TestResolverUsesExactCaseInsensitiveIndexMapping(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "Pack", "icons"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.json"), []byte(`{"pack_asset":"Pack/icons/PACK_ASSET.png"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resolver, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	decodedURL := strings.TrimPrefix(resolver.URL("PACK_ASSET"), "/assets/image/")
	decoded, err := base64.RawURLEncoding.DecodeString(decodedURL)
	if err != nil || string(decoded) != "Pack/icons/PACK_ASSET.png" {
		t.Fatalf("selected image=%q err=%v", decoded, err)
	}
	if got := resolver.URL("missing_asset"); got != "" {
		t.Fatalf("URL() for an unknown identifier = %q", got)
	}
}

func TestResolverRejectsUnsafeIndexOutput(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.json"), []byte(`{"unsafe":"../outside.png"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err == nil || !strings.Contains(err.Error(), "unsafe output") {
		t.Fatalf("Open() error = %v", err)
	}
}
