// Package packlab owns pack rules, draws, economy, and durable opening history.
package packlab

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	mathrand "math/rand"
	"sort"
	"strings"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
)

var ErrInvalidRule = errors.New("invalid pack rule")

const (
	shineDustPerReturnedPack  = 20
	maxIllegalReturnedPacks   = 10
	maxIllegalCardsPerBooster = 11
)

type OpenInput struct {
	PlayerID, TransactionID, ProductID string
	RequestedCount                     int
	Share                              bool
}

type PreviewInput struct {
	PlayerID, PackID string
	RequestedCount   int
}

type RarityOption struct {
	Value int
	Label string
}

// Engine is the deep module for all local booster behavior.
type Engine struct {
	state              *store.Store
	catalog            *catalog.Catalog
	rarityCodesByLabel map[string]map[int]struct{}
}

func New(state *store.Store, master *catalog.Catalog) *Engine {
	return &Engine{state: state, catalog: master, rarityCodesByLabel: catalogRarityCodes(master)}
}

func (e *Engine) Rule(ctx context.Context) (store.PackRule, error) {
	return e.state.GlobalPackRule(ctx)
}

func (e *Engine) SaveRule(ctx context.Context, rule store.PackRule) error {
	if rule.Mode == "illegal" {
		rule.TargetPackID = ""
		rule.Packs = nil
	}
	if err := e.validateRule(rule); err != nil {
		return err
	}
	return e.state.SaveGlobalPackRule(ctx, rule)
}

func (e *Engine) IllegalPoolSize(rule store.IllegalPackRule) int {
	return len(e.illegalCardPool(rule))
}

func (e *Engine) IllegalRarities() []RarityOption {
	labels := make(map[int]string)
	for label, codes := range e.rarityCodesByLabel {
		for code := range codes {
			labels[code] = label
		}
	}
	seen := make(map[int]bool)
	for _, card := range e.catalog.Cards() {
		seen[card.Rarity] = true
	}
	result := make([]RarityOption, 0, len(seen))
	for value := range seen {
		label := labels[value]
		if label == "" {
			label = fmt.Sprintf("R%d", value)
		}
		result = append(result, RarityOption{Value: value, Label: label})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Value < result[j].Value })
	return result
}

func (e *Engine) Preview(ctx context.Context, input PreviewInput) (store.PackOpening, error) {
	pack, err := e.catalog.Pack(input.PackID)
	if err != nil {
		return store.PackOpening{}, err
	}
	rule, err := e.state.GlobalPackRule(ctx)
	if err != nil {
		return store.PackOpening{}, err
	}
	seed, err := seedFor(rule)
	if err != nil {
		return store.PackOpening{}, err
	}
	packs, err := e.draw(pack, rule, normalizedRequestedCount(input.RequestedCount), seed)
	if err != nil {
		return store.PackOpening{}, err
	}
	return store.PackOpening{PlayerID: input.PlayerID, PackID: pack.ID, Mode: rule.Mode, RequestedCount: input.RequestedCount, ReturnedCount: len(packs), Seed: seed, Free: rule.FreeOpenings, Packs: packs}, nil
}

func (e *Engine) Open(ctx context.Context, input OpenInput) (store.PackOpening, bool, error) {
	if strings.TrimSpace(input.TransactionID) == "" {
		return store.PackOpening{}, false, fmt.Errorf("transaction id is required")
	}
	product, err := e.catalog.PackProduct(input.ProductID)
	if err != nil {
		return store.PackOpening{}, false, err
	}
	pack, err := e.catalog.Pack(product.PackID)
	if err != nil {
		return store.PackOpening{}, false, err
	}
	rule, err := e.state.GlobalPackRule(ctx)
	if err != nil {
		return store.PackOpening{}, false, err
	}
	requested := normalizedRequestedCount(input.RequestedCount)
	seed, err := seedFor(rule)
	if err != nil {
		return store.PackOpening{}, false, err
	}
	packs, err := e.draw(pack, rule, requested, seed)
	if err != nil {
		return store.PackOpening{}, false, err
	}
	powerCost := product.OnePrice
	if requested == 10 {
		powerCost = product.TenPrice
	}
	opening := store.PackOpening{
		PlayerID: input.PlayerID, TransactionID: input.TransactionID, ProductID: product.ID, PackID: pack.ID,
		RequestedCount: requested, ReturnedCount: len(packs), Mode: rule.Mode, Seed: seed, Free: rule.FreeOpenings,
		PowerCost: powerCost, ExperienceReward: product.OneExperience * int64(len(packs)),
		CeilPointReward: product.OneCeilPoints * int64(len(packs)), ShineDustReward: shineDustPerReturnedPack * int64(len(packs)), Packs: packs,
	}
	maximum, period := e.packPowerSettings()
	return e.state.CommitPackOpening(ctx, store.CommitPackOpening{Opening: opening, CeilGroup: pack.CeilGroupID, Share: input.Share, MaxPower: maximum, HealPeriod: period})
}

