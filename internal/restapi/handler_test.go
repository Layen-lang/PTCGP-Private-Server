package restapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/testprofile"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
)

func TestLoginContractAndStableDevice(t *testing.T) {
	t.Parallel()
	state, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	h := New(state, slog.New(slog.NewTextHandler(io.Discard, nil)), testprofile.Baseline())
	body := []byte(`{"deviceAccount":null,"appVersion":"1.7.2","sdkVersion":"Unity-3.13.1-c55a4366e"}`)
	var first map[string]any
	for attempt := 0; attempt < 2; attempt++ {
		req := httptest.NewRequest(http.MethodPost, "https://baas.local/core/v1/gateway/sdk/login", bytes.NewReader(body))
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("status = %d, body=%s", res.Code, res.Body.String())
		}
		var got map[string]any
		if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		if attempt == 0 {
			first = got
			if got["sessionId"] == nil || got["createdDeviceAccount"] == nil {
				t.Fatal("first login did not create device credentials and a session")
			}
			body, err = json.Marshal(map[string]any{
				"deviceAccount": got["createdDeviceAccount"],
				"appVersion":    "1.7.2",
				"sdkVersion":    "Unity-3.13.1-c55a4366e",
			})
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		if got["sessionId"] != nil || got["createdDeviceAccount"] != nil {
			t.Fatal("existing-device login must return null creation fields")
		}
		firstUser := first["user"].(map[string]any)
		gotUser := got["user"].(map[string]any)
		if firstUser["id"] != gotUser["id"] {
			t.Fatalf("player changed: %v != %v", firstUser["id"], gotUser["id"])
		}
	}
}

func TestLoginRejectsChangedDevicePassword(t *testing.T) {
	t.Parallel()
	state, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	h := New(state, slog.New(slog.NewTextHandler(io.Discard, nil)), testprofile.Baseline())
	credentials := map[string]string{"id": "0011223344556677", "password": strings.Repeat("a", 40)}
	request := func(password string) *httptest.ResponseRecorder {
		credentials["password"] = password
		body, marshalErr := json.Marshal(map[string]any{
			"deviceAccount": credentials,
			"appVersion":    "1.7.2",
			"sdkVersion":    "Unity-3.13.1-c55a4366e",
		})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodPost, "https://baas.local/core/v1/gateway/sdk/login", bytes.NewReader(body)))
		return res
	}
	if got := request(strings.Repeat("a", 40)).Code; got != http.StatusOK {
		t.Fatalf("initial status = %d", got)
	}
	if got := request(strings.Repeat("b", 40)).Code; got != http.StatusUnauthorized {
		t.Fatalf("changed-password status = %d, want %d", got, http.StatusUnauthorized)
	}
}

func TestWalletContractContainsAllZeroBalanceMarkets(t *testing.T) {
	t.Parallel()
	state, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	h := New(state, slog.New(slog.NewTextHandler(io.Discard, nil)), testprofile.Baseline())
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "https://baas.local/vcm/v1/users/fixture-user/wallets/split", nil))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", res.Code, res.Body.String())
	}
	var wallets []struct {
		Balance struct {
			Free  int   `json:"free"`
			Paid  []any `json:"paid"`
			Total int   `json:"total"`
		} `json:"balance"`
		Market              string `json:"market"`
		UserID              string `json:"userId"`
		VirtualCurrencyName string `json:"virtualCurrencyName"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &wallets); err != nil {
		t.Fatal(err)
	}
	if len(wallets) != 6 {
		t.Fatalf("wallet count = %d, want 6", len(wallets))
	}
	for _, wallet := range wallets {
		if wallet.UserID != "fixture-user" || wallet.Market == "" || wallet.VirtualCurrencyName == "" || wallet.Balance.Free != 0 || wallet.Balance.Total != 0 || wallet.Balance.Paid == nil {
			t.Fatalf("invalid zero-balance wallet: %+v", wallet)
		}
	}
}

func TestVCMPurchaseReconciliationContractsReturnEmptyArrays(t *testing.T) {
	t.Parallel()
	state, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	h := New(state, slog.New(slog.NewTextHandler(io.Discard, nil)), testprofile.Baseline())
	for _, path := range []string{
		"/vcm/v1/users/fixture-user/transaction_histories/split",
		"/vcm/v1/markets/GOOGLE/bundles",
	} {
		res := httptest.NewRecorder()
		h.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "https://baas.local"+path, nil))
		if res.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body=%s", path, res.Code, res.Body.String())
		}
		var values []any
		if err := json.Unmarshal(res.Body.Bytes(), &values); err != nil {
			t.Fatalf("%s response is not an array: %v", path, err)
		}
		if values == nil || len(values) != 0 {
			t.Fatalf("%s response = %#v, want a non-nil empty array", path, values)
		}
	}
}

func TestPurchaseSyncReturnsAnArray(t *testing.T) {
	t.Parallel()
	state, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	h := New(state, slog.New(slog.NewTextHandler(io.Discard, nil)), testprofile.Baseline())
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(http.MethodPut, "https://baas.local/subs/v1/users/fixture-user/markets/GOOGLE/purchases", strings.NewReader(`{"purchases":[]}`)))
	if res.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", res.Code, res.Body.String())
	}
	var purchases []any
	if err := json.Unmarshal(res.Body.Bytes(), &purchases); err != nil {
		t.Fatalf("purchase response is not an array: %v", err)
	}
	if purchases == nil || len(purchases) != 0 {
		t.Fatalf("purchase response = %#v, want a non-nil empty array", purchases)
	}
}

func TestPushChannelRegistrationIsANoOp(t *testing.T) {
	t.Parallel()
	state, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	h := New(state, slog.New(slog.NewTextHandler(io.Discard, nil)), testprofile.Baseline())
	res := httptest.NewRecorder()
	h.ServeHTTP(res, httptest.NewRequest(
		http.MethodPut,
		"https://baas.local/notification/v1/push_channels/fixture-user/fixture-device",
		strings.NewReader(`{"token":"local-no-op"}`),
	))
	if res.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d; body=%s", res.Code, http.StatusNoContent, res.Body.String())
	}
	if res.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty", res.Body.String())
	}
}

func TestSanitizedHARContracts(t *testing.T) {
	t.Parallel()
	state, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer state.Close()
	h := New(state, slog.New(slog.NewTextHandler(io.Discard, nil)), testprofile.Baseline())
	data, err := os.ReadFile(filepath.Join("testdata", "rest-contracts.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contracts []struct {
		Method        string   `json:"method"`
		Host          string   `json:"host"`
		Path          string   `json:"path"`
		RequestFields []string `json:"request_fields"`
		Status        int      `json:"status"`
	}
	if err := json.Unmarshal(data, &contracts); err != nil {
		t.Fatal(err)
	}
	for _, contract := range contracts {
		if contract.Host == "prod-game-assets-app-41283.akamaized.net" || contract.Host == "cdn-prod-assets-notice-app-41283.cdn-dena.com" {
			continue
		}
		body := make(map[string]any, len(contract.RequestFields))
		for _, field := range contract.RequestFields {
			body[field] = nil
		}
		bodyData, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		path := strings.ReplaceAll(contract.Path, "{user_id}", "fixture-user")
		req := httptest.NewRequest(contract.Method, "https://"+contract.Host+path, bytes.NewReader(bodyData))
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != contract.Status {
			t.Errorf("%s %s: status=%d want=%d body=%s", contract.Method, path, res.Code, contract.Status, res.Body.String())
		}
	}
}
