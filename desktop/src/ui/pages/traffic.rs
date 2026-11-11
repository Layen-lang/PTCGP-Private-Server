//! Port of `TrafficPage.tsx`: live game exchanges with filters and payloads.
//! History is loaded once, then new exchanges arrive through the server-sent
//! event stream, exactly like the web panel.

use crate::api::{Api, TrafficEvent, TrafficPayload};
use crate::i18n::{number, t, tf};
use crate::theme::Palette;
use crate::ui::app::PageEvent;
use crate::ui::forms::{self, Choice, ChoiceState};
use crate::ui::kit::{self, icon, pal, Kind};
use gpui_kit::component::input::{InputEvent, InputState};
use gpui_kit::component::select::SelectEvent;
use gpui_kit::prelude::FluentBuilder as _;
use gpui_kit::*;
use futures::StreamExt as _;
use std::collections::HashSet;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;

enum Feed {
    History(anyhow::Result<Vec<TrafficEvent>>),
    Event(TrafficEvent),
    Live(bool),
    Error(String),
}

pub struct TrafficPage {
    api: Api,
    pub server_running: bool,
    events: Vec<TrafficEvent>,
    selected: String,
    query: Entity<InputState>,
    protocol_select: ChoiceState,
    protocol: String,
    failures_only: bool,
    live: bool,
    connected: bool,
    loading: bool,
    error: String,
    /// Stops the current history/stream worker.
    stop: Arc<AtomicBool>,
    _feed: Task<()>,
    _subs: Vec<Subscription>,
}

impl EventEmitter<PageEvent> for TrafficPage {}

fn short_method(method: &str) -> String {
    let parts: Vec<&str> = method.split('/').filter(|p| !p.is_empty()).collect();
    if parts.len() < 2 { return method.to_string() }
    let service = parts[parts.len() - 2].rsplit('.').next().unwrap_or_default();
    format!("{service}/{}", parts[parts.len() - 1])
}

fn is_success(event: &TrafficEvent) -> bool {
    if event.protocol.starts_with("grpc") { return event.status == "OK" }
    event.status.parse::<i64>().map(|s| (200..400).contains(&s)).unwrap_or(false)
}

fn payload_text(payload: &TrafficPayload) -> String {
    match payload.encoding.as_str() {
        "empty" => t("traffic.noContent"),
        "json" => serde_json::to_string_pretty(&payload.data).unwrap_or_default(),
        _ => payload.data.as_str().map(str::to_string).unwrap_or_else(|| payload.data.to_string()),
    }
}

fn clock(value: &str) -> String {
    chrono::DateTime::parse_from_rfc3339(value).map(|t| t.with_timezone(&chrono::Local).format("%H:%M:%S").to_string()).unwrap_or_default()
}

impl TrafficPage {
    pub fn new(api: Api, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let query = forms::text_state("", &t("traffic.search"), window, cx);
        let protocol_select = forms::select_state(vec![
            Choice::new("all", t("traffic.all")), Choice::new("grpc", "gRPC"), Choice::new("grpc-stream", "gRPC compatibility"), Choice::new("http", "HTTP REST"),
        ], "all", window, cx);
        let subs = vec![
            cx.subscribe(&query, |_, _, e: &InputEvent, cx| if matches!(e, InputEvent::Change) { cx.notify() }),
            cx.subscribe(&protocol_select, |this: &mut Self, _, e: &SelectEvent<_>, cx| if let SelectEvent::Confirm(Some(v)) = e { this.protocol = v.to_string(); cx.notify() }),
        ];
        let mut page = TrafficPage { api, server_running: false, events: Vec::new(), selected: String::new(), query, protocol_select, protocol: "all".into(), failures_only: false, live: true, connected: false, loading: true, error: String::new(), stop: Arc::new(AtomicBool::new(true)), _feed: Task::ready(()), _subs: subs };
        page.start(cx);
        page
    }

    /// Called by the shell whenever the server state may have changed.
    pub fn set_server_running(&mut self, running: bool, cx: &mut Context<Self>) {
        if running == self.server_running { return }
        self.server_running = running;
        self.start(cx);
        cx.notify();
    }

