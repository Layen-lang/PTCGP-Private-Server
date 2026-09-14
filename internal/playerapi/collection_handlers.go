package playerapi

import (
	"context"
	"errors"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	albumpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/album"
	cardpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card"
	collectionpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/collection"
	mountpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/mount"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const collectionDiscoveryLimit = 20

func (s collectionServer) ListV1(ctx context.Context, _ *api.CollectionListV1_Types_Request) (*api.CollectionListV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	albums, mounts, err := s.collectionShowcases(ctx, current.ID, current.ID)
	if err != nil {
		return nil, err
	}
	response := &api.CollectionListV1_Types_Response{Albums: albums, Mounts: mounts}
	if payload, err := s.players.BestCollection(ctx, current.ID); err == nil {
		response.MyBest = decodeBestCollection(payload)
	} else if !errors.Is(err, store.ErrNotFound) {
		return nil, status.Error(codes.Internal, "cannot load best collection")
	}
	return response, nil
}

func (s collectionServer) SetMyBestV1(ctx context.Context, request *api.CollectionSetMyBestV1_Types_Request) (*api.CollectionSetMyBestV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	best := request.GetMyBest()
	if best == nil || best.GetCollectionId() == 0 {
		return nil, status.Error(codes.InvalidArgument, "best collection is required")
	}
	derived, err := s.deriveBestCollection(ctx, current.ID, best.GetMyBestType(), best.GetCollectionId())
	if err != nil {
		return nil, err
	}
	payload, err := proto.Marshal(derived)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot encode best collection")
	}
	if err := s.players.SaveBestCollection(ctx, current.ID, payload); err != nil {
		return nil, status.Error(codes.Internal, "cannot save best collection")
	}
	if err := s.players.AddAction(ctx, current.ID, player.ActionBoardCreated, "", 1, true); err != nil {
		return nil, status.Error(codes.Internal, "cannot record collection action")
	}
	return &api.CollectionSetMyBestV1_Types_Response{}, nil
}

func (s collectionServer) LikeV1(ctx context.Context, request *api.CollectionLikeV1_Types_Request) (*api.CollectionLikeV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	expires, _, err := s.players.LikeCollection(ctx, current.ID, request.GetTargetPlayerId())
	if err != nil {
		return nil, collectionError(err)
	}
	return &api.CollectionLikeV1_Types_Response{LikeHistoryExpiredAt: timestamppb.New(expires)}, nil
}

func (s collectionServer) OtherListV1(ctx context.Context, request *api.CollectionOtherListV1_Types_Request) (*api.CollectionOtherListV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	targetID := request.GetPlayerId()
	if targetID == "" && request.GetFriendId() != "" {
		players, searchErr := s.players.SearchPlayers(ctx, current.ID, request.GetFriendId(), "")
		if searchErr == nil && len(players) == 1 {
			targetID = players[0].ID
		}
	}
	profile, err := s.players.Profile(ctx, targetID)
	if err != nil {
		return nil, status.Error(codes.NotFound, "local profile not found")
	}
	albums, mounts, err := s.collectionShowcases(ctx, current.ID, targetID)
	if err != nil {
		return nil, err
	}
	count, err := s.players.CollectionLikeCount(ctx, targetID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load collection likes")
	}
	expires, err := s.players.CollectionLikeExpiry(ctx, current.ID, targetID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load collection like state")
	}
	spine := profileSpine(profile)
	if payload, loadErr := s.players.BestCollection(ctx, targetID); loadErr == nil {
		spine.Collection = decodeBestCollection(payload)
	}
	response := &api.CollectionOtherListV1_Types_Response{LikeCount: count, Albums: albums, Mounts: mounts, Profile: spine}
	if !expires.IsZero() {
		response.LikeHistoryExpiredAt = timestamppb.New(expires)
	}
	return response, nil
}

