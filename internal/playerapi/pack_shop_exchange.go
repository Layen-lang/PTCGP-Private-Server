package playerapi

import (
	"context"
	"errors"

	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	cardpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card"
	itempb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s packShopServer) ExchangeV1(ctx context.Context, request *api.PackShopExchangeV1_Types_Request) (*api.PackShopExchangeV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	remaining, replay, err := s.players.ExchangePackPoints(ctx, current.ID, request.GetTransactionId(), request.GetPackCeilPointSharedGroupId(), request.GetCardId(), int64(request.GetAmount()))
	if err != nil {
		if errors.Is(err, store.ErrInsufficientInventory) {
			return nil, status.Error(codes.FailedPrecondition, "not enough pack points")
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	profile, _ := s.players.Profile(ctx, current.ID)
	acquisition := emptyItemAcquisitionResult(profile)
	if !replay {
		definition, _ := s.players.CardDefinition(request.GetCardId())
		expansion := ""
		if len(definition.ExpansionIDs) > 0 {
			expansion = definition.ExpansionIDs[0]
		}
		card := &itempb.CardInstance{CardInstance: &cardpb.CardInstance{CardId: definition.ID, ExpansionId: expansion}, Amount: int64(request.GetAmount())}
		acquisition.AcquiredItems.CardInstances = append(acquisition.AcquiredItems.CardInstances, card)
		acquisition.AcceptedItems.CardInstances = append(acquisition.AcceptedItems.CardInstances, card)
	}
	return &api.PackShopExchangeV1_Types_Response{PackCeilPoint: &itempb.PackCeilPoint{Amount: remaining, PackCeilPointSharedGroupId: request.GetPackCeilPointSharedGroupId()}, ItemAcquisitionResult: acquisition}, nil
}
