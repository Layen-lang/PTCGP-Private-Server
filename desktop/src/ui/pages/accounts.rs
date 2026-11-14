//! Port of `AccountsPage.tsx`: profile rail with drag reordering, profile
//! editor, resources, card collection, cosmetics, create and delete dialogs.

use crate::api::{AndroidDevice, Api, Bootstrap, CardPage, CatalogResult, PlayerSummary, Profile, VisualCatalogItem};
use crate::i18n::{t, tf};
use crate::theme::Palette;
use crate::ui::app::PageEvent;
use crate::ui::forms::{self, Choice, ChoiceState};
use crate::ui::kit::{self, icon, pal, Kind};
use crate::ui::tasks::{after, spawn_api, spawn_api_in};
use gpui_kit::component::input::{InputEvent, InputState};
use gpui_kit::component::select::SelectEvent;
use gpui_kit::component::Sizable as _;
use gpui_kit::prelude::FluentBuilder as _;
use gpui_kit::*;
use std::collections::{HashMap, HashSet};
use std::time::Duration;

const PRIMARY_RESOURCES: &[&str] = &[
    "POKEGOLD_FREE", "POKEGOLD_PAID", "SHINEDUST", "SHOPTICKET", "SHOPTICKET_P", "PREMIUM_TICKET", "ITSUKA_TICKET", "TICKET_DECK_A2b",
    "PACK_CHARGER_100030", "SPPACK_CHARGER_170010", "CHALLENGE_CHARGER_110030", "EVENT_CHARGER", "REVIVAL_CLOCK_100010", "TRADE_ITEM_130010", "TRADE_CHAGER_140010",
];

fn card_languages() -> Vec<(i64, String)> {
    [(1, "japanese"), (2, "english"), (3, "traditionalChinese"), (4, "french"), (5, "italian"), (6, "german"), (7, "spanish"), (8, "portugueseBrazil"), (9, "korean"), (10, "spanishLatam"), (11, "simplifiedChinese"), (12, "indonesian")]
        .into_iter().map(|(id, key)| (id, t(&format!("cards.{key}")))).collect()
}

fn account_languages() -> Vec<Choice> {
    [("ja_JP", "japanese"), ("en_US", "english"), ("zh_TW", "traditionalChinese"), ("fr_FR", "french"), ("it_IT", "italian"), ("de_DE", "german"), ("es_ES", "spanish"), ("pt_BR", "portugueseBrazil"), ("ko_KR", "korean"), ("es_419", "spanishLatam"), ("zh_CN", "simplifiedChinese"), ("id_ID", "indonesian")]
        .into_iter().map(|(locale, key)| Choice::new(locale, t(&format!("cards.{key}")))).collect()
}