func (s collectionServer) GetSomeonesV1(ctx context.Context, _ *api.CollectionGetSomeonesV1_Types_Request) (*api.CollectionGetSomeonesV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	candidates, err := s.players.CollectionCandidates(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot discover collections")
	}
	social, err := s.players.SocialSnapshot(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load friends")
	}
	friends := make(map[string]bool, len(social.Friends))
	for _, value := range social.Friends {
		friends[value.PlayerID] = true
	}
	response := &api.CollectionGetSomeonesV1_Types_Response{}
	for _, candidate := range candidates {
		info, buildErr := s.someoneCollectionInfo(ctx, current.ID, candidate)
		if buildErr != nil {
			continue
		}
		if friends[candidate.Player.ID] {
			if len(response.FriendCollectionPlayerInfos) < collectionDiscoveryLimit {
				response.FriendCollectionPlayerInfos = append(response.FriendCollectionPlayerInfos, info)
			}
			continue
		}
		if len(response.PopularCollectionPlayerInfos) < collectionDiscoveryLimit {
			response.PopularCollectionPlayerInfos = append(response.PopularCollectionPlayerInfos, info)
		}
	}
	// Deterministic local discovery avoids unstable UI ordering while still
	// providing the second server bucket expected by the client.
	for i := len(candidates) - 1; i >= 0 && len(response.RandomCollectionPlayerInfos) < collectionDiscoveryLimit; i-- {
		candidate := candidates[i]
		if friends[candidate.Player.ID] {
			continue
		}
		if info, buildErr := s.someoneCollectionInfo(ctx, current.ID, candidate); buildErr == nil {
			response.RandomCollectionPlayerInfos = append(response.RandomCollectionPlayerInfos, info)
		}
	}
	return response, nil
}

func (s collectionServer) collectionShowcases(ctx context.Context, viewerID, ownerID string) ([]*api.AlbumSpine, []*mountpb.Mount, error) {
	isFriend := viewerID == ownerID
	if !isFriend {
		social, err := s.players.SocialSnapshot(ctx, viewerID)
		if err == nil {
			for _, friend := range social.Friends {
				if friend.PlayerID == ownerID {
					isFriend = true
					break
				}
			}
		}
	}
	visible := func(setting int32) bool {
		return viewerID == ownerID || setting == int32(albumpb.PublicSetting_PUBLIC_SETTING_ALL_USERS) || (isFriend && setting == int32(albumpb.PublicSetting_PUBLIC_SETTING_FRIEND_ONLY))
	}
	albumRows, err := s.players.Showcases(ctx, ownerID, "album")
	if err != nil {
		return nil, nil, status.Error(codes.Internal, "cannot load albums")
	}
	var albums []*api.AlbumSpine
	for _, row := range albumRows {
		if !visible(row.PublicSetting) {
			continue
		}
		album, decodeErr := decodeAlbum(row)
		if decodeErr != nil {
			return nil, nil, status.Error(codes.Internal, "cannot decode album")
		}
		albums = append(albums, albumSpine(album))
	}
	mountRows, err := s.players.Showcases(ctx, ownerID, "mount")
	if err != nil {
		return nil, nil, status.Error(codes.Internal, "cannot load mounts")
	}
	var mounts []*mountpb.Mount
	for _, row := range mountRows {
		if !visible(row.PublicSetting) {
			continue
		}
		mount, decodeErr := decodeMount(row)
		if decodeErr != nil {
			return nil, nil, status.Error(codes.Internal, "cannot decode mount")
		}
		mounts = append(mounts, mount)
	}
	return albums, mounts, nil
}

