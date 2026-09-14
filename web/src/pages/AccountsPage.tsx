import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Award, Check, ChevronDown, ChevronLeft, ChevronRight, Copy, Gamepad2, GripVertical, Image as ImageIcon,
  Layers3, Minus, PackageOpen, Plus, Search, Shield, Sparkles, Trash2, UserRound, WalletCards, X,
} from 'lucide-react'
import { api } from '../api'
import NumberInput from '../components/NumberInput'
import type { Bootstrap, CardPage, CatalogResult, PlayerSummary, Profile, VisualCatalogItem } from '../types'
import { localeLower, t } from '../i18n'

type Props = { canLaunch: boolean; bootstrap: Bootstrap; refresh: () => void; reportError: (message: string) => void }
type Section = 'profile' | 'resources' | 'cards' | 'battle' | 'showcase'
type DropHint = { id: string; placement: 'before' | 'after' }

const primaryResourceOrder = [
  'POKEGOLD_FREE', 'POKEGOLD_PAID', 'SHINEDUST', 'SHOPTICKET', 'SHOPTICKET_P', 'PREMIUM_TICKET', 'ITSUKA_TICKET', 'TICKET_DECK_A2b',
  'PACK_CHARGER_100030', 'CHALLENGE_CHARGER_110030', 'EVENT_CHARGER', 'REVIVAL_CLOCK_100010', 'TRADE_ITEM_130010', 'TRADE_CHAGER_140010',
]

function cardLanguages() { return [
  { id: 1, name: t('cards.japanese') }, { id: 2, name: t('cards.english') }, { id: 3, name: t('cards.traditionalChinese') },
  { id: 4, name: t('cards.french') }, { id: 5, name: t('cards.italian') }, { id: 6, name: t('cards.german') },
  { id: 7, name: t('cards.spanish') }, { id: 8, name: t('cards.portugueseBrazil') }, { id: 9, name: t('cards.korean') },
  { id: 10, name: t('cards.spanishLatam') }, { id: 11, name: t('cards.simplifiedChinese') }, { id: 12, name: t('cards.indonesian') },
] }

function accountLanguages() { return [
  { locale: 'ja_JP', name: t('cards.japanese') }, { locale: 'en_US', name: t('cards.english') },
  { locale: 'zh_TW', name: t('cards.traditionalChinese') }, { locale: 'fr_FR', name: t('cards.french') },
  { locale: 'it_IT', name: t('cards.italian') }, { locale: 'de_DE', name: t('cards.german') },
  { locale: 'es_ES', name: t('cards.spanish') }, { locale: 'pt_BR', name: t('cards.portugueseBrazil') },
  { locale: 'ko_KR', name: t('cards.korean') }, { locale: 'es_419', name: t('cards.spanishLatam') },
  { locale: 'zh_CN', name: t('cards.simplifiedChinese') }, { locale: 'id_ID', name: t('cards.indonesian') },
] }

