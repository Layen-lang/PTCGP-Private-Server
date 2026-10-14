import { Download } from 'lucide-react'
import type { PreparationState } from '../types'
import { t, formatNumber } from '../i18n'
import type { TranslationKey } from '../i18n'

export default function PreparationPage({ state, pending, disabled, onStart }: { state: PreparationState; pending: boolean; disabled: boolean; onStart: () => void }) {
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
    <div className="preparation-progress">
      <div className={`preparation-progress-track${progress === null ? ' is-indeterminate' : ''}${state.phase === 'blocked' ? ' is-blocked' : ''}`} role="progressbar" aria-label={t('prepare.operation')} aria-valuemin={0} aria-valuemax={100} aria-valuenow={progress === null ? undefined : Math.round(progress)} aria-valuetext={activity}>
        {progress !== null && <span className="preparation-progress-fill" style={{ width: `${progress}%` }} />}
      </div>
      <p className="preparation-activity" role="status" aria-live="polite">{activity}</p>
    </div>
    {!pending && state.error && <div className="preparation-error" role="alert">{state.error}</div>}
    {!state.busy && <button className="primary-button" disabled={disabled} onClick={onStart}>{pending ? t('prepare.starting') : state.phase === 'blocked' ? t('prepare.retry') : t('prepare.start')}</button>}
  </section>
}
