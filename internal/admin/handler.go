// Package admin exposes the loopback-only JSON administration API and serves
// the separately built React application.
package admin

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/assets"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/gamelocale"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/packlab"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/player"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/traffic"
)

const maxBodyBytes = 1 << 20

//go:embed dist
var frontend embed.FS

// Frontend returns the embedded administration application. The lightweight
// control panel serves the same files while proxying data requests to the
// private server process.
func Frontend() (fs.FS, error) {
	return fs.Sub(frontend, "dist")
}

type Handler struct {
	catalogVersion string
	catalogs       *catalog.Registry
	players        *player.Manager
	packs          *packlab.Engine
	images         *assets.Resolver
	launcher       player.GameLauncher
	logger         *slog.Logger
	traffic        *traffic.Recorder
	trafficHistory string
	csrf           string
	static         http.Handler
}

type Option func(*Handler)

// WithCatalogVersion labels the catalog with the selected release version.
func WithCatalogVersion(version string) Option {
	return func(h *Handler) { h.catalogVersion = version }
}

// WithCatalogs makes localized master-data views available to administration.
// Mutations and game behavior continue to use the player's canonical catalog.
func WithCatalogs(catalogs *catalog.Registry) Option {
	return func(h *Handler) { h.catalogs = catalogs }
}

func WithPackLab(packs *packlab.Engine) Option  { return func(h *Handler) { h.packs = packs } }
func WithImages(images *assets.Resolver) Option { return func(h *Handler) { h.images = images } }

// WithTraffic exposes the sanitized recorder timeline to loopback administration.
func WithTraffic(recorder *traffic.Recorder) Option {
	return func(h *Handler) { h.traffic = recorder }
}

// WithTrafficHistory exposes saved exchanges when the game server is stopped.
func WithTrafficHistory(path string) Option { return func(h *Handler) { h.trafficHistory = path } }

func New(players *player.Manager, launcher player.GameLauncher, logger *slog.Logger, options ...Option) (*Handler, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	dist, err := Frontend()
	if err != nil {
		return nil, fmt.Errorf("open embedded admin frontend: %w", err)
	}
	handler := &Handler{players: players, launcher: launcher, logger: logger, csrf: token, static: http.FileServer(http.FS(dist))}
	for _, option := range options {
		option(handler)
	}
	return handler, nil
}

