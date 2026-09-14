package playerapi

import (
	"context"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/testprofile"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/packlab"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	language "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/language"
	playerstorage "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/resource/player_storage"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/protocol"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/testfixture"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

func TestAuthorizeBootstrapAndUnknownMutation(t *testing.T) {
	t.Parallel()
	state, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	listener := bufconn.Listen(1 << 20)
	masterPath := os.Getenv("PTCGP_TEST_MASTER_DATA")
	if masterPath == "" {
		masterPath = testfixture.MasterData(t)
	}
	master, err := catalog.Open(masterPath)
	if err != nil {
		t.Fatal(err)
	}
	players := player.New(state, master)
	if _, _, err := state.EnsurePlayer(context.Background(), "fixture-device"); err != nil {
		t.Fatal(err)
	}
	chosen, err := players.Create(context.Background(), player.CreateInput{DisplayName: "Ondine", Language: "en_US", Country: "US", Level: 7, Experience: 1175, TutorialComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := players.Apply(context.Background(), chosen.ID, player.Change{Cards: map[string]int64{"PK_10_000010_00": 2}, Currencies: map[string]int64{"SHOPTICKET": 76}, Items: map[string]int64{"PACK_CHARGER_100030": 39}}); err != nil {
		t.Fatal(err)
	}
	if err := players.SelectActive(context.Background(), chosen.ID); err != nil {
		t.Fatal(err)
	}
	server, err := NewGRPCServer(players, packlab.New(state, master), master, slog.New(slog.NewTextHandler(io.Discard, nil)), true, testprofile.Baseline())
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	conn, err := grpc.NewClient("passthrough:///local",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(transport.NewCodec())),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	base := metadata.Pairs(
		"x-takasho-app-version", testprofile.Baseline().AppVersion,
		"x-takasho-protocol-version", testprofile.Baseline().SDKVersion,
		"x-takasho-build-version", testprofile.Baseline().BuildHash,
		"x-takasho-sdk-version", testprofile.Baseline().ClientSDKVersion,
	)
	ctx := metadata.NewOutgoingContext(context.Background(), base)
	var responseHeaders metadata.MD
	requestStartedAt := time.Now().Unix()
	authorize, err := api.NewSystemClient(conn).AuthorizeV1(
		ctx,
		&api.SystemAuthorizeV1_Types_Request{DeviceAccount: "fixture-device"},
		grpc.Header(&responseHeaders),
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := responseHeaders.Get(protocol.MasterMemoryAladdinHeader); len(got) != 1 || got[0] != testprofile.Baseline().MasterMemoryAladdinHash {
		t.Fatalf("master revision response metadata = %q, want %q", got, testprofile.Baseline().MasterMemoryAladdinHash)
	}
	if got := responseHeaders.Get(protocol.AndroidAssetAladdinHeader); len(got) != 1 || got[0] != testprofile.Baseline().AndroidAssetAladdinHash {
		t.Fatalf("asset revision response metadata = %q, want %q", got, testprofile.Baseline().AndroidAssetAladdinHash)
	}
	if got := responseHeaders.Get(protocol.ProtocolVersionHeader); len(got) != 1 || got[0] != testprofile.Baseline().ResponseProtocolVersion {
		t.Fatalf("response protocol metadata = %q, want %q", got, testprofile.Baseline().ResponseProtocolVersion)
	}
	timestamps := responseHeaders.Get(protocol.RequestedTimestampHeader)
	if len(timestamps) != 1 {
		t.Fatalf("requested timestamp response metadata = %q", timestamps)
	}
	responseTimestamp, err := strconv.ParseInt(timestamps[0], 10, 64)
	if err != nil || responseTimestamp < requestStartedAt || responseTimestamp > time.Now().Unix() {
		t.Fatalf("requested timestamp response metadata = %q, want current Unix time", timestamps[0])
	}
	if authorize.GetSessionToken() == "" || authorize.GetPlayerId() == "" || authorize.GetAssetBaseUrl() != testprofile.Baseline().AssetBaseURL {
		t.Fatalf("incomplete authorization: %+v", authorize)
	}
	if authorize.GetSignedCookie() == nil || authorize.GetSignedCookie().GetExpireAt() == nil {
		t.Fatalf("authorization omitted the signed-cookie envelope: %+v", authorize)
	}
	if err := authorize.GetSignedCookie().GetExpireAt().CheckValid(); err != nil {
		t.Fatalf("authorization returned an invalid cookie expiration: %v", err)
	}
	authed := base.Copy()
	authed.Set("x-takasho-session-token", authorize.GetSessionToken())
	ctx = metadata.NewOutgoingContext(context.Background(), authed)
	profileResponse, err := api.NewPlayerProfileClient(conn).MyProfileV1(ctx, &api.MyProfileV1_Types_Request{})
	if err != nil {
		t.Fatal(err)
	}
	if got := profileResponse.GetProfile().GetProfileSpine().GetPlayerId(); got != authorize.GetPlayerId() {
		t.Fatalf("profile player ID = %q, want %q", got, authorize.GetPlayerId())
	}
	if spine := profileResponse.GetProfile().GetProfileSpine(); spine.GetCollection() == nil || spine.GetIconId() == "" {
		t.Fatalf("profile omitted required visual envelope: %+v", spine)
	}
	if profileResponse.GetNicknameChangedAt() == nil || profileResponse.GetProfile().GetMessageId() == "" {
		t.Fatalf("profile omitted required stable metadata: %+v", profileResponse)
	}
	if spine := profileResponse.GetProfile().GetProfileSpine(); spine.GetNickname() != "Ondine" || spine.GetPlayerLevel() != 7 {
		t.Fatalf("selected profile not rendered: %+v", spine)
	}
	resourcesResponse, err := api.NewPlayerResourcesClient(conn).SyncV1(ctx, &api.PlayerResourcesSyncV1_Types_Request{})
	if err != nil {
		t.Fatal(err)
	}
	resources := resourcesResponse.GetPlayerResources()
	if resources.GetExpStock().GetCurrentLevel() != 7 || resources.GetExpStock().GetExp() != 1175 || len(resources.GetCardStocks()) != 1 || resources.GetCardStocks()[0].GetCardAmount() != 2 || len(resources.GetCurrencies()) != 1 || len(resources.GetPackPowerChargers()) != 1 {
		t.Fatalf("profile resources not rendered: %+v", resources)
	}
	loginResponse, err := api.NewSystemClient(conn).LoginV1(ctx, &api.SystemLoginV1_Types_Request{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := loginResponse.GetPlayerSettingsInfo().GetSupportId(), store.SupportID(chosen.ID); got != want {
		t.Fatalf("support ID = %q, want selected player's %q", got, want)
	}
	if got := loginResponse.GetPlayerSettingsInfo(); got.GetLanguageType() != language.Language_LANGUAGE_EN || got.GetCountryRegionCode() != "US" {
		t.Fatalf("login settings = %+v, want English/US", got)
	}
	savedSettings, err := api.NewPlayerSettingsClient(conn).SaveInfoV1(ctx, &api.PlayerSettingSaveInfoV1_Types_Request{LanguageType: language.Language_LANGUAGE_DE, CountryRegionCode: "DE"})
	if err != nil {
		t.Fatal(err)
	}
	if got := savedSettings.GetInfo(); got.GetLanguageType() != language.Language_LANGUAGE_DE || got.GetCountryRegionCode() != "DE" {
		t.Fatalf("saved settings = %+v, want German/DE", got)
	}
	loginResponse, err = api.NewSystemClient(conn).LoginV1(ctx, &api.SystemLoginV1_Types_Request{LanguageType: language.Language_LANGUAGE_FR, CountryRegionCode: "FR"})
	if err != nil {
		t.Fatal(err)
	}
	if got := loginResponse.GetPlayerSettingsInfo(); got.GetLanguageType() != language.Language_LANGUAGE_DE || got.GetCountryRegionCode() != "DE" {
		t.Fatalf("authoritative login settings = %+v, want persisted German/DE", got)
	}
	acquisition := loginResponse.GetItemAcquisitionResult()
	if acquisition == nil || acquisition.GetAcquiredItems() == nil || acquisition.GetAcceptedItems() == nil || acquisition.GetItemState() == nil || acquisition.GetAcquiredItemStats() == nil || acquisition.GetOverflownItems() == nil {
		t.Fatalf("login omitted the empty item-acquisition envelope: %+v", acquisition)
	}
	tutorialResponse, err := api.NewTutorialClient(conn).CompleteV1(ctx, &api.TutorialCompleteV1_Types_Request{TutorialId: "7777", TutorialStep: 4})
	if err != nil {
		t.Fatal(err)
	}
	if tutorialResponse.GetTutorialId() != "7777" || tutorialResponse.GetTutorialStep() != 4 {
		t.Fatalf("tutorial completion response = %+v", tutorialResponse)
	}
	loginResponse, err = api.NewSystemClient(conn).LoginV1(ctx, &api.SystemLoginV1_Types_Request{})
	if err != nil {
		t.Fatal(err)
	}
	foundTutorial := false
	for _, progress := range loginResponse.GetTutorialCompletes() {
		if progress.GetTutorialId() == "7777" && progress.GetTutorialStep() == 4 {
			foundTutorial = true
			break
		}
	}
	if !foundTutorial {
		t.Fatalf("login omitted persisted tutorial completion: %+v", loginResponse.GetTutorialCompletes())
	}
	comebackResponse, err := api.NewComebackPlayerClient(conn).GetComebackPlayerInfoV1(ctx, &api.ComebackPlayerGetComebackPlayerInfoV1_Types_Request{})
	if err != nil {
		t.Fatal(err)
	}
	if comebackResponse.GetComebackPlayerInfo() == nil {
		t.Fatal("comeback response omitted comeback_player_info")
	}
	if _, err := api.NewSoloBattleClient(conn).GetEventBattlesV1(ctx, &api.SoloBattleGetEventBattlesV1_Types_Request{}); err != nil {
		t.Fatalf("event-battle startup read failed: %v", err)
	}
	feedResponse, err := api.NewFeedClient(conn).RenewTimelineV1(ctx, &api.FeedRenewTimelineV1_Types_Request{})
	if err != nil {
		t.Fatal(err)
	}
	if feedResponse.GetTimeline() == nil || feedResponse.GetTimeline().GetRenewAfter() == nil || feedResponse.GetChallengePower() == nil || feedResponse.GetChallengePower().GetLastAutoHealedAt() == nil {
		t.Fatalf("feed response omitted required empty-state envelopes: %+v", feedResponse)
	}
	preMessage, err := api.NewTradeClient(conn).GetPreMessageV1(ctx, &api.TradeGetPreMessageV1_Types_Request{PlayerId: authorize.GetPlayerId()})
	if err != nil {
		t.Fatal(err)
	}
	if preMessage.GetPreMessage().GetTradeMessageStanceId() != "STANCE_ID_DESIRED_ONLY" || preMessage.GetPreMessage().GetLanguage().String() != "LANGUAGE_FR" {
		t.Fatalf("trade profile prerequisite not rendered: %+v", preMessage)
	}
	emblems, err := api.NewPlayerResourcesClient(conn).GetNewEmblemArrivalV1(ctx, &api.PlayerResourcesGetNewEmblemArrivalV1_Types_Request{})
	if err != nil {
		t.Fatal(err)
	}
	if emblems.GetNewArrivals() == nil || emblems.GetPlayerResources() == nil {
		t.Fatalf("emblem response omitted required empty-state envelopes: %+v", emblems)
	}
	if len(emblems.GetPlayerResources().GetCardStocks()) != 0 || len(emblems.GetPlayerResources().GetCurrencies()) != 0 || emblems.GetPlayerResources().GetExpStock() != nil {
		t.Fatalf("emblem response leaked the full player inventory: %+v", emblems.GetPlayerResources())
	}
	actions, err := api.NewActionClient(conn).SyncStatesV1(ctx, &api.ActionSyncStatesV1_Types_Request{})
	if err != nil {
		t.Fatal(err)
	}
	if actions.GetLastModifiedAt() == nil {
		t.Fatalf("action response omitted last_modified_at: %+v", actions)
	}
	storageResponse, err := api.NewPlayerStorageClient(conn).SetEntriesV1(ctx, &api.PlayerStorageSetEntriesV1_Types_Request{
		Entries:      []*playerstorage.Entry{{Key: "PlayerPrefSetting--1", Value: []byte("fixture")}},
		NextRevision: "fixture-revision",
	})
	if err != nil {
		t.Fatal(err)
	}
	if storageResponse.GetRevision() != "fixture-revision" || len(storageResponse.GetEntries()) != 1 || storageResponse.GetEntries()[0].GetPlayerId() != authorize.GetPlayerId() || storageResponse.GetEntries()[0].GetCreatedAt() == nil {
		t.Fatalf("storage mutation did not echo a server-owned entry: %+v", storageResponse)
	}
	missionResponse, err := api.NewMissionClient(conn).CompleteV2(ctx, &api.MissionCompleteV2_Types_Request{})
	if err != nil {
		t.Fatal(err)
	}
	if missionResponse.GetItemAcquisitionResult() == nil {
		t.Fatalf("mission completion omitted item-acquisition envelope: %+v", missionResponse)
	}
	setDesires, err := api.NewCardClient(conn).SetDesiresV1(ctx, &api.CardSetDesiresV1_Types_Request{})
	if err != nil {
		t.Fatalf("registered compatibility mutation failed: %v", err)
	}
	if setDesires == nil {
		t.Fatal("registered compatibility mutation returned a nil response")
	}
	albumList, err := api.NewAlbumClient(conn).ListV1(ctx, &api.AlbumListV1_Types_Request{})
	if err != nil {
		t.Fatalf("unregistered compatibility read failed: %v", err)
	}
	if albumList == nil {
		t.Fatal("unregistered compatibility read returned a nil response")
	}
}
