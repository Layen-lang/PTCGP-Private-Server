// Package player contains the shared business rules used by administration and
// Player API adapters.
package player

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Layen-lang/PTCGP-Private-Server/internal/catalog"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/gamelocale"
	"github.com/Layen-lang/PTCGP-Private-Server/internal/store"
)

const (
	sessionLifetime  = 24 * time.Hour
	nicknameCooldown = 30 * 24 * time.Hour
)

var ErrValidation = errors.New("invalid player change")

const (
	ActionGetCards                 int32 = 1
	ActionLogin                    int32 = 2
	ActionPVETry                   int32 = 5
	ActionPVEWon                   int32 = 6
	ActionPackOpened               int32 = 7
	ActionRentalDeckUse            int32 = 15
	ActionFriendAdded              int32 = 16
	ActionDeckCreated              int32 = 17
	ActionThankSent                int32 = 20
	ActionPackOpenTotal            int32 = 24
	ActionPackByExpansion          int32 = 25
	ActionCardGetTotal             int32 = 26
	ActionCardGetDexTotal          int32 = 27
	ActionCardGetRarityTotal       int32 = 28
	ActionCardGetRarityDexTotal    int32 = 29
	ActionCardGetTypeTotal         int32 = 30
	ActionCardGetTypeDexTotal      int32 = 31
	ActionTrainersCardGetTypeTotal int32 = 32
	ActionCardGetExpansionTotal    int32 = 38
	ActionCardGetExpansionDexTotal int32 = 39
	ActionBoardCreated             int32 = 55
	ActionGiveCardTotal            int32 = 57
)

type CreateInput struct {
	DisplayName      string
	Language         string
	Country          string
	Level            int
	Experience       int64
	TutorialComplete bool
	Preset           string
	CardQuantity     int64
}

type Change struct {
	DisplayName      *string
	Level            *int
	Experience       *int64
	TutorialComplete *bool
	Cards            map[string]int64
	Currencies       map[string]int64
	Items            map[string]int64
}

type CardExchangeInput struct {
	CatalogID       string
	Amount          int64
	ResourceCardIDs []string
}

type FeedChallengeResult struct {
	Entry  store.FeedEntry
	Card   catalog.Card
	State  store.ChallengeState
	Replay bool
}

type ShopPurchaseResult struct {
	Product catalog.ShopProduct
	Rewards []store.ShopInventoryChange
	Amount  int64
	Count   int64
	Replay  bool
}

type CatalogView struct {
	Cards      []catalog.Card
	Expansions []catalog.Expansion
	Items      []catalog.Item
	Currencies []catalog.Currency
	Levels     []catalog.Level
	Cosmetics  []catalog.Cosmetic
	Messages   []catalog.ProfileMessage
	Packs      []catalog.Pack
}

type OwnedCard struct {
	Definition                      catalog.Card
	Quantity                        int64
	Languages                       []store.CardLanguageStock
	FirstReceivedAt, LastReceivedAt time.Time
}
type OwnedCurrency struct {
	Definition catalog.Currency
	Quantity   int64
}
type OwnedItem struct {
	Definition catalog.Item
	Quantity   int64
	ObtainedAt time.Time
}
type OwnedCosmetic struct {
	Definition catalog.Cosmetic
	Quantity   int64
	ObtainedAt time.Time
}
type OwnedCardCosmetic struct {
	Card       catalog.Card
	Definition catalog.Cosmetic
	Quantity   int64
}
type Profile struct {
	Player             store.Player
	Settings           store.Settings
	Cards              []OwnedCard
	Currencies         []OwnedCurrency
	Items              []OwnedItem
	ProfileDecorations []OwnedCosmetic
	CardSkins          []OwnedCardCosmetic
	CardFrames         []OwnedCardCosmetic
	Tutorial           []store.TutorialStep
	TutorialComplete   bool
	RequiredExperience int64
	EmblemIDs          []string
	LevelHistories     []store.LevelHistory
}

// ProfileSummary is the small validated profile view used by administration
// before a specific inventory section is opened.
type ProfileSummary struct {
	Player             store.Player
	Settings           store.Settings
	TutorialComplete   bool
	RequiredExperience int64
	CardCount          int
	EmblemIDs          []string
}

type Authorization struct {
	Player  store.Player
	Profile Profile
	Token   string
	Created bool
}

type GameLauncher interface{ Open(context.Context) error }

// Manager is the deep module for profile lifecycle and catalog validation.
type Manager struct {
	state   *store.Store
	catalog *catalog.Catalog
}

func New(state *store.Store, master *catalog.Catalog) *Manager {
	return &Manager{state: state, catalog: master}
}

