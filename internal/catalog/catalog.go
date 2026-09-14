// Package catalog loads the small subset of the immutable 1.7.2 master-data
// needed by local profiles, inventories, and the home screen.
package catalog

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	DefaultLocale  = "fr_FR"
	FallbackLocale = "en_US"
)

var ErrUnknownID = errors.New("unknown catalog identifier")

type CardKind string

const (
	PokemonCard CardKind = "pokemon"
	TrainerCard CardKind = "trainer"
)

type ItemKind string

const (
	ItemPeripheral       ItemKind = "peripheral_goods"
	ItemPackCharger      ItemKind = "pack_power_charger"
	ItemChallengeCharger ItemKind = "challenge_power_charger"
	ItemEventCharger     ItemKind = "event_power_charger"
	ItemTradeCharger     ItemKind = "trade_power_charger"
	ItemRewardTicket     ItemKind = "reward_ticket"
	ItemRevivalClock     ItemKind = "revival_clock"
	ItemTrade            ItemKind = "trade_item"
	ItemHiddenEvent      ItemKind = "hidden_event_item"
)

type CosmeticKind string

const (
	CosmeticProfileDecoration CosmeticKind = "profile_decoration"
	CosmeticCardSkin          CosmeticKind = "card_skin"
	CosmeticCardFrame         CosmeticKind = "card_frame"
)

type Card struct {
	ID               string
	Name             string
	Kind             CardKind
	Rarity           int
	PokemonTypes     []int32
	TrainerType      int32
	ExpansionIDs     []string
	CollectionNumber int
}

// Trophy contains the progression rule read from the immutable master data.
// Amounts and BrightSandRewards use indexes 0..3 for bronze..rainbow.
type Trophy struct {
	ID                string
	Type              int
	TargetIDs         []int32
	Amounts           [4]int64
	BrightSandRewards [4]int64
}

type Expansion struct {
	ID, Name, LongName, SeriesID string
	Promotion                    bool
}

type Pack struct {
	ID, Name, Description, ExpansionID string
	AssetID, SkuAssetID, CeilGroupID   string
	Featured                           []string
	Tables                             []PackTable
	Products                           []PackProduct
}

type WeightedCard struct {
	CardID string
	Weight int64
}

type PackSlotPool struct {
	Rarity string
	Weight int64
	Cards  []WeightedCard
}

type PackSlot struct {
	Position int
	Label    string
	Pools    []PackSlotPool
}

type PackTable struct {
	ID, Name, Kind string
	Weight         int64
	NoticeType     int
	Slots          []PackSlot
}

type PackProduct struct {
	ID, PackID                   string
	OnePrice, TenPrice           int64
	OneExperience, TenExperience int64
	OneCeilPoints, TenCeilPoints int64
}

type Item struct {
	ID, Name, Description, AssetID string
	Kind                           ItemKind
	Variant                        int
}

type Currency struct {
	ID, Name, Description, AssetID string
	ProtocolType                   int32
}

type Level struct {
	Number      int
	RequiredExp int64
}

type Cosmetic struct {
	ID, Name, Description, ExpansionID, AssetID string
	Kind                                        CosmeticKind
	Variant                                     int
}

type CardCosmeticGrant struct {
	CardID, CosmeticID string
	Amount             int64
}

type CardExchange struct {
	ID, CardID, ProductID, ParentID            string
	CardType, ProductType, RouteType           int
	ConsumeCardAmount, ConsumeBrightSandAmount int64
	ProductAmount, ParentExchangeCount         int64
}

type ShopContent struct {
	ID, ItemID, ExpansionID string
	ItemType                int
	Amount                  int64
}

type ShopProduct struct {
	ID, ShopID, PriceUnitID string
	ProductType             int
	Price, MaxAmount        int64
	ContentIDs              []string
}

type ProfileMessage struct{ ID, Text string }
type PackPower struct {
	ID                                                   string
	AutoHealLimit, HealSecondsPerPower, PokeGoldUseLimit int64
}
type HomeSettings struct {
	ThirdPartyDataVersion, PrivacyPolicyVersion, TermsOfServiceVersion string
	PackPowers                                                         []PackPower
}

type RentalDeck struct {
	ID       string
	UseLimit int64
}

type SoloBattle struct {
	ID     string
	TryIDs []string
}

type Mission struct {
	ID                string
	RewardIDs         []string
	ThemeDeckRecipeID string
}

type MissionReward struct {
	ID, ItemID, ExpansionID string
	ItemType                int
	Amount                  int64
}

type MissionGroupRewardStep struct {
	ID, MissionGroupID, ItemID string
	ItemType                   int
	Amount                     int64
}

// TutorialCompletion is the terminal status expected by the client for one
// tutorial. Reward-backed tutorials are discovered from the master data.
type TutorialCompletion struct {
	ID   string
	Step int64
}

// Catalog is an immutable in-memory index. JSON structures stay private to the
// implementation and never leak to the administration or gRPC adapters.
type Catalog struct {
	root, locale, fallback   string
	cards                    map[string]Card
	expansions               map[string]Expansion
	packs                    map[string]Pack
	packTables               map[string]PackTable
	packProducts             map[string]PackProduct
	items                    map[string]Item
	currencies               map[string]Currency
	levels                   map[int]Level
	cosmetics                map[string]Cosmetic
	cardSkinGrants           []CardCosmeticGrant
	cardFrameGrants          []CardCosmeticGrant
	cardSkinCompatibilities  []CardCosmeticGrant
	cardFrameCompatibilities []CardCosmeticGrant
	cardExchanges            map[string]CardExchange
	shopContents             map[string]ShopContent
	shopProducts             map[string]ShopProduct
	trophies                 map[string]Trophy
	profileMessages          map[string]ProfileMessage
	rentalDecks              map[string]RentalDeck
	soloBattles              map[string]SoloBattle
	missions                 map[string]Mission
	missionRewards           map[string]MissionReward
	missionGroupSteps        map[string]MissionGroupRewardStep
	tutorialCompletions      []TutorialCompletion
	home                     HomeSettings
}

func Open(root string) (*Catalog, error) { return OpenLocale(root, DefaultLocale, FallbackLocale) }

