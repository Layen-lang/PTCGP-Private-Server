package playerapi

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/packlab"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	cardpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card"
	item "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	itemacquisition "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item_acquisition"
	language "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/language"
	order "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/order"
	packpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/pack"
	packshop "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/pack_shop"
	resources "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/player_resources"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type packServer struct {
	api.UnimplementedPackServer
	master *catalog.Catalog
	packs  *packlab.Engine
}

func (s packServer) GetPackPowerV1(ctx context.Context, _ *api.PackGetPackPowerV1_Types_Request) (*api.PackGetPackPowerV1_Types_Response, error) {
	resolved, ok := ctx.Value(playerContextKey{}).(store.Player)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing local player")
	}
	state, err := s.packs.State(ctx, resolved.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "local pack state unavailable")
	}
	response := &api.PackGetPackPowerV1_Types_Response{}
	for _, setting := range s.master.HomeSettings().PackPowers {
		amount := int64(0)
		updated := time.Now()
		if setting.ID == "PACK_POWER_NORMAL" {
			amount = state.Power
			updated = state.PowerUpdatedAt
		}
		response.PackPowers = append(response.PackPowers, &packpb.PackPower{Amount: amount, LastAutoHealedAt: timestamppb.New(updated), AutoHealLimit: setting.AutoHealLimit, HealSecPerPower: setting.HealSecondsPerPower, ManualHealLimit: setting.PokeGoldUseLimit, PackPowerId: setting.ID})
	}
	return response, nil
}

