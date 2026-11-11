//! Port of `web/src/theme.ts`: the same presets, persistence rules and
//! colour derivation, so the desktop panel matches the browser panel.

use gpui_kit::{rgb, Hsla};
use serde::{Deserialize, Serialize};

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize, Default)]
#[serde(rename_all = "lowercase")]
pub enum Preference {
    #[default]
    System,
    Light,
    Dark,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Scheme {
    pub preset_id: String,
    pub accent: String,
    pub background: String,
    pub foreground: String,
    pub contrast: f32,
}

#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct ThemeSettings {
    pub version: u32,
    pub preference: Preference,
    pub light: Scheme,
    pub dark: Scheme,
    pub ui_font_size: f32,
}

pub struct Preset {
    pub id: &'static str,
    pub label: &'static str,
    pub dark: bool,
    pub accent: &'static str,
    pub background: &'static str,
    pub foreground: &'static str,
}

const fn p(id: &'static str, label: &'static str, dark: bool, accent: &'static str, background: &'static str, foreground: &'static str) -> Preset {
    Preset { id, label, dark, accent, background, foreground }
}

pub const PRESETS: &[Preset] = &[
    p("absolutely-light", "Absolutely", false, "#7557E8", "#FAFAFC", "#17151C"),
    p("catppuccin-latte", "Catppuccin", false, "#8839EF", "#EFF1F5", "#4C4F69"),
    p("codex-light", "Codex", false, "#0285FF", "#FFFFFF", "#0D0D0D"),
    p("everforest-light", "Everforest", false, "#8DA101", "#FDF6E3", "#5C6A72"),
    p("github-light", "GitHub", false, "#0969DA", "#FFFFFF", "#24292F"),
    p("gruvbox-light", "Gruvbox", false, "#D65D0E", "#FBF1C7", "#3C3836"),
    p("linear-light", "Linear", false, "#5E6AD2", "#FFFFFF", "#202124"),
    p("notion-light", "Notion", false, "#37352F", "#FFFFFF", "#37352F"),
    p("one-light", "One", false, "#4078F2", "#FAFAFA", "#383A42"),
    p("proof-light", "Proof", false, "#6C5CE7", "#FCFCFD", "#24242A"),
    p("raycast-light", "Raycast", false, "#E74646", "#FFFFFF", "#1A1A1A"),
    p("rose-pine-dawn", "Rose Pine", false, "#D7827E", "#FAF4ED", "#575279"),
    p("solarized-light", "Solarized", false, "#268BD2", "#FDF6E3", "#586E75"),
    p("vercel-light", "Vercel", false, "#000000", "#FFFFFF", "#111111"),
    p("vscode-light-plus", "VS Code Plus", false, "#0066B8", "#FFFFFF", "#333333"),
    p("xcode-light", "Xcode", false, "#0066CC", "#FFFFFF", "#292A30"),
    p("absolutely-dark", "Absolutely", true, "#9A86FF", "#121116", "#F5F2FF"),
    p("ayu-dark", "Ayu", true, "#E6B450", "#0B0E14", "#BFBDB6"),
    p("catppuccin-mocha", "Catppuccin", true, "#CBA6F7", "#1E1E2E", "#CDD6F4"),
    p("codex-dark", "Codex", true, "#339CFF", "#181818", "#FFFFFF"),
    p("dracula", "Dracula", true, "#FF79C6", "#282A36", "#F8F8F2"),
    p("everforest-dark", "Everforest", true, "#A7C080", "#2D353B", "#D3C6AA"),
    p("github-dark", "GitHub", true, "#58A6FF", "#0D1117", "#C9D1D9"),
    p("gruvbox-dark", "Gruvbox", true, "#FE8019", "#282828", "#EBDBB2"),
    p("linear-dark", "Linear", true, "#8A8EF2", "#16171D", "#F2F3F5"),
    p("lobster-dark", "Lobster", true, "#FF6B81", "#1C1014", "#FFECEF"),
    p("material-darker", "Material", true, "#89DDFF", "#212121", "#EEFFFF"),
    p("matrix-dark", "Matrix", true, "#00E676", "#050A07", "#B8FFD2"),
    p("monokai", "Monokai", true, "#A6E22E", "#272822", "#F8F8F2"),
    p("night-owl", "Night Owl", true, "#82AAFF", "#011627", "#D6DEEB"),
    p("nord", "Nord", true, "#88C0D0", "#2E3440", "#ECEFF4"),
    p("notion-dark", "Notion", true, "#E6E6E6", "#191919", "#F1F1F1"),
    p("oscurange", "Oscurange", true, "#FF8C42", "#0F1115", "#EDEDED"),
    p("one-dark", "One", true, "#61AFEF", "#282C34", "#ABB2BF"),
    p("raycast-dark", "Raycast", true, "#FF6363", "#1A1A1A", "#F5F5F5"),
    p("rose-pine-moon", "Rose Pine", true, "#C4A7E7", "#232136", "#E0DEF4"),
    p("sentry-dark", "Sentry", true, "#A58CFF", "#181225", "#F0EAFF"),
    p("solarized-dark", "Solarized", true, "#2AA198", "#002B36", "#EEE8D5"),
    p("tokyo-night", "Tokyo Night", true, "#7AA2F7", "#1A1B26", "#C0CAF5"),
    p("temple-dark", "Temple", true, "#E8B05A", "#1B1712", "#F3E8D2"),
    p("vercel-dark", "Vercel", true, "#FFFFFF", "#000000", "#F5F5F5"),
    p("vscode-dark-plus", "VS Code Plus", true, "#007ACC", "#1E1E1E", "#D4D4D4"),
    p("xcode-dark", "Xcode", true, "#4AA5F0", "#1F1F24", "#DFDFE0"),
];

