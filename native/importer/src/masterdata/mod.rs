use std::{
    collections::BTreeMap,
    fs,
    path::{Path, PathBuf},
};

use anyhow::{bail, Context, Result};
use serde::Serialize;
use serde_json::{Map, Number, Value};
use walkdir::WalkDir;

mod locales;

pub struct LocaleBootstrapSummary {
    pub releases: usize,
    pub files: usize,
}

pub fn bootstrap_locale_snapshot(
    release_root: &Path,
    cache_path: &Path,
) -> Result<Option<LocaleBootstrapSummary>> {
    Ok(
        locales::bootstrap_from_releases(release_root, cache_path)?.map(|summary| {
            LocaleBootstrapSummary {
                releases: summary.releases,
                files: summary.files,
            }
        }),
    )
}

#[derive(Debug, Clone, Serialize)]
pub struct MasterDataSummary {
    pub source: PathBuf,
    pub tables: usize,
    pub languages: Vec<String>,
    pub skipped_tables: Vec<String>,
    pub locale_files_refreshed: usize,
    pub locale_files_reused: usize,
}

/// Searches decrypted `aladin` blobs for the MasterMemory MessagePack container,
/// then exports every independently decodable table as UTF-8 JSON.
pub fn export(
    aladin_blobs: &Path,
    locale_root: &Path,
    locale_cache: &Path,
    destination: &Path,
) -> Result<Option<MasterDataSummary>> {
    if !aladin_blobs.is_dir() {
        return Ok(None);
    }
    for item in WalkDir::new(aladin_blobs).sort_by_file_name() {
        let item = item?;
        if !item.file_type().is_file() {
            continue;
        }
        let raw = fs::read(item.path())?;
        let Ok(master) = decode_master(&raw) else {
            continue;
        };
        if master.tables.is_empty() {
            continue;
        }
        fs::create_dir_all(destination)?;
        let locale_state = locales::load(locale_root, locale_cache)?;
        let references = locales::references(&master.tables);
        let raw_tables = master.tables.len();
        for (language, dictionary) in &locale_state.dictionaries {
            crate::support::json::write_json(
                &destination
                    .parent()
                    .context("master-data parent")?
                    .join("locales")
                    .join(format!("{language}.json")),
                dictionary,
            )?;
        }
        let mut skipped_tables = Vec::new();
        let mut tables = 0;
        for (name, value) in master.tables {
            let clean = safe_filename(&name);
            write_table(
                &destination.join("raw").join(format!("{clean}.json")),
                &value,
            )?;
            if locale_state.dictionaries.is_empty() {
                write_table(&destination.join(format!("{clean}.json")), &value)?;
                tables += 1;
                continue;
            }
            for (language, values) in &locale_state.dictionaries {
                let resolved = locales::resolve_value(&value, values, &references, None);
                match write_table(
                    &destination.join(language).join(format!("{clean}.json")),
                    &resolved,
                ) {
                    Ok(()) => tables += 1,
                    Err(error) => skipped_tables.push(format!("{language}/{name}: {error}")),
                }
            }
        }
        crate::support::json::write_json(
            &destination.join("index.json"),
            &serde_json::json!({
                "schemaVersion": 3,
                "rawTables": raw_tables,
                "source": item.path(),
                "tables": tables,
                "languages": locale_state.dictionaries.keys().collect::<Vec<_>>(),
                "localeFilesRefreshed": locale_state.refreshed_files,
                "localeFilesReused": locale_state.reused_files,
                "skipped_tables": skipped_tables
            }),
        )?;
        return Ok(Some(MasterDataSummary {
            source: item.into_path(),
            tables,
            languages: locale_state.dictionaries.into_keys().collect(),
            skipped_tables,
            locale_files_refreshed: locale_state.refreshed_files,
            locale_files_reused: locale_state.reused_files,
        }));
    }
    Ok(None)
}

fn write_table(path: &Path, value: &Value) -> Result<()> {
    let text = serde_json::to_string_pretty(value)? + "\n";
    if let Some(parent) = path.parent() {
        fs::create_dir_all(parent)?;
    }
    fs::write(path, text)?;
    Ok(())
}

