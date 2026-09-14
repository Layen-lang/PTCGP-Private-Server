package packlab

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/testfixture"
)

func TestIllegalOpeningIsPersistentAndIdempotent(t *testing.T) {
	ctx := context.Background()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	player, err := state.CreatePlayer(ctx, "Pack Test", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := state.UpdatePlayerLocale(ctx, player.ID, "en_US", "US"); err != nil {
		t.Fatal(err)
	}
	_, product := firstDrawableProduct(t, master)
	seed := int64(42)
	engine := New(state, master)
	rule := store.PackRule{
		Mode: "illegal", ReturnPackCount: 2, FreeOpenings: true, FixedSeed: &seed,
		Illegal: store.IllegalPackRule{CardCount: 7, AllowDuplicates: true},
	}
	if err := engine.SaveRule(ctx, rule); err != nil {
		t.Fatal(err)
	}

	first, replayed, err := engine.Open(ctx, OpenInput{PlayerID: player.ID, TransactionID: "tx-1", ProductID: product.ID, RequestedCount: 1, Share: true})
	if err != nil {
		t.Fatal(err)
	}
	if replayed || len(first.Packs) != 1 {
		t.Fatalf("first opening replayed=%v packs=%d", replayed, len(first.Packs))
	}
	second, replayed, err := engine.Open(ctx, OpenInput{PlayerID: player.ID, TransactionID: "tx-1", ProductID: product.ID, RequestedCount: 1, Share: true})
	if err != nil {
		t.Fatal(err)
	}
	if !replayed || second.ID != first.ID {
		t.Fatalf("replay = %v id=%q want %q", replayed, second.ID, first.ID)
	}
	snapshot, err := state.Snapshot(ctx, player.ID)
	if err != nil {
		t.Fatal(err)
	}
	var cardCount int64
	for _, card := range snapshot.Cards {
		cardCount += card.Quantity
		if len(card.Languages) != 1 || card.Languages[0].Language != 2 || card.Languages[0].Quantity != card.Quantity {
			t.Fatalf("persisted card languages = %+v, want English quantities", card.Languages)
		}
	}
	if cardCount != 7 {
		t.Fatalf("persisted cards = %d, want 7: %#v", cardCount, snapshot.Cards)
	}
}

func TestIllegalOpeningUsesBoundedX10PackAndCardCounts(t *testing.T) {
	ctx := context.Background()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	pack, _ := firstDrawableProduct(t, master)
	engine := New(state, master)
	rule := store.PackRule{Mode: "illegal", ReturnPackCount: 10, Illegal: store.IllegalPackRule{CardCount: 11, AllowDuplicates: true}}
	if err := engine.SaveRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	single, err := engine.Preview(ctx, PreviewInput{PackID: pack.ID, RequestedCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(single.Packs) != 1 {
		t.Fatalf("single opening returned packs = %d, want 1", len(single.Packs))
	}
	preview, err := engine.Preview(ctx, PreviewInput{PackID: pack.ID, RequestedCount: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Packs) != 10 {
		t.Fatalf("x10 opening returned packs = %d, want 10", len(preview.Packs))
	}
	for index, returned := range preview.Packs {
		if len(returned.Cards) != 11 {
			t.Fatalf("pack %d cards = %d, want 11", index, len(returned.Cards))
		}
		if !tableHasKind(pack, returned.TableID, "normal") {
			t.Fatalf("pack %d table %q is not a normal probability table", index, returned.TableID)
		}
	}
}

func TestIllegalOpeningFiltersGlobalCatalogAndPreventsPerPackDuplicates(t *testing.T) {
	ctx := context.Background()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	pack, _ := firstDrawableProduct(t, master)
	engine := New(state, master)

	var filter store.IllegalPackRule
	for _, card := range master.Cards() {
		if len(card.ExpansionIDs) == 0 {
			continue
		}
		candidate := store.IllegalPackRule{ExpansionIDs: []string{card.ExpansionIDs[0]}, Rarities: []int{card.Rarity}, CardKinds: []string{string(card.Kind)}}
		if engine.IllegalPoolSize(candidate) >= 5 {
			filter = candidate
			break
		}
	}
	if len(filter.ExpansionIDs) == 0 {
		t.Fatal("catalog has no filter combination with at least five cards")
	}
	filter.CardCount = 5
	rule := store.PackRule{Mode: "illegal", ReturnPackCount: 3, Illegal: filter}
	if err := engine.SaveRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	preview, err := engine.Preview(ctx, PreviewInput{PackID: pack.ID, RequestedCount: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Packs) != 3 {
		t.Fatalf("returned packs = %d, want configured 3", len(preview.Packs))
	}
	for packIndex, returned := range preview.Packs {
		seen := make(map[string]bool)
		for _, cardID := range returned.Cards {
			if seen[cardID] {
				t.Fatalf("pack %d contains duplicate %q", packIndex, cardID)
			}
			seen[cardID] = true
			card, err := master.Card(cardID)
			if err != nil {
				t.Fatal(err)
			}
			if card.Rarity != filter.Rarities[0] || string(card.Kind) != filter.CardKinds[0] || !containsString(card.ExpansionIDs, filter.ExpansionIDs[0]) {
				t.Fatalf("card %q escaped filters: %+v", cardID, card)
			}
		}
	}
}

func TestIllegalRuleRejectsEmptyOrTooSmallPool(t *testing.T) {
	ctx := context.Background()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	engine := New(state, master)
	if err := engine.SaveRule(ctx, store.PackRule{Mode: "illegal", ReturnPackCount: 11, Illegal: store.IllegalPackRule{CardCount: 5, AllowDuplicates: true}}); err == nil {
		t.Fatal("more than 10 returned packs were accepted")
	}
	if err := engine.SaveRule(ctx, store.PackRule{Mode: "illegal", ReturnPackCount: 10, Illegal: store.IllegalPackRule{CardCount: 12, AllowDuplicates: true}}); err == nil {
		t.Fatal("more than 11 cards per booster were accepted")
	}
	if err := engine.SaveRule(ctx, store.PackRule{Mode: "illegal", ReturnPackCount: 1, Illegal: store.IllegalPackRule{CardCount: 1, ExpansionIDs: []string{"UNKNOWN"}}}); err == nil {
		t.Fatal("empty filtered pool was accepted")
	}
	poolSize := engine.IllegalPoolSize(store.IllegalPackRule{})
	if err := engine.SaveRule(ctx, store.PackRule{Mode: "illegal", ReturnPackCount: 1, Illegal: store.IllegalPackRule{CardCount: poolSize + 1}}); err == nil {
		t.Fatal("card count larger than no-duplicate pool was accepted")
	}
}

func TestFiveCardRuleKeepsOtherPacksOfficial(t *testing.T) {
	ctx := context.Background()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	player, err := state.CreatePlayer(ctx, "Rule Test", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	target, _ := firstDrawableProduct(t, master)
	var other catalog.Pack
	for _, candidate := range master.Packs() {
		if candidate.ID != target.ID && len(positiveTables(candidate.Tables)) > 0 {
			other = candidate
			break
		}
	}
	if other.ID == "" {
		t.Fatal("catalog has no second drawable pack")
	}
	engine := New(state, master)
	cardID := target.Tables[0].Slots[0].Pools[0].Cards[0].CardID
	rule := store.PackRule{
		Mode: "table", TargetPackID: target.ID, TableKind: target.Tables[0].Kind, ReturnPackCount: 1,
		Packs: [][]store.PackRuleSlot{{
			{CardID: cardID, Locked: true}, {CardID: cardID, Locked: true}, {CardID: cardID, Locked: true},
			{CardID: cardID, Locked: true}, {CardID: cardID, Locked: true},
		}},
	}
	if err := engine.SaveRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	preview, err := engine.Preview(ctx, PreviewInput{PlayerID: player.ID, PackID: other.ID, RequestedCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Packs) != 1 || !packOwnsTable(other, preview.Packs[0].TableID) {
		t.Fatalf("other pack did not use its official tables: %#v", preview.Packs)
	}
}

func TestGlobalTableRuleAppliesToEveryPack(t *testing.T) {
	ctx := context.Background()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	packs := master.Packs()
	kind := commonTableKind(t, packs)
	engine := New(state, master)
	if err := engine.SaveRule(ctx, store.PackRule{Mode: "table", TableKind: kind, ReturnPackCount: 1}); err != nil {
		t.Fatal(err)
	}
	for _, pack := range packs {
		preview, err := engine.Preview(ctx, PreviewInput{PackID: pack.ID, RequestedCount: 1})
		if err != nil {
			t.Fatalf("preview pack %q: %v", pack.ID, err)
		}
		if len(preview.Packs) != 1 || !tableHasKind(pack, preview.Packs[0].TableID, kind) {
			t.Fatalf("pack %q did not use table kind %q: %#v", pack.ID, kind, preview.Packs)
		}
	}
}

func TestGlobalTableRuleFallsBackForUnsupportedPack(t *testing.T) {
	ctx := context.Background()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	kind, supported, unsupported := tableKindWithFallback(t, master.Packs())
	engine := New(state, master)
	if err := engine.SaveRule(ctx, store.PackRule{Mode: "table", TableKind: kind, ReturnPackCount: 1}); err != nil {
		t.Fatal(err)
	}
	forced, err := engine.Preview(ctx, PreviewInput{PackID: supported.ID, RequestedCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(forced.Packs) != 1 || !tableHasKind(supported, forced.Packs[0].TableID, kind) {
		t.Fatalf("supported pack %q did not use table kind %q: %#v", supported.ID, kind, forced.Packs)
	}
	official, err := engine.Preview(ctx, PreviewInput{PackID: unsupported.ID, RequestedCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(official.Packs) != 1 || !packOwnsTable(unsupported, official.Packs[0].TableID) {
		t.Fatalf("unsupported pack %q did not fall back to an official table: %#v", unsupported.ID, official.Packs)
	}
}

func TestTableRuleAcceptsFiveExplicitCards(t *testing.T) {
	ctx := context.Background()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()

	pack, _ := firstDrawableProduct(t, master)
	cardID := pack.Tables[0].Slots[0].Pools[0].Cards[0].CardID
	rule := store.PackRule{
		Mode: "table", TargetPackID: pack.ID, TableKind: pack.Tables[0].Kind, ReturnPackCount: 1,
		Packs: [][]store.PackRuleSlot{{
			{CardID: cardID, Locked: true}, {CardID: cardID, Locked: true}, {CardID: cardID, Locked: true},
			{CardID: cardID, Locked: true}, {CardID: cardID, Locked: true},
		}},
	}
	engine := New(state, master)
	if err := engine.SaveRule(ctx, rule); err != nil {
		t.Fatal(err)
	}
	preview, err := engine.Preview(ctx, PreviewInput{PackID: pack.ID, RequestedCount: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Packs) != 1 || len(preview.Packs[0].Cards) != 5 {
		t.Fatalf("preview packs = %#v", preview.Packs)
	}
	for index, got := range preview.Packs[0].Cards {
		if got != cardID {
			t.Fatalf("card %d = %q, want %q", index, got, cardID)
		}
	}
}

func firstDrawableProduct(t *testing.T, master *catalog.Catalog) (catalog.Pack, catalog.PackProduct) {
	t.Helper()
	for _, pack := range master.Packs() {
		if len(pack.Tables) > 0 && len(pack.Products) > 0 && len(pack.Tables[0].Slots) > 0 && len(pack.Tables[0].Slots[0].Pools) > 0 && len(pack.Tables[0].Slots[0].Pools[0].Cards) > 0 {
			return pack, pack.Products[0]
		}
	}
	t.Fatal("catalog has no drawable product")
	return catalog.Pack{}, catalog.PackProduct{}
}

func commonTableKind(t *testing.T, packs []catalog.Pack) string {
	t.Helper()
	if len(packs) == 0 {
		t.Fatal("catalog has no packs")
	}
	for _, candidate := range packs[0].Tables {
		if packHasTableKindInEveryPack(packs, candidate.Kind) {
			return candidate.Kind
		}
	}
	t.Fatal("catalog has no table kind common to every pack")
	return ""
}

func tableKindWithFallback(t *testing.T, packs []catalog.Pack) (string, catalog.Pack, catalog.Pack) {
	t.Helper()
	for _, supported := range packs {
		for _, table := range supported.Tables {
			for _, unsupported := range packs {
				if unsupported.ID != supported.ID && !packHasTableKind(unsupported, table.Kind) && len(positiveTables(unsupported.Tables)) > 0 {
					return table.Kind, supported, unsupported
				}
			}
		}
	}
	t.Fatal("catalog has no table kind requiring an official fallback")
	return "", catalog.Pack{}, catalog.Pack{}
}

func packHasTableKindInEveryPack(packs []catalog.Pack, kind string) bool {
	for _, pack := range packs {
		if !packHasTableKind(pack, kind) {
			return false
		}
	}
	return true
}

func packOwnsTable(pack catalog.Pack, tableID string) bool {
	for _, table := range pack.Tables {
		if table.ID == tableID {
			return true
		}
	}
	return false
}

func tableHasKind(pack catalog.Pack, tableID, kind string) bool {
	for _, table := range pack.Tables {
		if table.ID == tableID && table.Kind == kind {
			return true
		}
	}
	return false
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
