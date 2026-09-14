package playerapi

import (
	"context"
	"errors"
	"fmt"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	albumpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/album"
	cardpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card"
	mountpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/mount"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	albumCardLimit = 30
	mountCardLimit = 1
)

type albumServer struct {
	api.UnimplementedAlbumServer
	players *player.Manager
}

func (s albumServer) ListV1(ctx context.Context, _ *api.AlbumListV1_Types_Request) (*api.AlbumListV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	values, err := s.players.Showcases(ctx, current.ID, "album")
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load albums")
	}
	response := &api.AlbumListV1_Types_Response{AlbumLimitNum: store.ShowcaseLimit}
	for _, value := range values {
		album, err := decodeAlbum(value)
		if err != nil {
			return nil, status.Error(codes.Internal, "cannot decode local album")
		}
		response.Albums = append(response.Albums, &api.AlbumSpine{AlbumId: album.GetAlbumId(), CoverId: album.GetCoverId(), BackgroundColor: album.GetBackgroundColor(), CardNum: uint64(len(album.GetSlots())), PublicSetting: album.GetPublicSetting(), DisplayOrder: album.GetDisplayOrder(), Slots: album.GetSlots(), FileName: album.GetFileName(), HashTagIds: album.GetHashTagIds()})
	}
	return response, nil
}

func (s albumServer) SetV1(ctx context.Context, request *api.AlbumSetV1_Types_Request) (*api.AlbumSetV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	album := request.GetAlbum()
	if album == nil || album.GetPublicSetting() == albumpb.PublicSetting_PUBLIC_SETTING_UNSPECIFIED || len(album.GetSlots()) > albumCardLimit {
		return nil, status.Error(codes.InvalidArgument, "invalid album")
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot validate album cards")
	}
	cards := make([]*cardpb.CardInstance, 0, len(album.GetSlots()))
	for _, slot := range album.GetSlots() {
		if slot != nil {
			cards = append(cards, slot.GetCardInstance())
		}
	}
	if err := validateShowcaseCards(profile, cards); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	payload, _ := proto.Marshal(album)
	if _, err := s.players.SaveShowcase(ctx, current.ID, "album", store.Showcase{ID: album.GetAlbumId(), DisplayOrder: album.GetDisplayOrder(), PublicSetting: int32(album.GetPublicSetting()), Payload: payload}); err != nil {
		return nil, showcaseError(err)
	}
	return &api.AlbumSetV1_Types_Response{}, nil
}

func (s albumServer) GetDetailV1(ctx context.Context, request *api.AlbumGetDetailV1_Types_Request) (*api.AlbumGetDetailV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	value, err := s.players.Showcase(ctx, current.ID, "album", request.GetAlbumId())
	if err != nil {
		return nil, showcaseError(err)
	}
	album, err := decodeAlbum(value)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot decode local album")
	}
	return &api.AlbumGetDetailV1_Types_Response{CardLimitNum: albumCardLimit, Album: album}, nil
}

func (s albumServer) GetOtherDetailV1(ctx context.Context, request *api.AlbumGetOtherDetailV1_Types_Request) (*api.AlbumGetOtherDetailV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	value, err := s.players.Showcase(ctx, request.GetOtherPlayerId(), "album", request.GetAlbumId())
	if err != nil {
		return nil, showcaseError(err)
	}
	if err := s.requireShowcaseVisible(ctx, current.ID, request.GetOtherPlayerId(), albumpb.PublicSetting(value.PublicSetting)); err != nil {
		return nil, err
	}
	album, err := decodeAlbum(value)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot decode local album")
	}
	return &api.AlbumGetOtherDetailV1_Types_Response{CardLimitNum: albumCardLimit, Album: album}, nil
}

func (s albumServer) ChangeOrderV1(ctx context.Context, request *api.AlbumChangeOrderV1_Types_Request) (*api.AlbumChangeOrderV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	orders := make([]store.ShowcaseOrder, 0, len(request.GetOrders()))
	for _, order := range request.GetOrders() {
		if order != nil {
			orders = append(orders, store.ShowcaseOrder{ID: order.GetAlbumId(), DisplayOrder: order.GetOrder()})
		}
	}
	if err := s.players.OrderShowcases(ctx, current.ID, "album", orders); err != nil {
		return nil, showcaseError(err)
	}
	return &api.AlbumChangeOrderV1_Types_Response{}, nil
}

func (s albumServer) DeleteV1(ctx context.Context, request *api.AlbumDeleteV1_Types_Request) (*api.AlbumDeleteV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.players.DeleteShowcase(ctx, current.ID, "album", request.GetAlbumId()); err != nil {
		return nil, showcaseError(err)
	}
	return &api.AlbumDeleteV1_Types_Response{}, nil
}

type mountServer struct {
	api.UnimplementedMountServer
	players *player.Manager
}

func (s mountServer) ListV1(ctx context.Context, _ *api.MountListV1_Types_Request) (*api.MountListV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	values, err := s.players.Showcases(ctx, current.ID, "mount")
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load mounts")
	}
	response := &api.MountListV1_Types_Response{MountLimitNum: store.ShowcaseLimit}
	for _, value := range values {
		mount, err := decodeMount(value)
		if err != nil {
			return nil, status.Error(codes.Internal, "cannot decode local mount")
		}
		response.Mounts = append(response.Mounts, mount)
	}
	return response, nil
}

