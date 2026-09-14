// Package restapi emulates the small REST surface used during client startup.
package restapi

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/protocol"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
)

const maxBodyBytes = 1 << 20

// Handler serves BaaS, analytics, wallet, locale, and word-filter contracts.
type Handler struct {
	profile protocol.Profile
	store   *store.Store
	logger  *slog.Logger
	now     func() time.Time

	signingOnce sync.Once
	signingKey  *rsa.PrivateKey
	signingKid  string
	signingErr  error
}

// New returns a REST handler backed by local identity state.
func New(state *store.Store, logger *slog.Logger, profile protocol.Profile) *Handler {
	return &Handler{profile: profile, store: state, logger: logger, now: time.Now}
}

// ServeHTTP routes only explicitly supported startup contracts.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tracked := &responseStatusWriter{ResponseWriter: w, status: http.StatusOK}
	w = tracked
	defer func() {
		h.logger.InfoContext(r.Context(), "REST response", "method", r.Method, "path", r.URL.Path, "status", tracked.status)
	}()
	h.logger.InfoContext(r.Context(), "REST request", "method", r.Method, "host", r.Host, "path", r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/core/v1/gateway/sdk/login":
		h.login(w, r)
	case r.Method == http.MethodPost && r.URL.Path == "/core/v1/gateway/sdk/federation":
		h.login(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/core/v1/certificates":
		h.jwks(w, r)
	case r.Method == http.MethodGet && r.URL.Path == "/bigdata/v1/analytics/events/config":
		h.analyticsConfig(w)
	case r.Method == http.MethodPost && r.URL.Path == "/bigdata/v1/analytics/events":
		w.WriteHeader(http.StatusAccepted)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/wallets/split"):
		h.wallets(w, r)
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/transaction_histories/split"):
		// The Unity SDK probes this VCM collection while reconciling purchases.
		// There is no remote billing service in local mode, so an empty history
		// is the truthful successful response and avoids a client-side startup
		// error caused by the old 404 fallback.
		writeJSON(w, http.StatusOK, []any{})
	case r.Method == http.MethodGet && r.URL.Path == "/vcm/v1/markets/GOOGLE/bundles":
		// Google bundle reconciliation is intentionally empty in local mode.
		writeJSON(w, http.StatusOK, []any{})
	case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/markets/GOOGLE/purchases"):
		writeJSON(w, http.StatusOK, []any{})
	case r.Method == http.MethodPut && isPushChannelPath(r.URL.Path):
		// Nintendo's Unity SDK uses a completion-only callback for this PUT;
		// a successful empty response is sufficient for the local no-op channel.
		writeJSON(w, http.StatusNoContent, nil)
	case r.Method == http.MethodPost && r.URL.Path == "/api/location/v1/estimate_country":
		writeJSON(w, http.StatusOK, map[string]string{"country": "FR"})
	case r.Method == http.MethodPost && r.URL.Path == "/api/badword/v1/check_word":
		writeJSON(w, http.StatusOK, map[string]any{"results": []any{}})
	case r.Method == http.MethodGet && r.URL.Path == "/healthz":
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "appVersion": h.profile.AppVersion})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unsupported local endpoint"})
	}
}

func isPushChannelPath(path string) bool {
	const prefix = "/notification/v1/push_channels/"
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(path, prefix), "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != ""
}

func (h *Handler) wallets(w http.ResponseWriter, r *http.Request) {
	const prefix = "/vcm/v1/users/"
	const suffix = "/wallets/split"
	if !strings.HasPrefix(r.URL.Path, prefix) || !strings.HasSuffix(r.URL.Path, suffix) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unsupported local endpoint"})
		return
	}
	userID := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, prefix), suffix)
	if userID == "" || strings.Contains(userID, "/") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid local user"})
		return
	}

	wallets := make([]map[string]any, 0, 6)
	for _, entry := range [][2]string{
		{"gold-free", "GOOGLE"},
		{"gold-free", "APPLE"},
		{"gold-free", "WEB"},
		{"gold-paid", "WEB"},
		{"gold-paid", "APPLE"},
		{"gold-paid", "GOOGLE"},
	} {
		currency, market := entry[0], entry[1]
		wallets = append(wallets, map[string]any{
			"balance": map[string]any{
				"free":  0,
				"paid":  []any{},
				"total": 0,
			},
			"market":              market,
			"remittedBalances":    []any{},
			"userId":              userID,
			"virtualCurrencyName": currency,
		})
	}
	writeJSON(w, http.StatusOK, wallets)
}

type responseStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *responseStatusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

type deviceAccountCredentials struct {
	ID       string `json:"id"`
	Password string `json:"password"`
}

type loginRequest struct {
	DeviceAccount *deviceAccountCredentials `json:"deviceAccount"`
	AppVersion    string                    `json:"appVersion"`
	SDKVersion    string                    `json:"sdkVersion"`
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer body.Close()
	var request loginRequest
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	h.logger.InfoContext(r.Context(), "BaaS login contract", "app_version", request.AppVersion, "sdk_version", request.SDKVersion, "has_device", request.DeviceAccount != nil)
	if request.AppVersion != "" && request.AppVersion != h.profile.AppVersion {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "unsupported app version"})
		return
	}
	if request.SDKVersion != "" && request.SDKVersion != h.profile.BaaSSDKVersion {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "unsupported SDK version"})
		return
	}
	createdDevice := request.DeviceAccount == nil
	credentials := request.DeviceAccount
	var createdDeviceAccount any
	if createdDevice {
		deviceID, err := hexToken(8)
		if err != nil {
			h.loginFailure(w, r, err)
			return
		}
		devicePassword, err := hexToken(20)
		if err != nil {
			h.loginFailure(w, r, err)
			return
		}
		credentials = &deviceAccountCredentials{ID: strings.ToUpper(deviceID), Password: devicePassword}
		createdDeviceAccount = credentials
	}
	if credentials == nil || !validOpaque(credentials.ID, 16) || !validOpaque(credentials.Password, 40) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid device credentials"})
		return
	}
	if err := h.store.RegisterOrValidateDevice(r.Context(), credentials.ID, credentials.Password); err != nil {
		if errors.Is(err, store.ErrInvalidDeviceCredentials) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid device credentials"})
			return
		}
		h.loginFailure(w, r, err)
		return
	}
	deviceCreatedAt, err := h.store.DeviceCreatedAt(r.Context(), credentials.ID)
	if err != nil {
		h.loginFailure(w, r, err)
		return
	}
	now := h.now().UTC()
	baasUserID := store.BaaSUserID(credentials.ID)
	idJTI, err := uuidToken()
	if err != nil {
		h.loginFailure(w, r, err)
		return
	}
	accessJTI, err := uuidToken()
	if err != nil {
		h.loginFailure(w, r, err)
		return
	}
	idToken, err := h.localJWT(map[string]any{
		"aud":                "96cceb7c988bf040",
		"sub":                baasUserID,
		"iss":                "https://" + h.profile.BaaSHost,
		"typ":                "id_token",
		"exp":                now.Add(time.Hour).Unix(),
		"iat":                now.Unix(),
		"bs:did":             credentials.ID,
		"jti":                idJTI,
		"bs:user_created_at": deviceCreatedAt.Unix(),
	}, true)
	if err != nil {
		h.loginFailure(w, r, err)
		return
	}
	accessToken, err := h.localJWT(map[string]any{
		"sub":    baasUserID,
		"aud":    "96cceb7c988bf040",
		"iss":    "https://" + h.profile.BaaSHost,
		"typ":    "token",
		"bs:grt": 2,
		"exp":    now.Add(15 * time.Minute).Unix(),
		"bs:nac": nil,
		"iat":    now.Unix(),
		"bs:did": credentials.ID,
		"jti":    accessJTI,
	}, false)
	if err != nil {
		h.loginFailure(w, r, err)
		return
	}
	var sessionID any
	if createdDevice {
		sessionID, err = opaqueToken(22)
		if err != nil {
			h.loginFailure(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"idToken":              idToken,
		"accessToken":          accessToken,
		"createdDeviceAccount": createdDeviceAccount,
		"sessionId":            sessionID,
		"error":                nil,
		"expiresIn":            900,
		"market":               nil,
		"capability": map[string]any{
			"accountHost":           "accounts.nintendo.com",
			"accountApiHost":        "api.accounts.nintendo.com",
			"pointProgramHost":      "my.nintendo.com",
			"sessionUpdateInterval": 180000,
		},
		"behaviorSettings": map[string]any{},
		"user": map[string]any{
			"id":             baasUserID,
			"nickname":       "",
			"country":        "",
			"birthday":       "2000-01-01",
			"gender":         "unknown",
			"deviceAccounts": []map[string]string{{"id": credentials.ID}},
			"links":          map[string]any{},
			"permissions": map[string]any{
				"personalAnalytics":             true,
				"personalAnalyticsUpdatedAt":    now.Unix(),
				"personalNotification":          true,
				"personalNotificationUpdatedAt": now.Unix(),
			},
			"createdAt":          deviceCreatedAt.Unix(),
			"updatedAt":          now.Unix(),
			"hasUnreadCsComment": false,
		},
	})
}

