export type ThemePreference = 'system' | 'light' | 'dark'
export type ThemeMode = Exclude<ThemePreference, 'system'>

export type ThemeScheme = {
  presetId: string
  accent: string
  background: string
  foreground: string
  contrast: number
}

export type ThemeSettings = {
  version: 3
  preference: ThemePreference
  light: ThemeScheme
  dark: ThemeScheme
  uiFontSize: number
}

export type ThemePreset = {
  id: string
  label: string
  mode: ThemeMode
  scheme: ThemeScheme
}

const STORAGE_KEY = 'ptcgp-theme-settings-v3'
const PREVIOUS_STORAGE_KEY = 'ptcgp-theme-settings-v2'
const LEGACY_STORAGE_KEY = 'ptcgp-interface-theme'
const HEX_COLOR = /^#[0-9a-f]{6}$/i

function preset(id: string, label: string, mode: ThemeMode, accent: string, background: string, foreground: string, contrast = mode === 'dark' ? 58 : 45): ThemePreset {
  return { id, label, mode, scheme: { presetId: id, accent, background, foreground, contrast } }
}

// Presets exposed by the current Codex desktop app, adapted to the PTCGP chrome.
export const THEME_PRESETS: ThemePreset[] = [
  preset('absolutely-light', 'Absolutely', 'light', '#7557E8', '#FAFAFC', '#17151C'),
  preset('catppuccin-latte', 'Catppuccin', 'light', '#8839EF', '#EFF1F5', '#4C4F69'),
  preset('codex-light', 'Codex', 'light', '#0285FF', '#FFFFFF', '#0D0D0D'),
  preset('everforest-light', 'Everforest', 'light', '#8DA101', '#FDF6E3', '#5C6A72'),
  preset('github-light', 'GitHub', 'light', '#0969DA', '#FFFFFF', '#24292F'),
  preset('gruvbox-light', 'Gruvbox', 'light', '#D65D0E', '#FBF1C7', '#3C3836'),
  preset('linear-light', 'Linear', 'light', '#5E6AD2', '#FFFFFF', '#202124'),
  preset('notion-light', 'Notion', 'light', '#37352F', '#FFFFFF', '#37352F'),
  preset('one-light', 'One', 'light', '#4078F2', '#FAFAFA', '#383A42'),
  preset('proof-light', 'Proof', 'light', '#6C5CE7', '#FCFCFD', '#24242A'),
  preset('raycast-light', 'Raycast', 'light', '#E74646', '#FFFFFF', '#1A1A1A'),
  preset('rose-pine-dawn', 'Rose Pine', 'light', '#D7827E', '#FAF4ED', '#575279'),
  preset('solarized-light', 'Solarized', 'light', '#268BD2', '#FDF6E3', '#586E75'),
  preset('vercel-light', 'Vercel', 'light', '#000000', '#FFFFFF', '#111111'),
  preset('vscode-light-plus', 'VS Code Plus', 'light', '#0066B8', '#FFFFFF', '#333333'),
  preset('xcode-light', 'Xcode', 'light', '#0066CC', '#FFFFFF', '#292A30'),

  preset('absolutely-dark', 'Absolutely', 'dark', '#9A86FF', '#121116', '#F5F2FF'),
  preset('ayu-dark', 'Ayu', 'dark', '#E6B450', '#0B0E14', '#BFBDB6'),
  preset('catppuccin-mocha', 'Catppuccin', 'dark', '#CBA6F7', '#1E1E2E', '#CDD6F4'),
  preset('codex-dark', 'Codex', 'dark', '#339CFF', '#181818', '#FFFFFF'),
  preset('dracula', 'Dracula', 'dark', '#FF79C6', '#282A36', '#F8F8F2'),
  preset('everforest-dark', 'Everforest', 'dark', '#A7C080', '#2D353B', '#D3C6AA'),
  preset('github-dark', 'GitHub', 'dark', '#58A6FF', '#0D1117', '#C9D1D9'),
  preset('gruvbox-dark', 'Gruvbox', 'dark', '#FE8019', '#282828', '#EBDBB2'),
  preset('linear-dark', 'Linear', 'dark', '#8A8EF2', '#16171D', '#F2F3F5'),
  preset('lobster-dark', 'Lobster', 'dark', '#FF6B81', '#1C1014', '#FFECEF'),
  preset('material-darker', 'Material', 'dark', '#89DDFF', '#212121', '#EEFFFF'),
  preset('matrix-dark', 'Matrix', 'dark', '#00E676', '#050A07', '#B8FFD2'),
  preset('monokai', 'Monokai', 'dark', '#A6E22E', '#272822', '#F8F8F2'),
  preset('night-owl', 'Night Owl', 'dark', '#82AAFF', '#011627', '#D6DEEB'),
  preset('nord', 'Nord', 'dark', '#88C0D0', '#2E3440', '#ECEFF4'),
  preset('notion-dark', 'Notion', 'dark', '#E6E6E6', '#191919', '#F1F1F1'),
  preset('oscurange', 'Oscurange', 'dark', '#FF8C42', '#0F1115', '#EDEDED'),
  preset('one-dark', 'One', 'dark', '#61AFEF', '#282C34', '#ABB2BF'),
  preset('raycast-dark', 'Raycast', 'dark', '#FF6363', '#1A1A1A', '#F5F5F5'),
  preset('rose-pine-moon', 'Rose Pine', 'dark', '#C4A7E7', '#232136', '#E0DEF4'),
  preset('sentry-dark', 'Sentry', 'dark', '#A58CFF', '#181225', '#F0EAFF'),
  preset('solarized-dark', 'Solarized', 'dark', '#2AA198', '#002B36', '#EEE8D5'),
  preset('tokyo-night', 'Tokyo Night', 'dark', '#7AA2F7', '#1A1B26', '#C0CAF5'),
  preset('temple-dark', 'Temple', 'dark', '#E8B05A', '#1B1712', '#F3E8D2'),
  preset('vercel-dark', 'Vercel', 'dark', '#FFFFFF', '#000000', '#F5F5F5'),
  preset('vscode-dark-plus', 'VS Code Plus', 'dark', '#007ACC', '#1E1E1E', '#D4D4D4'),
  preset('xcode-dark', 'Xcode', 'dark', '#4AA5F0', '#1F1F24', '#DFDFE0'),
]

