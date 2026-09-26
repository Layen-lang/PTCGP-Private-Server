use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    io::Read,
    path::{Path, PathBuf},
};

use anyhow::{bail, Context, Result};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use walkdir::WalkDir;

const LOCALE_FILES: [&str; 6] = [
    "Master.bytes",
    "Master_Attribute.bytes",
    "System.bytes",
    "System_Attribute.bytes",
    "UI.bytes",
    "UI_Attribute.bytes",
];

#[derive(Debug, Default, Serialize, Deserialize)]
struct LocaleSnapshot {
    #[serde(rename = "schemaVersion")]
    schema_version: u32,
    files: BTreeMap<String, BTreeMap<String, BTreeMap<String, String>>>,
}

pub(super) struct LocaleState {
    pub dictionaries: BTreeMap<String, BTreeMap<String, String>>,
    pub refreshed_files: usize,
    pub reused_files: usize,
}

pub(super) struct BootstrapSummary {
    pub releases: usize,
    pub files: usize,
}

/// Seeds a missing transactional snapshot from exact `.bytes` files retained
/// by earlier successful normalized releases. Releases are applied oldest to
/// newest so each locale file has its latest observed complete contents.
pub(super) fn bootstrap_from_releases(
    release_root: &Path,
    cache_path: &Path,
) -> Result<Option<BootstrapSummary>> {
    if cache_path.is_file() || !release_root.is_dir() {
        return Ok(None);
    }
    let mut releases = Vec::new();
    for entry in fs::read_dir(release_root)? {
        let entry = entry?;
        if !entry.file_type()?.is_dir() {
            continue;
        }
        let manifest = entry.path().join("manifests/run.json");
        let Ok(value) = serde_json::from_slice::<Value>(&fs::read(&manifest).unwrap_or_default())
        else {
            continue;
        };
        let Some(completed_at) = value.get("completed_at").and_then(Value::as_str) else {
            continue;
        };
        let stages = value
            .get("stages")
            .and_then(Value::as_array)
            .cloned()
            .unwrap_or_default();
        let failed = stages
            .iter()
            .any(|stage| stage.get("status").and_then(Value::as_str) == Some("failed"));
        let normalized = stages.iter().any(|stage| {
            stage.get("name").and_then(Value::as_str) == Some("normalization")
                && stage.get("status").and_then(Value::as_str) == Some("success")
        });
        if !failed && normalized {
            releases.push((completed_at.to_owned(), entry.path()));
        }
    }
    releases.sort_by(|left, right| left.0.cmp(&right.0));

    let mut snapshot = LocaleSnapshot::default();
    let mut contributing_releases = 0;
    for (_, release) in releases {
        let other = release.join("datamine/other");
        if !other.is_dir() {
            continue;
        }
        let mut contributed = false;
        for entry in WalkDir::new(&other).follow_links(false) {
            let entry = entry?;
            if !entry.file_type().is_dir() || entry.file_name() != "Locale" {
                continue;
            }
            let current = load_current_files(entry.path())?;
            if !current.is_empty() {
                overlay_snapshot(&mut snapshot.files, current);
                contributed = true;
            }
        }
        if contributed {
            contributing_releases += 1;
        }
    }
    let files = snapshot.files.values().map(BTreeMap::len).sum();
    if files == 0 {
        return Ok(None);
    }
    snapshot.schema_version = 1;
    save_snapshot(cache_path, &snapshot)?;
    Ok(Some(BootstrapSummary {
        releases: contributing_releases,
        files,
    }))
}

/// Reconstructs the complete localization state from the current AssetRipper
/// delta and the last committed per-file snapshot. A present `.bytes` file
/// replaces its prior dictionary; an absent file means "unchanged".
pub(super) fn load(root: &Path, cache_path: &Path) -> Result<LocaleState> {
    let mut snapshot = load_snapshot(cache_path)?;
    let current = load_current_files(root)?;
    let refreshed_files = current.values().map(BTreeMap::len).sum::<usize>();
    overlay_snapshot(&mut snapshot.files, current);
    snapshot.schema_version = 1;
    save_snapshot(cache_path, &snapshot)?;

    let total_files = snapshot.files.values().map(BTreeMap::len).sum::<usize>();
    Ok(LocaleState {
        dictionaries: combine_snapshot(&snapshot.files),
        refreshed_files,
        reused_files: total_files.saturating_sub(refreshed_files),
    })
}

