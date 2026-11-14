//! Blocking client for the panel's HTTP API (same endpoints and CSRF rules as
//! the web panel it replaced). Calls run on GPUI's background executor.

use anyhow::{anyhow, Result};
use serde::de::DeserializeOwned;
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::sync::{Arc, Mutex};
use std::time::Duration;


/// Go encodes empty slices as `null`; read them as empty collections.
fn null_default<'de, D: serde::Deserializer<'de>, T: Default + Deserialize<'de>>(d: D) -> Result<T, D::Error> {
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}

/// Pack outputs may contain `null` entries (`[[…], null]`).
fn pack_slots<'de, D: serde::Deserializer<'de>>(d: D) -> Result<Option<Vec<Vec<RuleSlot>>>, D::Error> {
    let raw = Option::<Vec<Option<Vec<RuleSlot>>>>::deserialize(d)?;
    Ok(raw.map(|packs| packs.into_iter().map(Option::unwrap_or_default).collect()))
}

#[derive(Clone, Debug, Default, Deserialize, PartialEq)]
#[serde(default)]
pub struct PreparationState {
    pub phase: String,
    pub message: String,
    pub ready: bool,
    pub busy: bool,
    pub completed: i64,
    pub total: i64,
    pub serial: Option<String>,
    pub error: Option<String>,
}

#[derive(Clone, Debug, Default, Deserialize, PartialEq)]
#[serde(default)]
pub struct AndroidDevice {
    pub serial: String,
    pub name: String,
    pub state: String,
    pub aliases: Option<Vec<String>>,
    /// "emulator", "usb" or "wifi".
    pub kind: String,
    pub emulator: String,
    pub instance: String,
    pub model: String,
}

#[derive(Clone, Debug, Default, Deserialize, PartialEq)]
#[serde(default, rename_all = "PascalCase")]
pub struct PlayerSummary {
    #[serde(rename = "ID")]
    pub id: String,
    pub display_name: String,
    pub level: i64,
    pub experience: i64,
    pub state_version: i64,
    pub active: bool,
    pub authorized: bool,
    pub in_use_on: Option<String>,
}

#[derive(Clone, Debug, Default, Deserialize, PartialEq)]
#[serde(default)]
pub struct Currency {
    #[serde(rename = "ID")]
    pub id: String,
    #[serde(rename = "Name")]
    pub name: String,
    #[serde(rename = "ImageURL")]
    pub image_url: String,
}

#[derive(Clone, Debug, Default, Deserialize, PartialEq)]
#[serde(default, rename_all = "camelCase")]
pub struct Bootstrap {
    pub emulator: Option<String>,
    pub csrf_token: String,
    pub catalog_version: String,
    #[serde(deserialize_with = "null_default")]
    pub players: Vec<PlayerSummary>,
    pub currencies: Option<Vec<Currency>>,
}

#[derive(Clone, Debug, Default, Deserialize, PartialEq)]
#[serde(default)]
pub struct ServerStatus {
    pub running: bool,
    pub pid: Option<i64>,
}

#[derive(Clone, Debug, Default, Deserialize, PartialEq)]
#[serde(default, rename_all = "camelCase")]
pub struct AndroidStatus {
    pub serial: Option<String>,
    pub recovery_error: Option<String>,
    pub connected: bool,
    pub root: bool,
    pub routing: String,
    pub ca: String,
    pub native: String,
    pub game: String,
    pub running: bool,
}

#[derive(Clone, Debug, Default, Deserialize, PartialEq)]
#[serde(default, rename_all = "camelCase")]
pub struct ControlStatus {
    pub preparation: Option<PreparationState>,
    pub csrf_token: String,
    pub busy: bool,
    pub operation: Option<String>,
    pub mode: String,
    pub server: ServerStatus,
    pub android: AndroidStatus,
    /// Latest progress message of the running operation.
    pub step: Option<String>,
    #[serde(deserialize_with = "null_default")]
    pub managed: Vec<ManagedDevice>,
}

/// Outcome of a Wi-Fi pairing or connection: the connected ADB serial, empty
/// when pairing worked but the connection address must still be entered.
#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct WirelessResult {
    pub serial: String,
}

/// A device the panel remembers, connected or not.
#[derive(Clone, Debug, Default, Deserialize, PartialEq)]
#[serde(default, rename_all = "camelCase")]
pub struct ManagedDevice {
    pub serial: String,
    pub selected: bool,
    pub running: bool,
    pub needs_restoration: bool,
    pub pending_restoration: bool,
}

