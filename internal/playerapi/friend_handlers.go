package playerapi

import (
	"context"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	friendpb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/friend"
	profilepb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/player_profile"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type friendServer struct {
	api.UnimplementedFriendServer
	players *player.Manager
}

func (s friendServer) ListV1(ctx context.Context, _ *api.FriendListV1_Types_Request) (*api.FriendListV1_Types_Response, error) {
	return s.snapshot(ctx)
}

func (s friendServer) SearchV1(ctx context.Context, request *api.FriendSearchV1_Types_Request) (*api.FriendSearchV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	players, err := s.players.SearchPlayers(ctx, current.ID, request.GetSearchFriendId(), request.GetSearchPlayerName())
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot search local profiles")
	}
	social, _ := s.players.SocialSnapshot(ctx, current.ID)
	response := &api.FriendSearchV1_Types_Response{}
	for _, candidate := range players {
		profile, err := s.players.Profile(ctx, candidate.ID)
		if err != nil {
			continue
		}
		response.Results = append(response.Results, &friendpb.SearchResult{PlayerId: candidate.ID, FriendStatus: socialStatus(social, candidate.ID), Profile: profileSpine(profile)})
	}
	return response, nil
}

func (s friendServer) SendRequestsV1(ctx context.Context, request *api.FriendSendRequestsV1_Types_Request) (*api.FriendSendRequestsV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	approved, err := s.players.SendFriendRequests(ctx, current.ID, request.GetReceiverPlayerIds())
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot send local friend request")
	}
	state, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &api.FriendSendRequestsV1_Types_Response{ApprovedPlayerIds: approved, Friends: state.Friends, SentFriendRequests: state.SentFriendRequests, ReceivedFriendRequests: state.ReceivedFriendRequests, Profiles: state.Profiles}, nil
}

