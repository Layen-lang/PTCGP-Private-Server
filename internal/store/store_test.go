package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

func TestSupportIDMatchesCapturedFormatAndBelongsToPlayer(t *testing.T) {
	t.Parallel()

	const firstPlayer = "00000000-0000-4000-8000-000000000001"
	first := SupportID(firstPlayer)
	if !regexp.MustCompile(`^[0-9A-Za-z]{10}$`).MatchString(first) {
		t.Fatalf("support ID %q does not match the captured 10-character alphanumeric format", first)
	}
	if again := SupportID(firstPlayer); again != first {
		t.Fatalf("support ID is not stable: first=%q again=%q", first, again)
	}
	if second := SupportID("00000000-0000-4000-8000-000000000002"); second == first {
		t.Fatalf("different players received the same support ID %q", first)
	}
}

func TestPlayerPersistsAndSessionRenews(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	player, created, err := s.EnsurePlayer(ctx, "device-fixture")
	if err != nil || !created {
		t.Fatalf("first ensure: created=%v err=%v", created, err)
	}
	token, err := s.NewSession(ctx, player.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlayerBySession(ctx, token); err != nil {
		t.Fatal(err)
	}
	newToken, err := s.NewSession(ctx, player.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlayerBySession(ctx, token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("old session error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	again, created, err := s.EnsurePlayer(ctx, "device-fixture")
	if err != nil || created || again.ID != player.ID {
		t.Fatalf("second ensure: player=%+v created=%v err=%v", again, created, err)
	}
	if _, err := s.PlayerBySession(ctx, newToken); err != nil {
		t.Fatal(err)
	}
}

func TestMultiplePlayersSelectionRevokesDeviceSessionsAndPersists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := s.EnsurePlayer(ctx, "android-device")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreatePlayer(ctx, "Deuxième", 7, 1175, true)
	if err != nil {
		t.Fatal(err)
	}
	token, err := s.NewDeviceSession(ctx, "android-device", first.ID, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SelectActive(ctx, "android-device", second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PlayerBySession(ctx, token); !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("old session error = %v", err)
	}
	active, err := s.ActivePlayer(ctx, "android-device")
	if err != nil || active.ID != second.ID {
		t.Fatalf("active = %+v err=%v", active, err)
	}
	if err := s.SelectActive(ctx, "android-device", second.ID); err != nil {
		t.Fatalf("idempotent select: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	players, err := s.Players(ctx)
	if err != nil || len(players) != 2 {
		t.Fatalf("players after restart = %+v err=%v", players, err)
	}
	active, err = s.ActivePlayer(ctx, "android-device")
	if err != nil || active.ID != second.ID {
		t.Fatalf("active after restart = %+v err=%v", active, err)
	}
}

func TestNewDeviceReusesMostRecentlyAuthorizedPlayer(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "recent-player.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := s.CreatePlayer(ctx, "Premier", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreatePlayer(ctx, "Second", 1, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	s.now = func() time.Time { return now }
	if err := s.SelectActive(ctx, "old-device", first.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAuthorization(ctx, "old-device", first.ID); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	if err := s.SelectActive(ctx, "old-device", second.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAuthorization(ctx, "old-device", second.ID); err != nil {
		t.Fatal(err)
	}

	got, created, err := s.EnsurePlayer(ctx, "new-device")
	if err != nil {
		t.Fatal(err)
	}
	if created || got.ID != second.ID {
		t.Fatalf("player=%+v created=%t, want most recently authorized %s", got, created, second.ID)
	}
	players, err := s.Players(ctx)
	if err != nil || len(players) != 2 {
		t.Fatalf("players=%+v error=%v", players, err)
	}
}

func TestMigratesLegacyPlayerWithoutDataLoss(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := `
CREATE TABLE devices(device_account TEXT PRIMARY KEY,password_hash BLOB,created_at INTEGER NOT NULL,last_seen_at INTEGER NOT NULL);
CREATE TABLE players(player_id TEXT PRIMARY KEY,device_account TEXT NOT NULL UNIQUE REFERENCES devices(device_account),display_name TEXT NOT NULL,state_version INTEGER NOT NULL,state_json TEXT NOT NULL,created_at INTEGER NOT NULL,updated_at INTEGER NOT NULL);
CREATE TABLE sessions(token_hash BLOB PRIMARY KEY,player_id TEXT NOT NULL REFERENCES players(player_id),created_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,revoked_at INTEGER);
CREATE TABLE idempotency_keys(player_id TEXT NOT NULL REFERENCES players(player_id),method TEXT NOT NULL,key TEXT NOT NULL,response BLOB NOT NULL,created_at INTEGER NOT NULL,PRIMARY KEY(player_id,method,key));
INSERT INTO devices VALUES('legacy-device',NULL,100,200);
INSERT INTO players VALUES('00000000-0000-4000-8000-000000000001','legacy-device','Ancien',4,'{"tutorial_complete":true,"level":12,"experience":2605}',100,200);`
	if _, err := db.ExecContext(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	active, err := s.ActivePlayer(ctx, "legacy-device")
	if err != nil {
		t.Fatal(err)
	}
	if active.DisplayName != "Ancien" || active.Level != 12 || active.Experience != 2605 || active.StateVersion != 4 {
		t.Fatalf("migrated player = %+v", active)
	}
	snapshot, err := s.Snapshot(ctx, active.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.TutorialComplete {
		t.Fatalf("tutorial was not preserved: %+v", snapshot.Tutorial)
	}
}

func TestIdempotencyKeepsFirstResponse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	player, _, err := s.EnsurePlayer(ctx, "device-fixture")
	if err != nil {
		t.Fatal(err)
	}
	got, replay, err := s.RememberIdempotency(ctx, player.ID, "/Mutation", "key-1", []byte("first"))
	if err != nil || replay || string(got) != "first" {
		t.Fatalf("first: %q replay=%v err=%v", got, replay, err)
	}
	got, replay, err = s.RememberIdempotency(ctx, player.ID, "/Mutation", "key-1", []byte("second"))
	if err != nil || !replay || string(got) != "first" {
		t.Fatalf("replay: %q replay=%v err=%v", got, replay, err)
	}
}

func TestPlayerLifecycleSeedDuplicateReorderAndDelete(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, _, err := s.EnsurePlayer(ctx, "device")
	if err != nil {
		t.Fatal(err)
	}
	seed := InitialInventory{
		Cards: map[string]int64{"CARD": 2}, Currencies: map[string]int64{"MONEY": 99},
		Items:              []InitialItem{{ID: "MAT", Kind: "peripheral_goods", Quantity: 1}},
		ProfileDecorations: map[string]int64{"ICON": 1},
		CardSkins:          []CardCosmeticStock{{CardID: "CARD", CosmeticID: "SKIN", Quantity: 1}},
		CardFrames:         []CardCosmeticStock{{CardID: "CARD", CosmeticID: "FRAME", Quantity: 1}},
		PokeGold:           123,
	}
	second, err := s.CreatePlayerWithInventory(ctx, "Complet", 60, 2755, true, seed)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(ctx, second.ID)
	if err != nil || len(snapshot.Cards) != 1 || len(snapshot.CardSkins) != 1 || len(snapshot.CardFrames) != 1 || len(snapshot.ProfileDecorations) != 1 {
		t.Fatalf("seeded snapshot=%+v err=%v", snapshot, err)
	}
	copy, err := s.DuplicatePlayer(ctx, second.ID, "Copie")
	if err != nil {
		t.Fatal(err)
	}
	copySnapshot, err := s.Snapshot(ctx, copy.ID)
	if err != nil || len(copySnapshot.Cards) != 1 || len(copySnapshot.CardSkins) != 1 || copySnapshot.Cards[0].Quantity != 2 {
		t.Fatalf("duplicate snapshot=%+v err=%v", copySnapshot, err)
	}
	if err := s.ReorderPlayers(ctx, []string{copy.ID, second.ID, first.ID}); err != nil {
		t.Fatal(err)
	}
	players, err := s.Players(ctx)
	if err != nil || players[0].ID != copy.ID || players[2].ID != first.ID {
		t.Fatalf("reordered players=%+v err=%v", players, err)
	}
	if err := s.SelectActive(ctx, "device", second.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeletePlayer(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	active, err := s.ActivePlayer(ctx, "device")
	if err != nil || active.ID == second.ID {
		t.Fatalf("replacement active=%+v err=%v", active, err)
	}
}

func TestCardLanguageQuantitiesPersistAndDuplicate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "languages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	created, err := s.CreatePlayer(ctx, "Polyglotte", 1, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if total, err := s.SetCardLanguage(ctx, created.ID, "CARD", 2, 10); err != nil || total != 10 {
		t.Fatalf("set English card: total=%d err=%v", total, err)
	}
	if total, err := s.SetCardLanguage(ctx, created.ID, "CARD", 4, 3); err != nil || total != 13 {
		t.Fatalf("set French card: total=%d err=%v", total, err)
	}
	snapshot, err := s.Snapshot(ctx, created.ID)
	if err != nil || len(snapshot.Cards) != 1 || snapshot.Cards[0].Quantity != 13 || len(snapshot.Cards[0].Languages) != 2 {
		t.Fatalf("language snapshot=%+v err=%v", snapshot.Cards, err)
	}
	duplicate, err := s.DuplicatePlayer(ctx, created.ID, "Copie langues")
	if err != nil {
		t.Fatal(err)
	}
	copySnapshot, err := s.Snapshot(ctx, duplicate.ID)
	if err != nil || len(copySnapshot.Cards) != 1 || len(copySnapshot.Cards[0].Languages) != 2 || copySnapshot.Cards[0].Quantity != 13 {
		t.Fatalf("duplicated language snapshot=%+v err=%v", copySnapshot.Cards, err)
	}
}

func TestTutorialCompletionPersistsHighestStep(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tutorial.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreatePlayer(ctx, "Tutoriel", 1, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTutorialCompletion(ctx, created.ID, "2160", 4); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTutorialCompletion(ctx, created.ID, "2160", 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot, err := s.Snapshot(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, progress := range snapshot.Tutorial {
		if progress.TutorialID == "2160" {
			if progress.Step != 4 || !progress.Completed {
				t.Fatalf("tutorial progress = %+v, want completed step 4", progress)
			}
			return
		}
	}
	t.Fatal("tutorial 2160 was not persisted")
}

func TestTutorialCompleteSettingIncludesEveryKnownFeature(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "tutorial-complete.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	created, err := s.CreatePlayer(ctx, "Complet", 60, 2755, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTutorialComplete(ctx, created.ID, true); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.TutorialComplete || len(snapshot.Tutorial) != 41 {
		t.Fatalf("completed tutorial state = %t with %d entries, want true with 41", snapshot.TutorialComplete, len(snapshot.Tutorial))
	}
	want := map[string]int64{
		"1000": 999, "1010": 2, "1011": 3,
		"2000": 3, "2001": 5, "2002": 5, "2003": 5, "2010": 2,
		"2140": 6, "2160": 5, "2161": 2, "2162": 2, "2300": 2, "2400": 2,
		"2500": 2, "2510": 2, "2520": 2, "2521": 2, "2530": 2, "2531": 2,
		"2540": 2, "2600": 2, "2700": 2,
		"2800": 999, "2900": 999,
		"3000": 2, "3001": 2, "3002": 2, "3003": 2,
		"3004": 2, "3005": 2, "3006": 2, "3007": 2,
		"3008": 2, "3009": 2, "3010": 2, "3011": 2,
		"3012": 2, "3013": 2, "3014": 2, "3015": 2,
	}
	for _, progress := range snapshot.Tutorial {
		step, ok := want[progress.TutorialID]
		if !ok || progress.Step != step || !progress.Completed {
			t.Fatalf("unexpected completed tutorial: %+v", progress)
		}
		delete(want, progress.TutorialID)
	}
	if len(want) != 0 {
		t.Fatalf("missing completed tutorials: %+v", want)
	}
}

func TestTutorialMasterDataMigrationBackfillsVersion4Profiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tutorial-master-data-migration.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreatePlayer(ctx, "Complet", 60, 2755, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetTutorialCompletions(ctx, created.ID, tutorialCompletionV4); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=5`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot, err := s.Snapshot(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.TutorialComplete || len(snapshot.Tutorial) != 41 {
		t.Fatalf("migrated tutorial state = %t with %d entries, want true with 41", snapshot.TutorialComplete, len(snapshot.Tutorial))
	}
	for _, progress := range snapshot.Tutorial {
		if progress.TutorialID == "3015" && progress.Step == 2 && progress.Completed {
			return
		}
	}
	t.Fatal("tutorial 3015 was not backfilled")
}

func TestTutorialCompletionMigrationBackfillsCompletedProfiles(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tutorial-migration.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	created, err := s.CreatePlayer(ctx, "Complet", 60, 2755, true)
	if err != nil {
		t.Fatal(err)
	}
	incomplete, err := s.CreatePlayer(ctx, "En cours", 1, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordTutorialCompletion(ctx, incomplete.ID, "1000", 100); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM tutorial_progress WHERE player_id=? AND tutorial_id='2160'`, created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version=4`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var step int64
	if err := s.db.QueryRowContext(ctx, `SELECT step FROM tutorial_progress WHERE player_id=? AND tutorial_id='2160'`, created.ID).Scan(&step); err != nil {
		t.Fatal(err)
	}
	if step != 5 {
		t.Fatalf("migrated tutorial step = %d, want 5", step)
	}
	incompleteSnapshot, err := s.Snapshot(ctx, incomplete.ID)
	if err != nil {
		t.Fatal(err)
	}
	if incompleteSnapshot.TutorialComplete || len(incompleteSnapshot.Tutorial) != 1 || incompleteSnapshot.Tutorial[0].TutorialID != "1000" || incompleteSnapshot.Tutorial[0].Step != 100 {
		t.Fatalf("migration changed incomplete tutorial state: %+v", incompleteSnapshot.Tutorial)
	}
}

func TestGlobalPackRuleMigrationPreservesNewestPlayerRule(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pack-rule.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	player, _, err := s.EnsurePlayer(ctx, "device-fixture")
	if err != nil {
		t.Fatal(err)
	}
	seed := int64(421337)
	want := PackRule{Mode: "illegal", ReturnPackCount: 2, FreeOpenings: true, FixedSeed: &seed, Packs: [][]PackRuleSlot{{{CardID: "PK_10_000010_00", Locked: true}}}}
	if err := s.SavePackRule(ctx, player.ID, want); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM global_pack_rule; DELETE FROM schema_migrations WHERE version=3`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GlobalPackRule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != want.Mode || got.ReturnPackCount != 2 || !got.FreeOpenings || got.FixedSeed == nil || *got.FixedSeed != seed || len(got.Packs) != 1 || got.Packs[0][0].CardID != want.Packs[0][0].CardID {
		t.Fatalf("migrated global rule=%+v", got)
	}
}

func TestGlobalPackRulePersistsIllegalGeneratorSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "illegal-rule.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	seed := int64(9876)
	want := PackRule{
		Mode: "illegal", ReturnPackCount: 40, FreeOpenings: true, FixedSeed: &seed,
		Illegal: IllegalPackRule{
			CardCount: 10, AllowDuplicates: true,
			ExpansionIDs: []string{"A1", "A2"}, Rarities: []int{1, 4}, CardKinds: []string{"pokemon"},
		},
	}
	if err := s.SaveGlobalPackRule(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GlobalPackRule(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != want.Mode || got.ReturnPackCount != want.ReturnPackCount || got.Illegal.CardCount != want.Illegal.CardCount || !got.Illegal.AllowDuplicates || !equalStrings(got.Illegal.ExpansionIDs, want.Illegal.ExpansionIDs) || !equalInts(got.Illegal.Rarities, want.Illegal.Rarities) || !equalStrings(got.Illegal.CardKinds, want.Illegal.CardKinds) {
		t.Fatalf("persisted illegal rule=%+v, want %+v", got, want)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalInts(left, right []int) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
