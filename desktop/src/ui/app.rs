//! Application shell: sidebar, control top bar, global errors, status polling
//! and page routing. Port of `web/src/App.tsx`.

use super::forms::{self, Choice, ChoiceState};
use super::kit::{self, icon, pal, Colors, Kind};
use super::pages::{accounts::AccountsPage, logs::LogsPage, packs::PacksPage, settings::SettingsPage, traffic::TrafficPage};
use super::tasks::spawn_api_in;
use crate::api::{AndroidDevice, Api, Bootstrap, ControlStatus, PreparationState};
use crate::backend::{Installation, Preferences};
use crate::i18n::{number, t, tf};
use crate::theme::{Palette, ThemeSettings};
use gpui_kit::component::select::SelectEvent;
use gpui_kit::*;
use gpui_kit::prelude::FluentBuilder as _;
use std::sync::Arc;
use std::time::{Duration, Instant};

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
pub enum Route {
    Accounts,
    Packs,
    Traffic,
    Logs,
    Settings,
}

impl Route {
    fn key(self) -> &'static str {
        match self { Route::Accounts => "accounts", Route::Packs => "packs", Route::Traffic => "traffic", Route::Logs => "logs", Route::Settings => "settings" }
    }
    fn parse(value: &str) -> Route {
        match value { "packs" => Route::Packs, "traffic" => Route::Traffic, "logs" => Route::Logs, "settings" => Route::Settings, _ => Route::Accounts }
    }
}

/// Events pages send to the shell.
pub enum PageEvent {
    Error(String),
    Refresh,
    Theme(ThemeSettings),
    Locale(String),
}

#[derive(Clone, PartialEq)]
enum Backend {
    Connecting,
    Starting(Instant),
    Ready,
    Failed(String),
}

pub struct AppView {
    inst: Arc<Installation>,
    api: Api,
    prefs: Preferences,
    system_dark: bool,
    route: Route,
    backend: Backend,
    control: ControlStatus,
    status_loaded: bool,
    data: Option<Bootstrap>,
    devices: Vec<AndroidDevice>,
    pending: Option<String>,
    error: String,
    connection_error: String,
    device_error: String,
    refreshing: u8,
    restarting: bool,
    action_lock: bool,
    device_select: ChoiceState,
    accounts: Option<(String, Entity<AccountsPage>)>,
    packs: Option<Entity<PacksPage>>,
    traffic: Option<(String, Entity<TrafficPage>)>,
    logs: Option<Entity<LogsPage>>,
    settings: Option<Entity<SettingsPage>>,
    _tasks: Vec<Task<()>>,
    /// Displayed preparation progress, eased toward the server value.
    shown_progress: f32,
    _subscriptions: Vec<Subscription>,
}

fn default_control() -> ControlStatus {
    ControlStatus { mode: "unknown".into(), ..Default::default() }
}

impl AppView {
    pub fn new(inst: Arc<Installation>, prefs: Preferences, window: &mut Window, cx: &mut Context<Self>) -> Self {
        let api = Api::new(&inst.address);
        let system_dark = matches!(window.appearance(), WindowAppearance::Dark | WindowAppearance::VibrantDark);
        let device_select = forms::select_state(Vec::new(), "", window, cx);
        let sub = cx.subscribe_in(&device_select, window, |this: &mut Self, _, event: &SelectEvent<_>, window, cx| {
            let SelectEvent::Confirm(Some(value)) = event else { return };
            this.change_device(value.to_string(), window, cx);
        });
        let appearance = cx.observe_window_appearance(window, |this: &mut Self, window, cx| {
            this.system_dark = matches!(window.appearance(), WindowAppearance::Dark | WindowAppearance::VibrantDark);
            this.apply_theme(cx);
        });
        let mut view = AppView {
            route: Route::parse(&prefs.route),
            inst,
            api,
            prefs,
            system_dark,
            backend: Backend::Connecting,
            control: default_control(),
            status_loaded: false,
            data: None,
            devices: Vec::new(),
            pending: None,
            error: String::new(),
            connection_error: String::new(),
            device_error: String::new(),
            refreshing: 0,
            restarting: false,
            action_lock: false,
            device_select,
            accounts: None,
            packs: None,
            traffic: None,
            logs: None,
            settings: None,
            _tasks: Vec::new(),
            shown_progress: 0.0,
            _subscriptions: vec![sub, appearance],
        };
        view.apply_theme(cx);
        view.connect(window, cx);
        view
    }

    pub fn palette(prefs: &Preferences, system_dark: bool) -> Palette { Palette::new(&prefs.theme, system_dark) }

    fn apply_theme(&mut self, cx: &mut Context<Self>) {
        let palette = Arc::new(Palette::new(&self.prefs.theme, self.system_dark));
        forms::sync_component_theme(&palette, cx);
        cx.set_global(Colors(palette));
        cx.refresh_windows();
    }