func (m *Manager) Catalog() CatalogView {
	return CatalogView{Cards: m.catalog.Cards(), Expansions: m.catalog.Expansions(), Items: m.catalog.Items(), Currencies: m.catalog.Currencies(), Levels: m.catalog.Levels(), Cosmetics: m.catalog.Cosmetics(), Messages: m.catalog.ProfileMessages(), Packs: m.catalog.Packs()}
}
func (m *Manager) HomeSettings() catalog.HomeSettings                  { return m.catalog.HomeSettings() }
func (m *Manager) Players(ctx context.Context) ([]store.Player, error) { return m.state.Players(ctx) }
func (m *Manager) ProfileSummary(ctx context.Context, playerID string) (ProfileSummary, error) {
	value, err := m.state.PlayerProfileSummary(ctx, playerID)
	if err != nil {
		return ProfileSummary{}, err
	}
	if _, err := m.catalog.Cosmetic(value.Settings.IconID); err != nil {
		return ProfileSummary{}, fmt.Errorf("validate profile icon: %w", err)
	}
	if _, err := m.catalog.ProfileMessage(value.Settings.MessageID); err != nil {
		return ProfileSummary{}, fmt.Errorf("validate profile message: %w", err)
	}
	result := ProfileSummary{
		Player: value.Player, Settings: value.Settings,
		TutorialComplete: hasCompletedTutorial(value.Tutorial, m.tutorialCompletions()), CardCount: value.CardCount,
	}
	result.EmblemIDs, err = m.state.SelectedEmblems(ctx, playerID)
	if err != nil {
		return ProfileSummary{}, fmt.Errorf("load selected emblems: %w", err)
	}
	result.RequiredExperience = value.Player.Experience
	return result, nil
}
func (m *Manager) Profile(ctx context.Context, playerID string) (Profile, error) {
	snapshot, err := m.state.Snapshot(ctx, playerID)
	if err != nil {
		return Profile{}, err
	}
	result := Profile{
		Player: snapshot.Player, Settings: snapshot.Settings, Tutorial: snapshot.Tutorial,
		TutorialComplete: hasCompletedTutorial(snapshot.Tutorial, m.tutorialCompletions()),
	}
	result.EmblemIDs, err = m.state.SelectedEmblems(ctx, playerID)
	if err != nil {
		return Profile{}, fmt.Errorf("load selected emblems: %w", err)
	}
	if _, err := m.catalog.Cosmetic(snapshot.Settings.IconID); err != nil {
		return Profile{}, fmt.Errorf("validate profile icon: %w", err)
	}
	if _, err := m.catalog.ProfileMessage(snapshot.Settings.MessageID); err != nil {
		return Profile{}, fmt.Errorf("validate profile message: %w", err)
	}
	result.RequiredExperience = snapshot.Player.Experience
	result.LevelHistories, err = m.state.LevelHistories(ctx, playerID)
	if err != nil {
		return Profile{}, fmt.Errorf("load level histories: %w", err)
	}
	for _, stock := range snapshot.Cards {
		definition, err := m.catalog.Card(stock.CardID)
		if err != nil {
			return Profile{}, fmt.Errorf("validate stored card: %w", err)
		}
		result.Cards = append(result.Cards, OwnedCard{Definition: definition, Quantity: stock.Quantity, Languages: append([]store.CardLanguageStock(nil), stock.Languages...), FirstReceivedAt: stock.FirstReceivedAt, LastReceivedAt: stock.LastReceivedAt})
	}
	for _, stock := range snapshot.Currencies {
		definition, err := m.catalog.Currency(stock.CurrencyID)
		if err != nil {
			return Profile{}, fmt.Errorf("validate stored currency: %w", err)
		}
		result.Currencies = append(result.Currencies, OwnedCurrency{Definition: definition, Quantity: stock.Quantity})
	}
	for _, stock := range snapshot.Items {
		// These two legacy kinds were briefly stored in player_items before they
		// received dedicated progression tables. Ignore them here so an upgraded
		// profile remains loadable; new grants use the dedicated tables below.
		if stock.Kind == "rental_deck" || stock.Kind == "theme_deck_recipe" {
			continue
		}
		definition, err := m.catalog.Item(stock.ItemID)
		if err != nil {
			return Profile{}, fmt.Errorf("validate stored item: %w", err)
		}
		if string(definition.Kind) != stock.Kind {
			return Profile{}, fmt.Errorf("validate stored item %q: kind %q does not match catalog %q", stock.ItemID, stock.Kind, definition.Kind)
		}
		result.Items = append(result.Items, OwnedItem{Definition: definition, Quantity: stock.Quantity, ObtainedAt: stock.ObtainedAt})
	}
	for _, stock := range snapshot.ProfileDecorations {
		definition, err := m.catalog.Cosmetic(stock.CosmeticID)
		if err != nil || definition.Kind != catalog.CosmeticProfileDecoration {
			return Profile{}, fmt.Errorf("validate stored profile decoration %q: %w", stock.CosmeticID, err)
		}
		result.ProfileDecorations = append(result.ProfileDecorations, OwnedCosmetic{Definition: definition, Quantity: stock.Quantity, ObtainedAt: stock.ObtainedAt})
	}
	for _, source := range []struct {
		stocks []store.CardCosmeticStock
		kind   catalog.CosmeticKind
		target *[]OwnedCardCosmetic
	}{{snapshot.CardSkins, catalog.CosmeticCardSkin, &result.CardSkins}, {snapshot.CardFrames, catalog.CosmeticCardFrame, &result.CardFrames}} {
		for _, stock := range source.stocks {
			card, err := m.catalog.Card(stock.CardID)
			if err != nil {
				return Profile{}, fmt.Errorf("validate stored card cosmetic card: %w", err)
			}
			definition, err := m.catalog.Cosmetic(stock.CosmeticID)
			if err != nil || definition.Kind != source.kind {
				return Profile{}, fmt.Errorf("validate stored card cosmetic %q: %w", stock.CosmeticID, err)
			}
			*source.target = append(*source.target, OwnedCardCosmetic{Card: card, Definition: definition, Quantity: stock.Quantity})
		}
	}
	return result, nil
}
func (m *Manager) Device(ctx context.Context) (store.Device, error) {
	return m.state.CurrentDevice(ctx)
}

func (m *Manager) Create(ctx context.Context, input CreateInput) (store.Player, error) {
	name, err := validateName(input.DisplayName)
	if err != nil {
		return store.Player{}, err
	}
	if err := m.validateProgress(input.Level, input.Experience); err != nil {
		return store.Player{}, err
	}
	language, ok := gamelocale.NormalizeLocale(input.Language)
	if !ok {
		return store.Player{}, validation("unsupported account language %q", input.Language)
	}
	country, ok := gamelocale.NormalizeCountry(input.Country)
	if !ok {
		return store.Player{}, validation("account region must be a two-letter country code")
	}
	var created store.Player
	switch input.Preset {
	case "", "empty":
		created, err = m.state.CreatePlayerWithSettings(ctx, name, input.Level, input.Experience, input.TutorialComplete, language, country)
	case "complete":
		quantity := input.CardQuantity
		if quantity == 0 {
			quantity = 2
		}
		if quantity < 1 || quantity > 999999 {
			return store.Player{}, validation("complete account card quantity must be between 1 and 999999")
		}
		created, err = m.state.CreatePlayerWithInventoryAndSettings(ctx, name, input.Level, input.Experience, true, m.completeInventory(quantity), language, country)
	default:
		return store.Player{}, validation("unknown account preset %q", input.Preset)
	}
	if err != nil {
		return store.Player{}, err
	}
	if input.TutorialComplete || input.Preset == "complete" {
		if err := m.state.SetTutorialCompletions(ctx, created.ID, m.tutorialCompletions()); err != nil {
			return store.Player{}, fmt.Errorf("complete tutorials for created player: %w", err)
		}
	}
	return created, nil
}

func (m *Manager) completeInventory(cardQuantity int64) store.InitialInventory {
	result := store.InitialInventory{
		Cards: map[string]int64{}, Currencies: map[string]int64{}, ProfileDecorations: map[string]int64{},
		PokeGold: 999_999_999,
	}
	for _, value := range m.catalog.Cards() {
		result.Cards[value.ID] = cardQuantity * 12
		for cardLanguage := int32(1); cardLanguage <= 12; cardLanguage++ {
			result.CardLanguages = append(result.CardLanguages, store.InitialCardLanguage{CardID: value.ID, Language: cardLanguage, Quantity: cardQuantity})
		}
	}
	for _, value := range m.catalog.Currencies() {
		result.Currencies[value.ID] = 999_999_999
	}
	for _, value := range m.catalog.Items() {
		quantity := int64(999_999)
		if value.Kind == catalog.ItemPeripheral {
			quantity = 1
		}
		result.Items = append(result.Items, store.InitialItem{ID: value.ID, Kind: string(value.Kind), Quantity: quantity})
	}
	for _, value := range m.catalog.Cosmetics() {
		if value.Kind == catalog.CosmeticProfileDecoration {
			result.ProfileDecorations[value.ID] = 1
		}
	}
	for _, value := range m.catalog.CardSkinCompatibilities() {
		result.CardSkins = append(result.CardSkins, store.CardCosmeticStock{CardID: value.CardID, CosmeticID: value.CosmeticID, Quantity: 2})
	}
	for _, value := range m.catalog.CardFrameCompatibilities() {
		result.CardFrames = append(result.CardFrames, store.CardCosmeticStock{CardID: value.CardID, CosmeticID: value.CosmeticID, Quantity: 2})
	}
	for _, value := range m.catalog.RentalDecks() {
		result.RentalDecks = append(result.RentalDecks, value.ID)
	}
	return result
}

func (m *Manager) SetCardLanguage(ctx context.Context, playerID, cardID string, language int32, quantity int64) (int64, error) {
	if quantity < 0 || quantity > 999999 {
		return 0, validation("card %q quantity must be between 0 and 999999", cardID)
	}
	if language < 1 || language > 12 {
		return 0, validation("card language must be between 1 and 12")
	}
	if _, err := m.catalog.Card(cardID); err != nil {
		return 0, validation("%v", err)
	}
	return m.state.SetCardLanguage(ctx, playerID, cardID, language, quantity)
}