fn load_current_files(
    root: &Path,
) -> Result<BTreeMap<String, BTreeMap<String, BTreeMap<String, String>>>> {
    let mut locales = BTreeMap::new();
    if !root.is_dir() {
        return Ok(locales);
    }
    for (language, dir) in locale_dirs(root)? {
        let mut files = BTreeMap::new();
        for name in LOCALE_FILES {
            let path = dir.join(name);
            if path.is_file() {
                files.insert(
                    name.to_owned(),
                    parse_kvrf(&path)
                        .with_context(|| format!("parse localization file {}", path.display()))?,
                );
            }
        }
        if !files.is_empty() {
            locales.insert(language, files);
        }
    }
    Ok(locales)
}

fn overlay_snapshot(
    snapshot: &mut BTreeMap<String, BTreeMap<String, BTreeMap<String, String>>>,
    current: BTreeMap<String, BTreeMap<String, BTreeMap<String, String>>>,
) {
    for (language, files) in current {
        let destination = snapshot.entry(language).or_default();
        for (name, dictionary) in files {
            destination.insert(name, dictionary);
        }
    }
}

fn combine_snapshot(
    snapshot: &BTreeMap<String, BTreeMap<String, BTreeMap<String, String>>>,
) -> BTreeMap<String, BTreeMap<String, String>> {
    snapshot
        .iter()
        .filter_map(|(language, files)| {
            let mut values = BTreeMap::new();
            for name in LOCALE_FILES {
                let Some(dictionary) = files.get(name) else {
                    continue;
                };
                for (key, value) in dictionary {
                    if values.get(key).is_some_and(|old: &String| !old.is_empty())
                        && value.is_empty()
                    {
                        continue;
                    }
                    values.insert(key.clone(), value.clone());
                }
            }
            (!values.is_empty()).then(|| (language.clone(), values))
        })
        .collect()
}

fn load_snapshot(path: &Path) -> Result<LocaleSnapshot> {
    if !path.is_file() {
        return Ok(LocaleSnapshot::default());
    }
    let snapshot: LocaleSnapshot = serde_json::from_slice(&fs::read(path)?)?;
    if snapshot.schema_version != 1 {
        bail!(
            "unsupported localization snapshot schema {} at {}",
            snapshot.schema_version,
            path.display()
        );
    }
    Ok(snapshot)
}

fn save_snapshot(path: &Path, snapshot: &LocaleSnapshot) -> Result<()> {
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    fs::write(path, serde_json::to_vec_pretty(snapshot)?)?;
    Ok(())
}

fn locale_dirs(root: &Path) -> Result<Vec<(String, PathBuf)>> {
    if LOCALE_FILES.iter().any(|name| root.join(name).is_file()) {
        let language = root
            .file_name()
            .map(|name| name.to_string_lossy().to_string())
            .unwrap_or_else(|| "locale".to_owned());
        return Ok(vec![(language, root.to_owned())]);
    }

    let mut dirs = Vec::new();
    for entry in fs::read_dir(root)? {
        let entry = entry?;
        if entry.file_type()?.is_dir() {
            dirs.push((
                entry.file_name().to_string_lossy().to_string(),
                entry.path(),
            ));
        }
    }
    dirs.sort_by(|left, right| left.0.cmp(&right.0));
    Ok(dirs)
}

pub(super) fn references(tables: &BTreeMap<String, Value>) -> BTreeMap<String, String> {
    let mut references = BTreeMap::new();
    let Some(Value::Array(characters)) = tables.get("Character") else {
        return references;
    };
    for character in characters {
        let Some(character) = character.as_object() else {
            continue;
        };
        let (Some(id), Some(msid)) = (
            character.get("CharacterID").and_then(Value::as_str),
            character.get("DisplayNameMSID").and_then(Value::as_str),
        ) else {
            continue;
        };
        if !id.is_empty() && !msid.is_empty() {
            references.insert(id.to_owned(), msid.to_owned());
        }
    }
    references
}