struct Master {
    tables: BTreeMap<String, Value>,
}

fn decode_master(raw: &[u8]) -> Result<Master> {
    let mut parser = Parser::new(raw);
    let header = parser.value()?;
    let body_start = parser.pos;
    let object = header
        .as_object()
        .context("MasterMemory header is not a map")?;
    let mut tables = BTreeMap::new();
    for (name, range) in object {
        let values = range
            .as_array()
            .filter(|v| v.len() == 2)
            .context("invalid MasterMemory table range")?;
        let offset = values[0].as_u64().context("invalid table offset")? as usize;
        let length = values[1].as_u64().context("invalid table length")? as usize;
        let end = offset.checked_add(length).context("table range overflow")?;
        let body = raw
            .get(body_start + offset..body_start + end)
            .context("table range exceeds body")?;
        tables.insert(name.clone(), decode_chunk(body)?);
    }
    Ok(Master { tables })
}

fn decode_chunk(chunk: &[u8]) -> Result<Value> {
    let (type_id, payload) = ext_payload(chunk)?;
    if type_id == Some(99) {
        let mut size_parser = Parser::new(payload);
        let size = size_parser
            .value()?
            .as_u64()
            .context("LZ4 payload size is not an integer")? as usize;
        let plain = lz4_block(&payload[size_parser.pos..], size)?;
        return parse_complete(&plain);
    }
    parse_complete(chunk)
}

fn parse_complete(data: &[u8]) -> Result<Value> {
    let mut parser = Parser::new(data);
    let value = parser.value()?;
    if parser.pos != data.len() {
        bail!(
            "MessagePack value left {} trailing bytes",
            data.len() - parser.pos
        );
    }
    Ok(value)
}

fn ext_payload(data: &[u8]) -> Result<(Option<i8>, &[u8])> {
    let Some(&marker) = data.first() else {
        bail!("empty chunk");
    };
    let (len, kind_offset, body_offset) = match marker {
        0xc7 => (*data.get(1).context("short ext8")? as usize, 2, 3),
        0xc8 => (
            u16::from_be_bytes(data.get(1..3).context("short ext16")?.try_into()?) as usize,
            3,
            4,
        ),
        0xc9 => (
            u32::from_be_bytes(data.get(1..5).context("short ext32")?.try_into()?) as usize,
            5,
            6,
        ),
        _ => return Ok((None, data)),
    };
    let kind = *data.get(kind_offset).context("missing ext type")? as i8;
    let payload = data
        .get(body_offset..body_offset + len)
        .context("short ext payload")?;
    if body_offset + len != data.len() {
        bail!(
            "ext payload left {} trailing bytes",
            data.len() - body_offset - len
        );
    }
    Ok((Some(kind), payload))
}

fn lz4_block(input: &[u8], expected: usize) -> Result<Vec<u8>> {
    let mut source = 0;
    let mut output = Vec::with_capacity(expected);
    while source < input.len() {
        let token = input[source];
        source += 1;
        let literals = extended_length((token >> 4) as usize, input, &mut source)?;
        let bytes = input
            .get(source..source + literals)
            .context("LZ4 literals exceed input")?;
        output.extend_from_slice(bytes);
        source += literals;
        if source == input.len() {
            break;
        }
        let offset = u16::from_le_bytes(
            input
                .get(source..source + 2)
                .context("LZ4 missing offset")?
                .try_into()?,
        ) as usize;
        source += 2;
        if offset == 0 || offset > output.len() {
            bail!("invalid LZ4 match offset {offset}");
        }
        let length = extended_length((token & 0x0f) as usize, input, &mut source)? + 4;
        for _ in 0..length {
            let value = output[output.len() - offset];
            output.push(value);
        }
    }
    if output.len() != expected {
        bail!(
            "LZ4 length mismatch: expected {expected}, got {}",
            output.len()
        );
    }
    Ok(output)
}
fn extended_length(mut length: usize, data: &[u8], pos: &mut usize) -> Result<usize> {
    if length != 15 {
        return Ok(length);
    }
    loop {
        let byte = *data.get(*pos).context("truncated LZ4 length")?;
        *pos += 1;
        length += byte as usize;
        if byte != 255 {
            return Ok(length);
        }
    }
}

