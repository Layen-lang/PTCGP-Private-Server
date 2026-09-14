import { useEffect, useId, useRef, useState } from 'react'
import type { CSSProperties, KeyboardEvent, ReactNode } from 'react'
import { Check, ChevronDown, Languages, Laptop, Moon, Palette, RotateCcw, Sun } from 'lucide-react'
import {
  colorContrast,
  DEFAULT_THEME_SETTINGS,
  resolveTheme,
  schemeFromPreset,
  THEME_PRESETS,
} from '../theme'
import type { ThemeMode, ThemePreference, ThemePreset, ThemeScheme, ThemeSettings } from '../theme'
import { LANGUAGES, setLocale, t, useLocale } from '../i18n'
import type { Locale } from '../i18n'

function themes(): Array<{ value: ThemePreference; label: string; icon: typeof Sun }> {
  return [
    { value: 'system', label: t('settings.system'), icon: Laptop },
    { value: 'light', label: t('settings.light'), icon: Sun },
    { value: 'dark', label: t('settings.dark'), icon: Moon },
  ]
}

function ThemePreview({ preference, settings }: { preference: ThemePreference; settings: ThemeSettings }) {
  const mode = preference === 'system' ? 'light' : preference
  const scheme = settings[mode]
  const style = {
    '--preview-bg': scheme.background,
    '--preview-panel': scheme.background,
    '--preview-sidebar': scheme.accent,
    '--preview-ink': scheme.foreground,
  } as CSSProperties
  return (
    <span className={`theme-preview theme-preview-${preference}`} style={style} aria-hidden="true">
      <span className="theme-preview-sidebar"><i /><i /><i /></span>
      <span className="theme-preview-content"><i /><b /><b /></span>
    </span>
  )
}

function PresetBadge({ scheme }: { scheme: ThemeScheme }) {
  const style = {
    '--preset-background': scheme.background,
    '--preset-accent': scheme.accent,
    '--preset-foreground': scheme.foreground,
  } as CSSProperties
  return <span className="preset-badge" style={style} aria-hidden="true"><b>A</b><i>a</i></span>
}

