// Package testfixture creates small, synthetic fixtures shared by integration
// tests. It deliberately contains no extracted game data.
package testfixture

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// MasterData writes a minimal, self-contained master-data tree and returns its
// path. The caller's testing.TB owns and removes the temporary directory.
func MasterData(t testing.TB) string {
	t.Helper()
	root := t.TempDir()
	for _, locale := range []string{"fr_FR", "en_US"} {
		tables := masterDataTables()
		if locale == "en_US" {
			tables["Character.json"][0]["DisplayNameMSID"] = "NAME  (Bulbasaur)"
			tables["PackMaster.json"][0]["NameMSID"] = "PACK  (Synthetic Pack A EN)"
		}
		directory := filepath.Join(root, locale)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		for name, rows := range tables {
			data, err := json.Marshal(rows)
			if err != nil {
				t.Fatalf("marshal fixture %s: %v", name, err)
			}
			if err := os.WriteFile(filepath.Join(directory, name), data, 0o600); err != nil {
				t.Fatalf("write fixture %s: %v", name, err)
			}
		}
	}
	return root
}

func masterDataTables() map[string][]map[string]any {
	empty := []map[string]any{}
	characters := []map[string]any{
		{"CharacterID": "BULBASAUR", "DisplayNameMSID": "NAME  (Bulbizarre)"},
		{"CharacterID": "IVYSAUR", "DisplayNameMSID": "NAME  (Herbizarre)"},
		{"CharacterID": "VENUSAUR", "DisplayNameMSID": "NAME  (Florizarre)"},
		{"CharacterID": "ODDISH", "DisplayNameMSID": "NAME  (Mystherbe)"},
		{"CharacterID": "GLOOM", "DisplayNameMSID": "NAME  (Ortide)"},
		{"CharacterID": "VILEPLUME", "DisplayNameMSID": "NAME  (Rafflesia)"},
		{"CharacterID": "TRAINER", "DisplayNameMSID": "NAME  (Chercheuse)"},
	}
	pokemon := []map[string]any{}
	pokemonCards := []map[string]any{}
	collections := []map[string]any{}
	cardIDs := []string{
		"PK_10_000010_00", "PK_10_000020_00", "PK_10_000030_00",
		"PK_10_000040_00", "PK_10_000050_00", "PK_10_000060_00",
	}
	for index, cardID := range cardIDs {
		pokemonID := []string{"BULBASAUR", "IVYSAUR", "VENUSAUR", "ODDISH", "GLOOM", "VILEPLUME"}[index]
		pokemon = append(pokemon, map[string]any{"PokemonID": pokemonID, "CharacterID": pokemonID, "PokemonTypes": []int{2}})
		pokemonCards = append(pokemonCards, map[string]any{"CardID": cardID, "PokemonID": pokemonID, "Rarity": 100})
		collections = append(collections, map[string]any{"CardID": cardID, "ExpansionID": "A1", "CollectionNumber": index + 1})
	}
	collections = append(collections, map[string]any{"CardID": "TR_10_000080_00", "ExpansionID": "A1", "CollectionNumber": 7})

	levels := []map[string]any{}
	for level := 2; level <= 60; level++ {
		required := int64((level - 1) * 100)
		if level == 7 {
			required = 955
		}
		levels = append(levels, map[string]any{"Level": level, "RequiredExp": required})
	}

	tutorials := []map[string]any{{"TutorialID": "1011", "TutorialStep": 3}}
	for tutorial := 3000; tutorial <= 3015; tutorial++ {
		tutorials = append(tutorials, map[string]any{"TutorialID": strconv.Itoa(tutorial), "TutorialStep": 2})
	}

	packTables := []map[string]any{
		{"PackID": "PACK_A", "PackTableID": "PACK_A_NORMAL", "NameMSID": "PACK_TABLE_NAME_NORMAL  (Normal)", "ProbabilityWeight": 100},
		{"PackID": "PACK_A", "PackTableID": "PACK_A_RARE", "NameMSID": "PACK_TABLE_NAME_RARE  (Rare)", "ProbabilityWeight": 1},
		{"PackID": "PACK_B", "PackTableID": "PACK_B_NORMAL", "NameMSID": "PACK_TABLE_NAME_NORMAL  (Normal)", "ProbabilityWeight": 100},
	}
	labels := []map[string]any{}
	draws := []map[string]any{}
	weightedCards := []map[string]any{}
	for _, tableID := range []string{"PACK_A_NORMAL", "PACK_A_RARE", "PACK_B_NORMAL"} {
		labelID := tableID + "_POOL"
		labels = append(labels, map[string]any{"PackTableID": tableID, "PackTableLabelID": labelID, "DrawCountLabel": "slot", "Label": "R", "ProbabilityWeight": 100})
		for position := 1; position <= 5; position++ {
			draws = append(draws, map[string]any{"PackTableID": tableID, "DrawCountLabel": "slot", "Nth": position})
		}
		for _, cardID := range cardIDs {
			weightedCards = append(weightedCards, map[string]any{"PackTableLabelID": labelID, "CardID": cardID, "ProbabilityWeight": 1})
		}
	}

	return map[string][]map[string]any{
		"Character.json":                     characters,
		"Pokemon.json":                       pokemon,
		"Trainer.json":                       {{"TrainerID": "TRAINER_1", "CharacterID": "TRAINER", "TrainerType": 4}},
		"Expansion.json":                     {{"ExpansionID": "A1", "NameMSID": "EXP  (Synthetic Set)", "LongNameMSID": "EXP  (Synthetic Test Set)", "SeriesID": "TEST"}},
		"ExpansionCollectionNumber.json":     collections,
		"PokemonCard.json":                   pokemonCards,
		"TrainerCard.json":                   {{"CardID": "TR_10_000080_00", "TrainerID": "TRAINER_1", "Rarity": 100}},
		"PackSku.json":                       {{"PackSkuID": "SKU_A", "ExpansionID": "A1", "AssetID": "SKU_A_ASSET"}, {"PackSkuID": "SKU_B", "ExpansionID": "A1", "AssetID": "SKU_B_ASSET"}},
		"PackMaster.json":                    {{"PackID": "PACK_A", "NameMSID": "PACK  (Synthetic Pack A)", "DescriptionMSID": "DESC  (First synthetic pack)", "SkuID": "SKU_A", "AssetID": "PACK_A_ASSET", "PackCeilPointSharedGroupID": "TEST", "FeaturedCardIDs": []string{cardIDs[0]}}, {"PackID": "PACK_B", "NameMSID": "PACK  (Synthetic Pack B)", "DescriptionMSID": "DESC  (Second synthetic pack)", "SkuID": "SKU_B", "AssetID": "PACK_B_ASSET", "PackCeilPointSharedGroupID": "TEST", "FeaturedCardIDs": []string{cardIDs[1]}}},
		"PackTableMaster.json":               packTables,
		"PackTableLabelMaster.json":          labels,
		"PackTableDrawCountLabelMaster.json": draws,
		"PackTableCardMaster.json":           weightedCards,
		"PackShopProduct.json":               {{"PackID": "PACK_A", "PackShopProductID": "PRODUCT_A", "OnePackPrice": 1, "TenPackPrice": 10, "OnePackExp": 1, "TenPackExp": 10}, {"PackID": "PACK_B", "PackShopProductID": "PRODUCT_B", "OnePackPrice": 1, "TenPackPrice": 10, "OnePackExp": 1, "TenPackExp": 10}},
		"Currency.json":                      {{"ID": "SHOPTICKET", "NameMSID": "NAME  (Shop Ticket)", "AssetID": "SHOPTICKET"}, {"ID": "SHINEDUST", "NameMSID": "NAME  (Shinedust)", "AssetID": "SHINEDUST"}, {"ID": "POKEGOLD_FREE", "NameMSID": "NAME  (Free Gold)", "AssetID": "POKEGOLD"}, {"ID": "SHOPTICKET_P", "NameMSID": "NAME  (Premier Ticket)", "AssetID": "PREMIER_SHOPTICKET"}},
		"CollectionFile.json":                empty,
		"CollectionBoard.json":               empty,
		"PeripheralGoods.json":               empty,
		"PackPowerCharger.json":              {{"ID": "PACK_CHARGER_100030", "NameMSID": "NAME  (Pack Hourglass)"}},
		"ChallengePowerCharger.json":         empty,
		"EventPowerCharger.json":             empty,
		"TradePowerCharger.json":             {{"ID": "TRADE_CHAGER_140010", "NameMSID": "NAME  (Trade Hourglass)"}},
		"RewardTicket.json":                  {{"ID": "TICKET_2608000_EV_01", "NameMSID": "NAME  (Event Ticket)"}},
		"RevivalClock.json":                  {{"ID": "REVIVAL_CLOCK_100010", "NameMSID": "NAME  (Rewind Watch)"}},
		"TradeItem.json":                     empty,
		"TradeTicket.json":                   empty,
		"HiddenEventItem.json":               {{"ID": "HIDDEN_EVENT_ITEM_100010", "NameMSID": "NAME  (Synthetic Event Item)"}},
		"PlayerLevelSetting.json":            levels,
		"ProfileDecoration.json":             {{"ID": "PROFILE_ICON_100140_KABIGON", "NameMSID": "NAME  (Synthetic Icon)", "Type": 0}, {"ID": "EMBLEM_1", "NameMSID": "NAME  (Emblem One)", "Type": 1}, {"ID": "EMBLEM_2", "NameMSID": "NAME  (Emblem Two)", "Type": 1}, {"ID": "EMBLEM_3", "NameMSID": "NAME  (Emblem Three)", "Type": 1}, {"ID": "EMBLEM_4", "NameMSID": "NAME  (Emblem Four)", "Type": 1}},
		"CardSkin.json":                      {{"CardSkinID": "SKIN_1", "NameMSID": "NAME  (Synthetic Skin)", "CardSkinResourceIconID": "SKIN_ASSET", "SkinType": 1}},
		"CardFrame.json":                     {{"CardFrameID": "FRAME_1", "NameMSID": "NAME  (Synthetic Frame)", "CardFrameResourceIconID": "FRAME_ASSET"}},
		"CardSkinDropSetting.json":           {{"CardID": cardIDs[0], "CardSkinID": "SKIN_1", "CardSkinAmount": 1}},
		"CardFrameDropSetting.json":          {{"CardID": cardIDs[0], "CardFrameID": "FRAME_1", "CardFrameAmount": 1}},
		"CardExchangeCatalog.json":           {{"CardExchangeCatalogID": "EXCHANGE_SKIN", "CardID": cardIDs[0], "ProductID": "SKIN_1", "ProductType": 1, "ProductAmount": 1}, {"CardExchangeCatalogID": "EXCHANGE_FRAME", "CardID": cardIDs[0], "ProductID": "FRAME_1", "ProductType": 4, "ProductAmount": 1}},
		"ItemShopContent.json":               {{"ContentID": "EVENT_CONTENT", "ItemID": "HIDDEN_EVENT_ITEM_100010", "ItemType": 22, "Amount": 1}},
		"ItemShopPeriodLimit.json":           {{"LimitID": "EVENT_LIMIT", "MaxAmount": 10}},
		"ItemShopProduct.json":               {{"ProductID": "SH_CG_EV_2608000_11", "ShopID": "EVENT_ROCKET_DUMMY", "PriceUnitID": "TICKET_2608000_EV_01", "LimitID": "EVENT_LIMIT", "ProductType": 1, "Price": 1, "ContentIDs": []string{"EVENT_CONTENT"}}},
		"TrophyRewards.json":                 {{"RewardID": "BRONZE_DUST", "ItemID": "SHINEDUST", "ItemType": 7, "Amount": 30}},
		"Trophies.json":                      {{"ID": "TROPHY_100050_Grass_A1", "Type": 14, "TargetIDs": []string{"2"}, "BronzeAmount": 100, "SilverAmount": 200, "GoldAmount": 500, "RainbowAmount": 1000, "BronzeRewards": []string{"BRONZE_DUST"}}},
		"TutorialRewards.json":               tutorials,
		"RentalDeck.json":                    empty,
		"SoloStepupBattle.json":              empty,
		"SoloRandomBattle.json":              empty,
		"SoloEventBattle.json":               empty,
		"ActivityMissions.json":              empty,
		"CardMissions.json":                  empty,
		"MissionReward.json":                 empty,
		"MissionGroupRewardStep.json":        empty,
		"ProfileMessage.json":                {{"ID": "PROFILE_MESSAGE_1", "MessageMSID": "MESSAGE  (Ready to play!)"}},
		"PackPower.json":                     {{"PackPowerID": "PACK_POWER", "AutoHealLimit": 2, "HealSecondsPerPower": 43200, "PokeGoldUseLimit": 10}},
		"AgreementSetting.json":              {{"AgreementKey": 0, "AgreementVersion": "1"}, {"AgreementKey": 1, "AgreementVersion": "1"}, {"AgreementKey": 2, "AgreementVersion": "1"}},
	}
}