func (s mountServer) SetV1(ctx context.Context, request *api.MountSetV1_Types_Request) (*api.MountSetV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	mount := request.GetMount()
	if mount == nil || mount.GetMountTemplateId() == "" || mount.GetPublicSetting() == albumpb.PublicSetting_PUBLIC_SETTING_UNSPECIFIED || len(mount.GetSlots()) > mountCardLimit {
		return nil, status.Error(codes.InvalidArgument, "invalid mount")
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot validate mount cards")
	}
	cards := make([]*cardpb.CardInstance, 0, len(mount.GetSlots()))
	for _, slot := range mount.GetSlots() {
		if slot != nil {
			cards = append(cards, slot.GetCardInstance())
		}
	}
	if err := validateShowcaseCards(profile, cards); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	payload, _ := proto.Marshal(mount)
	if _, err := s.players.SaveShowcase(ctx, current.ID, "mount", store.Showcase{ID: mount.GetMountId(), DisplayOrder: mount.GetDisplayOrder(), PublicSetting: int32(mount.GetPublicSetting()), Payload: payload}); err != nil {
		return nil, showcaseError(err)
	}
	return &api.MountSetV1_Types_Response{}, nil
}

func (s mountServer) GetDetailV1(ctx context.Context, request *api.MountGetDetailV1_Types_Request) (*api.MountGetDetailV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	value, err := s.players.Showcase(ctx, current.ID, "mount", request.GetMountId())
	if err != nil {
		return nil, showcaseError(err)
	}
	mount, err := decodeMount(value)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot decode local mount")
	}
	return &api.MountGetDetailV1_Types_Response{Mount: mount}, nil
}

func (s mountServer) GetOtherDetailV1(ctx context.Context, request *api.MountGetOtherDetailV1_Types_Request) (*api.MountGetOtherDetailV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	value, err := s.players.Showcase(ctx, request.GetOtherPlayerId(), "mount", request.GetMountId())
	if err != nil {
		return nil, showcaseError(err)
	}
	if err := (&albumServer{players: s.players}).requireShowcaseVisible(ctx, current.ID, request.GetOtherPlayerId(), albumpb.PublicSetting(value.PublicSetting)); err != nil {
		return nil, err
	}
	mount, err := decodeMount(value)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot decode local mount")
	}
	return &api.MountGetOtherDetailV1_Types_Response{Mount: mount}, nil
}

func (s mountServer) ChangeOrderV1(ctx context.Context, request *api.MountChangeOrderV1_Types_Request) (*api.MountChangeOrderV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	orders := make([]store.ShowcaseOrder, 0, len(request.GetOrders()))
	for _, order := range request.GetOrders() {
		if order != nil {
			orders = append(orders, store.ShowcaseOrder{ID: order.GetMountId(), DisplayOrder: order.GetOrder()})
		}
	}
	if err := s.players.OrderShowcases(ctx, current.ID, "mount", orders); err != nil {
		return nil, showcaseError(err)
	}
	return &api.MountChangeOrderV1_Types_Response{}, nil
}

func (s mountServer) DeleteV1(ctx context.Context, request *api.MountDeleteV1_Types_Request) (*api.MountDeleteV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.players.DeleteShowcase(ctx, current.ID, "mount", request.GetMountId()); err != nil {
		return nil, showcaseError(err)
	}
	return &api.MountDeleteV1_Types_Response{}, nil
}

func (s albumServer) requireShowcaseVisible(ctx context.Context, viewerID, ownerID string, setting albumpb.PublicSetting) error {
	if viewerID == ownerID || setting == albumpb.PublicSetting_PUBLIC_SETTING_ALL_USERS {
		return nil
	}
	if setting == albumpb.PublicSetting_PUBLIC_SETTING_FRIEND_ONLY {
		social, err := s.players.SocialSnapshot(ctx, viewerID)
		if err == nil {
			for _, friend := range social.Friends {
				if friend.PlayerID == ownerID {
					return nil
				}
			}
		}
	}
	return status.Error(codes.PermissionDenied, "showcase is private")
}

func decodeAlbum(value store.Showcase) (*albumpb.Album, error) {
	result := &albumpb.Album{}
	if err := proto.Unmarshal(value.Payload, result); err != nil {
		return nil, err
	}
	result.AlbumId, result.DisplayOrder, result.PublicSetting = value.ID, value.DisplayOrder, albumpb.PublicSetting(value.PublicSetting)
	return result, nil
}

func decodeMount(value store.Showcase) (*mountpb.Mount, error) {
	result := &mountpb.Mount{}
	if err := proto.Unmarshal(value.Payload, result); err != nil {
		return nil, err
	}
	result.MountId, result.DisplayOrder, result.PublicSetting = value.ID, value.DisplayOrder, albumpb.PublicSetting(value.PublicSetting)
	return result, nil
}

func validateShowcaseCards(profile player.Profile, cards []*cardpb.CardInstance) error {
	owned := make(map[string]int64, len(profile.Cards))
	for _, card := range profile.Cards {
		owned[card.Definition.ID] = card.Quantity
	}
	used := make(map[string]int64)
	for _, card := range cards {
		if card == nil || card.GetCardId() == "" {
			return fmt.Errorf("showcase card is required")
		}
		used[card.GetCardId()]++
		if used[card.GetCardId()] > owned[card.GetCardId()] {
			return fmt.Errorf("not enough copies of card %q", card.GetCardId())
		}
	}
	return nil
}

func showcaseError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	if errors.Is(err, store.ErrRuleViolation) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return status.Error(codes.Internal, "local showcase unavailable")
}