export default function AccountsPage({ bootstrap, refresh, reportError, canLaunch }: Props) {
  const [players, setPlayers] = useState(bootstrap.players)
  const [selectedID, setSelectedID] = useState(bootstrap.players.find((value) => value.Active)?.ID || bootstrap.players[0]?.ID || '')
  const [profile, setProfile] = useState<Profile | null>(null)
  const [section, setSection] = useState<Section>('profile')
  const [creating, setCreating] = useState(false)
  const [deleting, setDeleting] = useState<PlayerSummary | null>(null)
  const [busy, setBusy] = useState(false)
  const [draggedID, setDraggedID] = useState('')
  const [dropHint, setDropHint] = useState<DropHint | null>(null)
  const profileCache = useRef(new Map<string, Profile>())
  const selectedSummary = players.find((value) => value.ID === selectedID)

  useEffect(() => setPlayers(bootstrap.players), [bootstrap.players])
  useEffect(() => {
    const timer = window.setTimeout(() => {
      for (const player of players) {
        if (profileCache.current.has(player.ID)) continue
        void api.profile(player.ID).then((value) => profileCache.current.set(player.ID, value)).catch(() => undefined)
      }
    }, 0)
    return () => window.clearTimeout(timer)
  }, [players])
  useEffect(() => {
    if (!selectedID) { setProfile(null); return }
    setProfile(profileCache.current.get(selectedID) || null)
    let cancelled = false
    api.profile(selectedID)
      .then((value) => {
        profileCache.current.set(selectedID, value)
        if (!cancelled) setProfile(value)
      })
      .catch((error: Error) => { if (!cancelled) reportError(error.message) })
    return () => { cancelled = true }
  }, [selectedID, reportError])

  function saveProfile(value: Profile) {
    profileCache.current.set(value.Player.ID, value)
    setProfile(value)
  }

  async function openAccount() {
    if (!selectedID) return
    setBusy(true)
    try {
      await api.openPlayer(selectedID)
      refresh()
    } catch (error) { reportError((error as Error).message) }
    finally { setBusy(false) }
  }

  async function duplicate(id: string) {
    setBusy(true)
    try { const created = await api.duplicatePlayer(id); setSelectedID(created.ID); refresh() }
    catch (error) { reportError((error as Error).message) }
    finally { setBusy(false) }
  }

  async function remove() {
    if (!deleting) return
    setBusy(true)
    try {
      await api.deletePlayer(deleting.ID)
      setSelectedID(players.find((value) => value.ID !== deleting.ID)?.ID || '')
      setDeleting(null); refresh()
    } catch (error) { reportError((error as Error).message) }
    finally { setBusy(false) }
  }

  function beginPlayerDrag(event: React.DragEvent<HTMLButtonElement>, playerID: string) {
    const row = event.currentTarget.closest<HTMLElement>('.managed-account')
    event.dataTransfer.effectAllowed = 'move'
    event.dataTransfer.setData('text/plain', playerID)
    if (row) event.dataTransfer.setDragImage(row, 24, Math.min(34, row.offsetHeight / 2))
    setDraggedID(playerID)
  }

  function previewPlayerDrop(event: React.DragEvent<HTMLDivElement>, targetID: string) {
    event.preventDefault()
    if (!draggedID || draggedID === targetID) { setDropHint(null); return }
    event.dataTransfer.dropEffect = 'move'
    const bounds = event.currentTarget.getBoundingClientRect()
    const placement = event.clientY < bounds.top + bounds.height / 2 ? 'before' : 'after'
    setDropHint((current) => current?.id === targetID && current.placement === placement ? current : { id: targetID, placement })
  }

  function finishPlayerDrag() {
    setDraggedID('')
    setDropHint(null)
  }

  async function dropPlayer(targetID: string, placement: 'before' | 'after') {
    const movedID = draggedID
    finishPlayerDrag()
    if (!movedID || movedID === targetID) return
    const reordered = [...players]
    const source = reordered.findIndex((value) => value.ID === movedID)
    const [moved] = reordered.splice(source, 1)
    let target = reordered.findIndex((value) => value.ID === targetID)
    if (placement === 'after') target += 1
    reordered.splice(target, 0, moved)
    setPlayers(reordered)
    try { await api.reorderPlayers(reordered.map((value) => value.ID)); refresh() }
    catch (error) { setPlayers(bootstrap.players); reportError((error as Error).message) }
  }

  return <div className="page accounts-page account-editor-page">
    <section className="page-heading compact-heading"><h1>{t('accounts.title')}</h1></section>
    <div className={`accounts-layout account-editor-layout ${section === 'cards' ? 'cards-focus' : ''}`}>
      <aside className="account-rail managed-rail" aria-label={t('accounts.list')}>
        <div className="rail-title"><strong>{t('accounts.profileCount', { count: players.length })}</strong></div>
        {players.map((value) => <div key={value.ID} className={`account-row managed-account ${selectedID === value.ID ? 'selected' : ''} ${draggedID === value.ID ? 'dragging' : ''} ${dropHint?.id === value.ID ? `drop-${dropHint.placement}` : ''}`} onDragOver={(event) => previewPlayerDrop(event, value.ID)} onDrop={() => void dropPlayer(value.ID, dropHint?.id === value.ID ? dropHint.placement : 'before')}>
          <button type="button" className="drag-handle" draggable onDragStart={(event) => beginPlayerDrag(event, value.ID)} onDragEnd={finishPlayerDrag} aria-label={t('accounts.move', { name: value.DisplayName })} aria-pressed={draggedID === value.ID} title={t('accounts.drag')}><GripVertical size={16} /></button>
          <button type="button" className="account-select" aria-pressed={selectedID === value.ID} onClick={() => setSelectedID(value.ID)}><span className="avatar">{value.DisplayName.slice(0, 1).toUpperCase()}</span><span><strong>{value.DisplayName}</strong><small>{t('accounts.level', { level: value.Level })}</small></span></button>
          <span className="account-tools"><button type="button" onClick={() => void duplicate(value.ID)} aria-label={t('accounts.duplicate', { name: value.DisplayName })} title={t('accounts.duplicateAction')}><Copy size={15} /></button><button type="button" className="danger-icon" onClick={() => setDeleting(value)} aria-label={t('accounts.delete', { name: value.DisplayName })} title={t('accounts.deleteAction')}><Trash2 size={15} /></button></span>
        </div>)}
        {!players.length && <div className="empty-rail"><UserRound /><strong>{t('accounts.none')}</strong></div>}
        <button type="button" className="rail-add-account" onClick={() => setCreating(true)} aria-label={t('accounts.createNew')}><Plus size={17} /><span>{t('accounts.new')}</span></button>
      </aside>
      <section className="workspace account-workspace">
        {profile ? <>
          <header className="profile-header">
            <div className="profile-identity"><span className="avatar large profile-avatar"><Image src={profile.ProfileImageURL} alt="" /></span><div><h2>{profile.Player.DisplayName}</h2><p>{t('accounts.cardCount', { level: profile.Player.Level, count: profile.CardCount })}</p></div></div>
            <div className="profile-actions"><button className="primary-button" disabled={busy || !canLaunch} title={canLaunch ? t('accounts.launchReady') : t('accounts.launchUnavailable')} onClick={() => void openAccount()}><Gamepad2 size={18} /> {t('accounts.launch')}</button></div>
          </header>
          <nav className="section-tabs account-sections" aria-label={t('accounts.profileData')}>
            <Tab active={section === 'profile'} onClick={() => setSection('profile')} icon={<UserRound />}>{t('accounts.profile')}</Tab><Tab active={section === 'resources'} onClick={() => setSection('resources')} icon={<Sparkles />}>{t('accounts.resources')}</Tab><Tab active={section === 'cards'} onClick={() => setSection('cards')} icon={<WalletCards />}>{t('accounts.cards')}</Tab><Tab active={section === 'battle'} onClick={() => setSection('battle')} icon={<Shield />}>{t('accounts.battle')}</Tab><Tab active={section === 'showcase'} onClick={() => setSection('showcase')} icon={<ImageIcon />}>{t('accounts.showcases')}</Tab>
          </nav>
          <div className="workspace-body account-section-body">
            {section === 'profile' && <ProfileEditor key={profile.Player.ID} profile={profile} saved={saveProfile} reportError={reportError} />}
            {section === 'resources' && <ResourcesEditor key={profile.Player.ID} profile={profile} reportError={reportError} />}
            {section === 'cards' && <CardCollection key={profile.Player.ID} profile={profile} cardCountChanged={(delta) => saveProfile({ ...profile, CardCount: Math.max(0, profile.CardCount + delta) })} reportError={reportError} />}
            {section === 'battle' && <PeripheralEditor key={`${profile.Player.ID}-battle`} group="battle" profile={profile} reportError={reportError} />}
            {section === 'showcase' && <PeripheralEditor key={`${profile.Player.ID}-showcase`} group="showcase" profile={profile} reportError={reportError} />}
          </div>
        </> : selectedSummary ? <ProfileLoading player={selectedSummary} /> : <div className="workspace-empty"><UserRound /><h2>{t('accounts.choose')}</h2></div>}
      </section>
    </div>
    {creating && <CreateDialog close={() => setCreating(false)} created={(id) => { setCreating(false); setSelectedID(id); refresh() }} reportError={reportError} />}
    {deleting && <DeleteDialog player={deleting} busy={busy} close={() => setDeleting(null)} confirm={() => void remove()} />}
  </div>
}

