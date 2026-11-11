//! Port of `LogsPage.tsx`: live journal with search, filters and diagnostics.

use crate::api::{Api, ControlLogs};
use crate::i18n::{t, tf};
use crate::ui::forms::{self, Choice, ChoiceState};
use crate::ui::kit::{self, icon, pal, Kind};
use crate::ui::tasks::spawn_api;
use gpui_kit::component::input::{InputEvent, InputState};
use gpui_kit::component::select::SelectEvent;
use gpui_kit::*;
use gpui_kit::prelude::FluentBuilder as _;
use std::time::Duration;

pub struct LogsPage {
    api: Api,
    logs: ControlLogs,
    query: Entity<InputState>,
    operation_select: ChoiceState,
    operation: String,
    errors_only: bool,
    live: bool,
    loading: bool,
    error: String,
    diagnostics_open: bool,
    exported: Option<String>,
    _poll: Task<()>,
    _subs: Vec<Subscription>,
}

fn operations() -> Vec<(&'static str, String)> {
    ["panel", "local", "online", "stop", "open"].into_iter().map(|k| (k, t(&format!("logs.op.{k}")))).collect()
}

impl LogsPage {
    pub fn new(api: Api, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let query = forms::text_state("", &t("common.search"), window, cx);
        let mut choices = vec![Choice::new("all", t("logs.allOperations"))];
        choices.extend(operations().into_iter().map(|(k, l)| Choice::new(k, l)));
        let operation_select = forms::select_state(choices, "all", window, cx);
        let subs = vec![
            cx.subscribe(&query, |_, _, event: &InputEvent, cx| if matches!(event, InputEvent::Change) { cx.notify() }),
            cx.subscribe(&operation_select, |this: &mut Self, _, event: &SelectEvent<_>, cx| {
                if let SelectEvent::Confirm(Some(value)) = event { this.operation = value.to_string(); cx.notify() }
            }),
        ];
        let mut page = LogsPage { api, logs: ControlLogs::default(), query, operation_select, operation: "all".into(), errors_only: false, live: true, loading: true, error: String::new(), diagnostics_open: false, exported: None, _poll: Task::ready(()), _subs: subs };
        page.start(cx);
        page
    }

    fn start(&mut self, cx: &mut Context<Self>) {
        let api = self.api.clone();
        let background = cx.background_executor().clone();
        self._poll = cx.spawn(async move |this, cx| loop {
            let probe = api.clone();
            let result = background.spawn(async move { probe.control_logs() }).await;
            let Ok(live) = this.update(cx, |page, cx| {
                match result {
                    Ok(logs) => { page.logs = logs; page.error.clear() }
                    Err(error) => page.error = error.to_string(),
                }
                page.loading = false;
                cx.notify();
                page.live
            }) else { return };
            if !live { return }
            background.timer(Duration::from_secs(1)).await;
        });
    }

    fn export(&mut self, cx: &mut Context<Self>) {
        spawn_api(cx, &self.api, |api| {
            let text = api.control_logs_export()?;
            let dir = std::env::var_os("USERPROFILE").map(std::path::PathBuf::from).map(|h| h.join("Downloads")).filter(|d| d.is_dir()).unwrap_or(std::env::temp_dir());
            let path = dir.join("ptcgp-logs.txt");
            std::fs::write(&path, text)?;
            Ok(path)
        }, |page, result, _| match result {
            Ok(path) => page.exported = Some(path.display().to_string()),
            Err(error) => page.error = error.to_string(),
        });
    }
}

