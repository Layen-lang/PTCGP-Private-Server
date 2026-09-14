package playerapi

import (
	"context"
	"fmt"
	"strings"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/packlab"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	cardresource "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card"
	cardframe "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card_frame"
	cardskin "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/card_skin"
	collection "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/collection"
	item "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/item"
	language "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/language"
	pack "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/pack"
	profilepb "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/player_profile"
	resources "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/player_resources"
	settings "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/player_settings"
	playerstorage "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/player_storage"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type startupPlayerSettingsServer struct {
	api.UnimplementedPlayerSettingsServer
	players *player.Manager
}

func (s startupPlayerSettingsServer) GetInfoV1(ctx context.Context, _ *api.PlayerSettingGetInfoV1_Types_Request) (*api.PlayerSettingGetInfoV1_Types_Response, error) {
	p, err := profileFor(ctx, s.players)
	if err != nil {
		return nil, err
	}
	flags, _ := s.players.Flags(ctx, p.Player.ID, "settings", nil)
	return &api.PlayerSettingGetInfoV1_Types_Response{Info: persistedSettingsInfo(p, flags, s.players.HomeSettings()), ErrorReportSamplingRate: 0.01, PerformanceLogSamplingRate: 0.0001}, nil
}

type startupPlayerResourcesServer struct {
	api.UnimplementedPlayerResourcesServer
	players *player.Manager
}

func (s startupPlayerResourcesServer) SyncV1(ctx context.Context, _ *api.PlayerResourcesSyncV1_Types_Request) (*api.PlayerResourcesSyncV1_Types_Response, error) {
	p, err := profileFor(ctx, s.players)
	if err != nil {
		return nil, err
	}
	return &api.PlayerResourcesSyncV1_Types_Response{PlayerResources: toProtoResources(p)}, nil
}
func (s startupPlayerResourcesServer) GetNewEmblemArrivalV1(ctx context.Context, _ *api.PlayerResourcesGetNewEmblemArrivalV1_Types_Request) (*api.PlayerResourcesGetNewEmblemArrivalV1_Types_Response, error) {
	p, err := profileFor(ctx, s.players)
	if err != nil {
		return nil, err
	}
	response := &api.PlayerResourcesGetNewEmblemArrivalV1_Types_Response{
		NewArrivals:     &item.InventoryItems{},
		PlayerResources: &resources.PlayerResources{},
	}
	flags, err := s.players.Flags(ctx, p.Player.ID, "arrival", []string{"emblem"})
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load viewed emblem arrivals")
	}
	lastViewed, ok := flags["emblem"]
	if !ok {
		return response, nil
	}
	for _, owned := range p.ProfileDecorations {
		if owned.Definition.Variant != 1 || owned.ObtainedAt.Unix() <= lastViewed.Value {
			continue
		}
		decoration := protoProfileDecoration(owned)
		response.NewArrivals.ProfileDecorations = append(response.NewArrivals.ProfileDecorations, decoration)
		response.PlayerResources.ProfileDecorations = append(response.PlayerResources.ProfileDecorations, decoration)
	}
	return response, nil
}

type startupPackServer struct {
	api.UnimplementedPackServer
	players *player.Manager
}

func (s startupPackServer) GetPackPowerV1(context.Context, *api.PackGetPackPowerV1_Types_Request) (*api.PackGetPackPowerV1_Types_Response, error) {
	response := &api.PackGetPackPowerV1_Types_Response{}
	for _, setting := range s.players.HomeSettings().PackPowers {
		response.PackPowers = append(response.PackPowers, &pack.PackPower{LastAutoHealedAt: timestamppb.Now(), AutoHealLimit: setting.AutoHealLimit, HealSecPerPower: setting.HealSecondsPerPower, ManualHealLimit: setting.PokeGoldUseLimit, PackPowerId: setting.ID})
	}
	return response, nil
}

type startupPlayerStorageServer struct {
	api.UnimplementedPlayerStorageServer
	players *player.Manager
}

