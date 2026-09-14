import { useEffect, useMemo, useRef, useState } from 'react'
import { AlertCircle, CheckCircle2, CirclePause, CirclePlay, Radio, Search } from 'lucide-react'
import { api } from '../api'
import type { TrafficEvent, TrafficPayload } from '../types'
import { formatDate, formatNumber, localeLower, t } from '../i18n'

type StreamState = 'connecting' | 'live' | 'reconnecting' | 'paused'

function shortMethod(method: string) {
  const parts = method.split('/').filter(Boolean)
  if (parts.length < 2) return method
  const service = parts[parts.length - 2].split('.').pop()
  return `${service}/${parts[parts.length - 1]}`
}

function isSuccess(event: TrafficEvent) {
  if (event.protocol.startsWith('grpc')) return event.status === 'OK'
  const status = Number(event.status)
  return status >= 200 && status < 400
}

function payloadText(payload: TrafficPayload) {
  if (payload.encoding === 'empty') return t('traffic.noContent')
  if (payload.encoding === 'json') return JSON.stringify(payload.data, null, 2)
  return String(payload.data ?? '')
}

function PayloadView({ title, payload }: { title: string; payload: TrafficPayload }) {
  return (
    <section className="traffic-payload">
      <header>
        <strong>{title}</strong>
        <span>{payload.encoding} · {t('traffic.bytes', { count: formatNumber(payload.sizeBytes) })}{payload.truncated ? t('traffic.truncated') : ''}</span>
      </header>
      <pre tabIndex={0}>{payloadText(payload)}</pre>
    </section>
  )
}

