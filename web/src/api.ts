import type { Bootstrap, CardPage, CatalogResult, ControlLogs, ControlStatus, IllegalPackRule, PackRule, PackStudio, Profile, StockAmount, TrafficEvent, VisualCatalogItem } from './types'
import { getLocale, t } from './i18n'

let csrfToken = ''
let controlToken = ''
const profileRequests = new Map<string, Promise<Profile>>()
const inventoryRequests = new Map<string, Promise<StockAmount[]>>()
const catalogRequests = new Map<string, Promise<VisualCatalogItem[]>>()

function cached<T>(cache: Map<string, Promise<T>>, key: string, load: () => Promise<T>): Promise<T> {
  const current = cache.get(key)
  if (current) return current
  const pending = load().catch((error) => {
    cache.delete(key)
    throw error
  })
  cache.set(key, pending)
  return pending
}

function replaceAmount(values: StockAmount[], id: string, quantity: number) {
  const next = values.filter((value) => value.ID !== id)
  if (quantity > 0) next.push({ ID: id, Quantity: quantity })
  return next
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(path, {
    ...init,
    headers: {
      Accept: 'application/json',
      ...(init?.body ? { 'Content-Type': 'application/json' } : {}),
      ...(init?.method && init.method !== 'GET' ? { 'X-CSRF-Token': csrfToken } : {}),
      'X-PTCGP-Locale': getLocale(),
      ...init?.headers,
    },
  })
  const body = await response.json().catch(() => ({}))
  if (!response.ok) throw new Error(body.error || t('common.httpError', { status: response.status }))
  return body as T
}

export async function loadBootstrap() {
  const value = await request<Bootstrap>('/api/bootstrap')
  csrfToken = value.csrfToken
  return value
}

export async function loadControlStatus() {
  const value = await request<ControlStatus>('/api/control/status')
  controlToken = value.csrfToken
  return value
}

export const loadControlLogs = () => request<ControlLogs>('/api/control/logs')

export async function runControlAction(action: 'local' | 'online' | 'stop') {
  const response = await fetch(`/api/control/actions/${action}`, {
    method: 'POST',
    headers: {
      Accept: 'application/json',
      'X-Control-CSRF-Token': controlToken,
      'X-PTCGP-Locale': getLocale(),
    },
  })
  const body = await response.json().catch(() => ({}))
  if (response.status === 409) return loadControlStatus()
  if (!response.ok) throw new Error(body.error || t('common.httpError', { status: response.status }))
  const value = body as ControlStatus
  controlToken = value.csrfToken
  return value
}

export const api = {
  profile: (id: string) => cached(profileRequests, `${getLocale()}:${id}`, () => request<Profile>(`/api/players/${id}/summary`)),
  inventory: (id: string, section: 'resources' | 'cards' | 'emblems' | 'battle' | 'showcase', language?: number) => {
    const key = `${id}:${section}${section === 'cards' ? `:${language || 4}` : ''}`
    return cached(inventoryRequests, key, async () =>
      (await request<StockAmount[] | null>(`/api/players/${id}/inventory/${section}${section === 'cards' ? `?language=${language || 4}` : ''}`)) ?? [],
    )
  },
  createPlayer: (input: object) => request('/api/players', { method: 'POST', body: JSON.stringify(input) }),
  duplicatePlayer: (id: string) => request<{ ID: string }>(`/api/players/${id}/duplicate`, { method: 'POST' }),
  deletePlayer: (id: string) => request<void>(`/api/players/${id}`, { method: 'DELETE' }),
  reorderPlayers: (PlayerIDs: string[]) => request('/api/players/order', { method: 'PUT', body: JSON.stringify({ PlayerIDs }) }),
  updateProfile: async (id: string, input: object) => {
    const value = await request<Profile>(`/api/players/${id}`, { method: 'PATCH', body: JSON.stringify(input) })
    profileRequests.set(`${getLocale()}:${id}`, Promise.resolve(value))
    return value
  },
  updateStock: async (playerID: string, kind: string, id: string, quantity: number, language?: number) => {
    const value = await request<StockAmount>(`/api/players/${playerID}/${kind}/${encodeURIComponent(id)}`, { method: 'PUT', body: JSON.stringify({ Quantity: quantity, ...(kind === 'cards' ? { Language: language || 4 } : {}) }) })
    if (kind === 'cards') {
      for (const key of inventoryRequests.keys()) {
        if (key.startsWith(`${playerID}:cards:`)) inventoryRequests.delete(key)
      }
    } else if (kind === 'currencies') {
      const key = `${playerID}:resources`
      const current = inventoryRequests.get(key)
      if (current) inventoryRequests.set(key, current.then((values) => replaceAmount(values, value.ID, value.Quantity)))
    } else {
      for (const section of ['resources', 'battle', 'showcase']) inventoryRequests.delete(`${playerID}:${section}`)
    }
    return value
  },
  openPlayer: (id: string) => request(`/api/players/${id}/open`, { method: 'POST' }),
  cards: (query: string, pack = '') => request<CatalogResult[]>(`/api/catalog/cards?q=${encodeURIComponent(query)}&pack=${encodeURIComponent(pack)}&limit=36`),
  items: (query: string) => request<CatalogResult[]>(`/api/catalog/items?q=${encodeURIComponent(query)}&limit=36`),
  cardPage: (query: string, expansion: string, rarity: number, page: number) => request<CardPage>(`/api/catalog/card-browser?q=${encodeURIComponent(query)}&expansion=${encodeURIComponent(expansion)}&rarity=${rarity || ''}&page=${page}`),
  resources: () => cached(catalogRequests, `${getLocale()}:resources`, () => request<VisualCatalogItem[]>('/api/catalog/resources')),
  profileIcons: () => cached(catalogRequests, `${getLocale()}:profile-icons`, () => request<VisualCatalogItem[]>('/api/catalog/profile-icons')),
  profileEmblems: () => cached(catalogRequests, `${getLocale()}:profile-emblems`, () => request<VisualCatalogItem[]>('/api/catalog/profile-emblems')),
  peripherals: (group: 'battle' | 'showcase') => cached(catalogRequests, `${getLocale()}:peripherals:${group}`, () => request<VisualCatalogItem[]>(`/api/catalog/peripherals?group=${group}`)),
  packStudio: () => request<PackStudio>('/api/packs'),
  illegalPackPool: (rule: IllegalPackRule) => request<{ Count: number }>('/api/packs/pool', { method: 'POST', body: JSON.stringify(rule) }),
  savePackRule: (rule: PackRule) => request<PackRule>('/api/packs/rule', { method: 'PUT', body: JSON.stringify(rule) }),
  traffic: (limit = 500, after = '') => request<TrafficEvent[]>(`/api/traffic?limit=${limit}${after ? `&after=${encodeURIComponent(after)}` : ''}`),
  trafficStream: (
    after: string,
    onEvent: (event: TrafficEvent) => void,
    onState: (state: 'live' | 'reconnecting') => void,
    onError: (error: Error) => void,
  ) => {
    const source = new EventSource(`/api/traffic/stream${after ? `?after=${encodeURIComponent(after)}` : ''}`)
    const listener: EventListener = (message) => {
      try {
        onEvent(JSON.parse((message as MessageEvent<string>).data) as TrafficEvent)
      } catch {
        onError(new Error(t('traffic.unreadable')))
      }
    }
    source.addEventListener('traffic', listener)
    source.onopen = () => onState('live')
    source.onerror = () => onState('reconnecting')
    return () => {
      source.removeEventListener('traffic', listener)
      source.close()
    }
  },
}