    /// Connects to the persistent panel, starting it if no panel answers.
    fn connect(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let api = self.api.clone();
        let inst = self.inst.clone();
        let background = cx.background_executor().clone();
        let task = cx.spawn_in(window, async move |this, cx| {
            let probe = api.clone();
            let healthy = background.spawn(async move { probe.health() }).await;
            if !healthy {
                let started = background.spawn(async move { inst.start_panel() }).await;
                if let Err(error) = started {
                    let _ = this.update(cx, |v, cx| { v.backend = Backend::Failed(error.to_string()); cx.notify() });
                    return;
                }
                let _ = this.update(cx, |v, cx| { v.backend = Backend::Starting(Instant::now()); cx.notify() });
                let deadline = Instant::now() + Duration::from_secs(45);
                loop {
                    background.timer(Duration::from_millis(10)).await;
                    let probe = api.clone();
                    if background.spawn(async move { probe.health() }).await { break }
                    if Instant::now() > deadline {
                        let _ = this.update(cx, |v, cx| { v.backend = Backend::Failed(t("app.statusUnavailable")); cx.notify() });
                        return;
                    }
                }
            }
            let _ = this.update_in(cx, |v, window, cx| {
                v.backend = Backend::Ready;
                v.refresh(window, cx);
                v.start_polling(window, cx);
            });
            // Accounts are served a few hundred milliseconds after start-up, once
            // the existing data is loaded. Ask for them directly until then.
            for _ in 0..100 {
                let Ok(loaded) = this.update(cx, |v, _| v.data.is_some()) else { return };
                if loaded { break }
                let probe = api.clone();
                if let Ok(data) = background.spawn(async move { probe.bootstrap() }).await {
                    let _ = this.update_in(cx, |v, window, cx| { if v.data.is_none() { v.set_data(Some(data), window, cx); cx.notify() } });
                    break;
                }
                background.timer(Duration::from_millis(150)).await;
            }
        });
        self._tasks.push(task);
    }

    fn poll_interval(&self) -> Duration {
        let update = self.control.update.as_ref().map(|u| u.phase.as_str());
        let prep = self.control.preparation.as_ref();
        Duration::from_millis(if prep.map(|p| p.phase.as_str()) == Some("restoring") { 200 } else if update == Some("downloading") { 1000 } else if prep.map(|p| p.phase.as_str()) == Some("images") { 500 } else if prep.map(|p| p.ready).unwrap_or(false) && !self.control.busy { 10_000 } else { 1_000 })
    }

    fn start_polling(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let background = cx.background_executor().clone();
        let api = self.api.clone();
        let status_task = cx.spawn_in(window, async move |this, cx| loop {
            let Ok(interval) = this.update(cx, |v, _| v.poll_interval()) else { return };
            background.timer(interval).await;
            let Ok(skip) = this.update(cx, |v, _| v.action_lock || v.restarting || v.refreshing > 0) else { return };
            if skip { continue }
            let probe = api.clone();
            let result = background.spawn(async move {
                let status = probe.control_status()?;
                let bootstrap = if !status.busy && status.preparation.as_ref().map(|p| p.ready).unwrap_or(false) { Some(probe.bootstrap()?) } else { None };
                anyhow::Ok((status, bootstrap))
            }).await;
            let _ = this.update_in(cx, |v, window, cx| {
                if v.action_lock || v.restarting { return }
                match result {
                    Ok((status, bootstrap)) => {
                        v.connection_error = status.android.recovery_error.clone().unwrap_or_default();
                        v.control = status;
                        if let Some(bootstrap) = bootstrap { v.pending = None; v.set_data(Some(bootstrap), window, cx) }
                    }
                    Err(error) => v.connection_error = error.to_string(),
                }
                cx.notify();
            });
        });
        let api = self.api.clone();
        let background = cx.background_executor().clone();
        let device_task = cx.spawn_in(window, async move |this, cx| loop {
            background.timer(Duration::from_secs(10)).await;
            let Ok(skip) = this.update(cx, |v, _| v.action_lock || v.restarting) else { return };
            if skip { continue }
            let probe = api.clone();
            let found = background.spawn(async move { probe.devices() }).await;
            let _ = this.update_in(cx, |v, window, cx| v.apply_devices(found, window, cx));
        });
        self._tasks.push(status_task);
        self._tasks.push(device_task);
    }

    fn apply_devices(&mut self, found: anyhow::Result<Vec<AndroidDevice>>, window: &mut Window, cx: &mut Context<Self>) {
        match found {
            Ok(devices) => { self.devices = devices; self.device_error.clear() }
            Err(error) => self.device_error = tf("app.deviceDiscoveryFailed", &[("message", &error)]),
        }
        self.sync_device_select(window, cx);
        cx.notify();
    }

    fn available_devices(&self) -> Vec<AndroidDevice> { self.devices.iter().filter(|d| d.state == "device").cloned().collect() }

    fn device_choice(&self) -> String {
        let selected = self.control.android.serial.clone().or_else(|| self.control.preparation.as_ref().and_then(|p| p.serial.clone())).unwrap_or_default();
        self.available_devices().into_iter()
            .find(|d| d.serial == selected || d.aliases.as_ref().map(|a| a.contains(&selected)).unwrap_or(false))
            .map(|d| d.serial).unwrap_or_default()
    }

    fn sync_device_select(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let choices = self.available_devices().into_iter().map(|d| Choice::new(d.serial.clone(), format!("{} · {}", d.name, d.serial))).collect();
        let choice = self.device_choice();
        forms::set_choices(&self.device_select, choices, &choice, window, cx);
    }

    fn set_data(&mut self, data: Option<Bootstrap>, window: &mut Window, cx: &mut Context<Self>) {
        if let (Some(data), Some((_, page))) = (&data, &self.accounts) {
            let data = data.clone();
            page.update(cx, |page, cx| page.update_bootstrap(data, window, cx));
        }
        self.data = data;
    }

    /// Loads status, devices and accounts concurrently. Accounts (a few ms)
    /// render without waiting for the ADB-backed status (seconds).
    pub fn refresh(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.refreshing > 0 { return }
        self.refreshing = 3;
        self.error.clear();
        spawn_api_in(window, cx, &self.api, |api| api.bootstrap(), |this, result, window, cx| {
            match result {
                Ok(data) => this.set_data(Some(data), window, cx),
                // Not prepared yet: the status tells the shell what to show.
                Err(_) => {}
            }
            this.refreshing -= 1;
        });
        spawn_api_in(window, cx, &self.api, |api| api.devices(), |this, result, window, cx| {
            this.apply_devices(result, window, cx);
            this.refreshing -= 1;
        });
        spawn_api_in(window, cx, &self.api, |api| api.control_status(), |this, result, window, cx| {
            match result {
                Ok(status) => {
                    this.connection_error = status.android.recovery_error.clone().unwrap_or_default();
                    let ready = status.preparation.as_ref().map(|p| p.ready).unwrap_or(false);
                    this.control = status;
                    if !ready { this.set_data(None, window, cx) }
                }
                Err(error) => this.error = error.to_string(),
            }
            this.sync_device_select(window, cx);
            this.refreshing -= 1;
            this.status_loaded = true;
        });
    }

