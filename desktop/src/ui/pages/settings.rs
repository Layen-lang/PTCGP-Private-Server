//! Port of `SettingsPage.tsx`: interface language and theme customisation.

use crate::i18n::{self, t};
use crate::theme::{contrast, Preference, Scheme, ThemeSettings, PRESETS};
use crate::ui::app::PageEvent;
use crate::ui::forms;
use crate::ui::kit::{icon, pal};
use crate::theme::Palette;
use gpui_kit::component::input::{InputEvent, InputState};
use gpui_kit::*;
use gpui_kit::prelude::FluentBuilder as _;

#[derive(Clone, Copy, PartialEq)]
enum Menu {
    None,
    Language,
    Preset,
}

pub struct SettingsPage {
    pub system_dark: bool,
    settings: ThemeSettings,
    menu: Menu,
    advanced: bool,
    accent: Entity<InputState>,
    background: Entity<InputState>,
    foreground: Entity<InputState>,
    _subs: Vec<Subscription>,
}

impl EventEmitter<PageEvent> for SettingsPage {}

fn color_of(value: &str) -> Hsla { crate::theme::color(u32::from_str_radix(value.trim_start_matches('#'), 16).unwrap_or(0)) }

impl SettingsPage {
    pub fn new(settings: ThemeSettings, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let accent = forms::text_state("", "", window, cx);
        let background = forms::text_state("", "", window, cx);
        let foreground = forms::text_state("", "", window, cx);
        let mut subs = Vec::new();
        for (state, field) in [(&accent, 0u8), (&background, 1), (&foreground, 2)] {
            subs.push(cx.subscribe_in(state, window, move |this: &mut Self, state, event: &InputEvent, window, cx| {
                if !matches!(event, InputEvent::Blur | InputEvent::PressEnter { .. }) { return }
                let value = forms::text(state, cx).trim().to_uppercase();
                let valid = value.len() == 7 && value.starts_with('#') && value[1..].chars().all(|c| c.is_ascii_hexdigit());
                if valid { this.update_scheme(field, value, cx) } else { this.sync_inputs(window, cx) }
            }));
        }
        let mut page = SettingsPage { system_dark: false, settings, menu: Menu::None, advanced: false, accent, background, foreground, _subs: subs };
        page.sync_inputs(window, cx);
        page
    }

    fn editing_dark(&self) -> bool {
        match self.settings.preference { Preference::System => self.system_dark, Preference::Light => false, Preference::Dark => true }
    }

    fn scheme(&self) -> &Scheme { if self.editing_dark() { &self.settings.dark } else { &self.settings.light } }

    fn sync_inputs(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let s = self.scheme().clone();
        forms::set_text(&self.accent, &s.accent, window, cx);
        forms::set_text(&self.background, &s.background, window, cx);
        forms::set_text(&self.foreground, &s.foreground, window, cx);
    }

    fn emit(&mut self, cx: &mut Context<Self>) {
        cx.emit(PageEvent::Theme(self.settings.clone()));
        cx.notify();
    }

    fn update_scheme(&mut self, field: u8, value: String, cx: &mut Context<Self>) {
        let dark = self.editing_dark();
        let scheme = if dark { &mut self.settings.dark } else { &mut self.settings.light };
        match field { 0 => scheme.accent = value, 1 => scheme.background = value, _ => scheme.foreground = value }
        scheme.preset_id = "custom".into();
        self.emit(cx);
    }

    fn select_preset(&mut self, id: &'static str, window: &mut Window, cx: &mut Context<Self>) {
        let dark = self.editing_dark();
        if let Some(preset) = PRESETS.iter().find(|p| p.id == id && p.dark == dark) {
            if dark { self.settings.dark = preset.scheme() } else { self.settings.light = preset.scheme() }
        }
        self.menu = Menu::None;
        self.sync_inputs(window, cx);
        self.emit(cx);
    }

    fn badge(bg: Hsla, accent: Hsla, fg: Hsla, text: &str, second: Option<&str>) -> Div {
        let mut border = fg;
        border.a = 0.24;
        div().flex().items_center().justify_center().size(px(28.)).flex_none().rounded(px(7.)).border_1().border_color(border).bg(bg).text_size(px(10.))
            .child(div().text_color(accent).font_weight(FontWeight::EXTRA_BOLD).child(text.to_string()))
            .when_some(second, |d, s| d.child(div().text_color(fg).font_weight(FontWeight::BOLD).child(s.to_string())))
    }

