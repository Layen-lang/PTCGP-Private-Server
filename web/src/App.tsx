import { useEffect, useRef, useState } from 'react'
import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import { Activity, Boxes, CircleDot, FileText, Globe2, Power, RefreshCw, Server, Settings } from 'lucide-react'
import { loadBootstrap, loadControlStatus, runControlAction } from './api'
import type { Bootstrap, ControlStatus } from './types'
import { applyTheme, getThemeSettings, saveThemeSettings } from './theme'
import type { ThemeSettings } from './theme'
import { t, useLocale } from './i18n'
import AppLogo from './components/AppLogo'
import AccountsPage from './pages/AccountsPage'
import PacksPage from './pages/PacksPage'
import TrafficPage from './pages/TrafficPage'
import LogsPage from './pages/LogsPage'
import SettingsPage from './pages/SettingsPage'

type ControlAction = 'local' | 'online' | 'stop'

function closeControlPanel() {
  window.close()
  window.setTimeout(() => window.location.replace('about:blank'), 200)
}

function modeCopy(status: ControlStatus) {
  if (status.mode === 'local' && status.server.running) return { title: t('app.privateActive') }
  if (status.mode === 'local') return { title: t('app.localIncomplete') }
  if (status.mode === 'online') return { title: t('app.officialMode') }
  if (status.mode === 'stopped') return { title: t('app.serverStopped') }
  return { title: status.server.running ? t('app.serverDeviceMissing') : t('app.emulatorDisconnected') }
}

function operationCopy(action: ControlStatus['operation'] | null) {
  if (action === 'local') return { title: t('app.activatingPrivate') }
  if (action === 'online') return { title: t('app.switchingOfficial') }
  if (action === 'stop') return { title: t('app.stopping') }
  if (action === 'open') return { title: t('app.openingAccount') }
  return { title: t('app.operation') }
}

function ModeControls({ status, operation, busy, onAction }: { status: ControlStatus; operation: ControlStatus['operation'] | null; busy: boolean; onAction: (action: ControlAction) => void }) {
  const copy = busy ? operationCopy(operation) : modeCopy(status)
  return (
    <div className="mode-controls" aria-live="polite" aria-busy={busy}>
      <div className="mode-state">
        <span className={`mode-dot ${status.mode}`} />
        <strong>{copy.title}</strong>
      </div>
      <div className="mode-switch" role="group" aria-label={t('app.connectionMode')}>
        <button className={status.mode === 'local' ? 'active' : ''} aria-pressed={status.mode === 'local'} disabled={busy} onClick={() => onAction('local')}>
          <Server /> <span>{t('app.local')}</span>
        </button>
        <button className={status.mode === 'online' ? 'active' : ''} aria-pressed={status.mode === 'online'} disabled={busy} onClick={() => onAction('online')}>
          <Globe2 /> <span>{t('app.official')}</span>
        </button>
      </div>
      <button className="stop-button" disabled={busy} onClick={() => onAction('stop')}>
        <Power /> <span>{t('app.stopAll')}</span>
      </button>
    </div>
  )
}