func (e *Engine) History(ctx context.Context, playerID string, limit int) ([]store.PackOpening, error) {
	return e.state.PackHistory(ctx, playerID, limit)
}

func (e *Engine) State(ctx context.Context, playerID string) (store.PackState, error) {
	maximum, period := e.packPowerSettings()
	return e.state.PackState(ctx, playerID, maximum, period)
}

func (e *Engine) SetPokeGold(ctx context.Context, playerID string, quantity int64) error {
	return e.state.SetPokeGold(ctx, playerID, quantity)
}

func (e *Engine) validateRule(rule store.PackRule) error {
	if rule.ReturnPackCount < 0 {
		return invalid("returned pack count must be non-negative")
	}
	switch rule.Mode {
	case "official":
	case "table", "rarity":
		if rule.TargetPackID != "" {
			pack, err := e.catalog.Pack(rule.TargetPackID)
			if err != nil {
				return invalid("%v", err)
			}
			if rule.Mode == "table" && !packHasTableKind(pack, rule.TableKind) {
				return invalid("pack %q has no %q table", pack.ID, rule.TableKind)
			}
			if rule.Mode == "rarity" && len(cardsForRarity(pack, rule.Rarity)) == 0 {
				return invalid("pack %q has no %q cards", pack.ID, rule.Rarity)
			}
		} else {
			supported := false
			for _, pack := range e.catalog.Packs() {
				if rule.Mode == "table" && packHasTableKind(pack, rule.TableKind) {
					supported = true
				}
				if rule.Mode == "rarity" && len(cardsForRarity(pack, rule.Rarity)) > 0 {
					supported = true
				}
			}
			if !supported {
				return invalid("no pack supports the selected %s", rule.Mode)
			}
		}
	case "illegal":
		if rule.ReturnPackCount <= 0 {
			return invalid("returned pack count must be positive")
		}
		if rule.ReturnPackCount > maxIllegalReturnedPacks {
			return invalid("returned pack count must not exceed %d", maxIllegalReturnedPacks)
		}
		if rule.Illegal.CardCount <= 0 {
			return invalid("cards per pack must be positive")
		}
		if rule.Illegal.CardCount > maxIllegalCardsPerBooster {
			return invalid("cards per pack must not exceed %d", maxIllegalCardsPerBooster)
		}
		poolSize := e.IllegalPoolSize(rule.Illegal)
		if poolSize == 0 {
			return invalid("no cards match the illegal filters")
		}
		if !rule.Illegal.AllowDuplicates && rule.Illegal.CardCount > poolSize {
			return invalid("cards per pack exceeds the filtered pool without duplicates")
		}
	default:
		return invalid("unknown mode %q", rule.Mode)
	}
	for _, output := range rule.Packs {
		for _, slot := range output {
			if strings.TrimSpace(slot.CardID) == "" {
				continue
			}
			if _, err := e.catalog.Card(slot.CardID); err != nil {
				return invalid("%v", err)
			}
		}
	}
	return nil
}

func (e *Engine) draw(pack catalog.Pack, rule store.PackRule, requested int, seed int64) ([]store.OpeningPack, error) {
	if rule.Mode != "official" && rule.Mode != "illegal" && rule.TargetPackID != "" && rule.TargetPackID != pack.ID {
		rule.Mode = "official"
	}
	if rule.TargetPackID == "" {
		if rule.Mode == "table" && !packHasTableKind(pack, rule.TableKind) {
			rule.Mode = "official"
		}
		if rule.Mode == "rarity" && len(cardsForRarity(pack, rule.Rarity)) == 0 {
			rule.Mode = "official"
		}
	}
	rng := mathrand.New(mathrand.NewSource(seed))
	count := requested
	var illegalPool []catalog.Card
	if rule.Mode == "illegal" {
		// The game has two opening actions. A x1 action must always return one
		// booster; the configured replacement count only applies to x10.
		count = 1
		if requested == 10 {
			count = max(1, min(rule.ReturnPackCount, maxIllegalReturnedPacks))
		}
		rule.Illegal.CardCount = max(1, min(rule.Illegal.CardCount, maxIllegalCardsPerBooster))
		illegalPool = e.illegalCardPool(rule.Illegal)
		if len(illegalPool) == 0 {
			return nil, invalid("no cards match the illegal filters")
		}
	}
	result := make([]store.OpeningPack, 0, count)
	for packIndex := 0; packIndex < count; packIndex++ {
		var drawn store.OpeningPack
		var err error
		switch rule.Mode {
		case "official":
			drawn, err = drawOfficial(pack, rng)
		case "table":
			drawn, err = drawTableMode(pack, rule, packIndex, rng)
		case "rarity":
			drawn, err = drawRarityMode(pack, rule, packIndex, rng)
		case "illegal":
			drawn, err = e.drawIllegal(pack, illegalPool, rule.Illegal, rng)
		default:
			err = invalid("unknown mode %q", rule.Mode)
		}
		if err != nil {
			return nil, err
		}
		result = append(result, drawn)
	}
	return result, nil
}