struct Parser<'a> {
    data: &'a [u8],
    pos: usize,
}
impl<'a> Parser<'a> {
    fn new(data: &'a [u8]) -> Self {
        Self { data, pos: 0 }
    }
    fn take(&mut self, count: usize) -> Result<&'a [u8]> {
        let value = self
            .data
            .get(self.pos..self.pos + count)
            .context("truncated MessagePack")?;
        self.pos += count;
        Ok(value)
    }
    fn byte(&mut self) -> Result<u8> {
        Ok(self.take(1)?[0])
    }
    fn int(&mut self, count: usize, signed: bool) -> Result<Value> {
        let bytes = self.take(count)?;
        let value = match (count, signed) {
            (1, true) => i8::from_be_bytes(bytes.try_into()?) as i64,
            (2, true) => i16::from_be_bytes(bytes.try_into()?) as i64,
            (4, true) => i32::from_be_bytes(bytes.try_into()?) as i64,
            (8, true) => i64::from_be_bytes(bytes.try_into()?),
            (1, false) => u8::from_be_bytes(bytes.try_into()?) as u64 as i64,
            (2, false) => u16::from_be_bytes(bytes.try_into()?) as u64 as i64,
            (4, false) => u32::from_be_bytes(bytes.try_into()?) as u64 as i64,
            (8, false) => {
                let value = u64::from_be_bytes(bytes.try_into()?);
                return Ok(Value::Number(Number::from(value)));
            }
            _ => unreachable!(),
        };
        Ok(Value::Number(Number::from(value)))
    }
    fn string(&mut self, length: usize) -> Result<String> {
        Ok(std::str::from_utf8(self.take(length)?)
            .context("invalid UTF-8 MessagePack string")?
            .to_owned())
    }
    fn array(&mut self, length: usize) -> Result<Value> {
        let mut values = Vec::with_capacity(length);
        for _ in 0..length {
            values.push(self.value()?);
        }
        Ok(Value::Array(values))
    }
    fn map(&mut self, length: usize) -> Result<Value> {
        let mut values = Map::new();
        for _ in 0..length {
            let key = match self.value()? {
                Value::String(value) => value,
                other => other.to_string(),
            };
            let value = self.value()?;
            if values.insert(key.clone(), value).is_some() {
                bail!("duplicate MessagePack map key {key}");
            }
        }
        Ok(Value::Object(values))
    }
    fn binary(&mut self, length: usize) -> Result<Value> {
        Ok(serde_json::json!({ "$bytes": hex(self.take(length)?) }))
    }
    fn ext(&mut self, length: usize) -> Result<Value> {
        let type_id = self.byte()? as i8;
        Ok(serde_json::json!({ "$ext": type_id, "$data": hex(self.take(length)?) }))
    }
    fn value(&mut self) -> Result<Value> {
        let marker = self.byte()?;
        match marker {
            0x00..=0x7f => Ok(Value::Number(Number::from(marker))),
            0x80..=0x8f => self.map((marker & 0x0f) as usize),
            0x90..=0x9f => self.array((marker & 0x0f) as usize),
            0xa0..=0xbf => Ok(Value::String(self.string((marker & 0x1f) as usize)?)),
            0xc0 => Ok(Value::Null),
            0xc2 => Ok(Value::Bool(false)),
            0xc3 => Ok(Value::Bool(true)),
            0xc4 => {
                let n = self.byte()? as usize;
                self.binary(n)
            }
            0xc5 => {
                let n = u16::from_be_bytes(self.take(2)?.try_into()?) as usize;
                self.binary(n)
            }
            0xc6 => {
                let n = u32::from_be_bytes(self.take(4)?.try_into()?) as usize;
                self.binary(n)
            }
            0xc7 => {
                let n = self.byte()? as usize;
                self.ext(n)
            }
            0xc8 => {
                let n = u16::from_be_bytes(self.take(2)?.try_into()?) as usize;
                self.ext(n)
            }
            0xc9 => {
                let n = u32::from_be_bytes(self.take(4)?.try_into()?) as usize;
                self.ext(n)
            }
            0xca => {
                let n = f32::from_be_bytes(self.take(4)?.try_into()?);
                Ok(Number::from_f64(n as f64)
                    .map(Value::Number)
                    .unwrap_or(Value::Null))
            }
            0xcb => {
                let n = f64::from_be_bytes(self.take(8)?.try_into()?);
                Ok(Number::from_f64(n)
                    .map(Value::Number)
                    .unwrap_or(Value::Null))
            }
            0xcc => self.int(1, false),
            0xcd => self.int(2, false),
            0xce => self.int(4, false),
            0xcf => self.int(8, false),
            0xd0 => self.int(1, true),
            0xd1 => self.int(2, true),
            0xd2 => self.int(4, true),
            0xd3 => self.int(8, true),
            0xd4 => self.ext(1),
            0xd5 => self.ext(2),
            0xd6 => self.ext(4),
            0xd7 => self.ext(8),
            0xd8 => self.ext(16),
            0xd9 => {
                let n = self.byte()? as usize;
                Ok(Value::String(self.string(n)?))
            }
            0xda => {
                let n = u16::from_be_bytes(self.take(2)?.try_into()?) as usize;
                Ok(Value::String(self.string(n)?))
            }
            0xdb => {
                let n = u32::from_be_bytes(self.take(4)?.try_into()?) as usize;
                Ok(Value::String(self.string(n)?))
            }
            0xdc => {
                let n = u16::from_be_bytes(self.take(2)?.try_into()?) as usize;
                self.array(n)
            }
            0xdd => {
                let n = u32::from_be_bytes(self.take(4)?.try_into()?) as usize;
                self.array(n)
            }
            0xde => {
                let n = u16::from_be_bytes(self.take(2)?.try_into()?) as usize;
                self.map(n)
            }
            0xdf => {
                let n = u32::from_be_bytes(self.take(4)?.try_into()?) as usize;
                self.map(n)
            }
            0xe0..=0xff => Ok(Value::Number(Number::from((marker as i8) as i64))),
            _ => bail!("unsupported MessagePack marker 0x{marker:02x}"),
        }
    }
}

