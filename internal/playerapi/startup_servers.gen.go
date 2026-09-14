// Package playerapi startup adapters combine scaffolded neutral reads with the
// small stateful handlers required by captured account-reuse flows.

package playerapi

import (
	"context"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	actionpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/action"
	comebackplayer "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/comeback_player"
	datepb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/date"
	solopb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/solo_battle"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type startupFaveServer struct {
	api.UnimplementedFaveServer
}

func (startupFaveServer) InsightV1(context.Context, *api.FaveInsightV1_Types_Request) (*api.FaveInsightV1_Types_Response, error) {
	return &api.FaveInsightV1_Types_Response{}, nil
}

type startupPassServer struct {
	api.UnimplementedPassServer
}

func (startupPassServer) GetEntitlementsV1(context.Context, *api.PassGetEntitlementsV1_Types_Request) (*api.PassGetEntitlementsV1_Types_Response, error) {
	return &api.PassGetEntitlementsV1_Types_Response{}, nil
}

type startupComebackPlayerServer struct {
	api.UnimplementedComebackPlayerServer
	players *player.Manager
}

func (s startupComebackPlayerServer) GetComebackPlayerInfoV1(ctx context.Context, _ *api.ComebackPlayerGetComebackPlayerInfoV1_Types_Request) (*api.ComebackPlayerGetComebackPlayerInfoV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	state, err := s.players.ComebackInfo(ctx, current.DeviceAccount)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load comeback player state")
	}
	info := &comebackplayer.ComebackPlayerInfo{IsReturningPlayer: state.IsReturning, AbsenceDays: state.AbsenceDays}
	if !state.ReturnedAt.IsZero() {
		info.ReturnedAt = timestamppb.New(state.ReturnedAt)
	}
	return &api.ComebackPlayerGetComebackPlayerInfoV1_Types_Response{ComebackPlayerInfo: info}, nil
}

type startupWebviewServer struct {
	api.UnimplementedWebviewServer
}

func (startupWebviewServer) GetNewsV1(_ context.Context, request *api.WebviewGetNewsV1_Types_Request) (*api.WebviewGetNewsV1_Types_Response, error) {
	return &api.WebviewGetNewsV1_Types_Response{NewsUrl: noticeCDNBaseURL + "/webview/news/?lang=" + webviewLocale(request.GetLanguage())}, nil
}

func (startupWebviewServer) GetPrivacyPolicyUrlV1(context.Context, *api.WebviewGetPrivacyPolicyUrlV1_Types_Request) (*api.WebviewGetPrivacyPolicyUrlV1_Types_Response, error) {
	return &api.WebviewGetPrivacyPolicyUrlV1_Types_Response{Url: privacyURL}, nil
}

func (startupWebviewServer) GetTermsOfServiceUrlV1(context.Context, *api.WebviewGetTermsOfServiceUrlV1_Types_Request) (*api.WebviewGetTermsOfServiceUrlV1_Types_Response, error) {
	return &api.WebviewGetTermsOfServiceUrlV1_Types_Response{Url: termsURL}, nil
}

type startupFeedServer struct {
	api.UnimplementedFeedServer
	players *player.Manager
}