func drawOfficial(pack catalog.Pack, rng *mathrand.Rand) (store.OpeningPack, error) {
	tables := positiveTables(pack.Tables)
	if len(tables) == 0 {
		return store.OpeningPack{}, invalid("pack %q has no drawable table", pack.ID)
	}
	table := tables[weightedIndex(rng, tableWeights(tables))]
	return drawFromTable(table, nil, rng)
}

func drawTableMode(pack catalog.Pack, rule store.PackRule, packIndex int, rng *mathrand.Rand) (store.OpeningPack, error) {
	var tables []catalog.PackTable
	for _, table := range pack.Tables {
		if table.Kind == rule.TableKind {
			tables = append(tables, table)
		}
	}
	if len(tables) == 0 {
		return store.OpeningPack{}, invalid("pack %q has no %q table", pack.ID, rule.TableKind)
	}
	locks := configuredOutput(rule, packIndex)
	if locked, ok := fullyLockedPack(tables[0].ID, locks); ok {
		return locked, nil
	}
	return drawFromTable(tables[0], locks, rng)
}

func fullyLockedPack(tableID string, locks []store.PackRuleSlot) (store.OpeningPack, bool) {
	if len(locks) != 5 {
		return store.OpeningPack{}, false
	}
	result := store.OpeningPack{TableID: tableID, Cards: make([]string, 0, 5)}
	for _, slot := range locks {
		if !slot.Locked || slot.CardID == "" {
			return store.OpeningPack{}, false
		}
		result.Cards = append(result.Cards, slot.CardID)
	}
	return result, true
}

func drawFromTable(table catalog.PackTable, locks []store.PackRuleSlot, rng *mathrand.Rand) (store.OpeningPack, error) {
	result := store.OpeningPack{TableID: table.ID}
	for slotIndex, slot := range table.Slots {
		if slotIndex < len(locks) && locks[slotIndex].Locked && locks[slotIndex].CardID != "" {
			result.Cards = append(result.Cards, locks[slotIndex].CardID)
			continue
		}
		pools := positivePools(slot.Pools)
		if len(pools) == 0 {
			return store.OpeningPack{}, invalid("table %q slot %d has no pool", table.ID, slotIndex+1)
		}
		pool := pools[weightedIndex(rng, poolWeights(pools))]
		cards := positiveCards(pool.Cards)
		if len(cards) == 0 {
			return store.OpeningPack{}, invalid("table %q rarity %q has no cards", table.ID, pool.Rarity)
		}
		result.Cards = append(result.Cards, cards[weightedIndex(rng, cardWeights(cards))].CardID)
	}
	return result, nil
}

func drawRarityMode(pack catalog.Pack, rule store.PackRule, packIndex int, rng *mathrand.Rand) (store.OpeningPack, error) {
	cards := cardsForRarity(pack, rule.Rarity)
	if len(cards) == 0 {
		return store.OpeningPack{}, invalid("pack %q has no %q cards", pack.ID, rule.Rarity)
	}
	tables := positiveTables(pack.Tables)
	if len(tables) == 0 {
		return store.OpeningPack{}, invalid("pack %q has no drawable table", pack.ID)
	}
	slotCount := len(tables[0].Slots)
	locks := configuredOutput(rule, packIndex)
	result := store.OpeningPack{TableID: tables[0].ID}
	for slotIndex := 0; slotIndex < slotCount; slotIndex++ {
		if slotIndex < len(locks) && locks[slotIndex].Locked && locks[slotIndex].CardID != "" {
			if !containsCard(cards, locks[slotIndex].CardID) {
				return store.OpeningPack{}, invalid("card %q is not in rarity %q for pack %q", locks[slotIndex].CardID, rule.Rarity, pack.ID)
			}
			result.Cards = append(result.Cards, locks[slotIndex].CardID)
			continue
		}
		result.Cards = append(result.Cards, cards[rng.Intn(len(cards))])
	}
	return result, nil
}

