package player

import (
	"context"
	"strconv"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
)

func trophyActionProgress(definition catalog.Trophy, progress map[actionTarget]int64) int64 {
	key := int32(0)
	switch definition.Type {
	case 9:
		key = ActionPackOpenTotal
	case 11:
		key = 8 // FEED_CHALLENGED
	case 36:
		key = ActionCardGetDexTotal
	case 12:
		key = ActionCardGetTotal
	case 14:
		key = ActionCardGetTypeTotal
	case 29:
		key = ActionTrainersCardGetTypeTotal
	case 4:
		key = 4 // PVP_WON
	case 8:
		key = 37 // PVE_WON_TOTAL
	case 49:
		key = ActionGiveCardTotal
	default:
		return 0
	}
	if len(definition.TargetIDs) == 0 {
		var total int64
		for target, count := range progress {
			if target.key == key {
				total += count
			}
		}
		return total
	}
	var total int64
	for _, targetID := range definition.TargetIDs {
		total += progress[actionTarget{key: key, target: strconv.FormatInt(int64(targetID), 10)}]
	}
	return total
}

// cardActionTotals is the single projection boundary between the inventory
// model and Action/SyncStatesV1. The official service exposes cumulative
// snapshots, so these values are lower bounds and are persisted monotonically.
func cardActionTotals(cards []store.CardStock, master *catalog.Catalog) []store.ActionTotal {
	totals := map[actionTarget]int64{}
	add := func(key int32, target string, count int64) {
		if count > 0 {
			totals[actionTarget{key: key, target: target}] += count
		}
	}
	var total, dex int64
	for _, stock := range cards {
		if stock.Quantity <= 0 {
			continue
		}
		card, err := master.Card(stock.CardID)
		if err != nil {
			continue
		}
		total += stock.Quantity
		dex++
		add(ActionGetCards, stock.CardID, stock.Quantity)
		add(ActionCardGetRarityTotal, rarityTarget(card.Rarity), stock.Quantity)
		add(ActionCardGetRarityDexTotal, rarityTarget(card.Rarity), 1)
		if len(card.ExpansionIDs) > 0 {
			// A card normally belongs to one expansion. Use the first catalog
			// relation for the cumulative expansion counters; this avoids
			// double-counting promotional aliases.
			add(ActionCardGetExpansionTotal, card.ExpansionIDs[0], stock.Quantity)
			add(ActionCardGetExpansionDexTotal, card.ExpansionIDs[0], 1)
		}
		if card.Kind == catalog.PokemonCard {
			for _, typ := range card.PokemonTypes {
				add(ActionCardGetTypeTotal, itoaTarget(typ), stock.Quantity)
				add(ActionCardGetTypeDexTotal, itoaTarget(typ), 1)
			}
		} else if card.TrainerType > 0 {
			add(ActionTrainersCardGetTypeTotal, itoaTarget(card.TrainerType), stock.Quantity)
		}
	}
	add(ActionCardGetTotal, "", total)
	add(ActionCardGetDexTotal, "", dex)
	result := make([]store.ActionTotal, 0, len(totals))
	for key, count := range totals {
		result = append(result, store.ActionTotal{Key: key.key, Target: key.target, Count: count})
	}
	return result
}

func rarityTarget(rarity int) string {
	if rarity >= 100 && rarity <= 700 {
		return itoaTarget(int32(rarity / 100))
	}
	if rarity == 830 {
		return "8"
	}
	if rarity == 860 {
		return "9"
	}
	return itoaTarget(int32(rarity))
}

func itoaTarget(value int32) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatInt(int64(value), 10)
}

type actionTarget struct {
	key    int32
	target string
}

func (m *Manager) projectInventoryActions(ctx context.Context, playerID string) error {
	cards, err := m.state.CardStocks(ctx, playerID)
	if err != nil {
		return err
	}
	if err := m.state.ProjectActionTotals(ctx, playerID, cardActionTotals(cards, m.catalog)); err != nil {
		return err
	}
	// Older handlers recorded per-battle PVE rows (keys 5/6). The official
	// aggregate keys 36/37 are derived here so existing profiles can claim the
	// solo trophies without replaying every battle.
	actions, err := m.state.LatestActionTotals(ctx, playerID)
	if err != nil {
		return err
	}
	var tries, wins int64
	for _, action := range actions {
		switch action.Key {
		case ActionPVETry:
			tries += action.Count
		case ActionPVEWon:
			wins += action.Count
		}
	}
	return m.state.ProjectActionTotals(ctx, playerID, []store.ActionTotal{
		{Key: 36, Count: tries}, {Key: 37, Count: wins},
	})
}
