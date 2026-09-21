package playerapi

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/packlab"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/testfixture"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/testprofile"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/test/bufconn"
)

func TestTutorialCreationFlowUsesRouteMasterData(t *testing.T) {
	for _, test := range []struct {
		packID     string
		route      int32
		rewardStep int64
	}{
		{packID: "TUTORIAL_1", route: 2, rewardStep: 2},
		{packID: "TUTORIAL_2", route: 1, rewardStep: 1},
		{packID: "TUTORIAL_3", route: 3, rewardStep: 3},
	} {
		t.Run(test.packID, func(t *testing.T) {
			ctx, players, playerID, conn, closeTest := newTutorialTestServer(t)
			defer closeTest()
			client := api.NewTutorialClient(conn)
			login, err := api.NewSystemClient(conn).LoginV1(ctx, &api.SystemLoginV1_Types_Request{})
			if err != nil {
				t.Fatal(err)
			}
			starter := login.GetItemAcquisitionResult().GetAcquiredItems()
			if len(starter.GetPeripheralGoods()) != 8 || len(starter.GetProfileDecorations()) != 6 {
				t.Fatalf("starter inventory = %+v", starter)
			}
			loginReplay, err := api.NewSystemClient(conn).LoginV1(ctx, &api.SystemLoginV1_Types_Request{})
			if err != nil || len(loginReplay.GetItemAcquisitionResult().GetAcquiredItems().GetPeripheralGoods()) != 0 || len(loginReplay.GetItemAcquisitionResult().GetAcquiredItems().GetProfileDecorations()) != 0 {
				t.Fatalf("starter inventory replay = %+v err=%v", loginReplay, err)
			}

			choice, err := client.ChoiceExchangeRouteV1(ctx, &api.TutorialChoiceExchangeRouteV1_Types_Request{PackId: test.packID, TutorialId: "1010", TutorialStep: 2})
			if err != nil {
				t.Fatal(err)
			}
			if int32(choice.GetExchangeRouteType()) != test.route || choice.GetUnpackOrder() == nil || len(choice.GetUnpackOrder().GetProduces().GetCardInstances()) != 5 {
				t.Fatalf("route choice response = %+v", choice)
			}
			if got := len(choice.GetItemAcquisitionResult().GetAcquiredItems().GetCardInstances()); got != 5 {
				t.Fatalf("route cards = %d, want 5", got)
			}
			afterChoice, err := players.Profile(context.Background(), playerID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.ChoiceExchangeRouteV1(ctx, &api.TutorialChoiceExchangeRouteV1_Types_Request{PackId: test.packID, TutorialId: "1010", TutorialStep: 2}); err != nil {
				t.Fatal(err)
			}
			afterChoiceReplay, err := players.Profile(context.Background(), playerID)
			if err != nil || totalCards(afterChoiceReplay) != totalCards(afterChoice) {
				t.Fatalf("route replay changed cards: before=%d after=%d err=%v", totalCards(afterChoice), totalCards(afterChoiceReplay), err)
			}

			timeline, err := client.GetFeedTimelineV1(ctx, &api.TutorialGetFeedTimelineV1_Types_Request{})
			if err != nil {
				t.Fatal(err)
			}
			feed, err := players.TutorialFeed(test.route)
			if err != nil {
				t.Fatal(err)
			}
			if len(timeline.GetTimeline().GetFreeFeeds()) != 1 || len(timeline.GetTimeline().GetFreeFeeds()[0].GetContents().GetCards()) != len(feed.CardIDs) {
				t.Fatalf("tutorial timeline = %+v", timeline.GetTimeline())
			}
			for index, card := range timeline.GetTimeline().GetFreeFeeds()[0].GetContents().GetCards() {
				if card.GetCardId() != feed.CardIDs[index] {
					t.Fatalf("feed card %d = %q, want %q", index, card.GetCardId(), feed.CardIDs[index])
				}
			}

			purchase, err := client.PackPurchaseV1(ctx, &api.TutorialPackPurchaseV1_Types_Request{TutorialId: "1000", TutorialStep: 600})
			if err != nil {
				t.Fatal(err)
			}
			items := purchase.GetItemAcquisitionResult().GetAcquiredItems()
			if purchase.GetUnpackOrder() == nil || len(items.GetCardInstances()) != 5 || len(items.GetExps()) != 1 || len(items.GetPackCeilPoints()) != 1 || len(items.GetPackGranteePoints()) != 1 {
				t.Fatalf("tutorial pack response = %+v", purchase)
			}
			if _, err := api.NewFeedClient(conn).ShareV1(ctx, &api.FeedShareV1_Types_Request{TransactionId: purchase.GetUnpackOrder().GetTransactionId()}); err != nil {
				t.Fatalf("share tutorial pack: %v", err)
			}
			afterPack, err := players.Profile(context.Background(), playerID)
			if err != nil {
				t.Fatal(err)
			}
			packReplay, err := client.PackPurchaseV1(ctx, &api.TutorialPackPurchaseV1_Types_Request{TutorialId: "1000", TutorialStep: 600})
			if err != nil {
				t.Fatal(err)
			}
			if packReplay.GetUnpackOrder().GetTransactionId() != purchase.GetUnpackOrder().GetTransactionId() || len(packReplay.GetItemAcquisitionResult().GetAcquiredItems().GetCardInstances()) != 0 {
				t.Fatalf("pack replay response = %+v", packReplay)
			}
			afterPackReplay, err := players.Profile(context.Background(), playerID)
			if err != nil || afterPackReplay.Player.Experience != afterPack.Player.Experience || totalCards(afterPackReplay) != totalCards(afterPack) {
				t.Fatalf("pack replay changed state: before=%+v after=%+v err=%v", afterPack.Player, afterPackReplay.Player, err)
			}

			level, err := api.NewPlayerLevelClient(conn).MayLevelUpV1(ctx, &api.PlayerLevelMayLevelUpV1_Types_Request{})
			if err != nil {
				t.Fatal(err)
			}
			levelItems := level.GetItemAcquisitionResult().GetAcquiredItems()
			if level.GetPreviousLevel() != 1 || level.GetCurrentLevel() != 2 || len(levelItems.GetPokeGolds()) != 1 || len(levelItems.GetRevivalClocks()) != 1 || len(levelItems.GetPackPowerChargers()) != 1 || len(levelItems.GetChallengePowerChargers()) != 1 {
				t.Fatalf("level-up response = %+v", level)
			}
			levelReplay, err := api.NewPlayerLevelClient(conn).MayLevelUpV1(ctx, &api.PlayerLevelMayLevelUpV1_Types_Request{})
			if err != nil || levelReplay.GetPreviousLevel() != 2 || levelReplay.GetCurrentLevel() != 2 || len(levelReplay.GetItemAcquisitionResult().GetAcquiredItems().GetPackPowerChargers()) != 0 {
				t.Fatalf("level replay response = %+v err=%v", levelReplay, err)
			}

			challenge, err := client.ChallengeFeedV1(ctx, &api.TutorialChallengeFeedV1_Types_Request{TutorialId: "1000", TutorialStep: 700})
			if err != nil {
				t.Fatal(err)
			}
			if len(challenge.GetPickedCards()) != 1 || challenge.GetPickedCards()[0].GetCardId() != feed.RewardCardID || len(challenge.GetUpdatedCardStocks()) != 1 {
				t.Fatalf("feed challenge response = %+v", challenge)
			}
			afterChallenge, err := players.Profile(context.Background(), playerID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.ChallengeFeedV1(ctx, &api.TutorialChallengeFeedV1_Types_Request{TutorialId: "1000", TutorialStep: 700}); err != nil {
				t.Fatal(err)
			}
			afterChallengeReplay, err := players.Profile(context.Background(), playerID)
			if err != nil || totalCards(afterChallengeReplay) != totalCards(afterChallenge) {
				t.Fatalf("challenge replay changed cards: before=%d after=%d err=%v", totalCards(afterChallenge), totalCards(afterChallengeReplay), err)
			}

			reward, err := client.CompleteV1(ctx, &api.TutorialCompleteV1_Types_Request{TutorialId: "1011", TutorialStep: test.rewardStep})
			if err != nil {
				t.Fatal(err)
			}
			masterReward, ok := players.CatalogTutorialReward("1011", test.rewardStep)
			if !ok || len(reward.GetItemAcquisitionResult().GetAcquiredItems().GetCardInstances()) != len(masterReward.Items) {
				t.Fatalf("route reward response = %+v master=%+v", reward, masterReward)
			}
			afterReward, err := players.Profile(context.Background(), playerID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.CompleteV1(ctx, &api.TutorialCompleteV1_Types_Request{TutorialId: "1011", TutorialStep: test.rewardStep}); err != nil {
				t.Fatal(err)
			}
			afterRewardReplay, err := players.Profile(context.Background(), playerID)
			if err != nil || totalCards(afterRewardReplay) != totalCards(afterReward) {
				t.Fatalf("reward replay changed cards: before=%d after=%d err=%v", totalCards(afterReward), totalCards(afterRewardReplay), err)
			}
		})
	}
}

func TestLegacyTutorialProfileWithoutPersistedRouteDoesNotFail(t *testing.T) {
	ctx, players, playerID, conn, closeTest := newTutorialTestServer(t)
	defer closeTest()
	if err := players.CompleteTutorial(context.Background(), playerID, "1010", 1); err != nil {
		t.Fatal(err)
	}
	client := api.NewTutorialClient(conn)
	if _, err := client.GetFeedTimelineV1(ctx, &api.TutorialGetFeedTimelineV1_Types_Request{}); err != nil {
		t.Fatalf("legacy tutorial timeline: %v", err)
	}
	if err := players.CompleteTutorial(context.Background(), playerID, "1000", 600); err != nil {
		t.Fatal(err)
	}
	if _, err := api.NewFeedClient(conn).ShareV1(ctx, &api.FeedShareV1_Types_Request{TransactionId: "legacy-tutorial-transaction"}); err != nil {
		t.Fatalf("legacy tutorial pack share: %v", err)
	}
	if _, err := client.CompleteV1(ctx, &api.TutorialCompleteV1_Types_Request{TutorialId: "1011", TutorialStep: 1}); err != nil {
		t.Fatalf("legacy route reward: %v", err)
	}
	if route, chosen, err := players.ChosenTutorialRoute(context.Background(), playerID); err != nil || !chosen || route != 1 {
		t.Fatalf("backfilled route = %d chosen=%v err=%v", route, chosen, err)
	}
}

func totalCards(profile player.Profile) int64 {
	var total int64
	for _, card := range profile.Cards {
		total += card.Quantity
	}
	return total
}

func newTutorialTestServer(t *testing.T) (context.Context, *player.Manager, string, *grpc.ClientConn, func()) {
	t.Helper()
	state, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	masterPath := os.Getenv("PTCGP_TEST_MASTER_DATA")
	if masterPath == "" {
		masterPath = testfixture.MasterData(t)
	}
	master, err := catalog.Open(masterPath)
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	players := player.New(state, master)
	if _, _, err := state.EnsurePlayer(context.Background(), "tutorial-device"); err != nil {
		state.Close()
		t.Fatal(err)
	}
	created, err := players.Create(context.Background(), player.CreateInput{DisplayName: "Tutoriel", Language: "fr_FR", Country: "FR", Level: 1, Experience: 0})
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	if err := players.SelectActive(context.Background(), created.ID); err != nil {
		state.Close()
		t.Fatal(err)
	}
	listener := bufconn.Listen(1 << 20)
	server, err := NewGRPCServer(players, packlab.New(state, master), master, slog.New(slog.NewTextHandler(io.Discard, nil)), true, testprofile.Baseline())
	if err != nil {
		state.Close()
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///local", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithDefaultCallOptions(grpc.ForceCodec(transport.NewCodec())))
	if err != nil {
		server.Stop()
		state.Close()
		t.Fatal(err)
	}
	base := metadata.Pairs("x-takasho-app-version", testprofile.Baseline().AppVersion, "x-takasho-protocol-version", testprofile.Baseline().SDKVersion, "x-takasho-build-version", testprofile.Baseline().BuildHash, "x-takasho-sdk-version", testprofile.Baseline().ClientSDKVersion)
	ctx := metadata.NewOutgoingContext(context.Background(), base)
	authorize, err := api.NewSystemClient(conn).AuthorizeV1(ctx, &api.SystemAuthorizeV1_Types_Request{DeviceAccount: "tutorial-device"})
	if err != nil {
		conn.Close()
		server.Stop()
		state.Close()
		t.Fatal(err)
	}
	authed := base.Copy()
	authed.Set("x-takasho-session-token", authorize.GetSessionToken())
	return metadata.NewOutgoingContext(context.Background(), authed), players, created.ID, conn, func() { conn.Close(); server.Stop(); state.Close() }
}