func (m *Manager) Duplicate(ctx context.Context, playerID string) (store.Player, error) {
	current, err := m.state.Player(ctx, playerID)
	if err != nil {
		return store.Player{}, err
	}
	name := []rune("Copie " + current.DisplayName)
	if len(name) > 14 {
		name = name[:14]
	}
	return m.state.DuplicatePlayer(ctx, playerID, string(name))
}

func (m *Manager) Delete(ctx context.Context, playerID string) error {
	return m.state.DeletePlayer(ctx, playerID)
}

func (m *Manager) Reorder(ctx context.Context, playerIDs []string) error {
	return m.state.ReorderPlayers(ctx, playerIDs)
}

func (m *Manager) Apply(ctx context.Context, playerID string, change Change) error {
	current, err := m.state.Player(ctx, playerID)
	if err != nil {
		return err
	}
	name, level, experience := current.DisplayName, current.Level, current.Experience
	if change.DisplayName != nil {
		name, err = validateName(*change.DisplayName)
		if err != nil {
			return err
		}
	}
	if change.Level != nil {
		level = *change.Level
	}
	if change.Experience != nil {
		experience = *change.Experience
	}
	if err := m.validateProgress(level, experience); err != nil {
		return err
	}
	if name != current.DisplayName || level != current.Level || experience != current.Experience {
		if err := m.state.UpdatePlayer(ctx, playerID, name, level, experience); err != nil {
			return err
		}
	}
	if change.TutorialComplete != nil {
		var tutorialErr error
		if *change.TutorialComplete {
			tutorialErr = m.state.SetTutorialCompletions(ctx, playerID, m.tutorialCompletions())
		} else {
			tutorialErr = m.state.SetTutorialComplete(ctx, playerID, false)
		}
		if tutorialErr != nil {
			return tutorialErr
		}
	}
	for id, quantity := range change.Cards {
		if quantity < 0 || quantity > 999999 {
			return validation("card %q quantity must be between 0 and 999999", id)
		}
		if _, err := m.catalog.Card(id); err != nil {
			return validation("%v", err)
		}
		if err := m.state.SetCard(ctx, playerID, id, quantity); err != nil {
			return err
		}
	}
	for id, quantity := range change.Currencies {
		if quantity < 0 || quantity > 999999999 {
			return validation("currency %q quantity must be between 0 and 999999999", id)
		}
		if _, err := m.catalog.Currency(id); err != nil {
			return validation("%v", err)
		}
		if err := m.state.SetCurrency(ctx, playerID, id, quantity); err != nil {
			return err
		}
		if id == "POKEGOLD_FREE" || id == "POKEGOLD_PAID" {
			if err := m.state.SetPokeGold(ctx, playerID, quantity); err != nil {
				return err
			}
		}
	}
	for id, quantity := range change.Items {
		if quantity < 0 || quantity > 999999 {
			return validation("item %q quantity must be between 0 and 999999", id)
		}
		item, err := m.catalog.Item(id)
		if err != nil {
			return validation("%v", err)
		}
		if err := m.state.SetItem(ctx, playerID, string(item.Kind), id, quantity); err != nil {
			return err
		}
	}
	return nil
}

// CompleteTutorial records a completed client tutorial step for the player.
func (m *Manager) CompleteTutorial(ctx context.Context, playerID, tutorialID string, step int64) error {
	tutorialID = strings.TrimSpace(tutorialID)
	if tutorialID == "" {
		return validation("tutorial ID is required")
	}
	if step < 0 {
		return validation("tutorial step must be non-negative")
	}
	return m.state.RecordTutorialCompletion(ctx, playerID, tutorialID, step)
}

func (m *Manager) tutorialCompletions() []store.TutorialStep {
	values := m.catalog.TutorialCompletions()
	result := make([]store.TutorialStep, len(values))
	for i, value := range values {
		result[i] = store.TutorialStep{TutorialID: value.ID, Step: value.Step, Completed: true}
	}
	return result
}

func hasCompletedTutorial(progress, required []store.TutorialStep) bool {
	completed := make(map[string]int64, len(progress))
	for _, value := range progress {
		if value.Completed && value.Step > completed[value.TutorialID] {
			completed[value.TutorialID] = value.Step
		}
	}
	for _, value := range required {
		if completed[value.TutorialID] < value.Step {
			return false
		}
	}
	return len(required) > 0
}

func (m *Manager) SelectActive(ctx context.Context, playerID string) error {
	device, err := m.state.CurrentDevice(ctx)
	if errors.Is(err, store.ErrNotFound) || (err == nil && device.Account == "") {
		return m.state.SelectPending(ctx, playerID)
	}
	if err != nil {
		return fmt.Errorf("select active profile: %w", err)
	}
	return m.state.SelectActive(ctx, device.Account, playerID)
}

func (m *Manager) OpenInGame(ctx context.Context, playerID string, launcher GameLauncher) error {
	if launcher == nil {
		return fmt.Errorf("game launcher is unavailable")
	}
	if err := m.SelectActive(ctx, playerID); err != nil {
		return err
	}
	device, err := m.state.CurrentDevice(ctx)
	if err != nil {
		return err
	}
	if err := m.state.RevokeDeviceSessions(ctx, device.Account); err != nil {
		return err
	}
	if err := launcher.Open(ctx); err != nil {
		return fmt.Errorf("open selected profile in game: %w", err)
	}
	return nil
}

func (m *Manager) Authorize(ctx context.Context, deviceAccount string) (Authorization, error) {
	profiles, err := m.state.Players(ctx)
	if err != nil {
		return Authorization{}, err
	}
	if len(profiles) == 0 {
		return Authorization{}, store.ErrNotFound
	}
	p, created, err := m.state.EnsurePlayer(ctx, deviceAccount)
	if err != nil {
		return Authorization{}, err
	}
	token, err := m.state.NewDeviceSession(ctx, deviceAccount, p.ID, sessionLifetime)
	if err != nil {
		return Authorization{}, err
	}
	if err := m.state.RecordAuthorization(ctx, deviceAccount, p.ID); err != nil {
		return Authorization{}, err
	}
	profile, err := m.Profile(ctx, p.ID)
	if err != nil {
		return Authorization{}, err
	}
	p.DeviceAccount = deviceAccount
	profile.Player.DeviceAccount = deviceAccount
	return Authorization{Player: p, Profile: profile, Token: token, Created: created}, nil
}

func (m *Manager) PlayerBySession(ctx context.Context, token string) (store.Player, error) {
	return m.state.PlayerBySession(ctx, token)
}

func (m *Manager) AddAction(ctx context.Context, playerID string, key int32, target string, count int64, dated bool) error {
	return m.state.AddAction(ctx, playerID, key, target, count, dated)
}

func (m *Manager) ActionStates(ctx context.Context, playerID string, modifiedAfter time.Time, offset, limit int) ([]store.ActionState, bool, time.Time, error) {
	if err := m.projectInventoryActions(ctx, playerID); err != nil {
		return nil, false, time.Time{}, err
	}
	return m.state.ActionStates(ctx, playerID, modifiedAfter, offset, limit)
}

func (m *Manager) ComebackInfo(ctx context.Context, deviceAccount string) (store.ComebackInfo, error) {
	return m.state.ComebackInfo(ctx, deviceAccount)
}
func (m *Manager) StorageEntries(ctx context.Context, playerID string, keys []string) ([]store.StorageEntry, error) {
	return m.state.StorageEntries(ctx, playerID, keys)
}
func (m *Manager) SetStorageEntries(ctx context.Context, playerID string, entries []store.StorageEntry) error {
	return m.state.SetStorageEntries(ctx, playerID, entries)
}

