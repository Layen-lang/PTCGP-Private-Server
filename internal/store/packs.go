package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrInsufficientResources = errors.New("insufficient local resources")

type PackRuleSlot struct {
	CardID string
	Locked bool
}

type IllegalPackRule struct {
	CardCount       int
	AllowDuplicates bool
	ExpansionIDs    []string
	Rarities        []int
	CardKinds       []string
}

type PackRule struct {
	Mode, TargetPackID, TableKind, Rarity string
	ReturnPackCount                       int
	FreeOpenings                          bool
	FixedSeed                             *int64
	Packs                                 [][]PackRuleSlot
	Illegal                               IllegalPackRule
	UpdatedAt                             time.Time
}

type PackState struct {
	Power, PokeGold int64
	PowerUpdatedAt  time.Time
}

type OpeningPack struct {
	TableID string
	Cards   []string
}

type PackOpening struct {
	ID, PlayerID, TransactionID, ProductID, PackID, Mode string
	RequestedCount, ReturnedCount                        int
	Seed                                                 int64
	Free                                                 bool
	PowerCost, ExperienceReward, CeilPointReward         int64
	ShineDustReward                                      int64
	Packs                                                []OpeningPack
	CreatedAt                                            time.Time
}

type CommitPackOpening struct {
	Opening    PackOpening
	CeilGroup  string
	Share      bool
	MaxPower   int64
	HealPeriod time.Duration
}

func defaultPackRule() PackRule {
	return PackRule{Mode: "official", ReturnPackCount: 1, Illegal: IllegalPackRule{CardCount: 5}}
}

func encodeIllegalFilters(rule IllegalPackRule) (string, string, string, error) {
	expansionIDs, err := json.Marshal(rule.ExpansionIDs)
	if err != nil {
		return "", "", "", err
	}
	rarities, err := json.Marshal(rule.Rarities)
	if err != nil {
		return "", "", "", err
	}
	cardKinds, err := json.Marshal(rule.CardKinds)
	if err != nil {
		return "", "", "", err
	}
	return string(expansionIDs), string(rarities), string(cardKinds), nil
}

func decodeIllegalFilters(rule *IllegalPackRule, expansionIDs, rarities, cardKinds string) error {
	if err := json.Unmarshal([]byte(expansionIDs), &rule.ExpansionIDs); err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(rarities), &rule.Rarities); err != nil {
		return err
	}
	return json.Unmarshal([]byte(cardKinds), &rule.CardKinds)
}

// GlobalPackRule returns the single booster rule shared by every local profile.
func (s *Store) GlobalPackRule(ctx context.Context) (PackRule, error) {
	var rule PackRule
	var seed sql.NullInt64
	var free, allowDuplicates int
	var expansionIDs, rarities, cardKinds string
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT mode,target_pack_id,table_kind,rarity,return_pack_count,illegal_card_count,illegal_allow_duplicates,illegal_expansion_ids,illegal_rarities,illegal_card_kinds,free_openings,fixed_seed,updated_at FROM global_pack_rule WHERE singleton=1`).Scan(
		&rule.Mode, &rule.TargetPackID, &rule.TableKind, &rule.Rarity, &rule.ReturnPackCount, &rule.Illegal.CardCount, &allowDuplicates, &expansionIDs, &rarities, &cardKinds, &free, &seed, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultPackRule(), nil
	}
	if err != nil {
		return PackRule{}, fmt.Errorf("load global pack rule: %w", err)
	}
	rule.FreeOpenings = free != 0
	rule.Illegal.AllowDuplicates = allowDuplicates != 0
	if err := decodeIllegalFilters(&rule.Illegal, expansionIDs, rarities, cardKinds); err != nil {
		return PackRule{}, fmt.Errorf("load global pack rule filters: %w", err)
	}
	rule.UpdatedAt = time.Unix(updated, 0).UTC()
	if seed.Valid {
		value := seed.Int64
		rule.FixedSeed = &value
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.pack_index,s.slot_index,s.card_id,s.locked FROM global_pack_rule_packs p LEFT JOIN global_pack_rule_slots s ON s.pack_index=p.pack_index ORDER BY p.pack_index,s.slot_index`)
	if err != nil {
		return PackRule{}, fmt.Errorf("load global pack rule slots: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var packIndex int
		var slotIndex sql.NullInt64
		var cardID sql.NullString
		var locked sql.NullInt64
		if err := rows.Scan(&packIndex, &slotIndex, &cardID, &locked); err != nil {
			return PackRule{}, err
		}
		for len(rule.Packs) <= packIndex {
			rule.Packs = append(rule.Packs, nil)
		}
		if slotIndex.Valid {
			for len(rule.Packs[packIndex]) <= int(slotIndex.Int64) {
				rule.Packs[packIndex] = append(rule.Packs[packIndex], PackRuleSlot{})
			}
			rule.Packs[packIndex][slotIndex.Int64] = PackRuleSlot{CardID: cardID.String, Locked: locked.Int64 != 0}
		}
	}
	return rule, rows.Err()
}