    /// Miniature app layout (sidebar + content) drawn with `scheme`, filling its parent.
    fn mini_ui(scheme: &Scheme) -> Div {
        let (bg, accent, ink) = (color_of(&scheme.background), color_of(&scheme.accent), color_of(&scheme.foreground));
        let mut faint = ink;
        faint.a = 0.13;
        let mut dim = white();
        dim.a = 0.36;
        let mut text = ink;
        text.a = 0.78;
        let bar = |w: f32, h: f32, c: Hsla| div().w(relative(w)).h(px(h)).rounded(px(2.)).bg(c);
        div().size_full().flex().bg(bg)
            .child(div().w(relative(0.29)).flex().flex_col().gap(px(7.)).px(px(7.)).py(px(12.)).bg(accent)
                .child(bar(0.7, 8., white())).child(bar(1.0, 5., dim)).child(bar(1.0, 5., dim)))
            .child(div().flex_1().flex().flex_col().gap(px(9.)).p(px(13.))
                .child(bar(0.55, 7., text))
                .child(div().w_full().h(px(23.)).rounded(px(4.)).border_1().border_color(faint).bg(bg))
                .child(div().w(relative(0.76)).h(px(23.)).rounded(px(4.)).border_1().border_color(faint).bg(bg)))
    }

    fn section(title: String, name: &str, body: impl IntoElement, p: &Palette) -> Div {
        div().relative().border_1().border_color(p.line).rounded(px(14.)).bg(p.panel)
            .child(div().flex().items_center().gap(px(13.)).px(px(24.)).py(px(18.)).border_b_1().border_color(p.line)
                .child(div().size(px(36.)).flex().items_center().justify_center().rounded(px(9.)).bg(p.brand_soft).child(icon(name, 18., p.brand_dark)))
                .child(div().text_size(px(15.)).font_weight(FontWeight::BOLD).child(title)))
            .child(body)
    }

    fn trigger(id: &'static str, badge: Option<Div>, label: Div, open: bool, p: &Palette) -> Stateful<Div> {
        div().id(id).flex().items_center().gap(px(9.)).w(px(190.)).h(px(38.)).pl(px(if badge.is_some() { 5. } else { 12. })).pr(px(9.)).rounded(px(9.)).border_1().border_color(p.line).bg(p.surface_soft)
            .text_size(px(11.)).font_weight(FontWeight::BOLD).cursor_pointer()
            .hover({ let (b, h) = (p.control_border, p.surface_hover); move |s| s.border_color(b).bg(h) })
            .children(badge).child(label.flex_1().min_w_0())
            .child(icon(if open { "chevron-up" } else { "chevron-down" }, 14., p.muted))
    }

    fn menu_panel(p: &Palette) -> Div {
        div().absolute().top(px(44.)).right_0().w(px(238.)).max_h(px(318.)).p(px(5.)).rounded(px(11.)).border_1().border_color(p.control_border).bg(p.panel).shadow_lg()
    }
}