function Tab({ active, onClick, icon, children }: { active: boolean; onClick: () => void; icon: React.ReactNode; children: React.ReactNode }) { return <button className={active ? 'active' : ''} onClick={onClick}>{icon}{children}</button> }

function ProfileEditor({ profile, saved, reportError }: { profile: Profile; saved: (value: Profile) => void; reportError: (value: string) => void }) {
  const [name, setName] = useState(profile.Player.DisplayName), [level, setLevel] = useState(profile.Player.Level), [experience, setExperience] = useState(profile.Player.Experience)
  const [language, setLanguage] = useState(profile.Settings.Language), [country, setCountry] = useState(profile.Settings.Country)
  const [tutorial, setTutorial] = useState(profile.TutorialComplete), [iconID, setIconID] = useState(profile.Settings.IconID), [icons, setIcons] = useState<VisualCatalogItem[]>([]), [query, setQuery] = useState(''), [busy, setBusy] = useState(false)
  const [emblems, setEmblems] = useState<VisualCatalogItem[]>([]), [selectedEmblemIDs, setSelectedEmblemIDs] = useState<string[]>(profile.EmblemIDs || []), [activeEmblemSlot, setActiveEmblemSlot] = useState(Math.min(profile.EmblemIDs?.length || 0, 2)), [emblemQuery, setEmblemQuery] = useState(''), [emblemsLoading, setEmblemsLoading] = useState(true)
  useEffect(() => { api.profileIcons().then(setIcons).catch((error: Error) => reportError(error.message)) }, [reportError])
  useEffect(() => {
    setEmblemsLoading(true)
    Promise.all([api.profileEmblems(), api.inventory(profile.Player.ID, 'emblems')])
      .then(([catalog, owned]) => {
        const ownedIDs = new Set(owned.filter((value) => value.Quantity > 0).map((value) => value.ID))
        setEmblems(catalog.filter((value) => ownedIDs.has(value.ID)))
      })
      .catch((error: Error) => reportError(error.message))
      .finally(() => setEmblemsLoading(false))
  }, [profile.Player.ID, reportError])
  const visibleIcons = icons.filter((value) => (value.Name + value.ID).toLowerCase().includes(query.toLowerCase()))
  const visibleEmblems = emblems.filter((value) => (value.Name + value.ID).toLowerCase().includes(emblemQuery.toLowerCase()))
  const emblemByID = useMemo(() => new Map(emblems.map((value) => [value.ID, value])), [emblems])
  function chooseEmblem(id: string) {
    setSelectedEmblemIDs((current) => {
      const next = current.filter((value) => value !== id)
      const target = Math.min(activeEmblemSlot, next.length)
      next.splice(target, 0, id)
      return next.slice(0, 3)
    })
    setActiveEmblemSlot((current) => Math.min(current + 1, 2))
  }
  function removeEmblem(index: number) {
    setSelectedEmblemIDs((current) => current.filter((_, currentIndex) => currentIndex !== index))
    setActiveEmblemSlot(Math.max(0, index - 1))
  }
  async function submit(event: React.FormEvent) { event.preventDefault(); setBusy(true); try { saved(await api.updateProfile(profile.Player.ID, { DisplayName: name, Language: language, Country: country.toUpperCase(), Level: level, Experience: experience, TutorialComplete: tutorial, IconID: iconID, EmblemIDs: selectedEmblemIDs })) } catch (error) { reportError((error as Error).message) } finally { setBusy(false) } }
  return <form className="editor-form" onSubmit={submit}>
    <div className="section-intro"><h3>{t('accounts.identity')}</h3><button className="primary-button" disabled={busy}>{busy ? t('accounts.saving') : t('accounts.saveProfile')}</button></div>
    <div className="form-grid profile-fields"><label><span>{t('accounts.nickname')}</span><input value={name} maxLength={14} onChange={(event) => setName(event.target.value)} /></label><label><span>{t('accounts.level', { level: '' }).trim()}</span><NumberInput required min={1} max={60} step={1} value={level} onValueChange={(value) => { if (value !== null) setLevel(value) }} /></label><label><span>{t('accounts.experience')}</span><NumberInput required min={0} step={1} value={experience} onValueChange={(value) => { if (value !== null) setExperience(value) }} /><small>{t('accounts.nextLevel', { value: profile.RequiredExperience || t('common.maximum') })}</small></label></div>
    <div className="form-grid two account-locale-fields"><label><span>{t('accounts.gameLanguage')}</span><select required value={language} onChange={(event) => setLanguage(event.target.value)}>{accountLanguages().map((option) => <option key={option.locale} value={option.locale}>{option.name}</option>)}</select></label><label><span>{t('accounts.region')}</span><input required value={country} minLength={2} maxLength={2} pattern="[A-Za-z]{2}" autoCapitalize="characters" placeholder={t('accounts.regionPlaceholder')} onChange={(event) => setCountry(event.target.value.toUpperCase())}/><small>{t('accounts.regionHint')}</small></label></div>
    <label className="switch-row"><input type="checkbox" checked={tutorial} onChange={(event) => setTutorial(event.target.checked)} /><span className="switch"/><strong>{t('accounts.tutorialDone')}</strong></label>
    <details className="profile-catalog-section">
      <summary className="profile-catalog-summary"><div><h4>{t('accounts.profilePicture')}</h4><p>{t('accounts.iconsAvailable', { count: icons.length })}</p></div><span>{t('common.modify')}</span><ChevronDown aria-hidden="true"/></summary>
      <div className="profile-catalog-content">
        <label className="mini-search catalog-fold-search"><Search size={16}/><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t('accounts.searchIcon')} /></label>
        <div className="profile-icon-grid">{visibleIcons.map((value) => <button type="button" key={value.ID} className={iconID === value.ID ? 'selected' : ''} onClick={() => setIconID(value.ID)} title={value.Name}><Image src={value.ImageURL} alt=""/><span>{cleanName(value.Name)}</span>{iconID === value.ID && <Check size={15}/>}</button>)}</div>
      </div>
    </details>
    <details className="profile-catalog-section emblem-section">
      <summary className="profile-catalog-summary"><div><h4>{t('accounts.emblems')}</h4></div><span>{t('accounts.selectedOfThree', { count: selectedEmblemIDs.length })}</span><ChevronDown aria-hidden="true"/></summary>
      <div className="profile-catalog-content">
        <label className="mini-search catalog-fold-search"><Search size={16}/><input value={emblemQuery} onChange={(event) => setEmblemQuery(event.target.value)} placeholder={t('accounts.searchEmblem')} /></label>
        <div className="emblem-slot-row" aria-label={t('accounts.emblemOrder')}>
          {[0, 1, 2].map((index) => {
            const selected = emblemByID.get(selectedEmblemIDs[index])
            const available = index <= selectedEmblemIDs.length
            return <div className={`emblem-slot ${activeEmblemSlot === index ? 'active' : ''} ${selected ? 'filled' : ''}`} key={index}>
              <button type="button" disabled={!available} onClick={() => setActiveEmblemSlot(index)} aria-pressed={activeEmblemSlot === index} aria-label={t('accounts.position', { position: index + 1, name: selected ? ` : ${selected.Name}` : t('accounts.emptyPosition') })}>
                <span className="emblem-position">{index + 1}</span>
                {selected ? <Image src={selected.ImageURL} alt=""/> : <span className="emblem-empty"><Award/><small>{index === 0 ? t('accounts.left') : index === 1 ? t('accounts.center') : t('accounts.right')}</small></span>}
                {selected && <strong>{cleanName(selected.Name)}</strong>}
              </button>
              {selected && <button type="button" className="emblem-remove" onClick={() => removeEmblem(index)} aria-label={t('common.remove') + ` ${selected.Name}`} title={t('common.remove')}><X/></button>}
            </div>
          })}
        </div>
        {emblemsLoading ? <div className="emblem-grid-loading" aria-label={t('accounts.loadingEmblems')} aria-busy="true">{Array.from({ length: 6 }, (_, index) => <span key={index}/>)}</div> : emblems.length ? <div className="emblem-catalog">{visibleEmblems.map((value) => { const position = selectedEmblemIDs.indexOf(value.ID); return <button type="button" key={value.ID} className={position >= 0 ? 'selected' : ''} onClick={() => chooseEmblem(value.ID)} title={value.Name}><Image src={value.ImageURL} alt=""/><span>{cleanName(value.Name)}</span>{position >= 0 && <b>{position + 1}</b>}</button> })}</div> : <div className="emblem-empty-state"><Award/><strong>{t('accounts.noOwnedEmblems')}</strong></div>}
      </div>
    </details>
  </form>
}