fn clean_name(value: &str) -> String {
    const PREFIXES: &[&str] = &["Icône", "Tapis de Jeu", "Protège-Carte", "Pièce Pokémon", "Couverture", "Arrière-Plan", "Icon", "Playmat", "Card Sleeve", "Pokémon Coin", "Binder", "Display Board", "Backdrop"];
    for prefix in PREFIXES {
        if value.get(..prefix.len()).map(|head| head.to_lowercase() == prefix.to_lowercase()).unwrap_or(false) {
            let rest = value[prefix.len()..].trim_start();
            if let Some(rest) = rest.strip_prefix('-') { return rest.trim_start().to_string() }
        }
    }
    value.to_string()
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Section {
    Profile,
    Resources,
    Cards,
    Battle,
    Showcase,
}

#[derive(Clone)]
struct DraggedPlayer {
    id: String,
    name: String,
}

impl Render for DraggedPlayer {
    fn render(&mut self, _: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let p = pal(cx);
        div().px(px(12.)).py(px(8.)).rounded(px(8.)).bg(p.panel).border_1().border_color(p.brand).shadow_lg().text_size(px(13.)).font_weight(FontWeight::BOLD).text_color(p.ink).child(self.name.clone())
    }
}

#[derive(Clone, Copy, PartialEq)]
enum RowState {
    Idle,
    Busy,
    Saved,
}

struct ProfileForm {
    name: Entity<InputState>,
    level: Entity<InputState>,
    experience: Entity<InputState>,
    country: Entity<InputState>,
    language: ChoiceState,
    icon_id: String,
    icon_query: Entity<InputState>,
    emblem_query: Entity<InputState>,
    emblem_ids: Vec<String>,
    active_slot: usize,
    pictures_open: bool,
    emblems_open: bool,
    saving: bool,
}

struct CardsState {
    query: Entity<InputState>,
    language: ChoiceState,
    expansion: ChoiceState,
    rarity: ChoiceState,
    language_id: i64,
    expansion_id: String,
    rarity_id: i64,
    page: i64,
    catalog: Option<CardPage>,
    owned: Option<HashMap<String, i64>>,
    totals: HashMap<String, i64>,
    drafts: HashMap<String, Entity<InputState>>,
    busy_id: String,
    /// Expansion and rarity options currently in the selects.
    filter_signature: String,
    _debounce: Task<()>,
}

struct CreateForm {
    name: Entity<InputState>,
    country: Entity<InputState>,
    level: Entity<InputState>,
    experience: Entity<InputState>,
    quantity: Entity<InputState>,
    language: ChoiceState,
    complete: bool,
    busy: bool,
}

pub struct AccountsPage {
    api: Api,
    emulator: String,
    players: Vec<PlayerSummary>,
    selected: String,
    profiles: HashMap<String, Profile>,
    section: Section,
    can_launch: bool,
    devices: Vec<AndroidDevice>,
    busy: bool,
    deleting: Option<PlayerSummary>,
    create: Option<CreateForm>,
    form: Option<ProfileForm>,
    icons: Vec<VisualCatalogItem>,
    emblems: Option<Vec<VisualCatalogItem>>,
    resources: Vec<VisualCatalogItem>,
    owned: Option<HashMap<String, i64>>,
    rows: HashMap<String, (Entity<InputState>, RowState)>,
    cards: Option<CardsState>,
    peripherals: Vec<VisualCatalogItem>,
    peripheral_query: Option<Entity<InputState>>,
    peripheral_filter: i64,
    peripheral_busy: String,
    /// Set by async callbacks: create the visible cards' quantity fields on the next render (needs the window).
    pending_sync: bool,
    _subs: Vec<Subscription>,
}

impl EventEmitter<PageEvent> for AccountsPage {}

fn error(cx: &mut Context<AccountsPage>, e: anyhow::Error) { cx.emit(PageEvent::Error(e.to_string())) }

impl AccountsPage {
    pub fn new(api: Api, bootstrap: Bootstrap, devices: Vec<AndroidDevice>, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let emulator = bootstrap.emulator.clone().unwrap_or_default();
        let selected = bootstrap.players.iter().find(|p| p.active).map(|p| p.id.clone())
            .or_else(|| bootstrap.players.iter().find(|p| p.in_use_on.as_deref().map(|d| d == emulator).unwrap_or(true)).map(|p| p.id.clone()))
            .or_else(|| bootstrap.players.first().map(|p| p.id.clone())).unwrap_or_default();
        let mut page = AccountsPage {
            api, emulator, players: bootstrap.players, selected, profiles: HashMap::new(), section: Section::Profile, can_launch: false, devices, busy: false,
            deleting: None, create: None, form: None, icons: Vec::new(), emblems: None, resources: Vec::new(), owned: None, rows: HashMap::new(), cards: None,
            peripherals: Vec::new(), peripheral_query: None, peripheral_filter: -1, peripheral_busy: String::new(), pending_sync: false, _subs: Vec::new(),
        };
        // Prefetch every profile so switching is instant (web: profileCache).
        for player in page.players.clone() {
            let id = player.id.clone();
            spawn_api(cx, &page.api, move |api| api.profile(&id), move |this: &mut Self, result, _| { if let Ok(p) = result { this.profiles.insert(player.id.clone(), p); } });
        }
        page.load_profile(window, cx);
        page
    }

    pub fn set_context(&mut self, can_launch: bool, devices: Vec<AndroidDevice>) { self.can_launch = can_launch; self.devices = devices; }

    pub fn update_bootstrap(&mut self, bootstrap: Bootstrap, _: &mut Window, cx: &mut Context<Self>) {
        self.players = bootstrap.players;
        cx.notify();
    }

    /// Refresh button: profiles are cached for instant switching, so reload
    /// them all and reopen the visible section from the server.
    pub fn reload(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.profiles.clear();
        for player in self.players.clone().into_iter().filter(|p| p.id != self.selected) {
            let id = player.id.clone();
            spawn_api(cx, &self.api, move |api| api.profile(&id), move |this: &mut Self, result, _| { if let Ok(p) = result { this.profiles.entry(player.id.clone()).or_insert(p); } });
        }
        self.load_profile(window, cx);
    }

    fn profile(&self) -> Option<&Profile> { self.profiles.get(&self.selected) }

    fn device_name(&self, serial: &str) -> String {
        let Some(device) = self.devices.iter().find(|d| d.serial == serial || d.aliases.as_ref().map(|a| a.iter().any(|x| x == serial)).unwrap_or(false)) else { return serial.to_string() };
        crate::ui::device::unique_title(device, &self.devices)
    }

    fn select(&mut self, id: String, window: &mut Window, cx: &mut Context<Self>) {
        if self.selected == id { return }
        self.selected = id;
        self.load_profile(window, cx);
    }

    fn load_profile(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.form = None;
        self.cards = None;
        self.owned = None;
        self.rows.clear();
        self.emblems = None;
        if self.selected.is_empty() { return }
        if self.profiles.contains_key(&self.selected) { self.open_section(self.section, window, cx) }
        let id = self.selected.clone();
        spawn_api_in(window, cx, &self.api, move |api| api.profile(&id), |this, result, window, cx| match result {
            Ok(profile) => {
                let fresh = !this.profiles.contains_key(&profile.player.id);
                this.profiles.insert(profile.player.id.clone(), profile.clone());
                if profile.player.id == this.selected && (fresh || this.form.is_none()) { this.open_section(this.section, window, cx) }
            }
            Err(e) => error(cx, e),
        });
    }

    fn open_section(&mut self, section: Section, window: &mut Window, cx: &mut Context<Self>) {
        self.section = section;
        let Some(profile) = self.profile().cloned() else { return };
        let id = profile.player.id.clone();
        match section {
            Section::Profile => self.build_form(&profile, window, cx),
            Section::Resources => {
                self.owned = None;
                spawn_api_in(window, cx, &self.api, move |api| Ok((api.catalog("/api/catalog/resources")?, api.inventory(&id, "resources", None)?)), |this, result, window, cx| match result {
                    Ok((catalog, owned)) => {
                        let owned: HashMap<_, _> = owned.into_iter().map(|a| (a.id, a.quantity)).collect();
                        this.rows = catalog.iter().map(|item| {
                            let value = owned.get(&item.id).copied().unwrap_or(0).to_string();
                            (item.id.clone(), (forms::text_state(&value, "", window, cx), RowState::Idle))
                        }).collect();
                        this.resources = catalog;
                        this.owned = Some(owned);
                    }
                    Err(e) => error(cx, e),
                });
            }
            Section::Cards => self.build_cards(window, cx),
            Section::Battle | Section::Showcase => {
                let group = if section == Section::Battle { "battle" } else { "showcase" };
                self.owned = None;
                self.peripheral_filter = -1;
                let query = forms::text_state("", &t("common.search"), window, cx);
                self._subs.push(cx.subscribe(&query, |_, _, e: &InputEvent, cx| if matches!(e, InputEvent::Change) { cx.notify() }));
                self.peripheral_query = Some(query);
                spawn_api(cx, &self.api, move |api| Ok((api.catalog(&format!("/api/catalog/peripherals?group={group}"))?, api.inventory(&id, group, None)?)), move |this, result, cx| match result {
                    Ok((catalog, owned)) => { this.peripherals = catalog; this.owned = Some(owned.into_iter().map(|a| (a.id, a.quantity)).collect()) }
                    Err(e) => error(cx, e),
                });
            }
        }
        cx.notify();
    }

    fn build_form(&mut self, profile: &Profile, window: &mut Window, cx: &mut Context<Self>) {
        let name = forms::text_state(&profile.player.display_name, "", window, cx);
        let level = forms::text_state(&profile.player.level.to_string(), "", window, cx);
        let experience = forms::text_state(&profile.player.experience.to_string(), "", window, cx);
        let country = forms::text_state(&profile.settings.country, &t("accounts.regionPlaceholder"), window, cx);
        let language = forms::select_state(account_languages(), &profile.settings.language, window, cx);
        let icon_query = forms::text_state("", &t("accounts.searchIcon"), window, cx);
        let emblem_query = forms::text_state("", &t("accounts.searchEmblem"), window, cx);
        for state in [&icon_query, &emblem_query] {
            self._subs.push(cx.subscribe(state, |_, _, e: &InputEvent, cx| if matches!(e, InputEvent::Change) { cx.notify() }));
        }
        let emblem_ids = profile.emblem_ids.clone().unwrap_or_default();
        self.form = Some(ProfileForm {
            name, level, experience, country, language, icon_id: profile.settings.icon_id.clone(), icon_query, emblem_query,
            active_slot: emblem_ids.len().min(2), emblem_ids, pictures_open: false, emblems_open: false, saving: false,
        });
        if self.icons.is_empty() {
            spawn_api(cx, &self.api, |api| api.catalog("/api/catalog/profile-icons"), |this, result, cx| match result { Ok(icons) => this.icons = icons, Err(e) => error(cx, e) });
        }
        let id = profile.player.id.clone();
        self.emblems = None;
        spawn_api(cx, &self.api, move |api| Ok((api.catalog("/api/catalog/profile-emblems")?, api.inventory(&id, "emblems", None)?)), |this, result, cx| match result {
            Ok((catalog, owned)) => {
                let owned: HashSet<_> = owned.into_iter().filter(|a| a.quantity > 0).map(|a| a.id).collect();
                this.emblems = Some(catalog.into_iter().filter(|e| owned.contains(&e.id)).collect());
            }
            Err(e) => { this.emblems = Some(Vec::new()); error(cx, e) }
        });
    }

    fn build_cards(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let query = forms::text_state("", &t("accounts.catalogSearch"), window, cx);
        let languages = card_languages().into_iter().map(|(id, name)| Choice::new(id.to_string(), name)).collect();
        let language = forms::select_state(languages, "4", window, cx);
        let expansion = forms::select_state(vec![Choice::new("", t("accounts.all"))], "", window, cx);
        let rarity = forms::select_state(vec![Choice::new("0", t("accounts.all"))], "0", window, cx);
        self._subs.push(cx.subscribe_in(&query, window, |this, _, e: &InputEvent, window, cx| if matches!(e, InputEvent::Change) {
            if let Some(cards) = this.cards.as_mut() { cards.page = 1 }
            this.load_card_page(true, window, cx)
        }));
        self._subs.push(cx.subscribe_in(&language, window, |this, _, e: &SelectEvent<_>, window, cx| if let SelectEvent::Confirm(Some(v)) = e {
            if let Some(cards) = this.cards.as_mut() { cards.language_id = v.parse().unwrap_or(4) }
            this.load_card_inventory(window, cx)
        }));
        self._subs.push(cx.subscribe_in(&expansion, window, |this, _, e: &SelectEvent<_>, window, cx| { let SelectEvent::Confirm(v) = e;
            if let Some(cards) = this.cards.as_mut() { cards.expansion_id = v.clone().map(|v| v.to_string()).unwrap_or_default(); cards.page = 1 }
            this.load_card_page(false, window, cx)
        }));
        self._subs.push(cx.subscribe_in(&rarity, window, |this, _, e: &SelectEvent<_>, window, cx| { let SelectEvent::Confirm(v) = e;
            if let Some(cards) = this.cards.as_mut() { cards.rarity_id = v.as_ref().and_then(|v| v.parse().ok()).unwrap_or(0); cards.page = 1 }
            this.load_card_page(false, window, cx)
        }));
        self.cards = Some(CardsState { query, language, expansion, rarity, language_id: 4, expansion_id: String::new(), rarity_id: 0, page: 1, catalog: None, owned: None, totals: HashMap::new(), drafts: HashMap::new(), busy_id: String::new(), filter_signature: String::new(), _debounce: Task::ready(()) });
        self.load_card_inventory(window, cx);
        self.load_card_page(false, window, cx);
    }

    fn load_card_inventory(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(cards) = self.cards.as_mut() else { return };
        cards.owned = None;
        cards.drafts.clear();
        let (id, language) = (self.selected.clone(), cards.language_id);
        spawn_api_in(window, cx, &self.api, move |api| api.inventory(&id, "cards", Some(language)), |this, result, window, cx| match result {
            Ok(amounts) => {
                if let Some(cards) = this.cards.as_mut() {
                    cards.totals = amounts.iter().map(|a| (a.id.clone(), a.total_quantity.unwrap_or(a.quantity))).collect();
                    cards.owned = Some(amounts.into_iter().map(|a| (a.id, a.quantity)).collect());
                }
                this.sync_drafts(window, cx);
            }
            Err(e) => error(cx, e),
        });
    }

    fn load_card_page(&mut self, debounce: bool, _window: &mut Window, cx: &mut Context<Self>) {
        let Some(cards) = self.cards.as_mut() else { return };
        if debounce {
            cards._debounce = after(cx, Duration::from_millis(180), |this: &mut Self, cx| this.fetch_card_page(cx));
            return;
        }
        self.fetch_card_page(cx);
    }

    fn fetch_card_page(&mut self, cx: &mut Context<Self>) {
        let Some(cards) = self.cards.as_ref() else { return };
        let (query, expansion, rarity, page) = (forms::text(&cards.query, cx), cards.expansion_id.clone(), cards.rarity_id, cards.page);
        spawn_api(cx, &self.api, move |api| api.card_page(&query, &expansion, rarity, page), |this, result, cx| match result {
            Ok(catalog) => { if let Some(cards) = this.cards.as_mut() { cards.catalog = Some(catalog) } this.pending_sync = true; }
            Err(e) => error(cx, e),
        });
    }

    /// Creates the per-card quantity fields for the visible page.
    fn sync_drafts(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(cards) = self.cards.as_mut() else { return };
        if let Some(catalog) = &cards.catalog {
            let expansions = catalog.expansions.clone().unwrap_or_default();
            let rarities = catalog.rarities.clone().unwrap_or_default();
            let signature = format!("{:?}|{:?}", expansions.iter().map(|e| &e.id).collect::<Vec<_>>(), rarities);
            if signature != cards.filter_signature {
                cards.filter_signature = signature;
                let mut choices = vec![Choice::new("", t("accounts.all"))];
                choices.extend(expansions.iter().map(|e| Choice::new(e.id.clone(), format!("{} · {}", e.name, e.id))));
                forms::set_choices(&cards.expansion, choices, &cards.expansion_id, window, cx);
                let mut choices = vec![Choice::new("0", t("accounts.all"))];
                choices.extend(rarities.iter().map(|r| Choice::new(r.to_string(), format!("{} {r}", t("accounts.rarity")))));
                forms::set_choices(&cards.rarity, choices, &cards.rarity_id.to_string(), window, cx);
            }
        }
        let (Some(catalog), Some(owned)) = (&cards.catalog, &cards.owned) else { return };
        for card in catalog.items.clone().unwrap_or_default() {
            let value = owned.get(&card.id).copied().unwrap_or(0).to_string();
            cards.drafts.entry(card.id.clone()).or_insert_with(|| forms::text_state(&value, "", window, cx));
        }
    }

    fn set_card_quantity(&mut self, card: CatalogResult, quantity: i64, cx: &mut Context<Self>) {
        let Some(cards) = self.cards.as_mut() else { return };
        if cards.owned.is_none() { return }
        let previous = cards.totals.get(&card.id).copied().unwrap_or(0);
        cards.busy_id = card.id.clone();
        let (player, language) = (self.selected.clone(), cards.language_id);
        let card_id = card.id.clone();
        spawn_api(cx, &self.api, move |api| api.update_stock(&player, "cards", &card_id, quantity.max(0), Some(language)), move |this, result, cx| {
            match result {
                Ok(amount) => {
                    let total = amount.total_quantity.unwrap_or(amount.quantity);
                    if let Some(cards) = this.cards.as_mut() {
                        if let Some(owned) = cards.owned.as_mut() { if amount.quantity > 0 { owned.insert(amount.id.clone(), amount.quantity); } else { owned.remove(&amount.id); } }
                        if total > 0 { cards.totals.insert(amount.id.clone(), total); } else { cards.totals.remove(&amount.id); }
                        cards.drafts.remove(&amount.id);
                    }
                    if (previous == 0) != (total == 0) {
                        if let Some(profile) = this.profiles.get_mut(&this.selected) { profile.card_count = (profile.card_count + if total > 0 { 1 } else { -1 }).max(0) }
                    }
                    this.pending_sync = true;
                }
                Err(e) => error(cx, e),
            }
            if let Some(cards) = this.cards.as_mut() { cards.busy_id.clear() }
        });
    }

    fn save_profile(&mut self, cx: &mut Context<Self>) {
        let Some(form) = self.form.as_mut() else { return };
        let level = forms::number(&form.level, 1, 60, cx).unwrap_or(1);
        let experience = forms::number(&form.experience, 0, i64::MAX, cx).unwrap_or(0);
        let body = serde_json::json!({
            "DisplayName": forms::text(&form.name, cx), "Language": form.language.read(cx).selected_value().map(|v| v.to_string()).unwrap_or_default(),
            "Country": forms::text(&form.country, cx).to_uppercase(), "Level": level, "Experience": experience,
            "IconID": form.icon_id, "EmblemIDs": form.emblem_ids,
        });
        form.saving = true;
        let id = self.selected.clone();
        spawn_api(cx, &self.api, move |api| api.update_profile(&id, body), |this, result, cx| {
            match result {
                Ok(profile) => { this.profiles.insert(profile.player.id.clone(), profile); cx.emit(PageEvent::Refresh) }
                Err(e) => error(cx, e),
            }
            if let Some(form) = this.form.as_mut() { form.saving = false }
        });
    }

    fn apply_resource(&mut self, item: VisualCatalogItem, cx: &mut Context<Self>) {
        let Some((state, row)) = self.rows.get_mut(&item.id) else { return };
        let Some(quantity) = forms::number(state, 0, i64::MAX, cx) else { return };
        *row = RowState::Busy;
        let player = self.selected.clone();
        spawn_api(cx, &self.api, move |api| api.update_stock(&player, &item.stock_kind, &item.id, quantity, None), |this, result, cx| match result {
            Ok(amount) => {
                if let Some(owned) = this.owned.as_mut() { owned.insert(amount.id.clone(), amount.quantity); }
                if let Some((_, row)) = this.rows.get_mut(&amount.id) { *row = RowState::Saved }
            }
            Err(e) => { for (_, row) in this.rows.values_mut() { if *row == RowState::Busy { *row = RowState::Idle } } error(cx, e) }
        });
    }

    fn toggle_peripheral(&mut self, item: VisualCatalogItem, cx: &mut Context<Self>) {
        let Some(owned) = self.owned.as_ref() else { return };
        let quantity = if owned.contains_key(&item.id) { 0 } else { 1 };
        self.peripheral_busy = item.id.clone();
        let player = self.selected.clone();
        spawn_api(cx, &self.api, move |api| api.update_stock(&player, "items", &item.id, quantity, None), |this, result, cx| {
            match result {
                Ok(amount) => if let Some(owned) = this.owned.as_mut() { if amount.quantity > 0 { owned.insert(amount.id, amount.quantity); } else { owned.remove(&amount.id); } },
                Err(e) => error(cx, e),
            }
            this.peripheral_busy.clear();
        });
    }

    fn open_account(&mut self, cx: &mut Context<Self>) {
        if self.selected.is_empty() { return }
        self.busy = true;
        let id = self.selected.clone();
        // The shell shows the progress (Local may be enabled first) and refreshes at the end.
        cx.emit(PageEvent::Launching(true));
        spawn_api(cx, &self.api, move |api| api.open_player(&id), |this, result, cx| {
            this.busy = false;
            // Refreshing clears errors: report the failure after it starts.
            cx.emit(PageEvent::Launching(false));
            if let Err(e) = result { error(cx, e) }
        });
    }

    fn duplicate(&mut self, id: String, window: &mut Window, cx: &mut Context<Self>) {
        self.busy = true;
        spawn_api_in(window, cx, &self.api, move |api| api.duplicate_player(&id), |this, result, window, cx| {
            this.busy = false;
            match result {
                Ok(value) => {
                    if let Some(id) = value.get("ID").and_then(|v| v.as_str()) { this.selected = id.to_string(); this.load_profile(window, cx) }
                    cx.emit(PageEvent::Refresh)
                }
                Err(e) => error(cx, e),
            }
        });
    }

    fn remove(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(player) = self.deleting.clone() else { return };
        self.busy = true;
        let id = player.id.clone();
        spawn_api_in(window, cx, &self.api, move |api| api.delete_player(&id), move |this, result, window, cx| {
            this.busy = false;
            match result {
                Ok(_) => {
                    this.deleting = None;
                    this.selected = this.players.iter().find(|p| p.id != player.id).map(|p| p.id.clone()).unwrap_or_default();
                    this.players.retain(|p| p.id != player.id);
                    this.load_profile(window, cx);
                    cx.emit(PageEvent::Refresh)
                }
                Err(e) => error(cx, e),
            }
        });
    }

    fn drop_player(&mut self, moved: &str, target: &str, cx: &mut Context<Self>) {
        if moved == target { return }
        let Some(from) = self.players.iter().position(|p| p.id == moved) else { return };
        let Some(to) = self.players.iter().position(|p| p.id == target) else { return };
        let player = self.players.remove(from);
        // Moving down drops after the target, moving up drops before it.
        let index = self.players.iter().position(|p| p.id == target).unwrap_or(to) + usize::from(from < to);
        self.players.insert(index.min(self.players.len()), player);
        let ids: Vec<String> = self.players.iter().map(|p| p.id.clone()).collect();
        spawn_api(cx, &self.api, move |api| api.reorder_players(&ids), |_, result, cx| match result { Ok(_) => cx.emit(PageEvent::Refresh), Err(e) => error(cx, e) });
        cx.notify();
    }

    fn open_create(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        self.create = Some(CreateForm {
            name: forms::text_state(&t("accounts.newPlayer"), "", window, cx),
            country: forms::text_state("", &t("accounts.regionPlaceholder"), window, cx),
            level: forms::text_state("1", "", window, cx),
            experience: forms::text_state("0", "", window, cx),
            quantity: forms::text_state("2", "", window, cx),
            language: forms::select_state(account_languages(), "", window, cx),
            complete: false,
            busy: false,
        });
        if let Some(form) = &self.create {
            for state in [&form.country] { self._subs.push(cx.subscribe(state, |_, _, e: &InputEvent, cx| if matches!(e, InputEvent::Change) { cx.notify() })) }
            self._subs.push(cx.subscribe(&form.language, |_, _, _: &SelectEvent<_>, cx| cx.notify()));
        }
        cx.notify();
    }

    fn submit_create(&mut self, cx: &mut Context<Self>) {
        let Some(form) = self.create.as_mut() else { return };
        let body = serde_json::json!({
            "DisplayName": forms::text(&form.name, cx), "Language": form.language.read(cx).selected_value().map(|v| v.to_string()).unwrap_or_default(),
            "Country": forms::text(&form.country, cx).to_uppercase(), "Level": forms::number(&form.level, 1, 60, cx).unwrap_or(1),
            "Experience": forms::number(&form.experience, 0, i64::MAX, cx).unwrap_or(0), "Preset": if form.complete { "complete" } else { "empty" },
            "CardQuantity": forms::number(&form.quantity, 1, 999_999, cx).unwrap_or(2),
        });
        form.busy = true;
        spawn_api(cx, &self.api, move |api| api.create_player(body), |this, result, cx| match result {
            Ok(value) => {
                this.create = None;
                if let Some(id) = value.get("ID").and_then(|v| v.as_str()) { this.selected = id.to_string() }
                this.form = None;
                cx.emit(PageEvent::Refresh);
            }
            Err(e) => { if let Some(form) = this.create.as_mut() { form.busy = false } error(cx, e) }
        });
    }
}

impl Render for AccountsPage {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let p = pal(cx);
        if self.pending_sync { self.pending_sync = false; self.sync_drafts(window, cx) }
        if self.form.is_none() && self.section == Section::Profile && self.profile().is_some() {
            let profile = self.profile().cloned().unwrap();
            self.build_form(&profile, window, cx);
        }
        // The rail collapses to avatars in the cards view, and whenever the window is narrow.
        let cards_focus = kit::narrow(cx);
        let rail = self.render_rail(&p, cards_focus, cx);
        let workspace = self.render_workspace(&p, window, cx);
        div().p(px(32.)).flex().flex_col()
            .child(div().mb(px(20.)).child(kit::heading(t("accounts.title"), &p)))
            .child(div().flex().min_h(px(680.)).overflow_hidden().rounded(px(12.)).border_1().border_color(p.line).bg(p.panel)
                .child(rail)
                .child(div().flex_1().min_w_0().overflow_hidden().child(workspace)))
            .when_some(self.render_create(&p, window, cx), |d, e| d.child(e))
            .when_some(self.render_delete(&p, window, cx), |d, e| d.child(e))
    }
}