#[derive(Clone, Debug, Default, Deserialize, PartialEq)]
#[serde(default)]
pub struct LogEvent {
    pub time: String,
    pub level: String,
    pub operation: String,
    pub message: String,
}

#[derive(Clone, Debug, Default, Deserialize, PartialEq)]
#[serde(default)]
pub struct ControlLogs {
    #[serde(deserialize_with = "null_default")]
    pub events: Vec<LogEvent>,
    #[serde(deserialize_with = "null_default")]
    pub diagnostics: Vec<String>,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
#[serde(default, rename_all = "PascalCase")]
pub struct StockAmount {
    #[serde(rename = "ID")]
    pub id: String,
    pub quantity: i64,
    pub total_quantity: Option<i64>,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, rename_all = "PascalCase")]
pub struct PlayerDetails {
    #[serde(rename = "ID")]
    pub id: String,
    pub display_name: String,
    pub level: i64,
    pub experience: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, rename_all = "PascalCase")]
pub struct ProfileSettings {
    pub language: String,
    pub country: String,
    #[serde(rename = "IconID")]
    pub icon_id: String,
    #[serde(rename = "MessageID")]
    pub message_id: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, rename_all = "PascalCase")]
pub struct Profile {
    pub player: PlayerDetails,
    pub settings: ProfileSettings,
    #[serde(rename = "ProfileImageURL")]
    pub profile_image_url: String,
    pub required_experience: i64,
    pub card_count: i64,
    #[serde(rename = "EmblemIDs")]
    pub emblem_ids: Option<Vec<String>>,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, rename_all = "PascalCase")]
pub struct VisualCatalogItem {
    #[serde(rename = "ID")]
    pub id: String,
    pub name: String,
    pub description: String,
    pub kind: String,
    pub stock_kind: String,
    #[serde(rename = "ImageURL")]
    pub image_url: String,
    pub variant: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, rename_all = "PascalCase")]
pub struct CatalogResult {
    #[serde(rename = "ID")]
    pub id: String,
    pub name: String,
    pub kind: String,
    pub meta: String,
    pub rarity: i64,
    #[serde(rename = "ImageURL")]
    pub image_url: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq)]
#[serde(default, rename_all = "PascalCase")]
pub struct NamedId {
    #[serde(rename = "ID")]
    pub id: String,
    pub name: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, rename_all = "PascalCase")]
pub struct CardPage {
    pub items: Option<Vec<CatalogResult>>,
    pub expansions: Option<Vec<NamedId>>,
    pub rarities: Option<Vec<i64>>,
    pub page: i64,
    pub page_size: i64,
    pub total: i64,
    pub total_pages: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, rename_all = "PascalCase")]
pub struct Pack {
    #[serde(rename = "ID")]
    pub id: String,
    pub name: String,
    pub description: String,
    #[serde(rename = "ExpansionID")]
    pub expansion_id: String,
    #[serde(rename = "ImageURL")]
    pub image_url: String,
    pub table_kinds: Option<Vec<String>>,
    pub rarities: Option<Vec<String>>,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq)]
#[serde(default, rename_all = "PascalCase")]
pub struct RuleSlot {
    #[serde(rename = "CardID")]
    pub card_id: String,
    pub locked: bool,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq)]
#[serde(default, rename_all = "PascalCase")]
pub struct IllegalPackRule {
    pub card_count: i64,
    pub allow_duplicates: bool,
    #[serde(rename = "ExpansionIDs")]
    pub expansion_ids: Option<Vec<String>>,
    pub rarities: Option<Vec<i64>>,
    pub card_kinds: Option<Vec<String>>,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq)]
#[serde(default, rename_all = "PascalCase")]
pub struct PackRule {
    pub mode: String,
    #[serde(rename = "TargetPackID")]
    pub target_pack_id: String,
    pub table_kind: String,
    pub rarity: String,
    pub return_pack_count: i64,
    pub free_openings: bool,
    pub fixed_seed: Option<i64>,
    #[serde(deserialize_with = "pack_slots")]
    pub packs: Option<Vec<Vec<RuleSlot>>>,
    pub illegal: IllegalPackRule,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub updated_at: Option<String>,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, rename_all = "PascalCase")]
pub struct RarityLabel {
    pub value: i64,
    pub label: String,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, rename_all = "PascalCase")]