function ResourcesEditor({ profile, reportError }: { profile: Profile; reportError: (value: string) => void }) {
  const [catalog, setCatalog] = useState<VisualCatalogItem[]>([])
  const [owned, setOwned] = useState<Map<string, number> | null>(null)
  useEffect(() => {
    Promise.all([api.resources(), api.inventory(profile.Player.ID, 'resources')])
      .then(([definitions, amounts]) => { setCatalog(definitions); setOwned(new Map(amounts.map((value) => [value.ID, value.Quantity]))) })
      .catch((error: Error) => reportError(error.message))
  }, [profile.Player.ID, reportError])
  const primaryByID = useMemo(() => new Map(primaryResourceOrder.map((id, index) => [id, index])), [])
  const primary = catalog.filter((value) => primaryByID.has(value.ID)).sort((left, right) => primaryByID.get(left.ID)! - primaryByID.get(right.ID)!)
  const secondary = catalog.filter((value) => !primaryByID.has(value.ID))
  const row = (value: VisualCatalogItem) => <QuantityRow key={`${profile.Player.ID}-${value.ID}`} item={value} value={owned?.get(value.ID) || 0} save={async (quantity) => { try { const amount = await api.updateStock(profile.Player.ID, value.StockKind, value.ID, quantity); setOwned((current) => { const next = new Map(current || []); next.set(amount.ID, amount.Quantity); return next }) } catch (error) { reportError((error as Error).message); throw error } }} />
  return <div><div className="section-intro"><h3>{t('accounts.resourcesTitle')}</h3><span className="count-label">{t('accounts.resourceCount', { count: catalog.length })}</span></div>
    {!owned ? <InventorySkeleton rows={7} /> : <><section className="resource-section primary-resources"><div className="resource-section-heading"><h4>{t('accounts.currentResources')}</h4><span>{primary.length}</span></div><div className="currency-grid resource-list">{primary.map(row)}</div></section>
    <section className="resource-section secondary-resources"><div className="resource-section-heading"><h4>{t('accounts.otherResources')}</h4><span>{secondary.length}</span></div><div className="currency-grid resource-list">{secondary.map(row)}</div></section></>}
  </div>
}

