package playerapi

import (
	"context"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type playerServer struct {
	api.UnimplementedPlayerServer
	players *player.Manager
}

func (s playerServer) DeleteAccountV1(ctx context.Context, _ *api.PlayerDeleteAccountV1_Types_Request) (*api.PlayerDeleteAccountV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.players.Delete(ctx, current.ID); err != nil {
		return nil, status.Error(codes.Internal, "cannot delete local account")
	}
	return &api.PlayerDeleteAccountV1_Types_Response{}, nil
}

func (s playerServer) UnlinkAccountV1(ctx context.Context, _ *api.PlayerUnlinkAccountV1_Types_Request) (*api.PlayerUnlinkAccountV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.players.UnlinkAccount(ctx, current.ID); err != nil {
		return nil, status.Error(codes.Internal, "cannot unlink local account")
	}
	return &api.PlayerUnlinkAccountV1_Types_Response{}, nil
}
