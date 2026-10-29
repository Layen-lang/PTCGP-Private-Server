package playerapi

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	api "github.com/Layen-lang/PTCGP-Private-Server/internal/proto/takasho/schema/lettuce_server/player_api"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/testfixture"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPrivateMatchRulesAreSharedByPasswordAndExpire(t *testing.T) {
	t.Parallel()
	rules := newPrivateMatchRules()
	now := time.Unix(1_800_000_000, 0)
	rules.now = func() time.Time { return now }
	server := pvpCasualServer{rules: rules}
	host := context.WithValue(context.Background(), playerContextKey{}, store.Player{ID: "host"})
	guest := context.WithValue(context.Background(), playerContextKey{}, store.Player{ID: "guest"})

	setting := &api.PrivateMatchRuleSetting{DeckTypeRestriction: api.DeckTypeRestriction_DECK_TYPE_RESTRICTION_MY_DECK_ONLY}
	created, err := server.SetupPrivateMatchRuleV1(host, &api.PvpCasualSetupPrivateMatchRuleV1_Types_Request{Password: " 1234 ", RuleSetting: setting})
	if err != nil || !created.GetIsNew() || created.GetRuleSetting().GetDeckTypeRestriction() != api.DeckTypeRestriction_DECK_TYPE_RESTRICTION_MY_DECK_ONLY {
		t.Fatalf("first setup = %+v, %v", created, err)
	}
	updated, err := server.SetupPrivateMatchRuleV1(host, &api.PvpCasualSetupPrivateMatchRuleV1_Types_Request{Password: "1234"})
	if err != nil || updated.GetIsNew() {
		t.Fatalf("second setup = %+v, %v", updated, err)
	}
	read, err := server.GetPrivateMatchRuleV1(guest, &api.PvpCasualGetPrivateMatchRuleV1_Types_Request{Password: "1234"})
	if err != nil || read.GetRuleSetting() == nil || read.GetRuleSetting().GetDeckTypeRestriction() != api.DeckTypeRestriction_DECK_TYPE_RESTRICTION_UNSPECIFIED {
		t.Fatalf("guest read = %+v, %v", read, err)
	}
	now = now.Add(privateMatchRuleLifetime)
	expired, err := server.GetPrivateMatchRuleV1(guest, &api.PvpCasualGetPrivateMatchRuleV1_Types_Request{Password: "1234"})
	if err != nil || expired.GetRuleSetting() != nil {
		t.Fatalf("expired read = %+v, %v", expired, err)
	}
	if _, err := server.GetPrivateMatchRuleV1(guest, &api.PvpCasualGetPrivateMatchRuleV1_Types_Request{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("empty password error = %v", err)
	}
}

func TestSomeoneDecksExposeFirstFriendDeck(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	masterPath := os.Getenv("PTCGP_TEST_MASTER_DATA")
	if masterPath == "" {
		masterPath = testfixture.MasterData(t)
	}
	master, err := catalog.Open(masterPath)
	if err != nil {
		t.Fatal(err)
	}
	players := player.New(state, master)
	if _, _, err := state.EnsurePlayer(ctx, "fixture-device"); err != nil {
		t.Fatal(err)
	}
	self, err := players.Create(ctx, player.CreateInput{DisplayName: "Ondine", Language: "en_US", Country: "US", Level: 7, Experience: 1175, TutorialComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	friend, err := players.Create(ctx, player.CreateInput{DisplayName: "Pierre", Language: "en_US", Country: "US", Level: 7, Experience: 1175, TutorialComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	const cardID = "PK_10_000010_00"
	if err := players.Apply(ctx, friend.ID, player.Change{Cards: map[string]int64{cardID: 2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := players.SaveDeck(ctx, friend.ID, store.Deck{Name: "Friend", Cards: map[string]int64{cardID: 2}, EnergyTypes: []int32{2}}); err != nil {
		t.Fatal(err)
	}
	if _, err := players.SendFriendRequests(ctx, self.ID, []string{friend.ID}); err != nil {
		t.Fatal(err)
	}
	if err := players.ApproveFriendRequest(ctx, friend.ID, self.ID); err != nil {
		t.Fatal(err)
	}

	server := deckServer{players: players}
	response, err := server.GetSomeoneDecksV1(context.WithValue(ctx, playerContextKey{}, self), &api.DeckGetSomeoneDecksV1_Types_Request{})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.GetFriends()) != 1 || response.GetFriends()[0].GetPlayerId() != friend.ID || len(response.GetSomeoneDecks()) != 1 {
		t.Fatalf("someone decks = %+v", response)
	}
	deck := response.GetSomeoneDecks()[0]
	if len(deck.GetPokemonIds()) != 2 || deck.GetPokemonIds()[0] != cardID || len(deck.GetTrainerIds()) != 0 || len(deck.GetEnergyTypes()) != 1 {
		t.Fatalf("someone deck = %+v", deck)
	}
	if _, err := server.CountSomeoneDeckV1(context.WithValue(ctx, playerContextKey{}, self), &api.DeckCountSomeoneDeckV1_Types_Request{SomeoneDeck: deck}); err != nil {
		t.Fatal(err)
	}
}
