package playerapi

import (
	"context"
	"errors"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	cardpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card"
	givecardpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/give_card"
	languagepb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/language"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type giveCardServer struct {
	api.UnimplementedGiveCardServer
	players *player.Manager
}

func (s giveCardServer) GetFriendsV1(ctx context.Context, _ *api.GiveCardGetFriendsV1_Types_Request) (*api.GiveCardGetFriendsV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	social, err := s.players.SocialSnapshot(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load local friends")
	}
	dayStart := time.Unix((time.Now().UTC().Unix()/86400)*86400, 0).UTC()
	response := &api.GiveCardGetFriendsV1_Types_Response{}
	myHistory, _ := s.players.GiveCardHistories(ctx, current.ID, dayStart)
	for _, history := range myHistory {
		if history.SenderPlayerID == current.ID {
			response.TodayGivenHistories = append(response.TodayGivenHistories, protoGiveCardHistory(history))
		}
	}
	for _, friend := range social.Friends {
		profile, err := s.players.Profile(ctx, friend.PlayerID)
		if err != nil {
			continue
		}
		desired, _ := s.players.DesiredCards(ctx, friend.PlayerID)
		friendHistory, _ := s.players.GiveCardHistories(ctx, friend.PlayerID, dayStart)
		var received int64
		for _, history := range friendHistory {
			if history.ReceiverPlayerID == friend.PlayerID {
				received++
			}
		}
		response.Friends = append(response.Friends, &givecardpb.GiveCardPlayerProfile{
			Profile:            profileSpine(profile),
			TodayReceivedCount: received,
			DesiredCardIds:     desiredCardIDs(desired),
		})
	}
	return response, nil
}

func (s giveCardServer) GetFriendDexRegisteredCardsV1(ctx context.Context, request *api.GiveCardGetFriendDexRegisteredCardsV1_Types_Request) (*api.GiveCardGetFriendDexRegisteredCardsV1_Types_Response, error) {
	profile, err := s.friendProfile(ctx, request.GetFriendPlayerId())
	if err != nil {
		return nil, err
	}
	response := &api.GiveCardGetFriendDexRegisteredCardsV1_Types_Response{}
	for _, card := range profile.Cards {
		if card.Quantity > 0 && cardInExpansion(card.Definition.ExpansionIDs, request.GetExpansionId()) {
			response.DexRegisteredCardIds = append(response.DexRegisteredCardIds, card.Definition.ID)
		}
	}
	desired, _ := s.players.DesiredCards(ctx, profile.Player.ID)
	response.DesiredCardIds = desiredCardIDs(desired)
	return response, nil
}

func (s giveCardServer) GetFriendDexProgressesV1(ctx context.Context, request *api.GiveCardGetFriendDexProgressesV1_Types_Request) (*api.GiveCardGetFriendDexProgressesV1_Types_Response, error) {
	profile, err := s.friendProfile(ctx, request.GetFriendPlayerId())
	if err != nil {
		return nil, err
	}
	response := &api.GiveCardGetFriendDexProgressesV1_Types_Response{}
	for _, expansionID := range request.GetExpansionIds() {
		var count int64
		for _, card := range profile.Cards {
			if card.Quantity > 0 && cardInExpansion(card.Definition.ExpansionIDs, expansionID) {
				count++
			}
		}
		response.ExpansionDexProgresses = append(response.ExpansionDexProgresses, &givecardpb.GiveCardPlayerExpansionDexProgress{ExpansionId: expansionID, Count: count})
	}
	return response, nil
}

func (s giveCardServer) GetHistoryV1(ctx context.Context, _ *api.GiveCardGetHistoryV1_Types_Request) (*api.GiveCardGetHistoryV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	histories, err := s.players.GiveCardHistories(ctx, current.ID, time.Unix(0, 0))
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load card gift history")
	}
	response := &api.GiveCardGetHistoryV1_Types_Response{}
	for _, history := range histories {
		if history.SenderPlayerID != current.ID {
			continue
		}
		receiver, err := s.players.Profile(ctx, history.ReceiverPlayerID)
		if err != nil {
			continue
		}
		response.Histories = append(response.Histories, &givecardpb.GiveCardHistoryWithFriendProfile{
			GivenCard:                protoGiftCard(history),
			ReceiverPlayerId:         history.ReceiverPlayerID,
			GivenAt:                  timestamppb.New(history.GivenAt),
			GivenCardRemainingAmount: history.SenderRemainingAmount,
			Nickname:                 receiver.Player.DisplayName,
			IconId:                   receiver.Settings.IconID,
		})
	}
	return response, nil
}