export const DEFAULT_THEME_SETTINGS: ThemeSettings = {
  version: 3,
  preference: 'system',
  light: { presetId: 'custom', accent: '#147A65', background: '#F6F5F0', foreground: '#19221E', contrast: 45 },
  dark: { presetId: 'custom', accent: '#4FBEA0', background: '#111412', foreground: '#EFF3F0', contrast: 58 },
  uiFontSize: 14,
}

function clamp(value: number, min: number, max: number) {
  return Math.min(max, Math.max(min, value))
}

function isThemePreference(value: unknown): value is ThemePreference {
  return value === 'system' || value === 'light' || value === 'dark'
}

function safeColor(value: unknown, fallback: string) {
  return typeof value === 'string' && HEX_COLOR.test(value) ? value.toUpperCase() : fallback
}

function safeScheme(value: unknown, fallback: ThemeScheme): ThemeScheme {
  const input = value && typeof value === 'object' ? value as Partial<ThemeScheme> : {}
  return {
    presetId: typeof input.presetId === 'string' ? input.presetId : fallback.presetId,
    accent: safeColor(input.accent, fallback.accent),
    background: safeColor(input.background, fallback.background),
    foreground: safeColor(input.foreground, fallback.foreground),
    contrast: clamp(typeof input.contrast === 'number' ? input.contrast : fallback.contrast, 20, 100),
  }
}

export function sanitizeThemeSettings(value: unknown): ThemeSettings {
  const input = value && typeof value === 'object' ? value as Partial<ThemeSettings> : {}
  return {
    version: 3,
    preference: isThemePreference(input.preference) ? input.preference : DEFAULT_THEME_SETTINGS.preference,
    light: safeScheme(input.light, DEFAULT_THEME_SETTINGS.light),
    dark: safeScheme(input.dark, DEFAULT_THEME_SETTINGS.dark),
    uiFontSize: clamp(typeof input.uiFontSize === 'number' ? input.uiFontSize : DEFAULT_THEME_SETTINGS.uiFontSize, 12, 18),
  }
}

export function getThemeSettings(): ThemeSettings {
  for (const key of [STORAGE_KEY, PREVIOUS_STORAGE_KEY]) {
    try {
      const stored = window.localStorage.getItem(key)
      if (stored) return sanitizeThemeSettings(JSON.parse(stored))
    } catch { /* Ignore invalid local preferences and keep looking. */ }
  }

  const legacyPreference = window.localStorage.getItem(LEGACY_STORAGE_KEY)
  return { ...DEFAULT_THEME_SETTINGS, preference: isThemePreference(legacyPreference) ? legacyPreference : 'system' }
}