fn parse_kvrf(path: &Path) -> Result<BTreeMap<String, String>> {
    let data = fs::read(path)?;
    parse_kvrf_bytes(&data)
}
pub(super) fn parse_kvrf_bytes(data: &[u8]) -> Result<BTreeMap<String, String>> {
    if data.get(..4) != Some(b"KVRF") {
        bail!("missing KVRF header");
    }
    let read_u32 = |offset| -> Result<u32> {
        Ok(u32::from_le_bytes(
            data.get(offset..offset + 4)
                .context("truncated KVRF")?
                .try_into()?,
        ))
    };
    let mut values = BTreeMap::new();
    let mut seen = BTreeSet::new();
    for bucket in 0..read_u32(0x14)? as usize {
        let mut offset = read_u32(read_u32(0x20)? as usize + bucket * 4)? as usize;
        while offset != 0 && seen.insert(offset) {
            let next = read_u32(offset)? as usize;
            let compression = *data.get(offset + 0x0a).context("truncated KVRF entry")?;
            let size = read_u32(offset + 0x0c)? as usize;
            let mut body = data
                .get(offset + 16..offset + 16 + size)
                .context("truncated KVRF body")?
                .to_vec();
            if compression == 1 {
                if body.len() < 4 {
                    bail!("truncated compressed KVRF body");
                }
                let mut plain = Vec::new();
                flate2::read::DeflateDecoder::new(&body[4..]).read_to_end(&mut plain)?;
                body = plain;
            } else if compression != 0 {
                offset = next;
                continue;
            }
            if body.len() >= 2 {
                let key_len = u16::from_le_bytes(body[..2].try_into()?) as usize;
                if body.len() >= 2 + key_len {
                    let key = String::from_utf16_lossy(&u16s(&body[2..2 + key_len]));
                    if !key.is_empty() {
                        let value = String::from_utf16_lossy(&u16s(&body[2 + key_len..]));
                        if values.get(&key).is_some_and(|old: &String| !old.is_empty())
                            && value.is_empty()
                        {
                            offset = next;
                            continue;
                        }
                        values.insert(key, value);
                    }
                }
            }
            offset = next;
        }
    }
    Ok(values)
}

pub(super) fn memory_dictionary(files: &[(String, Vec<u8>)]) -> Result<BTreeMap<String, String>> {
    let mut dictionaries = BTreeMap::new();
    for (name, data) in files {
        dictionaries.insert(format!("{name}.bytes"), parse_kvrf_bytes(data)?);
    }
    let mut values = BTreeMap::new();
    for name in LOCALE_FILES {
        let dictionary = dictionaries
            .remove(name)
            .with_context(|| format!("missing locale {name}"))?;
        for (key, value) in dictionary {
            if values.get(&key).is_some_and(|old: &String| !old.is_empty()) && value.is_empty() {
                continue;
            }
            values.insert(key, value);
        }
    }
    Ok(values)
}

pub(super) fn resolve_value(
    value: &Value,
    locale: &BTreeMap<String, String>,
    references: &BTreeMap<String, String>,
    params: Option<Vec<String>>,
) -> Value {
    match value {
        Value::Array(values) => Value::Array(
            values
                .iter()
                .map(|value| resolve_value(value, locale, references, params.clone()))
                .collect(),
        ),
        Value::Object(values) => {
            let local_params = values
                .iter()
                .find_map(|(key, value)| {
                    key.ends_with("Parameters")
                        .then(|| value.as_array())
                        .flatten()
                        .map(|items| items.iter().map(parameter_string).collect())
                })
                .or_else(|| values.get("Amount").map(|value| vec![value.to_string()]))
                .or(params);
            Value::Object(
                values
                    .iter()
                    .map(|(key, value)| {
                        (
                            key.clone(),
                            resolve_value(value, locale, references, local_params.clone()),
                        )
                    })
                    .collect(),
            )
        }
        Value::String(value) => {
            let translated = locale
                .get(value)
                .filter(|text| !text.is_empty())
                .map(|text| format!("{value}  ({text})"))
                .unwrap_or_else(|| value.clone());
            Value::String(resolve_tags(
                &translated,
                locale,
                references,
                params.as_deref().unwrap_or_default(),
            ))
        }
        _ => value.clone(),
    }
}

