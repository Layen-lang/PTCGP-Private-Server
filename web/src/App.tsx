import { useCallback, useEffect, useRef, useState } from 'react'
import { NavLink, Navigate, Route, Routes } from 'react-router-dom'
import { Activity, Boxes, CircleDot, FileText, Globe2, Power, RefreshCw, Server, Settings, Smartphone } from 'lucide-react'
import { loadBootstrap, loadControlStatus, loadDevices, prepareInstallation, selectDevice, runControlAction, installUpdate } from './api'
import type { AndroidDevice, Bootstrap, ControlStatus } from './types'
import { applyTheme, getThemeSettings, saveThemeSettings } from './theme'
import type { ThemeSettings } from './theme'
import { t, useLocale } from './i18n'
import AppLogo from './components/AppLogo'
import UpdateControl from './components/UpdateControl'
import AccountsPage from './pages/AccountsPage'
import PacksPage from './pages/PacksPage'
import TrafficPage from './pages/TrafficPage'
import LogsPage from './pages/LogsPage'
import SettingsPage from './pages/SettingsPage'
import PreparationPage from './pages/PreparationPage'

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
  if (action === 'device') return { title: t('app.switchingDevice') }
  if (action === 'prepare') return {title: t('prepare.operation')}
  if (action === 'update') return {title: t('prepare.updateOperation')}
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
        <button className={status.mode === 'local' ? 'active' : ''} aria-pressed={status.mode === 'local'} disabled={busy || !status.preparation?.ready || !status.android.connected} onClick={() => onAction('local')}>
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
  const [devices, setDevices] = useState<AndroidDevice[]>([])
  const [control, setControl] = useState<ControlStatus>({ csrfToken: '', busy: false, mode: 'unknown', server: { running: false }, android: { connected: false, root: false, routing: 'indisponible', ca: 'inconnue', native: 'inconnue', game: 'inconnu', running: false } })
  const [statusLoaded, setStatusLoaded] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const [pending, setPending] = useState<ControlStatus['operation'] | null>(null)
  const [error, setError] = useState('')
  const [connectionError, setConnectionError] = useState('')
  const [deviceError, setDeviceError] = useState('')
  const [refreshing, setRefreshing] = useState(false)
  const [themeSettings, setThemeSettings] = useState<ThemeSettings>(() => getThemeSettings())
  const actionLock = useRef(false)
  const restartLock = useRef(false)
  const refreshLock = useRef(false)
  const deviceRequest = useRef(0)
  const restartTimer = useRef<number | undefined>(undefined)

  useEffect(() => () => window.clearTimeout(restartTimer.current), [])

  const discoverDevices = useCallback(async () => {
    const request = ++deviceRequest.current
    try {
      const found = await loadDevices()
      if (request === deviceRequest.current) { setDevices(found); setDeviceError('') }
      return found
    } catch (value) {
      if (request === deviceRequest.current) setDeviceError(t('app.deviceDiscoveryFailed', { message: value instanceof Error ? value.message : t('app.refreshFailed') }))
      throw value
    }
  }, [])

  const refresh = useCallback(async () => {
    if (refreshLock.current) return
    refreshLock.current = true
    setRefreshing(true)
    setError('')
    try {
      const [result] = await Promise.allSettled([loadControlStatus(), discoverDevices()])
      if (result.status === 'rejected') throw result.reason
      const status = result.value
      setControl(status); setConnectionError(status.android.recoveryError || '')
      if (status.preparation?.ready) setData(await loadBootstrap()); else setData(null)
    } catch (value) {
      setError(value instanceof Error ? value.message : t('app.refreshFailed'))
    } finally {
      refreshLock.current = false
      setRefreshing(false)
      setStatusLoaded(true)
    }
  }, [discoverDevices])

  const startPreparation = async (serial: string) => {
    if (actionLock.current || restartLock.current || control.busy || !control.csrfToken) return
    actionLock.current = true
    setPending('prepare')
    setError('')
    try {
      const found = await discoverDevices()
      const available = found.filter((device) => device.state === 'device')
      const selected = available.find((device) => device.serial === serial)
      if (available.length > 1 && !selected) throw new Error(t('prepare.chooseDevice'))
      if (serial && !selected) throw new Error(t('app.deviceUnavailable'))
      await prepareInstallation(selected ? selected.serial : '')
      await refresh()
    } catch (value) {
      setError(value instanceof Error ? value.message : t('app.deviceChangeFailed'))
    } finally {
      actionLock.current = false
      setPending(null)
    }
  }

  const changeDevice = async (serial: string) => {
    if (actionLock.current || restartLock.current || control.busy || !control.csrfToken || serial === control.android.serial) return
    actionLock.current = true
    setPending('device')
    setError('')
    try {
      const status = await selectDevice(serial)
      const bootstrap = status.preparation?.ready ? await loadBootstrap() : null
      setControl(status)
      setData(bootstrap)
      setConnectionError(status.android.recoveryError || '')
    } catch (value) {
      setError(value instanceof Error ? value.message : t('app.deviceChangeFailed'))
    } finally {
      actionLock.current = false
      setPending(null)
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
      if (!status.busy && status.preparation?.ready) {
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

  useEffect(() => { void refresh() }, [locale, refresh])

  useEffect(() => {
    let cancelled = false
    let polling = false
    const timer = window.setInterval(async () => {
      if (polling || actionLock.current || restartLock.current) return
      polling = true
      try {
        if (!cancelled) await discoverDevices()
      } catch { /* Discovery already reports errors and preserves the last list. */ }
      finally {
        polling = false
      }
    }, 10_000)
    return () => { cancelled = true; window.clearInterval(timer) }
  }, [discoverDevices])

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
      if (polling || restartLock.current || actionLock.current) return
      polling = true
      try {
        const status = await loadControlStatus()
        if (cancelled || restartLock.current || actionLock.current) return
        const bootstrap = !status.busy && status.preparation?.ready ? await loadBootstrap() : null
        if (cancelled || restartLock.current || actionLock.current) return
        setConnectionError(status.android.recoveryError || '')
        setControl(status)
        if (bootstrap) {
          setPending(null)
          setData(bootstrap)
        }
      } catch (value) {
        if (!cancelled && !restartLock.current) setConnectionError(value instanceof Error ? value.message : t('app.connectionInterrupted'))
      } finally {
        polling = false
      }
    }
    const timer = window.setInterval(() => { void poll() }, control.update?.phase === 'downloading' ? 1000 : control.preparation?.phase === 'images' ? 500 : control.preparation?.ready ? 10_000 : 2_000)
    return () => {
      cancelled = true
      window.clearInterval(timer)
    }
  }, [control.busy, control.update?.phase, control.preparation?.phase, control.preparation?.ready, pending, data])

  const applyUpdate = async () => {
    if (restartLock.current || actionLock.current || control.busy || control.update?.phase !== 'ready') return
    restartLock.current = true
    setRestarting(true)
    setError('')
    setConnectionError('')
    const targetVersion = control.update.version
    const fail = (message: string) => {
      restartLock.current = false
      setRestarting(false)
      setError(message)
    }
    try {
      const { previousPID } = await installUpdate()
      const deadline = Date.now() + 120_000
      const wait = async () => {
        try {
          const response = await fetch('/api/control/health', { cache: 'no-store', signal: AbortSignal.timeout(3000) })
          if (response.ok) {
            const health = await response.json() as { healthy: boolean; pid: number; version?: string; updateError?: string }
            if (health.healthy && health.pid !== previousPID) {
              if (health.updateError) {
                fail(t('prepare.updateRolledBack', { message: health.updateError }))
                void loadControlStatus().then(setControl).catch(() => {})
                return
              }
              if (health.version === targetVersion) {
                window.location.reload()
                return
              }
            }
          }
        } catch { /* The detached installer restarts the panel independently. */ }
        if (Date.now() < deadline) restartTimer.current = window.setTimeout(() => void wait(), 1000)
        else fail(t('prepare.restartFailure'))
      }
      restartTimer.current = window.setTimeout(() => void wait(), 1000)
    } catch (value) {
      fail(value instanceof Error ? value.message : t('prepare.restartFailure'))
    }
  }
  const preparation = control.preparation || {phase:'checking',message:t('prepare.connecting'),ready:false,busy:true,completed:0,total:0}
  const active = data?.players.find((value) => value.Active)
  const effectivePending = restarting ? 'update' : pending ?? (control.busy ? control.operation ?? null : null)
  const controlsBusy = restarting || pending !== null || control.busy || !control.csrfToken
  const availableDevices = devices.filter((device) => device.state === 'device')
  const selectedSerial = control.android.serial || control.preparation?.serial || ''
  const deviceChoice = availableDevices.find((device) => device.serial === selectedSerial || device.aliases?.includes(selectedSerial))?.serial || ''
  const preparationPage = <PreparationPage state={preparation} pending={pending === 'prepare'} disabled={controlsBusy} onStart={() => void startPreparation(deviceChoice)} />
  const statusUnavailable = <div className="page" role="status"><h1>{t('app.statusUnavailable')}</h1><button className="secondary-button" onClick={() => void refresh()}>{t('app.refresh')}</button></div>
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
          <NavLink className="settings-nav-item" to="/settings"><Settings /><span>{t('app.settings')}</span></NavLink>
        </nav>
        <div className="sidebar-footer"><UpdateControl state={control.update} restarting={restarting} disabled={controlsBusy} onInstall={() => void applyUpdate()} /></div>
      </aside>
      <main className="main-area">
        <header className="topbar control-topbar">
          <div className="topbar-context">
            {active && <strong className="topbar-profile">{t('app.activeProfile', { name: active.DisplayName })}</strong>}
            <div className="topbar-compact-status"><span className={`mode-dot ${control.mode}`} /><strong>{controlsBusy ? operationCopy(effectivePending).title : modeCopy(control).title}</strong></div>
          </div>
          <ModeControls status={control} operation={effectivePending} busy={controlsBusy} onAction={(action) => void changeMode(action)} />
          <div className="topbar-actions">
            <label className="device-selector">
              <Smartphone size={16} aria-hidden="true" />
              <select aria-label={t('app.chooseDevice')} title={deviceChoice ? `${availableDevices.find((device) => device.serial === deviceChoice)?.name} · ${deviceChoice}` : t('app.chooseDevice')} value={deviceChoice} disabled={controlsBusy || refreshing || availableDevices.length === 0} onChange={(event) => void changeDevice(event.target.value)}>
                <option value="" disabled>{t(availableDevices.length === 0 ? 'app.noDevices' : 'app.chooseDevice')}</option>
                {availableDevices.map((device) => <option key={device.serial} value={device.serial}>{device.name} · {device.serial}</option>)}
              </select>
            </label>
            <button className="quiet-button refresh-button" disabled={controlsBusy || refreshing} onClick={() => void refresh()} aria-label={t('app.refreshState')} aria-busy={refreshing}><RefreshCw size={16} className={refreshing ? 'update-spinning' : undefined} /> <span>{t(refreshing ? 'app.refreshing' : 'app.refresh')}</span></button>
          </div>
        </header>
        {(error || connectionError || deviceError) && <div className="global-error" role="alert"><span>{error || connectionError || deviceError}</span><button onClick={() => { setError(''); setConnectionError(''); setDeviceError('') }}>{t('common.close')}</button></div>}
          <Routes key={locale}>
            <Route path="/accounts" element={!statusLoaded ? <div className="page-loading" role="status">{t('common.loading')}</div> : !control.csrfToken ? statusUnavailable : !preparation.ready ? preparationPage : data ? <AccountsPage key={data.emulator} bootstrap={data} devices={availableDevices} refresh={refresh} reportError={setError} canLaunch={control.mode === 'local' && control.server.running && control.android.connected && !controlsBusy} /> : <div className="page"><h1>{t('app.accounts')}</h1><p role="status">{error || t('app.loadAccounts')}</p><button className="secondary-button" onClick={() => void refresh()}>{t('app.refresh')}</button></div>} />
            <Route path="/packs" element={!statusLoaded ? <div className="page-loading" role="status">{t('common.loading')}</div> : !control.csrfToken ? statusUnavailable : preparation.ready ? <PacksPage reportError={setError} /> : preparationPage} />
            <Route path="/traffic" element={<TrafficPage key={control.android.serial} serverRunning={control.server.running} />} />
            <Route path="/logs" element={<LogsPage />} />
            <Route path="/settings" element={<SettingsPage settings={themeSettings} onSettingsChange={changeThemeSettings} />} />
            <Route path="*" element={<Navigate to="/accounts" replace />} />
          </Routes>
      </main>
      <div className="mobile-update"><UpdateControl state={control.update} restarting={restarting} disabled={controlsBusy} onInstall={() => void applyUpdate()} /></div>
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