func OpenLocale(root, locale, fallback string) (*Catalog, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("master-data directory is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve master-data path: %w", err)
	}
	if locale == "" {
		locale = DefaultLocale
	}
	if fallback == "" {
		fallback = FallbackLocale
	}
	for _, current := range []string{locale, fallback} {
		info, statErr := os.Stat(filepath.Join(abs, current))
		if statErr != nil || !info.IsDir() {
			return nil, fmt.Errorf("master-data locale %s is unavailable: %w", current, statErr)
		}
	}
	c := &Catalog{
		root: abs, locale: locale, fallback: fallback,
		cards: map[string]Card{}, expansions: map[string]Expansion{}, packs: map[string]Pack{},
		packTables: map[string]PackTable{}, packProducts: map[string]PackProduct{},
		items: map[string]Item{}, currencies: map[string]Currency{}, levels: map[int]Level{}, cosmetics: map[string]Cosmetic{}, profileMessages: map[string]ProfileMessage{}, cardExchanges: map[string]CardExchange{}, shopContents: map[string]ShopContent{}, shopProducts: map[string]ShopProduct{}, trophies: map[string]Trophy{}, rentalDecks: map[string]RentalDeck{}, soloBattles: map[string]SoloBattle{}, missions: map[string]Mission{}, missionRewards: map[string]MissionReward{}, missionGroupSteps: map[string]MissionGroupRewardStep{},
	}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Catalog) Locale() string { return c.locale }

func (c *Catalog) Card(id string) (Card, error) {
	v, ok := c.cards[id]
	if !ok {
		return Card{}, unknown("card", id)
	}
	v.ExpansionIDs = append([]string(nil), v.ExpansionIDs...)
	v.PokemonTypes = append([]int32(nil), v.PokemonTypes...)
	return v, nil
}
func (c *Catalog) Cards() []Card {
	return sorted(c.cards, func(v Card) string { return v.Name + v.ID })
}
func (c *Catalog) Expansion(id string) (Expansion, error) {
	v, ok := c.expansions[id]
	if !ok {
		return Expansion{}, unknown("expansion", id)
	}
	return v, nil
}
func (c *Catalog) Expansions() []Expansion {
	return sorted(c.expansions, func(v Expansion) string { return v.Name + v.ID })
}
func (c *Catalog) Pack(id string) (Pack, error) {
	v, ok := c.packs[id]
	if !ok {
		return Pack{}, unknown("pack", id)
	}
	v.Featured = append([]string(nil), v.Featured...)
	v.Tables = clonePackTables(v.Tables)
	v.Products = append([]PackProduct(nil), v.Products...)
	return v, nil
}
func (c *Catalog) Packs() []Pack {
	result := sorted(c.packs, func(v Pack) string { return v.Name + v.ID })
	for i := range result {
		result[i].Featured = append([]string(nil), result[i].Featured...)
		result[i].Tables = clonePackTables(result[i].Tables)
		result[i].Products = append([]PackProduct(nil), result[i].Products...)
	}
	return result
}

func (c *Catalog) PackTable(id string) (PackTable, error) {
	v, ok := c.packTables[id]
	if !ok {
		return PackTable{}, unknown("pack table", id)
	}
	return clonePackTable(v), nil
}

func (c *Catalog) PackProduct(id string) (PackProduct, error) {
	v, ok := c.packProducts[id]
	if !ok {
		return PackProduct{}, unknown("pack product", id)
	}
	return v, nil
}
func (c *Catalog) Item(id string) (Item, error) {
	v, ok := c.items[id]
	if !ok {
		return Item{}, unknown("item", id)
	}
	return v, nil
}
func (c *Catalog) Items() []Item {
	return sorted(c.items, func(v Item) string { return v.Name + v.ID })
}
func (c *Catalog) Currency(id string) (Currency, error) {
	v, ok := c.currencies[id]
	if !ok {
		return Currency{}, unknown("currency", id)
	}
	return v, nil
}
func (c *Catalog) Currencies() []Currency {
	return sorted(c.currencies, func(v Currency) string { return v.Name + v.ID })
}
func (c *Catalog) Level(number int) (Level, error) {
	if number == 1 {
		return Level{Number: 1}, nil
	}
	v, ok := c.levels[number]
	if !ok {
		return Level{}, unknown("level", fmt.Sprint(number))
	}
	return v, nil
}
func (c *Catalog) Levels() []Level {
	result := []Level{{Number: 1}}
	for _, v := range c.levels {
		result = append(result, v)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Number < result[j].Number })
	return result
}
func (c *Catalog) Cosmetic(id string) (Cosmetic, error) {
	v, ok := c.cosmetics[id]
	if !ok {
		return Cosmetic{}, unknown("cosmetic", id)
	}
	return v, nil
}
func (c *Catalog) Cosmetics() []Cosmetic {
	return sorted(c.cosmetics, func(v Cosmetic) string { return v.Name + v.ID })
}
func (c *Catalog) CardSkinGrants() []CardCosmeticGrant {
	return append([]CardCosmeticGrant(nil), c.cardSkinGrants...)
}
func (c *Catalog) CardFrameGrants() []CardCosmeticGrant {
	return append([]CardCosmeticGrant(nil), c.cardFrameGrants...)
}
func (c *Catalog) CardSkinCompatibilities() []CardCosmeticGrant {
	return append([]CardCosmeticGrant(nil), c.cardSkinCompatibilities...)
}
func (c *Catalog) CardFrameCompatibilities() []CardCosmeticGrant {
	return append([]CardCosmeticGrant(nil), c.cardFrameCompatibilities...)
}
func (c *Catalog) CardExchange(id string) (CardExchange, error) {
	value, ok := c.cardExchanges[id]
	if !ok {
		return CardExchange{}, unknown("card exchange", id)
	}
	return value, nil
}
func (c *Catalog) ShopProduct(id string) (ShopProduct, error) {
	value, ok := c.shopProducts[id]
	if !ok {
		return ShopProduct{}, unknown("shop product", id)
	}
	value.ContentIDs = append([]string(nil), value.ContentIDs...)
	return value, nil
}
func (c *Catalog) ShopContent(id string) (ShopContent, error) {
	value, ok := c.shopContents[id]
	if !ok {
		return ShopContent{}, unknown("shop content", id)
	}
	return value, nil
}
func (c *Catalog) Trophy(id string) (Trophy, error) {
	value, ok := c.trophies[id]
	if !ok {
		return Trophy{}, unknown("trophy", id)
	}
	value.TargetIDs = append([]int32(nil), value.TargetIDs...)
	return value, nil
}
func (c *Catalog) TrophyExists(id string) bool { _, ok := c.trophies[id]; return ok }
func (c *Catalog) ProfileMessage(id string) (ProfileMessage, error) {
	v, ok := c.profileMessages[id]
	if !ok {
		return ProfileMessage{}, unknown("profile message", id)
	}
	return v, nil
}
func (c *Catalog) ProfileMessages() []ProfileMessage {
	return sorted(c.profileMessages, func(v ProfileMessage) string { return v.Text + v.ID })
}
func (c *Catalog) HomeSettings() HomeSettings {
	v := c.home
	v.PackPowers = append([]PackPower(nil), v.PackPowers...)
	return v
}

