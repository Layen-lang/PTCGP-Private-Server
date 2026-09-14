package playerapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	cardpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card"
	feedpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/feed"
	itempb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	itemacquisitionpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item_acquisition"
	languagepb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/language"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s startupFeedServer) GetTimelineV1(ctx context.Context, _ *api.FeedGetTimelineV1_Types_Request) (*api.FeedGetTimelineV1_Types_Response, error) {
	timeline, power, err := s.feedTimeline(ctx)
	if err != nil {
		return nil, err
	}
	return &api.FeedGetTimelineV1_Types_Response{Timeline: timeline, ChallengePower: power}, nil
}

func (s startupFeedServer) RenewTimelineV1(ctx context.Context, _ *api.FeedRenewTimelineV1_Types_Request) (*api.FeedRenewTimelineV1_Types_Response, error) {
	timeline, power, err := s.feedTimeline(ctx)
	if err != nil {
		return nil, err
	}
	return &api.FeedRenewTimelineV1_Types_Response{Timeline: timeline, ChallengePower: power}, nil
}

func (s startupFeedServer) ChallengeV1(ctx context.Context, request *api.FeedChallengeV1_Types_Request) (*api.FeedChallengeV1_Types_Response, error) {
	result, profile, err := s.challengeFeed(ctx, request.GetFeedId(), request.GetTransactionId())
	if err != nil {
		return nil, err
	}
	card := protoFeedCard(result, languageForProfile(profile))
	acquisition := feedCardAcquisition(profile, card)
	return &api.FeedChallengeV1_Types_Response{PickedCards: []*cardpb.CardInstance{card}, PickedSubstitutionItem: &itempb.InventoryItems{}, UpdatedCardStocks: matchingCardStocks(profile, result.Card.ID), ChallengePower: protoChallengePower(result.State), ItemAcquisitionResult: acquisition}, nil
}

func (s startupFeedServer) ChallengeV2(ctx context.Context, request *api.FeedChallengeV2_Types_Request) (*api.FeedChallengeV2_Types_Response, error) {
	transactionID := fmt.Sprintf("v2:%s:%d", request.GetFeedId(), request.GetChallengeType())
	result, profile, err := s.challengeFeed(ctx, request.GetFeedId(), transactionID)
	if err != nil {
		return nil, err
	}
	card := protoFeedCard(result, languageForProfile(profile))
	timeline, _, timelineErr := s.feedTimeline(ctx)
	if timelineErr != nil {
		return nil, timelineErr
	}
	return &api.FeedChallengeV2_Types_Response{PickedCards: []*cardpb.CardInstance{card}, PickedSubstitutionItem: &itempb.InventoryItems{}, Timeline: timeline, ItemAcquisitionResult: feedCardAcquisition(profile, card)}, nil
}

