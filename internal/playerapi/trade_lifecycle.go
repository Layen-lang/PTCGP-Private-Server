package playerapi

import (
	"context"
	"errors"
	"strconv"

	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	cardpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card"
	itempb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	languagepb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/language"
	tradepb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/trade"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s startupTradeServer) GetV1(ctx context.Context, _ *api.TradeGetV1_Types_Request) (*api.TradeGetV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	session, err := s.players.ActiveTradeSession(ctx, current.ID)
	if errors.Is(err, store.ErrNotFound) {
		return &api.TradeGetV1_Types_Response{}, nil
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load active trade")
	}
	value, err := s.protoTradeSession(ctx, current.ID, session)
	if err != nil {
		return nil, err
	}
	return &api.TradeGetV1_Types_Response{TradeSession: value}, nil
}

func (s startupTradeServer) SubmitProposalV2(ctx context.Context, request *api.TradeSubmitProposalV2_Types_Request) (*api.TradeSubmitProposalV2_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load local profile")
	}
	card, err := requestTradeCard(request.GetTradeSubmission(), languageForProfile(profile))
	if err != nil {
		return nil, err
	}
	deposit, err := proto.Marshal(request.GetDepositItems())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid trade deposit")
	}
	message := request.GetProposerMessage()
	input := store.SubmitTradeInput{ProposerPlayerID: current.ID, PartnerPlayerID: request.GetPartnerPlayerId(), Card: card, Deposit: deposit}
	if message != nil {
		input.MessageStanceID, input.MessageLanguage = message.GetTradeMessageStanceId(), int32(message.GetLanguage())
	}
	session, power, err := s.players.SubmitTrade(ctx, input)
	if err != nil {
		return nil, tradeRuleError(err)
	}
	profile, err = s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot reload trade inventory")
	}
	value, err := s.protoTradeSession(ctx, current.ID, session)
	if err != nil {
		return nil, err
	}
	return &api.TradeSubmitProposalV2_Types_Response{TradeSession: value, ItemState: toProtoResources(profile), TradePower: protoTradePower(power.Power, power.PowerUpdatedAt)}, nil
}

func (s startupTradeServer) AcceptProposalV2(ctx context.Context, request *api.TradeAcceptProposalV2_Types_Request) (*api.TradeAcceptProposalV2_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load local profile")
	}
	card, err := requestTradeCard(request.GetTradeSubmission(), languageForProfile(profile))
	if err != nil {
		return nil, err
	}
	deposit, err := proto.Marshal(request.GetDepositItems())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid trade deposit")
	}
	session, power, err := s.players.AcceptTrade(ctx, current.ID, request.GetTradeSessionId(), card, deposit)
	if err != nil {
		return nil, tradeRuleError(err)
	}
	profile, err = s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot reload trade inventory")
	}
	value, err := s.protoTradeSession(ctx, current.ID, session)
	if err != nil {
		return nil, err
	}
	return &api.TradeAcceptProposalV2_Types_Response{TradeSession: value, ItemState: toProtoResources(profile), TradePower: protoTradePower(power.Power, power.PowerUpdatedAt)}, nil
}

func (s startupTradeServer) ConfirmV1(ctx context.Context, request *api.TradeConfirmV1_Types_Request) (*api.TradeConfirmV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	session, err := s.players.ConfirmTrade(ctx, current.ID, request.GetTradeSessionId())
	if err != nil {
		return nil, tradeRuleError(err)
	}
	return &api.TradeConfirmV1_Types_Response{Outcome: &tradepb.TradeOutcome{Card: protoStoreTradeCard(session.PartnerCard)}}, nil
}

func (s startupTradeServer) RejectV1(ctx context.Context, request *api.TradeRejectV1_Types_Request) (*api.TradeRejectV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	session, err := s.players.RejectTrade(ctx, current.ID, request.GetTradeSessionId())
	if err != nil {
		return nil, tradeRuleError(err)
	}
	return &api.TradeRejectV1_Types_Response{Deposit: protoTradeDeposit(current.ID, session)}, nil
}

