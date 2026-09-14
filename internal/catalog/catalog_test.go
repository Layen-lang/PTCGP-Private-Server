package catalog

import (
	"errors"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/testfixture"
)

func TestLoadsCatalogAndFindsCoreEntities(t *testing.T) {
	t.Parallel()
	c, err := Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	card, err := c.Card("PK_10_000010_00")
	if err != nil {
		t.Fatal(err)
	}
	if card.Name != "Bulbizarre" || len(card.ExpansionIDs) == 0 || card.ExpansionIDs[0] != "A1" {
		t.Fatalf("card = %+v", card)
	}
	if len(card.PokemonTypes) != 1 || card.PokemonTypes[0] != 2 {
		t.Fatalf("pokemon card types = %+v", card.PokemonTypes)
	}
	trainer, err := c.Card("TR_10_000080_00")
	if err != nil || trainer.TrainerType != 4 {
		t.Fatalf("trainer card type = %+v err=%v", trainer, err)
	}
	trophy, err := c.Trophy("TROPHY_100050_Grass_A1")
	if err != nil || trophy.Type != 14 || len(trophy.TargetIDs) != 1 || trophy.TargetIDs[0] != 2 || trophy.Amounts[3] != 1000 || trophy.BrightSandRewards[0] != 30 {
		t.Fatalf("trophy = %+v err=%v", trophy, err)
	}
	item, err := c.Item("PACK_CHARGER_100030")
	if err != nil {
		t.Fatal(err)
	}
	if item.Name != "Pack Hourglass" || item.Kind != ItemPackCharger {
		t.Fatalf("item = %+v", item)
	}
	level, err := c.Level(7)
	if err != nil {
		t.Fatal(err)
	}
	if level.RequiredExp != 955 {
		t.Fatalf("level = %+v", level)
	}
	gold, err := c.Currency("POKEGOLD_FREE")
	if err != nil {
		t.Fatal(err)
	}
	if gold.AssetID != "POKEGOLD" {
		t.Fatalf("gold currency = %+v", gold)
	}
	premierTicket, err := c.Currency("SHOPTICKET_P")
	if err != nil {
		t.Fatal(err)
	}
	if premierTicket.AssetID != "PREMIER_SHOPTICKET" {
		t.Fatalf("premier ticket currency = %+v", premierTicket)
	}
	tradeCharger, err := c.Item("TRADE_CHAGER_140010")
	if err != nil || tradeCharger.Kind != ItemTradeCharger {
		t.Fatalf("trade charger = %+v err=%v", tradeCharger, err)
	}
	hiddenEventItem, err := c.Item("HIDDEN_EVENT_ITEM_100010")
	if err != nil || hiddenEventItem.Kind != ItemHiddenEvent {
		t.Fatalf("hidden event item = %+v err=%v", hiddenEventItem, err)
	}
	if len(c.CardSkinGrants()) != 1 || len(c.CardFrameGrants()) != 1 {
		t.Fatalf("card cosmetic grants: skins=%d frames=%d", len(c.CardSkinGrants()), len(c.CardFrameGrants()))
	}
	if len(c.CardSkinCompatibilities()) != 1 || len(c.CardFrameCompatibilities()) != 1 {
		t.Fatalf("card cosmetic compatibilities: skins=%d frames=%d", len(c.CardSkinCompatibilities()), len(c.CardFrameCompatibilities()))
	}
	if len(c.Expansions()) != 1 || len(c.Packs()) != 2 || len(c.Cosmetics()) != 7 {
		t.Fatalf("catalog incomplete: expansions=%d packs=%d cosmetics=%d", len(c.Expansions()), len(c.Packs()), len(c.Cosmetics()))
	}
	tutorials := c.TutorialCompletions()
	if len(tutorials) != 41 {
		t.Fatalf("tutorial completions = %d, want 41", len(tutorials))
	}
	wantTutorials := map[string]int64{"1011": 3, "2002": 5, "2010": 2, "2161": 2, "2520": 2, "3015": 2}
	for _, tutorial := range tutorials {
		if want, ok := wantTutorials[tutorial.ID]; ok {
			if tutorial.Step != want {
				t.Fatalf("tutorial %s step = %d, want %d", tutorial.ID, tutorial.Step, want)
			}
			delete(wantTutorials, tutorial.ID)
		}
	}
	if len(wantTutorials) != 0 {
		t.Fatalf("missing tutorial completions: %v", wantTutorials)
	}
}

func TestUnknownIdentifiersAreRejected(t *testing.T) {
	t.Parallel()
	c, err := Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Card("NOPE"); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("card error = %v", err)
	}
	if _, err := c.Item("NOPE"); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("item error = %v", err)
	}
	if _, err := c.Level(999); !errors.Is(err, ErrUnknownID) {
		t.Fatalf("level error = %v", err)
	}
}

func TestLocalizedTextParser(t *testing.T) {
	if got := localized(`ID  (Booster[C:Nbsp ]Mewtwo [Text:Char v="X" ])`); got != "Booster Mewtwo" {
		t.Fatalf("localized = %q", got)
	}
}

func TestRegistrySelectsLocalizedCatalogAndFallsBack(t *testing.T) {
	t.Parallel()
	registry, err := OpenRegistry(testfixture.MasterData(t), DefaultLocale, FallbackLocale)
	if err != nil {
		t.Fatal(err)
	}
	french, err := registry.For("fr_FR").Card("PK_10_000010_00")
	if err != nil || french.Name != "Bulbizarre" {
		t.Fatalf("French card=%+v err=%v", french, err)
	}
	english, err := registry.For("en_US").Card("PK_10_000010_00")
	if err != nil || english.Name != "Bulbasaur" {
		t.Fatalf("English card=%+v err=%v", english, err)
	}
	englishPack, err := registry.For("en_US").Pack("PACK_A")
	if err != nil || englishPack.Name != "Synthetic Pack A EN" {
		t.Fatalf("English pack=%+v err=%v", englishPack, err)
	}
	unknown, _ := registry.For("../../invalid").Card("PK_10_000010_00")
	if unknown.Name != french.Name {
		t.Fatalf("unknown locale must fall back to French, got %+v", unknown)
	}
}