impl Preset {
    pub fn scheme(&self) -> Scheme {
        Scheme {
            preset_id: self.id.into(),
            accent: self.accent.into(),
            background: self.background.into(),
            foreground: self.foreground.into(),
            contrast: if self.dark { 58.0 } else { 45.0 },
        }
    }
}

impl Default for ThemeSettings {
    fn default() -> Self {
        Self {
            version: 3,
            preference: Preference::System,
            light: Scheme { preset_id: "custom".into(), accent: "#147A65".into(), background: "#F6F5F0".into(), foreground: "#19221E".into(), contrast: 45.0 },
            dark: Scheme { preset_id: "custom".into(), accent: "#4FBEA0".into(), background: "#111412".into(), foreground: "#EFF3F0".into(), contrast: 58.0 },
            ui_font_size: 14.0,
        }
    }
}

fn valid_hex(value: &str) -> bool {
    value.len() == 7 && value.starts_with('#') && value[1..].chars().all(|c| c.is_ascii_hexdigit())
}

impl ThemeSettings {
    /// Same sanitisation as `sanitizeThemeSettings`.
    pub fn sanitized(mut self) -> Self {
        let defaults = Self::default();
        for (scheme, fallback) in [(&mut self.light, &defaults.light), (&mut self.dark, &defaults.dark)] {
            for (value, default) in [(&mut scheme.accent, &fallback.accent), (&mut scheme.background, &fallback.background), (&mut scheme.foreground, &fallback.foreground)] {
                if valid_hex(value) { *value = value.to_uppercase() } else { *value = default.clone() }
            }
            scheme.contrast = scheme.contrast.clamp(20.0, 100.0);
        }
        self.version = 3;
        self.ui_font_size = self.ui_font_size.clamp(12.0, 18.0);
        self
    }
}

fn hex(value: &str) -> (f32, f32, f32) {
    let v = u32::from_str_radix(value.trim_start_matches('#'), 16).unwrap_or(0);
    (((v >> 16) & 255) as f32, ((v >> 8) & 255) as f32, (v & 255) as f32)
}

fn to_hex(c: (f32, f32, f32)) -> u32 {
    let ch = |v: f32| (v.round().clamp(0.0, 255.0)) as u32;
    (ch(c.0) << 16) | (ch(c.1) << 8) | ch(c.2)
}

/// `mix(first, second, secondAmount)` from theme.ts, returning 0xRRGGBB.
pub fn mix(first: &str, second: &str, amount: f32) -> u32 {
    let (a, b, r) = (hex(first), hex(second), amount.clamp(0.0, 100.0) / 100.0);
    to_hex((a.0 + (b.0 - a.0) * r, a.1 + (b.1 - a.1) * r, a.2 + (b.2 - a.2) * r))
}

fn hex_str(v: u32) -> String { format!("#{v:06X}") }

fn luminance(value: &str) -> f32 {
    let (r, g, b) = hex(value);
    let ch = |v: f32| {
        let n = v / 255.0;
        if n <= 0.03928 { n / 12.92 } else { ((n + 0.055) / 1.055).powf(2.4) }
    };
    0.2126 * ch(r) + 0.7152 * ch(g) + 0.0722 * ch(b)
}

pub fn contrast(first: &str, second: &str) -> f32 {
    let (a, b) = (luminance(first), luminance(second));
    (a.max(b) + 0.05) / (a.min(b) + 0.05)
}

fn readable_on(color: &str) -> &'static str {
    if contrast(color, "#FFFFFF") >= contrast(color, "#0B0F0D") { "#FFFFFF" } else { "#0B0F0D" }
}

pub fn color(v: u32) -> Hsla { rgb(v).into() }
pub fn alpha(v: u32, a: f32) -> Hsla { let mut c: Hsla = rgb(v).into(); c.a = a; c }
fn parse(value: &str) -> u32 { u32::from_str_radix(value.trim_start_matches('#'), 16).unwrap_or(0) }