func (m *Manager) SocialSnapshot(ctx context.Context, playerID string) (store.SocialSnapshot, error) {
	return m.state.SocialSnapshot(ctx, playerID)
}

func (m *Manager) SearchPlayers(ctx context.Context, playerID, friendID, name string) ([]store.Player, error) {
	return m.state.SearchPlayers(ctx, playerID, friendID, name)
}

func (m *Manager) SendFriendRequests(ctx context.Context, playerID string, receiverIDs []string) ([]string, error) {
	return m.state.SendFriendRequests(ctx, playerID, receiverIDs)
}

func (m *Manager) ApproveFriendRequest(ctx context.Context, playerID, senderID string) error {
	return m.state.ApproveFriendRequest(ctx, playerID, senderID)
}

func (m *Manager) RemoveFriendRequests(ctx context.Context, playerID string, otherIDs []string, sent bool) error {
	return m.state.RemoveFriendRequests(ctx, playerID, otherIDs, sent)
}

func (m *Manager) DeleteFriends(ctx context.Context, playerID string, otherIDs []string) error {
	return m.state.DeleteFriends(ctx, playerID, otherIDs)
}

func (m *Manager) SetFriendFavorite(ctx context.Context, playerID, friendID string, favorite bool) error {
	return m.state.SetFriendFavorite(ctx, playerID, friendID, favorite)
}

func (m *Manager) Decks(ctx context.Context, playerID string) ([]store.Deck, error) {
	return m.state.Decks(ctx, playerID)
}

func (m *Manager) SaveDeck(ctx context.Context, playerID string, deck store.Deck) (store.Deck, error) {
	deck.Name = strings.TrimSpace(deck.Name)
	if utf8.RuneCountInString(deck.Name) > 24 {
		return store.Deck{}, validation("deck name must contain at most 24 characters")
	}
	profile, err := m.Profile(ctx, playerID)
	if err != nil {
		return store.Deck{}, err
	}
	owned := make(map[string]int64, len(profile.Cards))
	for _, card := range profile.Cards {
		owned[card.Definition.ID] = card.Quantity
	}
	var total int64
	for cardID, quantity := range deck.Cards {
		if _, err := m.catalog.Card(cardID); err != nil {
			return store.Deck{}, validation("%v", err)
		}
		if quantity <= 0 || quantity > 2 {
			return store.Deck{}, validation("deck card %q must have a quantity between 1 and 2", cardID)
		}
		if owned[cardID] < quantity {
			return store.Deck{}, validation("deck requires %d copies of %q but the player owns %d", quantity, cardID, owned[cardID])
		}
		total += quantity
	}
	if total > 20 {
		return store.Deck{}, validation("deck cannot contain more than 20 cards")
	}
	return m.state.SaveDeck(ctx, playerID, deck)
}

func (m *Manager) DeleteDeck(ctx context.Context, playerID string, deckID int64) error {
	return m.state.DeleteDeck(ctx, playerID, deckID)
}

func (m *Manager) ChangeDeckOrder(ctx context.Context, playerID string, orders map[int64]int64) error {
	return m.state.ChangeDeckOrder(ctx, playerID, orders)
}

func (m *Manager) SharePackOpening(ctx context.Context, playerID, transactionID string) error {
	return m.state.SharePackOpening(ctx, playerID, transactionID)
}

func (m *Manager) StartSoloBattle(ctx context.Context, playerID, battleID string) (string, error) {
	return m.state.StartSoloBattle(ctx, playerID, battleID)
}

func (m *Manager) FinishSoloBattle(ctx context.Context, playerID, token string, result int32) (bool, error) {
	return m.state.FinishSoloBattle(ctx, playerID, token, result)
}

func (m *Manager) SoloBattleClearStates(ctx context.Context, playerID string, battleIDs []string) (map[string]bool, error) {
	return m.state.SoloBattleClearStates(ctx, playerID, battleIDs)
}

func (m *Manager) EventPower(ctx context.Context, playerID, eventID string) (store.EventPowerState, error) {
	return m.state.EventPower(ctx, playerID, eventID)
}

func (m *Manager) StartEventSoloBattle(ctx context.Context, playerID, eventID, battleID string) (string, store.EventPowerState, error) {
	return m.state.StartEventSoloBattle(ctx, playerID, eventID, battleID, 1)
}

func (m *Manager) HealEventPower(ctx context.Context, playerID, eventID string, chargers map[string]int64, pokeGoldAmount int64) (store.EventPowerState, error) {
	var chargerID string
	var chargerAmount int64
	for id, amount := range chargers {
		definition, err := m.catalog.Item(id)
		if err != nil || definition.Kind != catalog.ItemEventCharger || amount <= 0 || (chargerID != "" && chargerID != id) {
			return store.EventPowerState{}, validation("invalid event power charger %q", id)
		}
		chargerID, chargerAmount = id, chargerAmount+amount
	}
	return m.state.HealEventPower(ctx, playerID, eventID, chargerID, chargerAmount, pokeGoldAmount)
}

func (m *Manager) ExchangePackPoints(ctx context.Context, playerID, transactionID, groupID, cardID string, amount int64) (int64, bool, error) {
	card, err := m.catalog.Card(cardID)
	if err != nil {
		return 0, false, validation("unknown pack point card %q", cardID)
	}
	prices := map[int]int64{100: 35, 200: 70, 300: 150, 400: 500, 500: 400, 600: 1250, 700: 1250, 800: 1500, 830: 1000, 860: 1350, 900: 2500}
	price := prices[card.Rarity]
	if price == 0 {
		return 0, false, validation("card rarity cannot be exchanged")
	}
	return m.state.ExchangePackPoints(ctx, playerID, transactionID, groupID, cardID, amount, price)
}

func (m *Manager) ShopPurchaseCount(ctx context.Context, playerID, shopID, productID string) (int64, error) {
	return m.state.ShopPurchaseCount(ctx, playerID, shopID, productID)
}

func (m *Manager) PurchaseShopProduct(ctx context.Context, playerID, shopID, productID, transactionID string, amount int64) (ShopPurchaseResult, error) {
	product, err := m.catalog.ShopProduct(productID)
	if err != nil || product.ShopID != shopID || amount <= 0 {
		return ShopPurchaseResult{}, validation("invalid shop product %q", productID)
	}
	price := store.ShopInventoryChange{ID: product.PriceUnitID, Amount: product.Price}
	if strings.HasPrefix(product.PriceUnitID, "POKEGOLD") {
		price.Kind = "poke_gold"
	} else if _, err := m.catalog.Currency(product.PriceUnitID); err == nil {
		price.Kind = "currency"
	} else if item, err := m.catalog.Item(product.PriceUnitID); err == nil {
		price.Kind, price.SubID = "item", string(item.Kind)
	} else {
		return ShopPurchaseResult{}, validation("unsupported shop price %q", product.PriceUnitID)
	}
	rewards := make([]store.ShopInventoryChange, 0, len(product.ContentIDs))
	for _, contentID := range product.ContentIDs {
		content, err := m.catalog.ShopContent(contentID)
		if err != nil || content.Amount <= 0 {
			return ShopPurchaseResult{}, validation("invalid shop content %q", contentID)
		}
		reward := store.ShopInventoryChange{ID: content.ItemID, Amount: content.Amount}
		switch content.ItemType {
		case 0, 1:
			reward.Kind = "card"
		case 4:
			reward.Kind, reward.SubID = "item", string(catalog.ItemPackCharger)
		case 5:
			reward.Kind, reward.SubID = "item", string(catalog.ItemChallengeCharger)
		case 6, 9:
			reward.Kind = "profile_decoration"
		case 7:
			reward.Kind = "currency"
		case 15:
			reward.Kind = "theme_deck_recipe"
		case 22:
			item, itemErr := m.catalog.Item(content.ItemID)
			if itemErr != nil || item.Kind != catalog.ItemHiddenEvent {
				return ShopPurchaseResult{}, validation("invalid hidden event item %q", content.ItemID)
			}
			reward.Kind, reward.SubID = "item", string(item.Kind)
		default:
			return ShopPurchaseResult{}, validation("unsupported shop content type %d", content.ItemType)
		}
		rewards = append(rewards, reward)
	}
	count, replay, err := m.state.CommitShopPurchase(ctx, playerID, store.ShopPurchase{ShopID: shopID, ProductID: productID, TransactionID: transactionID, Amount: amount, MaxTotal: product.MaxAmount, Price: price, Rewards: rewards})
	return ShopPurchaseResult{Product: product, Rewards: rewards, Amount: amount, Count: count, Replay: replay}, err
}

