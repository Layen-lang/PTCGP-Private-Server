package playerapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/packlab"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	cardpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card"
	cardexchange "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card_exchange"
	feedpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/feed"
	itempb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	languagepb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/language"
	orderpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/order"
	packpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/pack"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"github.com/google/uuid"
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
	current, err := playerFor(ctx)
	if err != nil {
		return nil, err
	}
	result, err := s.players.MayLevelUp(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "player level unavailable")
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "player inventory unavailable")
	}
	return &api.PlayerLevelMayLevelUpV1_Types_Response{PreviousLevel: int64(result.PreviousLevel), CurrentLevel: int64(result.CurrentLevel), Exp: profile.Player.Experience, ItemAcquisitionResult: acquisitionFromChanges(profile, result.Changes, s.players.Catalog())}, nil
}

type tutorialServer struct {
	api.UnimplementedTutorialServer
	players *player.Manager
	packs   *packlab.Engine
}

func (s tutorialServer) CompleteV1(ctx context.Context, request *api.TutorialCompleteV1_Types_Request) (*api.TutorialCompleteV1_Types_Response, error) {
	current, err := playerFor(ctx)
	if err != nil {
		return nil, err
	}
	input := player.TutorialGrantInput{TutorialID: request.GetTutorialId(), Step: request.GetTutorialStep(), IncludeMasterReward: true}
	if request.GetTutorialId() == "1011" {
		route, chosen, routeErr := s.players.ChosenTutorialRoute(ctx, current.ID)
		if routeErr != nil {
			return nil, status.Error(codes.Internal, "tutorial route unavailable")
		}
		if chosen && int64(route) != request.GetTutorialStep() {
			return nil, status.Error(codes.FailedPrecondition, "tutorial deck reward does not match the selected route")
		}
		if !chosen {
			if _, ok := s.players.CatalogTutorialReward(request.GetTutorialId(), request.GetTutorialStep()); !ok || request.GetTutorialStep() < 1 || request.GetTutorialStep() > 3 {
				return nil, status.Error(codes.InvalidArgument, "unknown tutorial route reward")
			}
			input.Flags = map[string]int64{"exchange_route": request.GetTutorialStep()}
		}
	}
	result, err := s.grant(ctx, current.ID, input)
	if err != nil {
		return nil, err
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "tutorial inventory unavailable")
	}
	return &api.TutorialCompleteV1_Types_Response{TutorialId: request.GetTutorialId(), TutorialStep: request.GetTutorialStep(), ItemAcquisitionResult: acquisitionFromChanges(profile, result.Changes, s.players.Catalog())}, nil
}

func (s tutorialServer) ChallengeFeedV1(ctx context.Context, request *api.TutorialChallengeFeedV1_Types_Request) (*api.TutorialChallengeFeedV1_Types_Response, error) {
	current, err := playerFor(ctx)
	if err != nil {
		return nil, err
	}
	route, err := s.players.TutorialRoute(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "tutorial route unavailable")
	}
	feed, err := s.players.TutorialFeed(route)
	if err != nil {
		return nil, status.Error(codes.Internal, "tutorial feed unavailable")
	}
	change := store.ShopInventoryChange{Kind: "card", ID: feed.RewardCardID, Amount: 1}
	result, err := s.grant(ctx, current.ID, player.TutorialGrantInput{TutorialID: request.GetTutorialId(), Step: request.GetTutorialStep(), Rewards: []store.ShopInventoryChange{change}, Flags: map[string]int64{"feed_challenged": 1}})
	if err != nil {
		return nil, err
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "tutorial feed reward unavailable")
	}
	language := languageForProfile(profile)
	card := tutorialCardInstance(s.players, feed.RewardCardID, language)
	response := &api.TutorialChallengeFeedV1_Types_Response{TutorialId: request.GetTutorialId(), TutorialStep: request.GetTutorialStep(), PickedSubstitutionItem: &itempb.InventoryItems{}, ChallengePower: localChallengePower(), ItemAcquisitionResult: acquisitionFromChanges(profile, result.Changes, s.players.Catalog())}
	response.PickedCards = []*cardpb.CardInstance{card}
	for _, owned := range profile.Cards {
		if owned.Definition.ID == feed.RewardCardID {
			response.UpdatedCardStocks = []*cardpb.CardStock{protoCardStock(owned)}
			break
		}
	}
	return response, nil
}