/// Every CSS custom property the stylesheet uses, resolved to colours.
#[derive(Clone, Debug)]
pub struct Palette {
    pub dark: bool,
    pub ui_size: f32,
    pub ink: Hsla,
    pub muted: Hsla,
    pub paper: Hsla,
    pub panel: Hsla,
    pub line: Hsla,
    pub brand: Hsla,
    pub brand_dark: Hsla,
    pub brand_soft: Hsla,
    pub on_brand: Hsla,
    pub sidebar: Hsla,
    pub sidebar_ink: Hsla,
    pub sidebar_muted: Hsla,
    pub surface_soft: Hsla,
    pub surface_raised: Hsla,
    pub surface_hover: Hsla,
    pub control_border: Hsla,
    pub code_bg: Hsla,
    pub code_ink: Hsla,
    pub danger: Hsla,
    pub danger_soft: Hsla,
    pub danger_line: Hsla,
    pub warning: Hsla,
    pub warning_soft: Hsla,
    pub online: Hsla,
    pub stopped: Hsla,
    pub neutral_dot: Hsla,
    /// Light-theme literal greys (`#eceee9`, `#f1f2ee`…); dark mode maps them to surface-soft.
    pub tint: Hsla,
    /// Skeleton placeholders (`#e8eae5` / surface-raised).
    pub skeleton: Hsla,
    /// Owned-card border (`#9cc8b8` / brand 55% over line).
    pub owned_border: Hsla,
    pub owned_bg: Hsla,
    pub backdrop: Hsla,
}

impl Palette {
    pub fn new(settings: &ThemeSettings, system_dark: bool) -> Self {
        let dark = match settings.preference {
            Preference::System => system_dark,
            Preference::Light => false,
            Preference::Dark => true,
        };
        let s = if dark { &settings.dark } else { &settings.light };
        let (bg, fg, accent) = (s.background.as_str(), s.foreground.as_str(), s.accent.as_str());
        let surface = if dark { 4.0 + s.contrast * 0.055 } else { 2.0 + s.contrast * 0.025 };
        let line = if dark { 9.0 + s.contrast * 0.11 } else { 10.0 + s.contrast * 0.075 };
        let sidebar = if dark { mix(bg, "#000000", 32.0) } else { mix(accent, "#07110E", 66.0) };
        let sidebar_s = hex_str(sidebar);
        let sidebar_ink = readable_on(&sidebar_s);
        let line_c = mix(bg, fg, line);
        Palette {
            dark,
            ui_size: settings.ui_font_size,
            ink: color(parse(fg)),
            muted: color(mix(fg, bg, if dark { 38.0 } else { 43.0 })),
            paper: color(parse(bg)),
            panel: color(mix(bg, if dark { fg } else { "#FFFFFF" }, surface)),
            line: color(line_c),
            brand: color(parse(accent)),
            brand_dark: color(mix(accent, if dark { "#FFFFFF" } else { "#000000" }, 18.0)),
            brand_soft: color(mix(bg, accent, if dark { 20.0 } else { 13.0 })),
            on_brand: color(parse(readable_on(accent))),
            sidebar: color(sidebar),
            sidebar_ink: color(parse(sidebar_ink)),
            sidebar_muted: color(mix(sidebar_ink, &sidebar_s, 34.0)),
            surface_soft: color(mix(bg, fg, if dark { surface + 3.0 } else { surface + 1.5 })),
            surface_raised: color(mix(bg, fg, if dark { surface + 7.0 } else { surface + 4.0 })),
            surface_hover: color(mix(bg, fg, if dark { surface + 5.0 } else { surface + 2.5 })),
            control_border: color(mix(bg, fg, if dark { line + 7.0 } else { line + 8.0 })),
            code_bg: color(mix(bg, "#000000", if dark { 34.0 } else { 82.0 })),
            code_ink: color(if dark { mix(fg, accent, 10.0) } else { 0xD8E8E1 }),
            danger: color(if dark { 0xFF9399 } else { 0x9A3038 }),
            danger_soft: color(if dark { 0x3B1D21 } else { 0xFFF0F1 }),
            danger_line: color(if dark { 0x724046 } else { 0xDFB4B7 }),
            warning: color(if dark { 0xF0C86F } else { 0x755111 }),
            warning_soft: color(if dark { 0x392F19 } else { 0xFFF3D8 }),
            online: color(0x3972BD),
            stopped: color(0xA84A50),
            neutral_dot: color(0x929A96),
            tint: color(if dark { mix(bg, fg, surface + 3.0) } else { 0xEFF0EC }),
            skeleton: color(if dark { mix(bg, fg, surface + 7.0) } else { 0xE8EAE5 }),
            owned_border: color(if dark { mix(&hex_str(line_c), accent, 55.0) } else { 0x9CC8B8 }),
            owned_bg: if dark { color(mix(bg, accent, 8.0)) } else { color(0xFBFEFC) },
            backdrop: alpha(0x13251F, 0.45),
        }
    }
}
