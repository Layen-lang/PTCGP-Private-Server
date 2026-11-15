//! Pictograms of the installed game, used in place of Lucide icons.
//!
//! They are not shipped with the program: the importer cuts them out of the
//! game's UI atlas with the other game images (`ui/icons/` in the active data
//! generation). Until they are imported, and for icons without a game
//! equivalent, the Lucide icon is drawn. The game pictograms are white
//! shapes, so they are wrapped in an SVG and tinted like the Lucide icons.

use std::collections::HashSet;
use std::path::{Path, PathBuf};
use std::sync::{Mutex, OnceLock};
use std::time::{Duration, Instant};

#[derive(Clone, Copy)]
enum Turn {
    None,
    /// Mirrored left to right.
    Flip,
    /// Rotated clockwise by a quarter turn.
    Right,
    /// Rotated counter-clockwise by a quarter turn.
    Left,
}

struct Glyph {
    /// Name used by the interface: a Lucide icon name, or an alias for
    /// places where the Lucide name has another meaning elsewhere.
    name: &'static str,
    /// Lucide icon drawn when the game pictogram is not available.
    fallback: &'static str,
    sprite: &'static str,
    turn: Turn,
    /// Size relative to the sprite's 128 px canvas, to match the visual
    /// size of the Lucide icon it replaces. Sprites keep at least 8 px of
    /// margin on that canvas, so 1.12 does not crop them.
    scale: f32,
}

const fn glyph(name: &'static str, fallback: &'static str, sprite: &'static str, turn: Turn, scale: f32) -> Glyph {
    Glyph { name, fallback, sprite, turn, scale }
}

/// Only icons with the same meaning in the game are replaced.
const GLYPHS: &[Glyph] = &[
    glyph("search", "search", "cmn_pict_search", Turn::None, 1.12),
    glyph("trash", "trash", "cmn_pict_trash", Turn::None, 1.12),
    glyph("settings", "settings", "cmn_pict_setting", Turn::None, 1.12),
    glyph("copy", "copy", "cmn_pict_copy", Turn::None, 1.12),
    glyph("plus", "plus", "cmn_pict_plus", Turn::None, 0.95),
    glyph("minus", "minus", "cmn_pict_minus", Turn::None, 0.95),
    glyph("check", "check", "cmn_pict_check", Turn::None, 1.0),
    glyph("circle-check", "circle-check", "cmn_pict_check_circle", Turn::None, 1.12),
    glyph("x", "x", "cmn_pict_close", Turn::None, 0.95),
    glyph("download", "download", "cmn_pict_download", Turn::None, 1.12),
    glyph("arrow-down-to-line", "arrow-down-to-line", "cmn_pict_download", Turn::None, 1.12),
    glyph("chevron-right", "chevron-right", "cmn_pict_arrow", Turn::None, 0.85),
    glyph("chevron-left", "chevron-left", "cmn_pict_arrow", Turn::Flip, 0.85),
    glyph("chevron-down", "chevron-down", "cmn_pict_arrow", Turn::Right, 0.85),
    glyph("chevron-up", "chevron-up", "cmn_pict_arrow", Turn::Left, 0.85),
    glyph("user-round", "user-round", "cmn_pict_player", Turn::None, 1.12),
    glyph("palette", "palette", "cmn_pict_color", Turn::None, 1.12),
    glyph("circle-play", "circle-play", "cmn_pict_movie", Turn::None, 1.12),
    glyph("rotate-ccw", "rotate-ccw", "cmn_pict_reset", Turn::None, 1.12),
    glyph("rotate-cw", "rotate-cw", "cmn_pict_reset", Turn::Flip, 1.12),
    glyph("refresh-cw", "refresh-cw", "cmn_pict_reset", Turn::Flip, 1.12),
    glyph("circle-alert", "circle-alert", "cmn_pict_attention", Turn::None, 1.12),
    glyph("lock-keyhole", "lock-keyhole", "cmn_pict_lock", Turn::None, 1.12),
    glyph("file-text", "file-text", "cmn_pict_battlelog", Turn::None, 1.12),
    glyph("wallet-cards", "wallet-cards", "cmn_pict_card", Turn::None, 1.12),
    glyph("award", "award", "cmn_pict_emblem", Turn::None, 1.12),
    glyph("globe", "globe", "cmn_pict_global", Turn::None, 1.12),
    glyph("gamepad-2", "gamepad-2", "cmn_pict_controller", Turn::None, 1.12),
    glyph("accounts", "boxes", "cmn_pict_player", Turn::None, 1.12),
    glyph("pack-studio", "circle-dot", "cmn_pict_pack", Turn::None, 1.12),
    glyph("battle", "shield", "cmn_pict_battle_off", Turn::None, 1.12),
    glyph("showcase", "image", "cmn_pict_social_collection", Turn::None, 1.12),
    glyph("items", "sparkles", "cmn_pict_item", Turn::None, 1.12),
];

fn find(name: &str) -> Option<&'static Glyph> { GLYPHS.iter().find(|g| g.name == name) }

/// How often the active data generation is looked up again, so pictograms
/// appear once a preparation has imported them.
const REFRESH: Duration = Duration::from_secs(5);

struct State {
    root: PathBuf,
    checked: Option<Instant>,
    directory: Option<PathBuf>,
    sprites: HashSet<String>,
}

