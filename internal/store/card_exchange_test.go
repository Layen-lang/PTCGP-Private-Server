package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCardExchangeCommitsCostsProductAndCountTogether(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "card-exchange.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	owner, err := state.CreatePlayerWithInventory(ctx, "Collector", 1, 0, true, InitialInventory{Cards: map[string]int64{"CARD": 12}, Currencies: map[string]int64{"SHINEDUST": 100}})
	if err != nil {
		t.Fatal(err)
	}
	operation := CardExchangeOperation{CatalogID: "EXCHANGE", CardID: "CARD", ProductID: "SKIN", ProductKind: "card_skin", Amount: 2, ConsumeCards: 3, ConsumeBrightSand: 20, ProductAmount: 1}
	counts, err := state.ExchangeCardCosmetics(ctx, owner.ID, []CardExchangeOperation{operation})
	if err != nil || len(counts) != 1 || counts[0].Count != 2 {
		t.Fatalf("counts=%+v err=%v", counts, err)
	}
	snapshot, _ := state.Snapshot(ctx, owner.ID)
	if len(snapshot.Cards) != 1 || snapshot.Cards[0].Quantity != 6 || len(snapshot.Currencies) != 1 || snapshot.Currencies[0].Quantity != 60 || len(snapshot.CardSkins) != 1 || snapshot.CardSkins[0].Quantity != 2 {
		t.Fatalf("inventory cards=%+v currencies=%+v skins=%+v", snapshot.Cards, snapshot.Currencies, snapshot.CardSkins)
	}
	if _, err := state.ExchangeCardCosmetics(ctx, owner.ID, []CardExchangeOperation{{CatalogID: "TOO_EXPENSIVE", CardID: "CARD", ProductID: "FRAME", ProductKind: "card_frame", Amount: 1, ConsumeCards: 10, ProductAmount: 1}}); !errors.Is(err, ErrInsufficientInventory) {
		t.Fatalf("insufficient exchange=%v", err)
	}
	snapshot, _ = state.Snapshot(ctx, owner.ID)
	if len(snapshot.CardFrames) != 0 || snapshot.Cards[0].Quantity != 6 {
		t.Fatalf("failed exchange was not atomic: cards=%+v frames=%+v", snapshot.Cards, snapshot.CardFrames)
	}
}

func TestHealTradePowerConsumesResourcesAndRegenerates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := Open(ctx, filepath.Join(t.TempDir(), "trade-heal.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	state.now = func() time.Time { return now }
	owner, err := state.CreatePlayerWithInventory(ctx, "Trader", 1, 0, true, InitialInventory{Items: []InitialItem{{ID: "CHARGER", Kind: "trade_power_charger", Quantity: 24}}, PokeGold: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.TradeState(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := state.db.ExecContext(ctx, `UPDATE player_trade_state SET power=3,power_updated_at=? WHERE player_id=?`, now.Unix(), owner.ID); err != nil {
		t.Fatal(err)
	}
	healed, err := state.HealTradePower(ctx, owner.ID, "CHARGER", 24, 0)
	if err != nil || healed.Power != 4 {
		t.Fatalf("healed=%+v err=%v", healed, err)
	}
	snapshot, _ := state.Snapshot(ctx, owner.ID)
	if len(snapshot.Items) != 0 {
		t.Fatalf("chargers were not consumed: %+v", snapshot.Items)
	}
}