pub struct PackFilters {
    pub expansions: Option<Vec<NamedId>>,
    pub rarities: Option<Vec<RarityLabel>>,
    pub card_kinds: Option<Vec<String>>,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default)]
pub struct PackStudio {
    pub rule: PackRule,
    #[serde(deserialize_with = "null_default")]
    pub packs: Vec<Pack>,
    pub filters: PackFilters,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct TrafficPayload {
    pub encoding: String,
    pub size_bytes: i64,
    pub truncated: Option<bool>,
    pub data: Value,
}

#[derive(Clone, Debug, Default, Deserialize)]
#[serde(default, rename_all = "camelCase")]
pub struct TrafficEvent {
    pub id: String,
    pub captured_at: String,
    pub protocol: String,
    pub method: String,
    pub player_id: Option<String>,
    pub status: String,
    pub duration_ms: f64,
    pub error: Option<String>,
    pub request: TrafficPayload,
    pub response: TrafficPayload,
}

#[derive(Default)]
struct Tokens {
    admin: String,
    control: String,
}

/// Shared HTTP client. Cloning is cheap.
#[derive(Clone)]
pub struct Api {
    pub base: Arc<str>,
    agent: ureq::Agent,
    tokens: Arc<Mutex<Tokens>>,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum Csrf {
    None,
    Admin,
    Control,
}

impl Api {
    pub fn new(address: &str) -> Self {
        let agent = ureq::AgentBuilder::new()
            .timeout_connect(Duration::from_millis(800))
            // Mode switches may last up to the server's 3-minute limit.
            .timeout(Duration::from_secs(200))
            .max_idle_connections_per_host(8)
            .build();
        Api { base: format!("http://{address}").into(), agent, tokens: Arc::default() }
    }

    pub fn url(&self, path: &str) -> String {
        if path.starts_with("http://") || path.starts_with("https://") { path.to_string() } else { format!("{}{}", self.base, path) }
    }

    fn call(&self, method: &str, path: &str, body: Option<Value>, csrf: Csrf) -> Result<(u16, Value)> {
        let mut request = self.agent.request(method, &self.url(path)).set("Accept", "application/json").set("X-PTCGP-Locale", crate::i18n::locale());
        {
            let tokens = self.tokens.lock().unwrap();
            // The panel uses double-submit cookies: echo each token as cookie and header.
            let mut cookies = Vec::new();
            if !tokens.admin.is_empty() { cookies.push(format!("ptcgp_csrf={}", tokens.admin)) }
            if !tokens.control.is_empty() { cookies.push(format!("ptcgp_control_csrf={}", tokens.control)) }
            if !cookies.is_empty() { request = request.set("Cookie", &cookies.join("; ")) }
            match csrf {
                Csrf::Admin => request = request.set("X-CSRF-Token", &tokens.admin),
                Csrf::Control => request = request.set("X-Control-CSRF-Token", &tokens.control),
                Csrf::None => {}
            }
        }
        let result = match body {
            Some(body) => request.send_json(body),
            None => request.call(),
        };
        let response = match result {
            Ok(response) => response,
            Err(ureq::Error::Status(_, response)) => response,
            Err(error) => return Err(anyhow!(crate::i18n::tf("common.httpError", &[("status", &error.kind())]))),
        };
        let status = response.status();
        // `into_string` caps bodies at 10 MB, and a full traffic history exceeds it.
        let mut body = Vec::new();
        std::io::Read::read_to_end(&mut response.into_reader(), &mut body)?;
        let value: Value = serde_json::from_slice(&body).unwrap_or(Value::Null);
        Ok((status, value))
    }

    fn json<T: DeserializeOwned>(&self, method: &str, path: &str, body: Option<Value>, csrf: Csrf) -> Result<T> {
        let (status, value) = self.call(method, path, body, csrf)?;
        if !(200..300).contains(&status) {
            let message = value.get("error").and_then(Value::as_str).map(str::to_string)
                .unwrap_or_else(|| crate::i18n::tf("common.httpError", &[("status", &status)]));
            return Err(anyhow!(message));
        }
        Ok(serde_json::from_value(value)?)
    }

    fn admin<T: DeserializeOwned>(&self, method: &str, path: &str, body: Option<Value>) -> Result<T> {
        self.json(method, path, body, if method == "GET" { Csrf::None } else { Csrf::Admin })
    }