func (s giveCardServer) ExecuteV1(ctx context.Context, request *api.GiveCardExecuteV1_Types_Request) (*api.GiveCardExecuteV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	card := request.GetCard()
	if card == nil {
		return nil, status.Error(codes.InvalidArgument, "card is required")
	}
	language := card.GetLang()
	if language == languagepb.Language_LANGUAGE_UNSPECIFIED {
		profile, profileErr := s.players.Profile(ctx, current.ID)
		if profileErr != nil {
			return nil, status.Error(codes.Internal, "cannot load local profile")
		}
		language = languageForProfile(profile)
	}
	if _, err := s.players.GiveCard(ctx, current.ID, request.GetReceiverPlayerId(), card.GetCardId(), card.GetExpansionId(), int32(language)); err != nil {
		switch {
		case errors.Is(err, store.ErrInsufficientResources), errors.Is(err, store.ErrRuleViolation):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		default:
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
	}
	if err := s.players.AddAction(ctx, current.ID, player.ActionGiveCardTotal, request.GetReceiverPlayerId(), 1, true); err != nil {
		return nil, status.Error(codes.Internal, "cannot record card sharing action")
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot reload local profile")
	}
	return &api.GiveCardExecuteV1_Types_Response{PlayerResources: toProtoResources(profile)}, nil
}

func (s giveCardServer) friendProfile(ctx context.Context, friendID string) (player.Profile, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return player.Profile{}, err
	}
	social, err := s.players.SocialSnapshot(ctx, current.ID)
	if err != nil {
		return player.Profile{}, status.Error(codes.Internal, "cannot load local friends")
	}
	for _, friend := range social.Friends {
		if friend.PlayerID == friendID {
			profile, err := s.players.Profile(ctx, friendID)
			if err != nil {
				return player.Profile{}, status.Error(codes.NotFound, "local friend profile not found")
			}
			return profile, nil
		}
	}
	return player.Profile{}, status.Error(codes.FailedPrecondition, "player is not a local friend")
}

func desiredCardIDs(cards []store.DesiredCard) []string {
	result := make([]string, 0, len(cards))
	for _, card := range cards {
		result = append(result, card.CardID)
	}
	return result
}

func cardInExpansion(expansions []string, wanted string) bool {
	for _, expansion := range expansions {
		if expansion == wanted {
			return true
		}
	}
	return false
}

func protoGiveCardHistory(history store.GiveCardHistory) *givecardpb.GiveCardHistory {
	return &givecardpb.GiveCardHistory{GivenCard: protoGiftCard(history), ReceiverPlayerId: history.ReceiverPlayerID, GivenAt: timestamppb.New(history.GivenAt)}
}

func protoGiftCard(history store.GiveCardHistory) *cardpb.CardInstance {
	return &cardpb.CardInstance{CardId: history.CardID, ExpansionId: history.ExpansionID, Lang: languagepb.Language(history.Language)}
}

type thankRewardServer struct {
	api.UnimplementedThankRewardServer
	players *player.Manager
}

func (s thankRewardServer) SendV1(ctx context.Context, request *api.ThankRewardSendV1_Types_Request) (*api.ThankRewardSendV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.players.SendThankReward(ctx, current.ID, request.GetTargetPlayerId(), int32(request.GetRouteType())); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.players.AddAction(ctx, current.ID, player.ActionThankSent, request.GetTargetPlayerId(), 1, true); err != nil {
		return nil, status.Error(codes.Internal, "cannot record thank action")
	}
	return &api.ThankRewardSendV1_Types_Response{}, nil
}

func (thankRewardServer) SendToBotV1(context.Context, *api.ThankRewardSendToBotV1_Types_Request) (*api.ThankRewardSendToBotV1_Types_Response, error) {
	return &api.ThankRewardSendToBotV1_Types_Response{}, nil
}