    fn controls_busy(&self) -> bool { self.restarting || self.pending.is_some() || self.control.busy || self.control.csrf_token.is_empty() }

    fn change_mode(&mut self, action: &'static str, window: &mut Window, cx: &mut Context<Self>) {
        if self.action_lock || self.control.busy { return }
        self.action_lock = true;
        self.pending = Some(action.into());
        self.error.clear();
        spawn_api_in(window, cx, &self.api, move |api| {
            match api.control_action(action) {
                Ok(status) => {
                    let bootstrap = if action != "stop" && !status.busy && status.preparation.as_ref().map(|p| p.ready).unwrap_or(false) { api.bootstrap().ok() } else { None };
                    Ok((Ok(status), bootstrap))
                }
                Err(error) => {
                    let status = api.control_status();
                    let bootstrap = match &status { Ok(s) if !s.busy => api.bootstrap().ok(), _ => None };
                    Ok((Err((error.to_string(), status.ok())), bootstrap))
                }
            }
        }, move |this, result, window, cx| {
            let (outcome, bootstrap) = result.unwrap();
            match outcome {
                Ok(status) => {
                    this.control = status;
                    if action == "stop" { cx.quit(); return }
                }
                Err((message, status)) => {
                    if let Some(status) = status { this.control = status }
                    this.error = message;
                }
            }
            if let Some(bootstrap) = bootstrap { this.set_data(Some(bootstrap), window, cx) }
            this.action_lock = false;
            this.pending = None;
        });
    }

    fn change_device(&mut self, serial: String, window: &mut Window, cx: &mut Context<Self>) {
        if self.action_lock || self.restarting || self.control.busy || self.control.csrf_token.is_empty() || Some(&serial) == self.control.android.serial.as_ref() { return }
        self.action_lock = true;
        self.pending = Some("device".into());
        self.error.clear();
        spawn_api_in(window, cx, &self.api, move |api| {
            let status = api.select_device(&serial)?;
            let bootstrap = if status.preparation.as_ref().map(|p| p.ready).unwrap_or(false) { Some(api.bootstrap()?) } else { None };
            Ok((status, bootstrap))
        }, |this, result, window, cx| {
            match result {
                Ok((status, bootstrap)) => {
                    this.connection_error = status.android.recovery_error.clone().unwrap_or_default();
                    this.control = status;
                    this.set_data(bootstrap, window, cx);
                }
                Err(error) => this.error = error.to_string(),
            }
            this.action_lock = false;
            this.pending = None;
            this.sync_device_select(window, cx);
        });
    }

    fn start_preparation(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        if self.action_lock || self.restarting || self.control.busy || self.control.csrf_token.is_empty() { return }
        self.action_lock = true;
        self.pending = Some("prepare".into());
        self.error.clear();
        let serial = self.device_choice();
        spawn_api_in(window, cx, &self.api, move |api| {
            let found = api.devices()?;
            let available: Vec<_> = found.iter().filter(|d| d.state == "device").collect();
            let selected = available.iter().find(|d| d.serial == serial);
            if available.len() > 1 && selected.is_none() { anyhow::bail!(t("prepare.chooseDevice")) }
            if !serial.is_empty() && selected.is_none() { anyhow::bail!(t("app.deviceUnavailable")) }
            api.prepare(selected.map(|d| d.serial.as_str()).unwrap_or(""))?;
            Ok(())
        }, |this, result, window, cx| {
            if let Err(error) = result { this.error = error.to_string() }
            this.action_lock = false;
            this.pending = None;
            this.refresh(window, cx);
        });
    }

    fn apply_update(&mut self, window: &mut Window, cx: &mut Context<Self>) {
        let Some(update) = self.control.update.clone() else { return };
        if self.restarting || self.action_lock || self.control.busy || update.phase != "ready" { return }
        self.restarting = true;
        self.error.clear();
        self.connection_error.clear();
        let target = update.version.unwrap_or_default();
        spawn_api_in(window, cx, &self.api, move |api| {
            let previous = api.install_update()?.get("previousPID").and_then(|v| v.as_i64()).unwrap_or(0);
            let deadline = Instant::now() + Duration::from_secs(120);
            while Instant::now() < deadline {
                std::thread::sleep(Duration::from_secs(1));
                let Ok(health) = api.health_json() else { continue };
                let pid = health.get("pid").and_then(|v| v.as_i64()).unwrap_or(0);
                if health.get("healthy").and_then(|v| v.as_bool()) == Some(true) && pid != previous {
                    if let Some(message) = health.get("updateError").and_then(|v| v.as_str()).filter(|m| !m.is_empty()) {
                        anyhow::bail!(tf("prepare.updateRolledBack", &[("message", &message)]))
                    }
                    if health.get("version").and_then(|v| v.as_str()) == Some(target.as_str()) { return Ok(()) }
                }
            }
            anyhow::bail!(t("prepare.restartFailure"))
        }, |this, result, window, cx| {
            this.restarting = false;
            if let Err(error) = result { this.error = error.to_string() }
            this.refresh(window, cx);
        });
    }

    fn navigate(&mut self, route: Route, cx: &mut Context<Self>) {
        self.route = route;
        self.prefs.route = route.key().into();
        self.inst.save_preferences(&self.prefs);
        cx.notify();
    }