func (m *Manager) TrophyStates(ctx context.Context, playerID string, ids []string) ([]store.TrophyState, error) {
	return m.state.TrophyStates(ctx, playerID, ids)
}

func (m *Manager) RentalDecks(ctx context.Context, playerID string) ([]store.RentalDeckState, error) {
	values, err := m.state.RentalDecks(ctx, playerID)
	if err != nil {
		return nil, err
	}
	result := values[:0]
	for _, value := range values {
		definition, err := m.catalog.RentalDeck(value.ID)
		if err != nil {
			return nil, fmt.Errorf("validate stored rental deck: %w", err)
		}
		if value.UsedCount < definition.UseLimit {
			result = append(result, value)
		}
	}
	return result, nil
}

func (m *Manager) UseRentalDeck(ctx context.Context, playerID, rentalDeckID string) (int64, error) {
	definition, err := m.catalog.RentalDeck(rentalDeckID)
	if err != nil {
		return 0, validation("%v", err)
	}
	return m.state.UseRentalDeck(ctx, playerID, rentalDeckID, definition.UseLimit)
}

func (m *Manager) SoloBattleTries(ctx context.Context, playerID, battleID string) ([]store.SoloBattleTryState, error) {
	definition, err := m.catalog.SoloBattle(battleID)
	if err != nil {
		return nil, validation("%v", err)
	}
	return m.state.SoloBattleTries(ctx, playerID, battleID, definition.TryIDs)
}

func (m *Manager) SaveSoloBattleTries(ctx context.Context, playerID, battleID string, values []store.SoloBattleTryState) ([]store.SoloBattleTryState, error) {
	definition, err := m.catalog.SoloBattle(battleID)
	if err != nil {
		return nil, validation("%v", err)
	}
	allowed := make(map[string]bool, len(definition.TryIDs))
	for _, id := range definition.TryIDs {
		allowed[id] = true
	}
	for _, value := range values {
		if !allowed[value.ID] {
			return nil, validation("unknown battle try %q for %q", value.ID, battleID)
		}
	}
	if _, err := m.state.SaveSoloBattleTries(ctx, playerID, battleID, values); err != nil {
		return nil, err
	}
	return m.state.SoloBattleTries(ctx, playerID, battleID, definition.TryIDs)
}

func (m *Manager) CompletedMissions(ctx context.Context, playerID string, missionIDs []string) ([]store.CompletedMission, error) {
	return m.state.CompletedMissions(ctx, playerID, missionIDs)
}

func (m *Manager) ClaimMissions(ctx context.Context, playerID string, missionIDs []string) ([]store.ShopInventoryChange, []string, error) {
	claims := make([]store.MissionClaim, 0, len(missionIDs))
	for _, missionID := range missionIDs {
		mission, err := m.catalog.Mission(missionID)
		if err != nil {
			return nil, nil, validation("%v", err)
		}
		claim := store.MissionClaim{ID: mission.ID}
		for _, rewardID := range mission.RewardIDs {
			reward, err := m.catalog.MissionReward(rewardID)
			if err != nil {
				return nil, nil, validation("%v", err)
			}
			change, err := m.missionRewardChange(reward.ItemType, reward.ItemID, reward.ExpansionID, reward.Amount)
			if err != nil {
				return nil, nil, err
			}
			claim.Rewards = append(claim.Rewards, change)
		}
		if mission.ThemeDeckRecipeID != "" {
			claim.Rewards = append(claim.Rewards, store.ShopInventoryChange{Kind: "theme_deck_recipe", ID: mission.ThemeDeckRecipeID, Amount: 1})
		}
		claims = append(claims, claim)
	}
	applied, err := m.state.ClaimMissions(ctx, playerID, claims)
	var recipes []string
	for _, reward := range applied {
		if reward.Kind == "theme_deck_recipe" {
			recipes = append(recipes, reward.ID)
		}
	}
	return applied, recipes, err
}

func (m *Manager) MissionGroupStepStates(ctx context.Context, playerID string, stepIDs []string) (map[string]bool, error) {
	return m.state.MissionGroupStepStates(ctx, playerID, stepIDs)
}

func (m *Manager) ClaimMissionGroupSteps(ctx context.Context, playerID string, stepIDs []string) ([]store.ShopInventoryChange, error) {
	claims := make([]store.MissionClaim, 0, len(stepIDs))
	for _, stepID := range stepIDs {
		step, err := m.catalog.MissionGroupRewardStep(stepID)
		if err != nil {
			return nil, validation("%v", err)
		}
		change, err := m.missionRewardChange(step.ItemType, step.ItemID, "", step.Amount)
		if err != nil {
			return nil, err
		}
		claims = append(claims, store.MissionClaim{ID: step.ID, Rewards: []store.ShopInventoryChange{change}})
	}
	return m.state.ClaimMissionGroupSteps(ctx, playerID, claims)
}

func (m *Manager) missionRewardChange(itemType int, itemID, expansionID string, amount int64) (store.ShopInventoryChange, error) {
	change := store.ShopInventoryChange{ID: itemID, Amount: amount}
	switch itemType {
	case 0, 1:
		if _, err := m.catalog.Card(itemID); err != nil {
			return store.ShopInventoryChange{}, validation("%v", err)
		}
		change.Kind = "card"
	case 4, 5, 12, 16, 17:
		item, err := m.catalog.Item(itemID)
		if err != nil {
			return store.ShopInventoryChange{}, validation("%v", err)
		}
		change.Kind, change.SubID = "item", string(item.Kind)
	case 6:
		change.Kind = "profile_decoration"
	case 7:
		change.Kind = "currency"
	case 8, 9:
		item, err := m.catalog.Item(itemID)
		if err != nil {
			return store.ShopInventoryChange{}, validation("%v", err)
		}
		change.Kind, change.SubID = "item", string(item.Kind)
	case 10:
		if _, err := m.catalog.RentalDeck(itemID); err != nil {
			return store.ShopInventoryChange{}, validation("%v", err)
		}
		change.Kind = "rental_deck"
	case 13:
		change.Kind = "theme_deck_recipe"
	default:
		return store.ShopInventoryChange{}, validation("unsupported mission reward type %d", itemType)
	}
	_ = expansionID
	return change, nil
}

