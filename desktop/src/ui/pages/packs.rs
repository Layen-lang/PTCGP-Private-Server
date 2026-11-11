//! Port of `PacksPage.tsx`: the booster rule wizard and its live preview.

use crate::api::{Api, CardPage, CatalogResult, Pack, PackRule, PackStudio, RuleSlot};
use crate::i18n::{t, tf};
use crate::theme::Palette;
use crate::ui::app::PageEvent;
use crate::ui::forms;
use crate::ui::kit::{self, icon, pal};
use crate::ui::tasks::{after, spawn_api};
use gpui_kit::component::input::{InputEvent, InputState};
use gpui_kit::prelude::FluentBuilder as _;
use gpui_kit::*;
use std::collections::HashMap;
use std::time::Duration;

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Experience { Standard, Illegal }
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Path { Official, Forced }
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Content { Table, Rarity, Cards }
#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum Screen { Mode, Strategy, Illegal, Pack, Recipe, Detail, Cards, Review }

pub struct PacksPage {
    api: Api,
    studio: Option<PackStudio>,
    rule: Option<PackRule>,
    experience: Option<Experience>,
    path: Option<Path>,
    content: Option<Content>,
    screen: Screen,
    pack_query: Entity<InputState>,
    card_query: Entity<InputState>,
    seed: Entity<InputState>,
    return_count: Entity<InputState>,
    card_count: Entity<InputState>,
    card_rarity: i64,
    card_page: i64,
    catalog: Option<CardPage>,
    known: HashMap<String, CatalogResult>,
    pool: Option<i64>,
    pool_busy: bool,
    last_pack: String,
    busy: bool,
    saved: bool,
    /// Set when the rule changed outside the inputs; resynced on render.
    pending_inputs: bool,
    _debounce: Task<()>,
    _pool: Task<()>,
    _subs: Vec<Subscription>,
}

impl EventEmitter<PageEvent> for PacksPage {}

fn normalize(mut rule: PackRule, packs: &[Pack]) -> PackRule {
    let mut outputs = rule.packs.take().filter(|p| !p.is_empty()).unwrap_or_else(|| vec![Vec::new()]);
    if outputs.is_empty() { outputs.push(Vec::new()) }
    let locks = outputs.iter().any(|p| p.iter().any(|s| s.locked && !s.card_id.is_empty()));
    if rule.mode == "illegal" || !locks { rule.target_pack_id.clear() }
    let first = packs.first();
    if rule.table_kind.is_empty() { rule.table_kind = first.and_then(|p| p.table_kinds.as_ref()?.first().cloned()).unwrap_or_else(|| "normal".into()) }
    if rule.rarity.is_empty() { rule.rarity = first.and_then(|p| p.rarities.as_ref()?.first().cloned()).unwrap_or_else(|| "R".into()) }
    rule.return_pack_count = rule.return_pack_count.max(1).min(10);
    rule.packs = Some(outputs);
    rule.illegal.card_count = if rule.illegal.card_count == 0 { 5 } else { rule.illegal.card_count.clamp(1, 11) };
    rule.illegal.expansion_ids.get_or_insert_with(Vec::new);
    rule.illegal.rarities.get_or_insert_with(Vec::new);
    rule.illegal.card_kinds.get_or_insert_with(Vec::new);
    rule
}

fn all_values(packs: &[Pack], select: impl Fn(&Pack) -> Option<&Vec<String>>) -> Vec<String> {
    let mut out: Vec<String> = Vec::new();
    for pack in packs { for v in select(pack).into_iter().flatten() { if !out.contains(v) { out.push(v.clone()) } } }
    out
}

fn table_label(value: &str) -> String {
    match value { "normal" => t("packs.normalTable"), "rare" => t("packs.rareTable"), "plus1" => t("packs.bonusCard"), "guarantee" => t("packs.guaranteed"), "theme_rare" => t("packs.themeRare"), _ => value.to_string() }
}

fn screen_label(screen: Screen) -> String {
    t(match screen { Screen::Mode => "packs.mode", Screen::Strategy => "packs.drawType", Screen::Illegal => "packs.illegalPanel", Screen::Pack => "packs.pack", Screen::Recipe => "packs.composition", Screen::Detail => "packs.setting", Screen::Cards => "packs.cards", Screen::Review => "packs.activation" })
}

fn kind_label(value: &str) -> String {
    match value.to_lowercase().as_str() { "pokemon" => t("packs.pokemon"), "trainer" => t("packs.trainer"), _ => value.to_string() }
}

fn toggle<T: PartialEq + Clone>(values: &mut Vec<T>, value: T) {
    if let Some(i) = values.iter().position(|v| *v == value) { values.remove(i); } else { values.push(value) }
}

impl PacksPage {
    pub fn new(api: Api, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let pack_query = forms::text_state("", &t("packs.searchPack"), window, cx);
        let card_query = forms::text_state("", &t("packs.searchCard"), window, cx);
        let seed = forms::text_state("", &t("packs.newSeed"), window, cx);
        let return_count = forms::text_state("1", "", window, cx);
        let card_count = forms::text_state("5", "", window, cx);
        let mut subs = vec![
            cx.subscribe(&pack_query, |_, _, e: &InputEvent, cx| if matches!(e, InputEvent::Change) { cx.notify() }),
            cx.subscribe(&card_query, |this: &mut Self, _, e: &InputEvent, cx| if matches!(e, InputEvent::Change) { this.card_page = 1; this.load_cards(true, cx) }),
            cx.subscribe(&seed, |this: &mut Self, s, e: &InputEvent, cx| if matches!(e, InputEvent::Change) {
                let text = forms::text(&s, cx);
                this.update_rule(|r| r.fixed_seed = text.trim().parse().ok(), cx)
            }),
        ];
        subs.push(cx.subscribe(&return_count, |this: &mut Self, s, e: &InputEvent, cx| if matches!(e, InputEvent::Change) {
            if let Some(v) = forms::number(&s, 1, 10, cx) { this.update_rule(|r| r.return_pack_count = v, cx) }
        }));
        subs.push(cx.subscribe(&card_count, |this: &mut Self, s, e: &InputEvent, cx| if matches!(e, InputEvent::Change) {
            if let Some(v) = forms::number(&s, 1, 11, cx) { this.change_illegal(|i| i.card_count = v, cx) }
        }));
        let page = PacksPage {
            api, studio: None, rule: None, experience: None, path: None, content: None, screen: Screen::Mode, pack_query, card_query, seed, return_count, card_count,
            card_rarity: 0, card_page: 1, catalog: None, known: HashMap::new(), pool: None, pool_busy: false, last_pack: String::new(), busy: false, saved: false, pending_inputs: false,
            _debounce: Task::ready(()), _pool: Task::ready(()), _subs: subs,
        };
        spawn_api(cx, &page.api, |api| api.pack_studio(), |this, result, cx| match result {
            Ok(studio) => {
                let rule = normalize(studio.rule.clone(), &studio.packs);
                let locks = rule.packs.as_ref().map(|p| p.iter().any(|x| x.iter().any(|s| s.locked && !s.card_id.is_empty()))).unwrap_or(false);
                this.last_pack = if rule.target_pack_id.is_empty() { studio.packs.first().map(|p| p.id.clone()).unwrap_or_default() } else { rule.target_pack_id.clone() };
                if rule.mode == "illegal" { this.experience = Some(Experience::Illegal) } else {
                    this.experience = Some(Experience::Standard);
                    this.path = Some(if rule.mode == "official" { Path::Official } else { Path::Forced });
                    this.content = if rule.mode == "rarity" { Some(Content::Rarity) } else if locks { Some(Content::Cards) } else if rule.mode == "table" { Some(Content::Table) } else { None };
                }
                this.studio = Some(studio);
                this.rule = Some(rule);
                this.pending_inputs = true;
                this.fetch_missing(cx);
                if this.experience == Some(Experience::Illegal) { this.refresh_pool(cx) }
            }
            Err(e) => cx.emit(PageEvent::Error(e.to_string())),
        });
        page
    }