    fn on_page_event(&mut self, event: &PageEvent, window: &mut Window, cx: &mut Context<Self>) {
        match event {
            PageEvent::Error(message) => self.error = message.clone(),
            PageEvent::Refresh => self.refresh(window, cx),
            PageEvent::Theme(theme) => {
                self.prefs.theme = theme.clone().sanitized();
                self.inst.save_preferences(&self.prefs);
                self.apply_theme(cx);
            }
            PageEvent::Locale(locale) => {
                crate::i18n::set_locale(locale);
                self.prefs.locale = locale.clone();
                self.inst.save_preferences(&self.prefs);
                // Pages are rebuilt so every label is retranslated (web: key={locale}).
                self.accounts = None;
                self.packs = None;
                self.traffic = None;
                self.logs = None;
                self.refresh(window, cx);
            }
        }
        cx.notify();
    }

    // ---- rendering -------------------------------------------------------

    fn mode_title(&self) -> String {
        let s = &self.control;
        let key = if s.mode == "local" && s.server.running { "app.privateActive" } else if s.mode == "local" { "app.localIncomplete" } else if s.mode == "online" { "app.officialMode" } else if s.mode == "stopped" { "app.serverStopped" } else if s.server.running { "app.serverDeviceMissing" } else { "app.emulatorDisconnected" };
        t(key)
    }

    fn operation_title(operation: Option<&str>) -> String {
        t(match operation {
            Some("device") => "app.switchingDevice",
            Some("prepare") => "prepare.operation",
            Some("update") => "prepare.updateOperation",
            Some("local") => "app.activatingPrivate",
            Some("online") => "app.switchingOfficial",
            Some("stop") => "app.stopping",
            Some("open") => "app.openingAccount",
            _ => "app.operation",
        })
    }

    fn effective_pending(&self) -> Option<String> {
        if self.restarting { return Some("update".into()) }
        self.pending.clone().or_else(|| if self.control.busy { self.control.operation.clone() } else { None })
    }

    fn mode_dot(&self, p: &Palette) -> Div {
        let (color, ring) = match self.control.mode.as_str() {
            "local" => (p.brand, 0.12),
            "online" => (p.online, 0.12),
            "stopped" => (p.stopped, 0.10),
            _ => (p.neutral_dot, 0.10),
        };
        let mut halo = color;
        halo.a = ring;
        div().size(px(17.)).flex_none().rounded_full().bg(halo).flex().items_center().justify_center().child(div().size(px(9.)).rounded_full().bg(color))
    }