impl AccountsPage {
    fn render_rail(&self, p: &Palette, compact: bool, cx: &mut Context<Self>) -> Div {
        let mut rail = div().flex().flex_col().flex_none().w(px(if compact { 58. } else if kit::compact(cx) { 220. } else { 252. })).border_r_1().border_color(p.line).bg(p.surface_soft);
        if !compact {
            rail = rail.child(div().px(px(20.)).py(px(16.)).border_b_1().border_color(p.line).text_size(px(14.)).font_weight(FontWeight::BOLD).child(tf("accounts.profileCount", &[("count", &self.players.len())])));
        }
        for (index, player) in self.players.iter().enumerate() {
            let selected = player.id == self.selected;
            let (id, target) = (player.id.clone(), player.id.clone());
            let avatar = div().size(px(36.)).flex_none().flex().items_center().justify_center().rounded(px(8.)).bg(rgb(0xDFE5DC)).text_color(rgb(0x315D50))
                .text_size(px(14.)).font_weight(FontWeight::EXTRA_BOLD).child(player.display_name.chars().next().map(|c| c.to_uppercase().to_string()).unwrap_or_default());
            let mut row = div().id(("account", index)).relative().flex().items_center().gap(px(4.)).w_full().p(px(if compact { 8. } else { 10. })).border_b_1().border_color(p.line)
                .when(compact, |d| d.justify_center().py(px(12.)))
                .when(selected && !compact, |d| d.bg(p.panel).border_1().border_color(p.brand)).when(selected && compact, |d| d.bg(p.panel))
                .when(!selected, |d| { let mut h = p.panel; h.a = 0.72; d.hover(move |s| s.bg(h)) })
                .drag_over::<DraggedPlayer>({ let b = p.brand_soft; move |s, _, _, _| s.bg(b) })
                .on_drop(cx.listener(move |this, drag: &DraggedPlayer, _, cx| this.drop_player(&drag.id, &target, cx)));
            if !compact {
                let drag = DraggedPlayer { id: player.id.clone(), name: player.display_name.clone() };
                row = row.child(div().id(("drag", index)).w(px(22.)).flex_none().flex().justify_center().cursor_grab().child(icon("grip-vertical", 16., rgb(0x9AA19C).into()))
                    .on_drag(drag, |drag, _, _, cx| cx.new(|_| drag.clone())));
            }
            let mut select = div().id(("select", index)).flex().flex_1().min_w_0().items_center().gap(px(10.)).cursor_pointer().when(compact, |d| d.flex_none()).child(avatar)
                .on_click(cx.listener(move |this, _, window, cx| this.select(id.clone(), window, cx)));
            if !compact {
                select = select.child(div().flex().flex_col().min_w_0()
                    .child(div().truncate().text_size(px(13.)).font_weight(FontWeight::BOLD).child(player.display_name.clone()))
                    .child(div().truncate().mt(px(2.)).text_size(px(11.)).text_color(p.muted).child(tf("accounts.level", &[("level", &player.level)])))
                    .when_some(player.in_use_on.clone(), |d, device| d.child(div().text_size(px(11.)).text_color(p.brand_dark).child(tf("accounts.inUseOn", &[("device", &self.device_name(&device))])))));
            }
            row = row.child(select);
            if !compact {
                let (dup_id, del) = (player.id.clone(), player.clone());
                let tool = |id: (&'static str, usize), name: &str, danger: bool| {
                    let (h, ink) = (kit::hover_tint(p), if danger { rgb(0xB91C1C).into() } else { p.ink });
                    div().id(id).size(px(28.)).flex().items_center().justify_center().rounded(px(6.)).cursor_pointer().hover(move |s| s.bg(h).text_color(ink)).child(icon(name, 15., p.muted))
                };
                row = row.child(div().flex().gap(px(4.))
                    .child(tool(("dup", index), "copy", false).on_click(cx.listener(move |this, _, window, cx| this.duplicate(dup_id.clone(), window, cx))))
                    .child(tool(("del", index), "trash", true).on_click(cx.listener(move |this, _, _, cx| { this.deleting = Some(del.clone()); cx.notify() }))));
            }
            rail = rail.child(row);
        }
        if self.players.is_empty() && !compact {
            rail = rail.child(div().flex().flex_col().items_center().p(px(40.)).text_color(p.muted).child(icon("user-round", 32., p.muted)).child(div().mt(px(12.)).text_size(px(14.)).text_color(p.ink).font_weight(FontWeight::BOLD).child(t("accounts.none"))));
        }
        let mut add = div().id("add-account").m(px(12.)).flex().items_center().justify_center().gap(px(8.)).min_h(px(40.)).px(px(12.)).rounded(px(8.)).border_1().border_color(p.brand).bg(p.panel)
            .text_size(px(12.)).font_weight(FontWeight::EXTRA_BOLD).text_color(p.brand_dark).cursor_pointer().hover({ let b = p.brand_soft; move |s| s.bg(b) })
            .child(icon("plus", 17., p.brand_dark))
            .on_click(cx.listener(|this, _, window, cx| this.open_create(window, cx)));
        if compact { add = add.mx_auto().size(px(36.)).min_h(px(36.)).px_0() } else { add = add.child(t("accounts.new")) }
        rail.child(div().flex().child(add))
    }