func (c *Catalog) RentalDeck(id string) (RentalDeck, error) {
	v, ok := c.rentalDecks[id]
	if !ok {
		return RentalDeck{}, unknown("rental deck", id)
	}
	return v, nil
}
func (c *Catalog) RentalDecks() []RentalDeck {
	return sorted(c.rentalDecks, func(v RentalDeck) string { return v.ID })
}
func (c *Catalog) SoloBattle(id string) (SoloBattle, error) {
	v, ok := c.soloBattles[id]
	if !ok {
		return SoloBattle{}, unknown("solo battle", id)
	}
	v.TryIDs = append([]string(nil), v.TryIDs...)
	return v, nil
}
func (c *Catalog) Mission(id string) (Mission, error) {
	v, ok := c.missions[id]
	if !ok {
		return Mission{}, unknown("mission", id)
	}
	v.RewardIDs = append([]string(nil), v.RewardIDs...)
	return v, nil
}
func (c *Catalog) MissionReward(id string) (MissionReward, error) {
	v, ok := c.missionRewards[id]
	if !ok {
		return MissionReward{}, unknown("mission reward", id)
	}
	return v, nil
}
func (c *Catalog) MissionGroupRewardStep(id string) (MissionGroupRewardStep, error) {
	v, ok := c.missionGroupSteps[id]
	if !ok {
		return MissionGroupRewardStep{}, unknown("mission group reward step", id)
	}
	return v, nil
}

// TutorialCompletions returns every terminal tutorial status known by the
// active master data and supported client version.
func (c *Catalog) TutorialCompletions() []TutorialCompletion {
	return append([]TutorialCompletion(nil), c.tutorialCompletions...)
}

func unknown(kind, id string) error { return fmt.Errorf("%w: %s %q", ErrUnknownID, kind, id) }
func sorted[V any](source map[string]V, key func(V) string) []V {
	result := make([]V, 0, len(source))
	for _, v := range source {
		result = append(result, v)
	}
	sort.Slice(result, func(i, j int) bool { return key(result[i]) < key(result[j]) })
	return result
}

func clonePackTables(values []PackTable) []PackTable {
	result := make([]PackTable, len(values))
	for i, value := range values {
		result[i] = clonePackTable(value)
	}
	return result
}

func clonePackTable(value PackTable) PackTable {
	value.Slots = append([]PackSlot(nil), value.Slots...)
	for i := range value.Slots {
		value.Slots[i].Pools = append([]PackSlotPool(nil), value.Slots[i].Pools...)
		for j := range value.Slots[i].Pools {
			value.Slots[i].Pools[j].Cards = append([]WeightedCard(nil), value.Slots[i].Pools[j].Cards...)
		}
	}
	return value
}

func packTableKind(value string) string {
	switch {
	case strings.Contains(value, "PACK_TABLE_NAME_RARE"):
		return "rare"
	case strings.Contains(value, "PACK_TABLE_NAME_PLUS1"):
		return "plus1"
	case strings.Contains(value, "PACK_TABLE_NAME_GUARANTEE"):
		return "guarantee"
	case strings.Contains(value, "PACK_TABLE_NAME_THEMERARE"):
		return "theme_rare"
	default:
		return "normal"
	}
}

type characterRow struct{ CharacterID, DisplayNameMSID string }
type pokemonRow struct {
	PokemonID, CharacterID string
	PokemonTypes           []int32
}
type trainerRow struct {
	TrainerID, CharacterID string
	TrainerType            int32
}
type cardRow struct {
	CardID, PokemonID, TrainerID string
	Rarity                       int
}
type expansionRow struct {
	ExpansionID, NameMSID, LongNameMSID, SeriesID string
	IsPromotion                                   bool
}
type collectionRow struct {
	CardID, ExpansionID string
	CollectionNumber    int
}
type packRow struct {
	PackID, NameMSID, DescriptionMSID, SkuID string
	AssetID, PackCeilPointSharedGroupID      string
	FeaturedCardIDs                          []string
}
type packSkuRow struct{ PackSkuID, ExpansionID, AssetID string }
type packTableRow struct {
	PackID, PackTableID, NameMSID string
	ProbabilityWeight             int64
	NoticeType                    int
}
type packTableLabelRow struct {
	PackTableID, PackTableLabelID, DrawCountLabel, Label string
	ProbabilityWeight                                    int64
}
type packTableDrawRow struct {
	PackTableID, DrawCountLabel string
	Nth                         int
}
type packTableCardRow struct {
	PackTableLabelID, CardID string
	ProbabilityWeight        int64
}
type packProductRow struct {
	PackID, PackShopProductID          string
	OnePackPrice, TenPackPrice         int64
	OnePackExp, TenPackExp             int64
	OnePackCeilPoint, TenPackCeilPoint int64
}
type namedRow struct {
	ID, NameMSID, DescriptionMSID, ExpansionID, AssetID string
	Type                                                int
}
type peripheralAssetRow struct {
	FileID, BoardID, IllustrationIconID string
}
type levelRow struct {
	Level       int
	RequiredExp int64
}
type cardSkinRow struct {
	CardSkinID, NameMSID, DescriptionMSID, CardSkinResourceIconID string
	SkinType                                                      int
}
type cardFrameRow struct{ CardFrameID, NameMSID, DescriptionMSID, CardFrameResourceIconID string }
type cardSkinDropRow struct {
	CardID, CardSkinID string
	CardSkinAmount     int64
}
type cardFrameDropRow struct {
	CardID, CardFrameID string
	CardFrameAmount     int64
}
type cardExchangeRow struct {
	CardExchangeCatalogID, CardID, ProductID, ParentID       string
	CardType, ProductType, RouteType                         int
	ConsumeCardAmount, ConsumeLightSandAmount, ProductAmount int64
	ParentExchangeCount                                      int64
}
type shopContentRow struct {
	ContentID, ItemID, ExpansionID string
	ItemType                       int
	Amount                         int64
}
type shopProductRow struct {
	ProductID, ShopID, PriceUnitID, LimitID string
	ProductType, LimitType                  int
	Price                                   int64
	ContentIDs                              []string
}
type shopLimitRow struct {
	LimitID      string
	MaxAmount    int64
	PeriodicType int
}
type trophyRow struct {
	ID                                                        string
	Type                                                      int
	TargetIDs                                                 []string
	BronzeAmount, SilverAmount, GoldAmount, RainbowAmount     int64
	BronzeRewards, SilverRewards, GoldRewards, RainbowRewards []string
}
type trophyRewardRow struct {
	RewardID, ItemID, ExpansionID string
	ItemType                      int
	Amount                        int64
}
type profileMessageRow struct{ ID, MessageMSID string }
type packPowerRow struct {
	PackPowerID                                          string
	AutoHealLimit, HealSecondsPerPower, PokeGoldUseLimit int64
}
type agreementRow struct {
	AgreementKey     int
	AgreementVersion string
}
type tutorialRewardRow struct {
	TutorialID   string
	TutorialStep int64
}
type rentalDeckRow struct {
	RentalDeckID     string
	UsableLimitCount int64
}
type soloBattleRow struct {
	BattleID     string
	BattleTryIDs []string
}
type missionRow struct {
	ID                       string
	RewardIDs                []string
	RelatedThemeDeckRecipeID string
}
type missionRewardRow struct {
	RewardID, ItemID, ExpansionID string
	ItemType                      int
	Amount                        int64
}
type missionGroupRewardStepRow struct {
	MissionGroupRewardStepID, MissionGroupID, ItemID string
	ItemType                                         int
	Amount                                           int64
}