    fn render_sidebar(&self, p: &Palette, cx: &mut Context<Self>) -> impl IntoElement {
        // Under 950px the sidebar becomes an icon rail.
        let rail = kit::narrow(cx);
        let item = |route: Route, name: &'static str, label: String, top: f32, cx: &mut Context<Self>| {
            let active = self.route == route;
            let (bg, fg) = if active { (p.sidebar_ink, p.sidebar) } else { (transparent_black(), p.sidebar_muted) };
            let (hover_ink, mut hover_bg) = (p.sidebar_ink, p.sidebar_ink);
            hover_bg.a = 0.07;
            div().id(name).mt(px(top)).flex().items_center().gap(px(12.)).h(px(44.)).px(px(12.)).rounded(px(8.)).bg(bg).text_color(fg)
                .text_size(px(13.)).font_weight(FontWeight::SEMIBOLD).cursor_pointer()
                .when(rail, |d| d.justify_center().px_0())
                .when(!active, |d| d.hover(move |s| s.bg(hover_bg).text_color(hover_ink)))
                .child(icon(name, 18., fg)).when(!rail, |d| d.child(label))
                .on_click(cx.listener(move |this, _, _, cx| this.navigate(route, cx)))
        };
        let mut border = p.sidebar_ink;
        border.a = 0.11;
        let width = if rail { 72. } else if kit::compact(cx) { 208. } else { 244. };
        div().flex().flex_col().flex_none().w(px(width)).h_full().bg(p.sidebar).text_color(p.sidebar_ink).border_r_1().border_color(p.line)
            .child(div().flex().items_center().gap(px(12.)).h(px(84.)).px(px(if rail { 0. } else { 20. })).when(rail, |d| d.justify_center()).border_b_1().border_color(border).window_control_area(WindowControlArea::Drag)
                .child(div().size(px(40.)).flex_none().flex().items_center().justify_center().child(kit::logo(36., p.sidebar_ink)))
                // Wraps onto two lines when the sidebar is narrow, like the web panel.
                .when(!rail, |d| d.child(div().flex_1().min_w_0().text_size(px(15.)).font_weight(FontWeight::BOLD).line_height(px(19.)).child("PTCGP Private Server"))))
            .child(div().flex().flex_col().gap(px(4.)).p(px(12.))
                .child(item(Route::Accounts, "boxes", t("app.accounts"), 0., cx))
                .child(item(Route::Packs, "circle-dot", t("app.packStudio"), 0., cx))
                .child(item(Route::Traffic, "activity", t("app.traffic"), 0., cx))
                .child(item(Route::Logs, "file-text", t("app.logs"), 0., cx))
                .child(item(Route::Settings, "settings", t("app.settings"), 12., cx)))
            .child(div().flex_1())
            .when(!rail, |d| d.child(self.render_update(p, cx)))
    }

    fn render_update(&self, p: &Palette, cx: &mut Context<Self>) -> AnyElement {
        let state = self.control.update.clone();
        let phase = state.as_ref().map(|s| s.phase.as_str()).unwrap_or("idle");
        if !self.restarting && matches!(phase, "idle" | "current" | "unconfigured" | "") { return div().into_any_element() }
        let ready = phase == "ready";
        let downloading = phase == "downloading";
        let progress = state.as_ref().filter(|s| s.total > 0).map(|s| ((s.completed as f32 / s.total as f32) * 100.0).min(100.0)).unwrap_or(0.0);
        let label = t(if self.restarting { "prepare.restarting" } else if ready { "prepare.updateButton" } else if downloading { "prepare.updateDownloading" } else if phase == "offline" { "prepare.updateOffline" } else if phase == "incompatible" { "prepare.updateIncompatible" } else { "prepare.updateError" });
        let name = if self.restarting { "rotate-cw" } else if downloading { "loader-circle" } else if ready { "arrow-down-to-line" } else { "circle-alert" };
        let version = state.as_ref().and_then(|s| s.version.clone());
        let mut wrap = div().p(px(12.)).flex().flex_col();
        if ready || downloading || self.restarting {
            let disabled = self.controls_busy() || !ready || self.restarting;
            let (bg, fg) = if ready && !self.restarting { (p.brand, p.on_brand) } else { ({ let mut c = p.sidebar_ink; c.a = 0.08; c }, p.sidebar_ink) };
            let mut button = div().id("update").flex().items_center().justify_center().gap(px(7.)).min_h(px(32.)).px(px(9.)).py(px(6.)).rounded(px(9.)).bg(bg).text_color(fg)
                .child(icon(name, 15., fg))
                .child(div().flex().flex_col().text_size(px(12.)).font_weight(FontWeight::SEMIBOLD).child(label)
                    .when(downloading, |d| d.child(div().text_size(px(11.)).child(format!("{}%{}", progress.round(), version.as_ref().map(|v| format!(" · {v}")).unwrap_or_default())))));
            button = if disabled { button.opacity(if ready { 0.65 } else { 1.0 }) } else { button.cursor_pointer().hover({ let b = p.brand_dark; move |s| s.bg(b) }).on_click(cx.listener(|this, _, window, cx| this.apply_update(window, cx))) };
            wrap = wrap.child(button);
            if downloading {
                let mut track = p.sidebar_ink;
                track.a = 0.12;
                wrap = wrap.child(div().mx(px(8.)).mt(px(6.)).h(px(3.)).rounded(px(2.)).bg(track).child(div().h_full().rounded(px(2.)).bg(p.brand).w(relative(progress / 100.0))));
            }
        } else {
            wrap = wrap.child(div().flex().gap(px(10.)).text_color(p.sidebar_muted).child(icon(name, 18., p.sidebar_muted))
                .child(div().flex().flex_col().gap(px(4.)).child(div().text_size(px(12.)).font_weight(FontWeight::SEMIBOLD).child(label))
                    .when(phase == "error", |d| d.child(div().text_size(px(11.)).child(state.as_ref().map(|s| s.message.clone()).unwrap_or_default())))));
        }
        wrap.into_any_element()
    }

    fn render_topbar(&self, p: &Palette, cx: &mut Context<Self>) -> Div {
        let (compact, narrow) = (kit::compact(cx), kit::narrow(cx));
        let busy = self.controls_busy();
        let restoring = self.control.preparation.as_ref().map(|s| s.phase == "restoring").unwrap_or(false);
        // Loading existing data at start-up is not a preparation.
        let title = if busy && restoring { t("common.loading") } else if busy { Self::operation_title(self.effective_pending().as_deref()) } else { self.mode_title() };
        let active = self.data.as_ref().and_then(|d| d.players.iter().find(|p| p.active)).map(|p| tf("app.activeProfile", &[("name", &p.display_name)]));
        let prepared = self.control.preparation.as_ref().map(|p| p.ready).unwrap_or(false);
        let mode = self.control.mode.clone();
        let switch_button = |id: &'static str, name: &'static str, label: String, selected: bool, disabled: bool, color: Hsla, cx: &mut Context<Self>| {
            let fg = if selected { color } else { p.muted };
            let mut el = div().id(id).flex().items_center().justify_center().gap(px(6.)).px(px(8.)).flex_1().min_w_0().h_full().rounded(px(7.))
                .text_size(px(11.)).font_weight(FontWeight::EXTRA_BOLD).text_color(fg)
                .when(selected, |d| d.bg(p.panel).shadow_sm())
                .child(icon(name, 15., fg)).child(div().truncate().child(label));
            if disabled { el = el.opacity(0.55) } else {
                let (ink, mut hover) = (p.ink, p.panel);
                hover.a = 0.7;
                el = el.cursor_pointer().when(!selected, |d| d.hover(move |s| s.bg(hover).text_color(ink))).on_click(cx.listener(move |this, _, window, cx| this.change_mode(id, window, cx)));
            }
            el
        };
        let status = |width: Option<f32>| {
            let d = div().flex().items_center().gap(px(8.)).min_w_0().child(self.mode_dot(p))
                .child(div().min_w_0().truncate().text_size(px(11.)).font_weight(FontWeight::EXTRA_BOLD).text_color(p.ink).child(title.clone()));
            match width { Some(w) => d.w(px(w)).flex_none(), None => d }
        };
        let switch = div().flex().flex_none().w(px(188.)).h(px(40.)).p(px(3.)).rounded(px(10.)).border_1().border_color(p.line).bg(p.surface_soft)
            .child(switch_button("local", "server", t("app.local"), mode == "local", busy || !prepared || !self.control.android.connected, p.brand_dark, cx))
            .child(switch_button("online", "globe", t("app.official"), mode == "online", busy, if p.dark { p.online } else { rgb(0x285D9F).into() }, cx));
        let stop = {
            let mut stop = div().id("stop").flex().flex_none().items_center().justify_center().gap(px(6.)).w(px(if narrow { 40. } else { 112. })).h(px(40.)).px(px(if narrow { 0. } else { 10. })).rounded(px(8.)).border_1().border_color(p.danger_line)
                .bg(p.panel).text_color(p.danger).text_size(px(11.)).font_weight(FontWeight::EXTRA_BOLD)
                .child(icon("power", 15., p.danger)).when(!narrow, |d| d.child(div().truncate().child(t("app.stopAll"))));
            if busy { stop = stop.opacity(0.55) } else {
                let (d, ds) = (p.danger, p.danger_soft);
                stop = stop.cursor_pointer().hover(move |s| s.border_color(d).bg(ds)).on_click(cx.listener(|this, _, window, cx| this.change_mode("stop", window, cx)));
            }
            stop
        };
        // Wide: status, switch, stop. Compact (web <= 1100px): the status moves to the left.
        let mut controls = div().flex().flex_none().items_center().gap(px(10.));
        if !compact { controls = controls.child(status(Some(190.))) }
        let controls = controls.child(switch).child(stop);

        let devices = self.available_devices();
        let placeholder = t(if devices.is_empty() { "app.noDevices" } else { "app.chooseDevice" });
        let refreshing = self.refreshing > 0;
        let refresh = {
            let label = t(if refreshing { "app.refreshing" } else { "app.refresh" });
            let mut b = if compact {
                // Icon only, like the web panel between 761 and 1100px.
                let mut b = div().id("refresh").flex().flex_none().items_center().justify_center().size(px(40.)).rounded(px(6.)).child(icon("refresh-cw", 16., p.muted));
                if busy || refreshing { b = b.opacity(0.55) } else { let h = kit::hover_tint(p); b = b.cursor_pointer().hover(move |s| s.bg(h)) }
                b
            } else {
                kit::button("refresh", Kind::Quiet, label, Some("refresh-cw"), busy || refreshing, p)
            };
            if !(busy || refreshing) { b = b.on_click(cx.listener(|this, _, window, cx| this.refresh(window, cx))) }
            b
        };
        let actions_width = if narrow { 200. } else if compact { 228. } else { 304. };
        let actions = div().flex().flex_none().items_center().gap(px(8.)).w(px(actions_width))
            .child(div().flex().flex_1().min_w_0().items_center().gap(px(6.)).child(icon("smartphone", 16., p.muted))
                .child(div().flex_1().min_w_0().child(forms::select(&self.device_select).placeholder(placeholder).disabled(busy || refreshing || devices.is_empty()))))
            .child(refresh);
        // Only empty space moves the window: GPUI resolves the first
        // registered area under the cursor, so a parent drag area would
        // swallow the clicks of every button inside it.
        let context = div().flex_1().min_w_0().h(px(40.)).flex().items_center().overflow_hidden().window_control_area(WindowControlArea::Drag);
        let context = if compact { context.child(status(None)) } else {
            // Shown only when it has room next to the controls.
            context.when_some(active.filter(|_| kit::width(cx) >= 1400.), |d, name| d.child(div().text_size(px(12.)).font_weight(FontWeight::SEMIBOLD).text_color(p.ink).truncate().child(name)))
        };
        // Always one line: narrower windows shorten the labels instead.
        div().flex_1().min_w_0().bg(p.panel).border_b_1().border_color(p.line).px(px(if narrow { 16. } else { 24. }))
            .flex().items_center().gap(px(if narrow { 10. } else { 16. })).h(px(72.))
            .child(context)
            .child(controls)
            .child(div().flex_1().min_w(px(actions_width)).flex().justify_end().child(actions))
    }

    fn render_error(&self, p: &Palette, cx: &mut Context<Self>) -> Option<AnyElement> {
        let message = [&self.error, &self.connection_error, &self.device_error].into_iter().find(|m| !m.is_empty())?.clone();
        let (border, bg, fg): (Hsla, Hsla, Hsla) = if p.dark { (p.danger_line, p.danger_soft, p.danger) } else { (rgb(0xFECACA).into(), rgb(0xFEF2F2).into(), rgb(0x991B1B).into()) };
        Some(div().mx(px(32.)).mt(px(20.)).flex().items_center().justify_between().gap(px(16.)).border_1().border_color(border).bg(bg).px(px(16.)).py(px(12.))
            .text_size(px(14.)).text_color(fg)
            .child(div().flex_1().child(message))
            .child(div().id("close-error").text_size(px(12.)).font_weight(FontWeight::BOLD).cursor_pointer().child(t("common.close"))
                .on_click(cx.listener(|this, _, _, cx| { this.error.clear(); this.connection_error.clear(); this.device_error.clear(); cx.notify() })))
            .into_any_element())
    }

    fn render_preparation(&mut self, p: &Palette, window: &mut Window, cx: &mut Context<Self>) -> AnyElement {
        let state = self.control.preparation.clone().unwrap_or(PreparationState { phase: "checking".into(), message: t("prepare.connecting"), busy: true, ..Default::default() });
        let pending = self.pending.as_deref() == Some("prepare");
        let image_count = if state.phase == "images" && state.total > 0 { tf("prepare.imageCount", &[("done", &number(state.completed)), ("total", &number(state.total))]) } else { String::new() };
        let phase = if ["checking", "master", "images", "validating", "ready", "blocked"].contains(&state.phase.as_str()) { t(&format!("prepare.{}", state.phase)) } else if !state.message.is_empty() { state.message.clone() } else { t("prepare.connecting") };
        let activity = if pending { t("prepare.starting") } else if !image_count.is_empty() { format!("{phase} · {image_count}") } else { phase };
        let ratio = |s: &PreparationState| if s.total > 0 { (s.completed as f32 / s.total as f32).clamp(0.0, 1.0) } else { 0.0 };
        let progress = if pending { None } else if state.phase == "images" && state.total > 0 { Some(ratio(&state)) } else if state.phase == "ready" { Some(1.0) } else if state.phase == "blocked" { Some(ratio(&state)) } else { None };
        let fill = if state.phase == "blocked" { p.danger } else { p.brand };
        let track = div().relative().w_full().h(px(10.)).overflow_hidden().rounded_full().bg(p.surface_raised)
            .child(match progress {
                Some(target) => {
                    // Ease toward the reported value (CSS: width transition 600ms).
                    let delta = target - self.shown_progress;
                    if delta.abs() > 0.001 {
                        self.shown_progress += delta * 0.12;
                        window.request_animation_frame();
                    } else {
                        self.shown_progress = target;
                    }
                    div().h_full().rounded_full().bg(fill).w(relative(self.shown_progress)).into_any_element()
                }
                None => {
                    self.shown_progress = 0.0;
                    // Indeterminate sweep (CSS: preparation-sweep, 1.4s).
                    div().absolute().top_0().bottom_0().w(relative(0.35)).rounded_full().bg(fill)
                        .with_animation("preparation-sweep", Animation::new(Duration::from_millis(1400)).repeat().with_easing(ease_in_out), |bar, t| bar.left(relative(-0.35 + 1.35 * t)))
                        .into_any_element()
                }
            });
        let mut page = div().w_full().max_w(px(680.)).mx_auto().py(px(48.)).px(px(32.)).flex().flex_col()
            .child(div().flex().items_center().gap(px(16.)).child(icon("download", 28., p.brand)).child(kit::heading(t("prepare.title"), p)))
            .child(div().mt(px(16.)).mb(px(8.)).text_color(p.ink).line_height(relative(1.6)).child(t("prepare.intro")))
            .child(div().text_size(px(14.)).text_color(p.muted).line_height(relative(1.6)).child(t("prepare.requirements")))
            .child(div().mt(px(44.)).mb(px(24.)).flex().flex_col().gap(px(12.)).child(track).child(div().text_size(px(13.)).text_color(p.muted).child(activity)));
        if let Some(error) = state.error.clone().filter(|e| !pending && !e.is_empty()) {
            page = page.child(div().p(px(16.)).my(px(16.)).border_1().border_color(p.danger_line).rounded(px(8.)).text_color(p.danger).bg(p.danger_soft).child(error));
        }
        if !state.busy {
            let label = t(if pending { "prepare.starting" } else if state.phase == "blocked" { "prepare.retry" } else { "prepare.start" });
            let disabled = self.controls_busy();
            let mut b = kit::button("prepare", Kind::Primary, label, None, disabled, p);
            if !disabled { b = b.on_click(cx.listener(|this, _, window, cx| this.start_preparation(window, cx))) }
            page = page.child(div().flex().child(b));
        }
        page.into_any_element()
    }

    fn render_message_page(&self, title: String, message: Option<String>, p: &Palette, cx: &mut Context<Self>) -> AnyElement {
        div().p(px(32.)).flex().flex_col().gap(px(16.)).items_start()
            .child(kit::heading(title, p))
            .when_some(message, |d, m| d.child(div().text_color(p.muted).child(m)))
            .child(kit::button("refresh-page", Kind::Secondary, t("app.refresh"), None, false, p).on_click(cx.listener(|this, _, window, cx| this.refresh(window, cx))))
            .into_any_element()
    }

    fn loading(&self, p: &Palette) -> AnyElement {
        div().size_full().min_h(px(400.)).flex().items_center().justify_center().gap(px(12.)).text_color(p.muted).child(icon("loader-circle", 18., p.muted)).child(t("common.loading")).into_any_element()
    }

    fn render_route(&mut self, p: &Palette, window: &mut Window, cx: &mut Context<Self>) -> AnyElement {
        let ready = self.control.preparation.as_ref().map(|s| s.ready).unwrap_or(false);
        let have_data = self.data.is_some();
        let restoring = self.control.preparation.as_ref().map(|s| s.phase == "restoring").unwrap_or(false);
        match self.route {
            Route::Accounts if have_data && (restoring || !self.status_loaded) => self.render_accounts(p, window, cx),
            Route::Accounts | Route::Packs if restoring => self.loading(p),
            Route::Accounts | Route::Packs if !self.status_loaded => self.loading(p),
            Route::Accounts | Route::Packs if self.control.csrf_token.is_empty() => self.render_message_page(t("app.statusUnavailable"), None, p, cx),
            Route::Accounts | Route::Packs if !ready => self.render_preparation(p, window, cx),
            Route::Accounts => self.render_accounts(p, window, cx),
            Route::Packs => {
                if self.packs.is_none() {
                    let api = self.api.clone();
                    let page = cx.new(|cx| PacksPage::new(api, window, cx));
                    let sub = cx.subscribe_in(&page, window, |this, _, event: &PageEvent, window, cx| this.on_page_event(event, window, cx));
                    self._subscriptions.push(sub);
                    self.packs = Some(page);
                }
                self.packs.clone().unwrap().into_any_element()
            }
            Route::Traffic => {
                let key = self.control.android.serial.clone().unwrap_or_default();
                if self.traffic.as_ref().map(|(k, _)| k != &key).unwrap_or(true) {
                    let api = self.api.clone();
                    let page = cx.new(|cx| TrafficPage::new(api, window, cx));
                    self.traffic = Some((key, page));
                }
                let running = self.control.server.running;
                let (_, page) = self.traffic.as_ref().unwrap();
                page.update(cx, |page, cx| page.set_server_running(running, cx));
                page.clone().into_any_element()
            }
            Route::Logs => {
                if self.logs.is_none() {
                    let api = self.api.clone();
                    self.logs = Some(cx.new(|cx| LogsPage::new(api, window, cx)));
                }
                self.logs.clone().unwrap().into_any_element()
            }
            Route::Settings => {
                if self.settings.is_none() {
                    let theme = self.prefs.theme.clone();
                    let page = cx.new(|cx| SettingsPage::new(theme, window, cx));
                    let sub = cx.subscribe_in(&page, window, |this, _, event: &PageEvent, window, cx| this.on_page_event(event, window, cx));
                    self._subscriptions.push(sub);
                    self.settings = Some(page);
                }
                let system_dark = self.system_dark;
                let page = self.settings.clone().unwrap();
                page.update(cx, |page, _| page.system_dark = system_dark);
                page.into_any_element()
            }
        }
    }

    fn render_accounts(&mut self, p: &Palette, window: &mut Window, cx: &mut Context<Self>) -> AnyElement {
                let Some(data) = self.data.clone() else { return self.render_message_page(t("app.accounts"), Some(if self.error.is_empty() { t("app.loadAccounts") } else { self.error.clone() }), p, cx) };
            let key = data.emulator.clone().unwrap_or_default();
            if self.accounts.as_ref().map(|(k, _)| k != &key).unwrap_or(true) {
                let api = self.api.clone();
                let devices = self.available_devices();
                let page = cx.new(|cx| AccountsPage::new(api, data, devices, window, cx));
                let sub = cx.subscribe_in(&page, window, |this, _, event: &PageEvent, window, cx| this.on_page_event(event, window, cx));
                self._subscriptions.push(sub);
                self.accounts = Some((key, page));
            }
            let can_launch = self.control.mode == "local" && self.control.server.running && self.control.android.connected && !self.controls_busy();
            let devices = self.available_devices();
            let (_, page) = self.accounts.as_ref().unwrap();
            page.update(cx, |page, _| page.set_context(can_launch, devices));
            page.clone().into_any_element()
            }

    fn render_backend(&self, p: &Palette) -> Option<AnyElement> {
        let message = match &self.backend {
            Backend::Ready => return None,
            Backend::Connecting => t("prepare.connecting"),
            Backend::Starting(since) => format!("{} ({:.1} s)", t("prepare.connecting"), since.elapsed().as_secs_f32()),
            Backend::Failed(error) => error.clone(),
        };
        let failed = matches!(self.backend, Backend::Failed(_));
        Some(div().size_full().flex().flex_col().items_center().justify_center().gap(px(12.)).text_color(if failed { p.danger } else { p.muted })
            .child(icon(if failed { "circle-alert" } else { "loader-circle" }, 20., if failed { p.danger } else { p.muted }))
            .child(message).into_any_element())
    }
}