    fn render_workspace(&mut self, p: &Palette, window: &mut Window, cx: &mut Context<Self>) -> AnyElement {
        let Some(profile) = self.profile().cloned() else {
            let Some(player) = self.players.iter().find(|x| x.id == self.selected).cloned() else {
                return div().min_h(px(650.)).flex().flex_col().items_center().justify_center().text_color(p.muted).child(icon("user-round", 40., p.muted)).child(div().mt(px(12.)).text_size(px(18.)).font_weight(FontWeight::BOLD).text_color(p.ink).child(t("accounts.choose"))).into_any_element();
            };
            return div().min_h(px(650.)).child(div().flex().items_center().justify_between().min_h(px(108.)).px(px(24.)).py(px(20.)).border_b_1().border_color(p.line)
                    .child(div().flex().items_center().gap(px(16.)).child(div().size(px(56.)).rounded(px(12.)).bg(p.brand).text_color(p.on_brand).flex().items_center().justify_center().text_size(px(20.)).font_weight(FontWeight::EXTRA_BOLD).child(player.display_name.chars().next().unwrap_or(' ').to_uppercase().to_string()))
                        .child(div().flex().flex_col().child(div().text_size(px(20.)).font_weight(FontWeight::EXTRA_BOLD).child(player.display_name.clone())).child(div().mt(px(4.)).text_size(px(12.)).text_color(p.muted).child(tf("accounts.level", &[("level", &player.level)])))))
                    .child(div().rounded(px(6.)).bg(p.tint).px(px(12.)).py(px(8.)).text_size(px(12.)).font_weight(FontWeight::BOLD).text_color(p.muted).child(t("common.loading"))))
                .child(div().p(px(24.)).flex().flex_col().gap(px(16.)).child(kit::skeleton(p).h(px(48.)).w(relative(0.66))).child(kit::skeleton(p).h(px(48.)).w_full()).child(kit::skeleton(p).h(px(48.)).w(relative(0.8))))
                .into_any_element();
        };
        let selected_summary = self.players.iter().find(|x| x.id == self.selected).cloned();
        let used_elsewhere = selected_summary.and_then(|s| s.in_use_on).filter(|d| !d.is_empty() && *d != self.emulator);
        let launch_disabled = self.busy || !self.can_launch || used_elsewhere.is_some();
        let mut launch = kit::button("launch", Kind::Primary, t("accounts.launch"), Some("gamepad-2"), launch_disabled, p);
        if !launch_disabled { launch = launch.on_click(cx.listener(|this, _, _, cx| this.open_account(cx))) }
        let header = div().flex().flex_wrap().items_center().justify_between().gap(px(16.)).min_h(px(108.)).px(px(24.)).py(px(20.)).border_b_1().border_color(p.line)
            .child(div().flex().items_center().gap(px(16.))
                .child(div().size(px(56.)).rounded_full().overflow_hidden().child(kit::remote_image(&profile.profile_image_url, &self.api.base, p)))
                .child(div().flex().flex_col()
                    .child(div().text_size(px(20.)).font_weight(FontWeight::EXTRA_BOLD).child(profile.player.display_name.clone()))
                    .child(div().mt(px(4.)).text_size(px(12.)).text_color(p.muted).child(tf("accounts.cardCount", &[("level", &profile.player.level), ("count", &profile.card_count)])))))
            .child(div().flex().items_center().gap(px(8.))
                .when_some(used_elsewhere.clone(), |d, device| d.child(div().text_size(px(11.)).text_color(p.brand_dark).child(tf("accounts.inUseOn", &[("device", &self.device_name(&device))]))))
                .child(launch));
        let tab = |section: Section, id: &'static str, name: &str, label: String, cx: &mut Context<Self>| {
            let active = self.section == section;
            let fg = if active { p.brand_dark } else { p.muted };
            div().id(id).relative().flex().items_center().gap(px(8.)).h_full().px(px(16.)).text_size(px(12.)).font_weight(FontWeight::BOLD).text_color(fg).cursor_pointer()
                .child(icon(name, 16., fg)).child(label)
                .when(active, |d| d.child(div().absolute().left(px(8.)).right(px(8.)).bottom_0().h(px(2.)).bg(p.brand)))
                .on_click(cx.listener(move |this, _, window, cx| this.open_section(section, window, cx)))
        };
        let tabs = div().id("account-tabs").flex().items_end().gap(px(4.)).h(px(58.)).overflow_x_scroll().px(px(24.)).border_b_1().border_color(p.line)
            .child(tab(Section::Profile, "tab-profile", "user-round", t("accounts.profile"), cx))
            .child(tab(Section::Resources, "tab-resources", "sparkles", t("accounts.resources"), cx))
            .child(tab(Section::Cards, "tab-cards", "wallet-cards", t("accounts.cards"), cx))
            .child(tab(Section::Battle, "tab-battle", "shield", t("accounts.battle"), cx))
            .child(tab(Section::Showcase, "tab-showcase", "image", t("accounts.showcases"), cx));
        let body = match self.section {
            Section::Profile => self.render_profile(&profile, p, window, cx),
            Section::Resources => self.render_resources(p, cx),
            Section::Cards => self.render_cards(p, cx),
            Section::Battle | Section::Showcase => self.render_peripherals(p, cx),
        };
        div().child(header).child(tabs).child(div().p(px(24.)).min_h(px(600.)).child(body)).into_any_element()
    }

    fn render_profile(&mut self, profile: &Profile, p: &Palette, _: &mut Window, cx: &mut Context<Self>) -> AnyElement {
        let Some(form) = self.form.as_ref() else { return div().into_any_element() };
        let mut save = kit::button("save-profile", Kind::Primary, t(if form.saving { "accounts.saving" } else { "accounts.saveProfile" }), None, form.saving, p);
        if !form.saving { save = save.on_click(cx.listener(|this, _, _, cx| this.save_profile(cx))) }
        let next = if profile.required_experience > 0 { profile.required_experience.to_string() } else { t("common.maximum") };
        let fields = div().flex().gap(px(16.)).max_w(px(768.))
            .child(div().flex_1().child(kit::field(t("accounts.nickname"), forms::input(&form.name), None, p)))
            .child(div().flex_1().child(kit::field(tf("accounts.level", &[("level", &"")]).trim().to_string(), forms::input(&form.level), None, p)))
            .child(div().flex_1().child(kit::field(t("accounts.experience"), forms::input(&form.experience), Some(tf("accounts.nextLevel", &[("value", &next)])), p)));
        let locale = div().mt(px(16.)).flex().gap(px(16.)).max_w(px(672.))
            .child(div().flex_1().child(kit::field(t("accounts.gameLanguage"), forms::select(&form.language), None, p)))
            .child(div().flex_1().child(kit::field(t("accounts.region"), forms::input(&form.country), Some(t("accounts.regionHint")), p)));

        let fold = |id: &'static str, title: String, subtitle: Option<String>, tag: String, open: bool| div().id(id).flex().items_center().gap(px(16.)).py(px(20.)).cursor_pointer()
            .child(div().flex_1().min_w_0().child(div().text_size(px(14.)).font_weight(FontWeight::EXTRA_BOLD).child(title)).when_some(subtitle, |d, s| d.child(div().mt(px(4.)).text_size(px(11.)).text_color(p.muted).child(s))))
            .child(kit::badge(tag, p.tint, p.muted))
            .child(icon(if open { "chevron-up" } else { "chevron-down" }, 16., p.muted));

        // Profile picture.
        let query = forms::text(&form.icon_query, cx).to_lowercase();
        let icon_tiles = if form.pictures_open {
            self.icons.iter().filter(|x| format!("{}{}", x.name, x.id).to_lowercase().contains(&query)).map(|x| {
                let chosen = form.icon_id == x.id;
                let id = x.id.clone();
                div().id(SharedString::from(format!("icon-{}", x.id))).relative().flex().flex_col().items_center().justify_center().w(px(96.)).min_h(px(112.)).p(px(8.)).rounded(px(8.)).border_1().cursor_pointer()
                    .border_color(if chosen { p.brand } else { p.line }).bg(if chosen { p.brand_soft } else { p.panel })
                    .hover({ let (b, h) = (p.control_border, p.surface_hover); move |s| if chosen { s } else { s.border_color(b).bg(h) } })
                    .child(div().size(px(64.)).child(kit::remote_image(&x.image_url, &self.api.base, p)))
                    .child(div().mt(px(6.)).text_size(px(10.)).font_weight(FontWeight::SEMIBOLD).text_center().line_clamp(2).child(clean_name(&x.name)))
                    .when(chosen, |d| d.child(div().absolute().top(px(6.)).right(px(6.)).child(icon("check", 15., p.brand))))
                    .on_click(cx.listener(move |this, _, _, cx| { if let Some(f) = this.form.as_mut() { f.icon_id = id.clone() } cx.notify() }))
            }).collect::<Vec<_>>()
        } else { Vec::new() };
        let pictures = div().mt(px(28.)).border_t_1().border_color(p.line)
            .child(fold("fold-icons", t("accounts.profilePicture"), Some(tf("accounts.iconsAvailable", &[("count", &self.icons.len())])), t("common.modify"), form.pictures_open)
                .on_click(cx.listener(|this, _, _, cx| { if let Some(f) = this.form.as_mut() { f.pictures_open = !f.pictures_open } cx.notify() })))
            .when(form.pictures_open, |d| d.child(div().pb(px(24.))
                .child(div().flex().justify_end().child(div().w(px(220.)).child(forms::input(&form.icon_query).prefix(icon("search", 16., p.muted)))))
                .child(div().mt(px(16.)).flex().flex_wrap().gap(px(8.)).children(icon_tiles))));

        // Emblems.
        let emblem_query = forms::text(&form.emblem_query, cx).to_lowercase();
        let by_id: HashMap<String, VisualCatalogItem> = self.emblems.clone().unwrap_or_default().into_iter().map(|e| (e.id.clone(), e)).collect();
        let slots = (0..3).map(|index| {
            let chosen = form.emblem_ids.get(index).and_then(|id| by_id.get(id)).cloned();
            let active = form.active_slot == index;
            let available = index <= form.emblem_ids.len();
            let label = t(match index { 0 => "accounts.left", 1 => "accounts.center", _ => "accounts.right" });
            div().id(("slot", index)).relative().flex_1().min_w_0().rounded(px(12.)).border_1()
                .border_color(if active { p.brand } else { p.line }).bg(if active { p.brand_soft } else if chosen.is_some() { p.panel } else { p.tint })
                .child(div().absolute().left(px(10.)).top(px(10.)).size(px(20.)).rounded_full().flex().items_center().justify_center().text_size(px(9.)).font_weight(FontWeight::EXTRA_BOLD)
                    .bg(if active { p.brand } else { p.surface_raised }).text_color(if active { p.on_brand } else { p.muted }).child((index + 1).to_string()))
                .child(div().flex().flex_col().items_center().justify_center().min_h(px(160.)).px(px(12.)).py(px(12.)).when(!available, |d| d.opacity(0.45))
                    .child(match &chosen {
                        Some(e) => div().flex().flex_col().items_center().child(div().size(px(96.)).child(kit::remote_image(&e.image_url, &self.api.base, p))).child(div().mt(px(6.)).max_w_full().truncate().text_size(px(10.)).font_weight(FontWeight::BOLD).child(clean_name(&e.name))),
                        None => div().flex().flex_col().items_center().gap(px(8.)).text_color(p.muted).child(icon("award", 32., p.muted)).child(div().text_size(px(10.)).font_weight(FontWeight::BOLD).child(label)),
                    }))
                .when(available, |d| d.cursor_pointer().on_click(cx.listener(move |this, _, _, cx| { if let Some(f) = this.form.as_mut() { f.active_slot = index } cx.notify() })))
                .when(chosen.is_some(), |d| d.child(div().id(("remove", index)).absolute().right(px(8.)).top(px(8.)).size(px(28.)).rounded(px(6.)).bg(p.panel).flex().items_center().justify_center().cursor_pointer()
                    .child(icon("x", 14., p.muted))
                    .on_click(cx.listener(move |this, _, _, cx| { cx.stop_propagation(); if let Some(f) = this.form.as_mut() { if index < f.emblem_ids.len() { f.emblem_ids.remove(index); } f.active_slot = index.saturating_sub(1) } cx.notify() }))))
        }).collect::<Vec<_>>();
        let emblem_body = match &self.emblems {
            None => div().mt(px(16.)).flex().gap(px(8.)).children((0..6).map(|_| kit::skeleton(p).flex_1().h(px(128.)))),
            Some(list) if list.is_empty() => div().mt(px(16.)).max_w(px(768.)).min_h(px(160.)).flex().flex_col().items_center().justify_center().rounded(px(8.)).bg(p.tint).text_color(p.muted)
                .child(icon("award", 28., p.muted)).child(div().mt(px(8.)).text_size(px(12.)).text_color(p.ink).font_weight(FontWeight::BOLD).child(t("accounts.noOwnedEmblems"))),
            Some(list) => div().mt(px(16.)).flex().flex_wrap().gap(px(8.)).children(list.iter().filter(|e| format!("{}{}", e.name, e.id).to_lowercase().contains(&emblem_query)).map(|e| {
                let position = form.emblem_ids.iter().position(|x| x == &e.id);
                let id = e.id.clone();
                div().id(SharedString::from(format!("emblem-{}", e.id))).relative().flex().flex_col().items_center().justify_center().w(px(124.)).min_h(px(128.)).p(px(8.)).rounded(px(8.)).border_1().cursor_pointer()
                    .border_color(if position.is_some() { p.brand } else { p.line }).bg(if position.is_some() { p.brand_soft } else { p.panel })
                    .child(div().size(px(80.)).child(kit::remote_image(&e.image_url, &self.api.base, p)))
                    .child(div().mt(px(6.)).text_size(px(10.)).font_weight(FontWeight::SEMIBOLD).text_center().line_clamp(2).child(clean_name(&e.name)))
                    .when_some(position, |d, pos| d.child(div().absolute().right(px(8.)).top(px(8.)).size(px(20.)).rounded_full().bg(p.brand).text_color(p.on_brand).text_size(px(9.)).flex().items_center().justify_center().child((pos + 1).to_string())))
                    .on_click(cx.listener(move |this, _, _, cx| {
                        if let Some(f) = this.form.as_mut() {
                            f.emblem_ids.retain(|x| x != &id);
                            let target = f.active_slot.min(f.emblem_ids.len());
                            f.emblem_ids.insert(target, id.clone());
                            f.emblem_ids.truncate(3);
                            f.active_slot = (f.active_slot + 1).min(2);
                        }
                        cx.notify()
                    }))
            })),
        };
        let emblems = div().mt(px(28.)).border_t_1().border_color(p.line)
            .child(fold("fold-emblems", t("accounts.emblems"), None, tf("accounts.selectedOfThree", &[("count", &form.emblem_ids.len())]), form.emblems_open)
                .on_click(cx.listener(|this, _, _, cx| { if let Some(f) = this.form.as_mut() { f.emblems_open = !f.emblems_open } cx.notify() })))
            .when(form.emblems_open, |d| d.child(div().pb(px(24.))
                .child(div().flex().justify_end().child(div().w(px(220.)).child(forms::input(&form.emblem_query).prefix(icon("search", 16., p.muted)))))
                .child(div().mt(px(16.)).flex().gap(px(12.)).max_w(px(768.)).children(slots))
                .child(emblem_body)));

        div().child(kit::section_intro(t("accounts.identity"), Some(save.into_any_element()), p)).child(fields).child(locale).child(pictures).child(emblems).into_any_element()
    }

    fn render_resources(&mut self, p: &Palette, cx: &mut Context<Self>) -> AnyElement {
        let intro = kit::section_intro(t("accounts.resourcesTitle"), Some(kit::count_label(tf("accounts.resourceCount", &[("count", &self.resources.len())]), p).into_any_element()), p);
        if self.owned.is_none() { return div().child(intro).child(inventory_skeleton(7, p)).into_any_element() }
        let order: HashMap<&str, usize> = PRIMARY_RESOURCES.iter().enumerate().map(|(i, id)| (*id, i)).collect();
        let mut primary: Vec<_> = self.resources.iter().filter(|r| order.contains_key(r.id.as_str())).cloned().collect();
        primary.sort_by_key(|r| order[r.id.as_str()]);
        let secondary: Vec<_> = self.resources.iter().filter(|r| !order.contains_key(r.id.as_str())).cloned().collect();
        let row = |item: VisualCatalogItem, cx: &mut Context<Self>| {
            let Some((state, row_state)) = self.rows.get(&item.id) else { return div() };
            let row_state = *row_state;
            let label = t(match row_state { RowState::Busy => "common.applying", RowState::Saved => "common.applied", RowState::Idle => "common.apply" });
            let mut apply = kit::button(SharedString::from(format!("apply-{}", item.id)), Kind::Secondary, label, None, row_state == RowState::Busy, p).w(px(108.));
            if row_state == RowState::Saved { apply = apply.border_color(p.brand).bg(p.brand_soft).text_color(p.brand_dark) }
            if row_state != RowState::Busy { let item = item.clone(); apply = apply.on_click(cx.listener(move |this, _, _, cx| this.apply_resource(item.clone(), cx))) }
            div().flex().items_center().gap(px(16.)).py(px(16.)).border_b_1().border_color(p.line)
                .child(div().size(px(64.)).flex_none().rounded(px(8.)).bg(p.tint).flex().items_center().justify_center().child(div().size(px(48.)).child(kit::remote_image(&item.image_url, &self.api.base, p))))
                .child(div().flex_1().min_w_0().child(div().text_size(px(14.)).font_weight(FontWeight::BOLD).child(item.name.clone())).child(div().mt(px(2.)).text_size(px(9.)).font_weight(FontWeight::SEMIBOLD).text_color(p.muted).child(item.id.clone())))
                .child(div().w(px(130.)).flex_none().child(div().mb(px(4.)).text_size(px(10.)).font_weight(FontWeight::BOLD).text_color(p.muted).child(t("accounts.quantity"))).child(forms::input(state)))
                .child(apply)
        };
        let section = |title: String, items: Vec<VisualCatalogItem>, highlight: bool, top: bool, cx: &mut Context<Self>| {
            let count = items.len();
            div().max_w(px(1024.)).when(top, |d| d.mt(px(40.)).pt(px(32.)).border_t_1().border_color(p.line))
                .child(div().mb(px(12.)).flex().items_end().justify_between().child(div().text_size(px(14.)).font_weight(FontWeight::EXTRA_BOLD).child(title))
                    .child(kit::badge(count.to_string(), if highlight { p.brand_soft } else { p.tint }, if highlight { p.brand_dark } else { p.muted })))
                .child(div().border_t_1().border_color(p.line).children(items.into_iter().map(|i| row(i, cx)).collect::<Vec<_>>()))
        };
        div().child(intro).child(section(t("accounts.currentResources"), primary, true, false, cx)).child(section(t("accounts.otherResources"), secondary, false, true, cx)).into_any_element()
    }

    fn render_cards(&mut self, p: &Palette, cx: &mut Context<Self>) -> AnyElement {
        let Some(cards) = self.cards.as_ref() else { return div().into_any_element() };
        let total = cards.catalog.as_ref().map(|c| c.total).unwrap_or(0);
        let toolbar = div().mb(px(20.)).flex().flex_wrap().items_end().gap(px(12.)).p(px(12.)).rounded(px(8.)).bg(p.tint)
            .child(div().flex_1().min_w(px(180.)).child(forms::input(&cards.query).prefix(icon("search", 18., p.muted))))
            .child(div().w(px(190.)).child(kit::field(t("accounts.cardLanguage"), forms::select(&cards.language), None, p)))
            .child(div().w(px(200.)).child(kit::field(t("accounts.expansion"), forms::select(&cards.expansion), None, p)))
            .child(div().w(px(150.)).child(kit::field(t("accounts.rarity"), forms::select(&cards.rarity), None, p)));
        let intro = kit::section_intro(t("accounts.collection"), Some(kit::count_label(tf("accounts.resultCount", &[("count", &total)]), p).into_any_element()), p);
        let (Some(catalog), Some(owned)) = (cards.catalog.as_ref(), cards.owned.as_ref()) else {
            return div().child(intro).child(toolbar).child(div().grid().grid_cols(card_columns(cx)).gap(px(7.)).children((0..20).map(|_| kit::skeleton(p).h(px(card_art_height(cx) + 100.))))).into_any_element();
        };
        let language_name = card_languages().into_iter().find(|(id, _)| *id == cards.language_id).map(|(_, n)| n).unwrap_or_default();
        let _ = language_name;
        let items = catalog.items.clone().unwrap_or_default();
        let art_height = card_art_height(cx);
        let grid = div().grid().grid_cols(card_columns(cx)).gap(px(7.)).children(items.into_iter().enumerate().map(|(index, card)| {
            let current = owned.get(&card.id).copied().unwrap_or(0);
            let draft_state = cards.drafts.get(&card.id).cloned();
            let draft = draft_state.as_ref().and_then(|s| forms::number(s, 0, 999_999, cx)).unwrap_or(current);
            let busy = cards.busy_id == card.id;
            let step = |id: (&'static str, usize), name: &str, disabled: bool| div().id(id).h(px(28.)).flex().items_center().justify_center().bg(p.tint).text_color(p.ink)
                .when(!disabled, |d| d.cursor_pointer().hover({ let b = p.brand_soft; move |s| s.bg(b) })).when(disabled, |d| d.opacity(0.55)).child(icon(name, 13., p.ink));
            let (minus_card, plus_card, set_card) = (card.clone(), card.clone(), card.clone());
            div().relative().min_w_0().rounded(px(8.)).border_1().p(px(6.))
                .border_color(if current > 0 { p.owned_border } else { p.line }).bg(if current > 0 { p.owned_bg } else { p.panel })
                .child(div().relative().w_full().h(px(art_height)).rounded(px(6.)).overflow_hidden().bg(p.tint).child(kit::remote_image(&card.image_url, &self.api.base, p))
                    .when(current > 0, |d| d.child(div().absolute().right(px(4.)).top(px(4.)).rounded(px(4.)).bg(p.brand).px(px(6.)).py(px(2.)).text_size(px(9.)).text_color(p.on_brand).font_weight(FontWeight::BOLD).child(format!("x{current}")))))
                .child(div().absolute().left(px(8.)).top(px(8.)).rounded(px(4.)).bg(p.panel).px(px(4.)).py(px(2.)).text_size(px(8.)).font_weight(FontWeight::EXTRA_BOLD).child(format!("R{}", card.rarity)))
                .child(div().mt(px(6.)).truncate().text_size(px(10.)).font_weight(FontWeight::BOLD).child(card.name.clone()))
                .child(div().mt(px(2.)).truncate().text_size(px(8.)).text_color(p.muted).child(card.meta.clone()))
                .child(div().mt(px(8.)).flex().overflow_hidden().rounded(px(6.)).border_1().border_color(p.line)
                    .child(step(("minus", index), "minus", busy || current == 0).w(px(24.)).when(!(busy || current == 0), |d| d.on_click(cx.listener(move |this, _, _, cx| this.set_card_quantity(minus_card.clone(), current - 1, cx)))))
                    .child(div().flex_1().min_w_0().when_some(draft_state, |d, s| d.child(forms::input(&s).small().appearance(false).text_center())))
                    .child(step(("plus", index), "plus", busy).w(px(24.)).when(!busy, |d| d.on_click(cx.listener(move |this, _, _, cx| this.set_card_quantity(plus_card.clone(), current + 1, cx))))))
                .child({
                    let disabled = busy || draft == current;
                    div().id(("set", index)).mt(px(4.)).h(px(28.)).w_full().rounded(px(6.)).flex().items_center().justify_center().text_size(px(9.)).font_weight(FontWeight::BOLD)
                        .bg(if disabled { p.tint } else { p.brand_soft }).text_color(if disabled { p.muted } else { p.brand_dark })
                        .child(if busy { "…".to_string() } else { t("common.set") })
                        .when(!disabled, |d| d.cursor_pointer().on_click(cx.listener(move |this, _, _, cx| this.set_card_quantity(set_card.clone(), draft, cx))))
                })
        }).collect::<Vec<_>>());
        let (page, pages) = (catalog.page, catalog.total_pages.max(1));
        let nav = |id: &'static str, label: String, name: &str, leading: bool, disabled: bool, delta: i64, cx: &mut Context<Self>| {
            let mut b = div().id(id).flex().items_center().gap(px(4.)).h(px(36.)).px(px(12.)).rounded(px(6.)).border_1().border_color(p.line).bg(p.panel).text_size(px(12.)).font_weight(FontWeight::BOLD);
            b = if leading { b.child(icon(name, 16., p.ink)).child(label) } else { b.child(label).child(icon(name, 16., p.ink)) };
            if disabled { b.opacity(0.55) } else {
                b.cursor_pointer().hover({ let h = p.surface_hover; move |s| s.bg(h) }).on_click(cx.listener(move |this, _, window, cx| {
                    if let Some(cards) = this.cards.as_mut() { cards.page += delta }
                    this.load_card_page(false, window, cx)
                }))
            }
        };
        let pagination = div().mt(px(20.)).pt(px(16.)).border_t_1().border_color(p.line).flex().items_center().justify_center().gap(px(20.))
            .child(nav("prev", t("common.previous"), "chevron-left", true, page <= 1, -1, cx))
            .child(div().text_size(px(12.)).text_color(p.muted).child(tf("common.pageOf", &[("page", &page), ("pages", &pages)])))
            .child(nav("next", t("common.next"), "chevron-right", false, page >= pages, 1, cx));
        div().child(intro).child(toolbar).child(grid).child(pagination).into_any_element()
    }

    fn render_peripherals(&mut self, p: &Palette, cx: &mut Context<Self>) -> AnyElement {
        let battle = self.section == Section::Battle;
        let types: Vec<(i64, String)> = if battle {
            vec![(-1, t("accounts.allTypes")), (0, t("accounts.playmats")), (1, t("accounts.sleeves")), (2, t("accounts.coins"))]
        } else {
            vec![(-1, t("accounts.allTypes")), (3, t("accounts.binders")), (4, t("accounts.displayBoards"))]
        };
        let query = self.peripheral_query.as_ref().map(|q| forms::text(q, cx).to_lowercase()).unwrap_or_default();
        let visible: Vec<_> = self.peripherals.iter().filter(|x| (self.peripheral_filter < 0 || x.variant == self.peripheral_filter) && format!("{}{}", x.name, x.id).to_lowercase().contains(&query)).cloned().collect();
        let intro = kit::section_intro(t(if battle { "accounts.battleCosmetics" } else { "accounts.showcaseCosmetics" }), Some(kit::count_label(tf("accounts.itemCount", &[("count", &visible.len())]), p).into_any_element()), p);
        let chips = types.into_iter().map(|(id, name)| kit::chip(SharedString::from(format!("type-{id}")), name, self.peripheral_filter == id, p)
            .on_click(cx.listener(move |this, _, _, cx| { this.peripheral_filter = id; cx.notify() }))).collect::<Vec<_>>();
        let filter = div().mb(px(20.)).flex().flex_wrap().items_center().justify_between().gap(px(12.)).p(px(12.)).rounded(px(8.)).bg(p.tint)
            .when_some(self.peripheral_query.clone(), |d, q| d.child(div().w(px(288.)).child(forms::input(&q).prefix(icon("search", 18., p.muted)))))
            .child(div().flex().flex_wrap().gap(px(8.)).children(chips));
        let Some(owned) = self.owned.as_ref() else { return div().child(intro).child(filter).child(inventory_skeleton(5, p)).into_any_element() };
        let grid = div().flex().flex_wrap().gap(px(8.)).children(visible.into_iter().map(|item| {
            let has = owned.contains_key(&item.id);
            let busy = self.peripheral_busy == item.id;
            let toggle = item.clone();
            let mut button = kit::button(SharedString::from(format!("toggle-{}", item.id)), if has { Kind::Quiet } else { Kind::Secondary }, t(if has { "common.remove" } else { "common.add" }), None, busy, p).mt(px(8.)).w_full();
            if !busy { button = button.on_click(cx.listener(move |this, _, _, cx| this.toggle_peripheral(toggle.clone(), cx))) }
            div().w(px(168.)).min_w_0().rounded(px(8.)).border_1().p(px(10.)).border_color(if has { p.owned_border } else { p.line }).bg(if has { p.owned_bg } else { p.panel })
                .child(div().relative().h(px(112.)).rounded(px(6.)).overflow_hidden().bg(p.tint).p(px(8.)).child(kit::remote_image(&item.image_url, &self.api.base, p))
                    .when(has, |d| d.child(div().absolute().bottom(px(6.)).right(px(6.)).flex().items_center().gap(px(4.)).rounded(px(4.)).bg(p.brand).px(px(6.)).py(px(4.)).text_size(px(8.)).font_weight(FontWeight::EXTRA_BOLD).text_color(p.on_brand)
                        .child(icon("check", 14., p.on_brand)).child(t("accounts.owned").to_uppercase()))))
                .child(div().mt(px(8.)).truncate().text_size(px(12.)).font_weight(FontWeight::BOLD).child(clean_name(&item.name)))
                .child(div().mt(px(2.)).truncate().text_size(px(8.)).text_color(p.muted).child(item.id.clone()))
                .child(button)
        }).collect::<Vec<_>>());
        div().child(intro).child(filter).child(grid).into_any_element()
    }

    fn render_create(&mut self, p: &Palette, window: &mut Window, cx: &mut Context<Self>) -> Option<AnyElement> {
        let form = self.create.as_ref()?;
        let language_set = form.language.read(cx).selected_value().map(|v| !v.is_empty()).unwrap_or(false);
        let country_ok = forms::text(&form.country, cx).trim().len() == 2;
        let disabled = form.busy || !language_set || !country_ok;
        let preset = |id: &'static str, name: &str, title: String, text: String, chosen: bool, complete: bool, cx: &mut Context<Self>| div().id(id).relative().flex_1().flex().flex_col().items_start().min_h(px(128.)).p(px(12.)).rounded(px(8.)).border_1().cursor_pointer()
            .border_color(if chosen { p.brand } else { p.line }).bg(if chosen { p.brand_soft } else { p.panel })
            .child(div().mb(px(12.)).child(icon(name, 24., p.brand)))
            .child(div().text_size(px(14.)).font_weight(FontWeight::BOLD).child(title)).child(div().mt(px(4.)).text_size(px(10.)).text_color(p.muted).child(text))
            .when(chosen, |d| d.child(div().absolute().right(px(10.)).top(px(10.)).child(icon("check", 16., p.brand))))
            .on_click(cx.listener(move |this, _, window, cx| {
                if let Some(f) = this.create.as_mut() {
                    f.complete = complete;
                    let (level, xp) = if complete { ("60", "2755") } else { ("1", "0") };
                    forms::set_text(&f.level.clone(), level, window, cx);
                    forms::set_text(&f.experience.clone(), xp, window, cx);
                }
                cx.notify()
            }));
        let mut submit = kit::button("create-submit", Kind::Primary, t(if form.busy { "accounts.creating" } else { "accounts.createAction" }), None, disabled, p);
        if !disabled { submit = submit.on_click(cx.listener(|this, _, _, cx| this.submit_create(cx))) }
        let dialog = div().w(px(600.)).rounded(px(12.)).bg(p.panel).p(px(24.)).shadow_2xl().flex().flex_col()
            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
            .child(div().mb(px(24.)).flex().items_start().justify_between().child(div().text_size(px(20.)).font_weight(FontWeight::EXTRA_BOLD).child(t("accounts.create")))
                .child(div().id("close-create").size(px(36.)).rounded(px(6.)).flex().items_center().justify_center().cursor_pointer().hover({ let h = kit::hover_tint(p); move |s| s.bg(h) }).child(icon("x", 18., p.muted))
                    .on_click(cx.listener(|this, _, _, cx| { this.create = None; cx.notify() }))))
            .child(div().mb(px(20.)).flex().gap(px(8.))
                .child(preset("preset-empty", "package-open", t("accounts.emptyAccount"), t("accounts.emptyInventory"), !form.complete, false, cx))
                .child(preset("preset-god", "layers", t("accounts.godAccount"), t("accounts.godInventory"), form.complete, true, cx)))
            .when(form.complete, |d| d.child(div().mb(px(16.)).flex().items_center().gap(px(12.)).rounded(px(8.)).bg(p.brand_soft).p(px(12.))
                .child(div().flex_1().text_size(px(12.)).font_weight(FontWeight::BOLD).child(t("accounts.copiesPerLanguage"))).child(div().w(px(140.)).child(forms::input(&form.quantity)))))
            .child(div().mb(px(16.)).child(kit::field(t("accounts.nickname"), forms::input(&form.name), None, p)))
            .child(div().flex().gap(px(16.))
                .child(div().flex_1().child(kit::field(t("accounts.gameLanguage"), forms::select(&form.language).placeholder(t("accounts.chooseLanguage")), None, p)))
                .child(div().flex_1().child(kit::field(t("accounts.region"), forms::input(&form.country), Some(t("accounts.regionHint")), p))))
            .child(div().mt(px(16.)).flex().gap(px(16.))
                .child(div().flex_1().child(kit::field(tf("accounts.level", &[("level", &"")]).trim().to_string(), forms::input(&form.level), None, p)))
                .child(div().flex_1().child(kit::field(t("accounts.experience"), forms::input(&form.experience), None, p))))
            .child(div().mt(px(24.)).flex().justify_end().gap(px(8.))
                .child(kit::button("create-cancel", Kind::Quiet, t("common.cancel"), None, false, p).on_click(cx.listener(|this, _, _, cx| { this.create = None; cx.notify() })))
                .child(submit));
        Some(modal(dialog, p, window, cx.listener(|this, _, _, cx| { this.create = None; cx.notify() })))
    }

    fn render_delete(&mut self, p: &Palette, window: &mut Window, cx: &mut Context<Self>) -> Option<AnyElement> {
        let player = self.deleting.clone()?;
        let mut confirm = kit::button("delete-confirm", Kind::Danger, t(if self.busy { "accounts.deleting" } else { "accounts.deleteForever" }), None, self.busy, p);
        if !self.busy { confirm = confirm.on_click(cx.listener(|this, _, window, cx| this.remove(window, cx))) }
        let dialog = div().w(px(420.)).rounded(px(12.)).bg(p.panel).p(px(24.)).shadow_2xl().flex().flex_col().items_center()
            .on_mouse_down(MouseButton::Left, |_, _, cx| cx.stop_propagation())
            .child(div().mb(px(16.)).size(px(48.)).rounded_full().bg(if p.dark { p.danger_soft } else { rgb(0xFEE2E2).into() }).flex().items_center().justify_center().child(icon("trash", 22., if p.dark { p.danger } else { rgb(0xB91C1C).into() })))
            .child(div().text_size(px(20.)).font_weight(FontWeight::EXTRA_BOLD).text_center().child(tf("accounts.deleteQuestion", &[("name", &player.display_name)])))
            .child(div().mt(px(8.)).text_size(px(14.)).text_color(p.muted).text_center().line_height(px(24.)).child(t("accounts.deleteWarning")))
            .child(div().mt(px(24.)).flex().justify_end().gap(px(8.))
                .child(kit::button("delete-cancel", Kind::Quiet, t("common.cancel"), None, false, p).on_click(cx.listener(|this, _, _, cx| { this.deleting = None; cx.notify() })))
                .child(confirm));
        Some(modal(dialog, p, window, cx.listener(|this, _, _, cx| { this.deleting = None; cx.notify() })))
    }
}

