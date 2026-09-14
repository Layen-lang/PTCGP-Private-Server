// Package playerapi implements the local subset of the Player API.
package playerapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/packlab"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	item "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	itemacquisition "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item_acquisition"
	resources "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/player_resources"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/protocol"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/traffic"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const sessionLifetime = 24 * time.Hour

// Server owns the supported Player API handlers and authentication policy.
type Server struct {
	api.UnimplementedSystemServer
	players       *player.Manager
	logger        *slog.Logger
	clientProfile protocol.Profile
	policy        protocol.MetadataPolicy
	compatibility *compatibilityRegistry
}

type playerContextKey struct{}

// NewGRPCServer constructs a gRPC server forced to use the Takasho codec.
func NewGRPCServer(players *player.Manager, packs *packlab.Engine, master *catalog.Catalog, logger *slog.Logger, strictMetadata bool, profile protocol.Profile, recorders ...*traffic.Recorder) (*grpc.Server, error) {
	compatibility, err := newCompatibilityRegistry()
	if err != nil {
		return nil, fmt.Errorf("initialize Player API compatibility: %w", err)
	}
	service := &Server{
		players:       players,
		logger:        logger,
		clientProfile: profile,
		policy:        protocol.MetadataPolicy{Strict: strictMetadata, Profile: profile},
		compatibility: compatibility,
	}
	var unaryInterceptors []grpc.UnaryServerInterceptor
	var recorder *traffic.Recorder
	if len(recorders) > 0 {
		recorder = recorders[0]
	}
	if recorder != nil {
		unaryInterceptors = append(unaryInterceptors, recorder.UnaryServerInterceptor(service.trafficPlayerID))
	}
	unaryInterceptors = append(unaryInterceptors, service.unaryAuth, service.unaryCompatibility)
	serverOptions := []grpc.ServerOption{
		grpc.ForceServerCodec(transport.NewCodec()),
		grpc.MaxRecvMsgSize(100 * 1024 * 1024),
		grpc.MaxSendMsgSize(100 * 1024 * 1024),
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
		grpc.UnknownServiceHandler(service.unknownCompatibility),
	}
	if recorder != nil {
		serverOptions = append(serverOptions, grpc.ChainStreamInterceptor(recorder.StreamServerInterceptor(service.trafficPlayerID)))
	}
	server := grpc.NewServer(serverOptions...)
	api.RegisterSystemServer(server, service)
	registerStartupReadServers(server, players)
	registerStatefulReadServers(server, players, packs, master)
	registerSupplementalServers(server, players)
	registerSimpleServers(server, players)
	return server, nil
}

func (s *Server) trafficPlayerID(ctx context.Context) string {
	resolved, ok := ctx.Value(playerContextKey{}).(store.Player)
	if ok {
		return resolved.ID
	}
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("x-takasho-session-token")
	if len(values) == 0 {
		return ""
	}
	token := strings.TrimSpace(strings.TrimPrefix(values[0], "Bearer "))
	resolved, err := s.players.PlayerBySession(ctx, token)
	if err != nil {
		return ""
	}
	return resolved.ID
}

// AuthorizeV1 restores the selected local player for a device.
func (s *Server) AuthorizeV1(ctx context.Context, request *api.SystemAuthorizeV1_Types_Request) (*api.SystemAuthorizeV1_Types_Response, error) {
	if request.GetDeviceAccount() == "" {
		return nil, status.Error(codes.InvalidArgument, "device_account is required")
	}
	authorization, err := s.players.Authorize(ctx, request.GetDeviceAccount())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.FailedPrecondition, "select a local profile in the administration first")
		}
		s.logger.ErrorContext(ctx, "authorize player", "error", err)
		return nil, status.Error(codes.Internal, "local identity unavailable")
	}
	s.logger.InfoContext(ctx, "authorized local player", "player_id", authorization.Player.ID, "created", authorization.Created)
	return &api.SystemAuthorizeV1_Types_Response{
		SessionToken: authorization.Token,
		PlayerId:     authorization.Player.ID,
		AssetBaseUrl: s.clientProfile.AssetBaseURL,
		// The official client expects this nested message to be present even when
		// the asset CDN does not require a signed cookie. An absent message causes
		// the IL2CPP bootstrap path to terminate before issuing LoginV1.
		SignedCookie: &api.SystemAuthorizeV1_Types_Response_Types_SignedCookie{
			ExpireAt: timestamppb.New(time.Now().Add(sessionLifetime)),
		},
	}, nil
}