func (m *Manager) CompleteTrophies(ctx context.Context, playerID string, trophies []store.TrophyState) (int64, error) {
	if err := m.projectInventoryActions(ctx, playerID); err != nil {
		return 0, err
	}
	actions, err := m.state.LatestActionTotals(ctx, playerID)
	if err != nil {
		return 0, err
	}
	progress := make(map[actionTarget]int64, len(actions))
	for _, action := range actions {
		progress[actionTarget{key: action.Key, target: action.Target}] = action.Count
	}
	completions := make([]store.TrophyCompletion, 0, len(trophies))
	for _, trophy := range trophies {
		definition, definitionErr := m.catalog.Trophy(trophy.ID)
		if definitionErr != nil || trophy.Rank < 1 || trophy.Rank > 4 {
			return 0, validation("invalid trophy %q", trophy.ID)
		}
		if required := trophyActionProgress(definition, progress); required < definition.Amounts[trophy.Rank-1] {
			return 0, validation("trophy %q rank %d is not earned", trophy.ID, trophy.Rank)
		}
		completions = append(completions, store.TrophyCompletion{TrophyState: trophy, BrightSandRewards: definition.BrightSandRewards})
	}
	return m.state.CompleteTrophies(ctx, playerID, completions)
}

func (m *Manager) SaveProfile(ctx context.Context, playerID, nickname, iconID, messageID string, emblemIDs []string) (Profile, error) {
	if _, err := m.SaveProfileSummary(ctx, playerID, nickname, iconID, messageID); err != nil {
		return Profile{}, err
	}
	if err := m.SaveEmblems(ctx, playerID, emblemIDs); err != nil {
		return Profile{}, err
	}
	return m.Profile(ctx, playerID)
}

// SaveEmblems validates and persists the three ordered profile emblem slots.
func (m *Manager) SaveEmblems(ctx context.Context, playerID string, emblemIDs []string) error {
	if len(emblemIDs) > 3 {
		return validation("at most 3 emblems can be selected")
	}
	seen := make(map[string]bool, len(emblemIDs))
	for _, emblemID := range emblemIDs {
		definition, err := m.catalog.Cosmetic(emblemID)
		if err != nil || definition.Kind != catalog.CosmeticProfileDecoration || definition.Variant != 1 || seen[emblemID] {
			return validation("invalid emblem %q", emblemID)
		}
		seen[emblemID] = true
	}
	if err := m.state.SetSelectedEmblems(ctx, playerID, emblemIDs); err != nil {
		return err
	}
	return nil
}

// SaveProfileSummary applies profile identity rules without loading inventory.
func (m *Manager) SaveProfileSummary(ctx context.Context, playerID, nickname, iconID, messageID string) (ProfileSummary, error) {
	current, err := m.ProfileSummary(ctx, playerID)
	if err != nil {
		return ProfileSummary{}, err
	}
	if nickname == "" {
		nickname = current.Player.DisplayName
	}
	name, err := validateName(nickname)
	if err != nil {
		return ProfileSummary{}, err
	}
	if iconID == "" {
		iconID = current.Settings.IconID
	}
	icon, err := m.catalog.Cosmetic(iconID)
	if err != nil || icon.Kind != catalog.CosmeticProfileDecoration || icon.Variant != 0 {
		return ProfileSummary{}, validation("unknown profile icon %q", iconID)
	}
	if messageID == "" {
		messageID = current.Settings.MessageID
	}
	if _, err := m.catalog.ProfileMessage(messageID); err != nil {
		return ProfileSummary{}, validation("%v", err)
	}
	if name != current.Player.DisplayName {
		if err := m.state.UpdateNickname(ctx, playerID, name, nicknameCooldown); err != nil {
			return ProfileSummary{}, err
		}
	}
	if iconID != current.Settings.IconID || messageID != current.Settings.MessageID {
		if err := m.state.UpdateProfileSettings(ctx, playerID, iconID, messageID); err != nil {
			return ProfileSummary{}, err
		}
	}
	return m.ProfileSummary(ctx, playerID)
}

func (m *Manager) NicknameChangedAt(ctx context.Context, playerID string) (time.Time, bool, error) {
	return m.state.NicknameChangedAt(ctx, playerID)
}

func (m *Manager) DesiredCards(ctx context.Context, playerID string) ([]store.DesiredCard, error) {
	return m.state.DesiredCards(ctx, playerID)
}

func (m *Manager) SetDesiredCards(ctx context.Context, playerID string, cards []store.DesiredCard) error {
	if len(cards) > 40 {
		return fmt.Errorf("%w: at most 40 desired cards are allowed", ErrValidation)
	}
	seen := make(map[string]bool, len(cards))
	profileCount := 0
	for _, card := range cards {
		if seen[card.CardID] {
			return fmt.Errorf("%w: duplicate desired card %q", ErrValidation, card.CardID)
		}
		seen[card.CardID] = true
		if _, err := m.catalog.Card(card.CardID); err != nil {
			return fmt.Errorf("%w: desired card %q", ErrValidation, card.CardID)
		}
		if card.ProfileOrder > 0 {
			profileCount++
		}
	}
	if profileCount > 3 {
		return fmt.Errorf("%w: at most 3 profile cards are allowed", ErrValidation)
	}
	return m.state.SetDesiredCards(ctx, playerID, cards)
}

func (m *Manager) GiveCard(ctx context.Context, senderID, receiverID, cardID, expansionID string, language int32) (store.GiveCardHistory, error) {
	card, err := m.catalog.Card(cardID)
	if err != nil {
		return store.GiveCardHistory{}, fmt.Errorf("%w: card %q", ErrValidation, cardID)
	}
	if card.Rarity < 100 || card.Rarity > 400 {
		return store.GiveCardHistory{}, fmt.Errorf("%w: card rarity is not shareable", ErrValidation)
	}
	foundExpansion := false
	for _, candidate := range card.ExpansionIDs {
		if candidate == expansionID {
			foundExpansion = true
			break
		}
	}
	if !foundExpansion {
		return store.GiveCardHistory{}, fmt.Errorf("%w: card is not in expansion %q", ErrValidation, expansionID)
	}
	return m.state.GiveCard(ctx, senderID, receiverID, cardID, expansionID, language)
}

func (m *Manager) GiveCardHistories(ctx context.Context, playerID string, since time.Time) ([]store.GiveCardHistory, error) {
	return m.state.GiveCardHistories(ctx, playerID, since)
}

func (m *Manager) SendThankReward(ctx context.Context, senderID, targetID string, routeType int32) error {
	return m.state.SendThankReward(ctx, senderID, targetID, routeType)
}

func (m *Manager) TradeState(ctx context.Context, playerID string) (store.TradeState, error) {
	return m.state.TradeState(ctx, playerID)
}

func (m *Manager) SaveTradeSettings(ctx context.Context, playerID string, blockAll bool) (store.TradeState, error) {
	return m.state.SaveTradeSettings(ctx, playerID, blockAll)
}

func (m *Manager) SaveTradeMessage(ctx context.Context, playerID, stanceID string, language int32) (store.TradeState, error) {
	return m.state.SaveTradeMessage(ctx, playerID, stanceID, language)
}

func (m *Manager) HealTradePower(ctx context.Context, playerID string, chargers map[string]int64, pokeGoldAmount int64) (store.TradeState, error) {
	var chargerID string
	var chargerAmount int64
	for id, amount := range chargers {
		definition, err := m.catalog.Item(id)
		if err != nil || definition.Kind != catalog.ItemTradeCharger || amount <= 0 || (chargerID != "" && chargerID != id) {
			return store.TradeState{}, validation("invalid trade power charger %q", id)
		}
		chargerID, chargerAmount = id, chargerAmount+amount
	}
	return m.state.HealTradePower(ctx, playerID, chargerID, chargerAmount, pokeGoldAmount)
}