    /// Whether the panel accepts connections. A bare TCP connect with a short
    /// timeout: on Windows, connecting to a closed loopback port otherwise
    /// waits for SYN retries (about two seconds).
    pub fn health(&self) -> bool {
        use std::net::ToSocketAddrs;
        let address = self.base.trim_start_matches("http://");
        address.to_socket_addrs().ok().and_then(|mut a| a.next())
            .map(|addr| std::net::TcpStream::connect_timeout(&addr, Duration::from_millis(50)).is_ok())
            .unwrap_or(false)
    }

    pub fn bootstrap(&self) -> Result<Bootstrap> {
        let value: Bootstrap = self.json("GET", "/api/bootstrap", None, Csrf::None)?;
        self.tokens.lock().unwrap().admin = value.csrf_token.clone();
        Ok(value)
    }

    pub fn control_status(&self) -> Result<ControlStatus> {
        let value: ControlStatus = self.json("GET", "/api/control/status", None, Csrf::None)?;
        self.tokens.lock().unwrap().control = value.csrf_token.clone();
        Ok(value)
    }

    pub fn control_action(&self, action: &str) -> Result<ControlStatus> {
        let (status, value) = self.call("POST", &format!("/api/control/actions/{action}"), None, Csrf::Control)?;
        if status == 409 { return self.control_status() }
        if !(200..300).contains(&status) {
            return Err(anyhow!(value.get("error").and_then(Value::as_str).map(str::to_string).unwrap_or_else(|| crate::i18n::tf("common.httpError", &[("status", &status)]))));
        }
        let value: ControlStatus = serde_json::from_value(value)?;
        self.tokens.lock().unwrap().control = value.csrf_token.clone();
        Ok(value)
    }

    pub fn control_logs(&self) -> Result<ControlLogs> { self.json("GET", "/api/control/logs", None, Csrf::None) }
    pub fn control_logs_export(&self) -> Result<String> {
        Ok(self.agent.get(&self.url("/api/control/logs/export")).call()?.into_string()?)
    }
    pub fn devices(&self) -> Result<Vec<AndroidDevice>> { self.json("GET", "/api/control/devices", None, Csrf::None) }
    pub fn prepare(&self, serial: &str) -> Result<Value> { self.json("POST", "/api/control/prepare", Some(serde_json::json!({ "serial": serial })), Csrf::Control) }
    pub fn select_device(&self, serial: &str) -> Result<ControlStatus> { self.json("POST", "/api/control/device", Some(serde_json::json!({ "serial": serial })), Csrf::Control) }
    pub fn forget_device(&self, serial: &str) -> Result<Value> { self.json("POST", "/api/control/forget", Some(serde_json::json!({ "serial": serial })), Csrf::Control) }
    /// Wi-Fi phone: pairs, then connects when the phone announces its port.
    pub fn pair_wireless(&self, address: &str, code: &str) -> Result<WirelessResult> { self.json("POST", "/api/control/wireless/pair", Some(serde_json::json!({ "address": address, "code": code })), Csrf::Control) }
    pub fn connect_wireless(&self, address: &str) -> Result<WirelessResult> { self.json("POST", "/api/control/wireless/connect", Some(serde_json::json!({ "address": address })), Csrf::Control) }
    /// Restores the official game and stops the backend so the program can be replaced.
    pub fn prepare_update(&self) -> Result<Value> { self.json("POST", "/api/control/update", None, Csrf::Control) }