func (e *Engine) drawIllegal(pack catalog.Pack, pool []catalog.Card, rule store.IllegalPackRule, rng *mathrand.Rand) (store.OpeningPack, error) {
	tables := illegalProbabilityTables(pack)
	if len(tables) == 0 {
		return store.OpeningPack{}, invalid("pack %q has no probability table", pack.ID)
	}
	table := tables[weightedIndex(rng, tableWeights(tables))]
	if len(table.Slots) == 0 {
		return store.OpeningPack{}, invalid("table %q has no slots", table.ID)
	}
	available := append([]catalog.Card(nil), pool...)
	result := store.OpeningPack{TableID: table.ID, Cards: make([]string, 0, rule.CardCount)}
	for cardIndex := 0; cardIndex < rule.CardCount; cardIndex++ {
		slot := table.Slots[cardIndex%len(table.Slots)]
		choices := e.illegalPoolChoices([]catalog.PackSlot{slot}, available)
		if len(choices) == 0 {
			choices = e.illegalPoolChoices(table.Slots, available)
		}
		if len(choices) == 0 {
			return store.OpeningPack{}, invalid("table %q has no probability for the filtered pool", table.ID)
		}
		choice := choices[weightedIndex(rng, illegalChoiceWeights(choices))]
		candidateIndex := choice.CardIndexes[rng.Intn(len(choice.CardIndexes))]
		result.Cards = append(result.Cards, available[candidateIndex].ID)
		if !rule.AllowDuplicates {
			available = append(available[:candidateIndex], available[candidateIndex+1:]...)
		}
	}
	return result, nil
}

type illegalPoolChoice struct {
	Weight      int64
	CardIndexes []int
}

func (e *Engine) illegalPoolChoices(slots []catalog.PackSlot, cards []catalog.Card) []illegalPoolChoice {
	var result []illegalPoolChoice
	for _, slot := range slots {
		for _, pool := range positivePools(slot.Pools) {
			codes := e.rarityCodesByLabel[pool.Rarity]
			var indexes []int
			for index, card := range cards {
				if _, ok := codes[card.Rarity]; ok {
					indexes = append(indexes, index)
				}
			}
			if len(indexes) > 0 {
				result = append(result, illegalPoolChoice{Weight: pool.Weight, CardIndexes: indexes})
			}
		}
	}
	return result
}

func illegalChoiceWeights(choices []illegalPoolChoice) []int64 {
	result := make([]int64, len(choices))
	for index, choice := range choices {
		result[index] = choice.Weight
	}
	return result
}

func illegalProbabilityTables(pack catalog.Pack) []catalog.PackTable {
	var normal, positiveNormal []catalog.PackTable
	for _, table := range pack.Tables {
		if table.Kind != "normal" || len(table.Slots) == 0 {
			continue
		}
		normal = append(normal, table)
		if table.Weight > 0 {
			positiveNormal = append(positiveNormal, table)
		}
	}
	if len(positiveNormal) > 0 {
		return positiveNormal
	}
	if len(normal) > 0 {
		return normal
	}
	tables := positiveTables(pack.Tables)
	if len(tables) > 0 {
		return tables
	}
	for _, table := range pack.Tables {
		if len(table.Slots) > 0 {
			tables = append(tables, table)
		}
	}
	return tables
}

func (e *Engine) illegalCardPool(rule store.IllegalPackRule) []catalog.Card {
	expansions := stringSet(rule.ExpansionIDs)
	rarities := intSet(rule.Rarities)
	kinds := stringSet(rule.CardKinds)
	var result []catalog.Card
	for _, card := range e.catalog.Cards() {
		if len(expansions) > 0 && !hasSelectedExpansion(card.ExpansionIDs, expansions) {
			continue
		}
		if len(rarities) > 0 {
			if _, ok := rarities[card.Rarity]; !ok {
				continue
			}
		}
		if len(kinds) > 0 {
			if _, ok := kinds[string(card.Kind)]; !ok {
				continue
			}
		}
		result = append(result, card)
	}
	return result
}

