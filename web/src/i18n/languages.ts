export const LANGUAGES = [
  { locale: 'fr_FR', htmlLang: 'fr-FR', label: 'Français' },
  { locale: 'en_US', htmlLang: 'en-US', label: 'English' },
] as const

export type Locale = (typeof LANGUAGES)[number]['locale']
export const DEFAULT_LOCALE: Locale = 'en_US'

export function isLocale(value: string | null): value is Locale {
  return LANGUAGES.some((language) => language.locale === value)
}