export function resolveTheme(preference: ThemePreference): ThemeMode {
  if (preference !== 'system') return preference
  return window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

function hexToRgb(hex: string) {
  const value = Number.parseInt(hex.slice(1), 16)
  return { r: value >> 16, g: (value >> 8) & 255, b: value & 255 }
}

function rgbToHex(r: number, g: number, b: number) {
  return `#${[r, g, b].map((value) => Math.round(value).toString(16).padStart(2, '0')).join('')}`
}

function mix(first: string, second: string, secondAmount: number) {
  const a = hexToRgb(first), b = hexToRgb(second), ratio = clamp(secondAmount, 0, 100) / 100
  return rgbToHex(a.r + (b.r - a.r) * ratio, a.g + (b.g - a.g) * ratio, a.b + (b.b - a.b) * ratio)
}

function luminance(hex: string) {
  const { r, g, b } = hexToRgb(hex)
  const channels = [r, g, b].map((value) => {
    const normalized = value / 255
    return normalized <= .03928 ? normalized / 12.92 : ((normalized + .055) / 1.055) ** 2.4
  })
  return .2126 * channels[0] + .7152 * channels[1] + .0722 * channels[2]
}

export function colorContrast(first: string, second: string) {
  const light = Math.max(luminance(first), luminance(second))
  const dark = Math.min(luminance(first), luminance(second))
  return (light + .05) / (dark + .05)
}

function readableOn(color: string) {
  return colorContrast(color, '#FFFFFF') >= colorContrast(color, '#0B0F0D') ? '#FFFFFF' : '#0B0F0D'
}

export function applyTheme(settings: ThemeSettings) {
  const resolved = resolveTheme(settings.preference)
  const scheme = settings[resolved]
  const dark = resolved === 'dark'
  const surfaceStep = dark ? 4 + scheme.contrast * .055 : 2 + scheme.contrast * .025
  const lineStep = dark ? 9 + scheme.contrast * .11 : 10 + scheme.contrast * .075
  const sidebar = dark ? mix(scheme.background, '#000000', 32) : mix(scheme.accent, '#07110E', 66)
  const root = document.documentElement

  root.dataset.theme = resolved
  root.dataset.themePreference = settings.preference
  delete root.dataset.sidebarTranslucent
  delete root.dataset.pointerCursor
  root.style.colorScheme = resolved

  const variables: Record<string, string> = {
    '--theme-ink': scheme.foreground,
    '--theme-muted': mix(scheme.foreground, scheme.background, dark ? 38 : 43),
    '--theme-paper': scheme.background,
    '--theme-panel': mix(scheme.background, dark ? scheme.foreground : '#FFFFFF', surfaceStep),
    '--theme-line': mix(scheme.background, scheme.foreground, lineStep),
    '--theme-brand': scheme.accent,
    '--theme-brand-strong': mix(scheme.accent, dark ? '#FFFFFF' : '#000000', 18),
    '--theme-brand-soft': mix(scheme.background, scheme.accent, dark ? 20 : 13),
    '--theme-on-brand': readableOn(scheme.accent),
    '--theme-sidebar': sidebar,
    '--theme-sidebar-ink': readableOn(sidebar),
    '--theme-sidebar-muted': mix(readableOn(sidebar), sidebar, 34),
    '--theme-surface-soft': mix(scheme.background, scheme.foreground, dark ? surfaceStep + 3 : surfaceStep + 1.5),
    '--theme-surface-raised': mix(scheme.background, scheme.foreground, dark ? surfaceStep + 7 : surfaceStep + 4),
    '--theme-surface-hover': mix(scheme.background, scheme.foreground, dark ? surfaceStep + 5 : surfaceStep + 2.5),
    '--theme-control-border': mix(scheme.background, scheme.foreground, dark ? lineStep + 7 : lineStep + 8),
    '--theme-code-bg': mix(scheme.background, '#000000', dark ? 34 : 82),
    '--theme-code-ink': dark ? mix(scheme.foreground, scheme.accent, 10) : '#D8E8E1',
    '--theme-ui-size': `${settings.uiFontSize}px`,
  }
  Object.entries(variables).forEach(([property, value]) => root.style.setProperty(property, value))

  document.querySelector<HTMLMetaElement>('meta[name="theme-color"]')?.setAttribute('content', sidebar)
}

export function saveThemeSettings(settings: ThemeSettings) {
  const safeSettings = sanitizeThemeSettings(settings)
  window.localStorage.setItem(STORAGE_KEY, JSON.stringify(safeSettings))
  window.localStorage.setItem(LEGACY_STORAGE_KEY, safeSettings.preference)
  applyTheme(safeSettings)
}

export function schemeFromPreset(presetId: string, mode: ThemeMode) {
  const found = THEME_PRESETS.find((value) => value.id === presetId && value.mode === mode)
  return found ? { ...found.scheme } : null
}