func (s startupFeedServer) HealChallengePowerV1(ctx context.Context, request *api.FeedHealChallengePowerV1_Types_Request) (*api.FeedHealChallengePowerV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if request.GetItems() == nil {
		return nil, status.Error(codes.InvalidArgument, "challenge power heal items are required")
	}
	chargers := map[string]int64{}
	for _, charger := range request.GetItems().GetChargers() {
		if charger != nil {
			chargers["CHALLENGE_CHARGER_110030"] += int64(charger.GetAmount())
		}
	}
	state, err := s.players.HealChallengePower(ctx, current.ID, chargers, request.GetItems().GetVcAmount())
	if err != nil {
		if errors.Is(err, store.ErrInsufficientInventory) || errors.Is(err, store.ErrRuleViolation) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	profile, _ := s.players.Profile(ctx, current.ID)
	return &api.FeedHealChallengePowerV1_Types_Response{ChallengePower: protoChallengePower(state), ItemAcquisitionResult: emptyItemAcquisitionResult(profile)}, nil
}

func (s startupFeedServer) SnoopV1(ctx context.Context, request *api.FeedSnoopV1_Types_Request) (*api.FeedSnoopV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if request.GetFeedType() != feedpb.FeedType_FEED_TYPE_SOMEONE {
		return nil, status.Error(codes.InvalidArgument, "only someone feeds can be snooped")
	}
	state, _, err := s.players.SnoopFeed(ctx, current.ID, request.GetFeedId(), request.GetUsedForRevivalChallengePower())
	if err != nil {
		if errors.Is(err, store.ErrInsufficientInventory) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	timeline, _, err := s.feedTimeline(ctx)
	if err != nil {
		return nil, err
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load snoop item state")
	}
	return &api.FeedSnoopV1_Types_Response{ChallengePower: protoChallengePower(state), Timeline: timeline, ItemAcquisitionResult: snoopItemAcquisitionResult(profile)}, nil
}

func (s startupFeedServer) feedTimeline(ctx context.Context) (*feedpb.FeedTimeline, *feedpb.ChallengePower, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, nil, err
	}
	entries, err := s.players.FeedEntries(ctx, current.ID)
	if err != nil {
		return nil, nil, status.Error(codes.Internal, "cannot load feed timeline")
	}
	state, err := s.players.ChallengeState(ctx, current.ID)
	if err != nil {
		return nil, nil, status.Error(codes.Internal, "cannot load challenge power")
	}
	timeline := &feedpb.FeedTimeline{TimelineId: "local-feed", RenewAfter: timestamppb.New(time.Now().Add(5 * time.Minute))}
	for _, entry := range entries {
		owner, profileErr := s.players.Profile(ctx, entry.OwnerID)
		if profileErr != nil {
			continue
		}
		feedLanguage := languageForProfile(owner)
		contents := &feedpb.FeedContents{Lang: feedLanguage}
		maxCost := int64(1)
		disabledCard := false
		for _, cardID := range entry.Cards {
			definition, definitionErr := s.players.CardDefinition(cardID)
			if definitionErr != nil {
				continue
			}
			cost := rarityFeedCost(definition.Rarity)
			if cost > maxCost {
				maxCost = cost
			}
			expansion := ""
			if len(definition.ExpansionIDs) > 0 {
				expansion = definition.ExpansionIDs[0]
			}
			disabled := !disabledCard && cardID == entry.ChallengedCardID
			contents.Cards = append(contents.Cards, &feedpb.FeedElementCard{CardId: cardID, Lang: feedLanguage, Disable: disabled, ExpansionId: expansion})
			disabledCard = disabledCard || disabled
		}
		challengeInfo := &feedpb.FeedChallengeInfo{StartAt: timestamppb.New(entry.CreatedAt), EndAt: timestamppb.New(entry.CreatedAt.Add(store.FeedLifetime)), RemainingCount: 1, RequireFeedStamina: maxCost, NextChallengeNum: 1, IsSnooping: entry.Snooping}
		if entry.ChallengedCardID != "" {
			challengeInfo.RemainingCount = 0
			challengeInfo.NextChallengeNum = 2
		}
		timeline.SomeoneFeeds = append(timeline.SomeoneFeeds, &feedpb.SomeoneFeed{SomeoneFeedId: entry.ID, Contents: contents, ChallengeInfo: challengeInfo, PackId: entry.PackID, Player: &feedpb.FeedSharedPlayer{PlayerId: owner.Player.ID, Nickname: owner.Player.DisplayName, IconId: owner.Settings.IconID}})
	}
	return timeline, protoChallengePower(state), nil
}

func (s startupFeedServer) challengeFeed(ctx context.Context, feedID, transactionID string) (player.FeedChallengeResult, player.Profile, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return player.FeedChallengeResult{}, player.Profile{}, err
	}
	result, err := s.players.ChallengeFeed(ctx, current.ID, feedID, transactionID)
	if err != nil {
		if errors.Is(err, store.ErrInsufficientInventory) {
			return result, player.Profile{}, status.Error(codes.FailedPrecondition, err.Error())
		}
		return result, player.Profile{}, status.Error(codes.InvalidArgument, err.Error())
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return result, player.Profile{}, status.Error(codes.Internal, "cannot load challenge reward")
	}
	return result, profile, nil
}

func protoChallengePower(state store.ChallengeState) *feedpb.ChallengePower {
	return &feedpb.ChallengePower{Amount: state.Power, LastAutoHealedAt: timestamppb.New(state.UpdatedAt), AutoHealLimit: store.ChallengePowerLimit, HealSecPerPower: int64(store.ChallengePowerPeriod.Seconds()), ManualHealLimit: 8}
}

func protoFeedCard(result player.FeedChallengeResult, language languagepb.Language) *cardpb.CardInstance {
	expansion := ""
	if len(result.Card.ExpansionIDs) > 0 {
		expansion = result.Card.ExpansionIDs[0]
	}
	return &cardpb.CardInstance{CardId: result.Card.ID, Lang: language, ExpansionId: expansion}
}

func feedCardAcquisition(profile player.Profile, card *cardpb.CardInstance) *itemacquisitionpb.ItemAcquisitionResult {
	result := emptyItemAcquisitionResult(profile)
	item := &itempb.CardInstance{CardInstance: card, Amount: 1}
	result.AcquiredItems.CardInstances = append(result.AcquiredItems.CardInstances, item)
	result.AcceptedItems.CardInstances = append(result.AcceptedItems.CardInstances, item)
	experience := &itempb.Exp{Amount: store.FeedChallengeExperience}
	result.AcquiredItems.Exps = append(result.AcquiredItems.Exps, experience)
	result.AcceptedItems.Exps = append(result.AcceptedItems.Exps, experience)
	return result
}

func snoopItemAcquisitionResult(profile player.Profile) *itemacquisitionpb.ItemAcquisitionResult {
	result := emptyItemAcquisitionResult()
	result.ItemState.RevivalClocks = toProtoResources(profile).GetRevivalClocks()
	return result
}

func matchingCardStocks(profile player.Profile, cardID string) []*cardpb.CardStock {
	for _, stock := range toProtoResources(profile).GetCardStocks() {
		if stock.GetCardId() == cardID {
			return []*cardpb.CardStock{stock}
		}
	}
	return nil
}

func rarityFeedCost(rarity int) int64 {
	if rarity >= 600 {
		return 4
	}
	if rarity == 400 || rarity == 500 {
		return 3
	}
	if rarity == 300 {
		return 2
	}
	return 1
}
