export type PlayerSummary = {
  ID: string
  DisplayName: string
  Level: number
  Experience: number
  StateVersion: number
  Active: boolean
  Authorized: boolean
}

export type PlayerDetails = Omit<PlayerSummary, 'Active' | 'Authorized'> & { CreatedAt?: string; UpdatedAt?: string }

export type Bootstrap = {
  csrfToken: string
  catalogVersion: string
  players: PlayerSummary[]
  device: { Account: string; ActivePlayerID: string; AuthorizedPlayerID: string } | null
  currencies: { ID: string; Name: string; Description: string; ImageURL: string }[]
}

export type ControlStatus = {
  csrfToken: string
  busy: boolean
  operation?: 'local' | 'online' | 'stop' | 'open'
  mode: 'local' | 'online' | 'stopped' | 'unknown'
  server: { running: boolean; pid?: number }
  android: {
    serial?: string
    connected: boolean
    root: boolean
    routing: string
    ca: string
    native: string
    game: string
    running: boolean
  }
}

export type Stock = {
  ID: string
  Name: string
  Kind: string
  Rarity: number
  Quantity: number
  ImageURL: string
}

export type ControlLogs = {
  events: { time: string; level: string; operation: string; message: string }[]
  diagnostics: string[]
}

export type StockAmount = {
  ID: string
  Quantity: number
  TotalQuantity?: number
}

export type Profile = {
  Player: PlayerDetails
  Settings: { Language: string; Country: string; IconID: string; MessageID: string }
  ProfileImageURL: string
  TutorialComplete: boolean
  RequiredExperience: number
  CardCount: number
  EmblemIDs: string[] | null
}

export type VisualCatalogItem = {
  ID: string
  Name: string
  Description: string
  Kind: string
  StockKind: 'cards' | 'items' | 'currencies'
  ImageURL: string
  Variant: number
}

export type CatalogResult = {
  ID: string
  Name: string
  Kind: string
  Meta: string
  Rarity: number
  ImageURL: string
}

export type CardPage = {
  Items: CatalogResult[] | null
  Expansions: { ID: string; Name: string }[] | null
  Rarities: number[] | null
  Page: number
  PageSize: number
  Total: number
  TotalPages: number
}

export type Pack = {
  ID: string
  Name: string
  Description: string
  ExpansionID: string
  ImageURL: string
  TableKinds: string[]
  Rarities: string[]
}

export type RuleSlot = { CardID: string; Locked: boolean }

export type IllegalPackRule = {
  CardCount: number
  AllowDuplicates: boolean
  ExpansionIDs: string[] | null
  Rarities: number[] | null
  CardKinds: string[] | null
}

export type PackRule = {
  Mode: 'official' | 'table' | 'rarity' | 'illegal'
  TargetPackID: string
  TableKind: string
  Rarity: string
  ReturnPackCount: number
  FreeOpenings: boolean
  FixedSeed: number | null
  Packs: RuleSlot[][] | null
  Illegal: IllegalPackRule
  UpdatedAt?: string
}

export type PackFilters = {
  Expansions: { ID: string; Name: string }[] | null
  Rarities: { Value: number; Label: string }[] | null
  CardKinds: string[] | null
}

export type PackStudio = { rule: PackRule; packs: Pack[]; filters: PackFilters }

export type TrafficPayload = {
  encoding: 'json' | 'text' | 'base64' | 'empty'
  sizeBytes: number
  truncated?: boolean
  data: unknown
}

export type TrafficEvent = {
  id: string
  capturedAt: string
  protocol: 'grpc' | 'grpc-stream' | 'http'
  method: string
  playerId?: string
  status: string
  durationMs: number
  error?: string
  request: TrafficPayload
  response: TrafficPayload
}
