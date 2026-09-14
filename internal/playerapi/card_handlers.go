package playerapi

import (
	"context"

	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	cardpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (s startupCardServer) GetDesiresV1(ctx context.Context, request *api.CardGetDesiresV1_Types_Request) (*api.CardGetDesiresV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	playerID := request.GetPlayerId()
	if playerID == "" {
		playerID = current.ID
	}
	cards, err := s.players.DesiredCards(ctx, playerID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load desired cards")
	}
	return &api.CardGetDesiresV1_Types_Response{DesiredCards: protoDesiredCards(cards)}, nil
}

func (s startupCardServer) SetDesiresV1(ctx context.Context, request *api.CardSetDesiresV1_Types_Request) (*api.CardSetDesiresV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	cards := make([]store.DesiredCard, 0, len(request.GetDesiredCards()))
	for _, card := range request.GetDesiredCards() {
		if card != nil {
			cards = append(cards, store.DesiredCard{CardID: card.GetCardId(), DisplayOrder: card.GetDisplayOrder(), ProfileOrder: card.GetProfileOrder()})
		}
	}
	if err := s.players.SetDesiredCards(ctx, current.ID, cards); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	stored, err := s.players.DesiredCards(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot reload desired cards")
	}
	return &api.CardSetDesiresV1_Types_Response{DesiredCards: protoDesiredCards(stored)}, nil
}

func (s startupCardServer) GetUsageV1(ctx context.Context, request *api.CardGetUsageV1_Types_Request) (*api.CardGetUsageV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	wanted := make(map[string]bool, len(request.GetCards()))
	for _, card := range request.GetCards() {
		if card != nil && card.GetCardId() != "" {
			wanted[card.GetCardId()] = true
		}
	}
	decks, err := s.players.Decks(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot inspect local decks")
	}
	usedFromDeck := false
	for _, deck := range decks {
		for cardID := range deck.Cards {
			if wanted[cardID] {
				usedFromDeck = true
				break
			}
		}
	}
	cardUsage := &cardpb.UsageStatus{IsUsedFromDeck: usedFromDeck}
	skinUsage := &cardpb.UsageStatus{}
	frameUsage := &cardpb.UsageStatus{}
	albums, err := s.players.Showcases(ctx, current.ID, "album")
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot inspect local albums")
	}
	for _, stored := range albums {
		album, err := decodeAlbum(stored)
		if err != nil {
			continue
		}
		for _, slot := range album.GetSlots() {
			markShowcaseUsage(slot.GetCardInstance(), wanted, true, cardUsage, skinUsage, frameUsage)
		}
	}
	mounts, err := s.players.Showcases(ctx, current.ID, "mount")
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot inspect local mounts")
	}
	for _, stored := range mounts {
		mount, err := decodeMount(stored)
		if err != nil {
			continue
		}
		for _, slot := range mount.GetSlots() {
			markShowcaseUsage(slot.GetCardInstance(), wanted, false, cardUsage, skinUsage, frameUsage)
		}
	}
	return &api.CardGetUsageV1_Types_Response{
		CardUsage:      cardUsage,
		CardSkinUsage:  skinUsage,
		CardFrameUsage: frameUsage,
	}, nil
}

func markShowcaseUsage(card *cardpb.CardInstance, wanted map[string]bool, album bool, cardUsage, skinUsage, frameUsage *cardpb.UsageStatus) {
	if card == nil || !wanted[card.GetCardId()] {
		return
	}
	if album {
		cardUsage.IsUsedFromAlbum = true
		skinUsage.IsUsedFromAlbum = card.GetCardSkin() != nil
		frameUsage.IsUsedFromAlbum = card.GetCardFrame() != nil
		return
	}
	cardUsage.IsUsedFromMount = true
	skinUsage.IsUsedFromMount = card.GetCardSkin() != nil
	frameUsage.IsUsedFromMount = card.GetCardFrame() != nil
}

func protoDesiredCards(cards []store.DesiredCard) []*cardpb.DesiredCard {
	result := make([]*cardpb.DesiredCard, 0, len(cards))
	for _, card := range cards {
		result = append(result, &cardpb.DesiredCard{CardId: card.CardID, DisplayOrder: card.DisplayOrder, ProfileOrder: card.ProfileOrder})
	}
	return result
}