func (s startupTradeServer) ReceiveOutcomesV1(ctx context.Context, request *api.TradeReceiveOutcomesV1_Types_Request) (*api.TradeReceiveOutcomesV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	_, outcome, err := s.players.ReceiveTradeOutcome(ctx, current.ID, request.GetTradeSessionId())
	if err != nil {
		return nil, tradeRuleError(err)
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot reload trade outcome")
	}
	acquisition := emptyItemAcquisitionResult(profile)
	acquisition.AcquiredItems.CardInstances = []*itempb.CardInstance{{CardInstance: protoStoreTradeCard(outcome), Amount: 1}}
	return &api.TradeReceiveOutcomesV1_Types_Response{Outcome: &tradepb.TradeOutcome{Card: protoStoreTradeCard(outcome)}, ItemAcquisitionResult: acquisition}, nil
}

func (s startupTradeServer) ReceiveDepositsV1(ctx context.Context, request *api.TradeReceiveDepositsV1_Types_Request) (*api.TradeReceiveDepositsV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	_, card, depositBytes, power, err := s.players.ReceiveTradeDeposit(ctx, current.ID, request.GetTradeSessionId())
	if err != nil {
		return nil, tradeRuleError(err)
	}
	depositItems := &itempb.InventoryItems{}
	if len(depositBytes) != 0 {
		if err := proto.Unmarshal(depositBytes, depositItems); err != nil {
			return nil, status.Error(codes.Internal, "cannot restore trade deposit")
		}
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot reload restored deposit")
	}
	acquisition := emptyItemAcquisitionResult(profile)
	acquisition.AcquiredItems = proto.Clone(depositItems).(*itempb.InventoryItems)
	acquisition.AcquiredItems.CardInstances = append(acquisition.AcquiredItems.CardInstances, &itempb.CardInstance{CardInstance: protoStoreTradeCard(card), Amount: 1})
	return &api.TradeReceiveDepositsV1_Types_Response{Deposit: &tradepb.TradeDeposit{PowerAmount: 1, Items: depositItems}, ItemAcquisitionResult: acquisition, TradePower: protoTradePower(power.Power, power.PowerUpdatedAt)}, nil
}

func (s startupTradeServer) GetHistoryV1(ctx context.Context, request *api.TradeGetHistoryV1_Types_Request) (*api.TradeGetHistoryV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	offset := 0
	if request.GetPageToken() != "" {
		offset, err = strconv.Atoi(request.GetPageToken())
		if err != nil || offset < 0 {
			return nil, status.Error(codes.InvalidArgument, "invalid trade history page token")
		}
	}
	sessions, err := s.players.TradeHistories(ctx, current.ID, 21, offset)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load trade history")
	}
	response := &api.TradeGetHistoryV1_Types_Response{}
	if len(sessions) > 20 {
		response.NextPageToken = strconv.Itoa(offset + 20)
		sessions = sessions[:20]
	}
	for _, session := range sessions {
		mine, partner, partnerID := session.ProposerCard, session.PartnerCard, session.PartnerPlayerID
		if current.ID == session.PartnerPlayerID {
			mine, partner, partnerID = session.PartnerCard, session.ProposerCard, session.ProposerPlayerID
		}
		partnerProfile, err := s.players.Profile(ctx, partnerID)
		if err != nil {
			continue
		}
		response.Histories = append(response.Histories, &tradepb.TradeHistory{TradeSessionId: session.ID, MySubmission: protoStoreTradeCard(mine), PartnerSubmission: protoStoreTradeCard(partner), CompletedAt: timestamppb.New(session.CompletedAt), MySubmissionCardLastAmount: mine.LastAmount, PartnerSubmissionCardLastAmount: partner.LastAmount, Nickname: partnerProfile.Player.DisplayName, IconId: partnerProfile.Settings.IconID})
	}
	return response, nil
}