    fn update_rule(&mut self, f: impl FnOnce(&mut PackRule), cx: &mut Context<Self>) {
        if let Some(rule) = self.rule.as_mut() { f(rule); self.saved = false; cx.notify() }
    }

    fn change_illegal(&mut self, f: impl FnOnce(&mut crate::api::IllegalPackRule), cx: &mut Context<Self>) {
        self.pool = None;
        self.update_rule(|r| f(&mut r.illegal), cx);
        self.refresh_pool(cx);
    }

    fn refresh_pool(&mut self, cx: &mut Context<Self>) {
        let Some(rule) = self.rule.as_ref() else { return };
        if self.experience != Some(Experience::Illegal) { return }
        self.pool_busy = true;
        let illegal = rule.illegal.clone();
        self._pool = after(cx, Duration::from_millis(180), move |this: &mut Self, cx| {
            spawn_api(cx, &this.api, move |api| api.illegal_pool(&illegal), |this, result, cx| {
                this.pool_busy = false;
                match result { Ok(v) => this.pool = v.get("Count").and_then(|c| c.as_i64()), Err(e) => cx.emit(PageEvent::Error(e.to_string())) }
            });
        });
    }

    fn selected_pack(&self) -> Option<&Pack> {
        let studio = self.studio.as_ref()?;
        let rule = self.rule.as_ref()?;
        let id = if rule.target_pack_id.is_empty() { &self.last_pack } else { &rule.target_pack_id };
        studio.packs.iter().find(|p| &p.id == id).or(studio.packs.first())
    }

    fn slots(&self) -> Vec<RuleSlot> { self.rule.as_ref().and_then(|r| r.packs.as_ref()?.first().cloned()).unwrap_or_default() }

    fn load_cards(&mut self, debounce: bool, cx: &mut Context<Self>) {
        if self.screen != Screen::Cards { return }
        let delay = if debounce { 180 } else { 0 };
        self._debounce = after(cx, Duration::from_millis(delay), |this: &mut Self, cx| {
            let expansion = this.selected_pack().map(|p| p.expansion_id.clone()).unwrap_or_default();
            let (query, rarity, page) = (forms::text(&this.card_query, cx), this.card_rarity, this.card_page);
            spawn_api(cx, &this.api, move |api| api.card_page(&query, &expansion, rarity, page), |this, result, cx| match result {
                Ok(catalog) => { for c in catalog.items.clone().unwrap_or_default() { this.known.insert(c.id.clone(), c); } this.catalog = Some(catalog) }
                Err(e) => cx.emit(PageEvent::Error(e.to_string())),
            });
        });
    }

    fn fetch_missing(&mut self, cx: &mut Context<Self>) {
        let missing: Vec<String> = self.slots().into_iter().map(|s| s.card_id).filter(|id| !id.is_empty() && !self.known.contains_key(id)).collect();
        if missing.is_empty() { return }
        spawn_api(cx, &self.api, move |api| {
            let mut found = Vec::new();
            for id in missing { if let Some(card) = api.cards(&id, "")?.into_iter().find(|c| c.id == id) { found.push(card) } }
            Ok(found)
        }, |this, result, _| if let Ok(cards) = result { for c in cards { this.known.insert(c.id.clone(), c); } });
    }

    fn go(&mut self, screen: Screen, cx: &mut Context<Self>) {
        self.screen = screen;
        if screen == Screen::Cards { self.load_cards(false, cx) }
        cx.notify();
    }

    fn choose_experience(&mut self, value: Experience, cx: &mut Context<Self>) {
        if Some(value) == self.experience { return self.go(if value == Experience::Standard { Screen::Strategy } else { Screen::Illegal }, cx) }
        self.experience = Some(value);
        self.path = None;
        self.content = None;
        self.catalog = None;
        match value {
            Experience::Standard => { self.update_rule(|r| { r.mode = "official".into(); r.target_pack_id.clear(); r.return_pack_count = 1; r.packs = Some(vec![Vec::new()]) }, cx); self.go(Screen::Strategy, cx) }
            Experience::Illegal => {
                self.pool = None;
                self.update_rule(|r| { r.mode = "illegal".into(); r.target_pack_id.clear(); r.return_pack_count = r.return_pack_count.clamp(1, 10); r.illegal.card_count = r.illegal.card_count.clamp(1, 11); r.packs = Some(vec![Vec::new()]); r.free_openings = true }, cx);
                self.pending_inputs = true;
                self.refresh_pool(cx);
                self.go(Screen::Illegal, cx)
            }
        }
    }

    fn choose_path(&mut self, value: Path, cx: &mut Context<Self>) {
        let next = if value == Path::Official { Screen::Review } else { Screen::Recipe };
        if Some(value) == self.path { return self.go(next, cx) }
        self.path = Some(value);
        self.content = None;
        let tables = all_values(&self.studio.as_ref().map(|s| s.packs.clone()).unwrap_or_default(), |p| p.table_kinds.as_ref());
        self.update_rule(|r| {
            r.mode = if value == Path::Official { "official".into() } else { "table".into() };
            r.target_pack_id.clear();
            if value == Path::Forced { r.table_kind = tables.first().cloned().unwrap_or_else(|| "normal".into()) }
            r.return_pack_count = 1;
            r.packs = Some(vec![Vec::new()]);
        }, cx);
        self.go(next, cx)
    }