func (m *Manager) SubmitTrade(ctx context.Context, input store.SubmitTradeInput) (store.TradeSession, store.TradeState, error) {
	if _, err := m.validateTradeCard(input.Card); err != nil {
		return store.TradeSession{}, store.TradeState{}, err
	}
	return m.state.SubmitTrade(ctx, input)
}

func (m *Manager) AcceptTrade(ctx context.Context, playerID, sessionID string, card store.TradeCard, deposit []byte) (store.TradeSession, store.TradeState, error) {
	answer, err := m.validateTradeCard(card)
	if err != nil {
		return store.TradeSession{}, store.TradeState{}, err
	}
	session, err := m.state.TradeSession(ctx, sessionID)
	if err != nil {
		return store.TradeSession{}, store.TradeState{}, err
	}
	proposal, err := m.catalog.Card(session.ProposerCard.CardID)
	if err != nil || proposal.Rarity != answer.Rarity {
		return store.TradeSession{}, store.TradeState{}, fmt.Errorf("%w: traded cards must have the same rarity", ErrValidation)
	}
	return m.state.AcceptTrade(ctx, playerID, sessionID, card, deposit)
}

func (m *Manager) ConfirmTrade(ctx context.Context, playerID, sessionID string) (store.TradeSession, error) {
	return m.state.ConfirmTrade(ctx, playerID, sessionID)
}

func (m *Manager) RejectTrade(ctx context.Context, playerID, sessionID string) (store.TradeSession, error) {
	return m.state.RejectTrade(ctx, playerID, sessionID)
}

func (m *Manager) ReceiveTradeOutcome(ctx context.Context, playerID, sessionID string) (store.TradeSession, store.TradeCard, error) {
	return m.state.ReceiveTradeOutcome(ctx, playerID, sessionID)
}

func (m *Manager) ReceiveTradeDeposit(ctx context.Context, playerID, sessionID string) (store.TradeSession, store.TradeCard, []byte, store.TradeState, error) {
	return m.state.ReceiveTradeDeposit(ctx, playerID, sessionID)
}

func (m *Manager) ActiveTradeSession(ctx context.Context, playerID string) (store.TradeSession, error) {
	return m.state.ActiveTradeSession(ctx, playerID)
}

func (m *Manager) TradeHistories(ctx context.Context, playerID string, limit, offset int) ([]store.TradeSession, error) {
	return m.state.TradeHistories(ctx, playerID, limit, offset)
}

func (m *Manager) Flags(ctx context.Context, playerID, namespace string, keys []string) (map[string]store.Flag, error) {
	return m.state.Flags(ctx, playerID, namespace, keys)
}

func (m *Manager) SetFlags(ctx context.Context, playerID, namespace string, values map[string]int64) (time.Time, error) {
	return m.state.SetFlags(ctx, playerID, namespace, values)
}

func (m *Manager) SavePlayerSettings(ctx context.Context, playerID, language, country string, useLastLogin, useLoginData, usePerformanceErrors bool) error {
	language, ok := gamelocale.NormalizeLocale(language)
	if !ok {
		return fmt.Errorf("%w: unsupported account language", ErrValidation)
	}
	country, ok = gamelocale.NormalizeCountry(country)
	if !ok {
		return fmt.Errorf("%w: invalid country region code", ErrValidation)
	}
	return m.state.SavePlayerSettings(ctx, playerID, language, country, useLastLogin, useLoginData, usePerformanceErrors)
}

// UpdatePlayerLocale changes the account language and region without touching telemetry preferences.
func (m *Manager) UpdatePlayerLocale(ctx context.Context, playerID, language, country string) error {
	language, ok := gamelocale.NormalizeLocale(language)
	if !ok {
		return fmt.Errorf("%w: unsupported account language", ErrValidation)
	}
	country, ok = gamelocale.NormalizeCountry(country)
	if !ok {
		return fmt.Errorf("%w: invalid country region code", ErrValidation)
	}
	return m.state.UpdatePlayerLocale(ctx, playerID, language, country)
}

func (m *Manager) SaveShowcase(ctx context.Context, playerID, kind string, value store.Showcase) (store.Showcase, error) {
	return m.state.SaveShowcase(ctx, playerID, kind, value)
}

func (m *Manager) Showcases(ctx context.Context, playerID, kind string) ([]store.Showcase, error) {
	return m.state.Showcases(ctx, playerID, kind)
}

func (m *Manager) Showcase(ctx context.Context, playerID, kind string, id uint64) (store.Showcase, error) {
	return m.state.Showcase(ctx, playerID, kind, id)
}

func (m *Manager) DeleteShowcase(ctx context.Context, playerID, kind string, id uint64) error {
	return m.state.DeleteShowcase(ctx, playerID, kind, id)
}

func (m *Manager) OrderShowcases(ctx context.Context, playerID, kind string, orders []store.ShowcaseOrder) error {
	return m.state.OrderShowcases(ctx, playerID, kind, orders)
}

func (m *Manager) SaveBestCollection(ctx context.Context, playerID string, payload []byte) error {
	return m.state.SaveBestCollection(ctx, playerID, payload)
}

func (m *Manager) BestCollection(ctx context.Context, playerID string) ([]byte, error) {
	return m.state.BestCollection(ctx, playerID)
}

func (m *Manager) LikeCollection(ctx context.Context, playerID, targetID string) (time.Time, bool, error) {
	return m.state.LikeCollection(ctx, playerID, targetID)
}

func (m *Manager) CollectionLikeExpiry(ctx context.Context, playerID, targetID string) (time.Time, error) {
	return m.state.CollectionLikeExpiry(ctx, playerID, targetID)
}

func (m *Manager) CollectionLikeCount(ctx context.Context, targetID string) (uint64, error) {
	return m.state.CollectionLikeCount(ctx, targetID)
}

func (m *Manager) CollectionCandidates(ctx context.Context, excludedPlayerID string) ([]store.CollectionCandidate, error) {
	return m.state.CollectionCandidates(ctx, excludedPlayerID)
}

func (m *Manager) PendingPresents(ctx context.Context, playerID string, offset, limit int) ([]store.Present, error) {
	return m.state.PendingPresents(ctx, playerID, offset, limit)
}

func (m *Manager) PresentHistories(ctx context.Context, playerID string, offset, limit int) ([]store.Present, error) {
	return m.state.PresentHistories(ctx, playerID, offset, limit)
}

func (m *Manager) HasNewPresents(ctx context.Context, playerID string) (bool, error) {
	return m.state.HasNewPresents(ctx, playerID)
}

func (m *Manager) MarkPresentsViewed(ctx context.Context, playerID string) error {
	return m.state.MarkPresentsViewed(ctx, playerID)
}

func (m *Manager) ReceivePresent(ctx context.Context, playerID, presentID string) (store.Present, error) {
	return m.state.ReceivePresent(ctx, playerID, presentID)
}

func (m *Manager) SyncAccountLinks(ctx context.Context, playerID string, linkTypes []int32) error {
	return m.state.SyncAccountLinks(ctx, playerID, linkTypes)
}

func (m *Manager) UnlinkAccount(ctx context.Context, playerID string) error {
	return m.state.UnlinkAccount(ctx, playerID)
}