    pub fn profile(&self, id: &str) -> Result<Profile> { self.admin("GET", &format!("/api/players/{id}/summary"), None) }
    pub fn inventory(&self, id: &str, section: &str, language: Option<i64>) -> Result<Vec<StockAmount>> {
        let path = match language {
            Some(language) => format!("/api/players/{id}/inventory/{section}?language={language}"),
            None => format!("/api/players/{id}/inventory/{section}"),
        };
        Ok(self.admin::<Option<Vec<StockAmount>>>("GET", &path, None)?.unwrap_or_default())
    }
    pub fn create_player(&self, input: Value) -> Result<Value> { self.admin("POST", "/api/players", Some(input)) }
    pub fn duplicate_player(&self, id: &str) -> Result<Value> { self.admin("POST", &format!("/api/players/{id}/duplicate"), None) }
    pub fn delete_player(&self, id: &str) -> Result<Value> { self.admin("DELETE", &format!("/api/players/{id}"), None) }
    pub fn reorder_players(&self, ids: &[String]) -> Result<Value> { self.admin("PUT", "/api/players/order", Some(serde_json::json!({ "PlayerIDs": ids }))) }
    pub fn update_profile(&self, id: &str, input: Value) -> Result<Profile> { self.admin("PATCH", &format!("/api/players/{id}"), Some(input)) }
    pub fn update_stock(&self, player: &str, kind: &str, id: &str, quantity: i64, language: Option<i64>) -> Result<StockAmount> {
        let mut body = serde_json::json!({ "Quantity": quantity });
        if kind == "cards" { body["Language"] = serde_json::json!(language.unwrap_or(4)) }
        self.admin("PUT", &format!("/api/players/{player}/{kind}/{}", encode(id)), Some(body))
    }
    pub fn open_player(&self, id: &str) -> Result<Value> { self.admin("POST", &format!("/api/players/{id}/open"), None) }
    pub fn cards(&self, query: &str, pack: &str) -> Result<Vec<CatalogResult>> {
        self.admin("GET", &format!("/api/catalog/cards?q={}&pack={}&limit=36", encode(query), encode(pack)), None)
    }
    pub fn card_page(&self, query: &str, expansion: &str, rarity: i64, page: i64) -> Result<CardPage> {
        let rarity = if rarity > 0 { rarity.to_string() } else { String::new() };
        self.admin("GET", &format!("/api/catalog/card-browser?q={}&expansion={}&rarity={rarity}&page={page}", encode(query), encode(expansion)), None)
    }
    pub fn catalog(&self, path: &str) -> Result<Vec<VisualCatalogItem>> { self.admin("GET", path, None) }
    pub fn pack_studio(&self) -> Result<PackStudio> { self.admin("GET", "/api/packs", None) }
    pub fn illegal_pool(&self, rule: &IllegalPackRule) -> Result<Value> { self.admin("POST", "/api/packs/pool", Some(serde_json::to_value(rule)?)) }
    pub fn save_pack_rule(&self, rule: &PackRule) -> Result<PackRule> { self.admin("PUT", "/api/packs/rule", Some(serde_json::to_value(rule)?)) }
    /// Follows `/api/traffic/stream` (server-sent events, as the web panel
    /// does) and calls `on_event` for each exchange until `stop` is set or the
    /// stream ends. Heartbeats arrive every 15 seconds.
    pub fn traffic_stream(&self, after: &str, stop: &std::sync::atomic::AtomicBool, mut on_event: impl FnMut(TrafficEvent)) -> Result<()> {
        use std::io::BufRead;
        use std::sync::atomic::Ordering;
        let agent = ureq::AgentBuilder::new().timeout_connect(Duration::from_secs(2)).timeout_read(Duration::from_secs(40)).build();
        let suffix = if after.is_empty() { String::new() } else { format!("?after={}", encode(after)) };
        let response = agent.get(&self.url(&format!("/api/traffic/stream{suffix}")))
            .set("Accept", "text/event-stream").set("X-PTCGP-Locale", crate::i18n::locale()).call()?;
        let reader = std::io::BufReader::new(response.into_reader());
        let (mut event, mut data) = (String::new(), String::new());
        for line in reader.lines() {
            if stop.load(Ordering::Relaxed) { return Ok(()) }
            let line = line?;
            if line.is_empty() {
                if event == "traffic" && !data.is_empty() {
                    if let Ok(value) = serde_json::from_str::<TrafficEvent>(&data) { on_event(value) }
                }
                event.clear();
                data.clear();
            } else if let Some(value) = line.strip_prefix("event:") {
                event = value.trim().to_string();
            } else if let Some(value) = line.strip_prefix("data:") {
                if !data.is_empty() { data.push('\n') }
                data.push_str(value.trim_start());
            }
        }
        Ok(())
    }

    pub fn traffic(&self, limit: usize, after: &str) -> Result<Vec<TrafficEvent>> {
        let suffix = if after.is_empty() { String::new() } else { format!("&after={}", encode(after)) };
        Ok(self.admin::<Option<Vec<TrafficEvent>>>("GET", &format!("/api/traffic?limit={limit}{suffix}"), None)?.unwrap_or_default())
    }
}

pub fn encode(value: &str) -> String {
    let mut out = String::new();
    for byte in value.bytes() {
        match byte {
            b'A'..=b'Z' | b'a'..=b'z' | b'0'..=b'9' | b'-' | b'_' | b'.' | b'~' => out.push(byte as char),
            _ => out.push_str(&format!("%{byte:02X}")),
        }
    }
    out
}