impl Render for AppView {
    fn render(&mut self, window: &mut Window, cx: &mut Context<Self>) -> impl IntoElement {
        let p = pal(cx);
        window.set_rem_size(px(p.ui_size));
        let width = window.viewport_size().width / px(1.);
        let previous = kit::width(cx);
        if (previous - width).abs() > 0.5 {
            cx.set_global(kit::Viewport(width));
            // Pages are cached views: redraw them for the new width.
            if let Some((_, page)) = &self.accounts { page.update(cx, |_, cx| cx.notify()) }
            if let Some(page) = &self.packs { page.update(cx, |_, cx| cx.notify()) }
            if let Some((_, page)) = &self.traffic { page.update(cx, |_, cx| cx.notify()) }
            if let Some(page) = &self.logs { page.update(cx, |_, cx| cx.notify()) }
            if let Some(page) = &self.settings { page.update(cx, |_, cx| cx.notify()) }
        }
        let body = match self.render_backend(&p) {
            Some(waiting) => waiting,
            None => {
                // Live pages keep a fixed frame: only their lists scroll, so the
                // page does not move while new entries arrive.
                let fixed = matches!(self.route, Route::Logs | Route::Traffic);
                let route = self.render_route(&p, window, cx);
                let error = self.render_error(&p, cx);
                let content = if fixed {
                    div().flex_1().min_h_0().flex().flex_col()
                        .when_some(error, |d, e| d.child(div().flex_none().child(e)))
                        .child(div().flex_1().min_h_0().child(route))
                        .into_any_element()
                } else {
                    div().id("main-scroll").flex_1().min_h_0().overflow_y_scroll()
                        .when_some(error, |d, e| d.child(e))
                        .child(route)
                        .into_any_element()
                };
                div().flex().flex_col().size_full().min_h_0()
                    .child(div().flex().flex_none().child(self.render_topbar(&p, cx)).child(window_controls(window.is_maximized(), &p)))
                    .child(content)
                    .into_any_element()
            }
        };
        // No system title bar: the sidebar reaches the top edge, the control bar
        // moves the window and carries the window buttons.
        div().flex().size_full().bg(p.paper).text_color(p.ink).font_family("Segoe UI").text_size(px(p.ui_size)).font_weight(FontWeight::MEDIUM)
            .child(self.render_sidebar(&p, cx))
            .child(div().flex_1().min_w_0().h_full().child(body))
    }
}