// SaveGlobalPackRule atomically replaces the rule consumed by all pack opens.
func (s *Store) SaveGlobalPackRule(ctx context.Context, rule PackRule) error {
	expansionIDs, rarities, cardKinds, err := encodeIllegalFilters(rule.Illegal)
	if err != nil {
		return fmt.Errorf("encode global pack rule filters: %w", err)
	}
	now := s.now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save global pack rule: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO global_pack_rule(singleton,mode,target_pack_id,table_kind,rarity,return_pack_count,illegal_card_count,illegal_allow_duplicates,illegal_expansion_ids,illegal_rarities,illegal_card_kinds,free_openings,fixed_seed,updated_at) VALUES(1,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(singleton) DO UPDATE SET mode=excluded.mode,target_pack_id=excluded.target_pack_id,table_kind=excluded.table_kind,rarity=excluded.rarity,return_pack_count=excluded.return_pack_count,illegal_card_count=excluded.illegal_card_count,illegal_allow_duplicates=excluded.illegal_allow_duplicates,illegal_expansion_ids=excluded.illegal_expansion_ids,illegal_rarities=excluded.illegal_rarities,illegal_card_kinds=excluded.illegal_card_kinds,free_openings=excluded.free_openings,fixed_seed=excluded.fixed_seed,updated_at=excluded.updated_at`, rule.Mode, rule.TargetPackID, rule.TableKind, rule.Rarity, rule.ReturnPackCount, rule.Illegal.CardCount, rule.Illegal.AllowDuplicates, expansionIDs, rarities, cardKinds, rule.FreeOpenings, rule.FixedSeed, now); err != nil {
		return fmt.Errorf("save global pack rule: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM global_pack_rule_packs`); err != nil {
		return err
	}
	for packIndex, slots := range rule.Packs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO global_pack_rule_packs(pack_index) VALUES(?)`, packIndex); err != nil {
			return err
		}
		for slotIndex, slot := range slots {
			if strings.TrimSpace(slot.CardID) == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO global_pack_rule_slots(pack_index,slot_index,card_id,locked) VALUES(?,?,?,?)`, packIndex, slotIndex, slot.CardID, slot.Locked); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) PackRule(ctx context.Context, playerID string) (PackRule, error) {
	var rule PackRule
	var seed sql.NullInt64
	var free, allowDuplicates int
	var expansionIDs, rarities, cardKinds string
	var updated int64
	err := s.db.QueryRowContext(ctx, `SELECT mode,target_pack_id,table_kind,rarity,return_pack_count,illegal_card_count,illegal_allow_duplicates,illegal_expansion_ids,illegal_rarities,illegal_card_kinds,free_openings,fixed_seed,updated_at FROM player_pack_rules WHERE player_id=?`, playerID).Scan(
		&rule.Mode, &rule.TargetPackID, &rule.TableKind, &rule.Rarity, &rule.ReturnPackCount, &rule.Illegal.CardCount, &allowDuplicates, &expansionIDs, &rarities, &cardKinds, &free, &seed, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return defaultPackRule(), nil
	}
	if err != nil {
		return PackRule{}, fmt.Errorf("load pack rule: %w", err)
	}
	rule.FreeOpenings = free != 0
	rule.Illegal.AllowDuplicates = allowDuplicates != 0
	if err := decodeIllegalFilters(&rule.Illegal, expansionIDs, rarities, cardKinds); err != nil {
		return PackRule{}, fmt.Errorf("load player pack rule filters: %w", err)
	}
	rule.UpdatedAt = time.Unix(updated, 0).UTC()
	if seed.Valid {
		value := seed.Int64
		rule.FixedSeed = &value
	}
	rows, err := s.db.QueryContext(ctx, `SELECT p.pack_index,s.slot_index,s.card_id,s.locked FROM player_pack_rule_packs p LEFT JOIN player_pack_rule_slots s ON s.player_id=p.player_id AND s.pack_index=p.pack_index WHERE p.player_id=? ORDER BY p.pack_index,s.slot_index`, playerID)
	if err != nil {
		return PackRule{}, fmt.Errorf("load pack rule slots: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var packIndex int
		var slotIndex sql.NullInt64
		var cardID sql.NullString
		var locked sql.NullInt64
		if err := rows.Scan(&packIndex, &slotIndex, &cardID, &locked); err != nil {
			return PackRule{}, err
		}
		for len(rule.Packs) <= packIndex {
			rule.Packs = append(rule.Packs, nil)
		}
		if slotIndex.Valid {
			for len(rule.Packs[packIndex]) <= int(slotIndex.Int64) {
				rule.Packs[packIndex] = append(rule.Packs[packIndex], PackRuleSlot{})
			}
			rule.Packs[packIndex][slotIndex.Int64] = PackRuleSlot{CardID: cardID.String, Locked: locked.Int64 != 0}
		}
	}
	return rule, rows.Err()
}

func (s *Store) SavePackRule(ctx context.Context, playerID string, rule PackRule) error {
	expansionIDs, rarities, cardKinds, err := encodeIllegalFilters(rule.Illegal)
	if err != nil {
		return fmt.Errorf("encode player pack rule filters: %w", err)
	}
	now := s.now().UTC().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin save pack rule: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_pack_rules(player_id,mode,target_pack_id,table_kind,rarity,return_pack_count,illegal_card_count,illegal_allow_duplicates,illegal_expansion_ids,illegal_rarities,illegal_card_kinds,free_openings,fixed_seed,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(player_id) DO UPDATE SET mode=excluded.mode,target_pack_id=excluded.target_pack_id,table_kind=excluded.table_kind,rarity=excluded.rarity,return_pack_count=excluded.return_pack_count,illegal_card_count=excluded.illegal_card_count,illegal_allow_duplicates=excluded.illegal_allow_duplicates,illegal_expansion_ids=excluded.illegal_expansion_ids,illegal_rarities=excluded.illegal_rarities,illegal_card_kinds=excluded.illegal_card_kinds,free_openings=excluded.free_openings,fixed_seed=excluded.fixed_seed,updated_at=excluded.updated_at`, playerID, rule.Mode, rule.TargetPackID, rule.TableKind, rule.Rarity, rule.ReturnPackCount, rule.Illegal.CardCount, rule.Illegal.AllowDuplicates, expansionIDs, rarities, cardKinds, rule.FreeOpenings, rule.FixedSeed, now); err != nil {
		return fmt.Errorf("save pack rule: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM player_pack_rule_packs WHERE player_id=?`, playerID); err != nil {
		return err
	}
	for packIndex, slots := range rule.Packs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_pack_rule_packs(player_id,pack_index) VALUES(?,?)`, playerID, packIndex); err != nil {
			return err
		}
		for slotIndex, slot := range slots {
			if strings.TrimSpace(slot.CardID) == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO player_pack_rule_slots(player_id,pack_index,slot_index,card_id,locked) VALUES(?,?,?,?,?)`, playerID, packIndex, slotIndex, slot.CardID, slot.Locked); err != nil {
				return err
			}
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET state_version=state_version+1,updated_at=? WHERE player_id=?`, now, playerID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) PackState(ctx context.Context, playerID string, maxPower int64, healPeriod time.Duration) (PackState, error) {
	var state PackState
	var updated int64
	if err := s.db.QueryRowContext(ctx, `SELECT pack_power,pack_power_updated_at,poke_gold FROM player_pack_state WHERE player_id=?`, playerID).Scan(&state.Power, &updated, &state.PokeGold); err != nil {
		return PackState{}, fmt.Errorf("load pack state: %w", err)
	}
	state.PowerUpdatedAt = time.Unix(updated, 0).UTC()
	state.Power, state.PowerUpdatedAt = regeneratedPower(state.Power, state.PowerUpdatedAt, s.now().UTC(), maxPower, healPeriod)
	return state, nil
}

func (s *Store) SetPokeGold(ctx context.Context, playerID string, quantity int64) error {
	if quantity < 0 {
		return fmt.Errorf("poke gold must be non-negative")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE player_pack_state SET poke_gold=? WHERE player_id=?`, quantity, playerID)
	if err != nil {
		return fmt.Errorf("set poke gold: %w", err)
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return fmt.Errorf("%w: player %q", ErrNotFound, playerID)
	}
	return nil
}

