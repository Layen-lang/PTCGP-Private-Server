import { useEffect, useMemo, useState } from 'react'
import type { ReactNode } from 'react'
import {
  Check, ChevronLeft, ChevronRight, Dices, Filter, Flame, Layers3, LockKeyhole,
  PackageOpen, RotateCcw, Search, ShieldCheck, Sparkles, Trash2,
} from 'lucide-react'
import { api } from '../api'
import NumberInput from '../components/NumberInput'
import type { CardPage, CatalogResult, IllegalPackRule, Pack, PackFilters, PackRule, PackStudio } from '../types'
import { localeLower, t } from '../i18n'

type Experience = 'standard' | 'illegal' | null
type StandardPath = 'official' | 'forced' | null
type ForcedContent = 'table' | 'rarity' | 'cards' | null
type WizardScreen = 'mode' | 'strategy' | 'illegal' | 'pack' | 'recipe' | 'detail' | 'cards' | 'review'

export default function PacksPage({ reportError }: { reportError: (message: string) => void }) {
  const [studio, setStudio] = useState<PackStudio | null>(null)
  const [rule, setRule] = useState<PackRule | null>(null)
  const [experience, setExperience] = useState<Experience>(null)
  const [standardPath, setStandardPath] = useState<StandardPath>(null)
  const [forcedContent, setForcedContent] = useState<ForcedContent>(null)
  const [screen, setScreen] = useState<WizardScreen>('mode')
  const [packQuery, setPackQuery] = useState('')
  const [cardQuery, setCardQuery] = useState('')
  const [cardRarity, setCardRarity] = useState(0)
  const [cardPage, setCardPage] = useState(1)
  const [catalog, setCatalog] = useState<CardPage | null>(null)
  const [knownCards, setKnownCards] = useState<Map<string, CatalogResult>>(new Map())
  const [poolCount, setPoolCount] = useState<number | null>(null)
  const [poolBusy, setPoolBusy] = useState(false)
  const [lastPackID, setLastPackID] = useState('')
  const [busy, setBusy] = useState(false)
  const [saved, setSaved] = useState(false)
  const illegalRule = rule?.Illegal

  useEffect(() => {
    api.packStudio().then((value) => {
      const normalized = normalizeRule(value.rule, value.packs)
      const hasLocks = normalized.Packs?.some((pack) => pack.some((slot) => slot.Locked && slot.CardID)) || false
      setStudio(value)
      setRule(normalized)
      setLastPackID(normalized.TargetPackID || value.packs[0]?.ID || '')
      if (normalized.Mode === 'illegal') {
        setExperience('illegal')
      } else {
        setExperience('standard')
        setStandardPath(normalized.Mode === 'official' ? 'official' : 'forced')
        setForcedContent(normalized.Mode === 'rarity' ? 'rarity' : hasLocks ? 'cards' : normalized.Mode === 'table' ? 'table' : null)
      }
    }).catch((error: Error) => reportError(error.message))
  }, [reportError])

  useEffect(() => {
    if (!studio || screen === 'illegal') return
    const behavior = window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth'
    document.querySelector('.packs-page')?.scrollIntoView({ behavior, block: 'start' })
  }, [screen, studio])

  const needsCards = screen === 'cards'
  const selectedPack = studio?.packs.find((value) => value.ID === (rule?.TargetPackID || lastPackID)) || studio?.packs[0]
  const catalogExpansion = selectedPack?.ExpansionID || ''
  const availableTableKinds = allPackValues(studio?.packs || [], (pack) => pack.TableKinds)
  const availableRarities = allPackValues(studio?.packs || [], (pack) => pack.Rarities)

  useEffect(() => {
    if (!needsCards) return
    const timer = window.setTimeout(() => {
      api.cardPage(cardQuery, catalogExpansion, cardRarity, cardPage)
        .then((value) => {
          setCatalog(value)
          setKnownCards((current) => {
            const next = new Map(current)
            for (const card of value.Items || []) next.set(card.ID, card)
            return next
          })
        })
        .catch((error: Error) => reportError(error.message))
    }, 180)
    return () => window.clearTimeout(timer)
  }, [cardPage, cardQuery, cardRarity, catalogExpansion, needsCards, reportError])

  useEffect(() => {
    if (experience !== 'illegal' || !illegalRule) return
    let cancelled = false
    setPoolBusy(true)
    const timer = window.setTimeout(() => {
      api.illegalPackPool(illegalRule)
        .then((value) => { if (!cancelled) setPoolCount(value.Count) })
        .catch((error: Error) => { if (!cancelled) reportError(error.message) })
        .finally(() => { if (!cancelled) setPoolBusy(false) })
    }, 180)
    return () => {
      cancelled = true
      window.clearTimeout(timer)
    }
  }, [experience, illegalRule, reportError])

  const selectedCardIDs = useMemo(() => Array.from(new Set((rule?.Packs || []).flat().map((slot) => slot.CardID).filter(Boolean))), [rule?.Packs])
  useEffect(() => {
    const missing = selectedCardIDs.filter((id) => !knownCards.has(id))
    if (missing.length === 0) return
    let cancelled = false
    Promise.all(missing.map((id) => api.cards(id).then((values) => values.find((card) => card.ID === id))))
      .then((values) => {
        if (cancelled) return
        setKnownCards((current) => {
          const next = new Map(current)
          for (const card of values) if (card) next.set(card.ID, card)
          return next
        })
      })
      .catch(() => undefined)
    return () => { cancelled = true }
  }, [knownCards, selectedCardIDs])

  if (!studio || !rule || !selectedPack) return <div className="page-loading">{t('packs.loading')}</div>
  const activeStudio = studio
  const activeRule = rule
  const activePack = selectedPack

  const visiblePacks = studio.packs.filter((pack) => localeLower(pack.Name + pack.ExpansionID + pack.ID).includes(localeLower(packQuery)))
  const slots = rule.Packs?.[0] || []
  const customCardsComplete = slots.length === 5
  const canSave = experience === 'standard'
    ? standardPath === 'official' || (standardPath === 'forced' && forcedContent !== null && (forcedContent !== 'cards' || customCardsComplete))
    : experience === 'illegal' && poolCount !== null && poolCount > 0 && rule.ReturnPackCount >= 1 && rule.ReturnPackCount <= 10 && rule.Illegal.CardCount >= 1 && rule.Illegal.CardCount <= 11 && (rule.Illegal.AllowDuplicates || rule.Illegal.CardCount <= poolCount)
  const previewKind = forcedContent === 'cards'
    ? 'cards'
    : standardPath === 'official'
      ? 'official'
      : forcedContent || 'empty'

  function updateRule(recipe: (current: PackRule) => PackRule) {
    setSaved(false)
    setRule((current) => current ? recipe(current) : current)
  }

  function change<K extends keyof PackRule>(key: K, value: PackRule[K]) {
    updateRule((current) => ({ ...current, [key]: value }))
  }

  function changeIllegal(patch: Partial<IllegalPackRule>) {
    setPoolCount(null)
    updateRule((current) => ({ ...current, Illegal: { ...current.Illegal, ...patch } }))
  }

  function chooseExperience(value: Exclude<Experience, null>) {
    if (value === experience) {
      setScreen(value === 'standard' ? 'strategy' : 'illegal')
      return
    }
    setExperience(value)
    setCatalog(null)
    if (value === 'standard') {
      setStandardPath(null)
      setForcedContent(null)
      updateRule((current) => ({ ...current, Mode: 'official', TargetPackID: '', ReturnPackCount: 1, Packs: [[]] }))
      setScreen('strategy')
    } else {
      setStandardPath(null)
      setForcedContent(null)
      setPoolCount(null)
      updateRule((current) => ({ ...current, Mode: 'illegal', TargetPackID: '', ReturnPackCount: Math.min(10, Math.max(1, current.ReturnPackCount)), Illegal: { ...current.Illegal, CardCount: Math.min(11, Math.max(1, current.Illegal.CardCount)) }, Packs: [[]], FreeOpenings: true }))
      setScreen('illegal')
    }
  }

  function chooseStandardPath(value: Exclude<StandardPath, null>) {
    if (value === standardPath) {
      setScreen(value === 'official' ? 'review' : 'recipe')
      return
    }
    setStandardPath(value)
    setForcedContent(null)
    updateRule((current) => value === 'official'
      ? { ...current, Mode: 'official', TargetPackID: '', ReturnPackCount: 1, Packs: [[]] }
      : { ...current, Mode: 'table', TargetPackID: '', TableKind: availableTableKinds[0] || 'normal', ReturnPackCount: 1, Packs: [[]] })
    setScreen(value === 'official' ? 'review' : 'recipe')
  }

  function choosePack(pack: Pack) {
    setLastPackID(pack.ID)
    setCardPage(1)
    setCatalog(null)
    updateRule((current) => ({
      ...current,
      TargetPackID: pack.ID,
      TableKind: pack.TableKinds.includes(current.TableKind) ? current.TableKind : pack.TableKinds[0] || 'normal',
      Rarity: pack.Rarities.includes(current.Rarity) ? current.Rarity : pack.Rarities[0] || 'R',
      Packs: forcedContent === 'cards' ? [[]] : current.Packs,
    }))
    setScreen('cards')
  }

  function chooseForcedContent(value: Exclude<ForcedContent, null>) {
    if (value === forcedContent) {
      setScreen(value === 'cards' ? 'pack' : 'detail')
      return
    }
    setForcedContent(value)
    setCardPage(1)
    setCatalog(null)
    updateRule((current) => ({
      ...current,
      Mode: value === 'rarity' ? 'rarity' : 'table',
      TargetPackID: value === 'cards' ? activePack.ID : '',
      TableKind: value === 'cards'
        ? activePack.TableKinds.includes(current.TableKind) ? current.TableKind : activePack.TableKinds[0] || 'normal'
        : availableTableKinds.includes(current.TableKind) ? current.TableKind : availableTableKinds[0] || 'normal',
      Rarity: availableRarities.includes(current.Rarity) ? current.Rarity : availableRarities[0] || 'R',
      Packs: [[]],
    }))
    setScreen(value === 'cards' ? 'pack' : 'detail')
  }

  function addCard(card: CatalogResult) {
    if (slots.length >= 5) return
    const completesCurrent = slots.length === 4
    setKnownCards((current) => new Map(current).set(card.ID, card))
    updateRule((current) => {
      const packs = (current.Packs?.length ? current.Packs : [[]]).map((pack) => [...pack])
      packs[0].push({ CardID: card.ID, Locked: true })
      return { ...current, Packs: packs }
    })
    if (completesCurrent) {
      setScreen('review')
    }
  }

  function removeCard(index: number) {
    updateRule((current) => {
      const packs = (current.Packs?.length ? current.Packs : [[]]).map((pack) => [...pack])
      packs[0].splice(index, 1)
      return { ...current, Packs: packs }
    })
  }

  async function save() {
    if (!canSave) return
    setBusy(true)
    try {
      const value = await api.savePackRule(activeRule)
      setRule(normalizeRule(value, activeStudio.packs))
      setSaved(true)
    } catch (error) {
      reportError((error as Error).message)
    } finally {
      setBusy(false)
    }
  }

  function goBack() {
    const previous: Partial<Record<WizardScreen, WizardScreen>> = {
      strategy: 'mode', illegal: 'mode', recipe: 'strategy', detail: 'recipe', pack: 'recipe', cards: 'pack',
      review: standardPath === 'official' ? 'strategy' : forcedContent === 'cards' ? 'cards' : 'detail',
    }
    setScreen(previous[screen] || 'mode')
  }

  function restartWizard() {
    setScreen('mode')
  }

  const journey = screen === 'strategy'
    ? wizardJourney(experience, 'forced', forcedContent)
    : wizardJourney(experience, standardPath, forcedContent)
  const journeyIndex = Math.max(0, journey.indexOf(screen))
  const stepNumber = String(journeyIndex + 1)
  const progress = ((journeyIndex + 1) / journey.length) * 100

  const previewTitle = experience === 'illegal'
    ? t('packs.illegalTitle')
    : standardPath === 'official' ? t('packs.allPacks') : forcedContent === 'cards' ? selectedPack.Name : t('packs.allPacks')
  const previewDetail = experience === 'illegal'
    ? t('packs.previewIllegal', { packs: rule.ReturnPackCount, packsPlural: rule.ReturnPackCount > 1 ? 's' : '', cards: rule.Illegal.CardCount, cardsPlural: rule.Illegal.CardCount > 1 ? 's' : '' })
    : completionText(experience, standardPath, forcedContent, slots.length)

  return <div className="page packs-page">
    <section className="page-heading pack-heading">
      <div><span className="kicker">{t('packs.ruleAllAccounts')}</span><h1>{t('packs.title')}</h1><p>{t('packs.headingHint')}</p></div>
      <span className={`armed-status ${saved ? 'saved' : ''}`}><span/><strong>{saved ? t('packs.armed') : t('packs.draft')}</strong></span>
    </section>

    <div className="booster-wizard-layout">
      <main className="wizard-flow">
        <nav className={`wizard-page-nav ${screen === 'illegal' ? 'freeform' : ''}`} aria-label={t('packs.progress')}>
          <div className="wizard-nav-actions">
            <button disabled={screen === 'mode'} onClick={goBack}><ChevronLeft/> {t('packs.back')}</button>
            <button disabled={screen === 'mode'} onClick={restartWizard}><RotateCcw/> {t('packs.start')}</button>
          </div>
          <span><small>{screen === 'illegal' ? t('packs.freeform') : t('packs.guided')}</small><strong>{wizardLabel(screen)}</strong></span>
          {screen !== 'illegal' && <><span className="wizard-progress-track" aria-label={t('packs.stepOf', { step: stepNumber, total: journey.length })}><i style={{ width: `${progress}%` }}/></span><b>{stepNumber}/{journey.length}</b></>}
        </nav>

        {screen === 'mode' && <WizardStep key={screen} number={stepNumber} title={t('packs.chooseMode')} hint={t('packs.chooseModeHint')}>
          <div className="experience-choices">
            <button className={`experience-choice standard ${experience === 'standard' ? 'selected' : ''}`} onClick={() => chooseExperience('standard')}>
              <span className="choice-icon"><ShieldCheck/></span>
              <span><strong>{t('packs.normalMode')}</strong><small>{t('packs.normalDetail')}</small></span>
            </button>
            <button className={`experience-choice illegal ${experience === 'illegal' ? 'selected' : ''}`} onClick={() => chooseExperience('illegal')}>
              <span className="choice-icon"><Flame/></span>
              <span><strong>{t('packs.illegalMode')}</strong><small>{t('packs.illegalDetail')}</small></span>
            </button>
          </div>
        </WizardStep>}

        {screen === 'strategy' && <WizardStep key={screen} number={stepNumber} title={t('packs.strategyTitle')} hint={t('packs.strategyHint')}>
          <div className="binary-choices">
            <ChoiceButton active={standardPath === 'official'} icon={<Dices/>} title={t('packs.realOdds')} detail={t('packs.realOddsHint')} onClick={() => chooseStandardPath('official')}/>
            <ChoiceButton active={standardPath === 'forced'} icon={<Sparkles/>} title={t('packs.forceResult')} detail={t('packs.forceResultHint')} onClick={() => chooseStandardPath('forced')}/>
          </div>
        </WizardStep>}

        {screen === 'pack' && <WizardStep key={screen} number={stepNumber} title={t('packs.pickPackTitle')} hint={t('packs.pickPackHint')}>
          <PackPicker packs={visiblePacks} selected={selectedPack} query={packQuery} setQuery={setPackQuery} choose={choosePack}/>
        </WizardStep>}

        {screen === 'recipe' && <WizardStep key={screen} number={stepNumber} title={t('packs.recipeTitle')} hint={t('packs.recipeHint')}>
          <div className="recipe-choices">
            <ChoiceButton active={forcedContent === 'table'} icon={<Layers3/>} title={t('packs.oneTable')} detail={t('packs.oneTableHint')} onClick={() => chooseForcedContent('table')}/>
            <ChoiceButton active={forcedContent === 'rarity'} icon={<Dices/>} title={t('packs.oneRarity')} detail={t('packs.oneRarityHint')} onClick={() => chooseForcedContent('rarity')}/>
            <ChoiceButton active={forcedContent === 'cards'} icon={<LockKeyhole/>} title={t('packs.fiveCards')} detail={t('packs.fiveCardsHint')} onClick={() => chooseForcedContent('cards')}/>
          </div>
        </WizardStep>}

        {screen === 'detail' && forcedContent === 'table' && <WizardStep key={screen} number={stepNumber} title={t('packs.tableQuestion')} hint={t('packs.forceFallbackHint')}>
          <div className="large-chip-grid">{availableTableKinds.map((value) => <Chip key={value} active={rule.TableKind === value} onClick={() => { change('TableKind', value); setScreen('review') }}>{tableLabel(value)}</Chip>)}</div>
        </WizardStep>}

        {screen === 'detail' && forcedContent === 'rarity' && <WizardStep key={screen} number={stepNumber} title={t('packs.rarityQuestion')} hint={t('packs.forceFallbackHint')}>
          <div className="large-chip-grid">{availableRarities.map((value) => <Chip key={value} active={rule.Rarity === value} onClick={() => { change('Rarity', value); setScreen('review') }}>{value}</Chip>)}</div>
        </WizardStep>}

        {screen === 'illegal' && <IllegalPanel
          rule={rule}
          filters={studio.filters}
          poolCount={poolCount}
          poolBusy={poolBusy}
          canSave={canSave}
          busy={busy}
          saved={saved}
          change={change}
          changeIllegal={changeIllegal}
          save={() => void save()}
        />}

        {screen === 'cards' && <WizardStep key={screen} number={stepNumber} title={t('packs.addFive')} hint={t('packs.catalogLimited', { expansion: selectedPack.ExpansionID })}>
          <CardLibrary catalog={catalog} query={cardQuery} setQuery={(value) => { setCardQuery(value); setCardPage(1) }} expansion={catalogExpansion} setExpansion={() => undefined} rarity={cardRarity} setRarity={(value) => { setCardRarity(value); setCardPage(1) }} page={cardPage} setPage={setCardPage} selected={slots} add={addCard} lockedExpansion={selectedPack.ExpansionID}/>
          {customCardsComplete && <div className="wizard-actions"><span><Check/> {t('packs.compositionsReady')}</span><button onClick={() => setScreen('review')}>{t('packs.reviewRule')} <ChevronRight/></button></div>}
        </WizardStep>}

        {screen === 'review' && <WizardStep key={screen} number={stepNumber} title={t('packs.reviewTitle')} hint={t('packs.reviewHint')}>
          <div className="ready-message"><span><Check/></span><div><strong>{previewDetail}</strong><p>{previewTitle} {t('packs.normalSuffix')}</p></div></div>
          <OptionalSettings rule={rule} change={change}/>
          <div className="review-actions"><button className="arm-rule" disabled={!canSave || busy} onClick={() => void save()}>{busy ? t('packs.arming') : saved ? <><Check/> {t('packs.armed')}</> : t('packs.arm')}</button></div>
        </WizardStep>}
      </main>

      <aside className="studio-preview" aria-label={t('packs.previewLabel')}>
        <div className="preview-head"><div><span>{t('packs.preview')}</span><h2>{previewTitle}</h2></div>{experience === 'illegal' ? <Flame/> : <ShieldCheck/>}</div>
        {experience === 'illegal'
          ? <IllegalPreview rule={rule} poolCount={poolCount} poolBusy={poolBusy}/>
          : <PackPreview kind={previewKind} slots={slots} cards={knownCards} rarity={rule.Rarity} table={tableLabel(rule.TableKind)} remove={previewKind === 'cards' ? removeCard : undefined}/>}
        <div className="preview-summary"><strong>{previewDetail}</strong><span>{experience === 'illegal' ? t('packs.illegalActive') : forcedContent === 'cards' ? t('packs.otherPacksOfficial') : t('packs.ruleShared')}</span></div>
        <p className="preview-help">{screen === 'review' ? t('packs.validationFinal') : t('packs.continueLeft')}</p>
      </aside>
    </div>
  </div>
}

