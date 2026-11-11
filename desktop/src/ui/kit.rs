//! Small styled building blocks mirroring the CSS classes of the web panel
//! (`.primary-button`, `.secondary-button`, `.quiet-button`, chips…).

use crate::theme::Palette;
use gpui_kit::component::Icon;
use gpui_kit::*;
use gpui_kit::prelude::FluentBuilder as _;
use std::borrow::Cow;
use std::sync::Arc;

/// The palette for the current theme, shared by every view.
#[derive(Clone)]
pub struct Colors(pub Arc<Palette>);
impl Global for Colors {}

pub fn pal(cx: &App) -> Arc<Palette> { cx.global::<Colors>().0.clone() }

/// Window width in pixels, refreshed by the shell on every frame. Pages use
/// it for the same breakpoints as the web stylesheet (1180, 1100, 950 px).
#[derive(Clone, Copy)]
pub struct Viewport(pub f32);
impl Global for Viewport {}

pub fn width(cx: &App) -> f32 { cx.try_global::<Viewport>().map(|v| v.0).unwrap_or(1440.) }
/// `@media (max-width: 1100px)`.
pub fn compact(cx: &App) -> bool { width(cx) <= 1100. }
/// `@media (max-width: 950px)`.
pub fn narrow(cx: &App) -> bool { width(cx) <= 950. }

/// Lucide icon by file name (`icons/<name>.svg`).
pub fn icon(name: &str, size: f32, color: Hsla) -> Icon {
    Icon::empty().path(SharedString::from(format!("icons/{name}.svg"))).size(px(size)).text_color(color)
}

pub fn logo(size: f32, color: Hsla) -> Svg {
    svg().path("app/logo.svg").size(px(size)).text_color(color)
}

/// Assets: the full Lucide catalog plus the application logo.
pub struct AppAssets;
impl AssetSource for AppAssets {
    fn load(&self, path: &str) -> Result<Option<Cow<'static, [u8]>>> {
        if path == "app/logo.svg" {
            return Ok(Some(Cow::Borrowed(LOGO.as_bytes())));
        }
        gpui_kit::assets::AllAssets.load(path)
    }
    fn list(&self, path: &str) -> Result<Vec<SharedString>> { gpui_kit::assets::AllAssets.list(path) }
}

const LOGO: &str = r#"<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 40 40" fill="none"><rect x="6.5" y="3.5" width="27" height="33" rx="5.5" stroke="black" stroke-width="3"/><path d="M20 10.25c.76 3.8 2.95 5.99 6.75 6.75-3.8.76-5.99 2.95-6.75 6.75-.76-3.8-2.95-5.99-6.75-6.75 3.8-.76 5.99-2.95 6.75-6.75Z" fill="black"/><circle cx="27.5" cy="27.5" r="2" fill="black"/></svg>"#;

#[derive(Clone, Copy, PartialEq, Eq)]
pub enum Kind {
    Primary,
    Secondary,
    Quiet,
    Danger,
}

/// A button styled like the web panel. Add `.on_click` on the result.
pub fn button(id: impl Into<ElementId>, kind: Kind, label: impl Into<SharedString>, leading: Option<&str>, disabled: bool, p: &Palette) -> Stateful<Div> {
    let (bg, fg, border, hover_bg, hover_fg, height, size) = match kind {
        Kind::Primary => (p.brand, p.on_brand, None, p.brand_dark, p.on_brand, 40.0, 14.0),
        Kind::Secondary => (p.panel, p.ink, Some(p.line), p.surface_hover, p.ink, 40.0, 14.0),
        Kind::Quiet => (transparent_black(), p.muted, None, hover_tint(p), p.ink, 36.0, 12.0),
        Kind::Danger => (rgb(0xB91C1C).into(), white(), None, rgb(0x991B1B).into(), white(), 40.0, 14.0),
    };
    let mut el = div()
        .id(id)
        .flex()
        .flex_none()
        .items_center()
        .justify_center()
        .gap(px(8.))
        .min_h(px(height))
        .px(px(if kind == Kind::Quiet { 12. } else { 16. }))
        .rounded(px(if kind == Kind::Quiet { 6. } else { 8. }))
        .bg(bg)
        .text_color(fg)
        .text_size(px(size))
        .font_weight(FontWeight::BOLD);
    if let Some(border) = border { el = el.border_1().border_color(border) }
    if let Some(name) = leading { el = el.child(icon(name, if kind == Kind::Quiet { 16. } else { 18. }, fg)) }
    el = el.child(label.into());
    if disabled {
        el.opacity(0.55).cursor_not_allowed()
    } else {
        let border_hover = p.control_border;
        el.cursor_pointer().hover(move |s| {
            let s = s.bg(hover_bg).text_color(hover_fg);
            if kind == Kind::Secondary { s.border_color(border_hover) } else { s }
        })
    }
}

pub fn hover_tint(p: &Palette) -> Hsla {
    if p.dark { let mut c = p.ink; c.a = 0.08; c } else { hsla(0., 0., 0., 0.05) }
}

