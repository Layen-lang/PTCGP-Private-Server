package playerapi

import (
	"context"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	cardpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card"
	deckpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/deck"
	language "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/language"
	pokemon "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/pokemon"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type deckServer struct {
	api.UnimplementedDeckServer
	players *player.Manager
}

func (s deckServer) GetListV1(ctx context.Context, _ *api.DeckGetListV1_Types_Request) (*api.DeckGetListV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	decks, err := s.players.Decks(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load local decks")
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load local profile")
	}
	response := &api.DeckGetListV1_Types_Response{}
	for _, deck := range decks {
		response.Decks = append(response.Decks, protoDeck(deck, s.players.Catalog(), languageForProfile(profile)))
	}
	return response, nil
}

func (s deckServer) GetRentalDecksV1(ctx context.Context, _ *api.DeckGetRentalDecksV1_Types_Request) (*api.DeckGetRentalDecksV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	states, err := s.players.RentalDecks(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load rental decks")
	}
	response := &api.DeckGetRentalDecksV1_Types_Response{}
	for _, state := range states {
		response.RentalDecks = append(response.RentalDecks, &deckpb.RentalDeckStatus{RentalDeckId: state.ID, UsedCount: uint64(state.UsedCount), ObtainedAt: timestamppb.New(state.ObtainedAt)})
	}
	return response, nil
}

func (s deckServer) SaveV1(ctx context.Context, request *api.DeckSaveV1_Types_Request) (*api.DeckSaveV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	input := request.GetDeck()
	if input == nil {
		return nil, status.Error(codes.InvalidArgument, "deck is required")
	}
	deck := store.Deck{ID: input.GetDeckId(), Name: input.GetDeckName(), Cards: make(map[string]int64), CaseType: int32(input.GetDeckCaseType()), ShieldID: input.GetDeckShieldId(), CoinSkinID: input.GetCoinSkinId(), PlayMatID: input.GetPlayMatId(), DisplayOrder: input.GetDisplayOrder()}
	for _, energy := range input.GetEnergyTypes() {
		deck.EnergyTypes = append(deck.EnergyTypes, int32(energy))
	}
	for _, slot := range input.GetSlots() {
		if slot.GetCardInstance().GetCardId() != "" {
			deck.Cards[slot.GetCardInstance().GetCardId()]++
		}
	}
	stored, err := s.players.SaveDeck(ctx, current.ID, deck)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.players.AddAction(ctx, current.ID, player.ActionDeckCreated, "", 1, true); err != nil {
		return nil, status.Error(codes.Internal, "cannot record deck action")
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load local profile")
	}
	return &api.DeckSaveV1_Types_Response{Deck: protoDeck(stored, s.players.Catalog(), languageForProfile(profile))}, nil
}

func (s deckServer) DeleteV1(ctx context.Context, request *api.DeckDeleteV1_Types_Request) (*api.DeckDeleteV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.players.DeleteDeck(ctx, current.ID, int64(request.GetDeckId())); err != nil {
		return nil, status.Error(codes.NotFound, err.Error())
	}
	return &api.DeckDeleteV1_Types_Response{}, nil
}

func (s deckServer) ChangeOrderV1(ctx context.Context, request *api.DeckChangeOrderV1_Types_Request) (*api.DeckChangeOrderV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	orders := make(map[int64]int64, len(request.GetOrders()))
	for _, order := range request.GetOrders() {
		orders[int64(order.GetDeckId())] = order.GetOrder()
	}
	if err := s.players.ChangeDeckOrder(ctx, current.ID, orders); err != nil {
		return nil, status.Error(codes.Internal, "cannot reorder local decks")
	}
	return &api.DeckChangeOrderV1_Types_Response{}, nil
}

func protoDeck(value store.Deck, catalog player.CatalogView, language language.Language) *deckpb.Deck {
	result := &deckpb.Deck{DeckId: value.ID, DeckName: value.Name, DeckCaseType: deckpb.DeckCaseType(value.CaseType), DeckShieldId: value.ShieldID, CoinSkinId: value.CoinSkinID, PlayMatId: value.PlayMatID, DisplayOrder: value.DisplayOrder}
	for _, energy := range value.EnergyTypes {
		result.EnergyTypes = append(result.EnergyTypes, pokemon.EnergyType(energy))
	}
	expansions := make(map[string]string, len(catalog.Cards))
	for _, card := range catalog.Cards {
		if len(card.ExpansionIDs) > 0 {
			expansions[card.ID] = card.ExpansionIDs[0]
		}
	}
	var slot uint64 = 1
	for _, card := range catalog.Cards {
		for count := int64(0); count < value.Cards[card.ID]; count++ {
			result.Slots = append(result.Slots, &deckpb.DeckSlot{SlotNumber: slot, CardInstance: &cardpb.CardInstance{CardId: card.ID, Lang: language, ExpansionId: expansions[card.ID]}})
			slot++
		}
	}
	return result
}
