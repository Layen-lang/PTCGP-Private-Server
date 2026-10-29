package playerapi

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/packlab"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	itempb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	orderpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/order"
	packpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/pack"
	specialpackpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/special_pack"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const specialPackResetHour = 1 // Weekly server reset: Saturday at 01:00 UTC.

type specialPackServer struct {
	api.UnimplementedSpecialPackServer
	players *player.Manager
	packs   *packlab.Engine
}

func (s specialPackServer) GetUnpackRightV1(ctx context.Context, _ *api.SpecialPackGetUnpackRightV1_Types_Request) (*api.SpecialPackGetUnpackRightV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	cycleStart, next := specialPackCycle(now)
	used, err := s.players.SpecialPackRightUsed(ctx, current.ID, cycleStart.Format("2006-01-02"))
	if err != nil {
		return nil, status.Error(codes.Internal, "special pack right unavailable")
	}
	availableAt := now
	if used {
		availableAt = next
	}
	return &api.SpecialPackGetUnpackRightV1_Types_Response{
		SpecialPackUnpackRight: &specialpackpb.SpecialPackUnpackRight{NextAvailableAt: timestamppb.New(availableAt)},
	}, nil
}

func (s specialPackServer) PurchaseV1(ctx context.Context, request *api.SpecialPackPurchaseV1_Types_Request) (*api.SpecialPackPurchaseV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	amount := int(request.GetAmount())
	if amount != 1 && amount != 10 {
		return nil, status.Error(codes.InvalidArgument, "amount must be ONE or TEN")
	}
	if request.GetPackConsistentToken() == "" {
		return nil, status.Error(codes.InvalidArgument, "pack_consistent_token is required")
	}
	product, err := s.players.SpecialPackProduct(request.GetProductId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "unknown special pack product")
	}
	pack, err := s.players.Pack(product.PackID)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "special pack is unavailable")
	}

	var ticketAmount int64
	var rightWeekKey string
	switch request.GetConsumedResource() {
	case api.SpecialPackPurchaseV1_Types_Request_Types_CONSUMED_RESOURCE_SPECIAL_PACK_UNPACK_RIGHT:
		if amount != 1 || request.GetConsumedTicketAmount() != 0 {
			return nil, status.Error(codes.InvalidArgument, "the weekly unpack right opens exactly one pack")
		}
		cycleStart, _ := specialPackCycle(time.Now().UTC())
		rightWeekKey = cycleStart.Format("2006-01-02")
	case api.SpecialPackPurchaseV1_Types_Request_Types_CONSUMED_RESOURCE_SPECIAL_PACK_TICKET:
		ticketAmount = int64(amount) * product.TicketAmount
		if request.GetConsumedTicketAmount() != ticketAmount {
			return nil, status.Error(codes.InvalidArgument, "consumed ticket amount does not match the requested pack amount")
		}
	case api.SpecialPackPurchaseV1_Types_Request_Types_CONSUMED_RESOURCE_SPECIAL_PACK_UNPACK_RIGHT_AND_TICKET:
		if amount != 10 {
			return nil, status.Error(codes.InvalidArgument, "the weekly right can be combined with tickets only for a ten-pack opening")
		}
		ticketAmount = int64(amount-1) * product.TicketAmount
		if request.GetConsumedTicketAmount() != ticketAmount {
			return nil, status.Error(codes.InvalidArgument, "consumed ticket amount does not match the requested pack amount")
		}
		cycleStart, _ := specialPackCycle(time.Now().UTC())
		rightWeekKey = cycleStart.Format("2006-01-02")
	default:
		return nil, status.Error(codes.InvalidArgument, "consumed_resource is required")
	}

	opening, err := s.packs.Preview(ctx, packlab.PreviewInput{PlayerID: current.ID, PackID: pack.ID, RequestedCount: amount})
	if err != nil || len(opening.Packs) != amount {
		return nil, status.Error(codes.Internal, "special pack draw unavailable")
	}
	opening.PlayerID = current.ID
	opening.TransactionID = uuid.NewString()
	opening.ProductID = product.ID
	opening.PackID = pack.ID
	opening.Free = true // Special packs consume special tickets, never normal pack stamina.
	opening.ExperienceReward = int64(amount) * 25
	opening.CeilPointReward = 0
	opening.ShineDustReward = 0
	committed, replayed, err := s.players.CommitPackOpening(ctx, store.CommitPackOpening{
		Opening: opening, SpecialPackTicketID: product.TicketID,
		SpecialPackTicketAmount: ticketAmount, SpecialPackRightWeekKey: rightWeekKey,
	})
	if errors.Is(err, store.ErrInsufficientResources) {
		return nil, status.Error(codes.FailedPrecondition, "insufficient special pack tickets or weekly unpack right")
	}
	if err != nil {
		return nil, status.Error(codes.Internal, "special pack opening failed")
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "special pack inventory unavailable")
	}
	language := languageForProfile(profile)
	acquired := &itempb.InventoryItems{Exps: []*itempb.Exp{{Amount: committed.ExperienceReward}}}
	cardCounts := make(map[string]int64)
	response := &api.SpecialPackPurchaseV1_Types_Response{
		ConsumedResource:     api.SpecialPackPurchaseV1_Types_Response_Types_ConsumedResource(request.GetConsumedResource()),
		ConsumedTicketAmount: ticketAmount,
	}
	for packIndex, openedPack := range committed.Packs {
		produces := &orderpb.OrderResources{PackTableId: openedPack.TableID}
		for _, cardID := range openedPack.Cards {
			instance := &itempb.CardInstance{CardInstance: tutorialCardInstance(s.players, cardID, language), Amount: 1}
			produces.CardInstances = append(produces.CardInstances, instance)
			acquired.CardInstances = append(acquired.CardInstances, instance)
			cardCounts[cardID]++
		}
		response.UnpackOrders = append(response.UnpackOrders, &packpb.PackUnpackOrder{
			TransactionId: committed.TransactionID, OrderId: fmt.Sprintf("%s-%03d", committed.ID, packIndex+1),
			OrderedAt: timestamppb.New(committed.CreatedAt), Consumes: &orderpb.OrderResources{},
			Produces: produces, Lang: language, PackTableId: openedPack.TableID,
		})
	}
	_ = replayed
	response.ItemAcquisitionResult = packPurchaseAcquisition(profile, acquired, cardCounts, committed, language)
	for _, owned := range profile.Items {
		if owned.Definition.Kind == catalog.ItemSpecialPackTicket {
			response.ItemAcquisitionResult.ItemState.SpecialPackTickets = append(response.ItemAcquisitionResult.ItemState.SpecialPackTickets, &itempb.SpecialPackTicket{Id: owned.Definition.ID, Amount: owned.Quantity})
		}
	}
	cycleStart, next := specialPackCycle(time.Now().UTC())
	used, err := s.players.SpecialPackRightUsed(ctx, current.ID, cycleStart.Format("2006-01-02"))
	if err != nil {
		return nil, status.Error(codes.Internal, "special pack right unavailable")
	}
	availableAt := time.Now().UTC()
	if used {
		availableAt = next
	}
	response.UnpackRight = &specialpackpb.SpecialPackUnpackRight{NextAvailableAt: timestamppb.New(availableAt)}
	if !replayed {
		_ = s.players.AddAction(ctx, current.ID, player.ActionPackOpened, pack.ID, int64(len(committed.Packs)), true)
		_ = s.players.AddAction(ctx, current.ID, player.ActionPackOpenTotal, "", int64(len(committed.Packs)), true)
		_ = s.players.AddAction(ctx, current.ID, player.ActionPackByExpansion, pack.ExpansionID, int64(len(committed.Packs)), true)
		for cardID, count := range cardCounts {
			_ = s.players.AddAction(ctx, current.ID, player.ActionGetCards, cardID, count, true)
			_ = s.players.AddAction(ctx, current.ID, player.ActionCardGetTotal, "", count, true)
		}
	}
	return response, nil
}

func specialPackCycle(now time.Time) (start, next time.Time) {
	now = now.UTC()
	daysUntilSaturday := (int(time.Saturday) - int(now.Weekday()) + 7) % 7
	next = time.Date(now.Year(), now.Month(), now.Day()+daysUntilSaturday, specialPackResetHour, 0, 0, 0, time.UTC)
	if !now.Before(next) {
		next = next.AddDate(0, 0, 7)
	}
	start = next.AddDate(0, 0, -7)
	return start, next
}