fn hex(data: &[u8]) -> String {
    data.iter().map(|v| format!("{v:02x}")).collect()
}
fn safe_filename(name: &str) -> String {
    name.chars()
        .map(|value| {
            if value.is_ascii_alphanumeric() || matches!(value, '-' | '_' | '.') {
                value
            } else {
                '_'
            }
        })
        .collect()
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn export_preserves_structural_strings_separately_from_localization() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path();
        let blobs = root.join("blobs");
        fs::create_dir_all(&blobs).unwrap();
        // Header: Table -> [offset=0, size=10]; payload: "msid_name".
        fs::write(
            blobs.join("master"),
            [
                0x81, 0xa5, b'T', b'a', b'b', b'l', b'e', 0x92, 0x00, 0x0a, 0xa9, b'm', b's', b'i',
                b'd', b'_', b'n', b'a', b'm', b'e',
            ],
        )
        .unwrap();
        let output = root.join("datamine/master-data");
        export(
            &blobs,
            &root.join("absent-locales"),
            &root.join("locale-snapshot.json"),
            &output,
        )
        .unwrap()
        .unwrap();
        let raw: Value =
            serde_json::from_slice(&fs::read(output.join("raw/Table.json")).unwrap()).unwrap();
        assert_eq!(raw, Value::String("msid_name".into()));
        let index: Value =
            serde_json::from_slice(&fs::read(output.join("index.json")).unwrap()).unwrap();
        assert_eq!(index["rawTables"], 1);
        assert_eq!(index["schemaVersion"], 3);
    }

    #[test]
    fn export_resolves_msids_from_cache_when_current_delta_has_no_locale_bytes() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path();
        let blobs = root.join("blobs");
        fs::create_dir_all(&blobs).unwrap();
        fs::write(
            blobs.join("master"),
            [
                0x81, 0xa5, b'T', b'a', b'b', b'l', b'e', 0x92, 0x00, 0x0a, 0xa9, b'm', b's', b'i',
                b'd', b'_', b'n', b'a', b'm', b'e',
            ],
        )
        .unwrap();
        let cache = root.join("locale-snapshot.json");
        crate::support::json::write_json(
            &cache,
            &serde_json::json!({
                "schemaVersion": 1,
                "files": {
                    "fr_FR": {
                        "System.bytes": {"msid_name": "Pikachu"}
                    }
                }
            }),
        )
        .unwrap();
        let output = root.join("datamine/master-data");

        let summary = export(
            &blobs,
            &root.join("current-delta-without-locales"),
            &cache,
            &output,
        )
        .unwrap()
        .unwrap();

        let localized: Value =
            serde_json::from_slice(&fs::read(output.join("fr_FR/Table.json")).unwrap()).unwrap();
        assert_eq!(localized, Value::String("msid_name  (Pikachu)".into()));
        assert_eq!(summary.locale_files_refreshed, 0);
        assert_eq!(summary.locale_files_reused, 1);
    }

    #[test]
    fn decodes_a_minimal_master_memory() {
        let decoded = decode_master(&[
            0x81, 0xa5, b'T', b'a', b'b', b'l', b'e', 0x92, 0x00, 0x01, 0x01,
        ])
        .unwrap();
        assert_eq!(decoded.tables["Table"], Value::Number(Number::from(1)));
    }
    #[test]
    fn decodes_lz4_literals() {
        assert_eq!(lz4_block(&[0x30, b'a', b'b', b'c'], 3).unwrap(), b"abc");
    }

    #[test]
    fn resolves_msids_and_nested_tags_without_json_quotes() {
        let locale = BTreeMap::from([("msid_title".into(), "Value [Text: id=\"0\"]".into())]);
        let resolved = locales::resolve_value(
            &Value::String("msid_title".into()),
            &locale,
            &BTreeMap::new(),
            Some(vec!["Pikachu".into()]),
        );
        assert_eq!(
            resolved,
            Value::String("msid_title  (Value Pikachu)".into())
        );
        assert_eq!(
            locales::parameter_string(&Value::String("text".into())),
            "text"
        );
    }
}

