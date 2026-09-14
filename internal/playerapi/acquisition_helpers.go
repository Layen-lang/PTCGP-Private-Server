package playerapi

import (
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	cardpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card"
	itempb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	itemacquisition "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item_acquisition"
	languagepb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/language"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// acquisitionFromChanges mirrors the inventory delta returned by the official
// server after a claim. The authoritative post-transaction totals remain in
// ItemState; AcquiredItems and AcceptedItems describe this claim only.
func acquisitionFromChanges(profile player.Profile, changes []store.ShopInventoryChange, master player.CatalogView) *itemacquisition.ItemAcquisitionResult {
	result := emptyItemAcquisitionResult(profile)
	language := languageForProfile(profile)
	for _, change := range changes {
		appendInventoryChange(result.AcquiredItems, change, master, language)
		appendInventoryChange(result.AcceptedItems, change, master, language)
	}
	return result
}

func appendInventoryChange(items *itempb.InventoryItems, change store.ShopInventoryChange, master player.CatalogView, language languagepb.Language) {
	if items == nil || change.Amount <= 0 {
		return
	}
	amount := uint64(change.Amount)
	switch change.Kind {
	case "card":
		expansionID := ""
		for _, definition := range master.Cards {
			if definition.ID == change.ID && len(definition.ExpansionIDs) > 0 {
				expansionID = definition.ExpansionIDs[0]
				break
			}
		}
		items.CardInstances = append(items.CardInstances, &itempb.CardInstance{CardInstance: &cardpb.CardInstance{CardId: change.ID, Lang: language, ExpansionId: expansionID}, Amount: change.Amount})
	case "currency":
		for _, definition := range master.Currencies {
			if definition.ID == change.ID {
				items.Currencies = append(items.Currencies, &itempb.Currency{Type: itempb.Currency_Types_Type(definition.ProtocolType), Amount: amount})
				return
			}
		}
	case "profile_decoration":
		variant := 0
		for _, definition := range master.Cosmetics {
			if definition.ID == change.ID {
				variant = definition.Variant
				break
			}
		}
		decorationType := itempb.ProfileDecoration_Types_TYPE_EMBLEM
		if variant == 0 {
			decorationType = itempb.ProfileDecoration_Types_TYPE_ICON
		}
		items.ProfileDecorations = append(items.ProfileDecorations, &itempb.ProfileDecoration{Type: decorationType, Id: change.ID, Amount: amount, ObtainedAt: timestamppb.New(time.Now().UTC())})
	case "rental_deck":
		items.RentalDecks = append(items.RentalDecks, &itempb.RentalDeck{RentalDeckId: change.ID, Amount: change.Amount})
	case "theme_deck_recipe":
		items.ThemeDeckRecipes = append(items.ThemeDeckRecipes, &itempb.ThemeDeckRecipe{Id: change.ID, Amount: change.Amount})
	case "item":
		appendCatalogItem(items, change, master)
	}
}

func appendCatalogItem(items *itempb.InventoryItems, change store.ShopInventoryChange, master player.CatalogView) {
	definition := catalog.Item{ID: change.ID, Kind: catalog.ItemKind(change.SubID)}
	for _, candidate := range master.Items {
		if candidate.ID == change.ID {
			definition = candidate
			break
		}
	}
	amount := uint64(change.Amount)
	switch definition.Kind {
	case catalog.ItemPackCharger:
		items.PackPowerChargers = append(items.PackPowerChargers, &itempb.PackPowerCharger{Type: itempb.PackPowerCharger_Types_TYPE_LARGE, Amount: amount})
	case catalog.ItemChallengeCharger:
		items.ChallengePowerChargers = append(items.ChallengePowerChargers, &itempb.ChallengePowerCharger{Type: itempb.ChallengePowerCharger_Types_TYPE_LARGE, Amount: amount})
	case catalog.ItemEventCharger:
		items.EventPowerChargers = append(items.EventPowerChargers, &itempb.EventPowerCharger{Id: change.ID, Amount: amount})
	case catalog.ItemTradeCharger:
		items.TradePowerChargers = append(items.TradePowerChargers, &itempb.TradePowerCharger{Id: change.ID, Amount: change.Amount})
	case catalog.ItemRewardTicket:
		items.RewardTickets = append(items.RewardTickets, &itempb.RewardTicket{Type: itempb.RewardTicket_Types_Type(definition.Variant + 1), Id: change.ID, Amount: amount})
	case catalog.ItemRevivalClock:
		items.RevivalClocks = append(items.RevivalClocks, &itempb.RevivalClock{Amount: amount})
	case catalog.ItemTrade:
		items.TradeItems = append(items.TradeItems, &itempb.TradeItem{Id: change.ID, Amount: amount})
	case catalog.ItemPeripheral:
		items.PeripheralGoods = append(items.PeripheralGoods, &itempb.PeripheralGoods{Type: itempb.PeripheralGoods_Types_Type(definition.Variant + 1), Id: change.ID, Amount: amount, ObtainedAt: timestamppb.New(time.Now().UTC())})
	case catalog.ItemHiddenEvent:
		// The current checked-in protocol schema has no dedicated hidden-event
		// item message. Preserve the ID and quantity in the generic peripheral
		// goods collection rather than dropping a successful shop reward.
		items.PeripheralGoods = append(items.PeripheralGoods, &itempb.PeripheralGoods{Type: itempb.PeripheralGoods_Types_TYPE_UNSPECIFIED, Id: change.ID, Amount: amount, ObtainedAt: timestamppb.New(time.Now().UTC())})
	}
}