function WizardStep({ number, title, hint, children }: { number: string; title: string; hint: string; children: ReactNode }) {
  return <section className="wizard-step"><header><span className="step-number">{number}</span><div><h2>{title}</h2><p>{hint}</p></div></header><div className="step-content">{children}</div></section>
}

function ChoiceButton({ active, icon, title, detail, onClick }: { active: boolean; icon: ReactNode; title: string; detail: string; onClick: () => void }) {
  return <button className={`choice-button ${active ? 'selected' : ''}`} onClick={onClick}><span className="choice-icon">{icon}</span><span><strong>{title}</strong><small>{detail}</small></span><i>{active ? <Check/> : <ChevronRight/>}</i></button>
}

function PackPicker({ packs, selected, query, setQuery, choose }: { packs: Pack[]; selected: Pack; query: string; setQuery: (value: string) => void; choose: (pack: Pack) => void }) {
  return <div className="pack-picker"><label className="catalog-search pack-search"><Search/><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t('packs.searchPack')}/></label><div className="pack-choice-strip">{packs.map((pack) => <button key={pack.ID} className={pack.ID === selected.ID ? 'selected' : ''} onClick={() => choose(pack)}><span className="pack-choice-art">{pack.ImageURL ? <img src={pack.ImageURL} alt="" loading="lazy" decoding="async"/> : <PackageOpen/>}</span><span><strong>{pack.Name}</strong><small>{pack.ExpansionID}</small></span>{pack.ID === selected.ID && <i><Check/></i>}</button>)}</div></div>
}

