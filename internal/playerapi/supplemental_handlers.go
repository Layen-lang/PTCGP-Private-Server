package playerapi

import (
	"context"
	"errors"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	feedpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/feed"
	itempb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type analyticsLogServer struct {
	api.UnimplementedAnalyticsLogServer
}

func (analyticsLogServer) SendAnalyticsLogV1(context.Context, *api.AnalyticsLogSendV1_Types_Request) (*api.AnalyticsLogSendV1_Types_Response, error) {
	return &api.AnalyticsLogSendV1_Types_Response{}, nil
}

type collectionServer struct {
	api.UnimplementedCollectionServer
	players *player.Manager
}

type playerLevelServer struct {
	api.UnimplementedPlayerLevelServer
	players *player.Manager
}

func (s playerLevelServer) MayLevelUpV1(ctx context.Context, _ *api.PlayerLevelMayLevelUpV1_Types_Request) (*api.PlayerLevelMayLevelUpV1_Types_Response, error) {
	p, err := profileFor(ctx, s.players)
	if err != nil {
		return nil, err
	}
	return &api.PlayerLevelMayLevelUpV1_Types_Response{PreviousLevel: int64(p.Player.Level), CurrentLevel: int64(p.Player.Level), Exp: p.Player.Experience, ItemAcquisitionResult: emptyItemAcquisitionResult(p)}, nil
}

type tutorialServer struct {
	api.UnimplementedTutorialServer
	players *player.Manager
}

func (s tutorialServer) CompleteV1(ctx context.Context, request *api.TutorialCompleteV1_Types_Request) (*api.TutorialCompleteV1_Types_Response, error) {
	if err := s.recordCompletion(ctx, request.GetTutorialId(), request.GetTutorialStep()); err != nil {
		return nil, err
	}
	return &api.TutorialCompleteV1_Types_Response{TutorialId: request.GetTutorialId(), TutorialStep: request.GetTutorialStep(), ItemAcquisitionResult: emptyItemAcquisitionResult()}, nil
}
func (s tutorialServer) ChallengeFeedV1(ctx context.Context, request *api.TutorialChallengeFeedV1_Types_Request) (*api.TutorialChallengeFeedV1_Types_Response, error) {
	if err := s.recordCompletion(ctx, request.GetTutorialId(), request.GetTutorialStep()); err != nil {
		return nil, err
	}
	return &api.TutorialChallengeFeedV1_Types_Response{TutorialId: request.GetTutorialId(), TutorialStep: request.GetTutorialStep(), PickedSubstitutionItem: &itempb.InventoryItems{}, ChallengePower: localChallengePower(), ItemAcquisitionResult: emptyItemAcquisitionResult()}, nil
}
func (s tutorialServer) ChoiceExchangeRouteV1(ctx context.Context, request *api.TutorialChoiceExchangeRouteV1_Types_Request) (*api.TutorialChoiceExchangeRouteV1_Types_Response, error) {
	if err := s.recordCompletion(ctx, request.GetTutorialId(), request.GetTutorialStep()); err != nil {
		return nil, err
	}
	return &api.TutorialChoiceExchangeRouteV1_Types_Response{TutorialId: request.GetTutorialId(), TutorialStep: request.GetTutorialStep(), ItemAcquisitionResult: emptyItemAcquisitionResult()}, nil
}
func (tutorialServer) GetFeedTimelineV1(context.Context, *api.TutorialGetFeedTimelineV1_Types_Request) (*api.TutorialGetFeedTimelineV1_Types_Response, error) {
	return &api.TutorialGetFeedTimelineV1_Types_Response{Timeline: &feedpb.FeedTimeline{TimelineId: "00000000-0000-4000-8000-000000000000", RenewAfter: timestamppb.New(time.Now().Add(24 * time.Hour))}, ChallengePower: localChallengePower()}, nil
}
func (s tutorialServer) PackPurchaseV1(ctx context.Context, request *api.TutorialPackPurchaseV1_Types_Request) (*api.TutorialPackPurchaseV1_Types_Response, error) {
	if err := s.recordCompletion(ctx, request.GetTutorialId(), request.GetTutorialStep()); err != nil {
		return nil, err
	}
	return &api.TutorialPackPurchaseV1_Types_Response{TutorialId: request.GetTutorialId(), TutorialStep: request.GetTutorialStep(), ItemAcquisitionResult: emptyItemAcquisitionResult()}, nil
}

func (s tutorialServer) recordCompletion(ctx context.Context, tutorialID string, step int64) error {
	resolved, err := playerFor(ctx)
	if err != nil {
		return err
	}
	if err := s.players.CompleteTutorial(ctx, resolved.ID, tutorialID, step); errors.Is(err, player.ErrValidation) {
		return status.Error(codes.InvalidArgument, err.Error())
	} else if err != nil {
		return status.Error(codes.Internal, "tutorial progress unavailable")
	}
	return nil
}

func localChallengePower() *feedpb.ChallengePower {
	return &feedpb.ChallengePower{Amount: 5, LastAutoHealedAt: timestamppb.Now(), AutoHealLimit: 5, HealSecPerPower: 43200, ManualHealLimit: 8}
}

func registerSupplementalServers(server grpc.ServiceRegistrar, players *player.Manager) {
	api.RegisterAnalyticsLogServer(server, analyticsLogServer{})
	api.RegisterAlbumServer(server, albumServer{players: players})
	api.RegisterCardSkinServer(server, cardSkinServer{players: players})
	api.RegisterCollectionServer(server, collectionServer{players: players})
	api.RegisterGiveCardServer(server, giveCardServer{players: players})
	api.RegisterItemShopServer(server, itemShopServer{players: players})
	api.RegisterPlayerLevelServer(server, playerLevelServer{players: players})
	api.RegisterMountServer(server, mountServer{players: players})
	api.RegisterPlayerServer(server, playerServer{players: players})
	api.RegisterThankRewardServer(server, thankRewardServer{players: players})
	api.RegisterSignInWithAppleServer(server, signInWithAppleServer{})
	api.RegisterTutorialServer(server, tutorialServer{players: players})
}