    /// (Re)loads history and, while live and the server runs, follows the stream.
    fn start(&mut self, cx: &mut Context<Self>) {
        self.stop.store(true, Ordering::Relaxed);
        let stop = Arc::new(AtomicBool::new(false));
        self.stop = stop.clone();
        let (sender, mut receiver) = futures::channel::mpsc::unbounded::<Feed>();
        let api = self.api.clone();
        let after = self.events.first().map(|e| e.id.clone()).unwrap_or_default();
        let follow = self.live && self.server_running;
        std::thread::Builder::new().name("traffic-stream".into()).spawn(move || {
            let history = api.traffic(500, &after);
            let mut latest = history.as_ref().ok().and_then(|events| events.first().map(|e| e.id.clone())).unwrap_or(after);
            if sender.unbounded_send(Feed::History(history)).is_err() || !follow { return }
            while !stop.load(Ordering::Relaxed) {
                let _ = sender.unbounded_send(Feed::Live(true));
                let from = latest.clone();
                let result = api.traffic_stream(&from, &stop, |event| {
                    latest = event.id.clone();
                    let _ = sender.unbounded_send(Feed::Event(event));
                });
                if stop.load(Ordering::Relaxed) { break }
                let _ = sender.unbounded_send(Feed::Live(false));
                if let Err(error) = result { let _ = sender.unbounded_send(Feed::Error(error.to_string())); }
                std::thread::sleep(Duration::from_secs(1));
            }
        }).expect("spawn traffic stream");
        self._feed = cx.spawn(async move |this, cx| {
            while let Some(message) = receiver.next().await {
                if this.update(cx, |page, cx| { page.apply(message); cx.notify() }).is_err() { return }
            }
        });
    }

    fn merge(&mut self, next: Vec<TrafficEvent>) {
        if next.is_empty() { return }
        let known: HashSet<String> = self.events.iter().map(|e| e.id.clone()).collect();
        let mut merged: Vec<_> = next.into_iter().filter(|e| !known.contains(&e.id)).collect();
        merged.append(&mut self.events);
        merged.truncate(500);
        self.events = merged;
        if self.selected.is_empty() { self.selected = self.events.first().map(|e| e.id.clone()).unwrap_or_default() }
    }

    fn apply(&mut self, message: Feed) {
        match message {
            Feed::History(Ok(events)) => { self.merge(events); self.error.clear(); self.loading = false }
            Feed::History(Err(error)) => { self.error = error.to_string(); self.loading = false }
            Feed::Event(event) => { self.merge(vec![event]); self.error.clear() }
            Feed::Live(live) => self.connected = live,
            Feed::Error(error) => self.error = error,
        }
    }

    fn payload(title: String, payload: &TrafficPayload, p: &Palette) -> Div {
        let meta = format!("{} · {}{}", payload.encoding, tf("traffic.bytes", &[("count", &number(payload.size_bytes))]), if payload.truncated.unwrap_or(false) { t("traffic.truncated") } else { String::new() });
        div().flex_1().min_w_0().flex().flex_col().border_1().border_color(p.line).rounded(px(10.)).overflow_hidden()
            .child(div().flex().justify_between().gap(px(12.)).px(px(14.)).py(px(10.)).bg(p.surface_soft).text_size(px(12.))
                .child(div().font_weight(FontWeight::BOLD).child(title)).child(div().text_color(p.muted).child(meta)))
            .child(div().id(SharedString::from(format!("payload-{}", payload.size_bytes))).max_h(px(520.)).overflow_y_scroll().p(px(14.)).bg(p.code_bg).text_color(p.code_ink)
                .font_family("Cascadia Mono").text_size(px(12.)).child(payload_text(payload)))
    }
}

