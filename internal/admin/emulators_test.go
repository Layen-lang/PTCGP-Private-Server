package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
)

func TestBootstrapKeepsActiveAccountScopedToSelectedEmulator(t *testing.T) {
	h, manager, state := newFixture(t)
	defer state.Close()
	ctx := context.Background()
	ids := make([]string, 0, 2)
	for _, serial := range []string{"MuMu", "LD9"} {
		p, err := manager.Create(ctx, player.CreateInput{DisplayName: serial, Language: "fr_FR", Country: "FR", Level: 1})
		if err != nil {
			t.Fatal(err)
		}
		if err := state.SelectEmulatorPlayer(ctx, serial, p.ID, true); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, p.ID)
	}
	for i, serial := range []string{"MuMu", "LD9"} {
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/bootstrap", nil).WithContext(player.WithEmulator(ctx, serial))
		response := httptest.NewRecorder()
		h.ServeHTTP(response, req)
		var result struct {
			Emulator string          `json:"emulator"`
			Players  []playerSummary `json:"players"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if response.Code != 200 || result.Emulator != serial {
			t.Fatalf("bootstrap = %d %s", response.Code, response.Body.String())
		}
		active := ""
		for _, p := range result.Players {
			if p.Active {
				active = p.ID
			}
			if p.InUseOn == "" {
				t.Fatalf("reservation missing for %s", p.ID)
			}
		}
		if active != ids[i] {
			t.Fatalf("%s active player = %s, want %s", serial, active, ids[i])
		}
	}
}