func (s *Store) ExchangePackPoints(ctx context.Context, playerID, transactionID, groupID, cardID string, amount, unitCost int64) (int64, bool, error) {
	if transactionID == "" || groupID == "" || cardID == "" || amount <= 0 || unitCost <= 0 {
		return 0, false, fmt.Errorf("%w: invalid pack point exchange", ErrRuleViolation)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback()
	var existingCard, existingGroup string
	var existingAmount int64
	err = tx.QueryRowContext(ctx, `SELECT group_id,card_id,amount FROM pack_shop_exchanges WHERE player_id=? AND transaction_id=?`, playerID, transactionID).Scan(&existingGroup, &existingCard, &existingAmount)
	if err == nil {
		if existingGroup != groupID || existingCard != cardID || existingAmount != amount {
			return 0, false, fmt.Errorf("%w: transaction payload changed", ErrRuleViolation)
		}
		var remaining int64
		if err := tx.QueryRowContext(ctx, `SELECT quantity FROM player_pack_ceil_points WHERE player_id=? AND group_id=?`, playerID, groupID).Scan(&remaining); err != nil {
			return 0, false, err
		}
		return remaining, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	cost := amount * unitCost
	result, err := tx.ExecContext(ctx, `UPDATE player_pack_ceil_points SET quantity=quantity-? WHERE player_id=? AND group_id=? AND quantity>=?`, cost, playerID, groupID, cost)
	if err != nil {
		return 0, false, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return 0, false, ErrInsufficientInventory
	}
	now := s.now().UTC().Unix()
	if _, err := tx.ExecContext(ctx, `INSERT INTO player_cards(player_id,card_id,quantity,first_received_at,last_received_at) VALUES(?,?,?,?,?) ON CONFLICT(player_id,card_id) DO UPDATE SET quantity=quantity+excluded.quantity,last_received_at=excluded.last_received_at`, playerID, cardID, amount, now, now); err != nil {
		return 0, false, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pack_shop_exchanges(player_id,transaction_id,group_id,card_id,amount,cost,created_at) VALUES(?,?,?,?,?,?,?)`, playerID, transactionID, groupID, cardID, amount, cost, now); err != nil {
		return 0, false, err
	}
	var remaining int64
	if err := tx.QueryRowContext(ctx, `SELECT quantity FROM player_pack_ceil_points WHERE player_id=? AND group_id=?`, playerID, groupID).Scan(&remaining); err != nil {
		return 0, false, err
	}
	return remaining, false, tx.Commit()
}

func (s *Store) CommitPackOpening(ctx context.Context, input CommitPackOpening) (PackOpening, bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return PackOpening{}, false, fmt.Errorf("begin pack opening: %w", err)
	}
	defer tx.Rollback()
	if existing, err := loadPackOpening(ctx, tx, input.Opening.PlayerID, input.Opening.TransactionID); err == nil {
		return existing, true, nil
	} else if !errors.Is(err, ErrNotFound) {
		return PackOpening{}, false, err
	}

	now := s.now().UTC()
	opening := input.Opening
	opening.ID, err = randomUUID()
	if err != nil {
		return PackOpening{}, false, err
	}
	opening.CreatedAt = now
	if !opening.Free {
		var power, pokeGold, updated int64
		if err := tx.QueryRowContext(ctx, `SELECT pack_power,poke_gold,pack_power_updated_at FROM player_pack_state WHERE player_id=?`, opening.PlayerID).Scan(&power, &pokeGold, &updated); err != nil {
			return PackOpening{}, false, fmt.Errorf("load pack resources: %w", err)
		}
		power, _ = regeneratedPower(power, time.Unix(updated, 0).UTC(), now, input.MaxPower, input.HealPeriod)
		remaining := opening.PowerCost
		usedPower := minInt64(power, remaining)
		power -= usedPower
		remaining -= usedPower
		if remaining > pokeGold {
			return PackOpening{}, false, ErrInsufficientResources
		}
		pokeGold -= remaining
		if _, err := tx.ExecContext(ctx, `UPDATE player_pack_state SET pack_power=?,pack_power_updated_at=?,poke_gold=? WHERE player_id=?`, power, now.Unix(), pokeGold, opening.PlayerID); err != nil {
			return PackOpening{}, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO pack_openings(opening_id,player_id,transaction_id,product_id,pack_id,requested_count,returned_count,mode,seed,free_opening,power_cost,experience_reward,ceil_point_reward,shine_dust_reward,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, opening.ID, opening.PlayerID, opening.TransactionID, opening.ProductID, opening.PackID, opening.RequestedCount, len(opening.Packs), opening.Mode, opening.Seed, opening.Free, opening.PowerCost, opening.ExperienceReward, opening.CeilPointReward, opening.ShineDustReward, now.Unix()); err != nil {
		return PackOpening{}, false, fmt.Errorf("insert pack opening: %w", err)
	}
	for packIndex, pack := range opening.Packs {
		if _, err := tx.ExecContext(ctx, `INSERT INTO pack_opening_packs(opening_id,pack_index,pack_table_id) VALUES(?,?,?)`, opening.ID, packIndex, pack.TableID); err != nil {
			return PackOpening{}, false, err
		}
		for slotIndex, cardID := range pack.Cards {
			if _, err := tx.ExecContext(ctx, `INSERT INTO pack_opening_cards(opening_id,pack_index,slot_index,card_id) VALUES(?,?,?,?)`, opening.ID, packIndex, slotIndex, cardID); err != nil {
				return PackOpening{}, false, err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO player_cards(player_id,card_id,quantity,first_received_at,last_received_at) VALUES(?,?,1,?,?) ON CONFLICT(player_id,card_id) DO UPDATE SET quantity=quantity+1,last_received_at=excluded.last_received_at`, opening.PlayerID, cardID, now.Unix(), now.Unix()); err != nil {
				return PackOpening{}, false, err
			}
			if err := addCardInAccountLanguage(ctx, tx, opening.PlayerID, cardID, 1); err != nil {
				return PackOpening{}, false, err
			}
		}
	}
	if opening.ShineDustReward > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_currencies(player_id,currency_id,quantity) VALUES(?,?,?) ON CONFLICT(player_id,currency_id) DO UPDATE SET quantity=quantity+excluded.quantity`, opening.PlayerID, "SHINEDUST", opening.ShineDustReward); err != nil {
			return PackOpening{}, false, err
		}
	}
	if input.CeilGroup != "" && opening.CeilPointReward > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_pack_ceil_points(player_id,group_id,quantity) VALUES(?,?,?) ON CONFLICT(player_id,group_id) DO UPDATE SET quantity=quantity+excluded.quantity`, opening.PlayerID, input.CeilGroup, opening.CeilPointReward); err != nil {
			return PackOpening{}, false, err
		}
	}
	cardCount := 0
	for _, pack := range opening.Packs {
		cardCount += len(pack.Cards)
	}
	for eventType, quantity := range map[string]int{"packs_opened": len(opening.Packs), "cards_received": cardCount} {
		if _, err := tx.ExecContext(ctx, `INSERT INTO player_mission_counters(player_id,event_type,quantity) VALUES(?,?,?) ON CONFLICT(player_id,event_type) DO UPDATE SET quantity=quantity+excluded.quantity`, opening.PlayerID, eventType, quantity); err != nil {
			return PackOpening{}, false, err
		}
	}
	if input.Share {
		entryID, err := randomUUID()
		if err != nil {
			return PackOpening{}, false, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO feed_entries(entry_id,player_id,opening_id,created_at) VALUES(?,?,?,?)`, entryID, opening.PlayerID, opening.ID, now.Unix()); err != nil {
			return PackOpening{}, false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE players SET experience=experience+?,state_version=state_version+1,updated_at=? WHERE player_id=?`, opening.ExperienceReward, now.Unix(), opening.PlayerID); err != nil {
		return PackOpening{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return PackOpening{}, false, fmt.Errorf("commit pack opening: %w", err)
	}
	opening.ReturnedCount = len(opening.Packs)
	return opening, false, nil
}

func (s *Store) PackHistory(ctx context.Context, playerID string, limit int) ([]PackOpening, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT transaction_id FROM pack_openings WHERE player_id=? ORDER BY created_at DESC LIMIT ?`, playerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list pack history: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	var result []PackOpening
	for _, id := range ids {
		opening, err := loadPackOpening(ctx, s.db, playerID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, opening)
	}
	return result, rows.Err()
}

func loadPackOpening(ctx context.Context, query rowQueryer, playerID, transactionID string) (PackOpening, error) {
	var opening PackOpening
	var free int
	var created int64
	err := query.QueryRowContext(ctx, `SELECT opening_id,player_id,transaction_id,product_id,pack_id,requested_count,returned_count,mode,seed,free_opening,power_cost,experience_reward,ceil_point_reward,shine_dust_reward,created_at FROM pack_openings WHERE player_id=? AND transaction_id=?`, playerID, transactionID).Scan(&opening.ID, &opening.PlayerID, &opening.TransactionID, &opening.ProductID, &opening.PackID, &opening.RequestedCount, &opening.ReturnedCount, &opening.Mode, &opening.Seed, &free, &opening.PowerCost, &opening.ExperienceReward, &opening.CeilPointReward, &opening.ShineDustReward, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return PackOpening{}, ErrNotFound
	}
	if err != nil {
		return PackOpening{}, fmt.Errorf("load pack opening: %w", err)
	}
	opening.Free = free != 0
	opening.CreatedAt = time.Unix(created, 0).UTC()
	rows, err := query.QueryContext(ctx, `SELECT p.pack_index,p.pack_table_id,c.slot_index,c.card_id FROM pack_opening_packs p LEFT JOIN pack_opening_cards c ON c.opening_id=p.opening_id AND c.pack_index=p.pack_index WHERE p.opening_id=? ORDER BY p.pack_index,c.slot_index`, opening.ID)
	if err != nil {
		return PackOpening{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var packIndex int
		var tableID string
		var slotIndex sql.NullInt64
		var cardID sql.NullString
		if err := rows.Scan(&packIndex, &tableID, &slotIndex, &cardID); err != nil {
			return PackOpening{}, err
		}
		for len(opening.Packs) <= packIndex {
			opening.Packs = append(opening.Packs, OpeningPack{})
		}
		opening.Packs[packIndex].TableID = tableID
		if slotIndex.Valid {
			opening.Packs[packIndex].Cards = append(opening.Packs[packIndex].Cards, cardID.String)
		}
	}
	return opening, rows.Err()
}

type rowQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func regeneratedPower(value int64, updatedAt, now time.Time, maximum int64, period time.Duration) (int64, time.Time) {
	if value >= maximum || period <= 0 || now.Before(updatedAt) {
		return minInt64(value, maximum), updatedAt
	}
	steps := int64(now.Sub(updatedAt) / period)
	if steps <= 0 {
		return value, updatedAt
	}
	value = minInt64(maximum, value+steps)
	return value, updatedAt.Add(time.Duration(steps) * period)
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
