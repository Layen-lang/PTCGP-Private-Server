package playerapi

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"

	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	itempb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	presentpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/present"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s startupPresentBoxServer) GetNewArrivalV1(ctx context.Context, _ *api.PresentBoxGetNewArrivalV1_Types_Request) (*api.PresentBoxGetNewArrivalV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	hasNew, err := s.players.HasNewPresents(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load present arrival state")
	}
	return &api.PresentBoxGetNewArrivalV1_Types_Response{HasNewArrival: hasNew}, nil
}

func (s startupPresentBoxServer) ListV1(ctx context.Context, request *api.PresentBoxListV1_Types_Request) (*api.PresentBoxListV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	offset, err := decodePageToken(request.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid page token")
	}
	values, err := s.players.PendingPresents(ctx, current.ID, offset, store.PresentPageSize+1)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot list presents")
	}
	response := &api.PresentBoxListV1_Types_Response{}
	if len(values) > store.PresentPageSize {
		values = values[:store.PresentPageSize]
		response.NextPageToken = encodePageToken(offset + store.PresentPageSize)
	}
	for _, value := range values {
		item, decodeErr := decodePresent(value)
		if decodeErr != nil {
			return nil, status.Error(codes.Internal, "cannot decode present")
		}
		response.Presents = append(response.Presents, item)
	}
	if err := s.players.MarkPresentsViewed(ctx, current.ID); err != nil {
		return nil, status.Error(codes.Internal, "cannot update present arrival state")
	}
	return response, nil
}

func (s startupPresentBoxServer) ListHistoriesV1(ctx context.Context, request *api.PresentBoxListHistoriesV1_Types_Request) (*api.PresentBoxListHistoriesV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	offset, err := decodePageToken(request.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid page token")
	}
	values, err := s.players.PresentHistories(ctx, current.ID, offset, store.PresentPageSize+1)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot list present histories")
	}
	response := &api.PresentBoxListHistoriesV1_Types_Response{}
	if len(values) > store.PresentPageSize {
		values = values[:store.PresentPageSize]
		response.NextPageToken = encodePageToken(offset + store.PresentPageSize)
	}
	for _, value := range values {
		item, decodeErr := decodePresent(value)
		if decodeErr != nil {
			return nil, status.Error(codes.Internal, "cannot decode present history")
		}
		response.Histories = append(response.Histories, &api.PresentBoxListHistoriesV1_Types_Response_Types_History{Present: item, ReceivedAt: timestamppb.New(value.ReceivedAt)})
	}
	return response, nil
}

func (s startupPresentBoxServer) ReceiveV1(ctx context.Context, request *api.PresentBoxReceiveV1_Types_Request) (*api.PresentBoxReceiveV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	ids := append([]string(nil), request.GetPresentIds()...)
	if request.GetPresentId() != "" {
		ids = append([]string{request.GetPresentId()}, ids...)
	}
	ids = uniquePresentIDs(ids)
	if len(ids) == 0 || len(ids) > store.PresentPageSize {
		return nil, status.Error(codes.InvalidArgument, "between 1 and 100 presents are required")
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load player inventory")
	}
	response := &api.PresentBoxReceiveV1_Types_Response{ItemAcquisitionResult: emptyItemAcquisitionResult(profile)}
	for _, id := range ids {
		result := &api.PresentBoxReceiveV1_Types_Response_Types_Result{PresentId: id}
		value, receiveErr := s.players.ReceivePresent(ctx, current.ID, id)
		if receiveErr != nil {
			result.Failure = &api.PresentBoxReceiveV1_Types_Response_Types_Result_Types_Failure{Reason: presentFailureReason(receiveErr)}
			response.Results = append(response.Results, result)
			continue
		}
		item, decodeErr := decodePresent(value)
		if decodeErr != nil {
			return nil, status.Error(codes.Internal, "cannot decode received present")
		}
		result.IsSuccesse = true
		result.ItemAcquisitionResult = emptyItemAcquisitionResult()
		inventory := inventoryFromPresent(item)
		result.ItemAcquisitionResult.AcquiredItems = proto.Clone(inventory).(*itempb.InventoryItems)
		result.ItemAcquisitionResult.AcceptedItems = proto.Clone(inventory).(*itempb.InventoryItems)
		proto.Merge(response.ItemAcquisitionResult.AcquiredItems, inventory)
		proto.Merge(response.ItemAcquisitionResult.AcceptedItems, inventory)
		response.Results = append(response.Results, result)
	}
	if len(response.Results) == 1 {
		response.Result = response.Results[0]
	}
	if updated, loadErr := s.players.Profile(ctx, current.ID); loadErr == nil {
		response.ItemAcquisitionResult.ItemState = emptyItemAcquisitionResult(updated).ItemState
	}
	return response, nil
}