func (c *Catalog) load() error {
	characters, err := readLocalized[characterRow](c, "Character.json")
	if err != nil {
		return err
	}
	characterNames := map[string]string{}
	for _, r := range characters {
		characterNames[r.CharacterID] = localized(r.DisplayNameMSID)
	}
	pokemon, err := readLocalized[pokemonRow](c, "Pokemon.json")
	if err != nil {
		return err
	}
	pokemonNames := map[string]string{}
	pokemonTypes := map[string][]int32{}
	for _, r := range pokemon {
		name := characterNames[r.CharacterID]
		if name == "" {
			return fmt.Errorf("validate Pokemon.json: character %q is unknown", r.CharacterID)
		}
		pokemonNames[r.PokemonID] = name
		pokemonTypes[r.PokemonID] = append([]int32(nil), r.PokemonTypes...)
	}
	trainers, err := readLocalized[trainerRow](c, "Trainer.json")
	if err != nil {
		return err
	}
	trainerNames := map[string]string{}
	trainerTypes := map[string]int32{}
	for _, r := range trainers {
		name := characterNames[r.CharacterID]
		if name == "" {
			return fmt.Errorf("validate Trainer.json: character %q is unknown", r.CharacterID)
		}
		trainerNames[r.TrainerID] = name
		trainerTypes[r.TrainerID] = r.TrainerType
	}
	expansions, err := readLocalized[expansionRow](c, "Expansion.json")
	if err != nil {
		return err
	}
	for _, r := range expansions {
		if r.ExpansionID == "" {
			return fmt.Errorf("validate Expansion.json: empty identifier")
		}
		c.expansions[r.ExpansionID] = Expansion{ID: r.ExpansionID, Name: localized(r.NameMSID), LongName: localized(r.LongNameMSID), SeriesID: r.SeriesID, Promotion: r.IsPromotion}
	}
	collections, err := readLocalized[collectionRow](c, "ExpansionCollectionNumber.json")
	if err != nil {
		return err
	}
	cardExpansions, numbers := map[string][]string{}, map[string]int{}
	for _, r := range collections {
		if _, ok := c.expansions[r.ExpansionID]; !ok {
			return fmt.Errorf("validate ExpansionCollectionNumber.json: expansion %q is unknown", r.ExpansionID)
		}
		cardExpansions[r.CardID] = append(cardExpansions[r.CardID], r.ExpansionID)
		numbers[r.CardID] = r.CollectionNumber
	}
	if err := c.loadCards("PokemonCard.json", PokemonCard, pokemonNames, pokemonTypes, nil, cardExpansions, numbers); err != nil {
		return err
	}
	if err := c.loadCards("TrainerCard.json", TrainerCard, trainerNames, nil, trainerTypes, cardExpansions, numbers); err != nil {
		return err
	}
	packSkus, err := readLocalized[packSkuRow](c, "PackSku.json")
	if err != nil {
		return err
	}
	skuExpansions := map[string]string{}
	skuAssets := map[string]string{}
	for _, r := range packSkus {
		if _, ok := c.expansions[r.ExpansionID]; !ok {
			return fmt.Errorf("validate PackSku.json: expansion %q is unknown", r.ExpansionID)
		}
		skuExpansions[r.PackSkuID] = r.ExpansionID
		skuAssets[r.PackSkuID] = r.AssetID
	}
	packs, err := readLocalized[packRow](c, "PackMaster.json")
	if err != nil {
		return err
	}
	for _, r := range packs {
		expansionID := skuExpansions[r.SkuID]
		if expansionID == "" {
			return fmt.Errorf("validate PackMaster.json: sku %q is unknown", r.SkuID)
		}
		for _, cardID := range r.FeaturedCardIDs {
			if _, ok := c.cards[cardID]; !ok {
				return fmt.Errorf("validate PackMaster.json: featured card %q is unknown", cardID)
			}
		}
		c.packs[r.PackID] = Pack{
			ID: r.PackID, Name: localized(r.NameMSID), Description: localized(r.DescriptionMSID),
			ExpansionID: expansionID, AssetID: r.AssetID, SkuAssetID: skuAssets[r.SkuID],
			CeilGroupID: r.PackCeilPointSharedGroupID, Featured: append([]string(nil), r.FeaturedCardIDs...),
		}
	}
	if err := c.loadPackTables(); err != nil {
		return err
	}
	if err := c.loadPackProducts(); err != nil {
		return err
	}
	if err := c.loadCurrencies(); err != nil {
		return err
	}
	if err := c.loadItems(); err != nil {
		return err
	}
	if err := c.loadLevels(); err != nil {
		return err
	}
	if err := c.loadCosmetics(); err != nil {
		return err
	}
	if err := c.loadCardExchanges(); err != nil {
		return err
	}
	if err := c.loadItemShop(); err != nil {
		return err
	}
	rewardRows, err := readLocalized[trophyRewardRow](c, "TrophyRewards.json")
	if err != nil {
		return err
	}
	brightSandRewards := map[string]int64{}
	for _, reward := range rewardRows {
		if reward.ItemType == 7 && reward.ItemID == "SHINEDUST" {
			brightSandRewards[reward.RewardID] = reward.Amount
		}
	}
	trophies, err := readLocalized[trophyRow](c, "Trophies.json")
	if err != nil {
		return err
	}
	for _, trophy := range trophies {
		targetIDs := make([]int32, 0, len(trophy.TargetIDs))
		for _, target := range trophy.TargetIDs {
			value, parseErr := strconv.ParseInt(target, 10, 32)
			if parseErr != nil {
				return fmt.Errorf("validate Trophies.json: invalid target %q for trophy %q", target, trophy.ID)
			}
			targetIDs = append(targetIDs, int32(value))
		}
		definition := Trophy{
			ID: trophy.ID, Type: trophy.Type, TargetIDs: targetIDs,
			Amounts: [4]int64{trophy.BronzeAmount, trophy.SilverAmount, trophy.GoldAmount, trophy.RainbowAmount},
		}
		for rank, rewards := range [4][]string{trophy.BronzeRewards, trophy.SilverRewards, trophy.GoldRewards, trophy.RainbowRewards} {
			for _, rewardID := range rewards {
				definition.BrightSandRewards[rank] += brightSandRewards[rewardID]
			}
		}
		c.trophies[trophy.ID] = definition
	}
	if err := c.loadTutorialCompletions(); err != nil {
		return err
	}
	if err := c.loadGameplayDefinitions(); err != nil {
		return err
	}
	return c.loadHomeSettings()
}