impl Render for LogsPage {
    fn render(&mut self, _: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let p = pal(cx);
        let ops = operations();
        let label_of = |op: &str| ops.iter().find(|(k, _)| *k == op).map(|(_, l)| l.clone()).unwrap_or_else(|| op.to_string());
        let query = forms::text(&self.query, cx).to_lowercase();
        let filtered: Vec<_> = self.logs.events.iter().rev().filter(|e| {
            (self.operation == "all" || e.operation == self.operation)
                && (!self.errors_only || e.level == "error" || e.level == "warning")
                && format!("{} {}", e.message, label_of(&e.operation)).to_lowercase().contains(&query)
        }).cloned().collect();
        let status = t(if self.loading { "common.loading" } else if !self.error.is_empty() { "logs.offline" } else if self.live { "logs.live" } else { "logs.paused" });
        let count = filtered.len();
        let level_badge = |level: &str| {
            let (bg, fg) = match level {
                "error" => (p.danger_soft, p.danger),
                "warning" => (p.warning_soft, p.warning),
                "success" => (p.brand_soft, p.brand_dark),
                _ => (p.surface_soft, p.muted),
            };
            div().px(px(8.)).py(px(3.)).rounded(px(999.)).bg(bg).text_color(fg).text_size(px(11.)).font_weight(FontWeight::BOLD).child(t(&format!("logs.level.{level}")))
        };
        let header_cell = |text: String, w: Option<f32>| {
            let d = div().px(px(16.)).py(px(13.)).text_size(px(12.)).font_weight(FontWeight::SEMIBOLD).text_color(p.muted).child(text);
            match w { Some(w) => d.w(px(w)).flex_none(), None => d.flex_1() }
        };
        let rows = filtered.iter().enumerate().map(|(i, e)| {
            let time = format_time(&e.time);
            div().id(("log", i)).flex().items_start().border_t_1().border_color(p.line).text_size(px(13.))
                .child(div().w(px(150.)).flex_none().px(px(16.)).py(px(14.)).text_color(p.muted).child(time))
                .child(div().w(px(160.)).flex_none().px(px(16.)).py(px(14.)).child(label_of(&e.operation)))
                .child(div().w(px(120.)).flex_none().px(px(16.)).py(px(13.)).child(level_badge(&e.level)))
                .child(div().flex_1().min_w_0().px(px(16.)).py(px(14.)).line_height(relative(1.55)).child(e.message.clone()))
        }).collect::<Vec<_>>();
        let empty = (!self.loading && filtered.is_empty()).then(|| div().flex().flex_col().items_center().justify_center().gap(px(10.)).min_h(px(230.)).text_color(p.muted)
            .child(icon("file-text", 24., p.muted)).child(div().text_color(p.ink).font_weight(FontWeight::BOLD).child(t(if self.logs.events.is_empty() { "logs.empty" } else { "logs.noResult" }))));
        let live = self.live;
        div().size_full().max_w(px(1600.)).mx_auto().p(px(32.)).flex().flex_col()
            .child(div().mb(px(24.)).flex().items_start().justify_between().gap(px(24.))
                .child(div().text_size(px(28.)).font_weight(FontWeight::EXTRA_BOLD).child(t("logs.title")))
                .child(div().flex().gap(px(8.))
                    .child(kit::button("live", Kind::Secondary, t(if live { "logs.pause" } else { "logs.resume" }), Some(if live { "circle-pause" } else { "circle-play" }), false, &p)
                        .on_click(cx.listener(|this, _, _, cx| { this.live = !this.live; if this.live { this.start(cx) } cx.notify() })))
                    .child(kit::button("export", Kind::Secondary, t("logs.export"), Some("download"), false, &p).on_click(cx.listener(|this, _, _, cx| this.export(cx))))))
            .when_some(self.exported.clone(), |d, path| d.child(div().mb(px(12.)).text_size(px(12.)).text_color(p.brand_dark).child(path)))
            .child(div().flex().flex_wrap().items_end().gap(px(16.)).p(px(16.)).rounded(px(10.)).bg(p.surface_soft)
                .child(div().flex_1().min_w(px(260.)).child(forms::input(&self.query).prefix(icon("search", 17., p.muted))))
                .child(div().flex().flex_col().gap(px(6.)).w(px(220.)).text_size(px(12.)).text_color(p.muted).child(t("logs.operation")).child(forms::select(&self.operation_select)))
                .child(kit::checkbox("errors", self.errors_only, t("logs.errorsWarnings"), &p).on_click(cx.listener(|this, _, _, cx| { this.errors_only = !this.errors_only; cx.notify() }))))
            .child(div().flex().justify_between().mt(px(18.)).mb(px(10.)).text_size(px(12.)).text_color(p.muted)
                .child(status).child(tf("logs.eventCount", &[("count", &count), ("suffix", &if count != 1 { "s" } else { "" })])))
            .when(!self.error.is_empty(), |d| d.child(div().mb(px(10.)).p(px(12.)).rounded(px(8.)).border_1().border_color(p.danger_line).bg(p.danger_soft).text_color(p.danger).child(self.error.clone())))
            .child(div().flex_1().min_h(px(200.)).flex().flex_col().overflow_hidden().border_1().border_color(p.line).rounded(px(10.)).bg(p.panel)
                .child(div().flex().flex_none().bg(p.surface_soft).child(header_cell(t("logs.dateTime"), Some(150.))).child(header_cell(t("logs.operation"), Some(160.))).child(header_cell(t("logs.state"), Some(120.))).child(header_cell(t("logs.message"), None)))
                .child(div().id("logs-table").flex_1().min_h_0().overflow_y_scroll()
                    .children(rows)
                    .children(empty)))
            .child(div().mt(px(24.)).border_1().border_color(p.line).rounded(px(10.)).bg(p.panel).overflow_hidden()
                .child(div().id("diag").p(px(16.)).flex().gap(px(12.)).cursor_pointer().font_weight(FontWeight::SEMIBOLD)
                    .child(t("logs.technical")).child(div().text_size(px(12.)).text_color(p.muted).font_weight(FontWeight::NORMAL).child(tf("logs.lines", &[("count", &self.logs.diagnostics.len())])))
                    .on_click(cx.listener(|this, _, _, cx| { this.diagnostics_open = !this.diagnostics_open; cx.notify() })))
                .when(self.diagnostics_open, |d| d.child(div().id("diag-body").max_h(px(380.)).overflow_y_scroll().p(px(16.)).bg(p.code_bg).text_color(p.code_ink).font_family("Cascadia Mono").text_size(px(12.))
                    .child(if self.logs.diagnostics.is_empty() { t("logs.noOutput") } else { self.logs.diagnostics.join("\n") }))))
    }
}

/// `dd/MM HH:mm:ss` in local time from an RFC 3339 timestamp.
pub fn format_time(value: &str) -> String {
    chrono::DateTime::parse_from_rfc3339(value)
        .map(|t| t.with_timezone(&chrono::Local).format("%d/%m %H:%M:%S").to_string())
        .unwrap_or_else(|_| value.to_string())
}