function QuantityRow({ item, value, save }: { item: VisualCatalogItem; value: number; save: (value: number) => Promise<void> }) {
  const [quantity, setQuantity] = useState(value), [state, setState] = useState<'idle' | 'busy' | 'saved'>('idle')
  async function apply() { setState('busy'); try { await save(quantity); setState('saved') } catch { setState('idle') } }
  return <div className="currency-row"><span className="currency-icon"><Image src={item.ImageURL} alt="" /></span><span className="currency-copy"><strong>{item.Name}</strong><small>{item.ID}</small></span><label className="currency-quantity"><span>{t('accounts.quantity')}</span><NumberInput aria-label={t('accounts.quantityOf', { name: item.Name })} min={0} step={1} value={quantity} onValueChange={(next) => { if (next !== null) setQuantity(next); setState('idle') }}/></label><button type="button" className={state === 'saved' ? 'secondary-button applied' : 'secondary-button'} disabled={state === 'busy'} onClick={() => void apply()}>{state === 'busy' ? t('common.applying') : state === 'saved' ? t('common.applied') : t('common.apply')}</button></div>
}

function CardCollection({ profile, cardCountChanged, reportError }: { profile: Profile; cardCountChanged: (delta: number) => void; reportError: (value: string) => void }) {
	const [query, setQuery] = useState(''), [expansion, setExpansion] = useState(''), [rarity, setRarity] = useState(0), [language, setLanguage] = useState(4), [page, setPage] = useState(1), [catalog, setCatalog] = useState<CardPage | null>(null), [drafts, setDrafts] = useState<Record<string, number>>({}), [busyID, setBusyID] = useState('')
	const [owned, setOwned] = useState<Map<string, number> | null>(null)
	const [totals, setTotals] = useState<Map<string, number> | null>(null)
	useEffect(() => { setOwned(null); setTotals(null); setDrafts({}); api.inventory(profile.Player.ID, 'cards', language).then((amounts) => { setOwned(new Map(amounts.map((value) => [value.ID, value.Quantity]))); setTotals(new Map(amounts.map((value) => [value.ID, value.TotalQuantity ?? value.Quantity]))) }).catch((error: Error) => reportError(error.message)) }, [language, profile.Player.ID, reportError])
	useEffect(() => { const timer = window.setTimeout(() => api.cardPage(query, expansion, rarity, page).then(setCatalog).catch((error: Error) => reportError(error.message)), 180); return () => window.clearTimeout(timer) }, [query, expansion, rarity, page, reportError])
	async function setQuantity(card: CatalogResult, quantity: number) { if (!owned || !totals) return; quantity = Math.max(0, quantity); const previousTotal = totals.get(card.ID) || 0; setBusyID(card.ID); try { const amount = await api.updateStock(profile.Player.ID, 'cards', card.ID, quantity, language); const nextTotal = amount.TotalQuantity ?? amount.Quantity; setOwned((current) => { const next = new Map(current || []); if (amount.Quantity > 0) next.set(amount.ID, amount.Quantity); else next.delete(amount.ID); return next }); setTotals((current) => { const next = new Map(current || []); if (nextTotal > 0) next.set(amount.ID, nextTotal); else next.delete(amount.ID); return next }); if ((previousTotal === 0) !== (nextTotal === 0)) cardCountChanged(nextTotal > 0 ? 1 : -1); setDrafts((current) => ({ ...current, [card.ID]: amount.Quantity })) } catch (error) { reportError((error as Error).message) } finally { setBusyID('') } }
	return <div className="card-collection-editor">
	  <div className="section-intro"><h3>{t('accounts.collection')}</h3><span className="count-label">{t('accounts.resultCount', { count: catalog?.Total || 0 })}</span></div>
	  <div className="catalog-toolbar card-catalog-toolbar"><label className="catalog-search"><Search size={18}/><input value={query} onChange={(event) => { setQuery(event.target.value); setPage(1) }} placeholder={t('accounts.catalogSearch')} /></label><label><span>{t('accounts.cardLanguage')}</span><select value={language} onChange={(event) => setLanguage(Number(event.target.value))}>{cardLanguages().map((value) => <option key={value.id} value={value.id}>{value.name}</option>)}</select></label><label><span>{t('accounts.expansion')}</span><select value={expansion} onChange={(event) => { setExpansion(event.target.value); setPage(1) }}><option value="">{t('accounts.all')}</option>{(catalog?.Expansions || []).map((value) => <option key={value.ID} value={value.ID}>{value.Name} · {value.ID}</option>)}</select></label><label><span>{t('accounts.rarity')}</span><select value={rarity} onChange={(event) => { setRarity(Number(event.target.value)); setPage(1) }}><option value="0">{t('accounts.all')}</option>{(catalog?.Rarities || []).map((value) => <option key={value} value={value}>{t('accounts.rarity')} {value}</option>)}</select></label></div>
	  {!catalog || !owned || !totals ? <CatalogSkeleton /> : <div className="ten-card-grid">{(catalog.Items || []).map((card) => { const current = owned.get(card.ID) || 0; const draft = drafts[card.ID] ?? current; const printedLanguage = cardLanguages().find((value) => value.id === language)?.name || ''; return <article className={current > 0 ? 'collection-card owned' : 'collection-card'} key={card.ID}><div className="collection-card-art"><Image src={card.ImageURL} alt="" />{current > 0 && <b>x{current}</b>}</div><span className="card-rarity">R{card.Rarity}</span><strong title={card.Name}>{card.Name}</strong><small>{card.Meta}</small><div className="card-stepper"><button disabled={busyID === card.ID || current === 0} onClick={() => void setQuantity(card, current - 1)} aria-label={t('accounts.removeCard', { language: localeLower(printedLanguage), name: card.Name })}><Minus size={13}/></button><NumberInput aria-label={t('accounts.quantityLanguage', { name: card.Name, language: printedLanguage })} min={0} max={999999} step={1} value={draft} onValueChange={(next) => { if (next !== null) setDrafts((value) => ({ ...value, [card.ID]: next })) }}/><button disabled={busyID === card.ID} onClick={() => void setQuantity(card, current + 1)} aria-label={t('accounts.addCard', { language: localeLower(printedLanguage), name: card.Name })}><Plus size={13}/></button></div><button className="apply-card-quantity" disabled={busyID === card.ID || draft === current} onClick={() => void setQuantity(card, draft)}>{busyID === card.ID ? '…' : t('common.set')}</button></article> })}</div>}
    {catalog && <Pagination page={catalog.Page} pages={catalog.TotalPages} setPage={setPage}/>}
  </div>
}