func (c *Catalog) loadGameplayDefinitions() error {
	rentalDecks, err := readLocalized[rentalDeckRow](c, "RentalDeck.json")
	if err != nil {
		return err
	}
	for _, row := range rentalDecks {
		if row.RentalDeckID == "" || row.UsableLimitCount <= 0 {
			return fmt.Errorf("validate RentalDeck.json: invalid rental deck %q", row.RentalDeckID)
		}
		c.rentalDecks[row.RentalDeckID] = RentalDeck{ID: row.RentalDeckID, UseLimit: row.UsableLimitCount}
	}
	for _, table := range []string{"SoloStepupBattle.json", "SoloRandomBattle.json", "SoloEventBattle.json"} {
		rows, loadErr := readLocalized[soloBattleRow](c, table)
		if loadErr != nil {
			return loadErr
		}
		for _, row := range rows {
			if row.BattleID == "" {
				return fmt.Errorf("validate %s: empty battle identifier", table)
			}
			c.soloBattles[row.BattleID] = SoloBattle{ID: row.BattleID, TryIDs: append([]string(nil), row.BattleTryIDs...)}
		}
	}
	for _, table := range []string{"ActivityMissions.json", "CardMissions.json"} {
		rows, loadErr := readLocalized[missionRow](c, table)
		if loadErr != nil {
			return loadErr
		}
		for _, row := range rows {
			if row.ID == "" {
				return fmt.Errorf("validate %s: empty mission identifier", table)
			}
			c.missions[row.ID] = Mission{ID: row.ID, RewardIDs: append([]string(nil), row.RewardIDs...), ThemeDeckRecipeID: row.RelatedThemeDeckRecipeID}
		}
	}
	rewards, err := readLocalized[missionRewardRow](c, "MissionReward.json")
	if err != nil {
		return err
	}
	for _, row := range rewards {
		if row.RewardID == "" || row.ItemID == "" || row.Amount <= 0 {
			return fmt.Errorf("validate MissionReward.json: invalid reward %q", row.RewardID)
		}
		c.missionRewards[row.RewardID] = MissionReward{ID: row.RewardID, ItemID: row.ItemID, ExpansionID: row.ExpansionID, ItemType: row.ItemType, Amount: row.Amount}
	}
	steps, err := readLocalized[missionGroupRewardStepRow](c, "MissionGroupRewardStep.json")
	if err != nil {
		return err
	}
	for _, row := range steps {
		if row.MissionGroupRewardStepID == "" || row.ItemID == "" || row.Amount <= 0 {
			return fmt.Errorf("validate MissionGroupRewardStep.json: invalid step %q", row.MissionGroupRewardStepID)
		}
		c.missionGroupSteps[row.MissionGroupRewardStepID] = MissionGroupRewardStep{ID: row.MissionGroupRewardStepID, MissionGroupID: row.MissionGroupID, ItemID: row.ItemID, ItemType: row.ItemType, Amount: row.Amount}
	}
	return nil
}

// clientTutorialCompletions contains feature tutorials whose identifiers and
// terminal steps live in the IL2CPP client rather than in master-data tables.
// Values match TutorialId and TutorialSteps in the 1.7.2 client.
var clientTutorialCompletions = []TutorialCompletion{
	{"1000", 999}, {"1010", 2},
	{"2000", 3}, {"2001", 5}, {"2002", 5}, {"2003", 5}, {"2010", 2},
	{"2140", 6}, {"2160", 5}, {"2161", 2}, {"2162", 2}, {"2300", 2},
	{"2400", 2}, {"2500", 2}, {"2510", 2}, {"2520", 2}, {"2521", 2},
	{"2530", 2}, {"2531", 2}, {"2540", 2}, {"2600", 2}, {"2700", 2},
	{"2800", 999}, {"2900", 999},
}

func (c *Catalog) loadTutorialCompletions() error {
	completed := make(map[string]int64, len(clientTutorialCompletions))
	for _, value := range clientTutorialCompletions {
		completed[value.ID] = value.Step
	}
	rows, err := readLocalized[tutorialRewardRow](c, "TutorialRewards.json")
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.TutorialID == "" || row.TutorialStep <= 0 {
			return fmt.Errorf("validate TutorialRewards.json: invalid tutorial %q step %d", row.TutorialID, row.TutorialStep)
		}
		if row.TutorialStep > completed[row.TutorialID] {
			completed[row.TutorialID] = row.TutorialStep
		}
	}
	c.tutorialCompletions = make([]TutorialCompletion, 0, len(completed))
	for id, step := range completed {
		c.tutorialCompletions = append(c.tutorialCompletions, TutorialCompletion{ID: id, Step: step})
	}
	sort.Slice(c.tutorialCompletions, func(i, j int) bool {
		return c.tutorialCompletions[i].ID < c.tutorialCompletions[j].ID
	})
	return nil
}