func (s startupPlayerStorageServer) GetEntriesV1(ctx context.Context, request *api.PlayerStorageGetEntriesV1_Types_Request) (*api.PlayerStorageGetEntriesV1_Types_Response, error) {
	resolved, ok := ctx.Value(playerContextKey{}).(store.Player)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing local player")
	}
	entries, err := s.players.StorageEntries(ctx, resolved.ID, nil)
	if err != nil {
		return nil, status.Error(codes.Internal, "local storage unavailable")
	}
	response := &api.PlayerStorageGetEntriesV1_Types_Response{Revision: fmt.Sprintf("local-%d", resolved.StateVersion)}
	for _, entry := range entries {
		if matchesCriteria(entry.Key, request.GetCriteria()) {
			response.Entries = append(response.Entries, &playerstorage.Entry{PlayerId: resolved.ID, Key: entry.Key, Value: append([]byte(nil), entry.Value...), CreatedAt: timestamppb.New(entry.CreatedAt), UpdatedAt: timestamppb.New(entry.UpdatedAt)})
		}
	}
	return response, nil
}

func (s startupPlayerStorageServer) SetEntriesV1(ctx context.Context, request *api.PlayerStorageSetEntriesV1_Types_Request) (*api.PlayerStorageSetEntriesV1_Types_Response, error) {
	resolved, ok := ctx.Value(playerContextKey{}).(store.Player)
	if !ok {
		return nil, status.Error(codes.Unauthenticated, "missing local player")
	}
	stored := make([]store.StorageEntry, 0, len(request.GetEntries()))
	for _, entry := range request.GetEntries() {
		if entry != nil && entry.GetKey() != "" {
			stored = append(stored, store.StorageEntry{Key: entry.GetKey(), Value: append([]byte(nil), entry.GetValue()...)})
		}
	}
	if err := s.players.SetStorageEntries(ctx, resolved.ID, stored); err != nil {
		return nil, status.Error(codes.Internal, "cannot persist local storage")
	}
	entries, err := s.players.StorageEntries(ctx, resolved.ID, nil)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot reload local storage")
	}
	response := &api.PlayerStorageSetEntriesV1_Types_Response{Revision: request.GetNextRevision()}
	if response.Revision == "" {
		response.Revision = request.GetPreviousRevision()
	}
	if response.Revision == "" {
		response.Revision = fmt.Sprintf("local-%d", resolved.StateVersion)
	}
	for _, entry := range entries {
		response.Entries = append(response.Entries, &playerstorage.Entry{PlayerId: resolved.ID, Key: entry.Key, Value: entry.Value, CreatedAt: timestamppb.New(entry.CreatedAt), UpdatedAt: timestamppb.New(entry.UpdatedAt)})
	}
	return response, nil
}

func matchesCriteria(key string, criteria []*playerstorage.Criterion) bool {
	if len(criteria) == 0 {
		return true
	}
	for _, criterion := range criteria {
		if criterion == nil {
			continue
		}
		switch criterion.GetMatchingType() {
		case playerstorage.Criterion_Types_MATCHING_TYPE_EXACT:
			if key == criterion.GetKey() {
				return true
			}
		case playerstorage.Criterion_Types_MATCHING_TYPE_FORWARD:
			if strings.HasPrefix(key, criterion.GetKey()) {
				return true
			}
		}
	}
	return false
}

type startupMissionServer struct {
	api.UnimplementedMissionServer
	players *player.Manager
}