function PeripheralEditor({ group, profile, reportError }: { group: 'battle' | 'showcase'; profile: Profile; reportError: (value: string) => void }) {
  const [catalog, setCatalog] = useState<VisualCatalogItem[]>([]), [query, setQuery] = useState(''), [filter, setFilter] = useState(-1), [busyID, setBusyID] = useState('')
  const [owned, setOwned] = useState<Map<string, number> | null>(null)
  useEffect(() => { Promise.all([api.peripherals(group), api.inventory(profile.Player.ID, group)]).then(([definitions, amounts]) => { setCatalog(definitions); setOwned(new Map(amounts.map((value) => [value.ID, value.Quantity]))) }).catch((error: Error) => reportError(error.message)) }, [group, profile.Player.ID, reportError])
  const types = group === 'battle' ? [{ id: -1, name: t('accounts.allTypes') }, { id: 0, name: t('accounts.playmats') }, { id: 1, name: t('accounts.sleeves') }, { id: 2, name: t('accounts.coins') }] : [{ id: -1, name: t('accounts.allTypes') }, { id: 3, name: t('accounts.binders') }, { id: 4, name: t('accounts.displayBoards') }]
  const visible = catalog.filter((value) => (filter < 0 || value.Variant === filter) && (value.Name + value.ID).toLowerCase().includes(query.toLowerCase()))
  async function toggle(item: VisualCatalogItem) { if (!owned) return; setBusyID(item.ID); try { const amount = await api.updateStock(profile.Player.ID, 'items', item.ID, owned.has(item.ID) ? 0 : 1); setOwned((current) => { const next = new Map(current || []); if (amount.Quantity > 0) next.set(amount.ID, amount.Quantity); else next.delete(amount.ID); return next }) } catch (error) { reportError((error as Error).message) } finally { setBusyID('') } }
  return <div className="peripheral-editor"><div className="section-intro"><h3>{group === 'battle' ? t('accounts.battleCosmetics') : t('accounts.showcaseCosmetics')}</h3><span className="count-label">{t('accounts.itemCount', { count: visible.length })}</span></div><div className="visual-filterbar"><label className="catalog-search"><Search size={18}/><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t('common.search')} /></label><div className="choice-row">{types.map((value) => <button className={filter === value.id ? 'choice-chip selected' : 'choice-chip'} onClick={() => setFilter(value.id)} key={value.id}>{value.name}</button>)}</div></div>{!owned ? <InventorySkeleton rows={5} /> : <div className="peripheral-grid">{visible.map((item) => <article className={owned.has(item.ID) ? 'peripheral-card owned' : 'peripheral-card'} key={item.ID}><div><Image src={item.ImageURL} alt="" />{owned.has(item.ID) && <span><Check size={14}/> {t('accounts.owned')}</span>}</div><strong>{cleanName(item.Name)}</strong><small>{item.ID}</small><button disabled={busyID === item.ID} className={owned.has(item.ID) ? 'quiet-button' : 'secondary-button'} onClick={() => void toggle(item)}>{owned.has(item.ID) ? t('common.remove') : t('common.add')}</button></article>)}</div>}</div>
}