func (c *Catalog) loadItemShop() error {
	contents, err := readLocalized[shopContentRow](c, "ItemShopContent.json")
	if err != nil {
		return err
	}
	for _, row := range contents {
		c.shopContents[row.ContentID] = ShopContent{ID: row.ContentID, ItemID: row.ItemID, ExpansionID: row.ExpansionID, ItemType: row.ItemType, Amount: row.Amount}
	}
	limits, err := readLocalized[shopLimitRow](c, "ItemShopPeriodLimit.json")
	if err != nil {
		return err
	}
	limitAmounts := map[string]int64{}
	for _, row := range limits {
		limitAmounts[row.LimitID] = row.MaxAmount
	}
	products, err := readLocalized[shopProductRow](c, "ItemShopProduct.json")
	if err != nil {
		return err
	}
	for _, row := range products {
		if row.ProductID == "" || row.ShopID == "" || row.Price < 0 || len(row.ContentIDs) == 0 {
			return fmt.Errorf("validate ItemShopProduct.json: invalid product %q", row.ProductID)
		}
		for _, contentID := range row.ContentIDs {
			if _, ok := c.shopContents[contentID]; !ok {
				return fmt.Errorf("validate ItemShopProduct.json: content %q is unknown", contentID)
			}
		}
		c.shopProducts[row.ProductID] = ShopProduct{ID: row.ProductID, ShopID: row.ShopID, PriceUnitID: row.PriceUnitID, ProductType: row.ProductType, Price: row.Price, MaxAmount: limitAmounts[row.LimitID], ContentIDs: append([]string(nil), row.ContentIDs...)}
	}
	return nil
}

func (c *Catalog) loadCardExchanges() error {
	rows, err := readLocalized[cardExchangeRow](c, "CardExchangeCatalog.json")
	if err != nil {
		return err
	}
	type cosmeticPair struct{ cardID, cosmeticID string }
	skinCompatibilities := make(map[cosmeticPair]CardCosmeticGrant)
	frameCompatibilities := make(map[cosmeticPair]CardCosmeticGrant)
	for _, row := range rows {
		if row.CardExchangeCatalogID == "" || row.CardID == "" || row.ProductID == "" || row.ProductAmount <= 0 || row.ConsumeCardAmount < 0 || row.ConsumeLightSandAmount < 0 {
			return fmt.Errorf("validate CardExchangeCatalog.json: invalid catalog %q", row.CardExchangeCatalogID)
		}
		if _, ok := c.cards[row.CardID]; !ok {
			return fmt.Errorf("validate CardExchangeCatalog.json: card %q is unknown", row.CardID)
		}
		product, cosmeticOK := c.cosmetics[row.ProductID]
		_, currencyOK := c.currencies[row.ProductID]
		if (row.ProductType == 1 && (!cosmeticOK || product.Kind != CosmeticCardSkin)) || (row.ProductType == 2 && !currencyOK) || (row.ProductType == 4 && (!cosmeticOK || product.Kind != CosmeticCardFrame)) || (row.ProductType != 1 && row.ProductType != 2 && row.ProductType != 4) {
			return fmt.Errorf("validate CardExchangeCatalog.json: product %q is invalid", row.ProductID)
		}
		c.cardExchanges[row.CardExchangeCatalogID] = CardExchange{ID: row.CardExchangeCatalogID, CardID: row.CardID, ProductID: row.ProductID, ParentID: row.ParentID, CardType: row.CardType, ProductType: row.ProductType, RouteType: row.RouteType, ConsumeCardAmount: row.ConsumeCardAmount, ConsumeBrightSandAmount: row.ConsumeLightSandAmount, ProductAmount: row.ProductAmount, ParentExchangeCount: row.ParentExchangeCount}
		pair := cosmeticPair{cardID: row.CardID, cosmeticID: row.ProductID}
		grant := CardCosmeticGrant{CardID: row.CardID, CosmeticID: row.ProductID, Amount: row.ProductAmount}
		switch row.ProductType {
		case 1:
			skinCompatibilities[pair] = grant
		case 4:
			frameCompatibilities[pair] = grant
		}
	}
	for _, grant := range skinCompatibilities {
		c.cardSkinCompatibilities = append(c.cardSkinCompatibilities, grant)
	}
	for _, grant := range frameCompatibilities {
		c.cardFrameCompatibilities = append(c.cardFrameCompatibilities, grant)
	}
	for _, grants := range [][]CardCosmeticGrant{c.cardSkinCompatibilities, c.cardFrameCompatibilities} {
		sort.Slice(grants, func(i, j int) bool {
			if grants[i].CardID != grants[j].CardID {
				return grants[i].CardID < grants[j].CardID
			}
			return grants[i].CosmeticID < grants[j].CosmeticID
		})
	}
	return nil
}