func (m *Manager) CardExchangeRoute(playerID string) int32 {
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(playerID))
	return int32(hash.Sum32()%3 + 1)
}

func (m *Manager) CardExchangeDefinition(id string) (catalog.CardExchange, error) {
	return m.catalog.CardExchange(id)
}

func (m *Manager) CurrencyDefinition(id string) (catalog.Currency, error) {
	return m.catalog.Currency(id)
}

func (m *Manager) CardDefinition(id string) (catalog.Card, error) {
	return m.catalog.Card(id)
}

func (m *Manager) ExchangeCardCosmetics(ctx context.Context, playerID string, inputs []CardExchangeInput) ([]store.CardExchangeCount, error) {
	if len(inputs) == 0 || len(inputs) > 30 {
		return nil, validation("between 1 and 30 card exchanges are required")
	}
	operations := make([]store.CardExchangeOperation, 0, len(inputs))
	for _, input := range inputs {
		definition, err := m.catalog.CardExchange(input.CatalogID)
		if err != nil || input.Amount <= 0 {
			return nil, validation("invalid card exchange %q", input.CatalogID)
		}
		if definition.RouteType != 0 && int32(definition.RouteType) != m.CardExchangeRoute(playerID) {
			return nil, validation("card exchange %q is not available on this route", input.CatalogID)
		}
		requiredCards := definition.ConsumeCardAmount * input.Amount
		if int64(len(input.ResourceCardIDs)) < requiredCards {
			return nil, validation("card exchange %q requires %d resource cards", input.CatalogID, requiredCards)
		}
		for _, cardID := range input.ResourceCardIDs {
			if cardID != definition.CardID {
				return nil, validation("card exchange %q received an invalid resource card", input.CatalogID)
			}
		}
		kind := "card_skin"
		if definition.ProductType == 2 {
			kind = "currency"
		} else if definition.ProductType == 4 {
			kind = "card_frame"
		}
		operations = append(operations, store.CardExchangeOperation{CatalogID: definition.ID, CardID: definition.CardID, ProductID: definition.ProductID, ProductKind: kind, Amount: input.Amount, ConsumeCards: definition.ConsumeCardAmount, ConsumeBrightSand: definition.ConsumeBrightSandAmount, ProductAmount: definition.ProductAmount, ParentID: definition.ParentID, RequiredParentCount: definition.ParentExchangeCount})
	}
	return m.state.ExchangeCardCosmetics(ctx, playerID, operations)
}

func (m *Manager) CardExchangeCounts(ctx context.Context, playerID string) ([]store.CardExchangeCount, error) {
	return m.state.CardExchangeCounts(ctx, playerID)
}

func (m *Manager) FeedEntries(ctx context.Context, playerID string) ([]store.FeedEntry, error) {
	return m.state.FeedEntries(ctx, playerID, 16)
}

func (m *Manager) ChallengeState(ctx context.Context, playerID string) (store.ChallengeState, error) {
	return m.state.ChallengeState(ctx, playerID)
}

// SnoopFeed validates a feed and reserves its challenge power.
func (m *Manager) SnoopFeed(ctx context.Context, playerID, feedID string, cost int64) (store.ChallengeState, bool, error) {
	entry, err := m.state.FeedEntry(ctx, feedID)
	if err != nil || entry.OwnerID == playerID || len(entry.Cards) == 0 || time.Since(entry.CreatedAt) > store.FeedLifetime {
		return store.ChallengeState{}, false, validation("feed is unavailable")
	}
	expectedCost := int64(1)
	for _, cardID := range entry.Cards {
		card, err := m.catalog.Card(cardID)
		if err != nil {
			return store.ChallengeState{}, false, err
		}
		expectedCost = max(expectedCost, feedChallengeCost(card.Rarity))
	}
	if cost != expectedCost {
		return store.ChallengeState{}, false, validation("feed challenge power is %d, want %d", cost, expectedCost)
	}
	return m.state.BeginFeedSnoop(ctx, playerID, feedID, cost)
}

func (m *Manager) ChallengeFeed(ctx context.Context, playerID, feedID, transactionID string) (FeedChallengeResult, error) {
	entry, err := m.state.FeedEntry(ctx, feedID)
	if err != nil || entry.OwnerID == playerID || len(entry.Cards) == 0 || time.Since(entry.CreatedAt) > store.FeedLifetime {
		return FeedChallengeResult{}, validation("feed is unavailable")
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(playerID + ":" + feedID))
	cardID := entry.Cards[int(hash.Sum32())%len(entry.Cards)]
	card, err := m.catalog.Card(cardID)
	if err != nil {
		return FeedChallengeResult{}, err
	}
	cost := feedChallengeCost(card.Rarity)
	state, replay, err := m.state.CommitFeedChallenge(ctx, playerID, feedID, transactionID, cardID, cost)
	if err == nil && !replay {
		if actionErr := m.state.AddAction(ctx, playerID, 8, "", 1, true); actionErr != nil {
			return FeedChallengeResult{}, actionErr
		}
	}
	return FeedChallengeResult{Entry: entry, Card: card, State: state, Replay: replay}, err
}

func feedChallengeCost(rarity int) int64 {
	switch {
	case rarity >= 600:
		return 4
	case rarity == 400 || rarity == 500:
		return 3
	case rarity == 300:
		return 2
	default:
		return 1
	}
}

func (m *Manager) HealChallengePower(ctx context.Context, playerID string, chargers map[string]int64, pokeGoldAmount int64) (store.ChallengeState, error) {
	var chargerID string
	var chargerAmount int64
	for id, amount := range chargers {
		definition, err := m.catalog.Item(id)
		if err != nil || definition.Kind != catalog.ItemChallengeCharger || amount <= 0 || (chargerID != "" && chargerID != id) {
			return store.ChallengeState{}, validation("invalid challenge power charger %q", id)
		}
		chargerID, chargerAmount = id, chargerAmount+amount
	}
	return m.state.HealChallengePower(ctx, playerID, chargerID, chargerAmount, pokeGoldAmount)
}

func (m *Manager) validateTradeCard(card store.TradeCard) (catalog.Card, error) {
	definition, err := m.catalog.Card(card.CardID)
	if err != nil {
		return catalog.Card{}, fmt.Errorf("%w: trade card %q", ErrValidation, card.CardID)
	}
	foundExpansion := false
	for _, candidate := range definition.ExpansionIDs {
		if candidate == card.ExpansionID {
			foundExpansion = true
			break
		}
	}
	if !foundExpansion {
		return catalog.Card{}, fmt.Errorf("%w: card is not in expansion %q", ErrValidation, card.ExpansionID)
	}
	switch definition.Rarity {
	case 100, 200, 300, 400, 500, 600, 700, 830, 860:
		return definition, nil
	default:
		return catalog.Card{}, fmt.Errorf("%w: card rarity is not tradeable", ErrValidation)
	}
}

func (m *Manager) validateProgress(level int, experience int64) error {
	if _, err := m.catalog.Level(level); err != nil {
		return validation("%v", err)
	}
	if experience < 0 || experience > 9_999_999 {
		return validation("experience must be between 0 and 9999999")
	}
	return nil
}
func validateName(value string) (string, error) {
	value = strings.TrimSpace(value)
	size := utf8.RuneCountInString(value)
	if size < 1 || size > 14 {
		return "", validation("nickname must contain between 1 and 14 characters")
	}
	for _, r := range value {
		if r < ' ' || r == 0x7f {
			return "", validation("nickname contains a control character")
		}
	}
	return value, nil
}
func validation(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}
