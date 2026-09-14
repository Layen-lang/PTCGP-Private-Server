import { useEffect, useMemo, useState } from 'react'
import { CirclePause, CirclePlay, Download, Search, FileText } from 'lucide-react'
import { loadControlLogs } from '../api'
import type { ControlLogs } from '../types'
import { formatDate, localeLower, t } from '../i18n'

function operations(): Record<string, string> { return { panel: t('logs.op.panel'), local: t('logs.op.local'), online: t('logs.op.online'), stop: t('logs.op.stop'), open: t('logs.op.open') } }
function levels(): Record<string, string> { return { info: t('logs.level.info'), success: t('logs.level.success'), warning: t('logs.level.warning'), error: t('logs.level.error') } }

export default function LogsPage() {
  const [logs, setLogs] = useState<ControlLogs>({ events: [], diagnostics: [] })
  const [query, setQuery] = useState('')
  const [operation, setOperation] = useState('all')
  const [errorsOnly, setErrorsOnly] = useState(false)
  const [live, setLive] = useState(true)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  useEffect(() => {
    let disposed = false
    let timer: number | undefined
    const refresh = async () => {
      try {
        const next = await loadControlLogs()
        if (!disposed) { setLogs(next); setError('') }
      } catch (value) {
        if (!disposed) setError(value instanceof Error ? value.message : t('logs.loadFailed'))
      } finally {
        if (!disposed) {
          setLoading(false)
          if (live) timer = window.setTimeout(() => void refresh(), 1000)
        }
      }
    }
    void refresh()
    return () => { disposed = true; window.clearTimeout(timer) }
  }, [live])

  const filtered = useMemo(() => logs.events.filter((event) =>
    (operation === 'all' || event.operation === operation) &&
    (!errorsOnly || event.level === 'error' || event.level === 'warning') &&
    localeLower(`${event.message} ${operations()[event.operation] || event.operation}`).includes(localeLower(query)),
  ).reverse(), [logs.events, operation, errorsOnly, query])

  return <div className="page logs-page">
    <div className="page-heading">
      <h1>{t('logs.title')}</h1>
      <div className="logs-actions">
        <button className="secondary-button" onClick={() => setLive(!live)}>{live ? <CirclePause size={17} /> : <CirclePlay size={17} />}{live ? t('logs.pause') : t('logs.resume')}</button>
        <a className="secondary-button" href="/api/control/logs/export" download="ptcgp-logs.txt"><Download size={17} />{t('logs.export')}</a>
      </div>
    </div>
    <div className="logs-toolbar">
      <label className="logs-search"><Search size={17} /><input aria-label={t('common.search')} placeholder={t('common.search')} value={query} onChange={(event) => setQuery(event.target.value)} /></label>
      <label className="logs-filter"><span>{t('logs.operation')}</span><select value={operation} onChange={(event) => setOperation(event.target.value)}><option value="all">{t('logs.allOperations')}</option>{Object.entries(operations()).map(([value, label]) => <option key={value} value={value}>{label}</option>)}</select></label>
      <label className="logs-checkbox"><input type="checkbox" checked={errorsOnly} onChange={(event) => setErrorsOnly(event.target.checked)} />{t('logs.errorsWarnings')}</label>
    </div>
    <div className="logs-status" role="status"><span>{loading ? t('common.loading') : error ? t('logs.offline') : live ? t('logs.live') : t('logs.paused')}</span><span>{t('logs.eventCount', { count: filtered.length, suffix: filtered.length !== 1 ? 's' : '' })}</span></div>
    {error && <p className="logs-error" role="alert">{error}</p>}
    <div className="logs-table-wrap" tabIndex={0} aria-label={t('logs.title')}>
      <table className="logs-table"><thead><tr><th>{t('logs.dateTime')}</th><th>{t('logs.operation')}</th><th>{t('logs.state')}</th><th>{t('logs.message')}</th></tr></thead><tbody>
        {filtered.map((event, index) => <tr key={`${event.time}-${index}`}><td><time dateTime={event.time}>{formatDate(event.time, { day: '2-digit', month: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit' })}</time></td><td>{operations()[event.operation] || event.operation}</td><td><span className={`log-level ${event.level}`}>{levels()[event.level] || event.level}</span></td><td>{event.message}</td></tr>)}
      </tbody></table>
      {!loading && !filtered.length && <div className="logs-empty"><FileText /><strong>{logs.events.length ? t('logs.noResult') : t('logs.empty')}</strong></div>}
    </div>
    <details className="logs-diagnostics"><summary>{t('logs.technical')} <span>{t('logs.lines', { count: logs.diagnostics.length })}</span></summary><pre tabIndex={0}>{logs.diagnostics.join('\n') || t('logs.noOutput')}</pre></details>
  </div>
}