impl Render for TrafficPage {
    fn render(&mut self, _: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let p = pal(cx);
        let (compact, narrow) = (kit::compact(cx), kit::narrow(cx));
        let needle = forms::text(&self.query, cx).trim().to_lowercase();
        let filtered: Vec<TrafficEvent> = self.events.iter().filter(|e| {
            (self.protocol == "all" || e.protocol == self.protocol)
                && !(self.failures_only && is_success(e))
                && (needle.is_empty() || format!("{} {} {}", e.method, e.player_id.clone().unwrap_or_default(), e.status).to_lowercase().contains(&needle))
        }).cloned().collect();
        let selected = filtered.iter().find(|e| e.id == self.selected).or(filtered.first()).cloned();
        let failures = self.events.iter().filter(|e| !is_success(e)).count();
        let stream = t(if !self.server_running { "traffic.serverStopped" } else if !self.live { "traffic.paused" } else if self.connected { "traffic.live" } else if !self.error.is_empty() { "traffic.reconnecting" } else { "traffic.connecting" });
        let live_color = if self.server_running && self.live && self.connected { p.brand_dark } else { p.muted };

        let rows = if self.loading {
            (0..8).map(|_| kit::skeleton(&p).h(px(56.)).mx(px(8.)).my(px(4.)).into_any_element()).collect::<Vec<_>>()
        } else if filtered.is_empty() {
            vec![div().flex().flex_col().items_center().justify_center().gap(px(10.)).min_h(px(240.)).text_color(p.muted).child(icon("radio", 24., p.muted)).child(div().font_weight(FontWeight::BOLD).text_color(p.ink).child(t("traffic.empty"))).into_any_element()]
        } else {
            filtered.iter().map(|e| {
                let success = is_success(e);
                let chosen = selected.as_ref().map(|s| s.id == e.id).unwrap_or(false);
                let id = e.id.clone();
                let http = e.protocol == "http";
                let (badge_bg, badge_fg): (Hsla, Hsla) = if http { if p.dark { (rgb(0x233149).into(), rgb(0xB9D1F3).into()) } else { (rgb(0xE6EEF9).into(), rgb(0x285D9F).into()) } } else { (p.surface_raised, p.muted) };
                div().id(SharedString::from(format!("row-{}", e.id))).flex().items_center().gap(px(10.)).px(px(14.)).py(px(10.)).border_b_1().border_color(p.line).cursor_pointer()
                    .bg(if chosen { p.brand_soft } else { p.panel }).hover({ let h = p.surface_hover; move |s| if chosen { s } else { s.bg(h) } })
                    .child(icon(if success { "circle-check" } else { "circle-alert" }, 16., if success { p.brand } else { p.danger }))
                    .child(div().flex_1().min_w_0().flex().flex_col().child(div().truncate().text_size(px(12.)).font_weight(FontWeight::BOLD).child(short_method(&e.method)))
                        .child(div().text_size(px(10.)).text_color(p.muted).child(format!("{} · {} ms", clock(&e.captured_at), e.duration_ms))))
                    .child(kit::badge(if http { "HTTP" } else { "gRPC" }, badge_bg, badge_fg))
                    .child(div().text_size(px(11.)).font_weight(FontWeight::BOLD).text_color(if success { p.ink } else { p.danger }).child(e.status.clone()))
                    .on_click(cx.listener(move |this, _, _, cx| { this.selected = id.clone(); cx.notify() }))
                    .into_any_element()
            }).collect()
        };

        let detail = match &selected {
            None => div().flex().flex_col().items_center().justify_center().gap(px(10.)).min_h(px(320.)).text_color(p.muted).child(icon("radio", 24., p.muted)).child(t("traffic.select")),
            Some(e) => {
                let success = is_success(e);
                let meta = |label: String, value: String| div().flex_1().min_w_0().child(div().text_size(px(11.)).text_color(p.muted).child(label)).child(div().mt(px(4.)).truncate().text_size(px(12.)).font_weight(FontWeight::BOLD).child(value));
                div().flex().flex_col().gap(px(16.)).p(px(20.))
                    .child(div().flex().items_start().justify_between().gap(px(16.))
                        .child(div().min_w_0().flex().flex_col().gap(px(4.))
                            .child(div().text_size(px(11.)).text_color(p.muted).child(format!("{} · {}", e.protocol, chrono::DateTime::parse_from_rfc3339(&e.captured_at).map(|t| t.with_timezone(&chrono::Local).format("%d/%m/%Y %H:%M:%S").to_string()).unwrap_or_default())))
                            .child(div().text_size(px(20.)).font_weight(FontWeight::EXTRA_BOLD).child(short_method(&e.method)))
                            .child(div().text_size(px(11.)).font_family("Cascadia Mono").text_color(p.muted).child(e.method.clone())))
                        .child(div().px(px(10.)).py(px(6.)).rounded(px(6.)).border_1().text_size(px(12.)).font_weight(FontWeight::BOLD)
                            .border_color(if success { p.brand } else { p.danger_line }).bg(if success { p.brand_soft } else { p.danger_soft }).text_color(if success { p.brand_dark } else { p.danger }).child(e.status.clone())))
                    .child(div().flex().gap(px(16.)).p(px(14.)).rounded(px(10.)).bg(p.surface_soft)
                        .child(meta(t("traffic.duration"), format!("{} ms", e.duration_ms)))
                        .child(meta(t("traffic.player"), e.player_id.clone().filter(|v| !v.is_empty()).unwrap_or_else(|| t("traffic.unidentified"))))
                        .child(meta(t("traffic.identifier"), e.id.clone())))
                    .when_some(e.error.clone().filter(|v| !v.is_empty()), |d, err| d.child(div().flex().gap(px(8.)).p(px(12.)).rounded(px(8.)).border_1().border_color(p.danger_line).bg(p.danger_soft).text_color(p.danger)
                        .child(icon("circle-alert", 16., p.danger)).child(div().font_family("Cascadia Mono").text_size(px(12.)).child(err))))
                    .child(div().flex().gap(px(12.)).when(compact, |d| d.flex_col()).child(Self::payload(t("traffic.request"), &e.request, &p)).child(Self::payload(t("traffic.response"), &e.response, &p)))
            }
        };
        let live = self.live;
        div().size_full().p(px(32.)).flex().flex_col()
            .child(div().mb(px(24.)).flex().items_center().justify_between()
                .child(kit::heading(t("traffic.title"), &p))
                .child(kit::button("live", Kind::Secondary, t(if live { "traffic.pause" } else { "traffic.resume" }), Some(if live { "circle-pause" } else { "circle-play" }), false, &p)
                    .on_click(cx.listener(|this, _, _, cx| { this.live = !this.live; this.connected = false; this.start(cx); cx.notify() }))))
            .child(div().mb(px(16.)).flex().gap(px(20.)).text_size(px(12.)).text_color(p.muted)
                .child(div().flex().items_center().gap(px(6.)).text_color(live_color).child(icon("radio", 15., live_color)).child(stream))
                .child(tf("traffic.loaded", &[("count", &self.events.len())]))
                .child(div().text_color(if failures > 0 { p.danger } else { p.muted }).child(tf("traffic.failed", &[("count", &failures)]))))
            .child(div().mb(px(16.)).flex().flex_wrap().items_end().gap(px(12.)).p(px(12.)).rounded(px(8.)).bg(p.tint)
                .child(div().flex_1().child(forms::input(&self.query).prefix(icon("search", 18., p.muted))))
                .child(div().w(px(220.)).child(kit::field(t("traffic.protocol"), forms::select(&self.protocol_select), None, &p)))
                .child(kit::checkbox("failures", self.failures_only, t("traffic.errorsOnly"), &p).on_click(cx.listener(|this, _, _, cx| { this.failures_only = !this.failures_only; cx.notify() }))))
            .when(!self.error.is_empty(), |d| d.child(div().mb(px(16.)).flex().items_center().gap(px(8.)).p(px(12.)).rounded(px(8.)).border_1().border_color(p.danger_line).bg(p.danger_soft).text_color(p.danger)
                .child(icon("circle-alert", 16., p.danger)).child(self.error.clone())))
            // Web: 320px list under 1100px; list above the details when narrower.
            .child(div().flex().flex_1().min_h_0().gap(px(16.)).when(narrow, |d| d.flex_col())
                .child(div().id("traffic-list").flex_none().when(narrow, |d| d.w_full().h(px(260.))).when(!narrow, |d| d.w(px(if compact { 320. } else { 420. })).h_full()).overflow_y_scroll().rounded(px(12.)).border_1().border_color(p.line).bg(p.panel).children(rows))
                .child(div().id("traffic-detail").flex_1().min_w_0().min_h_0().overflow_y_scroll().rounded(px(12.)).border_1().border_color(p.line).bg(p.panel).child(detail)))
    }
}

impl Drop for TrafficPage {
    fn drop(&mut self) { self.stop.store(true, Ordering::Relaxed) }
}