impl Render for SettingsPage {
    fn render(&mut self, _window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let p = pal(cx);
        let locale = i18n::locale();
        let dark = self.editing_dark();
        let scheme = self.scheme().clone();
        let selected = PRESETS.iter().find(|x| x.id == scheme.preset_id && x.dark == dark);
        let preset_label = selected.map(|x| x.label.to_string()).unwrap_or_else(|| t("settings.custom"));
        let ratio = contrast(&scheme.foreground, &scheme.background);

        // Language selector.
        let lang_label = |l: &str| i18n::LANGUAGES.iter().find(|(k, _)| *k == l).map(|(_, v)| v.to_string()).unwrap_or_default();
        let mut language = div().relative().w(px(190.))
            .child(Self::trigger("language", None, div().truncate().child(lang_label(locale)), self.menu == Menu::Language, &p)
                .on_click(cx.listener(|this, _, _, cx| { this.menu = if this.menu == Menu::Language { Menu::None } else { Menu::Language }; cx.notify() })));
        if self.menu == Menu::Language {
            let items = i18n::LANGUAGES.iter().map(|(key, label)| {
                let key: &'static str = key;
                let chosen = key == locale;
                div().id(key).flex().items_center().gap(px(10.)).min_h(px(34.)).px(px(9.)).py(px(4.)).rounded(px(7.)).cursor_pointer().text_size(px(11.))
                    .font_weight(if chosen { FontWeight::BOLD } else { FontWeight::MEDIUM })
                    .hover({ let h = p.surface_soft; move |s| s.bg(h) })
                    .child(div().flex_1().child(label.to_string()))
                    .when(chosen, |d| d.child(icon("check", 15., p.brand)))
                    .on_click(cx.listener(move |this, _, _, cx| { this.menu = Menu::None; cx.emit(PageEvent::Locale(key.to_string())); cx.notify() }))
            }).collect::<Vec<_>>();
            language = language.child(deferred(Self::menu_panel(&p).left_0().children(items)).with_priority(1));
        }

        // Theme options.
        let options = [(Preference::System, "laptop", t("settings.system")), (Preference::Light, "sun", t("settings.light")), (Preference::Dark, "moon", t("settings.dark"))]
            .into_iter().map(|(pref, name, label)| {
                let chosen = self.settings.preference == pref;
                let base = match pref { Preference::Dark => &self.settings.dark, _ => &self.settings.light };
                let mut border = color_of(&base.foreground);
                border.a = 0.13;
                let preview = div().relative().h(px(105.)).mx(px(10.)).mt(px(10.)).overflow_hidden().rounded(px(8.)).border_1().border_color(border)
                    .child(Self::mini_ui(base))
                    .when(pref == Preference::System, |d| {
                        // Same layout in the dark scheme, clipped to the right half.
                        let mut divider = white();
                        divider.a = 0.4;
                        d.child(div().absolute().top_0().bottom_0().right_0().w(relative(0.5)).overflow_hidden().border_l_1().border_color(divider)
                            .child(div().absolute().top_0().bottom_0().right_0().w(relative(2.0)).child(Self::mini_ui(&self.settings.dark))))
                    });
                div().id(name).relative().flex().flex_col().flex_1().min_w_0().overflow_hidden().rounded(px(12.)).border_1().cursor_pointer()
                    .border_color(if chosen { p.brand } else { p.line }).bg(if chosen { p.brand_soft } else { p.panel })
                    .hover({ let (b, h) = (p.control_border, p.surface_hover); move |s| if chosen { s } else { s.border_color(b).bg(h) } })
                    .child(preview)
                    .child(div().flex().items_center().min_h(px(58.)).pl(px(14.)).pr(px(42.)).py(px(12.)).gap(px(7.)).text_size(px(13.)).font_weight(FontWeight::BOLD)
                        .child(icon(name, 15., p.brand_dark)).child(label))
                    .child(div().absolute().right(px(13.)).bottom(px(19.)).size(px(20.)).rounded_full().border_1().flex().items_center().justify_center()
                        .border_color(if chosen { p.brand } else { p.line }).bg(if chosen { p.brand } else { p.panel })
                        .when(chosen, |d| d.child(icon("check", 12., white()))))
                    .on_click(cx.listener(move |this, _, window, cx| { this.settings.preference = pref; this.sync_inputs(window, cx); this.emit(cx) }))
            }).collect::<Vec<_>>();

        // Preset selector.
        let mut presets = div().relative().w(px(190.))
            .child(Self::trigger("preset", Some(Self::badge(color_of(&scheme.background), color_of(&scheme.accent), color_of(&scheme.foreground), "A", Some("a"))), div().truncate().child(preset_label), self.menu == Menu::Preset, &p)
                .on_click(cx.listener(|this, _, _, cx| { this.menu = if this.menu == Menu::Preset { Menu::None } else { Menu::Preset }; cx.notify() })));
        if self.menu == Menu::Preset {
            let items = PRESETS.iter().filter(|x| x.dark == dark).map(|x| {
                let chosen = x.id == scheme.preset_id;
                let id = x.id;
                div().id(id).flex().items_center().gap(px(10.)).min_h(px(40.)).px(px(7.)).py(px(4.)).rounded(px(7.)).cursor_pointer().text_size(px(11.))
                    .hover({ let h = p.surface_soft; move |s| s.bg(h) })
                    .child(Self::badge(color_of(x.background), color_of(x.accent), color_of(x.foreground), "A", Some("a")))
                    .child(div().flex_1().child(x.label))
                    .when(chosen, |d| d.child(icon("check", 15., p.brand)))
                    .on_click(cx.listener(move |this, _, window, cx| this.select_preset(id, window, cx)))
            }).collect::<Vec<_>>();
            presets = presets.child(deferred(Self::menu_panel(&p).id("preset-menu").overflow_y_scroll().children(items)).with_priority(1));
        }

        let setting_row = |label: String, control: AnyElement| div().flex().items_center().justify_between().gap(px(16.)).px(px(14.)).py(px(12.)).border_t_1().border_color(p.line)
            .child(div().text_size(px(12.)).font_weight(FontWeight::BOLD).child(label)).child(control);
        let color_row = |label: String, state: &Entity<InputState>, value: &str| setting_row(label, div().flex().items_center().gap(px(8.))
            .child(div().size(px(30.)).rounded(px(7.)).border_1().border_color(p.line).bg(color_of(value)))
            .child(div().w(px(110.)).child(forms::input(state))).into_any_element());
        let size = self.settings.ui_font_size;
        let stepper = |id: &'static str, name: &str, delta: f32, cx: &mut Context<Self>| div().id(id).size(px(30.)).flex().items_center().justify_center().rounded(px(7.)).border_1().border_color(p.line).cursor_pointer()
            .hover({ let h = p.surface_hover; move |s| s.bg(h) }).child(icon(name, 14., p.ink))
            .on_click(cx.listener(move |this, _, _, cx| { this.settings.ui_font_size = (this.settings.ui_font_size + delta).clamp(12., 18.); this.emit(cx) }));
        let warning = ratio < 4.5;

        div().w_full().max_w(px(1120.)).mx_auto().p(px(32.)).flex().flex_col().gap(px(16.))
            .on_mouse_down_out(cx.listener(|this, _, _, cx| if this.menu != Menu::None { this.menu = Menu::None; cx.notify() }))
            .child(div().mb(px(8.)).text_size(px(30.)).font_weight(FontWeight::EXTRA_BOLD).child(t("settings.title")))
            .child(Self::section(t("settings.language"), "languages", div().px(px(24.)).py(px(18.)).child(language), &p))
            .child(Self::section(t("settings.appearance"), "palette", div().flex().flex_col()
                .child(div().flex().gap(px(12.)).px(px(24.)).pt(px(22.)).pb(px(26.)).children(options))
                .child(div().relative().mx(px(24.)).mb(px(24.)).border_1().border_color(p.line).rounded(px(12.)).bg(p.panel)
                    .child(div().flex().items_center().justify_between().gap(px(18.)).min_h(px(62.)).px(px(14.)).py(px(10.))
                        .child(div().text_size(px(11.)).font_weight(FontWeight::BOLD).text_color(p.brand_dark).child(t(if dark { "settings.darkMode" } else { "settings.lightMode" })))
                        .child(div().flex().items_center().gap(px(8.)).child(div().text_size(px(10.)).font_weight(FontWeight::BOLD).text_color(p.muted).child(t("settings.theme"))).child(presets)))
                    .child(div().id("advanced").flex().items_center().gap(px(8.)).px(px(14.)).py(px(12.)).border_t_1().border_color(p.line).text_size(px(12.)).font_weight(FontWeight::BOLD).cursor_pointer()
                        .child(icon("palette", 15., p.muted)).child(t("settings.customizeColors")).child(div().flex_1()).child(icon(if self.advanced { "chevron-up" } else { "chevron-down" }, 15., p.muted))
                        .on_click(cx.listener(|this, _, _, cx| { this.advanced = !this.advanced; cx.notify() })))
                    .when(self.advanced, |d| d
                        .child(color_row(t("settings.accent"), &self.accent, &scheme.accent))
                        .child(color_row(t("settings.background"), &self.background, &scheme.background))
                        .child(color_row(t("settings.text"), &self.foreground, &scheme.foreground))
                        .child(div().flex().items_center().gap(px(10.)).px(px(14.)).py(px(12.)).border_t_1().border_color(p.line).text_size(px(12.))
                            .bg(if warning { p.warning_soft } else { p.panel }).text_color(if warning { p.warning } else { p.ink })
                            .child(t("settings.contrast")).child(div().font_weight(FontWeight::EXTRA_BOLD).child(format!("{ratio:.1}:1")))
                            .child(div().text_size(px(11.)).text_color(p.muted).child(t(if warning { "settings.insufficient" } else { "settings.compliant" })))))
                    .child(setting_row(t("settings.interfaceSize"), div().flex().items_center().gap(px(8.))
                        .child(stepper("smaller", "minus", -1., cx)).child(div().w(px(44.)).text_center().text_size(px(12.)).font_weight(FontWeight::BOLD).child(format!("{size}px"))).child(stepper("larger", "plus", 1., cx)).into_any_element()))
                    .child(div().flex().justify_end().px(px(14.)).py(px(10.)).border_t_1().border_color(p.line)
                        .child(div().id("reset").flex().items_center().gap(px(6.)).px(px(10.)).py(px(6.)).rounded(px(7.)).text_size(px(11.)).font_weight(FontWeight::BOLD).text_color(p.muted).cursor_pointer()
                            .hover({ let (h, i) = (p.surface_soft, p.ink); move |s| s.bg(h).text_color(i) })
                            .child(icon("rotate-ccw", 14., p.muted)).child(t("settings.resetAll"))
                            .on_click(cx.listener(|this, _, window, cx| { this.settings = ThemeSettings::default(); this.sync_inputs(window, cx); this.emit(cx) })))))
                , &p))
    }
}