function Chip({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return <button className={`choice-chip ${active ? 'selected' : ''}`} onClick={onClick}>{children}{active && <Check/>}</button>
}

function IllegalPanel({ rule, filters, poolCount, poolBusy, canSave, busy, saved, change, changeIllegal, save }: {
  rule: PackRule
  filters: PackFilters
  poolCount: number | null
  poolBusy: boolean
  canSave: boolean
  busy: boolean
  saved: boolean
  change: <K extends keyof PackRule>(key: K, value: PackRule[K]) => void
  changeIllegal: (patch: Partial<IllegalPackRule>) => void
  save: () => void
}) {
  const illegal = rule.Illegal
  const expansions = illegal.ExpansionIDs || []
  const rarities = illegal.Rarities || []
  const cardKinds = illegal.CardKinds || []
  const exceedsPool = !illegal.AllowDuplicates && poolCount !== null && illegal.CardCount > poolCount
  const poolMessage = poolBusy || poolCount === null
    ? t('packs.poolCalculating')
    : poolCount === 0
      ? t('packs.poolEmpty')
      : exceedsPool
        ? t('packs.poolTooSmall', { count: poolCount })
        : t('packs.poolAvailable', { count: poolCount, plural: poolCount > 1 ? 's' : '' })

  return <section className="wizard-step illegal-control-panel">
    <header><span className="step-number"><Flame/></span><div><h2>{t('packs.illegalTitle')}</h2><p>{t('packs.illegalIntro')}</p></div></header>
    <div className="step-content">
      <div className="illegal-primary-controls">
        <label className="illegal-number-field"><span>{t('packs.tenOpenings')}</span><NumberInput min={1} max={10} step={1} inputMode="numeric" value={rule.ReturnPackCount} onValueChange={(value) => { if (value !== null) change('ReturnPackCount', value) }}/><small>{t('packs.tenOpeningHint')}</small></label>
        <label className="illegal-number-field"><span>{t('packs.cardsPerPack')}</span><NumberInput min={1} max={11} step={1} inputMode="numeric" value={illegal.CardCount} onValueChange={(value) => { if (value !== null) changeIllegal({ CardCount: value }) }}/><small>{t('packs.cardsPerPackHint')}</small></label>
      </div>

      <label className="switch-row illegal-duplicates"><input type="checkbox" checked={illegal.AllowDuplicates} onChange={(event) => changeIllegal({ AllowDuplicates: event.target.checked })}/><span className="switch"/><span><strong>{t('packs.allowDuplicates')}</strong><small>{t('packs.duplicatesHint')}</small></span></label>

      <section className="illegal-filter-section">
        <div className="illegal-filter-title"><span><Filter/></span><div><strong>{t('packs.catalogFilters')}</strong><small>{t('packs.filterHint')}</small></div></div>
        <FilterGroup title={t('packs.expansions')} selectedCount={expansions.length} clear={() => changeIllegal({ ExpansionIDs: [] })} scroll>
          {(filters.Expansions || []).map((value) => <Chip key={value.ID} active={expansions.includes(value.ID)} onClick={() => changeIllegal({ ExpansionIDs: toggleValue(expansions, value.ID) })}>{value.Name || value.ID}</Chip>)}
        </FilterGroup>
        <FilterGroup title={t('packs.rarities')} selectedCount={rarities.length} clear={() => changeIllegal({ Rarities: [] })}>
          {(filters.Rarities || []).map((value) => <Chip key={value.Value} active={rarities.includes(value.Value)} onClick={() => changeIllegal({ Rarities: toggleValue(rarities, value.Value) })}>{value.Label}</Chip>)}
        </FilterGroup>
        <FilterGroup title={t('packs.cardTypes')} selectedCount={cardKinds.length} clear={() => changeIllegal({ CardKinds: [] })}>
          {(filters.CardKinds || []).map((value) => <Chip key={value} active={cardKinds.includes(value)} onClick={() => changeIllegal({ CardKinds: toggleValue(cardKinds, value) })}>{cardKindLabel(value)}</Chip>)}
        </FilterGroup>
      </section>

      <div className={`illegal-pool-status ${poolCount === 0 || exceedsPool ? 'error' : ''}`}><span>{poolBusy ? <RotateCcw/> : <Layers3/>}</span><div><strong>{t('packs.globalPool')}</strong><small>{poolMessage}</small></div></div>
      <OptionalSettings rule={rule} change={change}/>
      <div className="review-actions"><button className="arm-rule" disabled={!canSave || busy || poolBusy} onClick={save}>{busy ? t('packs.arming') : saved ? <><Check/> {t('packs.armed')}</> : t('packs.armIllegal')}</button></div>
    </div>
  </section>
}

function FilterGroup({ title, selectedCount, clear, scroll = false, children }: { title: string; selectedCount: number; clear: () => void; scroll?: boolean; children: ReactNode }) {
  return <div className="illegal-filter-group"><div className="illegal-filter-head"><span><strong>{title}</strong><small>{selectedCount === 0 ? t('packs.allowAll') : t('packs.selected', { count: selectedCount, suffix: selectedCount > 1 ? 's' : '' })}</small></span>{selectedCount > 0 && <button onClick={clear}>{t('packs.clearAll')}</button>}</div><div className={`illegal-filter-options ${scroll ? 'scroll' : ''}`}>{children}</div></div>
}

function IllegalPreview({ rule, poolCount, poolBusy }: { rule: PackRule; poolCount: number | null; poolBusy: boolean }) {
  const total = rule.ReturnPackCount > 0 && rule.Illegal.CardCount > 0 ? rule.ReturnPackCount * rule.Illegal.CardCount : 0
  return <div className="illegal-preview">
    <div className="illegal-preview-mark"><Flame/><span>{t('packs.globalRecipe')}</span></div>
    <dl><div><dt>{t('packs.openingOne')}</dt><dd>1</dd></div><div><dt>{t('packs.openingTen')}</dt><dd>{rule.ReturnPackCount || '—'}</dd></div><div><dt>{t('packs.cardsEach')}</dt><dd>{rule.Illegal.CardCount || '—'}</dd></div><div><dt>{t('packs.cardsTen')}</dt><dd>{total || '—'}</dd></div><div><dt>{t('packs.pool')}</dt><dd>{poolBusy || poolCount === null ? '…' : poolCount}</dd></div></dl>
    <p>{t('packs.illegalRecipeHint')}</p>
  </div>
}

function OptionalSettings({ rule, change }: { rule: PackRule; change: <K extends keyof PackRule>(key: K, value: PackRule[K]) => void }) {
  return <div className="booster-options"><label className="switch-row"><input type="checkbox" checked={rule.FreeOpenings} onChange={(event) => change('FreeOpenings', event.target.checked)}/><span className="switch"/><span><strong>{t('packs.freeOpenings')}</strong><small>{t('packs.freeHint')}</small></span></label><label className="seed-field"><span>{t('packs.fixedSeed')} <small>{t('packs.optional')}</small></span><NumberInput allowEmpty step={1} value={rule.FixedSeed ?? null} placeholder={t('packs.newSeed')} onValueChange={(value) => change('FixedSeed', value)}/><small>{t('packs.seedHint')}</small></label></div>
}

function CardLibrary({ catalog, query, setQuery, expansion, setExpansion, rarity, setRarity, page, setPage, selected, add, lockedExpansion }: {
  catalog: CardPage | null
  query: string
  setQuery: (value: string) => void
  expansion: string
  setExpansion: (value: string) => void
  rarity: number
  setRarity: (value: number) => void
  page: number
  setPage: (value: number) => void
  selected: { CardID: string }[]
  add: (card: CatalogResult) => void
  lockedExpansion?: string
}) {
  return <div className="booster-card-library">
    <div className="booster-card-toolbar">
      <label className="catalog-search"><Search/><input value={query} onChange={(event) => setQuery(event.target.value)} placeholder={t('packs.searchCard')}/></label>
      <label><span>{t('packs.expansion')}</span><select value={expansion} disabled={Boolean(lockedExpansion)} onChange={(event) => setExpansion(event.target.value)}><option value="">{t('packs.allExpansions')}</option>{(catalog?.Expansions || []).map((value) => <option key={value.ID} value={value.ID}>{value.Name} · {value.ID}</option>)}</select></label>
      <label><span>{t('packs.rarity')}</span><select value={rarity} onChange={(event) => setRarity(Number(event.target.value))}><option value="0">{t('packs.allRarities')}</option>{(catalog?.Rarities || []).map((value) => <option key={value} value={value}>{t('packs.rarity')} {value}</option>)}</select></label>
      <span className="selection-count"><strong>{selected.length}/5</strong><small>{t('packs.cardsChosen')}</small></span>
    </div>
    {!catalog ? <div className="booster-card-skeleton">{Array.from({ length: 15 }, (_, index) => <span key={index}/>)}</div> : <>
      <div className="booster-card-grid">{(catalog.Items || []).map((card) => {
        const count = selected.filter((slot) => slot.CardID === card.ID).length
        return <button key={card.ID} className={count > 0 ? 'selected' : ''} disabled={selected.length >= 5} onClick={() => add(card)} aria-label={t('packs.addCard', { name: card.Name })}><span className="library-card-art">{card.ImageURL ? <img src={card.ImageURL} alt="" loading="lazy" decoding="async"/> : <LockKeyhole/>}{count > 0 && <b>x{count}</b>}</span><strong title={card.Name}>{card.Name}</strong><small>{card.Meta}</small><i>R{card.Rarity}</i></button>
      })}</div>
      <nav className="booster-pagination" aria-label={t('packs.pagination')}><button disabled={page <= 1} onClick={() => setPage(page - 1)}><ChevronLeft/> {t('common.previous')}</button><span>{t('packs.pageSummary', { page: catalog.Page, pages: catalog.TotalPages, count: catalog.Total })}</span><button disabled={page >= catalog.TotalPages} onClick={() => setPage(page + 1)}>{t('common.next')} <ChevronRight/></button></nav>
    </>}
  </div>
}

function PackPreview({ kind, slots, cards, rarity, table, remove }: { kind: string; slots: { CardID: string }[]; cards: Map<string, CatalogResult>; rarity: string; table: string; remove?: (index: number) => void }) {
  const indexes = [0, 1, 2, 3, 4]
  const card = (index: number) => {
    const slot = slots[index]
    const definition = slot ? cards.get(slot.CardID) : undefined
    const placeholder = kind === 'official' ? t('packs.probability') : kind === 'table' ? table : kind === 'rarity' ? rarity : t('packs.card')
    return <button key={index} className={`preview-card ${slot ? 'filled' : ''}`} disabled={!slot || !remove} onClick={() => slot && remove?.(index)} aria-label={slot && remove ? t('packs.removeCard', { name: definition?.Name || slot.CardID }) : t('packs.slot', { position: index + 1 })}>
      {slot && definition?.ImageURL ? <img src={definition.ImageURL} alt={definition.Name}/> : <span><LockKeyhole/><strong>{slot ? definition?.Name || t('packs.forcedCard') : placeholder}</strong></span>}
      <i>{index + 1}</i>{slot && remove && <b><Trash2/></b>}
    </button>
  }
  return <div className="pack-preview"><div className="preview-card-row top">{indexes.slice(0, 3).map(card)}</div><div className="preview-card-row bottom">{indexes.slice(3).map(card)}</div></div>
}

function normalizeRule(rule: PackRule, packs: Pack[]): PackRule {
  const outputs = rule.Packs?.length ? rule.Packs.map((pack) => Array.isArray(pack) ? pack : []) : [[]]
  const hasLocks = outputs.some((pack) => pack.some((slot) => slot.Locked && slot.CardID))
  const targetPackID = rule.Mode !== 'illegal' && hasLocks ? rule.TargetPackID : ''
  return {
    ...rule,
    TargetPackID: targetPackID,
    TableKind: rule.TableKind || packs[0]?.TableKinds[0] || 'normal',
    Rarity: rule.Rarity || packs[0]?.Rarities[0] || 'R',
    ReturnPackCount: Math.min(10, Math.max(1, rule.ReturnPackCount || 1)),
    Packs: outputs,
    Illegal: {
      CardCount: Math.min(11, Math.max(1, rule.Illegal?.CardCount || 5)),
      AllowDuplicates: Boolean(rule.Illegal?.AllowDuplicates),
      ExpansionIDs: rule.Illegal?.ExpansionIDs || [],
      Rarities: rule.Illegal?.Rarities || [],
      CardKinds: rule.Illegal?.CardKinds || [],
    },
  }
}

function allPackValues(packs: Pack[], select: (pack: Pack) => string[]) {
  return Array.from(new Set(packs.flatMap(select)))
}

function tableLabel(value: string) {
  return ({ normal: t('packs.normalTable'), rare: t('packs.rareTable'), plus1: t('packs.bonusCard'), guarantee: t('packs.guaranteed'), theme_rare: t('packs.themeRare') } as Record<string, string>)[value] || value
}

function wizardJourney(experience: Experience, path: StandardPath, content: ForcedContent): WizardScreen[] {
  if (experience === 'illegal') return ['mode', 'illegal']
  if (experience === 'standard') {
    if (path === 'official') return ['mode', 'strategy', 'review']
    if (path === 'forced') {
      const journey: WizardScreen[] = ['mode', 'strategy', 'recipe']
      if (content === 'cards') journey.push('pack', 'cards', 'review')
      else if (content) journey.push('detail', 'review')
      return journey
    }
    return ['mode', 'strategy']
  }
  return ['mode']
}

function wizardLabel(screen: WizardScreen) {
  return ({ mode: t('packs.mode'), strategy: t('packs.drawType'), illegal: t('packs.illegalPanel'), pack: t('packs.pack'), recipe: t('packs.composition'), detail: t('packs.setting'), cards: t('packs.cards'), review: t('packs.activation') } as Record<WizardScreen, string>)[screen]
}

function completionText(experience: Experience, path: StandardPath, content: ForcedContent, slotCount: number) {
  if (!experience) return t('packs.startChoice')
  if (!path) return t('packs.force')
  if (path === 'official') return t('packs.official')
  if (content === 'table') return t('packs.tableForced')
  if (content === 'rarity') return t('packs.rarityForced')
  if (content === 'cards') return slotCount === 5 ? t('packs.compositionComplete') : t('packs.cardsRemaining', { count: 5 - slotCount, plural: 5 - slotCount > 1 ? 's' : '' })
  return t('packs.continue')
}

function toggleValue<T>(values: T[], value: T) {
  return values.includes(value) ? values.filter((current) => current !== value) : [...values, value]
}

function cardKindLabel(value: string) {
  return ({ pokemon: t('packs.pokemon'), trainer: t('packs.trainer') } as Record<string, string>)[value.toLowerCase()] || value
}