pub(super) fn parameter_string(value: &Value) -> String {
    value
        .as_str()
        .map(str::to_owned)
        .unwrap_or_else(|| value.to_string())
}

fn resolve_tags(
    value: &str,
    locale: &BTreeMap<String, String>,
    references: &BTreeMap<String, String>,
    params: &[String],
) -> String {
    let mut current = value.to_owned();
    for _ in 0..3 {
        let next = resolve_tags_once(&current, locale, references, params);
        if next == current {
            break;
        }
        current = next;
    }
    current
}

fn resolve_tags_once(
    value: &str,
    locale: &BTreeMap<String, String>,
    references: &BTreeMap<String, String>,
    params: &[String],
) -> String {
    let mut output = String::new();
    let mut remaining = value;
    while let Some(start) = remaining.find('[') {
        output.push_str(&remaining[..start]);
        let after = &remaining[start + 1..];
        let Some(end) = after.find(']') else {
            output.push_str(&remaining[start..]);
            break;
        };
        let tag = &after[..end];
        let replacement = if tag.starts_with('/') || tag.starts_with("Ctrl:") {
            Some(String::new())
        } else if tag.starts_with("Num:") || tag.starts_with("Text:") || tag.starts_with("Img:") {
            tag_attr(tag, "id")
                .and_then(|value| value.parse::<usize>().ok())
                .and_then(|index| {
                    params.get(index).map(|value| {
                        lookup_tag_value(value, locale, references).unwrap_or_else(|| value.clone())
                    })
                })
                .or_else(|| {
                    tag_attr(tag, "v").and_then(|value| lookup_tag_value(value, locale, references))
                })
        } else if tag.starts_with("Mst:") {
            tag_attr(tag, "v").and_then(|value| lookup_tag_value(value, locale, references))
        } else {
            None
        };
        output.push_str(
            replacement
                .as_deref()
                .unwrap_or(&remaining[start..start + end + 2]),
        );
        remaining = &after[end + 1..];
    }
    output.push_str(remaining);
    output.replace("[[", "").replace("]]", "")
}

fn lookup_tag_value(
    value: &str,
    locale: &BTreeMap<String, String>,
    references: &BTreeMap<String, String>,
) -> Option<String> {
    locale
        .get(value)
        .filter(|text| !text.is_empty())
        .cloned()
        .or_else(|| {
            references
                .get(value)
                .and_then(|msid| locale.get(msid))
                .filter(|text| !text.is_empty())
                .cloned()
        })
}

fn tag_attr<'a>(tag: &'a str, name: &str) -> Option<&'a str> {
    tag.split(&format!("{name}=\""))
        .nth(1)
        .and_then(|value| value.split('"').next())
}