/// `.choice-chip`.
pub fn chip(id: impl Into<ElementId>, label: impl Into<SharedString>, selected: bool, p: &Palette) -> Stateful<Div> {
    div().id(id).flex().items_center().gap(px(8.)).min_h(px(36.)).px(px(12.)).rounded(px(8.)).border_1()
        .border_color(if selected { p.brand } else { p.line })
        .bg(if selected { p.brand_soft } else { p.panel })
        .text_color(if selected { p.brand_dark } else { p.ink })
        .text_size(px(12.)).font_weight(FontWeight::BOLD).cursor_pointer()
        .hover({ let b = p.control_border; move |s| if selected { s } else { s.border_color(b) } })
        .child(label.into())
}

/// Small rounded badge (`.count-label`, `.resource-section-heading > span`…).
pub fn badge(label: impl Into<SharedString>, bg: Hsla, fg: Hsla) -> Div {
    div().px(px(8.)).py(px(4.)).rounded(px(6.)).bg(bg).text_color(fg).text_size(px(10.)).font_weight(FontWeight::EXTRA_BOLD).child(label.into())
}

/// `<label><span>Title</span>{control}<small>hint</small></label>`.
pub fn field(title: impl Into<SharedString>, control: impl IntoElement, hint: Option<String>, p: &Palette) -> Div {
    let mut el = div().flex().flex_col().min_w_0()
        .child(div().mb(px(6.)).text_size(px(12.)).font_weight(FontWeight::BOLD).text_color(p.ink).child(title.into()))
        .child(control);
    if let Some(hint) = hint { el = el.child(div().mt(px(4.)).text_size(px(10.)).text_color(p.muted).child(hint)) }
    el
}

pub fn heading(title: impl Into<SharedString>, p: &Palette) -> Div {
    div().text_size(px(34.)).font_weight(FontWeight::EXTRA_BOLD).text_color(p.ink).line_height(px(40.)).child(title.into())
}

/// `.section-intro`: title on the left, actions on the right.
pub fn section_intro(title: impl Into<SharedString>, right: Option<AnyElement>, p: &Palette) -> Div {
    let mut el = div().flex().items_start().justify_between().gap(px(20.)).mb(px(20.))
        .child(div().text_size(px(16.)).font_weight(FontWeight::EXTRA_BOLD).text_color(p.ink).child(title.into()));
    if let Some(right) = right { el = el.child(right) }
    el
}

pub fn count_label(text: impl Into<SharedString>, p: &Palette) -> Div {
    div().border_1().border_color(p.line).bg(if p.dark { p.surface_soft } else { rgb(0xFAFAF8).into() }).px(px(10.)).py(px(6.))
        .text_size(px(11.)).font_weight(FontWeight::SEMIBOLD).text_color(p.muted).child(text.into())
}

/// Image from the panel with the `.image-placeholder` fallback.
pub fn remote_image(url: &str, base: &str, p: &Palette) -> AnyElement {
    if url.is_empty() {
        return placeholder(p).into_any_element();
    }
    let full = if url.starts_with("http") { url.to_string() } else { format!("{base}{url}") };
    let (tint, muted) = (p.tint, p.muted);
    img(SharedUri::from(full)).size_full().object_fit(ObjectFit::Contain)
        .with_fallback(move || div().size_full().flex().items_center().justify_center().bg(tint).child(icon("wallet-cards", 20., muted)).into_any_element())
        .into_any_element()
}

pub fn placeholder(p: &Palette) -> Div {
    div().size_full().flex().items_center().justify_center().bg(p.tint).child(icon("wallet-cards", 20., p.muted))
}

/// A pulsing-free skeleton block (`.card-skeleton`, `.inventory-skeleton i`).
pub fn skeleton(p: &Palette) -> Div { div().rounded(px(8.)).bg(p.skeleton) }

/// The rounded toggle used by `.switch-row`.
pub fn switch(id: impl Into<ElementId>, on: bool, p: &Palette) -> Stateful<Div> {
    div().id(id).relative().flex_none().w(px(44.)).h(px(24.)).rounded_full().cursor_pointer()
        .bg(if on { p.brand } else if p.dark { p.surface_raised } else { rgb(0xC8CCC7).into() })
        .child(div().absolute().top(px(3.)).left(px(if on { 23. } else { 3. })).size(px(18.)).rounded_full().bg(white()))
}

/// Checkbox square (logs "errors and warnings").
pub fn checkbox(id: impl Into<ElementId>, checked: bool, label: impl Into<SharedString>, p: &Palette) -> Stateful<Div> {
    div().id(id).flex().items_center().gap(px(8.)).h(px(42.)).text_size(px(12.)).text_color(p.ink).cursor_pointer()
        .child(div().size(px(16.)).rounded(px(3.)).border_1().border_color(if checked { p.brand } else { p.control_border })
            .bg(if checked { p.brand } else { p.panel }).flex().items_center().justify_center()
            .when(checked, |d| d.child(icon("check", 12., p.on_brand))))
        .child(label.into())
}
