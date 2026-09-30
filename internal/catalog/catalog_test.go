package catalog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/testfixture"
)

func TestPackSKUOptionalExpansion(t *testing.T) {
	for _, test := range []struct {
		name, expansion, sku, wantError string
	}{
		{name: "event pack without expansion", sku: "SKU_A"},
		{name: "unknown expansion", expansion: "UNKNOWN", sku: "SKU_A", wantError: `validate PackSku.json: expansion "UNKNOWN" is unknown`},
		{name: "unknown sku", sku: "UNKNOWN", wantError: `validate PackMaster.json: sku "UNKNOWN" is unknown`},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := testfixture.MasterData(t)
			for _, table := range []struct {
				file, field, value string
			}{
				{"PackSku.json", "ExpansionID", test.expansion},
				{"PackMaster.json", "SkuID", test.sku},
			} {
				filename := filepath.Join(root, DefaultLocale, table.file)
				data, err := os.ReadFile(filename)
				if err != nil {
					t.Fatal(err)
				}
				var rows []map[string]any
				if err := json.Unmarshal(data, &rows); err != nil {
					t.Fatal(err)
				}
				rows[0][table.field] = table.value
				data, err = json.Marshal(rows)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filename, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			c, err := Open(root)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			pack, err := c.Pack("PACK_A")
			if err != nil || pack.ExpansionID != "" || pack.SkuAssetID != "SKU_A_ASSET" {
				t.Fatalf("event pack = %+v, error = %v", pack, err)
			}
		})
	}
}

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
	if len(c.Expansions()) != 1 || len(c.Packs()) != 5 || len(c.Cosmetics()) != 12 {
		t.Fatalf("catalog incomplete: expansions=%d packs=%d cosmetics=%d", len(c.Expansions()), len(c.Packs()), len(c.Cosmetics()))
	}
	tutorials := c.TutorialCompletions()
	if len(tutorials) != 41 {
		t.Fatalf("tutorial completions = %d, want 41", len(tutorials))
	}
	wantTutorials := map[string]int64{"1011": 1, "2002": 5, "2010": 2, "2161": 2, "2520": 2, "3015": 2}
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
	route, err := c.TutorialExchangeRoute("TUTORIAL_1")
	if err != nil || route.RouteType != 2 || route.DeckID != "1001003" {
		t.Fatalf("tutorial route = %+v err=%v", route, err)
	}
	feed, err := c.TutorialFeed(route.RouteType)
	if err != nil || len(feed.CardIDs) != 5 || feed.RewardCardID != "PK_10_000020_00" {
		t.Fatalf("tutorial feed = %+v err=%v", feed, err)
	}
	setting := c.TutorialPackSetting()
	if setting.PackID != "PACK_A" || setting.Experience != 100 || setting.CeilPoints != 5 || setting.GuaranteeID != "GP_TEST" {
		t.Fatalf("tutorial pack setting = %+v", setting)
	}
	reward, ok := c.TutorialReward("1011", 2)
	if !ok || len(reward.Items) != 1 || reward.Items[0].ItemID != "PK_10_000020_00" {
		t.Fatalf("tutorial reward = %+v ok=%v", reward, ok)
	}
	if rewards := c.LevelRewards(2); len(rewards) != 4 {
		t.Fatalf("level 2 rewards = %+v", rewards)
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