function Pagination({ page, pages, setPage }: { page: number; pages: number; setPage: (value: number) => void }) { return <nav className="catalog-pagination" aria-label={t('accounts.pagination')}><button disabled={page <= 1} onClick={() => setPage(page - 1)}><ChevronLeft/> {t('common.previous')}</button><span>{t('common.pageOf', { page, pages })}</span><button disabled={page >= pages} onClick={() => setPage(page + 1)}>{t('common.next')} <ChevronRight/></button></nav> }
function CatalogSkeleton() { return <div className="ten-card-grid">{Array.from({ length: 20 }, (_, index) => <span className="card-skeleton" key={index}/>)}</div> }
function InventorySkeleton({ rows }: { rows: number }) { return <div className="inventory-skeleton" aria-label={t('accounts.inventoryLoading')} aria-busy="true">{Array.from({ length: rows }, (_, index) => <span key={index}><i/><b/><em/></span>)}</div> }
function ProfileLoading({ player }: { player: PlayerSummary }) { return <div className="profile-loading" aria-label={t('accounts.loadingPlayer', { name: player.DisplayName })} aria-busy="true"><header className="profile-header"><div className="profile-identity"><span className="avatar large">{player.DisplayName.slice(0, 1).toUpperCase()}</span><div><h2>{player.DisplayName}</h2><p>{t('accounts.level', { level: player.Level })}</p></div></div><span className="loading-chip">{t('common.loading')}</span></header><div className="profile-loading-body"><span/><span/><span/></div></div> }