    fn choose_content(&mut self, value: Content, cx: &mut Context<Self>) {
        let next = if value == Content::Cards { Screen::Pack } else { Screen::Detail };
        if Some(value) == self.content { return self.go(next, cx) }
        self.content = Some(value);
        self.card_page = 1;
        self.catalog = None;
        let packs = self.studio.as_ref().map(|s| s.packs.clone()).unwrap_or_default();
        let (tables, rarities) = (all_values(&packs, |p| p.table_kinds.as_ref()), all_values(&packs, |p| p.rarities.as_ref()));
        let active = self.selected_pack().cloned();
        self.update_rule(|r| {
            r.mode = if value == Content::Rarity { "rarity".into() } else { "table".into() };
            r.target_pack_id = if value == Content::Cards { active.as_ref().map(|p| p.id.clone()).unwrap_or_default() } else { String::new() };
            let pool = if value == Content::Cards { active.as_ref().and_then(|p| p.table_kinds.clone()).unwrap_or_default() } else { tables.clone() };
            if !pool.contains(&r.table_kind) { r.table_kind = pool.first().cloned().unwrap_or_else(|| "normal".into()) }
            if !rarities.contains(&r.rarity) { r.rarity = rarities.first().cloned().unwrap_or_else(|| "R".into()) }
            r.packs = Some(vec![Vec::new()]);
        }, cx);
        self.go(next, cx)
    }

    fn choose_pack(&mut self, pack: Pack, cx: &mut Context<Self>) {
        self.last_pack = pack.id.clone();
        self.card_page = 1;
        self.catalog = None;
        let cards = self.content == Some(Content::Cards);
        self.update_rule(|r| {
            r.target_pack_id = pack.id.clone();
            let tables = pack.table_kinds.clone().unwrap_or_default();
            if !tables.contains(&r.table_kind) { r.table_kind = tables.first().cloned().unwrap_or_else(|| "normal".into()) }
            let rarities = pack.rarities.clone().unwrap_or_default();
            if !rarities.contains(&r.rarity) { r.rarity = rarities.first().cloned().unwrap_or_else(|| "R".into()) }
            if cards { r.packs = Some(vec![Vec::new()]) }
        }, cx);
        self.go(Screen::Cards, cx)
    }

    fn add_card(&mut self, card: CatalogResult, cx: &mut Context<Self>) {
        let count = self.slots().len();
        if count >= 5 { return }
        self.known.insert(card.id.clone(), card.clone());
        self.update_rule(|r| { let packs = r.packs.get_or_insert_with(|| vec![Vec::new()]); if packs.is_empty() { packs.push(Vec::new()) } packs[0].push(RuleSlot { card_id: card.id.clone(), locked: true }) }, cx);
        if count == 4 { self.go(Screen::Review, cx) }
    }

    fn remove_card(&mut self, index: usize, cx: &mut Context<Self>) {
        self.update_rule(|r| if let Some(first) = r.packs.as_mut().and_then(|p| p.first_mut()) { if index < first.len() { first.remove(index); } }, cx);
    }

    fn can_save(&self) -> bool {
        let Some(rule) = self.rule.as_ref() else { return false };
        match self.experience {
            Some(Experience::Standard) => self.path == Some(Path::Official) || (self.path == Some(Path::Forced) && self.content.is_some() && (self.content != Some(Content::Cards) || self.slots().len() == 5)),
            Some(Experience::Illegal) => self.pool.map(|pool| pool > 0 && (1..=10).contains(&rule.return_pack_count) && (1..=11).contains(&rule.illegal.card_count) && (rule.illegal.allow_duplicates || rule.illegal.card_count <= pool)).unwrap_or(false),
            None => false,
        }
    }

    fn save(&mut self, cx: &mut Context<Self>) {
        if !self.can_save() { return }
        let Some(rule) = self.rule.clone() else { return };
        self.busy = true;
        spawn_api(cx, &self.api, move |api| api.save_pack_rule(&rule), |this, result, cx| {
            this.busy = false;
            match result {
                Ok(rule) => { let packs = this.studio.as_ref().map(|s| s.packs.clone()).unwrap_or_default(); this.rule = Some(normalize(rule, &packs)); this.saved = true }
                Err(e) => cx.emit(PageEvent::Error(e.to_string())),
            }
        });
    }

    fn back(&mut self, cx: &mut Context<Self>) {
        let previous = match self.screen {
            Screen::Strategy | Screen::Illegal => Screen::Mode,
            Screen::Recipe => Screen::Strategy,
            Screen::Detail | Screen::Pack => Screen::Recipe,
            Screen::Cards => Screen::Pack,
            Screen::Review => if self.path == Some(Path::Official) { Screen::Strategy } else if self.content == Some(Content::Cards) { Screen::Cards } else { Screen::Detail },
            Screen::Mode => Screen::Mode,
        };
        self.go(previous, cx)
    }

    fn journey(&self) -> Vec<Screen> {
        let path = if self.screen == Screen::Strategy { Some(Path::Forced) } else { self.path };
        match self.experience {
            Some(Experience::Illegal) => vec![Screen::Mode, Screen::Illegal],
            Some(Experience::Standard) => match path {
                Some(Path::Official) => vec![Screen::Mode, Screen::Strategy, Screen::Review],
                Some(Path::Forced) => {
                    let mut j = vec![Screen::Mode, Screen::Strategy, Screen::Recipe];
                    match self.content { Some(Content::Cards) => j.extend([Screen::Pack, Screen::Cards, Screen::Review]), Some(_) => j.extend([Screen::Detail, Screen::Review]), None => {} }
                    j
                }
                None => vec![Screen::Mode, Screen::Strategy],
            },
            None => vec![Screen::Mode],
        }
    }

    fn completion(&self) -> String {
        let count = self.slots().len() as i64;
        match (self.experience, self.path, self.content) {
            (None, _, _) => t("packs.startChoice"),
            (_, None, _) => t("packs.force"),
            (_, Some(Path::Official), _) => t("packs.official"),
            (_, _, Some(Content::Table)) => t("packs.tableForced"),
            (_, _, Some(Content::Rarity)) => t("packs.rarityForced"),
            (_, _, Some(Content::Cards)) => if count == 5 { t("packs.compositionComplete") } else { tf("packs.cardsRemaining", &[("count", &(5 - count)), ("plural", &if 5 - count > 1 { "s" } else { "" })]) },
            _ => t("packs.continue"),
        }
    }
}

