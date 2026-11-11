package catalog

import (
	"fmt"
	"sort"
	"strings"
	"sync"
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
	unique := make([]string, 0, len(requested))
	seen := make(map[string]bool, len(requested))
	for _, locale := range requested {
		locale = strings.TrimSpace(locale)
		if locale == "" || seen[locale] {
			continue
		}
		seen[locale] = true
		unique = append(unique, locale)
	}
	// Locales are independent and immutable: decode them concurrently.
	loaded := make([]*Catalog, len(unique))
	errs := make([]error, len(unique))
	var wg sync.WaitGroup
	for i, locale := range unique {
		wg.Add(1)
		go func() {
			defer wg.Done()
			loaded[i], errs[i] = OpenLocale(root, locale, FallbackLocale)
		}()
	}
	wg.Wait()
	catalogs := make(map[string]*Catalog, len(unique))
	for i, locale := range unique {
		if errs[i] != nil {
			return nil, fmt.Errorf("load catalog locale %s: %w", locale, errs[i])
		}
		catalogs[locale] = loaded[i]
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
