package player

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/testfixture"
)

type launcherFake struct{ calls int }

func (f *launcherFake) Open(context.Context) error { f.calls++; return nil }

func TestManagerValidatesAndBuildsProfile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	manager := New(state, master)
	if _, err := manager.Create(ctx, CreateInput{DisplayName: "Sans langue", Level: 1}); !errors.Is(err, ErrValidation) {
		t.Fatalf("missing account locale error = %v, want validation error", err)
	}
	created, err := manager.Create(ctx, CreateInput{DisplayName: "Ondine", Language: "fr_FR", Country: "FR", Level: 7, Experience: 1175, TutorialComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(ctx, created.ID, Change{Cards: map[string]int64{"PK_10_000010_00": 2}, Items: map[string]int64{"PACK_CHARGER_100030": 39}, Currencies: map[string]int64{"SHOPTICKET": 76}}); err != nil {
		t.Fatal(err)
	}
	profile, err := manager.Profile(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.Cards) != 1 || profile.Cards[0].Definition.Name != "Bulbizarre" || len(profile.Items) != 1 || len(profile.Currencies) != 1 {
		t.Fatalf("profile = %+v", profile)
	}
	if !profile.TutorialComplete || len(profile.Tutorial) != len(master.TutorialCompletions()) {
		t.Fatalf("tutorial state = %t with %d entries, want true with %d", profile.TutorialComplete, len(profile.Tutorial), len(master.TutorialCompletions()))
	}
	version := profile.Player.StateVersion
	if err := manager.Apply(ctx, created.ID, Change{Cards: map[string]int64{"PK_10_000010_00": 2}}); err != nil {
		t.Fatal(err)
	}
	profile, err = manager.Profile(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Player.StateVersion != version {
		t.Fatalf("idempotent quantity changed version: %d -> %d", version, profile.Player.StateVersion)
	}
	if err := manager.Apply(ctx, created.ID, Change{Cards: map[string]int64{"UNKNOWN": 1}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unknown card error = %v", err)
	}
}

func TestOpenSelectsThenAuthorizeConfirmsProfile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	manager := New(state, master)
	_, _, err = state.EnsurePlayer(ctx, "android")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Create(ctx, CreateInput{DisplayName: "Pierre", Language: "fr_FR", Country: "FR", Level: 12, Experience: 2605, TutorialComplete: true})
	if err != nil {
		t.Fatal(err)
	}
	launcher := &launcherFake{}
	if err := manager.OpenInGame(ctx, second.ID, launcher); err != nil {
		t.Fatal(err)
	}
	if launcher.calls != 1 {
		t.Fatalf("launcher calls=%d", launcher.calls)
	}
	authorization, err := manager.Authorize(ctx, "android")
	if err != nil {
		t.Fatal(err)
	}
	if authorization.Player.ID != second.ID || authorization.Profile.Player.DisplayName != "Pierre" {
		t.Fatalf("authorization = %+v", authorization)
	}
	device, err := manager.Device(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if device.AuthorizedPlayerID != second.ID {
		t.Fatalf("authorized device = %+v", device)
	}
}

func TestCompletePresetUsesTheCatalog(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "complete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	manager := New(state, master)
	created, err := manager.Create(ctx, CreateInput{DisplayName: "Complet", Language: "fr_FR", Country: "FR", Level: 60, Experience: 2755, Preset: "complete", CardQuantity: 10})
	if err != nil {
		t.Fatal(err)
	}
	profile, err := manager.Profile(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(profile.Cards) != len(master.Cards()) || profile.Cards[0].Quantity != 120 || len(profile.Cards[0].Languages) != 12 || len(profile.Items) != len(master.Items()) || len(profile.CardSkins) != len(master.CardSkinCompatibilities()) || len(profile.CardFrames) != len(master.CardFrameCompatibilities()) {
		t.Fatalf("complete profile counts: cards=%d items=%d skins=%d frames=%d", len(profile.Cards), len(profile.Items), len(profile.CardSkins), len(profile.CardFrames))
	}
	for _, amount := range profile.Cards[0].Languages {
		if amount.Quantity != 10 {
			t.Fatalf("complete card language %d quantity=%d, want 10", amount.Language, amount.Quantity)
		}
	}
	for _, cosmetic := range append(profile.CardSkins, profile.CardFrames...) {
		if cosmetic.Quantity != 2 {
			t.Fatalf("complete card cosmetic %s/%s quantity=%d, want 2", cosmetic.Card.ID, cosmetic.Definition.ID, cosmetic.Quantity)
		}
	}
}

func TestCompleteTrophyUsesProjectedInventoryAndRejectsUnmetRank(t *testing.T) {
	ctx := context.Background()
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "trophy-progress.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	manager := New(state, master)
	created, err := manager.Create(ctx, CreateInput{DisplayName: "Trophées", Language: "fr_FR", Country: "FR", Level: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(ctx, created.ID, Change{Cards: map[string]int64{"PK_10_000010_00": 100}}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.CompleteTrophies(ctx, created.ID, []store.TrophyState{{ID: "TROPHY_100050_Grass_A1", Rank: 1}}); err != nil {
		t.Fatalf("bronze trophy completion = %v", err)
	}
	if _, err := manager.CompleteTrophies(ctx, created.ID, []store.TrophyState{{ID: "TROPHY_100050_Grass_A1", Rank: 2}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("unmet silver completion error = %v", err)
	}
}

func TestHiddenEventShopPurchaseSupportsType22(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	state, err := store.Open(ctx, filepath.Join(t.TempDir(), "hidden-event.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	master, err := catalog.Open(testfixture.MasterData(t))
	if err != nil {
		t.Fatal(err)
	}
	manager := New(state, master)
	created, err := manager.Create(ctx, CreateInput{DisplayName: "Rocket", Language: "fr_FR", Country: "FR", Level: 7, Experience: 1175})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Apply(ctx, created.ID, Change{Items: map[string]int64{"TICKET_2608000_EV_01": 9999}}); err != nil {
		t.Fatal(err)
	}
	result, err := manager.PurchaseShopProduct(ctx, created.ID, "EVENT_ROCKET_DUMMY", "SH_CG_EV_2608000_11", "hidden-event-test", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rewards) != 1 || result.Rewards[0].Kind != "item" || result.Rewards[0].SubID != string(catalog.ItemHiddenEvent) {
		t.Fatalf("purchase rewards = %+v", result.Rewards)
	}
	profile, err := manager.Profile(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range profile.Items {
		if item.Definition.ID == "HIDDEN_EVENT_ITEM_100010" {
			found = item.Quantity == 1
		}
	}
	if !found {
		t.Fatalf("hidden event item missing from profile: %+v", profile.Items)
	}
}