func (c *Catalog) loadPackTables() error {
	tableRows, err := readLocalized[packTableRow](c, "PackTableMaster.json")
	if err != nil {
		return err
	}
	labelRows, err := readLocalized[packTableLabelRow](c, "PackTableLabelMaster.json")
	if err != nil {
		return err
	}
	drawRows, err := readLocalized[packTableDrawRow](c, "PackTableDrawCountLabelMaster.json")
	if err != nil {
		return err
	}
	cardRows, err := readLocalized[packTableCardRow](c, "PackTableCardMaster.json")
	if err != nil {
		return err
	}

	cardsByLabel := make(map[string][]WeightedCard)
	for _, row := range cardRows {
		if _, ok := c.cards[row.CardID]; !ok {
			return fmt.Errorf("validate PackTableCardMaster.json: card %q is unknown", row.CardID)
		}
		if row.ProbabilityWeight <= 0 {
			return fmt.Errorf("validate PackTableCardMaster.json: card %q has invalid weight", row.CardID)
		}
		cardsByLabel[row.PackTableLabelID] = append(cardsByLabel[row.PackTableLabelID], WeightedCard{CardID: row.CardID, Weight: row.ProbabilityWeight})
	}
	labelsByTableAndDraw := make(map[string]map[string][]packTableLabelRow)
	for _, row := range labelRows {
		if len(cardsByLabel[row.PackTableLabelID]) == 0 {
			return fmt.Errorf("validate PackTableLabelMaster.json: label %q has no cards", row.PackTableLabelID)
		}
		if labelsByTableAndDraw[row.PackTableID] == nil {
			labelsByTableAndDraw[row.PackTableID] = make(map[string][]packTableLabelRow)
		}
		labelsByTableAndDraw[row.PackTableID][row.DrawCountLabel] = append(labelsByTableAndDraw[row.PackTableID][row.DrawCountLabel], row)
	}
	drawsByTable := make(map[string][]packTableDrawRow)
	for _, row := range drawRows {
		drawsByTable[row.PackTableID] = append(drawsByTable[row.PackTableID], row)
	}

	for _, row := range tableRows {
		pack, ok := c.packs[row.PackID]
		if !ok {
			return fmt.Errorf("validate PackTableMaster.json: pack %q is unknown", row.PackID)
		}
		table := PackTable{ID: row.PackTableID, Name: localized(row.NameMSID), Kind: packTableKind(row.NameMSID), Weight: row.ProbabilityWeight, NoticeType: row.NoticeType}
		draws := append([]packTableDrawRow(nil), drawsByTable[row.PackTableID]...)
		sort.Slice(draws, func(i, j int) bool { return draws[i].Nth < draws[j].Nth })
		for _, draw := range draws {
			poolRows := labelsByTableAndDraw[row.PackTableID][draw.DrawCountLabel]
			if len(poolRows) == 0 {
				return fmt.Errorf("validate PackTableDrawCountLabelMaster.json: table %q draw %q has no pools", row.PackTableID, draw.DrawCountLabel)
			}
			slot := PackSlot{Position: draw.Nth, Label: draw.DrawCountLabel}
			for _, poolRow := range poolRows {
				slot.Pools = append(slot.Pools, PackSlotPool{Rarity: poolRow.Label, Weight: poolRow.ProbabilityWeight, Cards: append([]WeightedCard(nil), cardsByLabel[poolRow.PackTableLabelID]...)})
			}
			table.Slots = append(table.Slots, slot)
		}
		if len(table.Slots) == 0 {
			return fmt.Errorf("validate PackTableMaster.json: table %q has no slots", row.PackTableID)
		}
		c.packTables[table.ID] = table
		pack.Tables = append(pack.Tables, table)
		c.packs[pack.ID] = pack
	}
	return nil
}

func (c *Catalog) loadPackProducts() error {
	rows, err := readLocalized[packProductRow](c, "PackShopProduct.json")
	if err != nil {
		return err
	}
	for _, row := range rows {
		pack, ok := c.packs[row.PackID]
		if !ok {
			return fmt.Errorf("validate PackShopProduct.json: pack %q is unknown", row.PackID)
		}
		product := PackProduct{
			ID: row.PackShopProductID, PackID: row.PackID, OnePrice: row.OnePackPrice, TenPrice: row.TenPackPrice,
			OneExperience: row.OnePackExp, TenExperience: row.TenPackExp,
			OneCeilPoints: row.OnePackCeilPoint, TenCeilPoints: row.TenPackCeilPoint,
		}
		c.packProducts[product.ID] = product
		pack.Products = append(pack.Products, product)
		c.packs[pack.ID] = pack
	}
	return nil
}

func (c *Catalog) loadCards(table string, kind CardKind, names map[string]string, pokemonTypes map[string][]int32, trainerTypes map[string]int32, expansions map[string][]string, numbers map[string]int) error {
	rows, err := readLocalized[cardRow](c, table)
	if err != nil {
		return err
	}
	for _, r := range rows {
		relationID := r.PokemonID
		if kind == TrainerCard {
			relationID = r.TrainerID
		}
		name := names[relationID]
		if name == "" {
			return fmt.Errorf("validate %s: source %q is unknown", table, relationID)
		}
		linked := expansions[r.CardID]
		if len(linked) == 0 {
			return fmt.Errorf("validate %s: card %q has no expansion", table, r.CardID)
		}
		c.cards[r.CardID] = Card{
			ID: r.CardID, Name: name, Kind: kind, Rarity: r.Rarity,
			PokemonTypes: append([]int32(nil), pokemonTypes[relationID]...), TrainerType: trainerTypes[relationID],
			ExpansionIDs: append([]string(nil), linked...), CollectionNumber: numbers[r.CardID],
		}
	}
	return nil
}

func (c *Catalog) loadCurrencies() error {
	rows, err := readLocalized[namedRow](c, "Currency.json")
	if err != nil {
		return err
	}
	protocolTypes := map[string]int32{"SHOPTICKET": 3, "SHINEDUST": 4, "SHOPTICKET_P": 5, "PREMIUM_TICKET": 6}
	for _, r := range rows {
		c.currencies[r.ID] = Currency{ID: r.ID, Name: localized(r.NameMSID), Description: localized(r.DescriptionMSID), AssetID: r.AssetID, ProtocolType: protocolTypes[r.ID]}
	}
	return nil
}

func (c *Catalog) loadItems() error {
	assets := map[string]string{}
	for _, table := range []string{"CollectionFile.json", "CollectionBoard.json"} {
		rows, err := readLocalized[peripheralAssetRow](c, table)
		if err != nil {
			return err
		}
		for _, row := range rows {
			id := row.FileID
			if id == "" {
				id = row.BoardID
			}
			assets[id] = row.IllustrationIconID
		}
	}
	for _, spec := range []struct {
		table string
		kind  ItemKind
	}{{"PeripheralGoods.json", ItemPeripheral}, {"PackPowerCharger.json", ItemPackCharger}, {"ChallengePowerCharger.json", ItemChallengeCharger}, {"EventPowerCharger.json", ItemEventCharger}, {"TradePowerCharger.json", ItemTradeCharger}, {"RewardTicket.json", ItemRewardTicket}, {"RevivalClock.json", ItemRevivalClock}, {"TradeItem.json", ItemTrade}, {"TradeTicket.json", ItemTrade}, {"HiddenEventItem.json", ItemHiddenEvent}} {
		rows, err := readLocalized[namedRow](c, spec.table)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.ID == "" {
				continue
			}
			if _, exists := c.items[r.ID]; exists {
				return fmt.Errorf("validate %s: duplicate item %q", spec.table, r.ID)
			}
			assetID := r.AssetID
			if assetID == "" {
				assetID = assets[r.ID]
			}
			if r.ID == "ITSUKA_TICKET" {
				assetID = "TICKET_ITSUKA"
			}
			c.items[r.ID] = Item{ID: r.ID, Name: localized(r.NameMSID), Description: localized(r.DescriptionMSID), AssetID: assetID, Kind: spec.kind, Variant: r.Type}
		}
	}
	return nil
}