static STATE: OnceLock<Mutex<State>> = OnceLock::new();

/// Sets the user data folder holding `data/generations`.
pub fn init(root: PathBuf) {
    let _ = STATE.set(Mutex::new(State { root, checked: None, directory: None, sprites: HashSet::new() }));
}

/// Folder of the imported pictograms in the active generation.
fn icon_directory(root: &Path) -> Option<PathBuf> {
    let generations = root.join("data").join("generations");
    let active: serde_json::Value = serde_json::from_slice(&std::fs::read(generations.join("active.json")).ok()?).ok()?;
    let key = active["key"].as_str()?;
    if key.len() != 64 || !key.bytes().all(|b| b.is_ascii_hexdigit()) { return None }
    Some(generations.join(key).join("images").join("images").join("ui").join("icons"))
}

/// The imported sprite file of a glyph, when it is available.
fn sprite_file(glyph: &Glyph) -> Option<PathBuf> {
    let mut state = STATE.get()?.lock().ok()?;
    if state.checked.is_none_or(|at| at.elapsed() >= REFRESH) {
        state.checked = Some(Instant::now());
        state.directory = icon_directory(&state.root);
        state.sprites = state.directory.as_deref().and_then(|d| std::fs::read_dir(d).ok())
            .map(|entries| entries.flatten().filter_map(|e| e.file_name().to_str()?.strip_suffix(".png").map(str::to_owned)).collect())
            .unwrap_or_default();
    }
    if !state.sprites.contains(glyph.sprite) { return None }
    Some(state.directory.as_ref()?.join(format!("{}.png", glyph.sprite)))
}

/// Asset path of the icon `name`: the game pictogram when it is imported,
/// otherwise the Lucide icon.
pub fn asset_path(name: &str) -> String {
    match find(name) {
        Some(glyph) if sprite_file(glyph).is_some() => format!("game-icons/{name}.svg"),
        Some(glyph) => format!("icons/{}.svg", glyph.fallback),
        None => format!("icons/{name}.svg"),
    }
}

/// Loads `game-icons/<name>.svg`. Falls back to the Lucide icon when the
/// sprite has disappeared since the path was chosen.
pub fn load(path: &str) -> Option<Vec<u8>> {
    let glyph = find(path.strip_prefix("game-icons/")?.strip_suffix(".svg")?)?;
    match sprite_file(glyph).and_then(|file| std::fs::read(file).ok()).filter(|png| png.starts_with(b"\x89PNG\r\n\x1a\n")) {
        Some(png) => Some(wrap(&png, glyph).into_bytes()),
        None => {
            let fallback = format!("icons/{}.svg", glyph.fallback);
            gpui_kit::AssetSource::load(&gpui_kit::assets::AllAssets, &fallback).ok().flatten().map(|b| b.into_owned())
        }
    }
}

/// An SVG drawing the PNG; only its alpha is used when the icon is tinted.
fn wrap(png: &[u8], glyph: &Glyph) -> String {
    let rotate = match glyph.turn { Turn::Right => " rotate(90)", Turn::Left => " rotate(-90)", _ => "" };
    let flip = if matches!(glyph.turn, Turn::Flip) { -1.0 } else { 1.0 };
    let s = glyph.scale;
    format!(
        r#"<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" viewBox="0 0 128 128" width="128" height="128"><g transform="translate(64 64){rotate} scale({} {s}) translate(-64 -64)"><image width="128" height="128" preserveAspectRatio="xMidYMid meet" xlink:href="data:image/png;base64,{}"/></g></svg>"#,
        flip * s,
        base64(png)
    )
}

fn base64(data: &[u8]) -> String {
    const TABLE: &[u8; 64] = b"ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
    let mut out = String::with_capacity(data.len().div_ceil(3) * 4);
    for chunk in data.chunks(3) {
        let n = (chunk[0] as u32) << 16 | (*chunk.get(1).unwrap_or(&0) as u32) << 8 | *chunk.get(2).unwrap_or(&0) as u32;
        for i in 0..4 {
            out.push(if i <= chunk.len() { TABLE[(n >> (18 - 6 * i) & 63) as usize] as char } else { '=' });
        }
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn base64_matches_the_standard_alphabet_and_padding() {
        assert_eq!(base64(b""), "");
        assert_eq!(base64(b"f"), "Zg==");
        assert_eq!(base64(b"fo"), "Zm8=");
        assert_eq!(base64(b"foo"), "Zm9v");
        assert_eq!(base64(b"\x89PNG\xff"), "iVBOR/8=");
    }

    #[test]
    fn glyph_names_are_unique_and_fallbacks_exist() {
        let mut seen = HashSet::new();
        for glyph in GLYPHS {
            assert!(seen.insert(glyph.name), "duplicate glyph {}", glyph.name);
            let lucide = format!("icons/{}.svg", glyph.fallback);
            assert!(gpui_kit::AssetSource::load(&gpui_kit::assets::AllAssets, &lucide).unwrap().is_some(), "missing {lucide}");
        }
    }

    #[test]
    fn missing_generation_keeps_lucide_icons() {
        init(std::env::temp_dir().join("ptcgp-game-icons-none"));
        assert_eq!(asset_path("search"), "icons/search.svg");
        assert_eq!(asset_path("accounts"), "icons/boxes.svg");
        assert_eq!(asset_path("server"), "icons/server.svg");
    }
}
