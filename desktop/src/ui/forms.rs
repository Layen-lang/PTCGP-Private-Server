//! Text inputs and selects (gpui-component) themed with the panel palette.

use crate::theme::Palette;
use gpui_kit::component::input::{Input, InputState};
use gpui_kit::component::select::{Select, SelectItem, SelectState};
use gpui_kit::component::searchable_list::SearchableVec;
use gpui_kit::component::theme::{Theme, ThemeMode};
use gpui_kit::component::{ActiveTheme, IndexPath, Sizable};
use gpui_kit::prelude::FluentBuilder as _;
use gpui_kit::*;

/// Applies the palette to gpui-component's theme so inputs, selects,
/// popovers and scrollbars match the rest of the panel.
pub fn sync_component_theme(p: &Palette, cx: &mut App) {
    // `Theme::update` keeps GPUI Kit's other copies of the colours (tokens and
    // the Base layer used by lists and popovers) in step; writing the global
    // directly left dropdowns in the default light colours. The mode goes
    // first because switching it reloads the default colours.
    let mode = if p.dark { ThemeMode::Dark } else { ThemeMode::Light };
    Theme::update(cx, |theme| theme.mode = mode);
    let p = p.clone();
    Theme::update(cx, move |theme| apply_palette(theme, &p));
}

fn apply_palette(theme: &mut Theme, p: &Palette) {
    theme.font_family = "Segoe UI".into();
    theme.font_size = px(p.ui_size);
    theme.radius = px(8.);
    theme.radius_lg = px(12.);
    let c = &mut theme.colors;
    c.background = p.panel;
    c.foreground = p.ink;
    c.border = p.control_border;
    c.input = p.control_border;
    c.ring = p.brand;
    c.primary = p.brand;
    c.primary_hover = p.brand_dark;
    c.primary_active = p.brand_dark;
    c.primary_foreground = p.on_brand;
    c.muted = p.surface_soft;
    c.muted_foreground = p.muted;
    c.popover = p.panel;
    c.popover_foreground = p.ink;
    c.accent = p.surface_soft;
    c.accent_foreground = p.ink;
    c.list = p.panel;
    c.list_hover = p.surface_soft;
    c.list_active = p.brand_soft;
    c.list_active_border = p.brand;
    c.selection = { let mut s = p.brand; s.a = 0.3; s };
    c.caret = p.ink;
    c.scrollbar = gpui_kit::transparent_black();
    c.scrollbar_thumb = { let mut s = p.muted; s.a = 0.45; s };
    c.scrollbar_thumb_hover = p.muted;
    c.secondary = p.surface_soft;
    c.secondary_hover = p.surface_hover;
    c.secondary_foreground = p.ink;
    c.switch = p.surface_raised;
    c.window_border = p.line;
}

pub fn text_state(value: &str, placeholder: &str, window: &mut Window, cx: &mut App) -> Entity<InputState> {
    let (value, placeholder) = (value.to_string(), placeholder.to_string());
    cx.new(|cx| InputState::new(window, cx).placeholder(placeholder).default_value(value))
}

pub fn set_text(state: &Entity<InputState>, value: &str, window: &mut Window, cx: &mut App) {
    let value = value.to_string();
    state.update(cx, |s, cx| s.set_value(value, window, cx));
}

pub fn text(state: &Entity<InputState>, cx: &App) -> String { state.read(cx).value().to_string() }

/// Parses a numeric field, clamped like the web `NumberInput`.
pub fn number(state: &Entity<InputState>, min: i64, max: i64, cx: &App) -> Option<i64> {
    text(state, cx).trim().parse::<f64>().ok().filter(|v| v.is_finite()).map(|v| (v.trunc() as i64).clamp(min, max))
}

/// A 40px text field (`input` in the stylesheet).
pub fn input(state: &Entity<InputState>) -> Input { Input::new(state).large() }

#[derive(Clone, Debug, PartialEq)]
pub struct Choice {
    pub value: SharedString,
    pub label: SharedString,
    pub icon: Option<SharedString>,
    pub detail: Option<SharedString>,
}

impl Choice {
    pub fn new(value: impl Into<SharedString>, label: impl Into<SharedString>) -> Self { Choice { value: value.into(), label: label.into(), icon: None, detail: None } }

    /// Adds a leading icon and a muted second line to the dropdown row.
    pub fn described(mut self, icon: &str, detail: impl Into<SharedString>) -> Self {
        self.icon = Some(SharedString::from(icon.to_string()));
        self.detail = Some(detail.into()).filter(|d: &SharedString| !d.is_empty());
        self
    }
}

impl SelectItem for Choice {
    type Value = SharedString;
    fn title(&self) -> SharedString { self.label.clone() }
    fn value(&self) -> &SharedString { &self.value }

    fn render(&self, _: &mut Window, cx: &mut App) -> impl IntoElement {
        let muted = cx.theme().muted_foreground;
        div().flex().items_center().gap(px(10.)).min_w_0()
            .when_some(self.icon.clone(), |d, name| d.child(crate::ui::kit::icon(name.as_ref(), 16., muted)))
            .child(div().flex().flex_col().min_w_0()
                .child(div().truncate().child(self.label.clone()))
                .when_some(self.detail.clone(), |d, detail| d.child(div().text_size(px(11.)).text_color(muted).truncate().child(detail))))
    }

    fn matches(&self, query: &str) -> bool {
        let query = query.to_lowercase();
        self.label.to_lowercase().contains(&query) || self.detail.as_ref().is_some_and(|d| d.to_lowercase().contains(&query))
    }
}

pub type ChoiceState = Entity<SelectState<SearchableVec<Choice>>>;

pub fn select_state(choices: Vec<Choice>, selected: &str, window: &mut Window, cx: &mut App) -> ChoiceState {
    let index = choices.iter().position(|c| c.value.as_ref() == selected).map(IndexPath::new);
    cx.new(|cx| SelectState::new(SearchableVec::new(choices), index, window, cx))
}

pub fn set_choices(state: &ChoiceState, choices: Vec<Choice>, selected: &str, window: &mut Window, cx: &mut App) {
    let index = choices.iter().position(|c| c.value.as_ref() == selected).map(IndexPath::new);
    state.update(cx, |s, cx| {
        s.set_items(SearchableVec::new(choices), window, cx);
        match index {
            Some(index) => s.set_selected_index(Some(index), window, cx),
            None => s.set_selected_index(None, window, cx),
        }
    });
}

pub fn select(state: &ChoiceState) -> Select<SearchableVec<Choice>> { Select::new(state).large() }