func catalogRarityCodes(master *catalog.Catalog) map[string]map[int]struct{} {
	result := make(map[string]map[int]struct{})
	for _, pack := range master.Packs() {
		for _, table := range pack.Tables {
			for _, slot := range table.Slots {
				for _, pool := range slot.Pools {
					codes := result[pool.Rarity]
					if codes == nil {
						codes = make(map[int]struct{})
						result[pool.Rarity] = codes
					}
					for _, weighted := range pool.Cards {
						card, err := master.Card(weighted.CardID)
						if err == nil {
							codes[card.Rarity] = struct{}{}
						}
					}
				}
			}
		}
	}
	return result
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func intSet(values []int) map[int]struct{} {
	result := make(map[int]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func hasSelectedExpansion(values []string, selected map[string]struct{}) bool {
	for _, value := range values {
		if _, ok := selected[value]; ok {
			return true
		}
	}
	return false
}

func configuredOutput(rule store.PackRule, index int) []store.PackRuleSlot {
	if len(rule.Packs) == 0 {
		return nil
	}
	return rule.Packs[index%len(rule.Packs)]
}

func cardsForRarity(pack catalog.Pack, rarity string) []string {
	seen := make(map[string]struct{})
	for _, table := range pack.Tables {
		for _, slot := range table.Slots {
			for _, pool := range slot.Pools {
				if pool.Rarity != rarity {
					continue
				}
				for _, card := range pool.Cards {
					seen[card.CardID] = struct{}{}
				}
			}
		}
	}
	result := make([]string, 0, len(seen))
	for cardID := range seen {
		result = append(result, cardID)
	}
	sort.Strings(result)
	return result
}

func positiveTables(values []catalog.PackTable) []catalog.PackTable {
	var result []catalog.PackTable
	for _, value := range values {
		if value.Weight > 0 {
			result = append(result, value)
		}
	}
	return result
}

func positivePools(values []catalog.PackSlotPool) []catalog.PackSlotPool {
	var result []catalog.PackSlotPool
	for _, value := range values {
		if value.Weight > 0 {
			result = append(result, value)
		}
	}
	return result
}

func positiveCards(values []catalog.WeightedCard) []catalog.WeightedCard {
	var result []catalog.WeightedCard
	for _, value := range values {
		if value.Weight > 0 {
			result = append(result, value)
		}
	}
	return result
}

func tableWeights(values []catalog.PackTable) []int64 {
	result := make([]int64, len(values))
	for i := range values {
		result[i] = values[i].Weight
	}
	return result
}

func poolWeights(values []catalog.PackSlotPool) []int64 {
	result := make([]int64, len(values))
	for i := range values {
		result[i] = values[i].Weight
	}
	return result
}

func cardWeights(values []catalog.WeightedCard) []int64 {
	result := make([]int64, len(values))
	for i := range values {
		result[i] = values[i].Weight
	}
	return result
}

func weightedIndex(rng *mathrand.Rand, weights []int64) int {
	var total int64
	for _, weight := range weights {
		if weight > 0 {
			total += weight
		}
	}
	if total <= 0 {
		return 0
	}
	value := rng.Int63n(total)
	for index, weight := range weights {
		if weight <= 0 {
			continue
		}
		if value < weight {
			return index
		}
		value -= weight
	}
	return len(weights) - 1
}

func seedFor(rule store.PackRule) (int64, error) {
	if rule.FixedSeed != nil {
		return *rule.FixedSeed, nil
	}
	var data [8]byte
	if _, err := cryptorand.Read(data[:]); err != nil {
		return 0, fmt.Errorf("generate pack seed: %w", err)
	}
	return int64(binary.LittleEndian.Uint64(data[:])), nil
}

func (e *Engine) packPowerSettings() (int64, time.Duration) {
	for _, power := range e.catalog.HomeSettings().PackPowers {
		if power.ID == "PACK_POWER_NORMAL" {
			return power.AutoHealLimit, time.Duration(power.HealSecondsPerPower) * time.Second
		}
	}
	return 2, 12 * time.Hour
}

func normalizedRequestedCount(value int) int {
	if value == 10 {
		return 10
	}
	return 1
}

func packHasTableKind(pack catalog.Pack, kind string) bool {
	for _, table := range pack.Tables {
		if table.Kind == kind {
			return true
		}
	}
	return false
}

func containsCard(values []string, cardID string) bool {
	index := sort.SearchStrings(values, cardID)
	return index < len(values) && values[index] == cardID
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidRule, fmt.Sprintf(format, args...))
}