// Inputs whose values follow the loaded rule are synchronised on render (needs the window).
impl PacksPage {
    fn sync_inputs(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(rule) = self.rule.clone() else { return };
        forms::set_text(&self.return_count, &rule.return_pack_count.to_string(), window, cx);
        forms::set_text(&self.card_count, &rule.illegal.card_count.to_string(), window, cx);
        forms::set_text(&self.seed, &rule.fixed_seed.map(|s| s.to_string()).unwrap_or_default(), window, cx);
    }
}

fn step(number: AnyElement, title: String, hint: String, body: impl IntoElement, p: &Palette) -> Div {
    div().rounded(px(14.)).border_1().border_color(p.line).bg(p.panel)
        .child(div().flex().items_start().gap(px(16.)).p(px(24.)).border_b_1().border_color(p.line)
            .child(div().size(px(36.)).flex_none().rounded_full().bg(p.brand_soft).text_color(p.brand_dark).flex().items_center().justify_center().text_size(px(14.)).font_weight(FontWeight::EXTRA_BOLD).child(number))
            .child(div().flex().flex_col().gap(px(4.)).child(div().text_size(px(18.)).font_weight(FontWeight::EXTRA_BOLD).child(title)).child(div().text_size(px(12.)).text_color(p.muted).child(hint))))
        .child(div().p(px(24.)).child(body))
}

fn choice(id: &'static str, active: bool, name: &str, title: String, detail: String, accent: Hsla, p: &Palette) -> Stateful<Div> {
    div().id(id).flex().flex_1().min_w_0().overflow_hidden().items_center().gap(px(14.)).min_h(px(96.)).p(px(16.)).rounded(px(12.)).border_1().cursor_pointer()
        .border_color(if active { p.brand } else { p.line }).bg(if active { p.brand_soft } else { p.panel })
        .hover({ let (b, h) = (p.control_border, p.surface_hover); move |s| if active { s } else { s.border_color(b).bg(h) } })
        .child(div().size(px(40.)).flex_none().rounded(px(10.)).bg(if active { p.panel } else { p.brand_soft }).flex().items_center().justify_center().child(icon(name, 20., accent)))
        .child(div().flex_1().min_w_0().flex().flex_col().gap(px(4.)).child(div().text_size(px(14.)).font_weight(FontWeight::EXTRA_BOLD).child(title)).child(div().text_size(px(11.)).text_color(p.muted).line_height(px(16.)).child(detail)))
        .child(icon(if active { "check" } else { "chevron-right" }, 18., if active { p.brand } else { p.muted }))
}

fn switch_row(id: &'static str, on: bool, title: String, hint: String, p: &Palette) -> Stateful<Div> {
    div().id(id).flex().items_center().gap(px(12.)).cursor_pointer()
        .child(kit::switch(SharedString::from(format!("{id}-switch")), on, p))
        .child(div().flex().flex_col().child(div().text_size(px(12.)).font_weight(FontWeight::BOLD).child(title)).child(div().text_size(px(10.)).text_color(p.muted).child(hint)))
}

impl Render for PacksPage {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let p = pal(cx);
        if self.pending_inputs { self.pending_inputs = false; self.sync_inputs(window, cx) }
        let (Some(studio), Some(rule), Some(pack)) = (self.studio.clone(), self.rule.clone(), self.selected_pack().cloned()) else {
            return div().size_full().min_h(px(400.)).flex().items_center().justify_center().text_color(p.muted).child(t("packs.loading")).into_any_element();
        };
        let slots = self.slots();
        let journey = self.journey();
        let index = journey.iter().position(|s| *s == self.screen).unwrap_or(0);
        let number = (index + 1).to_string();
        let progress = (index + 1) as f32 / journey.len() as f32;
        let packs = studio.packs.clone();
        let tables = all_values(&packs, |x| x.table_kinds.as_ref());
        let rarities = all_values(&packs, |x| x.rarities.as_ref());
        let illegal = self.experience == Some(Experience::Illegal);
        let preview_title = if illegal { t("packs.illegalTitle") } else if self.path == Some(Path::Forced) && self.content == Some(Content::Cards) { pack.name.clone() } else { t("packs.allPacks") };
        let preview_detail = if illegal {
            tf("packs.previewIllegal", &[("packs", &rule.return_pack_count), ("packsPlural", &if rule.return_pack_count > 1 { "s" } else { "" }), ("cards", &rule.illegal.card_count), ("cardsPlural", &if rule.illegal.card_count > 1 { "s" } else { "" })])
        } else { self.completion() };
        let num = |text: String| div().child(text).into_any_element();

        // Header.
        let armed = div().flex().items_center().gap(px(8.)).px(px(12.)).py(px(8.)).rounded_full().border_1()
            .border_color(if self.saved { p.brand } else { p.line }).bg(if self.saved { p.brand_soft } else { p.panel })
            .child(div().size(px(8.)).rounded_full().bg(if self.saved { p.brand } else { p.neutral_dot }))
            .child(div().text_size(px(12.)).font_weight(FontWeight::BOLD).text_color(if self.saved { p.brand_dark } else { p.muted }).child(t(if self.saved { "packs.armed" } else { "packs.draft" })));
        let heading = div().mb(px(24.)).flex().items_center().justify_between().gap(px(24.))
            .child(div().flex().flex_col()
                .child(div().text_size(px(10.)).font_weight(FontWeight::EXTRA_BOLD).text_color(p.brand).child(t("packs.ruleAllAccounts").to_uppercase()))
                .child(kit::heading(t("packs.title"), &p))
                .child(div().mt(px(8.)).text_size(px(14.)).text_color(p.muted).child(t("packs.headingHint"))))
            .child(armed);

        // Wizard navigation.
        let nav_button = |id: &'static str, name: &str, label: String, disabled: bool| div().id(id).flex().items_center().gap(px(6.)).h(px(34.)).px(px(10.)).rounded(px(8.)).border_1().border_color(p.line).bg(p.panel)
            .text_size(px(12.)).font_weight(FontWeight::BOLD).child(icon(name, 15., p.ink)).child(label)
            .when(disabled, |d| d.opacity(0.55)).when(!disabled, |d| d.cursor_pointer().hover({ let h = p.surface_hover; move |s| s.bg(h) }));
        let at_start = self.screen == Screen::Mode;
        let nav = div().mb(px(16.)).flex().items_center().gap(px(16.)).p(px(12.)).rounded(px(12.)).border_1().border_color(p.line).bg(p.panel)
            .child(nav_button("back", "chevron-left", t("packs.back"), at_start).when(!at_start, |d| d.on_click(cx.listener(|this, _, _, cx| this.back(cx)))))
            .child(nav_button("restart", "rotate-ccw", t("packs.start"), at_start).when(!at_start, |d| d.on_click(cx.listener(|this, _, _, cx| this.go(Screen::Mode, cx)))))
            .child(div().flex().flex_col().child(div().text_size(px(10.)).text_color(p.muted).child(t(if illegal && self.screen == Screen::Illegal { "packs.freeform" } else { "packs.guided" })))
                .child(div().text_size(px(13.)).font_weight(FontWeight::EXTRA_BOLD).child(screen_label(self.screen))))
            .when(self.screen != Screen::Illegal, |d| d
                .child(div().flex_1().h(px(6.)).rounded_full().bg(p.surface_raised).child(div().h_full().rounded_full().bg(p.brand).w(relative(progress))))
                .child(div().text_size(px(12.)).font_weight(FontWeight::BOLD).child(format!("{}/{}", index + 1, journey.len()))));