export default function TrafficPage({ serverRunning }: { serverRunning: boolean }) {
  const [events, setEvents] = useState<TrafficEvent[]>([])
  const [selectedID, setSelectedID] = useState('')
  const [query, setQuery] = useState('')
  const [protocol, setProtocol] = useState('all')
  const [failuresOnly, setFailuresOnly] = useState(false)
  const [live, setLive] = useState(true)
  const [streamState, setStreamState] = useState<StreamState>('connecting')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const latestID = useRef('')

  useEffect(() => {
    let disposed = false
    let closeStream: (() => void) | undefined
    const mergeEvents = (next: TrafficEvent[]) => {
      if (disposed || next.length === 0) return
      setEvents((current) => {
        if (!latestID.current) return next
        const known = new Set(current.map((event) => event.id))
        return [...next.filter((event) => !known.has(event.id)), ...current].slice(0, 500)
      })
      latestID.current = next[0].id
      setSelectedID((current) => current || next[0].id)
    }
    const refresh = async () => {
      try {
        const next = await api.traffic(500, latestID.current)
        if (disposed) return
        mergeEvents(next ?? [])
        setError('')
        if (!live || !serverRunning) {
          setStreamState('paused')
          return
        }
        setStreamState('connecting')
        closeStream = api.trafficStream(
          latestID.current,
          (event) => {
            mergeEvents([event])
            setError('')
          },
          (state) => { if (!disposed) setStreamState(state) },
          (streamError) => { if (!disposed) setError(streamError.message) },
        )
      } catch (value) {
        if (!disposed) {
          setError((value as Error).message)
          setStreamState(live ? 'reconnecting' : 'paused')
        }
      } finally {
        if (!disposed) setLoading(false)
      }
    }
    void refresh()
    return () => {
      disposed = true
      closeStream?.()
    }
  }, [live, serverRunning])

  const filtered = useMemo(() => {
    const needle = localeLower(query.trim())
    return events.filter((event) => {
      if (protocol !== 'all' && event.protocol !== protocol) return false
      if (failuresOnly && isSuccess(event)) return false
      if (!needle) return true
      return localeLower(`${event.method} ${event.playerId ?? ''} ${event.status}`).includes(needle)
    })
  }, [events, failuresOnly, protocol, query])

  const selected = filtered.find((event) => event.id === selectedID) ?? filtered[0]
  const failures = events.filter((event) => !isSuccess(event)).length
  const streamCopy = !serverRunning ? t('traffic.serverStopped') : !live
    ? t('traffic.paused')
    : streamState === 'live'
      ? t('traffic.live')
      : streamState === 'reconnecting'
        ? t('traffic.reconnecting')
        : t('traffic.connecting')

  return (
    <div className="page traffic-page">
      <div className="page-heading traffic-heading">
        <h1>{t('traffic.title')}</h1>
        <button className={`secondary-button live-control ${live ? 'active' : ''}`} onClick={() => setLive((value) => !value)}>
          {live ? <CirclePause /> : <CirclePlay />}
          {live ? t('traffic.pause') : t('traffic.resume')}
        </button>
      </div>

      <div className={`traffic-summary stream-${streamState}`} aria-live="polite">
        <span><Radio /> {streamCopy}</span>
        <span>{t('traffic.loaded', { count: events.length })}</span>
        <span className={failures ? 'has-failures' : ''}>{t('traffic.failed', { count: failures })}</span>
      </div>

      <div className="traffic-toolbar">
        <label className="catalog-search">
          <Search aria-hidden="true" />
          <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t('traffic.search')} aria-label={t('traffic.filter')} />
        </label>
        <label>
          <span>{t('traffic.protocol')}</span>
          <select value={protocol} onChange={(event) => setProtocol(event.target.value)}>
            <option value="all">{t('traffic.all')}</option>
            <option value="grpc">gRPC</option>
            <option value="grpc-stream">gRPC compatibility</option>
            <option value="http">HTTP REST</option>
          </select>
        </label>
        <label className="failure-filter">
          <input type="checkbox" checked={failuresOnly} onChange={(event) => setFailuresOnly(event.target.checked)} />
          <span>{t('traffic.errorsOnly')}</span>
        </label>
      </div>

      {error && <div className="traffic-error" role="alert"><AlertCircle /> <span>{error}</span></div>}

      <div className="traffic-workspace">
        <section className="traffic-list" aria-label={t('traffic.exchanges')}>
          {loading && Array.from({ length: 8 }, (_, index) => <div className="traffic-row-skeleton" key={index} />)}
          {!loading && filtered.length === 0 && (
            <div className="traffic-empty"><Radio /><strong>{t('traffic.empty')}</strong></div>
          )}
          {filtered.map((event) => {
            const success = isSuccess(event)
            return (
              <button className={`traffic-row ${selected?.id === event.id ? 'selected' : ''}`} key={event.id} onClick={() => setSelectedID(event.id)}>
                <span className={`traffic-status ${success ? 'success' : 'failure'}`}>{success ? <CheckCircle2 /> : <AlertCircle />}</span>
                <span className="traffic-row-main">
                  <strong>{shortMethod(event.method)}</strong>
                  <small>{formatDate(event.capturedAt, { hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false })} · {event.durationMs} ms</small>
                </span>
                <span className={`protocol-badge ${event.protocol}`}>{event.protocol === 'http' ? 'HTTP' : 'gRPC'}</span>
                <b>{event.status}</b>
              </button>
            )
          })}
        </section>

        <section className="traffic-detail" aria-live="polite">
          {!selected ? (
            <div className="traffic-detail-empty"><Radio /><span>{t('traffic.select')}</span></div>
          ) : (
            <>
              <header className="traffic-detail-header">
                <div>
                  <span>{selected.protocol} · {formatDate(selected.capturedAt)}</span>
                  <h2>{shortMethod(selected.method)}</h2>
                  <code>{selected.method}</code>
                </div>
                <span className={`detail-status ${isSuccess(selected) ? 'success' : 'failure'}`}>{selected.status}</span>
              </header>
              <dl className="traffic-metadata">
                <div><dt>{t('traffic.duration')}</dt><dd>{selected.durationMs} ms</dd></div>
                <div><dt>{t('traffic.player')}</dt><dd>{selected.playerId || t('traffic.unidentified')}</dd></div>
                <div><dt>{t('traffic.identifier')}</dt><dd>{selected.id}</dd></div>
              </dl>
              {selected.error && <div className="traffic-call-error"><AlertCircle /><code>{selected.error}</code></div>}
              <div className="payload-grid">
                <PayloadView title={t('traffic.request')} payload={selected.request} />
                <PayloadView title={t('traffic.response')} payload={selected.response} />
              </div>
            </>
          )}
        </section>
      </div>
    </div>
  )
}