func (s startupMissionServer) GetGroupRewardStepStatesV1(ctx context.Context, request *api.MissionGetGroupRewardStepStatesV1_Types_Request) (*api.MissionGetGroupRewardStepStatesV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	states, err := s.players.MissionGroupStepStates(ctx, current.ID, request.GetMissionGroupRewardStepIds())
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load mission group reward states")
	}
	response := &api.MissionGetGroupRewardStepStatesV1_Types_Response{}
	for _, id := range request.GetMissionGroupRewardStepIds() {
		response.States = append(response.States, &api.MissionGetGroupRewardStepStatesV1_Types_Response_Types_MissionGroupRewardStepState{MissionGroupRewardStepId: id, IsCleared: states[id]})
	}
	return response, nil
}
func (s startupMissionServer) IsCompletedV1(ctx context.Context, request *api.MissionIsCompletedV1_Types_Request) (*api.MissionIsCompletedV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	completed, err := s.players.CompletedMissions(ctx, current.ID, request.GetMissionIds())
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot load completed missions")
	}
	response := &api.MissionIsCompletedV1_Types_Response{}
	for _, mission := range completed {
		response.CompletedMissions = append(response.CompletedMissions, &api.MissionIsCompletedV1_Types_Response_Types_CompletedMission{MissionId: mission.ID, LastCompletedAt: timestamppb.New(mission.CompletedAt)})
	}
	return response, nil
}
func (s startupMissionServer) CompleteV2(ctx context.Context, request *api.MissionCompleteV2_Types_Request) (*api.MissionCompleteV2_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	rewards, recipes, err := s.players.ClaimMissions(ctx, current.ID, request.GetMissionIds())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot reload mission rewards")
	}
	return &api.MissionCompleteV2_Types_Response{ThemeDeckRecipeIds: recipes, ItemAcquisitionResult: acquisitionFromChanges(profile, rewards, s.players.Catalog())}, nil
}
func (s startupMissionServer) CompleteGroupRewardStepV1(ctx context.Context, request *api.MissionCompleteGroupRewardStepV1_Types_Request) (*api.MissionCompleteGroupRewardStepV1_Types_Response, error) {
	current, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	rewards, err := s.players.ClaimMissionGroupSteps(ctx, current.ID, request.GetMissionGroupRewardStepIds())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	profile, err := s.players.Profile(ctx, current.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, "cannot reload mission group rewards")
	}
	return &api.MissionCompleteGroupRewardStepV1_Types_Response{ItemAcquisitionResult: acquisitionFromChanges(profile, rewards, s.players.Catalog())}, nil
}

type startupPlayerProfileServer struct {
	api.UnimplementedPlayerProfileServer
	players *player.Manager
}

func (s startupPlayerProfileServer) MyProfileV1(ctx context.Context, _ *api.MyProfileV1_Types_Request) (*api.MyProfileV1_Types_Response, error) {
	p, err := profileFor(ctx, s.players)
	if err != nil {
		return nil, err
	}
	return &api.MyProfileV1_Types_Response{Profile: protoProfile(p, true), RequiredExperience: uint64(p.RequiredExperience), NicknameChangedAt: profileNicknameChangedAt(ctx, s.players, p), AccountRegisteredAt: timestamppb.New(p.Player.CreatedAt), LevelUpHistories: protoLevelHistories(p)}, nil
}

func (s startupPlayerProfileServer) SaveMyProfileV1(ctx context.Context, request *api.SaveMyProfileV1_Types_Request) (*api.SaveMyProfileV1_Types_Response, error) {
	resolved, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.players.SaveProfile(ctx, resolved.ID, request.GetNickname(), request.GetIconId(), request.GetMessageId(), request.GetEmblemIds())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}
	return &api.SaveMyProfileV1_Types_Response{Profile: protoProfile(p, true), RequiredExperience: uint64(p.RequiredExperience), NicknameChangedAt: profileNicknameChangedAt(ctx, s.players, p), AccountRegisteredAt: timestamppb.New(p.Player.CreatedAt), LevelUpHistories: protoLevelHistories(p)}, nil
}

func protoLevelHistories(profile player.Profile) []*profilepb.LevelUpHistory {
	result := make([]*profilepb.LevelUpHistory, 0, len(profile.LevelHistories))
	for _, history := range profile.LevelHistories {
		result = append(result, &profilepb.LevelUpHistory{PlayerLevel: uint64(history.Level), LeveledUpAt: timestamppb.New(history.LeveledUpAt)})
	}
	return result
}

func profileNicknameChangedAt(ctx context.Context, players *player.Manager, profile player.Profile) *timestamppb.Timestamp {
	changedAt, ok, err := players.NicknameChangedAt(ctx, profile.Player.ID)
	if err == nil && ok {
		return timestamppb.New(changedAt)
	}
	return timestamppb.New(profile.Player.CreatedAt)
}

func (s startupPlayerProfileServer) OtherPlayerProfileV1(ctx context.Context, request *api.OtherPlayerProfileV1_Types_Request) (*api.OtherPlayerProfileV1_Types_Response, error) {
	resolved, err := currentPlayer(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.players.Profile(ctx, request.GetPlayerId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "local profile not found")
	}
	social, _ := s.players.SocialSnapshot(ctx, resolved.ID)
	return &api.OtherPlayerProfileV1_Types_Response{Profile: protoProfile(p, false), FriendStatus: socialStatus(social, p.Player.ID)}, nil
}