func (h *Handler) displayCatalog(r *http.Request) player.CatalogView {
	if h.catalogs == nil {
		return h.players.Catalog()
	}
	selected := h.catalogs.For(r.Header.Get("X-PTCGP-Locale"))
	return player.CatalogView{
		Cards: selected.Cards(), Expansions: selected.Expansions(), Items: selected.Items(),
		Currencies: selected.Currencies(), Levels: selected.Levels(), Cosmetics: selected.Cosmetics(),
		Messages: selected.ProfileMessages(), Packs: selected.Packs(),
	}
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !loopbackHost(r.Host) {
		h.writeError(w, http.StatusForbidden, "Administration disponible uniquement sur 127.0.0.1.")
		return
	}
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")

	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/assets/image/") && h.images != nil {
		h.images.ServeHTTP(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/api/") {
		h.serveAPI(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		h.writeError(w, http.StatusMethodNotAllowed, "Méthode non autorisée.")
		return
	}
	if !strings.Contains(strings.TrimPrefix(r.URL.Path, "/"), ".") {
		clone := r.Clone(r.Context())
		clone.URL.Path = "/"
		h.static.ServeHTTP(w, clone)
		return
	}
	h.static.ServeHTTP(w, r)
}

func (h *Handler) serveAPI(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/"), "/")
	parts := strings.Split(path, "/")
	switch {
	case r.Method == http.MethodGet && path == "bootstrap":
		h.bootstrap(w, r)
	case r.Method == http.MethodGet && path == "catalog/cards":
		h.searchCards(w, r)
	case r.Method == http.MethodGet && path == "catalog/items":
		h.searchItems(w, r)
	case r.Method == http.MethodGet && path == "catalog/card-browser":
		h.cardBrowser(w, r)
	case r.Method == http.MethodGet && path == "catalog/resources":
		h.resourceCatalog(w, r)
	case r.Method == http.MethodGet && path == "catalog/profile-icons":
		h.profileIcons(w, r)
	case r.Method == http.MethodGet && path == "catalog/profile-emblems":
		h.profileEmblems(w, r)
	case r.Method == http.MethodGet && path == "catalog/peripherals":
		h.peripheralCatalog(w, r)
	case r.Method == http.MethodGet && path == "packs":
		h.packStudio(w, r)
	case r.Method == http.MethodPost && path == "packs/pool":
		if h.validateMutation(w, r) {
			h.packPool(w, r)
		}
	case r.Method == http.MethodGet && path == "traffic":
		h.trafficEvents(w, r)
	case r.Method == http.MethodGet && path == "traffic/stream":
		h.trafficStream(w, r)
	case r.Method == http.MethodPut && path == "packs/rule":
		if h.validateMutation(w, r) {
			h.savePackRule(w, r)
		}
	case len(parts) == 1 && parts[0] == "players" && r.Method == http.MethodPost:
		if h.validateMutation(w, r) {
			h.createPlayer(w, r)
		}
	case len(parts) == 2 && parts[0] == "players" && r.Method == http.MethodGet:
		h.profile(w, r, parts[1])
	case len(parts) == 3 && parts[0] == "players" && parts[2] == "summary" && r.Method == http.MethodGet:
		h.profileSummary(w, r, parts[1])
	case len(parts) == 4 && parts[0] == "players" && parts[2] == "inventory" && r.Method == http.MethodGet:
		h.profileInventory(w, r, parts[1], parts[3])
	case len(parts) == 2 && parts[0] == "players" && parts[1] == "order" && r.Method == http.MethodPut:
		if h.validateMutation(w, r) {
			h.reorderPlayers(w, r)
		}
	case len(parts) == 2 && parts[0] == "players" && r.Method == http.MethodPatch:
		if h.validateMutation(w, r) {
			h.updateProfile(w, r, parts[1])
		}
	case len(parts) == 3 && parts[0] == "players" && parts[2] == "select" && r.Method == http.MethodPost:
		if h.validateMutation(w, r) {
			h.playerAction(w, r, parts[1], false)
		}
	case len(parts) == 3 && parts[0] == "players" && parts[2] == "open" && r.Method == http.MethodPost:
		if h.validateMutation(w, r) {
			h.playerAction(w, r, parts[1], true)
		}
	case len(parts) == 3 && parts[0] == "players" && parts[2] == "duplicate" && r.Method == http.MethodPost:
		if h.validateMutation(w, r) {
			h.duplicatePlayer(w, r, parts[1])
		}
	case len(parts) == 2 && parts[0] == "players" && r.Method == http.MethodDelete:
		if h.validateMutation(w, r) {
			h.deletePlayer(w, r, parts[1])
		}
	case len(parts) == 4 && parts[0] == "players" && r.Method == http.MethodPut:
		if h.validateMutation(w, r) {
			h.updateStock(w, r, parts[1], parts[2], parts[3])
		}
	default:
		h.writeError(w, http.StatusNotFound, "Route API locale inconnue.")
	}
}

func (h *Handler) trafficEvents(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 500 {
			h.writeError(w, http.StatusBadRequest, "La limite du trafic doit être comprise entre 1 et 500.")
			return
		}
		limit = parsed
	}
	recorder := h.traffic
	if recorder == nil && h.trafficHistory != "" {
		var err error
		recorder, err = traffic.ReadHistory(h.trafficHistory)
		if err != nil {
			h.fail(w, r, err)
			return
		}
	}
	if recorder == nil {
		h.writeJSON(w, http.StatusOK, []traffic.Event{})
		return
	}
	after := strings.TrimSpace(r.URL.Query().Get("after"))
	if after != "" {
		h.writeJSON(w, http.StatusOK, recorder.After(after, limit))
		return
	}
	h.writeJSON(w, http.StatusOK, recorder.Recent(limit))
}

