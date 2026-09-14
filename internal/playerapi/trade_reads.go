package playerapi

import (
	"context"
	"errors"
	"time"

	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	language "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/language"
	trade "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/trade"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const tradeUnlockAge = 14 * 24 * time.Hour

func (s startupTradeServer) GetFriendsV1(ctx context.Context, _ *api.TradeGetFriendsV1_Types_Request) (*api.TradeGetFriendsV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	me, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load local profile")
	}
	social, err := s.players.SocialSnapshot(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load local friends")
	}
	response := &api.TradeGetFriendsV1_Types_Response{}
	for _, friend := range social.Friends {
		profile, err := s.players.Profile(ctx, friend.PlayerID)
		if err != nil {
			continue
		}
		friendState, err := s.players.TradeState(ctx, friend.PlayerID)
		if err != nil {
			continue
		}
		desired, _ := s.players.DesiredCards(ctx, friend.PlayerID)
		tradeStatus := trade.TradePlayerProfile_Types_TRADE_STATUS_CAN_TRADE
		canTrade := true
		if time.Since(me.Player.CreatedAt) < tradeUnlockAge || time.Since(profile.Player.CreatedAt) < tradeUnlockAge {
			tradeStatus = trade.TradePlayerProfile_Types_TRADE_STATUS_FEATURE_LOCKED
			canTrade = false
		} else if friendState.BlockAll {
			tradeStatus = trade.TradePlayerProfile_Types_TRADE_STATUS_BLOCKED
			canTrade = false
		}
		response.Profiles = append(response.Profiles, &trade.TradePlayerProfile{
			Profile: profileSpine(profile), CanTrade: canTrade, Status: tradeStatus,
			DesiredCardIds: desiredCardIDs(desired),
		})
	}
	return response, nil
}

func (s startupTradeServer) GetSettingV1(ctx context.Context, _ *api.TradeGetSettingV1_Types_Request) (*api.TradeGetSettingV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	state, err := s.players.TradeState(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load trade settings")
	}
	return &api.TradeGetSettingV1_Types_Response{Settings: protoTradeSettings(state.BlockAll)}, nil
}

func (s startupTradeServer) SaveSettingV1(ctx context.Context, request *api.TradeSaveSettingV1_Types_Request) (*api.TradeSaveSettingV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if request.GetSettings() == nil {
		return nil, status.Error(codes.InvalidArgument, "trade settings are required")
	}
	state, err := s.players.SaveTradeSettings(ctx, current.ID, request.GetSettings().GetEnableTradeAllBlock())
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot save trade settings")
	}
	return &api.TradeSaveSettingV1_Types_Response{Settings: protoTradeSettings(state.BlockAll)}, nil
}

func (s startupTradeServer) GetTradePowerV1(ctx context.Context, _ *api.TradeGetTradePowerV1_Types_Request) (*api.TradeGetTradePowerV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	state, err := s.players.TradeState(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load trade power")
	}
	return &api.TradeGetTradePowerV1_Types_Response{TradePower: protoTradePower(state.Power, state.PowerUpdatedAt)}, nil
}

func (s startupTradeServer) HealTradePowerV1(ctx context.Context, request *api.TradeHealTradePowerV1_Types_Request) (*api.TradeHealTradePowerV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if request.GetItems() == nil {
		return nil, status.Error(codes.InvalidArgument, "trade power heal items are required")
	}
	chargers := make(map[string]int64)
	for _, charger := range request.GetItems().GetChargers() {
		if charger != nil {
			chargers[charger.GetId()] += charger.GetAmount()
		}
	}
	state, err := s.players.HealTradePower(ctx, current.ID, chargers, request.GetItems().GetVcAmount())
	if err != nil {
		if errors.Is(err, store.ErrInsufficientInventory) || errors.Is(err, store.ErrRuleViolation) {
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load healed trade resources")
	}
	return &api.TradeHealTradePowerV1_Types_Response{TradePower: protoTradePower(state.Power, state.PowerUpdatedAt), ItemAcquisitionResult: emptyItemAcquisitionResult(profile)}, nil
}

func (s startupTradeServer) SavePreMessageV1(ctx context.Context, request *api.TradeSavePreMessageV1_Types_Request) (*api.TradeSavePreMessageV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	message := request.GetPreMessage()
	if message == nil {
		return nil, status.Error(codes.InvalidArgument, "trade pre-message is required")
	}
	state, err := s.players.SaveTradeMessage(ctx, current.ID, message.GetTradeMessageStanceId(), int32(message.GetLanguage()))
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &api.TradeSavePreMessageV1_Types_Response{PreMessage: protoTradeMessage(state.MessageStanceID, state.MessageLanguage)}, nil
}

func (s startupTradeServer) GetPreMessageV1(ctx context.Context, request *api.TradeGetPreMessageV1_Types_Request) (*api.TradeGetPreMessageV1_Types_Response, error) {
	playerID := request.GetPlayerId()
	if playerID == "" {
		current, err := currentPlayer(ctx)
		if err != nil {
			return nil, err
		}
		playerID = current.ID
	}
	state, err := s.players.TradeState(ctx, playerID)
	if err != nil {
		return nil, status.Error(codes.NotFound, "trade profile not found")
	}
	return &api.TradeGetPreMessageV1_Types_Response{PreMessage: protoTradeMessage(state.MessageStanceID, state.MessageLanguage)}, nil
}

func protoTradeSettings(blockAll bool) *trade.TradeSettings {
	return &trade.TradeSettings{EnableTradeAllBlock: blockAll}
}

func protoTradeMessage(stanceID string, lang int32) *trade.TradeMessage {
	return &trade.TradeMessage{TradeMessageStanceId: stanceID, Language: language.Language(lang)}
}

func protoTradePower(amount int64, updatedAt time.Time) *trade.TradePower {
	return &trade.TradePower{
		Amount: amount, LastAutoHealedAt: timestamppb.New(updatedAt), AutoHealLimit: 5,
		HealSecPerPower: int64((24 * time.Hour).Seconds()), ManualHealLimit: 8,
	}
}
