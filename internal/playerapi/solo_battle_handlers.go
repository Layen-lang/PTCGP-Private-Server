package playerapi

import (
	"context"
	"errors"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	solopb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/solo_battle"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s startupSoloBattleServer) GetEventPowersV1(ctx context.Context, request *api.SoloBattleGetEventPowersV1_Types_Request) (*api.SoloBattleGetEventPowersV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	response := &api.SoloBattleGetEventPowersV1_Types_Response{}
	for _, eventID := range request.GetEventIds() {
		state, loadErr := s.players.EventPower(ctx, current.ID, eventID)
		if loadErr != nil {
			return nil, status.Error(codes.InvalidArgument, loadErr.Error())
		}
		response.EventPowers = append(response.EventPowers, protoEventPower(state))
	}
	return response, nil
}

func (s startupSoloBattleServer) StartEventBattleV1(ctx context.Context, request *api.SoloBattleStartEventBattleV1_Types_Request) (*api.SoloBattleStartEventBattleV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	battleID := request.GetSoloEventBattleId()
	token, _, err := s.players.StartEventSoloBattle(ctx, current.ID, battleID, battleID)
	if err != nil {
		if errors.Is(err, store.ErrInsufficientInventory) {
			return nil, status.Error(codes.FailedPrecondition, "not enough event power")
		}
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.players.AddAction(ctx, current.ID, player.ActionPVETry, battleID, 1, true); err != nil {
		return nil, status.Error(codes.Internal, "cannot record solo battle action")
	}
	usedCount, err := useRentalDeck(ctx, s.players, current.ID, request.GetDeck())
	if err != nil {
		return nil, err
	}
	tries, err := s.players.SoloBattleTries(ctx, current.ID, battleID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &api.SoloBattleStartEventBattleV1_Types_Response{BattleSessionToken: token, RentalDeckUsedCount: usedCount, BattleTries: protoSoloBattleTryProgresses(tries)}, nil
}

func (s startupSoloBattleServer) FinishEventBattleV1(ctx context.Context, request *api.SoloBattleFinishEventBattleV1_Types_Request) (*api.SoloBattleFinishEventBattleV1_Types_Response, error) {
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
	profile, _ := s.players.Profile(ctx, current.ID)
	state, _ := s.players.EventPower(ctx, current.ID, request.GetBattleId())
	return &api.SoloBattleFinishEventBattleV1_Types_Response{IsFirstClear: first, BattleTries: protoSoloBattleTryResults(tries), ItemAcquisitionResult: emptyItemAcquisitionResult(profile), EventPower: protoEventPower(state)}, nil
}

func (s startupSoloBattleServer) HealEventPowerV1(ctx context.Context, request *api.SoloBattleHealEventPowerV1_Types_Request) (*api.SoloBattleHealEventPowerV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if request.GetItems() == nil {
		return nil, status.Error(codes.InvalidArgument, "event power heal items are required")
	}
	chargers := map[string]int64{}
	for _, charger := range request.GetItems().GetChargers() {
		if charger != nil {
			chargers[charger.GetId()] += int64(charger.GetAmount())
		}
	}
	state, err := s.players.HealEventPower(ctx, current.ID, request.GetEventId(), chargers, request.GetItems().GetVcAmount())
	if err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	profile, _ := s.players.Profile(ctx, current.ID)
	return &api.SoloBattleHealEventPowerV1_Types_Response{EventPower: protoEventPower(state), ItemAcquisitionResult: emptyItemAcquisitionResult(profile)}, nil
}

func (s startupSoloBattleServer) StartRandomBattleV1(ctx context.Context, request *api.SoloBattleStartRandomBattleV1_Types_Request) (*api.SoloBattleStartRandomBattleV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	token, err := s.players.StartSoloBattle(ctx, current.ID, request.GetSoloRandomBattleId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.players.AddAction(ctx, current.ID, player.ActionPVETry, request.GetSoloRandomBattleId(), 1, true); err != nil {
		return nil, status.Error(codes.Internal, "cannot record solo battle action")
	}
	usedCount, err := useRentalDeck(ctx, s.players, current.ID, request.GetDeck())
	if err != nil {
		return nil, err
	}
	tries, err := s.players.SoloBattleTries(ctx, current.ID, request.GetSoloRandomBattleId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &api.SoloBattleStartRandomBattleV1_Types_Response{BattleSessionToken: token, RentalDeckUsedCount: usedCount, BattleTries: protoSoloBattleTryProgresses(tries)}, nil
}

func (s startupSoloBattleServer) FinishRandomBattleV1(ctx context.Context, request *api.SoloBattleFinishRandomBattleV1_Types_Request) (*api.SoloBattleFinishRandomBattleV1_Types_Response, error) {
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
	profile, _ := s.players.Profile(ctx, current.ID)
	return &api.SoloBattleFinishRandomBattleV1_Types_Response{IsFirstClear: first, BattleTries: protoSoloBattleTryResults(tries), ItemAcquisitionResult: emptyItemAcquisitionResult(profile)}, nil
}

func useRentalDeck(ctx context.Context, players *player.Manager, playerID string, deck *solopb.SoloBattleDeck) (int64, error) {
	if deck == nil || deck.GetType() != solopb.SoloBattleDeck_Types_DECK_TYPE_RENTAL_DECK {
		return 0, nil
	}
	used, err := players.UseRentalDeck(ctx, playerID, deck.GetRentalDeckId())
	if err != nil {
		return 0, status.Error(codes.FailedPrecondition, err.Error())
	}
	if err := players.AddAction(ctx, playerID, player.ActionRentalDeckUse, deck.GetRentalDeckId(), 1, true); err != nil {
		return 0, status.Error(codes.Internal, "cannot record rental deck action")
	}
	return used, nil
}

func saveSoloBattleTryProgresses(ctx context.Context, players *player.Manager, playerID, battleID string, progresses []*solopb.SoloBattleTryProgress) ([]store.SoloBattleTryState, error) {
	if len(progresses) == 0 {
		values, err := players.SoloBattleTries(ctx, playerID, battleID)
		if err != nil {
			return nil, status.Error(codes.InvalidArgument, err.Error())
		}
		return values, nil
	}
	values := make([]store.SoloBattleTryState, 0, len(progresses))
	for _, progress := range progresses {
		if progress != nil {
			values = append(values, store.SoloBattleTryState{ID: progress.GetBattleTryId(), CurrentCount: progress.GetCurrentCount()})
		}
	}
	stored, err := players.SaveSoloBattleTries(ctx, playerID, battleID, values)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return stored, nil
}

func protoSoloBattleTries(states []store.SoloBattleTryState) []*solopb.SoloBattleTry {
	result := make([]*solopb.SoloBattleTry, 0, len(states))
	for _, state := range states {
		result = append(result, &solopb.SoloBattleTry{BattleTryId: state.ID, CurrentCount: state.CurrentCount})
	}
	return result
}

func protoSoloBattleTryProgresses(states []store.SoloBattleTryState) []*solopb.SoloBattleTryProgress {
	result := make([]*solopb.SoloBattleTryProgress, 0, len(states))
	for _, state := range states {
		result = append(result, &solopb.SoloBattleTryProgress{BattleTryId: state.ID, CurrentCount: state.CurrentCount})
	}
	return result
}

func protoSoloBattleTryResults(states []store.SoloBattleTryState) []*solopb.SoloBattleTryProgressResult {
	result := make([]*solopb.SoloBattleTryProgressResult, 0, len(states))
	for _, state := range states {
		result = append(result, &solopb.SoloBattleTryProgressResult{BattleTryId: state.ID, CurrentCount: state.CurrentCount, IsReceivedReward: state.RewardReceived})
	}
	return result
}

func protoEventPower(state store.EventPowerState) *solopb.SoloBattleEventPower {
	return &solopb.SoloBattleEventPower{EventId: state.EventID, Amount: state.Power, LastAutoHealedAt: timestamppb.New(state.UpdatedAt), AutoHealLimit: store.EventPowerLimit, HealSecPerPower: int64(store.EventPowerPeriod.Seconds()), ManualHealLimit: 8}
}
