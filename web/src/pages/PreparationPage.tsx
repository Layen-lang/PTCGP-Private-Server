import { useEffect, useState } from 'react'
import { Download } from 'lucide-react'
import { loadDevices, prepareInstallation } from '../api'
import type { PreparationState } from '../types'
import { t, formatNumber } from '../i18n'
import type { TranslationKey } from '../i18n'

export default function PreparationPage({ state, refresh }: { state: PreparationState; refresh: () => Promise<void> }) {
  const [devices, setDevices] = useState<{ serial: string; name: string; state: string }[]>([])
  const [serial, setSerial] = useState('')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const discover = async () => {
    try { setDevices(await loadDevices()); setError('') }
    catch (value) { setError((value as Error).message) }
  }
  useEffect(() => { void discover() }, [])
  const start = async () => {
    setPending(true); setError('')
    try {
      const found = await loadDevices()
      setDevices(found)
      const available = found.filter((device) => device.state === 'device')
      if (available.length > 1 && !available.some((device) => device.serial === serial)) {
        setError(t('prepare.chooseDevice'))
        return
      }
      await prepareInstallation(available.some((device) => device.serial === serial) ? serial : '')
      await refresh()
    }
    catch (value) {
      setError((value as Error).message)
    }
    finally { setPending(false) }
  }
  const availableDevices = devices.filter((device) => device.state === 'device')
  const showDeviceChoice = availableDevices.length > 1
  const busy = pending || state.busy
  const imageCount = state.phase === 'images' && state.total > 0
    ? t('prepare.imageCount', { done: formatNumber(state.completed), total: formatNumber(state.total) })
    : ''
  const phase = ['checking', 'master', 'images', 'validating', 'ready', 'blocked'].includes(state.phase)
    ? t(('prepare.' + state.phase) as TranslationKey)
    : state.message || t('prepare.connecting')
  const activity = pending ? t('prepare.starting') : imageCount ? `${phase} · ${imageCount}` : phase
  const progress = pending ? null : state.phase === 'images' && state.total > 0
    ? Math.min(100, Math.max(0, state.completed / state.total * 100))
    : state.phase === 'ready' ? 100
      : state.phase === 'blocked' ? (state.total > 0 ? Math.min(100, Math.max(0, state.completed / state.total * 100)) : 0)
        : null
  return <section className="page preparation-page" aria-labelledby="preparation-title">
    <div className="preparation-heading"><Download size={28} aria-hidden="true" /><h1 id="preparation-title">{t('prepare.title')}</h1></div>
    <p className="preparation-intro">{t('prepare.intro')}</p>
    <p className="preparation-requirements">{t('prepare.requirements')}</p>
    {showDeviceChoice && <div className="preparation-device">
      <label htmlFor="preparation-device">{t('prepare.device')}</label>
      <select id="preparation-device" value={serial} disabled={busy} onChange={(event) => setSerial(event.target.value)}>
        <option value="">{t('prepare.auto')}</option>
        {availableDevices.map((device) => <option key={device.serial} value={device.serial}>{device.name} · {device.serial}</option>)}
      </select>
    </div>}
    <div className="preparation-progress">
      <div className={`preparation-progress-track${progress === null ? ' is-indeterminate' : ''}${state.phase === 'blocked' ? ' is-blocked' : ''}`} role="progressbar" aria-label={t('prepare.operation')} aria-valuemin={0} aria-valuemax={100} aria-valuenow={progress === null ? undefined : Math.round(progress)} aria-valuetext={activity}>
        {progress !== null && <span className="preparation-progress-fill" style={{ width: `${progress}%` }} />}
      </div>
      <p className="preparation-activity" role="status" aria-live="polite">{activity}</p>
    </div>
    {!pending && (state.error || error) && <div className="preparation-error" role="alert">{error || state.error}</div>}
    {!state.busy && <button className="primary-button" disabled={pending} onClick={() => void start()}>{pending ? t('prepare.starting') : state.phase === 'blocked' ? t('prepare.retry') : t('prepare.start')}</button>}
  </section>
}