func (s startupFeedServer) ShareV1(ctx context.Context, request *api.FeedShareV1_Types_Request) (*api.FeedShareV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.players.SharePackOpening(ctx, current.ID, request.GetTransactionId()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &api.FeedShareV1_Types_Response{}, nil
}

type startupTradeServer struct {
	api.UnimplementedTradeServer
	players *player.Manager
}

type startupCardServer struct {
	api.UnimplementedCardServer
	players *player.Manager
}

type startupSoloBattleServer struct {
	api.UnimplementedSoloBattleServer
	players *player.Manager
}

func (s startupSoloBattleServer) StartStepupBattleV1(ctx context.Context, request *api.SoloBattleStartStepupBattleV1_Types_Request) (*api.SoloBattleStartStepupBattleV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	token, err := s.players.StartSoloBattle(ctx, current.ID, request.GetSoloStepupBattleId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.players.AddAction(ctx, current.ID, player.ActionPVETry, request.GetSoloStepupBattleId(), 1, true); err != nil {
		return nil, status.Error(codes.Internal, "cannot record solo battle action")
	}
	usedCount, err := useRentalDeck(ctx, s.players, current.ID, request.GetDeck())
	if err != nil {
		return nil, err
	}
	tries, err := s.players.SoloBattleTries(ctx, current.ID, request.GetSoloStepupBattleId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &api.SoloBattleStartStepupBattleV1_Types_Response{BattleSessionToken: token, RentalDeckUsedCount: usedCount, BattleTries: protoSoloBattleTryProgresses(tries)}, nil
}

func (s startupSoloBattleServer) FinishStepupBattleV1(ctx context.Context, request *api.SoloBattleFinishStepupBattleV1_Types_Request) (*api.SoloBattleFinishStepupBattleV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	first, err := s.players.FinishSoloBattle(ctx, current.ID, request.GetBattleSessionToken(), int32(request.GetResultType()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if int32(request.GetResultType()) == 1 {
		if err := s.players.AddAction(ctx, current.ID, player.ActionPVEWon, request.GetBattleId(), 1, true); err != nil {
			return nil, status.Error(codes.Internal, "cannot record solo battle win")
		}
	}
	tries, err := saveSoloBattleTryProgresses(ctx, s.players, current.ID, request.GetBattleId(), request.GetBattleTryProgresses())
	if err != nil {
		return nil, err
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load local profile")
	}
	return &api.SoloBattleFinishStepupBattleV1_Types_Response{IsFirstClear: first, BattleTries: protoSoloBattleTryResults(tries), ItemAcquisitionResult: emptyItemAcquisitionResult(profile)}, nil
}

func (s startupSoloBattleServer) GetStepupBattlesV1(ctx context.Context, request *api.SoloBattleGetStepupBattlesV1_Types_Request) (*api.SoloBattleGetStepupBattlesV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	states, err := s.players.SoloBattleClearStates(ctx, current.ID, request.GetSoloStepupBattleIds())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	response := &api.SoloBattleGetStepupBattlesV1_Types_Response{}
	for _, battleID := range request.GetSoloStepupBattleIds() {
		tries, tryErr := s.players.SoloBattleTries(ctx, current.ID, battleID)
		if tryErr != nil {
			return nil, status.Error(codes.InvalidArgument, tryErr.Error())
		}
		response.SoloStepupBattles = append(response.SoloStepupBattles, &solopb.SoloStepupBattle{BattleId: battleID, IsCleared: states[battleID], BattleTries: protoSoloBattleTries(tries)})
	}
	return response, nil
}

func (s startupSoloBattleServer) GetRandomBattlesV1(ctx context.Context, request *api.SoloBattleGetRandomBattlesV1_Types_Request) (*api.SoloBattleGetRandomBattlesV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	states, err := s.players.SoloBattleClearStates(ctx, current.ID, request.GetSoloRandomBattleIds())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	response := &api.SoloBattleGetRandomBattlesV1_Types_Response{}
	for _, battleID := range request.GetSoloRandomBattleIds() {
		tries, tryErr := s.players.SoloBattleTries(ctx, current.ID, battleID)
		if tryErr != nil {
			return nil, status.Error(codes.InvalidArgument, tryErr.Error())
		}
		response.SoloRandomBattles = append(response.SoloRandomBattles, &solopb.SoloRandomBattle{BattleId: battleID, IsCleared: states[battleID], BattleTries: protoSoloBattleTries(tries)})
	}
	return response, nil
}

func (s startupSoloBattleServer) GetEventBattlesV1(ctx context.Context, request *api.SoloBattleGetEventBattlesV1_Types_Request) (*api.SoloBattleGetEventBattlesV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	states, err := s.players.SoloBattleClearStates(ctx, current.ID, request.GetSoloEventBattleIds())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	response := &api.SoloBattleGetEventBattlesV1_Types_Response{}
	for _, battleID := range request.GetSoloEventBattleIds() {
		tries, tryErr := s.players.SoloBattleTries(ctx, current.ID, battleID)
		if tryErr != nil {
			return nil, status.Error(codes.InvalidArgument, tryErr.Error())
		}
		response.SoloEventBattles = append(response.SoloEventBattles, &solopb.SoloEventBattle{BattleId: battleID, IsCleared: states[battleID], BattleTries: protoSoloBattleTries(tries)})
	}
	return response, nil
}

type startupActionServer struct {
	api.UnimplementedActionServer
	players *player.Manager
}

func (s startupActionServer) SyncStatesV1(ctx context.Context, request *api.ActionSyncStatesV1_Types_Request) (*api.ActionSyncStatesV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	offset, err := decodePageToken(request.GetCursor())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid action cursor")
	}
	// The client and the official service use this sentinel for a first sync.
	modifiedAfter := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	if request.GetLastSyncedAt() != nil {
		modifiedAfter = request.GetLastSyncedAt().AsTime()
	}
	// A continuation cursor pages the same snapshot. Do not apply the first
	// page's watermark again, otherwise rows written at that exact timestamp
	// disappear on page two.
	if request.GetCursor() != "" {
		modifiedAfter = time.Unix(0, 0).UTC()
	}
	states, hasNext, lastModified, err := s.players.ActionStates(ctx, current.ID, modifiedAfter, offset, 1000)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot sync action states")
	}
	response := &api.ActionSyncStatesV1_Types_Response{HasNext: hasNext}
	for _, state := range states {
		value := &actionpb.ActionState{ActionKey: actionpb.ActionState_Types_ActionKey(state.Key), ActionTarget: state.Target, Count: state.Count}
		if !state.Date.IsZero() {
			value.Date = &datepb.Date{Year: int32(state.Date.Year()), Month: int32(state.Date.Month()), Day: int32(state.Date.Day())}
		}
		response.ActionStates = append(response.ActionStates, value)
	}
	if hasNext {
		response.NextCursor = encodePageToken(offset + len(states))
	}
	if lastModified.IsZero() {
		// The official endpoint echoes the caller's watermark when there are
		// no newer states; advancing it would make an empty sync skip updates.
		lastModified = modifiedAfter
	}
	response.LastModifiedAt = timestamppb.New(lastModified)
	return response, nil
}

type startupShopServer struct {
	api.UnimplementedShopServer
	players *player.Manager
}

func (startupShopServer) GetNewNotificationV1(context.Context, *api.ShopGetNewNotificationV1_Types_Request) (*api.ShopGetNewNotificationV1_Types_Response, error) {
	return &api.ShopGetNewNotificationV1_Types_Response{}, nil
}

type startupPresentBoxServer struct {
	api.UnimplementedPresentBoxServer
	players *player.Manager
}

type startupTrophyServer struct {
	api.UnimplementedTrophyServer
	players *player.Manager
}

func registerStartupReadServers(server grpc.ServiceRegistrar, players *player.Manager) {
	api.RegisterFaveServer(server, startupFaveServer{})
	api.RegisterPassServer(server, startupPassServer{})
	api.RegisterComebackPlayerServer(server, startupComebackPlayerServer{players: players})
	api.RegisterWebviewServer(server, startupWebviewServer{})
	api.RegisterFeedServer(server, startupFeedServer{players: players})
	api.RegisterTradeServer(server, startupTradeServer{players: players})
	api.RegisterCardServer(server, startupCardServer{players: players})
	api.RegisterSoloBattleServer(server, startupSoloBattleServer{players: players})
	api.RegisterActionServer(server, startupActionServer{players: players})
	api.RegisterShopServer(server, startupShopServer{players: players})
	api.RegisterPresentBoxServer(server, startupPresentBoxServer{players: players})
	api.RegisterTrophyServer(server, startupTrophyServer{players: players})
}