func (s startupTradeServer) protoTradeSession(ctx context.Context, viewerID string, session store.TradeSession) (*tradepb.TradeSession, error) {
	mine, partner, partnerID := session.ProposerCard, session.PartnerCard, session.PartnerPlayerID
	isProposer := viewerID == session.ProposerPlayerID
	if !isProposer {
		mine, partner, partnerID = session.PartnerCard, session.ProposerCard, session.ProposerPlayerID
	}
	profile, err := s.players.Profile(ctx, partnerID)
	if err != nil {
		return nil, status.Error(codes.NotFound, "trade partner profile not found")
	}
	value := &tradepb.TradeSession{SessionId: session.ID, MySubmission: protoStoreTradeCard(mine), PartnerSubmission: protoStoreTradeCard(partner), Partner: profileSpine(profile), IsProposer: isProposer, Status: &tradepb.TradeSessionStatus{ExpireAt: timestamppb.New(session.ExpireAt), State: tradeSessionState(viewerID, session)}, Deposit: protoTradeDeposit(viewerID, session)}
	if session.ProposerMessageStanceID != "" {
		value.ProposerMessage = protoTradeMessage(session.ProposerMessageStanceID, session.ProposerMessageLanguage)
	}
	if session.State == store.TradeSessionConfirmed {
		value.Outcome = &tradepb.TradeOutcome{Card: protoStoreTradeCard(partner)}
	}
	return value, nil
}

func tradeSessionState(viewerID string, session store.TradeSession) tradepb.TradeSessionState {
	switch session.State {
	case store.TradeSessionWaiting:
		if viewerID == session.ProposerPlayerID {
			return tradepb.TradeSessionState_TRADE_SESSION_STATE_WAIT_ANSWER
		}
		return tradepb.TradeSessionState_TRADE_SESSION_STATE_HAS_REQUEST
	case store.TradeSessionAnswered:
		if viewerID == session.ProposerPlayerID {
			return tradepb.TradeSessionState_TRADE_SESSION_STATE_HAS_ANSWER
		}
		return tradepb.TradeSessionState_TRADE_SESSION_STATE_WAIT_CONFIRM
	case store.TradeSessionConfirmed:
		return tradepb.TradeSessionState_TRADE_SESSION_STATE_READY_READY_TO_RECEIVE_OUTCOME
	case store.TradeSessionRejected:
		return tradepb.TradeSessionState_TRADE_SESSION_STATE_READY_READY_TO_RECEIVE_DEPOSIT
	default:
		return tradepb.TradeSessionState_TRADE_SESSION_STATE_UNSPECIFIED
	}
}

func protoTradeDeposit(viewerID string, session store.TradeSession) *tradepb.TradeDeposit {
	data := session.ProposerDeposit
	if viewerID == session.PartnerPlayerID {
		data = session.PartnerDeposit
	}
	items := &itempb.InventoryItems{}
	if len(data) != 0 {
		_ = proto.Unmarshal(data, items)
	}
	return &tradepb.TradeDeposit{PowerAmount: 1, Items: items}
}

func requestTradeCard(card *cardpb.CardInstance, fallbackLanguage languagepb.Language) (store.TradeCard, error) {
	if card == nil || card.GetCardId() == "" || card.GetExpansionId() == "" {
		return store.TradeCard{}, status.Error(codes.InvalidArgument, "trade card is required")
	}
	lang := card.GetLang()
	if lang == languagepb.Language_LANGUAGE_UNSPECIFIED {
		lang = fallbackLanguage
	}
	return store.TradeCard{CardID: card.GetCardId(), ExpansionID: card.GetExpansionId(), Language: int32(lang)}, nil
}

func protoStoreTradeCard(card store.TradeCard) *cardpb.CardInstance {
	if card.CardID == "" {
		return nil
	}
	return &cardpb.CardInstance{CardId: card.CardID, ExpansionId: card.ExpansionID, Lang: languagepb.Language(card.Language)}
}

func tradeRuleError(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, store.ErrInsufficientResources), errors.Is(err, store.ErrRuleViolation):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.InvalidArgument, err.Error())
	}
}