func decodePresent(value store.Present) (*presentpb.PresentItem, error) {
	result := &presentpb.PresentItem{}
	if err := proto.Unmarshal(value.Payload, result); err != nil {
		return nil, err
	}
	result.PresentId = value.ID
	result.CreatedAt = timestamppb.New(value.CreatedAt)
	if value.ExpiresAt.IsZero() {
		result.IsForever = true
		result.ExpiredAt = nil
	} else {
		result.IsForever = false
		result.ExpiredAt = timestamppb.New(value.ExpiresAt)
	}
	return result, nil
}

func inventoryFromPresent(value *presentpb.PresentItem) *itempb.InventoryItems {
	result := &itempb.InventoryItems{}
	switch item := value.GetItem().(type) {
	case *presentpb.PresentItem_Card:
		result.CardInstances = append(result.CardInstances, item.Card)
	case *presentpb.PresentItem_EventPowerCharger:
		result.EventPowerChargers = append(result.EventPowerChargers, item.EventPowerCharger)
	case *presentpb.PresentItem_PackPowerCharger:
		result.PackPowerChargers = append(result.PackPowerChargers, item.PackPowerCharger)
	case *presentpb.PresentItem_ChallengePowerCharger:
		result.ChallengePowerChargers = append(result.ChallengePowerChargers, item.ChallengePowerCharger)
	case *presentpb.PresentItem_PeripheralGoods:
		result.PeripheralGoods = append(result.PeripheralGoods, item.PeripheralGoods)
	case *presentpb.PresentItem_ProfileDecoration:
		result.ProfileDecorations = append(result.ProfileDecorations, item.ProfileDecoration)
	case *presentpb.PresentItem_Currency:
		result.Currencies = append(result.Currencies, item.Currency)
	case *presentpb.PresentItem_PokeGold:
		result.PokeGolds = append(result.PokeGolds, item.PokeGold)
	case *presentpb.PresentItem_RewardTicket:
		result.RewardTickets = append(result.RewardTickets, item.RewardTicket)
	case *presentpb.PresentItem_RevivalClock:
		result.RevivalClocks = append(result.RevivalClocks, item.RevivalClock)
	case *presentpb.PresentItem_RentalDeck:
		result.RentalDecks = append(result.RentalDecks, item.RentalDeck)
	case *presentpb.PresentItem_ThemeDeckRecipe:
		result.ThemeDeckRecipes = append(result.ThemeDeckRecipes, item.ThemeDeckRecipe)
	case *presentpb.PresentItem_TradePowerCharger:
		result.TradePowerChargers = append(result.TradePowerChargers, item.TradePowerCharger)
	case *presentpb.PresentItem_TradeItem:
		result.TradeItems = append(result.TradeItems, item.TradeItem)
	case *presentpb.PresentItem_TradeTicket:
		result.TradeTickets = append(result.TradeTickets, item.TradeTicket)
	case *presentpb.PresentItem_CardSkin:
		result.CardSkinStocks = append(result.CardSkinStocks, item.CardSkin)
	case *presentpb.PresentItem_CardFrame:
		result.CardFrameStocks = append(result.CardFrameStocks, item.CardFrame)
	}
	return result
}

func presentFailureReason(err error) api.PresentBoxReceiveV1_Types_Response_Types_Result_Types_Failure_Types_FailureReason {
	if errors.Is(err, store.ErrExpired) {
		return api.PresentBoxReceiveV1_Types_Response_Types_Result_Types_Failure_Types_FAILURE_REASON_EXPIRED
	}
	return api.PresentBoxReceiveV1_Types_Response_Types_Result_Types_Failure_Types_FAILURE_REASON_SKIP
}

func encodePageToken(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(offset)))
}

func decodePageToken(token string) (int, error) {
	if strings.TrimSpace(token) == "" {
		return 0, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return 0, err
	}
	offset, err := strconv.Atoi(string(decoded))
	if err != nil || offset < 0 {
		return 0, errors.New("invalid offset")
	}
	return offset, nil
}

func uniquePresentIDs(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