function CreateDialog({ close, created, reportError }: { close: () => void; created: (id: string) => void; reportError: (value: string) => void }) {
	const [name, setName] = useState(t('accounts.newPlayer')), [language, setLanguage] = useState(''), [country, setCountry] = useState(''), [level, setLevel] = useState(1), [experience, setExperience] = useState(0), [cardQuantity, setCardQuantity] = useState(2), [preset, setPreset] = useState<'empty' | 'complete'>('empty'), [busy, setBusy] = useState(false)
	function choose(value: 'empty' | 'complete') { setPreset(value); if (value === 'complete') { setLevel(60); setExperience(2755) } }
	async function submit(event: React.FormEvent) { event.preventDefault(); setBusy(true); try { const value = await api.createPlayer({ DisplayName: name, Language: language, Country: country.toUpperCase(), Level: level, Experience: experience, TutorialComplete: preset === 'complete', Preset: preset, CardQuantity: cardQuantity }) as { ID: string }; created(value.ID) } catch (error) { reportError((error as Error).message); setBusy(false) } }
	return <div className="dialog-backdrop" role="presentation" onMouseDown={close}><form className="dialog create-account-dialog" role="dialog" aria-modal="true" aria-labelledby="create-account-title" onMouseDown={(event) => event.stopPropagation()} onSubmit={submit}><div className="dialog-head"><h2 id="create-account-title">{t('accounts.create')}</h2><button type="button" className="icon-button" aria-label={t('common.close')} onClick={close}><X/></button></div><div className="preset-choice"><button type="button" className={preset === 'empty' ? 'selected' : ''} onClick={() => choose('empty')}><PackageOpen/><span><strong>{t('accounts.emptyAccount')}</strong><small>{t('accounts.emptyInventory')}</small></span>{preset === 'empty' && <Check/>}</button><button type="button" className={preset === 'complete' ? 'selected' : ''} onClick={() => choose('complete')}><Layers3/><span><strong>{t('accounts.godAccount')}</strong><small>{t('accounts.godInventory')}</small></span>{preset === 'complete' && <Check/>}</button></div>{preset === 'complete' && <label className="god-card-quantity"><span>{t('accounts.copiesPerLanguage')}</span><NumberInput required min={1} max={999999} step={1} value={cardQuantity} onValueChange={(value) => { if (value !== null) setCardQuantity(value) }}/></label>}<label><span>{t('accounts.nickname')}</span><input autoFocus maxLength={14} value={name} onChange={(event) => setName(event.target.value)}/></label><div className="form-grid two account-locale-fields"><label><span>{t('accounts.gameLanguage')}</span><select required value={language} onChange={(event) => setLanguage(event.target.value)}><option value="" disabled>{t('accounts.chooseLanguage')}</option>{accountLanguages().map((option) => <option key={option.locale} value={option.locale}>{option.name}</option>)}</select></label><label><span>{t('accounts.region')}</span><input required value={country} minLength={2} maxLength={2} pattern="[A-Za-z]{2}" autoCapitalize="characters" placeholder={t('accounts.regionPlaceholder')} onChange={(event) => setCountry(event.target.value.toUpperCase())}/><small>{t('accounts.regionHint')}</small></label></div><div className="form-grid two"><label><span>{t('accounts.level', { level: '' }).trim()}</span><NumberInput required min={1} max={60} step={1} value={level} onValueChange={(value) => { if (value !== null) setLevel(value) }}/></label><label><span>{t('accounts.experience')}</span><NumberInput required min={0} step={1} value={experience} onValueChange={(value) => { if (value !== null) setExperience(value) }}/></label></div><div className="dialog-actions"><button type="button" className="quiet-button" onClick={close}>{t('common.cancel')}</button><button className="primary-button" disabled={busy || !language || country.length !== 2}>{busy ? t('accounts.creating') : t('accounts.createAction')}</button></div></form></div>
}

function DeleteDialog({ player, busy, close, confirm }: { player: PlayerSummary; busy: boolean; close: () => void; confirm: () => void }) { return <div className="dialog-backdrop" role="presentation" onMouseDown={close}><div className="dialog delete-dialog" role="alertdialog" aria-modal="true" onMouseDown={(event) => event.stopPropagation()}><div className="danger-mark"><Trash2/></div><h2>{t('accounts.deleteQuestion', { name: player.DisplayName })}</h2><p>{t('accounts.deleteWarning')}</p><div className="dialog-actions"><button className="quiet-button" onClick={close}>{t('common.cancel')}</button><button className="danger-button" disabled={busy} onClick={confirm}>{busy ? t('accounts.deleting') : t('accounts.deleteForever')}</button></div></div></div> }

function cleanName(value: string) {
  return value.replace(/^(Icône|Tapis de Jeu|Protège-Carte|Pièce Pokémon|Couverture|Arrière-Plan|Icon|Playmat|Card Sleeve|Pokémon Coin|Binder|Display Board|Backdrop)\s*-\s*/i, '')
}
function Image({ src, alt }: { src: string; alt: string }) { return src ? <img src={src} alt={alt} loading="lazy" decoding="async" /> : <span className="image-placeholder"><WalletCards/></span> }
