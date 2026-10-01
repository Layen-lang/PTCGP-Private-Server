import { AlertCircle, ArrowDownToLine, LoaderCircle, RotateCw } from 'lucide-react'
import type { UpdateState } from '../types'
import { t } from '../i18n'

export default function UpdateControl({ state, restarting, disabled, onInstall }: {
  state?: UpdateState
  restarting: boolean
  disabled: boolean
  onInstall: () => void
}) {
  if (!restarting && (!state || ['idle', 'current', 'unconfigured'].includes(state.phase))) return null

  const ready = state?.phase === 'ready'
  const downloading = state?.phase === 'downloading'
  const progress = state && state.total > 0 ? Math.min(100, Math.round(state.completed / state.total * 100)) : 0
  const label = restarting ? t('prepare.restarting') : ready ? t('prepare.updateButton') : downloading ? t('prepare.updateDownloading') : state?.phase === 'offline' ? t('prepare.updateOffline') : state?.phase === 'incompatible' ? t('prepare.updateIncompatible') : t('prepare.updateError')
  const Icon = restarting ? RotateCw : downloading ? LoaderCircle : ready ? ArrowDownToLine : AlertCircle
  const details = downloading ? `${progress}%${state?.version ? ' · ' + state.version : ''}` : state?.version ? t('prepare.updateVersion', { version: state.version }) : ''

  return (
    <div className="update-control" role="status" aria-live="polite" aria-busy={restarting || downloading}>
      {ready || downloading || restarting ? (
        <button className={`update-action${ready && !restarting ? ' ready' : ''}`} title={details ? `${label} · ${details}` : label} disabled={disabled || !ready || restarting} onClick={onInstall}>
          <span className="update-icon"><Icon className={restarting || downloading ? 'update-spinning' : ''} size={15} strokeWidth={2.25} aria-hidden="true" /></span>
          <span className="update-copy"><strong>{label}</strong>{downloading && details && <small>{details}</small>}</span>
        </button>
      ) : (
        <div className="update-status"><Icon size={18} aria-hidden="true" /><span className="update-copy"><strong>{label}</strong>{state?.phase === 'error' && <small>{state.message}</small>}</span></div>
      )}
      {downloading && <progress className="update-progress" value={state?.completed} max={state?.total || 1} aria-label={t('prepare.updateDownloading')} />}
    </div>
  )
}