func (s packServer) GetDetailV2(_ context.Context, request *api.PackGetDetailV2_Types_Request) (*api.PackGetDetailV2_Types_Response, error) {
	pack, err := s.master.Pack(request.GetPackId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	detail := &packpb.PackDetail{PackId: pack.ID, ExpansionId: pack.ExpansionID, PackTables: make(map[string]*packpb.PackDetail_Types_PackTable)}
	var tableTotal int64
	for _, table := range pack.Tables {
		if table.Weight > 0 {
			tableTotal += table.Weight
		}
	}
	for _, table := range pack.Tables {
		detail.PackTableWeights = append(detail.PackTableWeights, &packpb.PackDetail_Types_PackTableWeight{PackTableId: table.ID, ProbabilityString: probability(table.Weight, tableTotal)})
		protoTable := &packpb.PackDetail_Types_PackTable{PackTableId: table.ID, DrawCountCardTables: make(map[string]*packpb.PackDetail_Types_PackTable_Types_CardTable)}
		seenLabels := make(map[string]bool)
		for _, slot := range table.Slots {
			if seenLabels[slot.Label] {
				continue
			}
			seenLabels[slot.Label] = true
			protoTable.DrawCountLabels = append(protoTable.DrawCountLabels, slot.Label)
			cardTable := &packpb.PackDetail_Types_PackTable_Types_CardTable{LabelItems: make(map[string]*packpb.PackDetail_Types_PackTable_Types_CardTable_Types_CardTableLabelItems)}
			var poolTotal int64
			for _, pool := range slot.Pools {
				poolTotal += pool.Weight
			}
			for _, pool := range slot.Pools {
				cardTable.LabelWeights = append(cardTable.LabelWeights, &packpb.PackDetail_Types_PackTable_Types_CardTable_Types_LabelWeight{Label: pool.Rarity, ProbabilityString: probability(pool.Weight, poolTotal)})
				items := &packpb.PackDetail_Types_PackTable_Types_CardTable_Types_CardTableLabelItems{}
				var cardTotal int64
				for _, card := range pool.Cards {
					cardTotal += card.Weight
				}
				for _, card := range pool.Cards {
					items.CardWeights = append(items.CardWeights, &packpb.PackDetail_Types_PackTable_Types_CardTable_Types_CardTableLabelItems_Types_CardWeight{CardId: card.CardID, ProbabilityString: probability(card.Weight, cardTotal)})
				}
				cardTable.LabelItems[pool.Rarity] = items
			}
			protoTable.DrawCountCardTables[slot.Label] = cardTable
		}
		detail.PackTables[table.ID] = protoTable
	}
	return &api.PackGetDetailV2_Types_Response{PackDetail: detail, PackConsistentToken: "local:" + pack.ID}, nil
}

type packShopServer struct {
	api.UnimplementedPackShopServer
	master  *catalog.Catalog
	players *player.Manager
	packs   *packlab.Engine
}

func (s packShopServer) PurchaseV2(ctx context.Context, request *api.PackShopPurchaseV2_Types_Request) (*api.PackShopPurchaseV2_Types_Response, error) {
	resolved, ok := ctx.Value(playerContextKey{}).(store.Player)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing local player")
	}
	requested := int(request.GetAmount())
	if requested != 1 && requested != 10 {
		return nil, status.Error(codes.InvalidArgument, "amount must be ONE or TEN")
	}
	opening, replayed, err := s.packs.Open(ctx, packlab.OpenInput{PlayerID: resolved.ID, TransactionID: request.GetTransactionId(), ProductID: request.GetProductId(), RequestedCount: requested, Share: request.GetDoShare()})
	if err != nil {
		switch {
		case errors.Is(err, store.ErrInsufficientResources):
			return nil, status.Error(codes.FailedPrecondition, "insufficient local pack power or Poke Gold")
		case errors.Is(err, packlab.ErrInvalidRule), errors.Is(err, catalog.ErrUnknownID):
			return nil, status.Error(codes.InvalidArgument, err.Error())
		default:
			return nil, status.Error(codes.Internal, "local pack opening failed")
		}
	}
	pack, err := s.master.Pack(opening.PackID)
	if err != nil {
		return nil, status.Error(codes.Internal, "opened pack is absent from catalog")
	}
	profile, err := s.players.Profile(ctx, resolved.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot reload local profile")
	}
	cardLanguage := languageForProfile(profile)
	if !replayed {
		_ = s.players.AddAction(ctx, resolved.ID, player.ActionPackOpened, pack.ID, int64(len(opening.Packs)), true)
		_ = s.players.AddAction(ctx, resolved.ID, player.ActionPackOpenTotal, "", int64(len(opening.Packs)), true)
		_ = s.players.AddAction(ctx, resolved.ID, player.ActionPackByExpansion, pack.ExpansionID, int64(len(opening.Packs)), true)
		cardCounts := make(map[string]int64)
		for _, openedPack := range opening.Packs {
			for _, cardID := range openedPack.Cards {
				cardCounts[cardID]++
			}
		}
		for cardID, count := range cardCounts {
			_ = s.players.AddAction(ctx, resolved.ID, player.ActionGetCards, cardID, count, true)
			_ = s.players.AddAction(ctx, resolved.ID, player.ActionCardGetTotal, "", count, true)
		}
	}
	response := &api.PackShopPurchaseV2_Types_Response{
		PurchaseOrder: &packshop.PackShopPurchaseOrder{
			TransactionId: opening.TransactionID, OrderId: opening.ID, OrderedAt: timestamppb.New(opening.CreatedAt), ExpansionId: pack.ExpansionID,
			Sku: pack.AssetID, ProductId: opening.ProductID,
			Consumes: &order.OrderResources{PackStamina: &order.OrderResources_Types_PackStamina{Value: opening.PowerCost}},
			Produces: &order.OrderResources{Packs: []*item.Pack{{Id: pack.ID, Amount: uint64(len(opening.Packs))}}},
		},
		PackCeilPoint: &item.PackCeilPoint{Amount: opening.CeilPointReward, PackCeilPointSharedGroupId: pack.CeilGroupID},
	}
	acquired := &item.InventoryItems{}
	if opening.ShineDustReward > 0 {
		acquired.Currencies = append(acquired.Currencies, &item.Currency{Type: item.Currency_Types_TYPE_BRIGHT_SAND, Amount: uint64(opening.ShineDustReward)})
	}
	if opening.ExperienceReward > 0 {
		acquired.Exps = append(acquired.Exps, &item.Exp{Amount: opening.ExperienceReward})
	}
	if opening.CeilPointReward > 0 {
		acquired.PackCeilPoints = append(acquired.PackCeilPoints, &item.PackCeilPoint{Amount: opening.CeilPointReward, PackCeilPointSharedGroupId: pack.CeilGroupID})
	}
	acquiredCardCounts := make(map[string]int64)
	for packIndex, openedPack := range opening.Packs {
		produces := &order.OrderResources{}
		for _, cardID := range openedPack.Cards {
			expansionID := pack.ExpansionID
			if definition, err := s.master.Card(cardID); err == nil && len(definition.ExpansionIDs) > 0 {
				expansionID = definition.ExpansionIDs[0]
			}
			instance := &item.CardInstance{CardInstance: &cardpb.CardInstance{CardId: cardID, Lang: cardLanguage, ExpansionId: expansionID}, Amount: 1}
			produces.CardInstances = append(produces.CardInstances, instance)
			acquired.CardInstances = append(acquired.CardInstances, instance)
			acquiredCardCounts[cardID]++
		}
		response.UnpackOrders = append(response.UnpackOrders, &packpb.PackUnpackOrder{
			TransactionId: opening.TransactionID, OrderId: fmt.Sprintf("%s-%03d", opening.ID, packIndex+1), OrderedAt: timestamppb.New(opening.CreatedAt),
			Consumes: &order.OrderResources{Packs: []*item.Pack{{Id: pack.ID, Amount: 1}}}, Produces: produces, Lang: cardLanguage, PackTableId: openedPack.TableID,
		})
	}
	response.ItemAcquisitionResult = packPurchaseAcquisition(profile, acquired, acquiredCardCounts, opening, cardLanguage)
	state, err := s.packs.State(ctx, resolved.ID)
	if err == nil {
		for _, setting := range s.master.HomeSettings().PackPowers {
			amount := int64(0)
			if setting.ID == "PACK_POWER_NORMAL" {
				amount = state.Power
			}
			response.PackPowers = append(response.PackPowers, &packpb.PackPower{Amount: amount, LastAutoHealedAt: timestamppb.New(state.PowerUpdatedAt), AutoHealLimit: setting.AutoHealLimit, HealSecPerPower: setting.HealSecondsPerPower, ManualHealLimit: setting.PokeGoldUseLimit, PackPowerId: setting.ID})
		}
	}
	return response, nil
}