func (h *Handler) trafficStream(w http.ResponseWriter, r *http.Request) {
	if h.traffic == nil {
		h.writeError(w, http.StatusServiceUnavailable, "Le flux de trafic n'est pas disponible.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		h.writeError(w, http.StatusInternalServerError, "Le direct n'est pas pris en charge par ce serveur.")
		return
	}

	after := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if after == "" {
		after = strings.TrimSpace(r.URL.Query().Get("after"))
	}
	updates, unsubscribe := h.traffic.Subscribe()
	defer unsubscribe()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("X-Accel-Buffering", "no")
	_, _ = io.WriteString(w, "retry: 1000\n\n")
	flusher.Flush()

	sendAvailable := func() error {
		events := h.traffic.After(after, 500)
		for index := len(events) - 1; index >= 0; index-- {
			event := events[index]
			encoded, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "id: %s\nevent: traffic\ndata: %s\n\n", event.ID, encoded); err != nil {
				return err
			}
			after = event.ID
		}
		if len(events) > 0 {
			flusher.Flush()
		}
		return nil
	}
	if err := sendAvailable(); err != nil {
		return
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case _, open := <-updates:
			if !open || sendAvailable() != nil {
				return
			}
		case <-heartbeat.C:
			if _, err := io.WriteString(w, ": keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

type playerSummary struct {
	ID, DisplayName          string
	Level                    int
	Experience, StateVersion int64
	Active, Authorized       bool
}

type currencyResponse struct {
	ID, Name, Description, ImageURL string
}

func (h *Handler) bootstrap(w http.ResponseWriter, r *http.Request) {
	h.setCSRFCookie(w)
	players, err := h.players.Players(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	var device *store.Device
	if value, err := h.players.Device(r.Context()); err == nil {
		device = &value
	}
	result := make([]playerSummary, 0, len(players))
	for _, value := range players {
		result = append(result, playerSummary{
			ID: value.ID, DisplayName: value.DisplayName, Level: value.Level,
			Experience: value.Experience, StateVersion: value.StateVersion,
			Active:     device != nil && device.ActivePlayerID == value.ID,
			Authorized: device != nil && device.AuthorizedPlayerID == value.ID,
		})
	}
	currencies := h.displayCatalog(r).Currencies
	currencyResults := make([]currencyResponse, 0, len(currencies))
	for _, value := range currencies {
		currencyResults = append(currencyResults, currencyResponse{
			ID: value.ID, Name: value.Name, Description: value.Description,
			ImageURL: h.imageURL(value.AssetID, 96),
		})
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"csrfToken": h.csrf, "players": result, "device": device, "catalogVersion": h.catalogVersion, "currencies": currencyResults})
}

type profileResponse struct {
	Player             store.Player
	Settings           store.Settings
	ProfileImageURL    string
	TutorialComplete   bool
	RequiredExperience int64
	Cards              []stockResponse
	Currencies         []stockResponse
	Items              []stockResponse
}

type profileSummaryResponse struct {
	Player             store.Player
	Settings           store.Settings
	ProfileImageURL    string
	TutorialComplete   bool
	RequiredExperience int64
	CardCount          int
	EmblemIDs          []string
}

type stockResponse struct {
	ID, Name, Kind string
	Rarity         int
	Quantity       int64
	ImageURL       string
}

type stockAmount struct {
	ID            string
	Quantity      int64
	TotalQuantity int64 `json:",omitempty"`
}

func (h *Handler) profileSummary(w http.ResponseWriter, r *http.Request, playerID string) {
	value, err := h.players.ProfileSummary(r.Context(), playerID)
	if err != nil {
		h.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, profileSummaryResponse{
		Player: value.Player, Settings: value.Settings, ProfileImageURL: h.imageURL(value.Settings.IconID, 160),
		TutorialComplete: value.TutorialComplete, RequiredExperience: value.RequiredExperience, CardCount: value.CardCount,
		EmblemIDs: append([]string(nil), value.EmblemIDs...),
	})
}

func (h *Handler) profileInventory(w http.ResponseWriter, r *http.Request, playerID, kind string) {
	value, err := h.players.Profile(r.Context(), playerID)
	if err != nil {
		h.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	result := make([]stockAmount, 0)
	switch kind {
	case "cards":
		cardLanguage, _ := strconv.Atoi(r.URL.Query().Get("language"))
		if cardLanguage == 0 {
			cardLanguage = 4
		}
		if cardLanguage < 1 || cardLanguage > 12 {
			h.writeError(w, http.StatusBadRequest, "Langue de carte inconnue.")
			return
		}
		result = make([]stockAmount, 0, len(value.Cards))
		for _, item := range value.Cards {
			var languageQuantity int64
			for _, amount := range item.Languages {
				if amount.Language == int32(cardLanguage) {
					languageQuantity = amount.Quantity
					break
				}
			}
			result = append(result, stockAmount{ID: item.Definition.ID, Quantity: languageQuantity, TotalQuantity: item.Quantity})
		}
	case "resources":
		result = make([]stockAmount, 0, len(value.Currencies)+len(value.Items))
		for _, item := range value.Currencies {
			result = append(result, stockAmount{ID: item.Definition.ID, Quantity: item.Quantity})
		}
		for _, item := range value.Items {
			if item.Definition.Kind != catalog.ItemPeripheral {
				result = append(result, stockAmount{ID: item.Definition.ID, Quantity: item.Quantity})
			}
		}
	case "emblems":
		for _, item := range value.ProfileDecorations {
			if item.Definition.Variant == 1 {
				result = append(result, stockAmount{ID: item.Definition.ID, Quantity: item.Quantity})
			}
		}
	case "battle", "showcase":
		for _, item := range value.Items {
			if item.Definition.Kind != catalog.ItemPeripheral || (kind == "battle" && item.Definition.Variant > 2) || (kind == "showcase" && item.Definition.Variant < 3) {
				continue
			}
			result = append(result, stockAmount{ID: item.Definition.ID, Quantity: item.Quantity})
		}
	default:
		h.writeError(w, http.StatusNotFound, "Section d’inventaire inconnue.")
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) profile(w http.ResponseWriter, r *http.Request, playerID string) {
	value, err := h.players.Profile(r.Context(), playerID)
	if err != nil {
		h.writeError(w, http.StatusNotFound, err.Error())
		return
	}
	names := displayNames(h.displayCatalog(r))
	response := profileResponse{Player: value.Player, Settings: value.Settings, ProfileImageURL: h.imageURL(value.Settings.IconID, 160), TutorialComplete: value.TutorialComplete, RequiredExperience: value.RequiredExperience}
	for _, item := range value.Cards {
		response.Cards = append(response.Cards, stockResponse{ID: item.Definition.ID, Name: localizedName(names, item.Definition.ID, item.Definition.Name), Kind: string(item.Definition.Kind), Rarity: item.Definition.Rarity, Quantity: item.Quantity, ImageURL: h.imageURL(item.Definition.ID, 120)})
	}
	for _, item := range value.Currencies {
		response.Currencies = append(response.Currencies, stockResponse{ID: item.Definition.ID, Name: localizedName(names, item.Definition.ID, item.Definition.Name), Quantity: item.Quantity})
	}
	for _, item := range value.Items {
		imageID := item.Definition.AssetID
		if imageID == "" {
			imageID = item.Definition.ID
		}
		response.Items = append(response.Items, stockResponse{ID: item.Definition.ID, Name: localizedName(names, item.Definition.ID, item.Definition.Name), Kind: string(item.Definition.Kind), Quantity: item.Quantity, ImageURL: h.imageURL(imageID, 120)})
	}
	h.writeJSON(w, http.StatusOK, response)
}

func displayNames(view player.CatalogView) map[string]string {
	names := make(map[string]string, len(view.Cards)+len(view.Currencies)+len(view.Items))
	for _, value := range view.Cards {
		names[value.ID] = value.Name
	}
	for _, value := range view.Currencies {
		names[value.ID] = value.Name
	}
	for _, value := range view.Items {
		names[value.ID] = value.Name
	}
	return names
}

func localizedName(names map[string]string, id, fallback string) string {
	if value := names[id]; value != "" {
		return value
	}
	return fallback
}

func (h *Handler) createPlayer(w http.ResponseWriter, r *http.Request) {
	var input player.CreateInput
	if !h.decodeJSON(w, r, &input) {
		return
	}
	created, err := h.players.Create(r.Context(), input)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request, playerID string) {
	var input struct {
		DisplayName      string
		Language         string
		Country          string
		Level            int
		Experience       int64
		TutorialComplete bool
		IconID           string
		EmblemIDs        *[]string
	}
	if !h.decodeJSON(w, r, &input) {
		return
	}
	canonicalLanguage, validLanguage := gamelocale.NormalizeLocale(input.Language)
	if !validLanguage {
		h.writeError(w, http.StatusBadRequest, "unsupported game language")
		return
	}
	canonicalCountry, validCountry := gamelocale.NormalizeCountry(input.Country)
	if !validCountry {
		h.writeError(w, http.StatusBadRequest, "country must be a two-letter region code")
		return
	}
	err := h.players.Apply(r.Context(), playerID, player.Change{DisplayName: &input.DisplayName, Level: &input.Level, Experience: &input.Experience, TutorialComplete: &input.TutorialComplete})
	if err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.players.UpdatePlayerLocale(r.Context(), playerID, canonicalLanguage, canonicalCountry); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := h.players.SaveProfileSummary(r.Context(), playerID, input.DisplayName, input.IconID, ""); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.EmblemIDs != nil {
		if err := h.players.SaveEmblems(r.Context(), playerID, *input.EmblemIDs); err != nil {
			h.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	h.profileSummary(w, r, playerID)
}

func (h *Handler) duplicatePlayer(w http.ResponseWriter, r *http.Request, playerID string) {
	created, err := h.players.Duplicate(r.Context(), playerID)
	if err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.writeJSON(w, http.StatusCreated, created)
}

func (h *Handler) deletePlayer(w http.ResponseWriter, r *http.Request, playerID string) {
	if err := h.players.Delete(r.Context(), playerID); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) reorderPlayers(w http.ResponseWriter, r *http.Request) {
	var input struct{ PlayerIDs []string }
	if !h.decodeJSON(w, r, &input) {
		return
	}
	if err := h.players.Reorder(r.Context(), input.PlayerIDs); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) updateStock(w http.ResponseWriter, r *http.Request, playerID, kind, catalogID string) {
	var input struct {
		Quantity int64
		Language int32
	}
	if !h.decodeJSON(w, r, &input) {
		return
	}
	change := player.Change{}
	switch kind {
	case "cards":
		if input.Language == 0 {
			input.Language = 4
		}
		total, err := h.players.SetCardLanguage(r.Context(), playerID, catalogID, input.Language, input.Quantity)
		if err != nil {
			h.writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		h.writeJSON(w, http.StatusOK, stockAmount{ID: catalogID, Quantity: input.Quantity, TotalQuantity: total})
		return
	case "items":
		change.Items = map[string]int64{catalogID: input.Quantity}
	case "currencies":
		change.Currencies = map[string]int64{catalogID: input.Quantity}
	default:
		h.writeError(w, http.StatusNotFound, "Type d’inventaire inconnu.")
		return
	}
	if err := h.players.Apply(r.Context(), playerID, change); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, stockAmount{ID: catalogID, Quantity: input.Quantity})
}

func (h *Handler) playerAction(w http.ResponseWriter, r *http.Request, playerID string, open bool) {
	var err error
	if open {
		err = h.players.OpenInGame(r.Context(), playerID, h.launcher)
	} else {
		err = h.players.SelectActive(r.Context(), playerID)
	}
	if err != nil {
		h.writeError(w, http.StatusConflict, err.Error())
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type catalogResult struct {
	ID, Name, Kind, Meta, ImageURL string
	Rarity                         int
}

type catalogPage struct {
	Items             []catalogResult
	Expansions        []map[string]string
	Rarities          []int
	Page, PageSize    int
	Total, TotalPages int
}

func (h *Handler) cardBrowser(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	expansionID := strings.TrimSpace(r.URL.Query().Get("expansion"))
	rarity, _ := strconv.Atoi(r.URL.Query().Get("rarity"))
	catalogView := h.displayCatalog(r)
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize := 100
	var matches []catalog.Card
	for _, value := range catalogView.Cards {
		if expansionID != "" && !contains(value.ExpansionIDs, expansionID) {
			continue
		}
		if rarity > 0 && value.Rarity != rarity {
			continue
		}
		haystack := strings.ToLower(value.Name + " " + value.ID + " " + strings.Join(value.ExpansionIDs, " "))
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		matches = append(matches, value)
	}
	start := (page - 1) * pageSize
	if start > len(matches) {
		start = len(matches)
	}
	end := min(start+pageSize, len(matches))
	result := catalogPage{Page: page, PageSize: pageSize, Total: len(matches), TotalPages: max(1, (len(matches)+pageSize-1)/pageSize)}
	for _, value := range matches[start:end] {
		result.Items = append(result.Items, catalogResult{ID: value.ID, Name: value.Name, Kind: string(value.Kind), Meta: strings.Join(value.ExpansionIDs, " · "), Rarity: value.Rarity, ImageURL: h.imageURL(value.ID, 160)})
	}
	raritySet := map[int]bool{}
	for _, value := range catalogView.Cards {
		raritySet[value.Rarity] = true
	}
	for value := range raritySet {
		result.Rarities = append(result.Rarities, value)
	}
	for i := 1; i < len(result.Rarities); i++ {
		for j := i; j > 0 && result.Rarities[j] < result.Rarities[j-1]; j-- {
			result.Rarities[j], result.Rarities[j-1] = result.Rarities[j-1], result.Rarities[j]
		}
	}
	for _, value := range catalogView.Expansions {
		result.Expansions = append(result.Expansions, map[string]string{"ID": value.ID, "Name": value.Name})
	}
	h.writeJSON(w, http.StatusOK, result)
}

type visualCatalogItem struct {
	ID, Name, Description, Kind, StockKind, ImageURL string
	Variant                                          int
}

func (h *Handler) resourceCatalog(w http.ResponseWriter, r *http.Request) {
	var result []visualCatalogItem
	for _, value := range h.displayCatalog(r).Currencies {
		result = append(result, visualCatalogItem{ID: value.ID, Name: value.Name, Description: value.Description, Kind: "currency", StockKind: "currencies", ImageURL: h.imageURL(value.AssetID, 96)})
	}
	for _, value := range h.displayCatalog(r).Items {
		if value.Kind == catalog.ItemPeripheral {
			continue
		}
		imageID := value.AssetID
		if imageID == "" {
			imageID = value.ID
		}
		result = append(result, visualCatalogItem{ID: value.ID, Name: value.Name, Description: value.Description, Kind: string(value.Kind), StockKind: "items", ImageURL: h.imageURL(imageID, 96), Variant: value.Variant})
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) profileIcons(w http.ResponseWriter, r *http.Request) {
	var result []visualCatalogItem
	for _, value := range h.displayCatalog(r).Cosmetics {
		if value.Kind != catalog.CosmeticProfileDecoration || value.Variant != 0 {
			continue
		}
		result = append(result, visualCatalogItem{ID: value.ID, Name: value.Name, Description: value.Description, Kind: string(value.Kind), ImageURL: h.imageURL(value.ID, 120)})
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) profileEmblems(w http.ResponseWriter, r *http.Request) {
	var result []visualCatalogItem
	for _, value := range h.displayCatalog(r).Cosmetics {
		if value.Kind != catalog.CosmeticProfileDecoration || value.Variant != 1 {
			continue
		}
		result = append(result, visualCatalogItem{ID: value.ID, Name: value.Name, Description: value.Description, Kind: string(value.Kind), ImageURL: h.imageURL(value.ID, 140), Variant: value.Variant})
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) peripheralCatalog(w http.ResponseWriter, r *http.Request) {
	group := r.URL.Query().Get("group")
	var result []visualCatalogItem
	for _, value := range h.displayCatalog(r).Items {
		if value.Kind != catalog.ItemPeripheral || (group == "battle" && value.Variant > 2) || (group == "showcase" && value.Variant < 3) {
			continue
		}
		imageID := value.AssetID
		if imageID == "" {
			imageID = value.ID
		}
		result = append(result, visualCatalogItem{ID: value.ID, Name: value.Name, Description: value.Description, Kind: string(value.Kind), StockKind: "items", ImageURL: h.imageURL(imageID, 140), Variant: value.Variant})
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) searchCards(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	packID := strings.TrimSpace(r.URL.Query().Get("pack"))
	expansionID := ""
	if packID != "" {
		for _, value := range h.displayCatalog(r).Packs {
			if value.ID == packID {
				expansionID = value.ExpansionID
				break
			}
		}
	}
	limit := queryLimit(r, 36)
	result := make([]catalogResult, 0, limit)
	for _, value := range h.displayCatalog(r).Cards {
		if expansionID != "" && !contains(value.ExpansionIDs, expansionID) {
			continue
		}
		haystack := strings.ToLower(value.Name + " " + value.ID + " " + strings.Join(value.ExpansionIDs, " "))
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		result = append(result, catalogResult{ID: value.ID, Name: value.Name, Kind: string(value.Kind), Meta: strings.Join(value.ExpansionIDs, " · "), Rarity: value.Rarity, ImageURL: h.imageURL(value.ID, 160)})
		if len(result) == limit {
			break
		}
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) searchItems(w http.ResponseWriter, r *http.Request) {
	query := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	limit := queryLimit(r, 36)
	result := make([]catalogResult, 0, limit)
	for _, value := range h.displayCatalog(r).Items {
		haystack := strings.ToLower(value.Name + " " + value.ID + " " + string(value.Kind))
		if query != "" && !strings.Contains(haystack, query) {
			continue
		}
		imageID := value.AssetID
		if imageID == "" {
			imageID = value.ID
		}
		result = append(result, catalogResult{ID: value.ID, Name: value.Name, Kind: string(value.Kind), Meta: string(value.Kind), ImageURL: h.imageURL(imageID, 120)})
		if len(result) == limit {
			break
		}
	}
	h.writeJSON(w, http.StatusOK, result)
}

type packResponse struct {
	ID, Name, Description, ExpansionID, ImageURL string
	TableKinds                                   []string
	Rarities                                     []string
}

type packFilterExpansion struct {
	ID, Name string
}

type packFilterOptions struct {
	Expansions []packFilterExpansion
	Rarities   []packlab.RarityOption
	CardKinds  []string
}

func (h *Handler) packStudio(w http.ResponseWriter, r *http.Request) {
	if h.packs == nil {
		h.writeError(w, http.StatusServiceUnavailable, "Studio boosters indisponible.")
		return
	}
	rule, err := h.packs.Rule(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	catalogView := h.displayCatalog(r)
	var packs []packResponse
	for _, value := range catalogView.Packs {
		kinds, rarities := map[string]bool{}, map[string]bool{}
		for _, table := range value.Tables {
			kinds[table.Kind] = true
			for _, slot := range table.Slots {
				for _, pool := range slot.Pools {
					rarities[pool.Rarity] = true
				}
			}
		}
		packs = append(packs, packResponse{ID: value.ID, Name: value.Name, Description: value.Description, ExpansionID: value.ExpansionID, ImageURL: h.imageURL(value.AssetID, 220), TableKinds: sortedKeys(kinds), Rarities: sortedKeys(rarities)})
	}
	expansionIDs, cardKinds := map[string]bool{}, map[string]bool{}
	for _, card := range catalogView.Cards {
		for _, expansionID := range card.ExpansionIDs {
			expansionIDs[expansionID] = true
		}
		cardKinds[string(card.Kind)] = true
	}
	filters := packFilterOptions{Rarities: h.packs.IllegalRarities(), CardKinds: sortedKeys(cardKinds)}
	for _, expansion := range catalogView.Expansions {
		if expansionIDs[expansion.ID] {
			filters.Expansions = append(filters.Expansions, packFilterExpansion{ID: expansion.ID, Name: expansion.Name})
		}
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"rule": rule, "packs": packs, "filters": filters})
}

func (h *Handler) packPool(w http.ResponseWriter, r *http.Request) {
	if h.packs == nil {
		h.writeError(w, http.StatusServiceUnavailable, "Studio boosters indisponible.")
		return
	}
	var rule store.IllegalPackRule
	if !h.decodeJSON(w, r, &rule) {
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]int{"Count": h.packs.IllegalPoolSize(rule)})
}

func (h *Handler) savePackRule(w http.ResponseWriter, r *http.Request) {
	if h.packs == nil {
		h.writeError(w, http.StatusServiceUnavailable, "Studio boosters indisponible.")
		return
	}
	var rule store.PackRule
	if !h.decodeJSON(w, r, &rule) {
		return
	}
	if err := h.packs.SaveRule(r.Context(), rule); err != nil {
		h.writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := h.packs.Rule(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, saved)
}

func (h *Handler) validateMutation(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin != "" && !sameOrigin(origin, r.Host, r.Header.Get("Sec-Fetch-Site")) {
		h.writeError(w, http.StatusForbidden, "Origine refusée.")
		return false
	}
	cookie, err := r.Cookie("ptcgp_csrf")
	if err != nil || !sameToken(cookie.Value, r.Header.Get("X-CSRF-Token")) || !sameToken(cookie.Value, h.csrf) {
		h.writeError(w, http.StatusForbidden, "Jeton CSRF invalide.")
		return false
	}
	return true
}

func (h *Handler) decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		h.writeError(w, http.StatusBadRequest, "Corps JSON invalide : "+err.Error())
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		h.writeError(w, http.StatusBadRequest, "Le corps JSON doit contenir un seul objet.")
		return false
	}
	return true
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		h.logger.Error("encode admin response", "error", err)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{"error": message})
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	h.logger.ErrorContext(r.Context(), "admin API", "error", err)
	h.writeError(w, http.StatusInternalServerError, "Administration locale indisponible.")
}

func (h *Handler) imageURL(identifier string, width int) string {
	if h.images == nil {
		return ""
	}
	return h.images.ThumbnailURL(identifier, width)
}

func (h *Handler) setCSRFCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "ptcgp_csrf", Value: h.csrf, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

func sameOrigin(raw, host, fetchSite string) bool {
	if raw == "null" {
		return strings.EqualFold(fetchSite, "same-origin")
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return false
	}
	return strings.EqualFold(parsed.Host, host) && (parsed.Path == "" || parsed.Path == "/")
}

func loopbackHost(hostport string) bool {
	host := hostport
	if parsed, _, err := net.SplitHostPort(hostport); err == nil {
		host = parsed
	}
	return strings.EqualFold(host, "127.0.0.1")
}

func sameToken(a, b string) bool {
	return len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func queryLimit(r *http.Request, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || value < 1 || value > 60 {
		return fallback
	}
	return value
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	for i := 1; i < len(result); i++ {
		for j := i; j > 0 && result[j] < result[j-1]; j-- {
			result[j], result[j-1] = result[j-1], result[j]
		}
	}
	return result
}

func randomToken() (string, error) {
	data := make([]byte, 32)
	if _, err := rand.Read(data); err != nil {
		return "", fmt.Errorf("generate CSRF token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
