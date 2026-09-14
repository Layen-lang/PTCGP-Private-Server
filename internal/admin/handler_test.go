package admin

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/packlab"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/testfixture"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/traffic"
)

type fakeLauncher struct{ calls int }

func (f *fakeLauncher) Open(context.Context) error { f.calls++; return nil }

func newFixture(t *testing.T) (*Handler, *player.Manager, *store.Store) {
	t.Helper()
	state, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	catalogs, err := catalog.OpenRegistry(testfixture.MasterData(t), catalog.DefaultLocale, catalog.FallbackLocale)
	if err != nil {
		t.Fatal(err)
	}
	master := catalogs.Default()
	manager := player.New(state, master)
	handler, err := New(manager, &fakeLauncher{}, slog.New(slog.NewTextHandler(io.Discard, nil)), WithCatalogs(catalogs), WithPackLab(packlab.New(state, master)))
	if err != nil {
		t.Fatal(err)
	}
	return handler, manager, state
}

func TestCatalogEndpointsUseRequestedDisplayLocale(t *testing.T) {
	t.Parallel()
	handler, _, state := newFixture(t)
	defer state.Close()

	french := request(handler, http.MethodGet, "/api/catalog/cards?q=PK_10_000010_00", "", "", "")
	if french.Code != http.StatusOK || !strings.Contains(french.Body.String(), `"Name":"Bulbizarre"`) {
		t.Fatalf("French catalog status=%d body=%s", french.Code, french.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/catalog/cards?q=PK_10_000010_00", nil)
	req.Host = "127.0.0.1:8080"
	req.Header.Set("X-PTCGP-Locale", "en_US")
	english := httptest.NewRecorder()
	handler.ServeHTTP(english, req)
	if english.Code != http.StatusOK || !strings.Contains(english.Body.String(), `"Name":"Bulbasaur"`) {
		t.Fatalf("English catalog status=%d body=%s", english.Code, english.Body.String())
	}
}

func TestTrafficEndpointReturnsRecorderTimeline(t *testing.T) {
	t.Parallel()
	handler, _, state := newFixture(t)
	defer state.Close()
	recorder, err := traffic.Open(filepath.Join(t.TempDir(), "traffic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	handler.traffic = recorder
	recorder.Record(traffic.Entry{Protocol: "grpc", Method: "/System/LoginV1", Status: "OK", StartedAt: time.Now()})

	response := request(handler, http.MethodGet, "/api/traffic?limit=10", "", "", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"method":"/System/LoginV1"`) {
		t.Fatalf("traffic status=%d body=%s", response.Code, response.Body.String())
	}
	invalid := request(handler, http.MethodGet, "/api/traffic?limit=501", "", "", "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid traffic limit status=%d, want %d", invalid.Code, http.StatusBadRequest)
	}
}

func TestTrafficStreamPushesRecordedEvent(t *testing.T) {
	handler, _, state := newFixture(t)
	defer state.Close()
	recorder, err := traffic.Open(filepath.Join(t.TempDir(), "traffic.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	handler.traffic = recorder

	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/traffic/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream status=%d content-type=%q", response.StatusCode, response.Header.Get("Content-Type"))
	}

	recorder.Record(traffic.Entry{Protocol: "grpc", Method: "/System/LoginV1", Status: "OK"})
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), `"method":"/System/LoginV1"`) {
			return
		}
	}
	t.Fatalf("traffic stream ended before event: %v", scanner.Err())
}

func request(handler *Handler, method, path, body, origin, fetchSite string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:8080"+path, strings.NewReader(body))
	req.Host = "127.0.0.1:8080"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		req.Header.Set("X-CSRF-Token", handler.csrf)
		req.AddCookie(&http.Cookie{Name: "ptcgp_csrf", Value: handler.csrf})
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestReactApplicationAndCSRFProtection(t *testing.T) {
	t.Parallel()
	handler, _, state := newFixture(t)
	defer state.Close()

	page := request(handler, http.MethodGet, "/accounts", "", "", "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "PTCGP") {
		t.Fatalf("page status=%d body=%s", page.Code, page.Body.String())
	}
	bootstrap := request(handler, http.MethodGet, "/api/bootstrap", "", "", "")
	if bootstrap.Code != http.StatusOK || !strings.Contains(bootstrap.Body.String(), `"csrfToken"`) {
		t.Fatalf("bootstrap status=%d body=%s", bootstrap.Code, bootstrap.Body.String())
	}

	mutation := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8080/api/players", strings.NewReader(`{"DisplayName":"Test","Level":1,"Experience":0}`))
	mutation.Host = "127.0.0.1:8080"
	mutation.Header.Set("Content-Type", "application/json")
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, mutation)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("missing-CSRF status=%d", denied.Code)
	}
}

func TestJSONMutationsValidateCatalogAndGlobalPackRule(t *testing.T) {
	t.Parallel()
	handler, manager, state := newFixture(t)
	defer state.Close()
	ctx := context.Background()
	first, _, err := state.EnsurePlayer(ctx, "android")
	if err != nil {
		t.Fatal(err)
	}

	created := request(handler, http.MethodPost, "/api/players", `{"DisplayName":"Second","Language":"en_US","Country":"US","Level":7,"Experience":1175,"TutorialComplete":true}`, "", "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	players, err := manager.Players(ctx)
	if err != nil || len(players) != 2 {
		t.Fatalf("players=%+v err=%v", players, err)
	}

	invalid := request(handler, http.MethodPut, "/api/players/"+first.ID+"/cards/UNKNOWN_CARD", `{"Quantity":2}`, "", "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid stock=%d body=%s", invalid.Code, invalid.Body.String())
	}
	profile, err := manager.Profile(ctx, first.ID)
	if err != nil || len(profile.Cards) != 0 {
		t.Fatalf("unknown card persisted: %+v err=%v", profile.Cards, err)
	}

	invalidRule := request(handler, http.MethodPut, "/api/packs/rule", `{"Mode":"rarity","TargetPackID":"UNKNOWN_PACK","Rarity":"UR","ReturnPackCount":1}`, "", "")
	if invalidRule.Code != http.StatusBadRequest {
		t.Fatalf("invalid rule=%d body=%s", invalidRule.Code, invalidRule.Body.String())
	}
	validRule := request(handler, http.MethodPut, "/api/packs/rule", `{"Mode":"illegal","TargetPackID":"","ReturnPackCount":2,"FreeOpenings":true,"Illegal":{"CardCount":10,"AllowDuplicates":true,"ExpansionIDs":[],"Rarities":[],"CardKinds":[]}}`, "", "")
	if validRule.Code != http.StatusOK {
		t.Fatalf("valid rule=%d body=%s", validRule.Code, validRule.Body.String())
	}
	rule, err := handler.packs.Rule(ctx)
	if err != nil || rule.Mode != "illegal" || rule.ReturnPackCount != 2 || rule.Illegal.CardCount != 10 || !rule.Illegal.AllowDuplicates {
		t.Fatalf("stored global pack rule=%+v err=%v", rule, err)
	}
	pool := request(handler, http.MethodPost, "/api/packs/pool", `{"CardCount":10,"ExpansionIDs":[],"Rarities":[],"CardKinds":[]}`, "", "")
	if pool.Code != http.StatusOK || !strings.Contains(pool.Body.String(), `"Count":`) {
		t.Fatalf("illegal pool=%d body=%s", pool.Code, pool.Body.String())
	}
}

func TestMutationOriginNormalization(t *testing.T) {
	t.Parallel()
	handler, _, state := newFixture(t)
	defer state.Close()

	tests := []struct {
		name, origin, fetchSite string
		want                    int
	}{
		{name: "canonical loopback", origin: "http://127.0.0.1:8080", want: http.StatusCreated},
		{name: "loopback with trailing slash", origin: "http://127.0.0.1:8080/", want: http.StatusCreated},
		{name: "chromium opaque same origin", origin: "null", fetchSite: "same-origin", want: http.StatusCreated},
		{name: "opaque cross site", origin: "null", fetchSite: "cross-site", want: http.StatusForbidden},
		{name: "different local port", origin: "http://127.0.0.1:8081", want: http.StatusForbidden},
		{name: "remote origin", origin: "https://example.invalid", want: http.StatusForbidden},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"DisplayName":"Test %d","Language":"fr_FR","Country":"FR","Level":1,"Experience":0,"TutorialComplete":true}`, index)
			response := request(handler, http.MethodPost, "/api/players", body, test.origin, test.fetchSite)
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestAccountEditorCatalogsAndLifecycleEndpoints(t *testing.T) {
	t.Parallel()
	handler, manager, state := newFixture(t)
	defer state.Close()
	ctx := context.Background()
	created := request(handler, http.MethodPost, "/api/players", `{"DisplayName":"Source","Language":"fr_FR","Country":"FR","Level":1,"Experience":0,"TutorialComplete":false,"Preset":"empty"}`, "", "")
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	players, err := manager.Players(ctx)
	if err != nil || len(players) != 1 {
		t.Fatalf("players=%+v err=%v", players, err)
	}
	duplicate := request(handler, http.MethodPost, "/api/players/"+players[0].ID+"/duplicate", "", "", "")
	if duplicate.Code != http.StatusCreated {
		t.Fatalf("duplicate status=%d body=%s", duplicate.Code, duplicate.Body.String())
	}
	players, _ = manager.Players(ctx)
	orderBody := fmt.Sprintf(`{"PlayerIDs":[%q,%q]}`, players[1].ID, players[0].ID)
	reordered := request(handler, http.MethodPut, "/api/players/order", orderBody, "", "")
	if reordered.Code != http.StatusOK {
		t.Fatalf("reorder status=%d body=%s", reordered.Code, reordered.Body.String())
	}
	page := request(handler, http.MethodGet, "/api/catalog/card-browser?page=1", "", "", "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `"PageSize":100`) || !strings.Contains(page.Body.String(), `"Expansions"`) {
		t.Fatalf("card page status=%d body=%s", page.Code, page.Body.String())
	}
	resources := request(handler, http.MethodGet, "/api/catalog/resources", "", "", "")
	if resources.Code != http.StatusOK || !strings.Contains(resources.Body.String(), "REVIVAL_CLOCK_100010") || !strings.Contains(resources.Body.String(), "TRADE_CHAGER_140010") {
		t.Fatalf("resources status=%d body=%s", resources.Code, resources.Body.String())
	}
	stock := request(handler, http.MethodPut, "/api/players/"+players[1].ID+"/cards/PK_10_000010_00", `{"Quantity":2}`, "", "")
	if stock.Code != http.StatusOK || !strings.Contains(stock.Body.String(), `"Quantity":2`) || strings.Contains(stock.Body.String(), `"Player"`) {
		t.Fatalf("compact stock response status=%d body=%s", stock.Code, stock.Body.String())
	}
	summary := request(handler, http.MethodGet, "/api/players/"+players[1].ID+"/summary", "", "", "")
	if summary.Code != http.StatusOK || !strings.Contains(summary.Body.String(), `"CardCount":1`) || strings.Contains(summary.Body.String(), `"Cards"`) {
		t.Fatalf("profile summary status=%d body=%s", summary.Code, summary.Body.String())
	}
	inventory := request(handler, http.MethodGet, "/api/players/"+players[1].ID+"/inventory/cards", "", "", "")
	if inventory.Code != http.StatusOK || !strings.Contains(inventory.Body.String(), `"ID":"PK_10_000010_00"`) || strings.Contains(inventory.Body.String(), `"ImageURL"`) {
		t.Fatalf("compact inventory status=%d body=%s", inventory.Code, inventory.Body.String())
	}
	englishStock := request(handler, http.MethodPut, "/api/players/"+players[1].ID+"/cards/PK_10_000010_00", `{"Quantity":7,"Language":2}`, "", "")
	if englishStock.Code != http.StatusOK || !strings.Contains(englishStock.Body.String(), `"Quantity":7`) || !strings.Contains(englishStock.Body.String(), `"TotalQuantity":9`) {
		t.Fatalf("English stock status=%d body=%s", englishStock.Code, englishStock.Body.String())
	}
	englishInventory := request(handler, http.MethodGet, "/api/players/"+players[1].ID+"/inventory/cards?language=2", "", "", "")
	if englishInventory.Code != http.StatusOK || !strings.Contains(englishInventory.Body.String(), `"Quantity":7`) || !strings.Contains(englishInventory.Body.String(), `"TotalQuantity":9`) {
		t.Fatalf("English inventory status=%d body=%s", englishInventory.Code, englishInventory.Body.String())
	}
	emptyBattle := request(handler, http.MethodGet, "/api/players/"+players[1].ID+"/inventory/battle", "", "", "")
	if emptyBattle.Code != http.StatusOK || strings.TrimSpace(emptyBattle.Body.String()) != "[]" {
		t.Fatalf("empty battle inventory must be an array: status=%d body=%s", emptyBattle.Code, emptyBattle.Body.String())
	}
	deleted := request(handler, http.MethodDelete, "/api/players/"+players[0].ID, "", "", "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", deleted.Code, deleted.Body.String())
	}
}

func TestAccountEditorSelectsOwnedEmblemsInDisplayOrder(t *testing.T) {
	t.Parallel()
	handler, manager, state := newFixture(t)
	defer state.Close()
	ctx := context.Background()

	var emblemIDs []string
	for _, cosmetic := range manager.Catalog().Cosmetics {
		if cosmetic.Kind == catalog.CosmeticProfileDecoration && cosmetic.Variant == 1 {
			emblemIDs = append(emblemIDs, cosmetic.ID)
			if len(emblemIDs) == 4 {
				break
			}
		}
	}
	if len(emblemIDs) < 4 {
		t.Fatalf("emblem catalog contains only %d entries", len(emblemIDs))
	}
	owned := map[string]int64{emblemIDs[0]: 1, emblemIDs[1]: 1, emblemIDs[2]: 1}
	created, err := state.CreatePlayerWithInventory(ctx, "Insignes", 12, 2605, true, store.InitialInventory{ProfileDecorations: owned})
	if err != nil {
		t.Fatal(err)
	}

	catalogResponse := request(handler, http.MethodGet, "/api/catalog/profile-emblems", "", "", "")
	if catalogResponse.Code != http.StatusOK || !strings.Contains(catalogResponse.Body.String(), emblemIDs[0]) {
		t.Fatalf("emblem catalog status=%d body=%s", catalogResponse.Code, catalogResponse.Body.String())
	}
	inventory := request(handler, http.MethodGet, "/api/players/"+created.ID+"/inventory/emblems", "", "", "")
	if inventory.Code != http.StatusOK || !strings.Contains(inventory.Body.String(), emblemIDs[2]) {
		t.Fatalf("emblem inventory status=%d body=%s", inventory.Code, inventory.Body.String())
	}

	updateBody := fmt.Sprintf(`{"DisplayName":"Insignes","Language":"en_US","Country":"US","Level":12,"Experience":2605,"TutorialComplete":true,"IconID":"","EmblemIDs":[%q,%q,%q]}`, emblemIDs[2], emblemIDs[0], emblemIDs[1])
	updated := request(handler, http.MethodPatch, "/api/players/"+created.ID, updateBody, "", "")
	if updated.Code != http.StatusOK {
		t.Fatalf("emblem update status=%d body=%s", updated.Code, updated.Body.String())
	}
	profile, err := manager.Profile(ctx, created.ID)
	if err != nil || len(profile.EmblemIDs) != 3 || profile.EmblemIDs[0] != emblemIDs[2] || profile.EmblemIDs[1] != emblemIDs[0] || profile.EmblemIDs[2] != emblemIDs[1] {
		t.Fatalf("selected emblems=%v err=%v", profile.EmblemIDs, err)
	}

	invalidBody := fmt.Sprintf(`{"DisplayName":"Insignes","Language":"en_US","Country":"US","Level":12,"Experience":2605,"TutorialComplete":true,"IconID":"","EmblemIDs":[%q]}`, emblemIDs[3])
	invalid := request(handler, http.MethodPatch, "/api/players/"+created.ID, invalidBody, "", "")
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("unowned emblem status=%d body=%s", invalid.Code, invalid.Body.String())
	}
}