pub fn export_memory(
    input: &[u8],
    assets: &[(String, Vec<(String, Vec<u8>)>)],
    destination: &Path,
) -> Result<Value> {
    use rayon::prelude::*;
    let start = std::time::Instant::now();
    let master = decode_master(input)?;
    let refs = locales::references(&master.tables);
    let dictionaries: Vec<_> = assets
        .par_iter()
        .map(|(language, files)| -> Result<_> {
            Ok((language.clone(), locales::memory_dictionary(files)?))
        })
        .collect::<Result<Vec<_>>>()?;
    let decode = start.elapsed().as_secs_f64();
    fs::create_dir_all(destination.join("raw"))?;
    for (language, _) in &dictionaries {
        fs::create_dir_all(destination.join(language))?;
    }
    let mut jobs = Vec::new();
    for (name, table) in &master.tables {
        let filename = format!("{}.json", safe_filename(name));
        jobs.push((destination.join("raw").join(&filename), table, None));
        for (language, dictionary) in &dictionaries {
            jobs.push((
                destination.join(language).join(&filename),
                table,
                Some(dictionary),
            ));
        }
    }
    let write = std::time::Instant::now();
    let bytes = jobs
        .par_iter()
        .map(|(path, table, dictionary)| -> Result<usize> {
            let value = dictionary.map(|d| locales::resolve_value(table, d, &refs, None));
            let bytes = serde_json::to_vec(value.as_ref().unwrap_or(table))?;
            fs::write(path, &bytes)?;
            Ok(bytes.len())
        })
        .try_reduce(|| 0, |a, b| Ok(a + b))?;
    Ok(
        serde_json::json!({"tables":master.tables.len(),"languages":dictionaries.iter().map(|(l,_)|l).collect::<Vec<_>>(),"files":jobs.len(),"bytes":bytes,"decode_and_dictionaries_seconds":decode,"resolve_write_seconds":write.elapsed().as_secs_f64(),"total_seconds":start.elapsed().as_secs_f64()}),
    )
}
