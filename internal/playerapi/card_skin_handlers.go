package playerapi

import (
	"context"
	"errors"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	cardexchangepb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card_exchange"
	cardframepb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card_frame"
	cardskinpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card_skin"
	itempb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type cardSkinServer struct {
	api.UnimplementedCardSkinServer
	players *player.Manager
}

func (s cardSkinServer) GetCardExchangeRouteV1(ctx context.Context, _ *api.CardSkinGetCardExchangeRouteV1_Types_Request) (*api.CardSkinGetCardExchangeRouteV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	return &api.CardSkinGetCardExchangeRouteV1_Types_Response{RouteType: cardexchangepb.PlayerCardExchangeRouteType(s.players.CardExchangeRoute(current.ID))}, nil
}

func (s cardSkinServer) SyncExchangedCatalogsV1(ctx context.Context, _ *api.CardSkinSyncExchangedCatalogsV1_Types_Request) (*api.CardSkinSyncExchangedCatalogsV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	counts, err := s.players.CardExchangeCounts(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load card exchanges")
	}
	response := &api.CardSkinSyncExchangedCatalogsV1_Types_Response{}
	lastModified := time.Unix(0, 0).UTC()
	for _, count := range counts {
		response.ExchangedCatalogs = append(response.ExchangedCatalogs, &cardexchangepb.CardExchangeCatalog{CatalogId: count.CatalogID, ExchangedCount: count.Count})
		if count.UpdatedAt.After(lastModified) {
			lastModified = count.UpdatedAt
		}
	}
	response.LastModifiedAt = timestamppb.New(lastModified)
	return response, nil
}

func (s cardSkinServer) ExchangeV2(ctx context.Context, request *api.CardSkinExchangeV2_Types_Request) (*api.CardSkinExchangeV2_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	inputs := make([]player.CardExchangeInput, 0, len(request.GetEntries()))
	for _, entry := range request.GetEntries() {
		if entry == nil {
			continue
		}
		input := player.CardExchangeInput{CatalogID: entry.GetCatalogId(), Amount: int64(entry.GetAmount())}
		for _, card := range entry.GetResourceCards() {
			if card != nil {
				input.ResourceCardIDs = append(input.ResourceCardIDs, card.GetCardId())
			}
		}
		inputs = append(inputs, input)
	}
	counts, err := s.players.ExchangeCardCosmetics(ctx, current.ID, inputs)
	if err != nil {
		if errors.Is(err, store.ErrInsufficientInventory) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load exchanged inventory")
	}
	response := &api.CardSkinExchangeV2_Types_Response{ItemAcquisitionResult: emptyItemAcquisitionResult(profile)}
	for index, count := range counts {
		response.ExchangedCatalogs = append(response.ExchangedCatalogs, &cardexchangepb.CardExchangeCatalog{CatalogId: count.CatalogID, ExchangedCount: count.Count})
		definition, lookupErr := s.players.CardExchangeDefinition(count.CatalogID)
		if lookupErr != nil {
			continue
		}
		amount := uint64(definition.ProductAmount * inputs[index].Amount)
		if definition.ProductType == 2 {
			currency, currencyErr := s.players.CurrencyDefinition(definition.ProductID)
			if currencyErr != nil {
				continue
			}
			stock := &itempb.Currency{Type: itempb.Currency_Types_Type(currency.ProtocolType), Amount: amount}
			response.ItemAcquisitionResult.AcquiredItems.Currencies = append(response.ItemAcquisitionResult.AcquiredItems.Currencies, stock)
			response.ItemAcquisitionResult.AcceptedItems.Currencies = append(response.ItemAcquisitionResult.AcceptedItems.Currencies, stock)
		} else if definition.ProductType == 4 {
			stock := &cardframepb.CardFrameStock{CardId: definition.CardID, CardFrameId: definition.ProductID, Amount: amount}
			response.ItemAcquisitionResult.AcquiredItems.CardFrameStocks = append(response.ItemAcquisitionResult.AcquiredItems.CardFrameStocks, stock)
			response.ItemAcquisitionResult.AcceptedItems.CardFrameStocks = append(response.ItemAcquisitionResult.AcceptedItems.CardFrameStocks, stock)
		} else {
			stock := &cardskinpb.CardSkinStock{CardId: definition.CardID, CardSkinId: definition.ProductID, Amount: amount}
			response.ItemAcquisitionResult.AcquiredItems.CardSkinStocks = append(response.ItemAcquisitionResult.AcquiredItems.CardSkinStocks, stock)
			response.ItemAcquisitionResult.AcceptedItems.CardSkinStocks = append(response.ItemAcquisitionResult.AcceptedItems.CardSkinStocks, stock)
		}
	}
	return response, nil
}
