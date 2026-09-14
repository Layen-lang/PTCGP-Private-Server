// Package gamelocale defines the account locales supported by the game protocol.
package gamelocale

import "strings"

const (
	DefaultLocale  = "fr_FR"
	DefaultCountry = "FR"
)

var localeByLanguage = map[int32]string{
	1: "ja_JP", 2: "en_US", 3: "zh_TW", 4: "fr_FR",
	5: "it_IT", 6: "de_DE", 7: "es_ES", 8: "pt_BR",
	9: "ko_KR", 10: "es_419", 11: "zh_CN", 12: "id_ID",
}

// LocaleForLanguage converts a protocol language to an account locale.
func LocaleForLanguage(language int32) (string, bool) {
	locale, ok := localeByLanguage[language]
	return locale, ok
}

// LanguageForLocale converts an account locale to a protocol language.
func LanguageForLocale(locale string) (int32, bool) {
	for language, candidate := range localeByLanguage {
		if strings.EqualFold(strings.TrimSpace(locale), candidate) {
			return language, true
		}
	}
	return 0, false
}

// NormalizeLocale returns the canonical spelling of a supported locale.
func NormalizeLocale(locale string) (string, bool) {
	language, ok := LanguageForLocale(locale)
	if !ok {
		return "", false
	}
	return localeByLanguage[language], true
}

// NormalizeCountry validates and canonicalizes a two-letter region code.
func NormalizeCountry(country string) (string, bool) {
	country = strings.ToUpper(strings.TrimSpace(country))
	if len(country) != 2 {
		return "", false
	}
	for _, character := range country {
		if character < 'A' || character > 'Z' {
			return "", false
		}
	}
	return country, true
}