        let content: AnyElement = match self.screen {
            Screen::Mode => step(num(number.clone()), t("packs.chooseMode"), t("packs.chooseModeHint"), div().flex().gap(px(12.)).when(kit::width(cx) <= 1180., |d| d.flex_col())
                .child(choice("standard", self.experience == Some(Experience::Standard), "shield-check", t("packs.normalMode"), t("packs.normalDetail"), p.brand, &p).on_click(cx.listener(|this, _, _, cx| this.choose_experience(Experience::Standard, cx))))
                .child(choice("illegal", illegal, "flame", t("packs.illegalMode"), t("packs.illegalDetail"), p.danger, &p).on_click(cx.listener(|this, _, _, cx| this.choose_experience(Experience::Illegal, cx)))), &p).into_any_element(),
            Screen::Strategy => step(num(number.clone()), t("packs.strategyTitle"), t("packs.strategyHint"), div().flex().gap(px(12.)).when(kit::width(cx) <= 1180., |d| d.flex_col())
                .child(choice("official", self.path == Some(Path::Official), "dices", t("packs.realOdds"), t("packs.realOddsHint"), p.brand, &p).on_click(cx.listener(|this, _, _, cx| this.choose_path(Path::Official, cx))))
                .child(choice("forced", self.path == Some(Path::Forced), "sparkles", t("packs.forceResult"), t("packs.forceResultHint"), p.brand, &p).on_click(cx.listener(|this, _, _, cx| this.choose_path(Path::Forced, cx)))), &p).into_any_element(),
            Screen::Recipe => step(num(number.clone()), t("packs.recipeTitle"), t("packs.recipeHint"), div().flex().flex_col().gap(px(10.))
                .child(choice("table", self.content == Some(Content::Table), "layers", t("packs.oneTable"), t("packs.oneTableHint"), p.brand, &p).on_click(cx.listener(|this, _, _, cx| this.choose_content(Content::Table, cx))))
                .child(choice("rarity", self.content == Some(Content::Rarity), "dices", t("packs.oneRarity"), t("packs.oneRarityHint"), p.brand, &p).on_click(cx.listener(|this, _, _, cx| this.choose_content(Content::Rarity, cx))))
                .child(choice("cards", self.content == Some(Content::Cards), "lock-keyhole", t("packs.fiveCards"), t("packs.fiveCardsHint"), p.brand, &p).on_click(cx.listener(|this, _, _, cx| this.choose_content(Content::Cards, cx)))), &p).into_any_element(),
            Screen::Detail => {
                let table = self.content == Some(Content::Table);
                let values = if table { tables.clone() } else { rarities.clone() };
                let chips = values.into_iter().enumerate().map(|(i, v)| {
                    let active = if table { rule.table_kind == v } else { rule.rarity == v };
                    let label = if table { table_label(&v) } else { v.clone() };
                    kit::chip(("detail", i), label, active, &p).min_h(px(44.)).px(px(16.)).when(active, |d| d.child(icon("check", 14., p.brand_dark)))
                        .on_click(cx.listener(move |this, _, _, cx| { let v = v.clone(); this.update_rule(|r| if table { r.table_kind = v } else { r.rarity = v }, cx); this.go(Screen::Review, cx) }))
                }).collect::<Vec<_>>();
                step(num(number.clone()), t(if table { "packs.tableQuestion" } else { "packs.rarityQuestion" }), t("packs.forceFallbackHint"), div().flex().flex_wrap().gap(px(10.)).children(chips), &p).into_any_element()
            }
            Screen::Pack => {
                let query = forms::text(&self.pack_query, cx).to_lowercase();
                let tiles = packs.iter().filter(|x| format!("{}{}{}", x.name, x.expansion_id, x.id).to_lowercase().contains(&query)).map(|x| {
                    let chosen = x.id == pack.id;
                    let choose = x.clone();
                    div().id(SharedString::from(format!("pack-{}", x.id))).relative().w(px(150.)).p(px(8.)).rounded(px(10.)).border_1().cursor_pointer()
                        .border_color(if chosen { p.brand } else { p.line }).bg(if chosen { p.brand_soft } else { p.panel })
                        .hover({ let (b, h) = (p.control_border, p.surface_hover); move |s| if chosen { s } else { s.border_color(b).bg(h) } })
                        .child(div().h(px(112.)).rounded(px(6.)).overflow_hidden().bg(p.tint).p(px(8.)).child(kit::remote_image(&x.image_url, &self.api.base, &p)))
                        .child(div().mt(px(8.)).truncate().text_size(px(11.)).font_weight(FontWeight::BOLD).child(x.name.clone()))
                        .child(div().mt(px(2.)).truncate().text_size(px(9.)).text_color(p.muted).child(x.expansion_id.clone()))
                        .when(chosen, |d| d.child(div().absolute().right(px(8.)).top(px(8.)).size(px(20.)).rounded_full().bg(p.brand).flex().items_center().justify_center().child(icon("check", 12., p.on_brand))))
                        .on_click(cx.listener(move |this, _, _, cx| this.choose_pack(choose.clone(), cx)))
                }).collect::<Vec<_>>();
                step(num(number.clone()), t("packs.pickPackTitle"), t("packs.pickPackHint"), div().flex().flex_col().gap(px(16.))
                    .child(div().w(px(320.)).child(forms::input(&self.pack_query).prefix(icon("search", 18., p.muted))))
                    .child(div().flex().flex_wrap().gap(px(8.)).children(tiles)), &p).into_any_element()
            }
            Screen::Cards => {
                let complete = slots.len() == 5;
                let library = match &self.catalog {
                    None => div().grid().grid_cols(if kit::narrow(cx) { 3 } else if kit::compact(cx) { 4 } else { 5 }).gap(px(8.)).children((0..15).map(|_| kit::skeleton(&p).h(px(170.)))),
                    Some(catalog) => {
                        let full = slots.len() >= 5;
                        let cards = catalog.items.clone().unwrap_or_default().into_iter().enumerate().map(|(i, card)| {
                            let count = slots.iter().filter(|s| s.card_id == card.id).count();
                            let add = card.clone();
                            div().id(("lib", i)).relative().min_w_0().p(px(8.)).rounded(px(8.)).border_1()
                                .border_color(if count > 0 { p.brand } else { p.line }).bg(if count > 0 { p.brand_soft } else { p.panel })
                                .when(full, |d| d.opacity(0.55)).when(!full, |d| d.cursor_pointer().hover({ let b = p.brand; move |s| s.border_color(b) }).on_click(cx.listener(move |this, _, _, cx| this.add_card(add.clone(), cx))))
                                .child(div().relative().h(px(120.)).rounded(px(6.)).overflow_hidden().bg(p.tint).child(kit::remote_image(&card.image_url, &self.api.base, &p))
                                    .when(count > 0, |d| d.child(div().absolute().right(px(4.)).top(px(4.)).rounded(px(4.)).bg(p.brand).px(px(6.)).py(px(2.)).text_size(px(9.)).font_weight(FontWeight::BOLD).text_color(p.on_brand).child(format!("x{count}")))))
                                .child(div().mt(px(6.)).truncate().text_size(px(10.)).font_weight(FontWeight::BOLD).child(card.name.clone()))
                                .child(div().flex().justify_between().child(div().truncate().text_size(px(8.)).text_color(p.muted).child(card.meta.clone())).child(div().text_size(px(8.)).font_weight(FontWeight::EXTRA_BOLD).child(format!("R{}", card.rarity))))
                        }).collect::<Vec<_>>();
                        let (page, pages) = (catalog.page, catalog.total_pages.max(1));
                        let pager = |id: &'static str, label: String, name: &str, disabled: bool, delta: i64, cx: &mut Context<Self>| {
                            let b = div().id(id).flex().items_center().gap(px(4.)).h(px(36.)).px(px(12.)).rounded(px(6.)).border_1().border_color(p.line).bg(p.panel).text_size(px(12.)).font_weight(FontWeight::BOLD).child(icon(name, 16., p.ink)).child(label);
                            if disabled { b.opacity(0.55) } else { b.cursor_pointer().on_click(cx.listener(move |this, _, _, cx| { this.card_page += delta; this.load_cards(false, cx) })) }
                        };
                        div().flex().flex_col()
                            .child(div().grid().grid_cols(if kit::narrow(cx) { 3 } else if kit::compact(cx) { 4 } else { 5 }).gap(px(8.)).children(cards))
                            .child(div().mt(px(16.)).flex().items_center().justify_center().gap(px(20.))
                                .child(pager("cprev", t("common.previous"), "chevron-left", page <= 1, -1, cx))
                                .child(div().text_size(px(12.)).text_color(p.muted).child(tf("packs.pageSummary", &[("page", &page), ("pages", &pages), ("count", &catalog.total)])))
                                .child(pager("cnext", t("common.next"), "chevron-right", page >= pages, 1, cx)))
                    }
                };
                let rarity_chips = self.catalog.as_ref().and_then(|c| c.rarities.clone()).unwrap_or_default();
                let mut chips = vec![kit::chip("r-all", t("packs.allRarities"), self.card_rarity == 0, &p).on_click(cx.listener(|this, _, _, cx| { this.card_rarity = 0; this.card_page = 1; this.load_cards(false, cx) }))];
                chips.extend(rarity_chips.into_iter().map(|r| kit::chip(SharedString::from(format!("r-{r}")), format!("R{r}"), self.card_rarity == r, &p)
                    .on_click(cx.listener(move |this, _, _, cx| { this.card_rarity = r; this.card_page = 1; this.load_cards(false, cx) }))));
                step(num(number.clone()), t("packs.addFive"), tf("packs.catalogLimited", &[("expansion", &pack.expansion_id)]), div().flex().flex_col().gap(px(16.))
                    .child(div().flex().items_center().gap(px(12.)).p(px(12.)).rounded(px(8.)).bg(p.tint)
                        .child(div().flex_1().child(forms::input(&self.card_query).prefix(icon("search", 18., p.muted))))
                        .child(div().flex().flex_col().items_end().child(div().text_size(px(16.)).font_weight(FontWeight::EXTRA_BOLD).child(format!("{}/5", slots.len()))).child(div().text_size(px(10.)).text_color(p.muted).child(t("packs.cardsChosen")))))
                    .child(div().flex().flex_wrap().gap(px(8.)).children(chips))
                    .child(library)
                    .when(complete, |d| d.child(div().flex().items_center().justify_between().p(px(12.)).rounded(px(8.)).bg(p.brand_soft)
                        .child(div().flex().items_center().gap(px(8.)).text_color(p.brand_dark).font_weight(FontWeight::BOLD).child(icon("check", 16., p.brand_dark)).child(t("packs.compositionsReady")))
                        .child(kit::button("to-review", kit::Kind::Primary, t("packs.reviewRule"), None, false, &p).on_click(cx.listener(|this, _, _, cx| this.go(Screen::Review, cx)))))), &p).into_any_element()
            }
            Screen::Review => step(num(number.clone()), t("packs.reviewTitle"), t("packs.reviewHint"), div().flex().flex_col().gap(px(20.))
                .child(div().flex().items_center().gap(px(14.)).p(px(16.)).rounded(px(12.)).bg(p.brand_soft)
                    .child(div().size(px(36.)).rounded_full().bg(p.brand).flex().items_center().justify_center().child(icon("check", 18., p.on_brand)))
                    .child(div().flex().flex_col().child(div().text_size(px(14.)).font_weight(FontWeight::EXTRA_BOLD).child(preview_detail.clone())).child(div().text_size(px(12.)).text_color(p.muted).child(format!("{} {}", preview_title, t("packs.normalSuffix"))))))
                .child(self.options(&rule, &p, cx))
                .child(self.arm_button(t("packs.arm"), &p, cx)), &p).into_any_element(),
            Screen::Illegal => self.render_illegal(&rule, &studio, &p, cx),
        };

        let preview = self.render_preview(&rule, &slots, &pack, preview_title, preview_detail, &p, cx);
        div().p(px(32.)).flex().flex_col()
            .child(heading)
            // Web: preview 300px under 1180px, stacked under the wizard under 900px.
            .child(div().flex().items_start().gap(px(20.)).when(kit::narrow(cx), |d| d.flex_col().items_stretch())
                .child(div().flex_1().min_w_0().flex().flex_col().child(nav).child(content))
                .child(preview))
            .into_any_element()
    }
}