func protoProfile(p player.Profile, includePrivate bool) *profilepb.Profile {
	var numberOfCards uint64
	if includePrivate {
		for _, stock := range p.Cards {
			numberOfCards += uint64(stock.Quantity)
		}
	}
	return &profilepb.Profile{ProfileSpine: &profilepb.ProfileSpine{PlayerId: p.Player.ID, Nickname: p.Player.DisplayName, IconId: p.Settings.IconID, PlayerLevel: uint64(p.Player.Level), LastLoggedInAt: timestamppb.Now(), Collection: &collection.MyBestCollection{}, FriendId: store.FriendID(p.Player.ID), AvailableType: profilepb.AvailableType_AVAILABLE_TYPE_AVAILABLE, EmblemIds: append([]string(nil), p.EmblemIDs...)}, MessageId: p.Settings.MessageID, NumberOfCards: numberOfCards, BattleRecordSummary: &profilepb.BattleRecordSummary{}}
}

func registerStatefulReadServers(server grpc.ServiceRegistrar, players *player.Manager, packs *packlab.Engine, master *catalog.Catalog) {
	api.RegisterPlayerSettingsServer(server, startupPlayerSettingsServer{players: players})
	api.RegisterPlayerResourcesServer(server, startupPlayerResourcesServer{players: players})
	api.RegisterPackServer(server, packServer{master: master, packs: packs})
	api.RegisterPackShopServer(server, packShopServer{master: master, players: players, packs: packs})
	api.RegisterPlayerStorageServer(server, startupPlayerStorageServer{players: players})
	api.RegisterMissionServer(server, startupMissionServer{players: players})
	api.RegisterPlayerProfileServer(server, startupPlayerProfileServer{players: players})
	api.RegisterFriendServer(server, friendServer{players: players})
	api.RegisterDeckServer(server, deckServer{players: players})
}

func profileFor(ctx context.Context, players *player.Manager) (player.Profile, error) {
	resolved, err := playerFor(ctx)
	if err != nil {
		return player.Profile{}, err
	}
	p, err := players.Profile(ctx, resolved.ID)
	if err != nil {
		return player.Profile{}, status.Error(codes.Internal, "local profile unavailable")
	}
	p.Player.DeviceAccount = resolved.DeviceAccount
	return p, nil
}

func playerFor(ctx context.Context) (store.Player, error) {
	resolved, ok := ctx.Value(playerContextKey{}).(store.Player)
	if !ok {
		return store.Player{}, status.Error(codes.Unauthenticated, "missing local player")
	}
	return resolved, nil
}

