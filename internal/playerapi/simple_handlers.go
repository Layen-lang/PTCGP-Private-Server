package playerapi

import (
	"context"
	"fmt"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/gamelocale"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	languagepb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/language"
	settingspb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/player_settings"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s startupActionServer) SyncAccountLinkStateV1(ctx context.Context, request *api.ActionSyncAccountLinkStateV1_Types_Request) (*api.ActionSyncAccountLinkStateV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	types := make([]int32, 0, len(request.GetAccountLinkTypes()))
	for _, value := range request.GetAccountLinkTypes() {
		types = append(types, int32(value))
	}
	if err := s.players.SyncAccountLinks(ctx, current.ID, types); err != nil {
		return nil, status.Error(codes.Internal, "cannot synchronize account links")
	}
	return &api.ActionSyncAccountLinkStateV1_Types_Response{}, nil
}

type echoServer struct{ api.UnimplementedEchoServer }

func (echoServer) EchoV1(context.Context, *api.EchoEchoV1_Types_Request) (*api.EchoEchoV1_Types_Response, error) {
	return &api.EchoEchoV1_Types_Response{Time: timestamppb.Now()}, nil
}

type performanceLogServer struct {
	api.UnimplementedPerformanceLogServer
}

func (performanceLogServer) SendV1(context.Context, *api.PerformanceLogSendV1_Types_Request) (*api.PerformanceLogSendV1_Types_Response, error) {
	return &api.PerformanceLogSendV1_Types_Response{}, nil
}

type notificationServer struct {
	api.UnimplementedNotificationServer
	players *player.Manager
}

func (s notificationServer) GetSettingsV1(ctx context.Context, _ *api.NotificationGetSettingsV1_Types_Request) (*api.NotificationGetSettingsV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	flags, err := s.players.Flags(ctx, current.ID, "notification", []string{"important_notice"})
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load notification settings")
	}
	important := true
	if flag, ok := flags["important_notice"]; ok {
		important = flag.Value != 0
	}
	return &api.NotificationGetSettingsV1_Types_Response{Setting: &api.NotificationSetting{ImportantNotice: important}}, nil
}

func (s notificationServer) SaveSettingsV1(ctx context.Context, request *api.NotificationSaveSettingsV1_Types_Request) (*api.NotificationSaveSettingsV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	if request.GetSetting() == nil {
		return nil, status.Error(codes.InvalidArgument, "notification settings are required")
	}
	value := int64(0)
	if request.GetSetting().GetImportantNotice() {
		value = 1
	}
	if _, err := s.players.SetFlags(ctx, current.ID, "notification", map[string]int64{"important_notice": value}); err != nil {
		return nil, status.Error(codes.Internal, "cannot save notification settings")
	}
	return &api.NotificationSaveSettingsV1_Types_Response{Setting: &api.NotificationSetting{ImportantNotice: value != 0}}, nil
}

func (s startupMissionServer) IsNotifiedV1(ctx context.Context, request *api.MissionIsNotifiedV1_Types_Request) (*api.MissionIsNotifiedV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	flags, err := s.players.Flags(ctx, current.ID, "mission_notified", nil)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load mission notifications")
	}
	response := &api.MissionIsNotifiedV1_Types_Response{}
	for _, missionID := range request.GetMissionIds() {
		if flags["50:"+missionID].Value != 0 {
			response.FiftyPercents = append(response.FiftyPercents, missionID)
		}
		if flags["90:"+missionID].Value != 0 {
			response.NinetyPercents = append(response.NinetyPercents, missionID)
		}
	}
	return response, nil
}

func (s startupMissionServer) SetNotifiedV1(ctx context.Context, request *api.MissionSetNotifiedV1_Types_Request) (*api.MissionSetNotifiedV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	values := make(map[string]int64)
	for _, missionID := range request.GetFiftyPercentNotifiedMissionIds() {
		if missionID != "" {
			values["50:"+missionID] = 1
		}
	}
	for _, missionID := range request.GetNinetyPercentNotifiedMissionIds() {
		if missionID != "" {
			values["90:"+missionID] = 1
		}
	}
	if _, err := s.players.SetFlags(ctx, current.ID, "mission_notified", values); err != nil {
		return nil, status.Error(codes.Internal, "cannot save mission notifications")
	}
	return &api.MissionSetNotifiedV1_Types_Response{FiftyPercentNotifiedMissionIds: append([]string(nil), request.GetFiftyPercentNotifiedMissionIds()...), NinetyPercentNotifiedMissionIds: append([]string(nil), request.GetNinetyPercentNotifiedMissionIds()...)}, nil
}