impl PacksPage {
    fn options(&self, rule: &PackRule, p: &Palette, cx: &mut Context<Self>) -> Div {
        div().flex().items_start().gap(px(24.)).p(px(16.)).rounded(px(12.)).border_1().border_color(p.line)
            .child(div().flex_1().child(switch_row("free", rule.free_openings, t("packs.freeOpenings"), t("packs.freeHint"), p)
                .on_click(cx.listener(|this, _, _, cx| this.update_rule(|r| r.free_openings = !r.free_openings, cx)))))
            .child(div().w(px(260.)).child(kit::field(format!("{} ({})", t("packs.fixedSeed"), t("packs.optional")), forms::input(&self.seed), Some(t("packs.seedHint")), p)))
    }

    fn arm_button(&self, label: String, p: &Palette, cx: &mut Context<Self>) -> Div {
        let disabled = !self.can_save() || self.busy || self.pool_busy;
        let text = if self.busy { t("packs.arming") } else if self.saved { t("packs.armed") } else { label };
        let mut b = kit::button("arm", kit::Kind::Primary, text, if self.saved { Some("check") } else { None }, disabled, p).min_h(px(44.)).px(px(24.));
        if !disabled { b = b.on_click(cx.listener(|this, _, _, cx| this.save(cx))) }
        div().flex().justify_end().child(b)
    }

