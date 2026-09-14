import { useEffect, useState } from 'react'
import enUS from './locales/en_US'
import frFR from './locales/fr_FR'
import type { TranslationKey } from './locales/fr_FR'
import { DEFAULT_LOCALE, isLocale, LANGUAGES } from './languages'
import type { Locale } from './languages'

export { DEFAULT_LOCALE, LANGUAGES }
export type { Locale, TranslationKey }

const STORAGE_KEY = 'ptcgp-interface-locale-v1'
const resources = { fr_FR: frFR, en_US: enUS } as const
let activeLocale: Locale = readStoredLocale()

function readStoredLocale(): Locale {
  if (typeof window === 'undefined') return DEFAULT_LOCALE
  const stored = window.localStorage.getItem(STORAGE_KEY)
  return isLocale(stored) ? stored : DEFAULT_LOCALE
}

function interpolate(message: string, values?: Record<string, string | number>) {
  if (!values) return message
  return message.replace(/\{(\w+)\}/g, (_, key: string) => String(values[key] ?? `{${key}}`))
}

export function getLocale(): Locale { return activeLocale }

export function setLocale(locale: Locale) {
  activeLocale = locale
  window.localStorage.setItem(STORAGE_KEY, locale)
  document.documentElement.lang = LANGUAGES.find((language) => language.locale === locale)?.htmlLang ?? 'fr-FR'
  window.dispatchEvent(new CustomEvent('ptcgp-locale-change', { detail: locale }))
}

export function t(key: TranslationKey, values?: Record<string, string | number>) {
  return interpolate(resources[activeLocale][key] ?? frFR[key], values)
}

function intlLocale() {
  return LANGUAGES.find((language) => language.locale === activeLocale)?.htmlLang ?? 'fr-FR'
}

export function formatNumber(value: number) { return new Intl.NumberFormat(intlLocale()).format(value) }
export function formatDate(value: string | number | Date, options?: Intl.DateTimeFormatOptions) { return new Intl.DateTimeFormat(intlLocale(), options).format(new Date(value)) }
export function localeLower(value: string) { return value.toLocaleLowerCase(intlLocale()) }

export function useLocale() {
  const [locale, update] = useState<Locale>(activeLocale)
  useEffect(() => {
    const onChange = (event: Event) => update((event as CustomEvent<Locale>).detail)
    window.addEventListener('ptcgp-locale-change', onChange)
    return () => window.removeEventListener('ptcgp-locale-change', onChange)
  }, [])
  return locale
}

document.documentElement.lang = intlLocale()