function LanguageSelector({ locale }: { locale: Locale }) {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const optionRefs = useRef<Array<HTMLButtonElement | null>>([])
  const listboxId = useId()
  const selectedIndex = Math.max(0, LANGUAGES.findIndex((language) => language.locale === locale))
  const selected = LANGUAGES[selectedIndex] ?? LANGUAGES[0]

  useEffect(() => {
    if (!open) return
    const closeOnOutsideClick = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('pointerdown', closeOnOutsideClick)
    return () => document.removeEventListener('pointerdown', closeOnOutsideClick)
  }, [open])

  const openAt = (index: number) => {
    setOpen(true)
    window.requestAnimationFrame(() => optionRefs.current[index]?.focus())
  }
  const closeAndFocus = () => {
    setOpen(false)
    window.requestAnimationFrame(() => triggerRef.current?.focus())
  }
  const choose = (nextLocale: Locale) => {
    setOpen(false)
    setLocale(nextLocale)
  }
  const handleTriggerKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
    event.preventDefault()
    openAt(event.key === 'ArrowUp' ? LANGUAGES.length - 1 : selectedIndex)
  }
  const handleOptionKeyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    let nextIndex: number | null = null
    if (event.key === 'ArrowDown') nextIndex = (index + 1) % LANGUAGES.length
    if (event.key === 'ArrowUp') nextIndex = (index - 1 + LANGUAGES.length) % LANGUAGES.length
    if (event.key === 'Home') nextIndex = 0
    if (event.key === 'End') nextIndex = LANGUAGES.length - 1
    if (event.key === 'Escape') {
      event.preventDefault()
      closeAndFocus()
      return
    }
    if (event.key === 'Tab') {
      setOpen(false)
      return
    }
    if (nextIndex !== null) {
      event.preventDefault()
      optionRefs.current[nextIndex]?.focus()
    }
  }

  const badge = (language: (typeof LANGUAGES)[number]) => (
    <span className="preset-badge language-badge" aria-hidden="true"><b>{language.htmlLang.slice(0, 2).toUpperCase()}</b></span>
  )
  const copy = (language: (typeof LANGUAGES)[number]) => (
    <span className="language-selector-copy"><strong>{language.label}</strong><small>{language.htmlLang}</small></span>
  )

  return (
    <div className="preset-selector language-selector" ref={rootRef}>
      <button
        ref={triggerRef}
        type="button"
        className="preset-trigger language-trigger"
        aria-label={t('settings.interfaceLanguage')}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={listboxId}
        onKeyDown={handleTriggerKeyDown}
        onClick={() => open ? setOpen(false) : openAt(selectedIndex)}
      >
        {badge(selected)}
        {copy(selected)}
        <ChevronDown className={open ? 'open' : ''} />
      </button>
      {open && (
        <div className="preset-menu language-menu" id={listboxId} role="listbox" aria-label={t('settings.interfaceLanguage')}>
          {LANGUAGES.map((language, index) => (
            <button
              ref={(element) => { optionRefs.current[index] = element }}
              type="button"
              role="option"
              aria-selected={language.locale === locale}
              className={language.locale === locale ? 'selected' : ''}
              key={language.locale}
              onKeyDown={(event) => handleOptionKeyDown(event, index)}
              onClick={() => choose(language.locale)}
            >
              {badge(language)}
              {copy(language)}
              {language.locale === locale && <Check />}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function PresetSelector({ mode, presets, scheme, onSelect }: {
  mode: ThemeMode
  presets: ThemePreset[]
  scheme: ThemeScheme
  onSelect: (presetId: string) => void
}) {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const optionRefs = useRef<Array<HTMLButtonElement | null>>([])
  const listboxId = useId()
  const selectedPreset = presets.find((preset) => preset.id === scheme.presetId)
  const selectedItem: ThemePreset = selectedPreset ?? { id: 'custom', label: t('settings.custom'), mode, scheme }
  const items = selectedPreset ? presets : [selectedItem, ...presets]
  const selectedIndex = Math.max(0, items.findIndex((preset) => preset.id === selectedItem.id))

  useEffect(() => {
    if (!open) return
    const closeOnOutsideClick = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false)
    }
    document.addEventListener('pointerdown', closeOnOutsideClick)
    return () => document.removeEventListener('pointerdown', closeOnOutsideClick)
  }, [open])

  const openAt = (index: number) => {
    setOpen(true)
    window.requestAnimationFrame(() => optionRefs.current[index]?.focus())
  }
  const closeAndFocus = () => {
    setOpen(false)
    window.requestAnimationFrame(() => triggerRef.current?.focus())
  }
  const choose = (preset: ThemePreset) => {
    if (preset.id !== 'custom') onSelect(preset.id)
    closeAndFocus()
  }
  const handleTriggerKeyDown = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
    event.preventDefault()
    openAt(event.key === 'ArrowUp' ? items.length - 1 : selectedIndex)
  }
  const handleOptionKeyDown = (event: KeyboardEvent<HTMLButtonElement>, index: number) => {
    let nextIndex: number | null = null
    if (event.key === 'ArrowDown') nextIndex = (index + 1) % items.length
    if (event.key === 'ArrowUp') nextIndex = (index - 1 + items.length) % items.length
    if (event.key === 'Home') nextIndex = 0
    if (event.key === 'End') nextIndex = items.length - 1
    if (event.key === 'Escape') {
      event.preventDefault()
      closeAndFocus()
      return
    }
    if (event.key === 'Tab') {
      setOpen(false)
      return
    }
    if (nextIndex !== null) {
      event.preventDefault()
      optionRefs.current[nextIndex]?.focus()
    }
  }

  return (
    <div className="preset-selector" ref={rootRef}>
      <button
        ref={triggerRef}
        type="button"
        className="preset-trigger"
        aria-label={t('settings.themeChoice', { mode: mode === 'dark' ? t('settings.dark') : t('settings.light'), name: selectedItem.label })}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-controls={listboxId}
        onKeyDown={handleTriggerKeyDown}
        onClick={() => open ? setOpen(false) : openAt(selectedIndex)}
      >
        <PresetBadge scheme={selectedItem.scheme} />
        <span>{selectedItem.label}</span>
        <ChevronDown className={open ? 'open' : ''} />
      </button>
      {open && (
        <div className="preset-menu" id={listboxId} role="listbox" aria-label={mode === 'dark' ? t('settings.darkThemes') : t('settings.lightThemes')}>
          {items.map((preset, index) => (
            <button
              ref={(element) => { optionRefs.current[index] = element }}
              type="button"
              role="option"
              aria-selected={preset.id === selectedItem.id}
              className={preset.id === selectedItem.id ? 'selected' : ''}
              key={preset.id}
              onKeyDown={(event) => handleOptionKeyDown(event, index)}
              onClick={() => choose(preset)}
            >
              <PresetBadge scheme={preset.scheme} />
              <span>{preset.label}</span>
              {preset.id === selectedItem.id && <Check />}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

function SettingRow({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="theme-setting-row">
      <span><strong>{label}</strong></span>
      <div>{children}</div>
    </div>
  )
}

function ColorControl({ label, value, onChange }: { label: string; value: string; onChange: (value: string) => void }) {
  const [draft, setDraft] = useState(value)
  useEffect(() => setDraft(value), [value])

  const commit = () => {
    if (/^#[0-9a-f]{6}$/i.test(draft)) onChange(draft.toUpperCase())
    else setDraft(value)
  }

  return (
    <div className="color-control">
      <input type="color" aria-label={t('settings.colorPicker', { label })} value={value} onChange={(event) => onChange(event.target.value.toUpperCase())} />
      <input
        className="color-hex"
        aria-label={t('settings.hexValue', { label })}
        value={draft}
        maxLength={7}
        spellCheck={false}
        onChange={(event) => setDraft(event.target.value)}
        onBlur={commit}
        onKeyDown={(event) => { if (event.key === 'Enter') event.currentTarget.blur() }}
      />
    </div>
  )
}

export default function SettingsPage({ settings, onSettingsChange }: { settings: ThemeSettings; onSettingsChange: (settings: ThemeSettings) => void }) {
  const locale = useLocale()
  const [systemMode, setSystemMode] = useState<ThemeMode>(() => resolveTheme('system'))
  const editingMode = settings.preference === 'system' ? systemMode : settings.preference
  const scheme = settings[editingMode]
  const presets = THEME_PRESETS.filter((preset) => preset.mode === editingMode)
  const foregroundContrast = colorContrast(scheme.foreground, scheme.background)
  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const followSystemMode = () => setSystemMode(media.matches ? 'dark' : 'light')
    media.addEventListener('change', followSystemMode)
    return () => media.removeEventListener('change', followSystemMode)
  }, [])

  const updateSettings = (patch: Partial<ThemeSettings>) => onSettingsChange({ ...settings, ...patch })
  const updateScheme = (patch: Partial<ThemeScheme>) => {
    onSettingsChange({ ...settings, [editingMode]: { ...scheme, ...patch, presetId: 'custom' } })
  }
  const selectPreference = (preference: ThemePreference) => updateSettings({ preference })
  const selectPreset = (presetId: string) => {
    const nextScheme = schemeFromPreset(presetId, editingMode)
    if (nextScheme) onSettingsChange({ ...settings, [editingMode]: nextScheme })
  }
  const reset = () => {
    onSettingsChange({
      ...DEFAULT_THEME_SETTINGS,
      light: { ...DEFAULT_THEME_SETTINGS.light },
      dark: { ...DEFAULT_THEME_SETTINGS.dark },
    })
  }

  return (
    <div className="page settings-page">
      <header className="page-heading settings-heading">
        <h1>{t('settings.title')}</h1>
      </header>

      <section className="settings-section" aria-labelledby="language-title">
        <div className="settings-section-heading">
          <span className="settings-section-icon"><Languages /></span>
          <h2 id="language-title">{t('settings.language')}</h2>
        </div>
        <fieldset className="theme-fieldset language-fieldset" aria-label={t('settings.interfaceLanguage')}>
          <LanguageSelector locale={locale} />
        </fieldset>
      </section>

      <section className="settings-section" aria-labelledby="appearance-title">
        <div className="settings-section-heading">
          <span className="settings-section-icon"><Palette /></span>
          <h2 id="appearance-title">{t('settings.appearance')}</h2>
        </div>

        <fieldset className="theme-fieldset" aria-label={t('settings.interfaceTheme')}>
          <div className="theme-options">
            {themes().map(({ value, label, icon: Icon }) => (
              <label className={`theme-option ${settings.preference === value ? 'selected' : ''}`} key={value}>
                <input type="radio" name="interface-theme" value={value} checked={settings.preference === value} onChange={() => selectPreference(value)} />
                <ThemePreview preference={value} settings={settings} />
                <span className="theme-option-copy"><strong><Icon />{label}</strong></span>
                <span className="theme-option-check" aria-hidden="true"><Check /></span>
              </label>
            ))}
          </div>
        </fieldset>

        <div className="theme-customizer">
          <header className="theme-customizer-head">
            <strong className="mode-editor">{editingMode === 'light' ? t('settings.lightMode') : t('settings.darkMode')}</strong>
            <div className="theme-preset-control">
              <span>{t('settings.theme')}</span>
              <PresetSelector mode={editingMode} presets={presets} scheme={scheme} onSelect={selectPreset} />
            </div>
          </header>

          <details className="theme-advanced">
            <summary><Palette />{t('settings.customizeColors')}<ChevronDown /></summary>
            <div className="theme-setting-list">
              <SettingRow label={t('settings.accent')}><ColorControl label={t('settings.accent')} value={scheme.accent} onChange={(accent) => updateScheme({ accent })} /></SettingRow>
              <SettingRow label={t('settings.background')}><ColorControl label={t('settings.background')} value={scheme.background} onChange={(background) => updateScheme({ background })} /></SettingRow>
              <SettingRow label={t('settings.text')}><ColorControl label={t('settings.text')} value={scheme.foreground} onChange={(foreground) => updateScheme({ foreground })} /></SettingRow>
            </div>
            <div className={`contrast-result ${foregroundContrast < 4.5 ? 'warning' : ''}`}>
              <span>{t('settings.contrast')}</span><strong>{foregroundContrast.toFixed(1)}:1</strong><small>{foregroundContrast >= 4.5 ? t('settings.compliant') : t('settings.insufficient')}</small>
            </div>
          </details>

          <div className="theme-compact-settings">
            <SettingRow label={t('settings.interfaceSize')}>
              <div className="range-control compact"><input type="range" min="12" max="18" value={settings.uiFontSize} aria-label={t('settings.interfaceSize')} onChange={(event) => updateSettings({ uiFontSize: Number(event.target.value) })} /><output>{settings.uiFontSize}px</output></div>
            </SettingRow>
          </div>

          <footer className="theme-customizer-footer">
            <span />
            <button type="button" onClick={reset}><RotateCcw />{t('settings.resetAll')}</button>
          </footer>
        </div>
      </section>
    </div>
  )
}