func (s tutorialServer) ChoiceExchangeRouteV1(ctx context.Context, request *api.TutorialChoiceExchangeRouteV1_Types_Request) (*api.TutorialChoiceExchangeRouteV1_Types_Response, error) {
	current, err := playerFor(ctx)
	if err != nil {
		return nil, err
	}
	route, err := s.players.TutorialExchangeRoute(request.GetPackId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "unknown tutorial route pack")
	}
	if selected, chosen, routeErr := s.players.ChosenTutorialRoute(ctx, current.ID); routeErr != nil {
		return nil, status.Error(codes.Internal, "tutorial route unavailable")
	} else if chosen && selected != route.RouteType {
		return nil, status.Error(codes.FailedPrecondition, "tutorial route is already selected")
	}
	opening, err := s.packs.PreviewOfficial(ctx, packlab.PreviewInput{PlayerID: current.ID, PackID: route.PackID, RequestedCount: 1})
	if err != nil || len(opening.Packs) != 1 {
		return nil, status.Error(codes.Internal, "tutorial route pack unavailable")
	}
	changes := cardChanges(opening.Packs[0].Cards)
	result, err := s.grant(ctx, current.ID, player.TutorialGrantInput{TutorialID: request.GetTutorialId(), Step: request.GetTutorialStep(), Rewards: changes, Flags: map[string]int64{"exchange_route": int64(route.RouteType)}})
	if err != nil {
		return nil, err
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "tutorial route inventory unavailable")
	}
	return &api.TutorialChoiceExchangeRouteV1_Types_Response{ExchangeRouteType: cardexchange.PlayerCardExchangeRouteType(route.RouteType), UnpackOrder: tutorialUnpackOrder(s.players, profile, opening.PackID, opening.Packs[0], ""), ItemAcquisitionResult: acquisitionFromChanges(profile, result.Changes, s.players.Catalog()), TutorialId: request.GetTutorialId(), TutorialStep: request.GetTutorialStep()}, nil
}

func (s tutorialServer) GetFeedTimelineV1(ctx context.Context, _ *api.TutorialGetFeedTimelineV1_Types_Request) (*api.TutorialGetFeedTimelineV1_Types_Response, error) {
	current, err := playerFor(ctx)
	if err != nil {
		return nil, err
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "tutorial profile unavailable")
	}
	route, err := s.players.TutorialRoute(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "tutorial route unavailable")
	}
	definition, err := s.players.TutorialFeed(route)
	if err != nil {
		return nil, status.Error(codes.Internal, "tutorial feed unavailable")
	}
	challenged, err := s.players.TutorialFeedChallenged(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "tutorial feed state unavailable")
	}
	contents := &feedpb.FeedContents{Lang: languageForProfile(profile)}
	for _, cardID := range definition.CardIDs {
		card := tutorialCardInstance(s.players, cardID, contents.Lang)
		contents.Cards = append(contents.Cards, &feedpb.FeedElementCard{CardId: card.CardId, Lang: card.Lang, ExpansionId: card.ExpansionId})
	}
	remaining, next := int64(1), int64(1)
	if challenged {
		remaining, next = 0, 2
	}
	endAt := time.Date(9999, time.January, 1, 0, 0, 0, 0, time.UTC)
	timeline := &feedpb.FeedTimeline{FreeFeeds: []*feedpb.FreeFeed{{FreeFeedId: "tutorial-feed", Contents: contents, ChallengeInfo: &feedpb.FeedChallengeInfo{EndAt: timestamppb.New(endAt), RemainingCount: remaining, NextChallengeNum: next}}}}
	return &api.TutorialGetFeedTimelineV1_Types_Response{Timeline: timeline, ChallengePower: localChallengePower()}, nil
}