func packPurchaseAcquisition(profile player.Profile, acquired *item.InventoryItems, acquiredCardCounts map[string]int64, opening store.PackOpening, cardLanguage language.Language) *itemacquisition.ItemAcquisitionResult {
	state := &resources.PlayerResources{
		ExpStock: &item.ExpStock{CurrentLevel: int64(profile.Player.Level), Exp: profile.Player.Experience},
	}
	stats := &itemacquisition.AcquiredItemStats{DroppedBrightSandAmount: uint64(opening.ShineDustReward)}
	for _, owned := range profile.Cards {
		acquiredAmount, ok := acquiredCardCounts[owned.Definition.ID]
		if !ok {
			continue
		}
		state.CardStocks = append(state.CardStocks, protoCardStock(owned))
		if owned.Quantity == acquiredAmount {
			stats.FirstAcquiredCardsInExpansions = append(stats.FirstAcquiredCardsInExpansions, &cardpb.CardInstance{
				CardId:      owned.Definition.ID,
				Lang:        cardLanguage,
				ExpansionId: firstExpansionID(owned.Definition.ExpansionIDs),
			})
		}
	}
	if opening.ShineDustReward > 0 {
		for _, owned := range profile.Currencies {
			if item.Currency_Types_Type(owned.Definition.ProtocolType) == item.Currency_Types_TYPE_BRIGHT_SAND {
				state.Currencies = append(state.Currencies, &item.Currency{Type: item.Currency_Types_TYPE_BRIGHT_SAND, Amount: uint64(owned.Quantity)})
				break
			}
		}
	}
	return &itemacquisition.ItemAcquisitionResult{
		AcquiredItems:     acquired,
		AcceptedItems:     proto.Clone(acquired).(*item.InventoryItems),
		ItemState:         state,
		AcquiredItemStats: stats,
		OverflownItems:    &item.InventoryItems{},
	}
}

func firstExpansionID(expansionIDs []string) string {
	if len(expansionIDs) == 0 {
		return ""
	}
	return expansionIDs[0]
}

func probability(weight, total int64) string {
	if total <= 0 || weight <= 0 {
		return "0,0000 %"
	}
	value := float64(weight) * 100 / float64(total)
	return strings.Replace(strconv.FormatFloat(value, 'f', 4, 64), ".", ",", 1) + " %"
}