/// Minimise, maximise/restore and close, drawn in the control bar. Windows
/// handles the clicks through the non-client hit test.
fn window_controls(maximized: bool, p: &Palette) -> Div {
    let button = |id: &'static str, name: &str, area: WindowControlArea, danger: bool| {
        let (hover_bg, hover_fg): (Hsla, Hsla) = if danger { (rgb(0xC42B1C).into(), white()) } else { (kit::hover_tint(p), p.ink) };
        div().id(id).w(px(46.)).h(px(32.)).flex().items_center().justify_center().text_color(p.muted)
            .hover(move |s| s.bg(hover_bg).text_color(hover_fg))
            .window_control_area(area)
            // No explicit colour: the icon follows the hover text colour.
            .child(gpui_kit::component::Icon::empty().path(SharedString::from(format!("icons/{name}.svg"))).size(px(15.)))
    };
    div().flex().flex_col().flex_none().bg(p.panel).border_b_1().border_color(p.line)
        .child(div().flex()
            .child(button("window-min", "minus", WindowControlArea::Min, false))
            .child(button("window-max", if maximized { "copy" } else { "square" }, WindowControlArea::Max, false))
            .child(button("window-close", "x", WindowControlArea::Close, true)))
        // The space under the buttons still moves the window.
        .child(div().flex_1().w_full().window_control_area(WindowControlArea::Drag))
}
