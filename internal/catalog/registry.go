package catalog

import (
	"fmt"
	"sort"
	"strings"
)

// Registry owns the immutable catalog adapters available to presentation
// layers. Game behavior keeps using Default; Locale only changes displayed
// master-data text.
type Registry struct {
	defaultLocale string
	catalogs      map[string]*Catalog
}

// OpenRegistry loads each requested locale once. Missing localized tables keep
// using the catalog's English fallback.
func OpenRegistry(root, defaultLocale string, locales ...string) (*Registry, error) {
	if defaultLocale == "" {
		defaultLocale = DefaultLocale
	}
	requested := append([]string{defaultLocale}, locales...)
	catalogs := make(map[string]*Catalog, len(requested))
	for _, locale := range requested {
		locale = strings.TrimSpace(locale)
		if locale == "" {
			continue
		}
		if _, exists := catalogs[locale]; exists {
			continue
		}
		loaded, err := OpenLocale(root, locale, FallbackLocale)
		if err != nil {
			return nil, fmt.Errorf("load catalog locale %s: %w", locale, err)
		}
		catalogs[locale] = loaded
	}
	if catalogs[defaultLocale] == nil {
		return nil, fmt.Errorf("default catalog locale %s is unavailable", defaultLocale)
	}
	return &Registry{defaultLocale: defaultLocale, catalogs: catalogs}, nil
}

// Default returns the catalog used for game behavior and locale fallbacks.
func (r *Registry) Default() *Catalog { return r.catalogs[r.defaultLocale] }

// For returns the requested locale and falls back to the configured default.
func (r *Registry) For(locale string) *Catalog {
	if current := r.catalogs[strings.TrimSpace(locale)]; current != nil {
		return current
	}
	return r.Default()
}

// Locales returns the loaded locale identifiers in stable order.
func (r *Registry) Locales() []string {
	result := make([]string, 0, len(r.catalogs))
	for locale := range r.catalogs {
		result = append(result, locale)
	}
	sort.Strings(result)
	return result
}