func (s friendServer) ApproveRequestV1(ctx context.Context, request *api.FriendApproveRequestV1_Types_Request) (*api.FriendApproveRequestV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.players.ApproveFriendRequest(ctx, current.ID, request.GetSenderPlayerId()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	if err := s.players.AddAction(ctx, current.ID, player.ActionFriendAdded, request.GetSenderPlayerId(), 1, true); err != nil {
		return nil, status.Error(codes.Internal, "cannot record friend action")
	}
	state, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &api.FriendApproveRequestV1_Types_Response{Friends: state.Friends, ReceivedFriendRequests: state.ReceivedFriendRequests, Profiles: state.Profiles}, nil
}

func (s friendServer) CancelSentRequestsV1(ctx context.Context, request *api.FriendCancelSentRequestsV1_Types_Request) (*api.FriendCancelSentRequestsV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.players.RemoveFriendRequests(ctx, current.ID, request.GetReceiverPlayerIds(), true); err != nil {
		return nil, status.Error(codes.Internal, "cannot cancel local friend requests")
	}
	state, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &api.FriendCancelSentRequestsV1_Types_Response{SentFriendRequests: state.SentFriendRequests, Profiles: state.Profiles}, nil
}

func (s friendServer) RejectRequestsV1(ctx context.Context, request *api.FriendRejectRequestsV1_Types_Request) (*api.FriendRejectRequestsV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.players.RemoveFriendRequests(ctx, current.ID, request.GetSenderPlayerIds(), false); err != nil {
		return nil, status.Error(codes.Internal, "cannot reject local friend requests")
	}
	state, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &api.FriendRejectRequestsV1_Types_Response{ReceivedFriendRequests: state.ReceivedFriendRequests, Profiles: state.Profiles}, nil
}

func (s friendServer) DeleteV1(ctx context.Context, request *api.FriendDeleteV1_Types_Request) (*api.FriendDeleteV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.players.DeleteFriends(ctx, current.ID, request.GetDeletePlayerIds()); err != nil {
		return nil, status.Error(codes.Internal, "cannot delete local friends")
	}
	state, err := s.snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return &api.FriendDeleteV1_Types_Response{Friends: state.Friends}, nil
}

func (s friendServer) GetFavoritesV1(ctx context.Context, _ *api.FriendGetFavoritesV1_Types_Request) (*api.FriendGetFavoritesV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	social, err := s.players.SocialSnapshot(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load favorite friends")
	}
	return &api.FriendGetFavoritesV1_Types_Response{FavoritePlayerIds: social.FavoritePlayerIDs}, nil
}

func (s friendServer) SetFavoriteV1(ctx context.Context, request *api.FriendSetFavoriteV1_Types_Request) (*api.FriendSetFavoriteV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	add := request.GetFavoriteUpdateType() == api.FriendSetFavoriteV1_Types_Request_Types_FAVORITE_UPDATE_TYPE_ADD
	if err := s.players.SetFriendFavorite(ctx, current.ID, request.GetFriendPlayerId(), add); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	social, err := s.players.SocialSnapshot(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load favorite friends")
	}
	return &api.FriendSetFavoriteV1_Types_Response{FavoritePlayerIds: social.FavoritePlayerIDs}, nil
}

func (s friendServer) snapshot(ctx context.Context) (*api.FriendListV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	social, err := s.players.SocialSnapshot(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load local friends")
	}
	response := &api.FriendListV1_Types_Response{}
	profileIDs := make(map[string]struct{})
	appendProfile := func(playerID string) *profilepb.ProfileSpine {
		profile, err := s.players.Profile(ctx, playerID)
		if err != nil {
			return nil
		}
		spine := profileSpine(profile)
		if _, exists := profileIDs[playerID]; !exists {
			profileIDs[playerID] = struct{}{}
			response.Profiles = append(response.Profiles, spine)
		}
		return spine
	}
	for _, link := range social.Friends {
		spine := appendProfile(link.PlayerID)
		if spine == nil {
			continue
		}
		response.Friends = append(response.Friends, &friendpb.Friend{PlayerId: link.PlayerID, BecameFriendsAt: timestamppb.New(link.Since), Profile: spine})
	}
	for _, request := range social.Sent {
		response.SentFriendRequests = append(response.SentFriendRequests, friendRequest(request))
		appendProfile(request.ToPlayerID)
	}
	for _, request := range social.Received {
		response.ReceivedFriendRequests = append(response.ReceivedFriendRequests, friendRequest(request))
		appendProfile(request.FromPlayerID)
	}
	return response, nil
}

func friendRequest(request store.FriendRequest) *friendpb.FriendRequest {
	return &friendpb.FriendRequest{FromPlayerId: request.FromPlayerID, ToPlayerId: request.ToPlayerID, CreatedAt: timestamppb.New(request.CreatedAt)}
}

func socialStatus(social store.SocialSnapshot, playerID string) friendpb.FriendStatus {
	for _, friend := range social.Friends {
		if friend.PlayerID == playerID {
			return friendpb.FriendStatus_FRIEND_STATUS_FRIEND
		}
	}
	for _, request := range social.Sent {
		if request.ToPlayerID == playerID {
			return friendpb.FriendStatus_FRIEND_STATUS_SENT_REQUEST
		}
	}
	for _, request := range social.Received {
		if request.FromPlayerID == playerID {
			return friendpb.FriendStatus_FRIEND_STATUS_RECEIVED_REQUEST
		}
	}
	return friendpb.FriendStatus_FRIEND_STATUS_UNSPECIFIED
}

func profileSpine(profile player.Profile) *profilepb.ProfileSpine {
	return &profilepb.ProfileSpine{PlayerId: profile.Player.ID, Nickname: profile.Player.DisplayName, IconId: profile.Settings.IconID, PlayerLevel: uint64(profile.Player.Level), LastLoggedInAt: timestamppb.Now(), FriendId: store.FriendID(profile.Player.ID), AvailableType: profilepb.AvailableType_AVAILABLE_TYPE_AVAILABLE}
}

func currentPlayer(ctx context.Context) (store.Player, error) {
	resolved, ok := ctx.Value(playerContextKey{}).(store.Player)
	if !ok {
		return store.Player{}, status.Error(codes.Unauthenticated, "missing local player")
	}
	return resolved, nil
}