fn inventory_skeleton(rows: usize, p: &Palette) -> Div {
    div().max_w(px(1024.)).border_y_1().border_color(p.line).children((0..rows).map(|_| div().h(px(96.)).flex().items_center().gap(px(16.)).px(px(4.)).border_b_1().border_color(p.line)
        .child(kit::skeleton(p).size(px(56.))).child(div().flex_1().flex().flex_col().gap(px(8.)).child(kit::skeleton(p).h(px(16.)).w(relative(0.66))).child(kit::skeleton(p).h(px(16.)).w_full())).child(div().w(px(130.)))))
}

/// Full-window backdrop (`.dialog-backdrop`) above everything else.
pub fn modal(dialog: Div, p: &Palette, window: &mut Window, close: impl Fn(&MouseDownEvent, &mut Window, &mut App) + 'static) -> AnyElement {
    let size = window.viewport_size();
    deferred(anchored().position(point(px(0.), px(0.))).child(
        div().w(size.width).h(size.height).bg(p.backdrop).flex().items_center().justify_center().p(px(20.))
            .on_mouse_down(MouseButton::Left, close)
            .child(dialog),
    )).with_priority(2).into_any_element()
}

/// Cards per row in the collection grid: six at most, fewer when the window
/// is too narrow, so the art never gets squeezed.
/// Height of the card art for the current column width (web: aspect-ratio .72).
fn card_art_height(cx: &App) -> f32 {
    let columns = card_columns(cx) as f32;
    let card = (card_grid_width(cx) + 7.) / columns - 7.;
    // Card padding (6px each side) and border.
    ((card - 14.) / 0.72).max(80.)
}

fn card_grid_width(cx: &App) -> f32 {
    let sidebar = if kit::narrow(cx) { 72. } else if kit::compact(cx) { 208. } else { 244. };
    let rail = if kit::narrow(cx) { 58. } else if kit::compact(cx) { 220. } else { 252. };
    // Page padding, layout borders, the profile list and the workspace padding.
    kit::width(cx) - sidebar - 64. - 2. - rail - 48.
}

fn card_columns(cx: &App) -> u16 {
    ((card_grid_width(cx) + 7.) / 104.).floor().clamp(3., 6.) as u16
}