// LoginV1 returns consent-complete local settings. Gameplay bootstrap reads
// are handled by their explicit read-only allowlist.
func (s *Server) LoginV1(ctx context.Context, _ *api.SystemLoginV1_Types_Request) (*api.SystemLoginV1_Types_Response, error) {
	profile, err := s.profile(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.players.AddAction(ctx, profile.Player.ID, player.ActionLogin, "", 1, true); err != nil {
		return nil, status.Error(codes.Internal, "cannot record login action")
	}
	tutorial := make([]*api.SystemLoginV1_Types_Response_Types_TutorialStatus, 0, len(profile.Tutorial))
	for _, step := range profile.Tutorial {
		tutorial = append(tutorial, &api.SystemLoginV1_Types_Response_Types_TutorialStatus{TutorialId: step.TutorialID, TutorialStep: step.Step})
	}
	return &api.SystemLoginV1_Types_Response{
		PlayerSettingsInfo:    persistedSettingsInfo(profile, nil, s.players.HomeSettings()),
		ItemAcquisitionResult: emptyItemAcquisitionResult(profile),
		TutorialCompletes:     tutorial,
	}, nil
}

func emptyItemAcquisitionResult(profiles ...player.Profile) *itemacquisition.ItemAcquisitionResult {
	result := &itemacquisition.ItemAcquisitionResult{
		AcquiredItems:     &item.InventoryItems{},
		AcceptedItems:     &item.InventoryItems{},
		ItemState:         &resources.PlayerResources{},
		AcquiredItemStats: &itemacquisition.AcquiredItemStats{},
		OverflownItems:    &item.InventoryItems{},
	}
	if len(profiles) > 0 {
		result.ItemState = toProtoResources(profiles[0])
	}
	return result
}

// PartialMaintenanceV1 reports no partial maintenance restrictions.
func (s *Server) PartialMaintenanceV1(context.Context, *api.SystemPartialMaintenanceV1_Types_Request) (*api.SystemPartialMaintenanceV1_Types_Response, error) {
	return &api.SystemPartialMaintenanceV1_Types_Response{}, nil
}

func (s *Server) unaryAuth(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (response any, err error) {
	startedAt := time.Now()
	defer func() {
		s.logger.InfoContext(ctx, "gRPC request",
			"method", info.FullMethod,
			"code", status.Code(err).String(),
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
	}()

	// These revisions are response metadata rather than protobuf fields. The
	// official client reads them after AuthorizeV1 and parses the master hash as
	// part of MasterDownloadService; a missing value becomes a fatal null parse.
	if err := grpc.SetHeader(ctx, s.responseMetadata()); err != nil {
		return nil, status.Error(codes.Internal, "cannot attach local revision metadata")
	}

	if err := s.policy.Validate(ctx, info.FullMethod); err != nil {
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	if info.FullMethod != api.System_AuthorizeV1_FullMethodName {
		player, err := s.authenticate(ctx)
		if err != nil {
			return nil, err
		}
		ctx = context.WithValue(ctx, playerContextKey{}, player)
	}
	return handler(ctx, request)
}

func (s *Server) unaryCompatibility(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
	response, err := handler(ctx, request)
	if status.Code(err) != codes.Unimplemented {
		return response, err
	}
	response, ok := s.compatibility.response(info.FullMethod)
	if !ok {
		return nil, err
	}
	s.logCompatibility(ctx, info.FullMethod)
	return response, nil
}

func (s *Server) unknownCompatibility(_ any, stream grpc.ServerStream) (err error) {
	startedAt := time.Now()
	method, ok := grpc.MethodFromServerStream(stream)
	if !ok {
		return status.Error(codes.Unimplemented, "unknown gRPC method")
	}
	defer func() {
		s.logger.InfoContext(stream.Context(), "gRPC request",
			"method", method,
			"code", status.Code(err).String(),
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
	}()
	if _, ok := s.compatibility.response(method); !ok {
		return status.Error(codes.Unimplemented, "method is outside the local Player API surface")
	}
	if err := stream.SetHeader(s.responseMetadata()); err != nil {
		return status.Error(codes.Internal, "cannot attach local revision metadata")
	}
	if err := s.policy.Validate(stream.Context(), method); err != nil {
		return status.Error(codes.FailedPrecondition, err.Error())
	}
	if _, err := s.authenticate(stream.Context()); err != nil {
		return err
	}
	s.logCompatibility(stream.Context(), method)
	return s.compatibility.serveUnknown(stream)
}

func (s *Server) logCompatibility(ctx context.Context, fullMethod string) {
	s.logger.WarnContext(ctx, "served descriptor compatibility response",
		"method", fullMethod,
		"mutation", compatibilityMutation(fullMethod),
		"state_effect", "none",
	)
}

func (s *Server) responseMetadata() metadata.MD {
	return metadata.Pairs(
		protocol.RequestedTimestampHeader, strconv.FormatInt(time.Now().Unix(), 10),
		protocol.ProtocolVersionHeader, s.clientProfile.ResponseProtocolVersion,
		protocol.MasterMemoryAladdinHeader, s.clientProfile.MasterMemoryAladdinHash,
		protocol.AndroidAssetAladdinHeader, s.clientProfile.AndroidAssetAladdinHash,
	)
}

func (s *Server) authenticate(ctx context.Context) (store.Player, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("x-takasho-session-token")
	if len(values) == 0 {
		return store.Player{}, status.Error(codes.Unauthenticated, "missing local session")
	}
	token := strings.TrimSpace(strings.TrimPrefix(values[0], "Bearer "))
	resolved, err := s.players.PlayerBySession(ctx, token)
	if errors.Is(err, store.ErrInvalidSession) {
		return store.Player{}, status.Error(codes.Unauthenticated, "invalid local session")
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "resolve local session", "error", err)
		return store.Player{}, status.Error(codes.Internal, "local session unavailable")
	}
	return resolved, nil
}

func (s *Server) profile(ctx context.Context) (player.Profile, error) {
	resolved, ok := ctx.Value(playerContextKey{}).(store.Player)
	if !ok {
		return player.Profile{}, status.Error(codes.Unauthenticated, "missing local player")
	}
	profile, err := s.players.Profile(ctx, resolved.ID)
	if err != nil {
		s.logger.ErrorContext(ctx, "load local profile", "error", err)
		return player.Profile{}, status.Error(codes.Internal, "local profile unavailable")
	}
	profile.Player.DeviceAccount = resolved.DeviceAccount
	return profile, nil
}