export default function App() {
  const locale = useLocale()
  const [data, setData] = useState<Bootstrap | null>(null)
  const [control, setControl] = useState<ControlStatus>({ csrfToken: '', busy: false, mode: 'unknown', server: { running: false }, android: { connected: false, root: false, routing: 'indisponible', ca: 'inconnue', native: 'inconnue', game: 'inconnu', running: false } })
  const [pending, setPending] = useState<ControlAction | null>(null)
  const [error, setError] = useState('')
  const [connectionError, setConnectionError] = useState('')
  const [themeSettings, setThemeSettings] = useState<ThemeSettings>(() => getThemeSettings())
  const actionLock = useRef(false)

  const refresh = async () => {
    setError('')
    try {
      const results = await Promise.allSettled([
        loadControlStatus().then(setControl), loadBootstrap().then(setData),
      ])
      const failed = results.find((result) => result.status === 'rejected')
      if (failed?.status === 'rejected') throw failed.reason
    } catch (value) {
      setError(value instanceof Error ? value.message : t('app.refreshFailed'))
    }
  }

  const changeMode = async (action: ControlAction) => {
    if (actionLock.current || control?.busy) return
    actionLock.current = true
    setPending(action)
    setError('')
    try {
      const status = await runControlAction(action)
      setControl(status)
      if (action === 'stop') {
        closeControlPanel()
        return
      }
      if (!status.busy) {
        setData(await loadBootstrap())
      }
    } catch (value) {
      const message = value instanceof Error ? value.message : t('app.modeChangeFailed')
      try {
        const status = await loadControlStatus()
        setControl(status)
        if (status.busy) return
        setData(await loadBootstrap())
      } catch { /* Keep the last known state. */ }
      setError(message)
    } finally {
      actionLock.current = false
      setPending(null)
    }
  }

  useEffect(() => { void refresh() }, [locale])

  useEffect(() => {
    applyTheme(themeSettings)
    if (themeSettings.preference !== 'system') return

    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const followSystemTheme = () => applyTheme(themeSettings)
    media.addEventListener('change', followSystemTheme)
    return () => media.removeEventListener('change', followSystemTheme)
  }, [themeSettings])

  useEffect(() => {
    let cancelled = false
    let polling = false
    const poll = async () => {
      if (polling) return
      polling = true
      try {
        const status = await loadControlStatus()
        if (cancelled) return
        setConnectionError('')
        setControl(status)
        if (!status.busy && (control.busy || pending)) {
          setPending(null)
          setData(await loadBootstrap())
        }
      } catch (value) {
        if (!cancelled) setConnectionError(value instanceof Error ? value.message : t('app.connectionInterrupted'))
      } finally {
        polling = false
      }
    }
    const timer = window.setInterval(() => { void poll() }, 10_000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [control.busy, pending])

  const active = data?.players.find((value) => value.Active)
  const effectivePending = pending ?? (control.busy ? control.operation ?? null : null)
  const controlsBusy = pending !== null || control.busy || !control.csrfToken
  const changeThemeSettings = (nextSettings: ThemeSettings) => {
    saveThemeSettings(nextSettings)
    setThemeSettings(nextSettings)
  }
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark"><AppLogo /></div>
          <div><strong>PTCGP Private Server</strong></div>
        </div>
        <nav className="main-nav" aria-label="Navigation">
          <NavLink to="/accounts"><Boxes /><span>{t('app.accounts')}</span></NavLink>
          <NavLink to="/packs"><CircleDot /><span>{t('app.packStudio')}</span></NavLink>
          <NavLink to="/traffic"><Activity /><span>{t('app.traffic')}</span></NavLink>
          <NavLink to="/logs"><FileText /><span>{t('app.logs')}</span></NavLink>
        </nav>
        <div className="sidebar-footer">
          <div className="sidebar-status">
            <span className={`status-dot ${control.android.connected ? 'online' : ''}`} />
            <strong>{control.android.connected ? t('app.emulatorConnected') : t('app.waitingDevice')}</strong>
          </div>
          <NavLink className="sidebar-settings" to="/settings" aria-label={t('app.settings')} title={t('app.settings')}><Settings /></NavLink>
        </div>
      </aside>
      <main className="main-area">
        <header className="topbar control-topbar">
          <div className="topbar-context"><strong>{active ? t('app.activeProfile', { name: active.DisplayName }) : modeCopy(control).title}</strong></div>
          <ModeControls status={control} operation={effectivePending} busy={controlsBusy} onAction={(action) => void changeMode(action)} />
          <button className="quiet-button refresh-button" disabled={pending !== null || control.busy} onClick={() => void refresh()} aria-label={t('app.refreshState')}><RefreshCw size={16} /> <span>{t('app.refresh')}</span></button>
        </header>
        {(error || connectionError) && <div className="global-error" role="alert"><span>{error || connectionError}</span><button onClick={() => { setError(''); setConnectionError('') }}>{t('common.close')}</button></div>}
          <Routes key={locale}>
            <Route path="/accounts" element={data ? <AccountsPage bootstrap={data} refresh={refresh} reportError={setError} canLaunch={control.mode === 'local' && control.server.running && control.android.connected && !controlsBusy} /> : <div className="page"><h1>{t('app.accounts')}</h1><p role="status">{error || t('app.loadAccounts')}</p><button className="secondary-button" onClick={() => void refresh()}>{t('app.refresh')}</button></div>} />
            <Route path="/packs" element={<PacksPage reportError={setError} />} />
            <Route path="/traffic" element={<TrafficPage serverRunning={control.server.running} />} />
            <Route path="/logs" element={<LogsPage />} />
            <Route path="/settings" element={<SettingsPage settings={themeSettings} onSettingsChange={changeThemeSettings} />} />
            <Route path="*" element={<Navigate to="/accounts" replace />} />
          </Routes>
      </main>
      <div className="mobile-nav">
        <NavLink to="/accounts"><Boxes /><span>{t('app.accounts')}</span></NavLink>
        <NavLink to="/packs"><CircleDot /><span>{t('app.packsMobile')}</span></NavLink>
        <NavLink to="/traffic"><Activity /><span>{t('app.traffic')}</span></NavLink>
        <NavLink to="/logs"><FileText /><span>{t('app.logs')}</span></NavLink>
        <NavLink to="/settings"><Settings /><span>{t('app.settings')}</span></NavLink>
      </div>
    </div>
  )
}