func (s tutorialServer) PackPurchaseV1(ctx context.Context, request *api.TutorialPackPurchaseV1_Types_Request) (*api.TutorialPackPurchaseV1_Types_Response, error) {
	current, err := playerFor(ctx)
	if err != nil {
		return nil, err
	}
	setting := s.players.TutorialPackSetting()
	opening, err := s.packs.PreviewOfficial(ctx, packlab.PreviewInput{PlayerID: current.ID, PackID: setting.PackID, RequestedCount: 1})
	if err != nil || len(opening.Packs) != 1 {
		return nil, status.Error(codes.Internal, "tutorial pack unavailable")
	}
	changes := cardChanges(opening.Packs[0].Cards)
	guaranteePoints := int64(0)
	if setting.GuaranteeID != "" {
		guaranteePoints = 1
	}
	result, err := s.grant(ctx, current.ID, player.TutorialGrantInput{TutorialID: request.GetTutorialId(), Step: request.GetTutorialStep(), Rewards: changes, Experience: setting.Experience, CeilGroup: setting.CeilGroupID, CeilPoints: setting.CeilPoints, GuaranteeID: setting.GuaranteeID, GuaranteePoints: guaranteePoints})
	if err != nil {
		return nil, err
	}
	opening.ExperienceReward = setting.Experience
	opening.CeilPointReward = setting.CeilPoints
	opening.TransactionID = tutorialPackTransactionID(current.ID, request.GetTutorialId(), request.GetTutorialStep(), opening.PackID)
	opening, _, err = s.players.RegisterTutorialPackOpening(ctx, opening)
	if err != nil || len(opening.Packs) != 1 {
		return nil, status.Error(codes.Internal, "tutorial pack transaction unavailable")
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "tutorial pack inventory unavailable")
	}
	language := languageForProfile(profile)
	acquired := &itempb.InventoryItems{}
	counts := make(map[string]int64)
	if !result.Replay {
		for _, cardID := range opening.Packs[0].Cards {
			instance := &itempb.CardInstance{CardInstance: tutorialCardInstance(s.players, cardID, language), Amount: 1}
			acquired.CardInstances = append(acquired.CardInstances, instance)
			counts[cardID]++
		}
		acquired.Exps = []*itempb.Exp{{Amount: setting.Experience}}
		acquired.PackCeilPoints = []*itempb.PackCeilPoint{{Amount: setting.CeilPoints, PackCeilPointSharedGroupId: setting.CeilGroupID}}
		if setting.GuaranteeID != "" {
			acquired.PackGranteePoints = []*itempb.PackGuaranteePoint{{Id: setting.GuaranteeID, Amount: 1}}
		}
	}
	return &api.TutorialPackPurchaseV1_Types_Response{TutorialId: request.GetTutorialId(), TutorialStep: request.GetTutorialStep(), UnpackOrder: tutorialUnpackOrder(s.players, profile, opening.PackID, opening.Packs[0], opening.TransactionID), ItemAcquisitionResult: packPurchaseAcquisition(profile, acquired, counts, opening, language)}, nil
}

func (s tutorialServer) grant(ctx context.Context, playerID string, input player.TutorialGrantInput) (player.TutorialGrantResult, error) {
	result, err := s.players.GrantTutorial(ctx, playerID, input)
	if errors.Is(err, player.ErrValidation) {
		return result, status.Error(codes.InvalidArgument, err.Error())
	}
	if err != nil {
		return result, status.Error(codes.Internal, "tutorial progress unavailable")
	}
	return result, nil
}

func cardChanges(cardIDs []string) []store.ShopInventoryChange {
	changes := make([]store.ShopInventoryChange, 0, len(cardIDs))
	for _, cardID := range cardIDs {
		changes = append(changes, store.ShopInventoryChange{Kind: "card", ID: cardID, Amount: 1})
	}
	return changes
}

func tutorialCardInstance(players *player.Manager, cardID string, language languagepb.Language) *cardpb.CardInstance {
	definition, _ := players.CardDefinition(cardID)
	return &cardpb.CardInstance{CardId: cardID, Lang: language, ExpansionId: firstExpansionID(definition.ExpansionIDs)}
}

func tutorialUnpackOrder(players *player.Manager, profile player.Profile, packID string, opened store.OpeningPack, transactionID string) *packpb.PackUnpackOrder {
	if transactionID == "" {
		transactionID = uuid.NewString()
	}
	language := languageForProfile(profile)
	produces := &orderpb.OrderResources{PackTableId: opened.TableID}
	for _, cardID := range opened.Cards {
		produces.CardInstances = append(produces.CardInstances, &itempb.CardInstance{CardInstance: tutorialCardInstance(players, cardID, language), Amount: 1})
	}
	orderID := uuid.NewSHA1(uuid.NameSpaceOID, []byte("tutorial-order:"+transactionID)).String()
	return &packpb.PackUnpackOrder{TransactionId: transactionID, OrderId: orderID, OrderedAt: timestamppb.Now(), Consumes: &orderpb.OrderResources{Packs: []*itempb.Pack{{Id: packID, Amount: 1}}}, Produces: produces, Lang: language, PackTableId: opened.TableID}
}

func tutorialPackTransactionID(playerID, tutorialID string, step int64, packID string) string {
	key := fmt.Sprintf("tutorial-pack:%s:%s:%d:%s", playerID, tutorialID, step, packID)
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(key)).String()
}

func localChallengePower() *feedpb.ChallengePower {
	return &feedpb.ChallengePower{Amount: 5, LastAutoHealedAt: timestamppb.Now(), AutoHealLimit: 5, HealSecPerPower: 43200, ManualHealLimit: 8}
}

func registerSupplementalServers(server grpc.ServiceRegistrar, players *player.Manager, packs *packlab.Engine) {
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
	api.RegisterTutorialServer(server, tutorialServer{players: players, packs: packs})
}
