package playerapi

import (
	"testing"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	cardpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card"
	itempb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	languagepb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/language"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
)

func TestPackPurchaseAcquisitionOnlyReturnsChangedResources(t *testing.T) {
	t.Parallel()
	now := time.Now().UTC()
	profile := player.Profile{
		Player: store.Player{Level: 12, Experience: 3456},
		Cards: []player.OwnedCard{
			{Definition: catalog.Card{ID: "acquired", ExpansionIDs: []string{"A1"}}, Quantity: 3, FirstReceivedAt: now, LastReceivedAt: now},
			{Definition: catalog.Card{ID: "unrelated", ExpansionIDs: []string{"A2"}}, Quantity: 99, FirstReceivedAt: now, LastReceivedAt: now},
		},
		Currencies: []player.OwnedCurrency{
			{Definition: catalog.Currency{ProtocolType: int32(itempb.Currency_Types_TYPE_BRIGHT_SAND)}, Quantity: 120},
			{Definition: catalog.Currency{ProtocolType: int32(itempb.Currency_Types_TYPE_SHOP_TICKET)}, Quantity: 500},
		},
	}
	acquired := &itempb.InventoryItems{
		CardInstances: []*itempb.CardInstance{{CardInstance: &cardpb.CardInstance{CardId: "acquired", ExpansionId: "A1"}, Amount: 1}},
		Currencies:    []*itempb.Currency{{Type: itempb.Currency_Types_TYPE_BRIGHT_SAND, Amount: 20}},
	}

	result := packPurchaseAcquisition(profile, acquired, map[string]int64{"acquired": 1}, store.PackOpening{ShineDustReward: 20}, languagepb.Language_LANGUAGE_EN)

	if got := result.GetItemState().GetCardStocks(); len(got) != 1 || got[0].GetCardId() != "acquired" || got[0].GetCardAmount() != 3 {
		t.Fatalf("changed card stocks = %+v, want only acquired card total", got)
	}
	if got := result.GetItemState().GetCurrencies(); len(got) != 1 || got[0].GetType() != itempb.Currency_Types_TYPE_BRIGHT_SAND || got[0].GetAmount() != 120 {
		t.Fatalf("changed currencies = %+v, want only bright sand total", got)
	}
	if got := result.GetItemState().GetExpStock(); got.GetCurrentLevel() != 12 || got.GetExp() != 3456 {
		t.Fatalf("experience state = %+v, want level 12 and 3456 experience", got)
	}
	if len(result.GetItemState().GetCardFrameStocks()) != 0 || len(result.GetItemState().GetCardSkinStocks()) != 0 || len(result.GetItemState().GetProfileDecorations()) != 0 {
		t.Fatalf("purchase response leaked unrelated cosmetics: %+v", result.GetItemState())
	}
	if len(result.GetAcceptedItems().GetCardInstances()) != 1 || len(result.GetAcceptedItems().GetCurrencies()) != 1 {
		t.Fatalf("accepted items do not mirror acquired items: %+v", result.GetAcceptedItems())
	}
}