func (h *Handler) loginFailure(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.ErrorContext(r.Context(), "REST login failed", "error", err)
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "local identity unavailable"})
}

func (h *Handler) analyticsConfig(w http.ResponseWriter) {
	writeJSON(w, http.StatusOK, map[string]any{
		"accessToken":        "local-analytics",
		"applicationId":      "ptcgp-local",
		"city":               "",
		"country":            "FR",
		"expirationTime":     h.now().UTC().Add(24 * time.Hour).Unix(),
		"immediateReporting": false,
		"mode":               "disabled",
		"region":             "local",
		"reportingPeriod":    86400,
		"topic":              "disabled",
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.WriteHeader(status)
	if status == http.StatusNoContent {
		return
	}
	if err := json.NewEncoder(w).Encode(value); err != nil {
		return
	}
}

func opaqueToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate opaque token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func hexToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate hexadecimal token: %w", err)
	}
	return fmt.Sprintf("%x", data), nil
}

func uuidToken() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate UUID: %w", err)
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", data[0:4], data[4:6], data[6:8], data[8:10], data[10:16]), nil
}

func validOpaque(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func (h *Handler) ensureSigningKey() (*rsa.PrivateKey, string, error) {
	h.signingOnce.Do(func() {
		h.signingKey, h.signingErr = rsa.GenerateKey(rand.Reader, 2048)
		if h.signingErr != nil {
			return
		}
		digest := sha256.Sum256(h.signingKey.PublicKey.N.Bytes())
		digest[6] = (digest[6] & 0x0f) | 0x40
		digest[8] = (digest[8] & 0x3f) | 0x80
		h.signingKid = fmt.Sprintf("%x-%x-%x-%x-%x", digest[0:4], digest[4:6], digest[6:8], digest[8:10], digest[10:16])
	})
	return h.signingKey, h.signingKid, h.signingErr
}

func (h *Handler) localJWT(claims map[string]any, includeJKU bool) (string, error) {
	key, kid, err := h.ensureSigningKey()
	if err != nil {
		return "", fmt.Errorf("initialize local JWT signer: %w", err)
	}
	headerValue := map[string]string{"alg": "RS256", "kid": kid}
	if includeJKU {
		headerValue["jku"] = "https://" + h.profile.BaaSHost + "/core/v1/certificates"
	}
	header, err := json.Marshal(headerValue)
	if err != nil {
		return "", fmt.Errorf("marshal JWT header: %w", err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal JWT claims: %w", err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign local JWT: %w", err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (h *Handler) jwks(w http.ResponseWriter, r *http.Request) {
	key, kid, err := h.ensureSigningKey()
	if err != nil {
		h.loginFailure(w, r, err)
		return
	}
	exponent := big.NewInt(int64(key.PublicKey.E)).Bytes()
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"alg": "RS256",
		"e":   base64.RawURLEncoding.EncodeToString(exponent),
		"kid": kid,
		"kty": "RSA",
		"n":   base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes()),
		"use": "sig",
	}}})
}