fn u16s(bytes: &[u8]) -> Vec<u16> {
    bytes
        .chunks_exact(2)
        .map(|pair| u16::from_le_bytes([pair[0], pair[1]]))
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn dictionary(values: &[(&str, &str)]) -> BTreeMap<String, String> {
        values
            .iter()
            .map(|(key, value)| ((*key).to_owned(), (*value).to_owned()))
            .collect()
    }

    fn kvrf(key: &str, value: &str) -> Vec<u8> {
        let key = key
            .encode_utf16()
            .flat_map(u16::to_le_bytes)
            .collect::<Vec<_>>();
        let value = value
            .encode_utf16()
            .flat_map(u16::to_le_bytes)
            .collect::<Vec<_>>();
        let mut body = Vec::with_capacity(2 + key.len() + value.len());
        body.extend_from_slice(&(key.len() as u16).to_le_bytes());
        body.extend_from_slice(&key);
        body.extend_from_slice(&value);
        let mut data = vec![0_u8; 0x38];
        data[..4].copy_from_slice(b"KVRF");
        data[0x14..0x18].copy_from_slice(&1_u32.to_le_bytes());
        data[0x20..0x24].copy_from_slice(&0x24_u32.to_le_bytes());
        data[0x24..0x28].copy_from_slice(&0x28_u32.to_le_bytes());
        data[0x34..0x38].copy_from_slice(&(body.len() as u32).to_le_bytes());
        data.extend_from_slice(&body);
        data
    }

    fn historical_release(root: &Path, name: &str, completed_at: &str) -> PathBuf {
        let release = root.join(name);
        crate::support::json::write_json(
            &release.join("manifests/run.json"),
            &serde_json::json!({
                "completed_at": completed_at,
                "stages": [{"name": "normalization", "status": "success"}]
            }),
        )
        .unwrap();
        release.join("datamine/other/Assets/Lettuce/_Data/Common/Locale/fr_FR")
    }

    #[test]
    fn an_incremental_file_replaces_only_its_own_previous_dictionary() {
        let mut snapshot = BTreeMap::from([(
            "fr_FR".to_owned(),
            BTreeMap::from([
                (
                    "System.bytes".to_owned(),
                    dictionary(&[("system_old", "Système")]),
                ),
                (
                    "UI.bytes".to_owned(),
                    dictionary(&[("ui_removed", "Ancien")]),
                ),
            ]),
        )]);
        let current = BTreeMap::from([(
            "fr_FR".to_owned(),
            BTreeMap::from([("UI.bytes".to_owned(), dictionary(&[("ui_new", "Nouveau")]))]),
        )]);

        overlay_snapshot(&mut snapshot, current);
        let combined = combine_snapshot(&snapshot);

        assert_eq!(combined["fr_FR"]["system_old"], "Système");
        assert_eq!(combined["fr_FR"]["ui_new"], "Nouveau");
        assert!(!combined["fr_FR"].contains_key("ui_removed"));
    }

    #[test]
    fn a_run_without_locale_bytes_reuses_the_committed_snapshot() {
        let temp = tempfile::tempdir().unwrap();
        let cache = temp.path().join("locale-snapshot.json");
        save_snapshot(
            &cache,
            &LocaleSnapshot {
                schema_version: 1,
                files: BTreeMap::from([(
                    "fr_FR".to_owned(),
                    BTreeMap::from([(
                        "System.bytes".to_owned(),
                        dictionary(&[("msid_name", "Pikachu")]),
                    )]),
                )]),
            },
        )
        .unwrap();

        let state = load(&temp.path().join("missing-current-delta"), &cache).unwrap();

        assert_eq!(state.refreshed_files, 0);
        assert_eq!(state.reused_files, 1);
        assert_eq!(state.dictionaries["fr_FR"]["msid_name"], "Pikachu");
    }

    #[test]
    fn bootstraps_from_historical_bytes_without_running_assetripper_again() {
        let temp = tempfile::tempdir().unwrap();
        let release_root = temp.path().join("release");
        let old = historical_release(&release_root, "old", "2026-01-01T00:00:00Z");
        fs::create_dir_all(&old).unwrap();
        fs::write(old.join("System.bytes"), kvrf("system", "Système")).unwrap();
        fs::write(old.join("UI.bytes"), kvrf("old_ui", "Ancien")).unwrap();
        let new = historical_release(&release_root, "new", "2026-01-02T00:00:00Z");
        fs::create_dir_all(&new).unwrap();
        fs::write(new.join("UI.bytes"), kvrf("new_ui", "Nouveau")).unwrap();
        let cache = temp.path().join("cache/locale-snapshot.json");

        let summary = bootstrap_from_releases(&release_root, &cache)
            .unwrap()
            .unwrap();
        let state = load(&temp.path().join("empty-current-delta"), &cache).unwrap();

        assert_eq!(summary.releases, 2);
        assert_eq!(summary.files, 2);
        assert_eq!(state.dictionaries["fr_FR"]["system"], "Système");
        assert_eq!(state.dictionaries["fr_FR"]["new_ui"], "Nouveau");
        assert!(!state.dictionaries["fr_FR"].contains_key("old_ui"));
    }
}