func (s collectionServer) deriveBestCollection(ctx context.Context, playerID string, collectionType collectionpb.CollectionType, collectionID uint64) (*collectionpb.MyBestCollection, error) {
	result := &collectionpb.MyBestCollection{MyBestType: collectionType, CollectionId: collectionID}
	switch collectionType {
	case collectionpb.CollectionType_COLLECTION_TYPE_COLLECTION_FILE:
		row, err := s.players.Showcase(ctx, playerID, "album", collectionID)
		if err != nil {
			return nil, collectionError(err)
		}
		if row.PublicSetting != int32(albumpb.PublicSetting_PUBLIC_SETTING_ALL_USERS) {
			return nil, status.Error(codes.FailedPrecondition, "best collection must be public")
		}
		album, err := decodeAlbum(row)
		if err != nil {
			return nil, status.Error(codes.Internal, "cannot decode album")
		}
		result.ThumbnailId, result.HashTagIds = album.GetCoverId(), album.GetHashTagIds()
		for _, slot := range album.GetSlots() {
			if slot != nil {
				result.Slots = append(result.Slots, collectionSlot(slot.GetSlotNumber(), slot.GetCardInstance()))
			}
		}
	case collectionpb.CollectionType_COLLECTION_TYPE_COLLECTION_BOARD:
		row, err := s.players.Showcase(ctx, playerID, "mount", collectionID)
		if err != nil {
			return nil, collectionError(err)
		}
		if row.PublicSetting != int32(albumpb.PublicSetting_PUBLIC_SETTING_ALL_USERS) {
			return nil, status.Error(codes.FailedPrecondition, "best collection must be public")
		}
		mount, err := decodeMount(row)
		if err != nil {
			return nil, status.Error(codes.Internal, "cannot decode mount")
		}
		result.ThumbnailId, result.HashTagIds = mount.GetMountTemplateId(), mount.GetHashTagIds()
		for _, slot := range mount.GetSlots() {
			if slot != nil {
				result.Slots = append(result.Slots, collectionSlot(slot.GetSlotNumber(), slot.GetCardInstance()))
			}
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "invalid collection type")
	}
	return result, nil
}

func (s collectionServer) someoneCollectionInfo(ctx context.Context, viewerID string, candidate store.CollectionCandidate) (*collectionpb.SomeoneCollectionPlayerInfo, error) {
	best := decodeBestCollection(candidate.Payload)
	if best == nil {
		return nil, errors.New("invalid best collection")
	}
	profile, err := s.players.Profile(ctx, candidate.Player.ID)
	if err != nil {
		return nil, err
	}
	expires, err := s.players.CollectionLikeExpiry(ctx, viewerID, candidate.Player.ID)
	if err != nil {
		return nil, err
	}
	result := &collectionpb.SomeoneCollectionPlayerInfo{PlayerId: candidate.Player.ID, Nickname: candidate.Player.DisplayName, IconId: profile.Settings.IconID, LikeCount: candidate.LikeCount, ThumbnailId: best.GetThumbnailId(), Type: best.GetMyBestType(), CollectionId: best.GetCollectionId(), Slots: best.GetSlots(), HashTagIds: best.GetHashTagIds()}
	if !expires.IsZero() {
		result.LikeHistoryExpiredAt = timestamppb.New(expires)
	}
	return result, nil
}

func albumSpine(album *albumpb.Album) *api.AlbumSpine {
	return &api.AlbumSpine{AlbumId: album.GetAlbumId(), CoverId: album.GetCoverId(), BackgroundColor: album.GetBackgroundColor(), CardNum: uint64(len(album.GetSlots())), PublicSetting: album.GetPublicSetting(), DisplayOrder: album.GetDisplayOrder(), Slots: album.GetSlots(), FileName: album.GetFileName(), HashTagIds: album.GetHashTagIds()}
}

func collectionSlot(number uint64, card *cardpb.CardInstance) *collectionpb.CollectionSlot {
	return &collectionpb.CollectionSlot{SlotNumber: number, CardInstance: card}
}

func decodeBestCollection(payload []byte) *collectionpb.MyBestCollection {
	if len(payload) == 0 {
		return nil
	}
	result := &collectionpb.MyBestCollection{}
	if err := proto.Unmarshal(payload, result); err != nil {
		return nil
	}
	return result
}

func collectionError(err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return status.Error(codes.NotFound, err.Error())
	}
	if errors.Is(err, store.ErrRuleViolation) {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	return status.Error(codes.Internal, "collection operation failed")
}