func toProtoResources(p player.Profile) *resources.PlayerResources {
	result := &resources.PlayerResources{ExpStock: &item.ExpStock{CurrentLevel: int64(p.Player.Level), Exp: p.Player.Experience}}
	for _, owned := range p.Cards {
		result.CardStocks = append(result.CardStocks, protoCardStock(owned))
	}
	for _, owned := range p.Currencies {
		if owned.Definition.ProtocolType != 0 {
			result.Currencies = append(result.Currencies, &item.Currency{Type: item.Currency_Types_Type(owned.Definition.ProtocolType), Amount: uint64(owned.Quantity)})
		}
	}
	for _, owned := range p.Items {
		amount := uint64(owned.Quantity)
		switch owned.Definition.Kind {
		case catalog.ItemPeripheral:
			result.PeripheralGoods = append(result.PeripheralGoods, &item.PeripheralGoods{Type: item.PeripheralGoods_Types_Type(owned.Definition.Variant + 1), Id: owned.Definition.ID, Amount: amount, ObtainedAt: timestamppb.New(owned.ObtainedAt)})
		case catalog.ItemHiddenEvent:
			// Hidden-event items have no dedicated message in the checked-in
			// schema; expose their ID through the generic item collection.
			result.PeripheralGoods = append(result.PeripheralGoods, &item.PeripheralGoods{Type: item.PeripheralGoods_Types_TYPE_UNSPECIFIED, Id: owned.Definition.ID, Amount: amount, ObtainedAt: timestamppb.New(owned.ObtainedAt)})
		case catalog.ItemPackCharger:
			result.PackPowerChargers = append(result.PackPowerChargers, &item.PackPowerCharger{Type: item.PackPowerCharger_Types_TYPE_LARGE, Amount: amount})
		case catalog.ItemChallengeCharger:
			result.ChallengePowerChargers = append(result.ChallengePowerChargers, &item.ChallengePowerCharger{Type: item.ChallengePowerCharger_Types_TYPE_LARGE, Amount: amount})
		case catalog.ItemEventCharger:
			result.EventPowerChargers = append(result.EventPowerChargers, &item.EventPowerCharger{Type: item.EventPowerCharger_Types_Type(owned.Definition.Variant), Id: owned.Definition.ID, Amount: amount})
		case catalog.ItemRewardTicket:
			result.RewardTickets = append(result.RewardTickets, &item.RewardTicket{Type: item.RewardTicket_Types_Type(owned.Definition.Variant + 1), Id: owned.Definition.ID, Amount: amount})
		case catalog.ItemRevivalClock:
			result.RevivalClocks = append(result.RevivalClocks, &item.RevivalClock{Amount: amount})
		case catalog.ItemTrade:
			result.TradeItems = append(result.TradeItems, &item.TradeItem{Id: owned.Definition.ID, Amount: amount})
		case catalog.ItemTradeCharger:
			result.TradePowerChargers = append(result.TradePowerChargers, &item.TradePowerCharger{Id: owned.Definition.ID, Amount: int64(amount)})
		}
	}
	for _, owned := range p.ProfileDecorations {
		result.ProfileDecorations = append(result.ProfileDecorations, protoProfileDecoration(owned))
	}
	for _, owned := range p.CardSkins {
		result.CardSkinStocks = append(result.CardSkinStocks, &cardskin.CardSkinStock{CardId: owned.Card.ID, CardSkinId: owned.Definition.ID, Amount: uint64(owned.Quantity)})
	}
	for _, owned := range p.CardFrames {
		result.CardFrameStocks = append(result.CardFrameStocks, &cardframe.CardFrameStock{CardId: owned.Card.ID, CardFrameId: owned.Definition.ID, Amount: uint64(owned.Quantity)})
	}
	return result
}

func protoCardStock(owned player.OwnedCard) *cardresource.CardStock {
	result := &cardresource.CardStock{
		CardId:          owned.Definition.ID,
		CardAmount:      uint64(owned.Quantity),
		ExpansionIds:    append([]string(nil), owned.Definition.ExpansionIDs...),
		FirstReceivedAt: timestamppb.New(owned.FirstReceivedAt),
		LastReceivedAt:  timestamppb.New(owned.LastReceivedAt),
	}
	for _, value := range owned.Languages {
		result.LanguageTags = append(result.LanguageTags, &cardresource.CardStock_Types_LangTag{Lang: language.Language(value.Language), Amount: uint64(value.Quantity)})
	}
	return result
}

func protoProfileDecoration(owned player.OwnedCosmetic) *item.ProfileDecoration {
	decorationType := item.ProfileDecoration_Types_TYPE_EMBLEM
	if owned.Definition.Variant == 0 {
		decorationType = item.ProfileDecoration_Types_TYPE_ICON
	}
	return &item.ProfileDecoration{
		Type:       decorationType,
		Id:         owned.Definition.ID,
		Amount:     uint64(owned.Quantity),
		ObtainedAt: timestamppb.New(owned.ObtainedAt),
	}
}

func localSettings(support string, homes ...catalog.HomeSettings) *settings.Info {
	home := catalog.HomeSettings{ThirdPartyDataVersion: "1.0.0", PrivacyPolicyVersion: "1.1.0", TermsOfServiceVersion: "1.1.0"}
	if len(homes) > 0 {
		home = homes[0]
	}
	return &settings.Info{SupportId: support, LanguageType: language.Language_LANGUAGE_FR, ThirdPartyDataProvisionVersion: home.ThirdPartyDataVersion, PrivacyPolicyConsentVersion: home.PrivacyPolicyVersion, TermsOfServiceConsentVersion: home.TermsOfServiceVersion, CountryRegionCode: "FR", YearNumOfBirth: 2000, MonthNumOfBirth: 1, UseOfLastLoginTime: true, UseOfLoginData: true, UseOfPerformanceErrors: false, AvailableType: profilepb.AvailableType_AVAILABLE_TYPE_AVAILABLE, AgeGateType: settings.Info_Types_AGE_GATE_TYPE_A}
}