func (s startupPlayerResourcesServer) MarkAsViewedNewEmblemArrivalV1(ctx context.Context, _ *api.PlayerResourcesMarkAsViewedNewEmblemArrivalV1_Types_Request) (*api.PlayerResourcesMarkAsViewedNewEmblemArrivalV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	viewedAt, err := s.players.SetFlags(ctx, current.ID, "arrival", map[string]int64{"emblem": time.Now().UTC().Unix()})
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot mark emblem arrivals")
	}
	return &api.PlayerResourcesMarkAsViewedNewEmblemArrivalV1_Types_Response{LastViewed: timestamppb.New(viewedAt)}, nil
}

func (s startupShopServer) MarkAsViewedV1(ctx context.Context, request *api.ShopMarkAsViewedV1_Types_Request) (*api.ShopMarkAsViewedV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	values := make(map[string]int64)
	for _, shop := range request.GetShops() {
		if shop == nil {
			continue
		}
		for _, id := range shop.GetIds() {
			if id != "" {
				values[fmt.Sprintf("%d:%s", shop.GetType(), id)] = 1
			}
		}
	}
	viewedAt, err := s.players.SetFlags(ctx, current.ID, "shop_viewed", values)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot mark shops as viewed")
	}
	return &api.ShopMarkAsViewedV1_Types_Response{Shops: request.GetShops(), LastViewedAt: timestamppb.New(viewedAt)}, nil
}

func (s startupPlayerSettingsServer) SaveInfoV1(ctx context.Context, request *api.PlayerSettingSaveInfoV1_Types_Request) (*api.PlayerSettingSaveInfoV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	locale, ok := localeForLanguage(request.GetLanguageType())
	if !ok || request.GetCountryRegionCode() == "" {
		return nil, status.Error(codes.InvalidArgument, "valid language and country are required")
	}
	if err := s.players.SavePlayerSettings(ctx, current.ID, locale, request.GetCountryRegionCode(), request.GetUseOfLastLoginTime(), request.GetUseOfLoginData(), request.GetUseOfPerformanceErrors()); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	p, err := profileFor(ctx, s.players)
	if err != nil {
		return nil, err
	}
	flags, _ := s.players.Flags(ctx, current.ID, "settings", nil)
	return &api.PlayerSettingSaveInfoV1_Types_Response{Info: persistedSettingsInfo(p, flags, s.players.HomeSettings())}, nil
}

func persistedSettingsInfo(profile player.Profile, flags map[string]store.Flag, home catalog.HomeSettings) *settingspb.Info {
	info := localSettings(store.SupportID(profile.Player.ID), home)
	if value, ok := languageForLocale(profile.Settings.Language); ok {
		info.LanguageType = value
	}
	info.CountryRegionCode = profile.Settings.Country
	info.YearNumOfBirth = int64(profile.Settings.YearOfBirth)
	info.MonthNumOfBirth = int64(profile.Settings.MonthOfBirth)
	if flag, ok := flags["use_last_login"]; ok {
		info.UseOfLastLoginTime = flag.Value != 0
	}
	if flag, ok := flags["use_login_data"]; ok {
		info.UseOfLoginData = flag.Value != 0
	}
	if flag, ok := flags["use_performance_errors"]; ok {
		info.UseOfPerformanceErrors = flag.Value != 0
	}
	return info
}

func localeForLanguage(value languagepb.Language) (string, bool) {
	return gamelocale.LocaleForLanguage(int32(value))
}

func languageForLocale(locale string) (languagepb.Language, bool) {
	value, ok := gamelocale.LanguageForLocale(locale)
	if !ok {
		return languagepb.Language_LANGUAGE_UNSPECIFIED, false
	}
	return languagepb.Language(value), true
}

func languageForProfile(profile player.Profile) languagepb.Language {
	value, ok := languageForLocale(profile.Settings.Language)
	if ok {
		return value
	}
	return languagepb.Language_LANGUAGE_FR
}

func registerSimpleServers(server grpc.ServiceRegistrar, players *player.Manager) {
	api.RegisterEchoServer(server, echoServer{})
	api.RegisterNotificationServer(server, notificationServer{players: players})
	api.RegisterPerformanceLogServer(server, performanceLogServer{})
}
