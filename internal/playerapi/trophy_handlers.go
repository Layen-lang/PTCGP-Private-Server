package playerapi

import (
	"context"
	"strings"

	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	itempb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	trophypb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/trophy"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s startupTrophyServer) GetStatusV1(ctx context.Context, request *api.TrophyGetStatusV1_Types_Request) (*api.TrophyGetStatusV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	playerID := request.GetPlayerId()
	if playerID == "" {
		playerID = current.ID
	}
	states, err := s.players.TrophyStates(ctx, playerID, request.GetTrophyIds())
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load trophies")
	}
	response := &api.TrophyGetStatusV1_Types_Response{}
	for _, state := range states {
		response.Trophies = append(response.Trophies, &trophypb.Trophy{Id: state.ID, Rank: trophypb.TrophyRank(state.Rank)})
	}
	return response, nil
}

func (s startupTrophyServer) CompleteV1(ctx context.Context, request *api.TrophyCompleteV1_Types_Request) (*api.TrophyCompleteV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	states := make([]store.TrophyState, 0, len(request.GetTrophies()))
	for _, trophy := range request.GetTrophies() {
		if trophy != nil {
			states = append(states, store.TrophyState{ID: trophy.GetId(), Rank: int32(trophy.GetRank())})
		}
	}
	sand, err := s.players.CompleteTrophies(ctx, current.ID, states)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	profile, _ := s.players.Profile(ctx, current.ID)
	acquisition := emptyItemAcquisitionResult(profile)
	if sand > 0 {
		currency := &itempb.Currency{Type: itempb.Currency_Types_TYPE_BRIGHT_SAND, Amount: uint64(sand)}
		acquisition.AcquiredItems.Currencies = append(acquisition.AcquiredItems.Currencies, currency)
		acquisition.AcceptedItems.Currencies = append(acquisition.AcceptedItems.Currencies, currency)
	}
	return &api.TrophyCompleteV1_Types_Response{ItemAcquisitionResult: acquisition}, nil
}

type signInWithAppleServer struct {
	api.UnimplementedSignInWithAppleServer
}

func (signInWithAppleServer) GenerateTokenV1(_ context.Context, request *api.SignInWithAppleGenerateTokenV1_Types_Request) (*api.SignInWithAppleGenerateTokenV1_Types_Response, error) {
	if strings.TrimSpace(request.GetCode()) == "" {
		return nil, status.Error(codes.InvalidArgument, "Apple authorization code is required")
	}
	return &api.SignInWithAppleGenerateTokenV1_Types_Response{}, nil
}