func (c *Catalog) loadLevels() error {
	rows, err := readLocalized[levelRow](c, "PlayerLevelSetting.json")
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.Level < 2 || r.RequiredExp < 0 {
			return fmt.Errorf("validate PlayerLevelSetting.json: invalid level %d", r.Level)
		}
		c.levels[r.Level] = Level{Number: r.Level, RequiredExp: r.RequiredExp}
	}
	for level := 2; level <= len(rows)+1; level++ {
		if _, ok := c.levels[level]; !ok {
			return fmt.Errorf("validate PlayerLevelSetting.json: level %d is missing", level)
		}
	}
	return nil
}

func (c *Catalog) loadCosmetics() error {
	rows, err := readLocalized[namedRow](c, "ProfileDecoration.json")
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.ExpansionID != "" {
			if _, ok := c.expansions[r.ExpansionID]; !ok {
				return fmt.Errorf("validate ProfileDecoration.json: expansion %q is unknown", r.ExpansionID)
			}
		}
		c.cosmetics[r.ID] = Cosmetic{ID: r.ID, Name: localized(r.NameMSID), Description: localized(r.DescriptionMSID), Kind: CosmeticProfileDecoration, Variant: r.Type, ExpansionID: r.ExpansionID}
	}
	skins, err := readLocalized[cardSkinRow](c, "CardSkin.json")
	if err != nil {
		return err
	}
	for _, r := range skins {
		c.cosmetics[r.CardSkinID] = Cosmetic{ID: r.CardSkinID, Name: localized(r.NameMSID), Description: localized(r.DescriptionMSID), AssetID: r.CardSkinResourceIconID, Kind: CosmeticCardSkin, Variant: r.SkinType}
	}
	frames, err := readLocalized[cardFrameRow](c, "CardFrame.json")
	if err != nil {
		return err
	}
	for _, r := range frames {
		c.cosmetics[r.CardFrameID] = Cosmetic{ID: r.CardFrameID, Name: localized(r.NameMSID), Description: localized(r.DescriptionMSID), AssetID: r.CardFrameResourceIconID, Kind: CosmeticCardFrame}
	}
	skinDrops, err := readLocalized[cardSkinDropRow](c, "CardSkinDropSetting.json")
	if err != nil {
		return err
	}
	for _, row := range skinDrops {
		if _, ok := c.cards[row.CardID]; !ok {
			return fmt.Errorf("validate CardSkinDropSetting.json: card %q is unknown", row.CardID)
		}
		cosmetic, ok := c.cosmetics[row.CardSkinID]
		if !ok || cosmetic.Kind != CosmeticCardSkin {
			return fmt.Errorf("validate CardSkinDropSetting.json: card skin %q is unknown", row.CardSkinID)
		}
		c.cardSkinGrants = append(c.cardSkinGrants, CardCosmeticGrant{CardID: row.CardID, CosmeticID: row.CardSkinID, Amount: max(row.CardSkinAmount, 1)})
	}
	frameDrops, err := readLocalized[cardFrameDropRow](c, "CardFrameDropSetting.json")
	if err != nil {
		return err
	}
	for _, row := range frameDrops {
		if _, ok := c.cards[row.CardID]; !ok {
			return fmt.Errorf("validate CardFrameDropSetting.json: card %q is unknown", row.CardID)
		}
		cosmetic, ok := c.cosmetics[row.CardFrameID]
		if !ok || cosmetic.Kind != CosmeticCardFrame {
			return fmt.Errorf("validate CardFrameDropSetting.json: card frame %q is unknown", row.CardFrameID)
		}
		c.cardFrameGrants = append(c.cardFrameGrants, CardCosmeticGrant{CardID: row.CardID, CosmeticID: row.CardFrameID, Amount: max(row.CardFrameAmount, 1)})
	}
	return nil
}

func (c *Catalog) loadHomeSettings() error {
	messages, err := readLocalized[profileMessageRow](c, "ProfileMessage.json")
	if err != nil {
		return err
	}
	for _, row := range messages {
		c.profileMessages[row.ID] = ProfileMessage{ID: row.ID, Text: localized(row.MessageMSID)}
	}
	packs, err := readLocalized[packPowerRow](c, "PackPower.json")
	if err != nil {
		return err
	}
	for _, row := range packs {
		c.home.PackPowers = append(c.home.PackPowers, PackPower{ID: row.PackPowerID, AutoHealLimit: row.AutoHealLimit, HealSecondsPerPower: row.HealSecondsPerPower, PokeGoldUseLimit: row.PokeGoldUseLimit})
	}
	agreements, err := readLocalized[agreementRow](c, "AgreementSetting.json")
	if err != nil {
		return err
	}
	for _, row := range agreements {
		switch row.AgreementKey {
		case 0:
			c.home.ThirdPartyDataVersion = row.AgreementVersion
		case 1:
			c.home.PrivacyPolicyVersion = row.AgreementVersion
		case 2:
			c.home.TermsOfServiceVersion = row.AgreementVersion
		}
	}
	if c.home.ThirdPartyDataVersion == "" || c.home.PrivacyPolicyVersion == "" || c.home.TermsOfServiceVersion == "" {
		return fmt.Errorf("validate AgreementSetting.json: required versions are missing")
	}
	return nil
}

func readLocalized[T any](c *Catalog, table string) ([]T, error) {
	primary, primaryErr := readTable[T](filepath.Join(c.root, c.locale, table))
	if primaryErr == nil {
		return primary, nil
	}
	fallback, fallbackErr := readTable[T](filepath.Join(c.root, c.fallback, table))
	if fallbackErr != nil {
		return nil, fmt.Errorf("load master-data table %s for %s (fallback %s also failed): %v; %w", table, c.locale, c.fallback, primaryErr, fallbackErr)
	}
	return fallback, nil
}

func readTable[T any](path string) ([]T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var rows []T
	if err := json.Unmarshal(data, &rows); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return rows, nil
}

var markup = regexp.MustCompile(`\[[^\]]+\]`)

func localized(value string) string {
	value = strings.TrimSpace(value)
	if start := strings.Index(value, "  ("); start >= 0 && strings.HasSuffix(value, ")") {
		value = value[start+3 : len(value)-1]
	}
	value = strings.ReplaceAll(value, "[C:Nbsp ]", " ")
	value = markup.ReplaceAllString(value, "")
	return strings.TrimSpace(strings.Join(strings.Fields(value), " "))
}
