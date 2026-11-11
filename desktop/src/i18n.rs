//! Same translation catalogue as the web panel (generated from
//! `web/src/i18n/locales/*.ts`), with `{name}` interpolation.

use std::collections::HashMap;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::OnceLock;

static FRENCH: AtomicBool = AtomicBool::new(false);

fn table(french: bool) -> &'static HashMap<String, String> {
    static FR: OnceLock<HashMap<String, String>> = OnceLock::new();
    static EN: OnceLock<HashMap<String, String>> = OnceLock::new();
    if french {
        FR.get_or_init(|| serde_json::from_str(include_str!("../locales/fr_FR.json")).unwrap_or_default())
    } else {
        EN.get_or_init(|| serde_json::from_str(include_str!("../locales/en_US.json")).unwrap_or_default())
    }
}

pub const LANGUAGES: &[(&str, &str)] = &[("fr_FR", "Français"), ("en_US", "English")];

pub fn locale() -> &'static str { if FRENCH.load(Ordering::Relaxed) { "fr_FR" } else { "en_US" } }

pub fn set_locale(locale: &str) { FRENCH.store(locale == "fr_FR", Ordering::Relaxed) }

pub fn t(key: &str) -> String {
    let french = FRENCH.load(Ordering::Relaxed);
    table(french).get(key).or_else(|| table(true).get(key)).cloned().unwrap_or_else(|| key.to_string())
}

pub fn tf(key: &str, values: &[(&str, &dyn std::fmt::Display)]) -> String {
    let mut message = t(key);
    for (name, value) in values {
        message = message.replace(&format!("{{{name}}}"), &value.to_string());
    }
    message
}

/// Group digits like `Intl.NumberFormat` (narrow no-break space in French).
pub fn number(value: i64) -> String {
    let digits = value.unsigned_abs().to_string();
    let sep = if FRENCH.load(Ordering::Relaxed) { "\u{202F}" } else { "," };
    let mut out = String::new();
    for (i, ch) in digits.chars().enumerate() {
        if i > 0 && (digits.len() - i) % 3 == 0 { out.push_str(sep) }
        out.push(ch);
    }
    if value < 0 { format!("-{out}") } else { out }
}
