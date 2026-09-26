import { useEffect, useState } from 'react'
import { Check, Download, Monitor, RefreshCw } from 'lucide-react'
import { loadDevices, prepareInstallation } from '../api'
import type { PreparationState } from '../types'
import { t, formatNumber } from '../i18n'
import type { TranslationKey } from '../i18n'

export default function PreparationPage({ state, refresh }: { state: PreparationState; refresh: () => Promise<void> }) {
  const [devices, setDevices] = useState<{ serial: string; name: string; state: string }[]>([])
  const [serial, setSerial] = useState(state.serial || '')
  const [error, setError] = useState('')
  const [pending, setPending] = useState(false)
  const discover = async () => {
    try { setDevices(await loadDevices()); setError('') }
    catch (value) { setError((value as Error).message) }
  }
  useEffect(() => { void discover() }, [])
  const start = async () => {
    setPending(true); setError('')
    try { await prepareInstallation(serial); await refresh() }
    catch (value) { setError((value as Error).message) }
    finally { setPending(false) }
  }
  const steps = [
    { id: 'checking', label: t('prepare.checking') },
    { id: 'master', label: t('prepare.master') },
    { id: 'images', label: t('prepare.images') },
    { id: 'validating', label: t('prepare.validating') },
  ]
  const current = steps.findIndex((step) => step.id === state.phase)
  const busy = pending || state.busy
  return <section className="page preparation-page" aria-labelledby="preparation-title">
    <div className="preparation-heading"><Download size={28} aria-hidden="true" /><h1 id="preparation-title">{t('prepare.title')}</h1></div>
    <p className="preparation-intro">{t('prepare.intro')}</p>
    <div className="preparation-device">
      <label htmlFor="preparation-device"><Monitor size={18} aria-hidden="true" /> {t('prepare.device')}</label>
      <div className="preparation-device-controls">
        <select id="preparation-device" value={serial} disabled={busy} onChange={(event) => setSerial(event.target.value)}>
          <option value="">{t('prepare.auto')}</option>
          {devices.map((device) => <option key={device.serial} value={device.serial} disabled={device.state !== 'device'}>{device.name} · {device.serial}{device.state !== 'device' ? ` (${device.state})` : ''}</option>)}
        </select>
        <button className="secondary-button" disabled={busy} onClick={() => void discover()} aria-label={t('prepare.discover')}><RefreshCw size={16} /></button>
      </div>
      <p>{t('prepare.requirements')}</p>
    </div>
    <ol className="preparation-steps">
      {steps.map((step, index) => <li key={step.id} className={index < current ? 'complete' : index === current ? 'current' : ''} aria-current={index === current ? 'step' : undefined}>
        <span className="preparation-step-marker">{index < current ? <Check size={16} aria-label={t('prepare.done')} /> : index + 1}</span><span>{step.label}</span>
      </li>)}
    </ol>
    <div className="preparation-progress" role="status" aria-live="polite">
      <strong>{['checking','master','images','validating','ready','blocked'].includes(state.phase) ? t(('prepare.'+state.phase) as TranslationKey) : state.message}</strong>
      {state.phase === 'images' && state.total > 0 && <><progress max={state.total} value={state.completed} aria-label={t('prepare.imageProgress')} /><span>{t('prepare.imageCount', {done:formatNumber(state.completed),total:formatNumber(state.total)})}</span></>}
    </div>
    {(state.error || error) && <div className="preparation-error" role="alert">{error || state.error}</div>}
    {!state.busy && <button className="primary-button" disabled={pending} onClick={() => void start()}>{pending ? t('prepare.starting') : state.phase === 'blocked' ? t('prepare.retry') : t('prepare.start')}</button>}
    <p className="preparation-note">{t('prepare.note')}</p>
  </section>
}