    fn render_illegal(&self, rule: &PackRule, studio: &PackStudio, p: &Palette, cx: &mut Context<Self>) -> AnyElement {
        let illegal = &rule.illegal;
        let expansions = illegal.expansion_ids.clone().unwrap_or_default();
        let rarities = illegal.rarities.clone().unwrap_or_default();
        let kinds = illegal.card_kinds.clone().unwrap_or_default();
        let exceeds = !illegal.allow_duplicates && self.pool.map(|pool| illegal.card_count > pool).unwrap_or(false);
        let message = match self.pool {
            _ if self.pool_busy => t("packs.poolCalculating"),
            None => t("packs.poolCalculating"),
            Some(0) => t("packs.poolEmpty"),
            Some(pool) if exceeds => tf("packs.poolTooSmall", &[("count", &pool)]),
            Some(pool) => tf("packs.poolAvailable", &[("count", &pool), ("plural", &if pool > 1 { "s" } else { "" })]),
        };
        let group = |id: &'static str, title: String, count: usize, chips: Vec<Stateful<Div>>, clear: Box<dyn Fn(&mut Self, &mut Context<Self>)>, scroll: bool, cx: &mut Context<Self>| {
            let summary = if count == 0 { t("packs.allowAll") } else { tf("packs.selected", &[("count", &count), ("suffix", &if count > 1 { "s" } else { "" })]) };
            div().flex().flex_col().gap(px(10.)).pt(px(14.)).border_t_1().border_color(p.line)
                .child(div().flex().items_center().justify_between()
                    .child(div().flex().flex_col().child(div().text_size(px(12.)).font_weight(FontWeight::BOLD).child(title)).child(div().text_size(px(10.)).text_color(p.muted).child(summary)))
                    .when(count > 0, |d| d.child(div().id(id).px(px(10.)).py(px(6.)).rounded(px(6.)).text_size(px(11.)).font_weight(FontWeight::BOLD).text_color(p.muted).cursor_pointer()
                        .hover({ let h = p.surface_soft; move |s| s.bg(h) }).child(t("packs.clearAll")).on_click(cx.listener(move |this, _, _, cx| clear(this, cx))))))
                .child(div().id(SharedString::from(format!("{id}-options"))).flex().flex_wrap().gap(px(8.)).when(scroll, |d| d.max_h(px(180.)).overflow_y_scroll()).children(chips))
        };
        let expansion_chips = studio.filters.expansions.clone().unwrap_or_default().into_iter().map(|x| {
            let active = expansions.contains(&x.id);
            let id = x.id.clone();
            kit::chip(SharedString::from(format!("exp-{}", x.id)), if x.name.is_empty() { x.id.clone() } else { x.name.clone() }, active, p)
                .on_click(cx.listener(move |this, _, _, cx| { let id = id.clone(); this.change_illegal(|i| toggle(i.expansion_ids.get_or_insert_with(Vec::new), id), cx) }))
        }).collect();
        let rarity_chips = studio.filters.rarities.clone().unwrap_or_default().into_iter().map(|x| {
            let active = rarities.contains(&x.value);
            let value = x.value;
            kit::chip(SharedString::from(format!("rar-{value}")), x.label.clone(), active, p)
                .on_click(cx.listener(move |this, _, _, cx| this.change_illegal(|i| toggle(i.rarities.get_or_insert_with(Vec::new), value), cx)))
        }).collect();
        let kind_chips = studio.filters.card_kinds.clone().unwrap_or_default().into_iter().map(|x| {
            let active = kinds.contains(&x);
            let value = x.clone();
            kit::chip(SharedString::from(format!("kind-{x}")), kind_label(&x), active, p)
                .on_click(cx.listener(move |this, _, _, cx| { let v = value.clone(); this.change_illegal(|i| toggle(i.card_kinds.get_or_insert_with(Vec::new), v), cx) }))
        }).collect();
        let error = self.pool == Some(0) || exceeds;
        let body = div().flex().flex_col().gap(px(20.))
            .child(div().flex().gap(px(16.))
                .child(div().flex_1().child(kit::field(t("packs.tenOpenings"), forms::input(&self.return_count), Some(t("packs.tenOpeningHint")), p)))
                .child(div().flex_1().child(kit::field(t("packs.cardsPerPack"), forms::input(&self.card_count), Some(t("packs.cardsPerPackHint")), p))))
            .child(switch_row("dups", illegal.allow_duplicates, t("packs.allowDuplicates"), t("packs.duplicatesHint"), p)
                .on_click(cx.listener(|this, _, _, cx| this.change_illegal(|i| i.allow_duplicates = !i.allow_duplicates, cx))))
            .child(div().flex().flex_col().gap(px(14.)).p(px(16.)).rounded(px(12.)).border_1().border_color(p.line)
                .child(div().flex().items_center().gap(px(10.)).child(icon("filter", 16., p.brand)).child(div().flex().flex_col().child(div().text_size(px(13.)).font_weight(FontWeight::EXTRA_BOLD).child(t("packs.catalogFilters"))).child(div().text_size(px(10.)).text_color(p.muted).child(t("packs.filterHint")))))
                .child(group("clear-exp", t("packs.expansions"), expansions.len(), expansion_chips, Box::new(|this, cx| this.change_illegal(|i| i.expansion_ids = Some(Vec::new()), cx)), true, cx))
                .child(group("clear-rar", t("packs.rarities"), rarities.len(), rarity_chips, Box::new(|this, cx| this.change_illegal(|i| i.rarities = Some(Vec::new()), cx)), false, cx))
                .child(group("clear-kind", t("packs.cardTypes"), kinds.len(), kind_chips, Box::new(|this, cx| this.change_illegal(|i| i.card_kinds = Some(Vec::new()), cx)), false, cx)))
            .child(div().flex().items_center().gap(px(12.)).p(px(14.)).rounded(px(10.)).border_1()
                .border_color(if error { p.danger_line } else { p.line }).bg(if error { p.danger_soft } else { p.surface_soft })
                .child(icon(if self.pool_busy { "rotate-ccw" } else { "layers" }, 18., if error { p.danger } else { p.brand }))
                .child(div().flex().flex_col().child(div().text_size(px(12.)).font_weight(FontWeight::BOLD).child(t("packs.globalPool"))).child(div().text_size(px(11.)).text_color(if error { p.danger } else { p.muted }).child(message))))
            .child(self.options(rule, p, cx))
            .child(self.arm_button(t("packs.armIllegal"), p, cx));
        step(icon("flame", 18., p.danger).into_any_element(), t("packs.illegalTitle"), t("packs.illegalIntro"), body, p).into_any_element()
    }

    #[allow(clippy::too_many_arguments)]
    fn render_preview(&self, rule: &PackRule, slots: &[RuleSlot], _pack: &Pack, title: String, detail: String, p: &Palette, cx: &mut Context<Self>) -> Div {
        let illegal = self.experience == Some(Experience::Illegal);
        let kind = if self.content == Some(Content::Cards) { "cards" } else if self.path == Some(Path::Official) { "official" } else { match self.content { Some(Content::Table) => "table", Some(Content::Rarity) => "rarity", _ => "empty" } };
        let body = if illegal {
            let total = if rule.return_pack_count > 0 && rule.illegal.card_count > 0 { (rule.return_pack_count * rule.illegal.card_count).to_string() } else { "—".into() };
            let pool = if self.pool_busy || self.pool.is_none() { "…".to_string() } else { self.pool.unwrap_or(0).to_string() };
            let row = |label: String, value: String| div().flex().justify_between().py(px(8.)).border_b_1().border_color(p.line).text_size(px(12.)).child(div().text_color(p.muted).child(label)).child(div().font_weight(FontWeight::EXTRA_BOLD).child(value));
            div().flex().flex_col().gap(px(12.))
                .child(div().flex().items_center().gap(px(8.)).p(px(12.)).rounded(px(10.)).bg(p.danger_soft).text_color(p.danger).font_weight(FontWeight::BOLD).child(icon("flame", 18., p.danger)).child(t("packs.globalRecipe")))
                .child(div().flex().flex_col()
                    .child(row(t("packs.openingOne"), "1".into())).child(row(t("packs.openingTen"), rule.return_pack_count.to_string()))
                    .child(row(t("packs.cardsEach"), rule.illegal.card_count.to_string())).child(row(t("packs.cardsTen"), total)).child(row(t("packs.pool"), pool)))
                .child(div().text_size(px(11.)).text_color(p.muted).child(t("packs.illegalRecipeHint")))
        } else {
            let card = |index: usize, cx: &mut Context<Self>| {
                let slot = slots.get(index);
                let definition = slot.and_then(|s| self.known.get(&s.card_id)).cloned();
                let placeholder = match kind { "official" => t("packs.probability"), "table" => table_label(&rule.table_kind), "rarity" => rule.rarity.clone(), _ => t("packs.card") };
                let removable = slot.is_some() && kind == "cards";
                div().id(("preview", index)).relative().w(px(84.)).h(px(116.)).rounded(px(8.)).border_1().overflow_hidden()
                    .border_color(if slot.is_some() { p.brand } else { p.line }).bg(if slot.is_some() { p.panel } else { p.tint })
                    .child(match (&definition, slot) {
                        (Some(def), Some(_)) if !def.image_url.is_empty() => kit::remote_image(&def.image_url, &self.api.base, p),
                        _ => div().size_full().flex().flex_col().items_center().justify_center().gap(px(6.)).p(px(6.)).child(icon("lock-keyhole", 16., p.muted))
                            .child(div().text_size(px(9.)).font_weight(FontWeight::BOLD).text_center().text_color(p.muted).child(if slot.is_some() { definition.map(|d| d.name).unwrap_or_else(|| t("packs.forcedCard")) } else { placeholder })).into_any_element(),
                    })
                    .child(div().absolute().left(px(4.)).top(px(4.)).size(px(16.)).rounded_full().bg(p.panel).text_size(px(9.)).font_weight(FontWeight::EXTRA_BOLD).flex().items_center().justify_center().child((index + 1).to_string()))
                    .when(removable, |d| d.cursor_pointer().child(div().absolute().right(px(4.)).bottom(px(4.)).size(px(20.)).rounded(px(4.)).bg(p.panel).flex().items_center().justify_center().child(icon("trash", 12., p.danger)))
                        .on_click(cx.listener(move |this, _, _, cx| this.remove_card(index, cx))))
            };
            div().flex().flex_col().items_center().gap(px(8.)).py(px(8.))
                .child(div().flex().gap(px(8.)).child(card(0, cx)).child(card(1, cx)).child(card(2, cx)))
                .child(div().flex().gap(px(8.)).child(card(3, cx)).child(card(4, cx)))
        };
        div().when(kit::narrow(cx), |d| d.w_full()).when(!kit::narrow(cx), |d| d.w(px(if kit::width(cx) <= 1180. { 300. } else { 340. })).flex_none()).flex().flex_col().gap(px(16.)).p(px(20.)).rounded(px(14.)).border_1().border_color(p.line).bg(p.panel)
            .child(div().flex().items_start().justify_between()
                .child(div().flex().flex_col().child(div().text_size(px(9.)).font_weight(FontWeight::EXTRA_BOLD).text_color(p.brand).child(t("packs.preview").to_uppercase())).child(div().text_size(px(16.)).font_weight(FontWeight::EXTRA_BOLD).child(title)))
                .child(icon(if illegal { "flame" } else { "shield-check" }, 20., if illegal { p.danger } else { p.brand })))
            .child(body)
            .child(div().flex().flex_col().gap(px(4.)).p(px(12.)).rounded(px(10.)).bg(p.surface_soft)
                .child(div().text_size(px(12.)).font_weight(FontWeight::EXTRA_BOLD).child(detail))
                .child(div().text_size(px(11.)).text_color(p.muted).child(t(if illegal { "packs.illegalActive" } else if self.content == Some(Content::Cards) { "packs.otherPacksOfficial" } else { "packs.ruleShared" }))))
            .child(div().text_size(px(11.)).text_color(p.muted).child(t(if self.screen == Screen::Review { "packs.validationFinal" } else { "packs.continueLeft" })))
    }
}
