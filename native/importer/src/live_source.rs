use crate::{
    crypto,
    fast_pipeline::{self, Plan},
};
use anyhow::{bail, ensure, Context, Result};
use rayon::prelude::*;
use std::{
    collections::{HashMap, HashSet},
    fs,
    io::{BufReader, Read},
    path::Path,
    process::{Command, Stdio},
    time::Instant,
};
fn remote() -> String {
    std::env::var("PTCGP_SOURCE_ROOT").expect("source root required")
}
const PACKAGE: &str = "jp.pokemon.pokemontcgp";
fn serial() -> String {
    std::env::var("PTCGP_ADB_SERIAL").expect("device required")
}
fn shell(command: &str) -> String {
    if std::env::var("PTCGP_USE_SU").as_deref() == Ok("1") {
        // LDPlayer's su can allocate a PTY even for adb exec-out. Its ONLCR
        // processing inserts CR bytes into tar archives and reader streams.
        format!(
            "su -c {}",
            quote(&format!("stty -onlcr 2>/dev/null; {command}"))
        )
    } else {
        command.to_owned()
    }
}
fn apks() -> Vec<String> {
    serde_json::from_str(&std::env::var("PTCGP_APKS").expect("APKs required")).expect("APKs JSON")
}
fn quote(s: &str) -> String {
    format!("'{}'", s.replace('\'', "'\\''"))
}
fn adb(command: &str) -> Result<Vec<u8>> {
    let r = adb_process()
        .args(["-s", &serial(), "exec-out", &shell(command)])
        .output()?;
    ensure!(
        r.status.success(),
        "ADB failed: {}",
        String::from_utf8_lossy(&r.stderr)
    );
    Ok(r.stdout)
}
fn le16(d: &[u8], p: usize) -> Result<u16> {
    Ok(u16::from_le_bytes(
        d.get(p..p + 2).context("truncated u16")?.try_into()?,
    ))
}
fn le32(d: &[u8], p: usize) -> Result<u32> {
    Ok(u32::from_le_bytes(
        d.get(p..p + 4).context("truncated u32")?.try_into()?,
    ))
}
fn le64(d: &[u8], p: usize) -> Result<u64> {
    Ok(u64::from_le_bytes(
        d.get(p..p + 8).context("truncated u64")?.try_into()?,
    ))
}
fn validate_index(data: &[u8], plan: &Plan) -> Result<usize> {
    ensure!(data.get(4..8) == Some(b"ALI2"), "missing ALI2 index");
    let root = le32(data, 0)? as usize;
    let delta = le32(data, root)? as i32;
    let vt = (root as isize - delta as isize) as usize;
    let vector = |field: usize| -> Result<usize> {
        let p = root + le16(data, vt + 4 + field * 2)? as usize;
        Ok(p + le32(data, p)? as usize)
    };
    let rows = vector(0)?;
    let names = vector(2)?;
    let n = le32(data, rows)? as usize;
    ensure!(n == le32(data, names)? as usize, "index counts differ");
    let wanted: HashMap<&str, _> = plan
        .blobs
        .iter()
        .map(|(hash, row)| (row.address.as_str(), (hash, row)))
        .collect();
    let mut found = HashSet::new();
    for i in 0..n {
        let p = names + 4 + i * 4;
        let p = p + le32(data, p)? as usize;
        let len = le32(data, p)? as usize;
        let name =
            std::str::from_utf8(data.get(p + 4..p + 4 + len).context("index string range")?)?;
        if let Some((hash, row)) = wanted.get(name) {
            let p = rows + 4 + i * 48;
            ensure!(
                le64(data, p)? == u64::from_str_radix(&row.content, 16)?
                    && le64(data, p + 16)? == u64::from_str_radix(hash, 16)?
                    && le64(data, p + 24)? as usize == row.bytes
                    && le64(data, p + 32)? == row.key,
                "installed asset revision differs: {name}"
            );
            found.insert(hash.as_str());
        }
    }
    ensure!(
        found.len() == plan.blobs.len(),
        "live index missing {} selected assets",
        plan.blobs.len() - found.len()
    );
    Ok(n)
}
struct RemoteFiles {
    paths: Vec<String>,
}
impl Drop for RemoteFiles {
    fn drop(&mut self) {
        let _ = adb(&format!(
            "rm -f {}",
            self.paths
                .iter()
                .map(|p| quote(p))
                .collect::<Vec<_>>()
                .join(" ")
        ));
    }
}
fn push(local: &Path, remote: &str) -> Result<()> {
    let r = adb_process()
        .args(["-s", &serial(), "push"])
        .arg(local)
        .arg(remote)
        .output()?;
    ensure!(r.status.success(), "push reader manifest failed");
    Ok(())
}
fn resource_root(_index: &str) -> Result<String> {
    Ok(remote())
}
fn octal(d: &[u8]) -> Result<usize> {
    let s = std::str::from_utf8(d)?.trim_matches(['\0', ' ']);
    Ok(if s.is_empty() {
        0
    } else {
        usize::from_str_radix(s, 8)?
    })
}
fn tar_files(
    command: &str,
    expected: &Plan,
) -> Result<(
    HashMap<String, Vec<u8>>,
    Option<[u8; 32]>,
    Option<Vec<u8>>,
    usize,
)> {
    let mut child = adb_process()
        .args(["-s", &serial(), "exec-out", &shell(command)])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()?;
    let mut input =
        BufReader::with_capacity(256 * 1024, child.stdout.take().context("missing stdout")?);
    let mut blobs = HashMap::new();
    let mut key = None;
    let mut index = None;
    let mut transferred = 0;
    loop {
        let mut header = [0u8; 512];
        input.read_exact(&mut header)?;
        transferred += 512;
        if header.iter().all(|v| *v == 0) {
            break;
        }
        let checksum: usize = header
            .iter()
            .enumerate()
            .map(|(i, v)| {
                if (148..156).contains(&i) {
                    32
                } else {
                    *v as usize
                }
            })
            .sum();
        ensure!(
            checksum == octal(&header[148..156])?,
            "invalid tar header; device file may be missing"
        );
        let len = header[..100].iter().position(|v| *v == 0).unwrap_or(100);
        let name = std::str::from_utf8(&header[..len])?;
        let size = octal(&header[124..136])?;
        ensure!(size < 128 * 1024 * 1024, "unexpected tar file size");
        let mut data = vec![0; size];
        input.read_exact(&mut data)?;
        let padding = (512 - size % 512) % 512;
        let mut pad = [0u8; 512];
        input.read_exact(&mut pad[..padding])?;
        transferred += size + padding;
        if header[156] == b'5' {
            continue;
        }
        let stem = Path::new(name)
            .file_stem()
            .context("tar file name")?
            .to_str()
            .context("tar UTF8")?;
        if name.starts_with("DefaultMasterData/blob/") && size == 32 {
            ensure!(key.is_none(), "multiple default keys");
            let decoded = crypto::decrypt("key", u64::from_str_radix(stem, 16)?, None, &data);
            key = Some(
                decoded
                    .try_into()
                    .map_err(|_| anyhow::anyhow!("key length"))?,
            );
        } else if name.contains("/index/") {
            ensure!(index.is_none(), "multiple indexes");
            index = Some(data);
        } else if name.starts_with("Sharin.Resources/Default/blob/") {
            let row = expected.blobs.get(stem).context("unexpected blob in tar")?;
            ensure!(row.bytes == size, "blob length mismatch");
            ensure!(
                blobs.insert(stem.to_owned(), data).is_none(),
                "duplicate tar blob"
            );
        } else {
            bail!("unexpected tar entry {name}");
        }
    }
    transferred += std::io::copy(&mut input, &mut std::io::sink())? as usize;
    let status = child.wait()?;
    ensure!(status.success(), "tar transfer failed");
    Ok((blobs, key, index, transferred))
}
fn stream_reader(
    command: &str,
    plan: &Plan,
    lookup: &HashMap<String, (usize, usize)>,
    sender: std::sync::mpsc::SyncSender<fast_pipeline::ImageJob>,
) -> Result<(usize, usize, f64)> {
    let start = Instant::now();
    let mut child = adb_process()
        .args(["-s", &serial(), "exec-out", &shell(command)])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()?;
    let mut input =
        BufReader::with_capacity(1024 * 1024, child.stdout.take().context("stream stdout")?);
    let mut pending: HashMap<usize, Vec<Option<Vec<u8>>>> = HashMap::new();
    let mut transferred = 0;
    let mut count = 0;
    let mut seen = HashSet::new();
    let result = (|| -> Result<()> {
        let mut magic = [0u8; 8];
        input.read_exact(&mut magic)?;
        ensure!(&magic == b"PTCGP001", "stream protocol mismatch");
        transferred += 8;
        loop {
            let mut header = [0u8; 11];
            input.read_exact(&mut header)?;
            let tag = header[0];
            let name_size = u16::from_le_bytes(header[1..3].try_into()?) as usize;
            let size = u64::from_le_bytes(header[3..11].try_into()?) as usize;
            ensure!(
                name_size <= 256 && size <= 128 * 1024 * 1024,
                "invalid stream record size"
            );
            let mut id = vec![0; name_size];
            input.read_exact(&mut id)?;
            let id = String::from_utf8(id)?;
            let mut data = vec![0; size];
            input.read_exact(&mut data)?;
            transferred += 11 + name_size + size;
            if tag == b'Z' {
                break;
            }
            ensure!(
                tag == b'B' && plan.blobs.get(&id).is_some_and(|r| r.bytes == size),
                "unexpected stream blob"
            );
            ensure!(seen.insert(id.clone()), "duplicate stream blob");
            let &(item, part) = lookup.get(&id).context("unmapped stream blob")?;
            let entry = pending
                .entry(item)
                .or_insert_with(|| vec![None; plan.outputs[item].blobs.len()]);
            entry[part] = Some(data);
            count += 1;
            if entry.iter().all(Option::is_some) {
                let data = pending
                    .remove(&item)
                    .unwrap()
                    .into_iter()
                    .map(Option::unwrap)
                    .collect();
                sender
                    .send(Ok((item, data)))
                    .map_err(|_| anyhow::anyhow!("stream processor stopped"))?;
            }
        }
        ensure!(pending.is_empty(), "incomplete card pairs in stream");
        Ok(())
    })();
    if result.is_err() {
        let _ = child.kill();
    }
    let status = child.wait()?;
    result?;
    ensure!(status.success(), "stream reader ADB failed");
    Ok((count, transferred, start.elapsed().as_secs_f64()))
}

pub fn stream_run(args: &[String]) -> Result<()> {
    let start = Instant::now();
    let plan: Plan = serde_json::from_slice(&fs::read(&args[2])?)?;
    let cfg: serde_json::Value = serde_json::from_slice(&fs::read(&args[3])?)?;
    let output = Path::new(&args[4]);
    fs::create_dir_all(output)?;
    let workers: usize = args[5].parse()?;
    let mode = &args[6];
    let filter = &args[7];
    let reader = Path::new(&args[8]);
    let reader_workers = &args[9];
    let streams: usize = args[10].parse()?;
    ensure!((1..=16).contains(&streams), "invalid stream count");
    let package = String::from_utf8(adb(&format!(
        "pm path {PACKAGE}; dumpsys package {PACKAGE} | grep versionName="
    ))?)?;
    let version = cfg["client"]["appVersion"]
        .as_str()
        .context("version missing")?;
    ensure!(
        package.contains(&format!("versionName={version}")),
        "installed version differs"
    );
    let apk = apks()[0].clone();
    let indexhash = cfg["client"]["androidAssetAladdinHash"]
        .as_str()
        .context("index revision missing")?;
    let indexpath = format!(
        "Sharin.Resources/Default/index/{}/{indexhash}.aladin",
        &indexhash[..2]
    );
    let source_root = resource_root(&indexpath)?;
    let controls = Instant::now();
    let (_, key, index, control_bytes) = tar_files(
        &format!(
            "tar -cf - -C {} DefaultMasterData/blob {}",
            quote(&source_root),
            quote(&indexpath)
        ),
        &plan,
    )?;
    let key = key.context("control key missing")?;
    validate_index(&index.context("control index missing")?, &plan)?;
    let control_seconds = controls.elapsed().as_secs_f64();
    let mut shards = vec![Vec::new(); streams];
    let mut weights = vec![0usize; streams];
    let mut lookup = HashMap::new();
    for (i, item) in plan.outputs.iter().enumerate() {
        let shard = weights
            .iter()
            .enumerate()
            .min_by_key(|(_, w)| **w)
            .unwrap()
            .0;
        for (part, hash) in item.blobs.iter().enumerate() {
            ensure!(
                lookup.insert(hash.clone(), (i, part)).is_none(),
                "duplicate dependency"
            );
            shards[shard].push(serde_json::json!({"hash":hash,"bytes":plan.blobs[hash].bytes}));
            weights[shard] += plan.blobs[hash].bytes;
        }
    }
    for shard in &mut shards {
        shard.sort_by(|a, b| a["hash"].as_str().cmp(&b["hash"].as_str()));
    }
    let prefix = format!(
        "/data/local/tmp/ptcgp-stream-benchmark-{}",
        std::process::id()
    );
    let mut remote = RemoteFiles {
        paths: vec![format!("{prefix}.bin")],
    };
    push(reader, &remote.paths[0])?;
    adb(&format!("chmod 700 {}", quote(&remote.paths[0])))?;
    let mut commands = Vec::new();
    for (i, jobs) in shards.iter().enumerate() {
        let path = format!("{prefix}-{i}.json");
        let log = format!("{prefix}-{i}.log");
        let local = output.join(format!("stream-manifest-{i}.json"));
        fs::write(
            &local,
            serde_json::to_vec(
                &serde_json::json!({"root":source_root,"apk":apk,"apks":apks(),"index":indexpath,"controls":false,"jobs":jobs}),
            )?,
        )?;
        remote.paths.extend([path.clone(), log.clone()]);
        push(&local, &path)?;
        commands.push(format!(
            "{} {} {} 2>{}",
            quote(&remote.paths[0]),
            quote(&path),
            quote(reader_workers),
            quote(&log)
        ));
    }
    let preparation = start.elapsed().as_secs_f64();
    let (send, receive) = std::sync::mpsc::sync_channel::<fast_pipeline::ImageJob>(32);
    let (processing, readers) = std::thread::scope(|scope| {
        let jobs: Vec<_> = commands
            .iter()
            .map(|command| {
                let sender = send.clone();
                let plan = &plan;
                let lookup = &lookup;
                scope.spawn(move || {
                    let result = stream_reader(command, plan, lookup, sender.clone());
                    if let Err(e) = &result {
                        let _ = sender.send(Err(anyhow::anyhow!("source reader: {e:#}")));
                    }
                    result
                })
            })
            .collect();
        drop(send);
        let processing = fast_pipeline::stream(&plan, &key, output, workers, mode, filter, receive);
        let readers = jobs
            .into_iter()
            .map(|j| j.join().unwrap())
            .collect::<Vec<_>>();
        (processing, readers)
    });
    let readers = readers.into_iter().collect::<Result<Vec<_>>>()?;
    let processing = processing?;
    ensure!(
        readers.iter().map(|r| r.0).sum::<usize>() == plan.blobs.len(),
        "missing streamed blobs"
    );
    let report = serde_json::json!({"total_seconds":start.elapsed().as_secs_f64(),"preparation_seconds":preparation,"control_seconds":control_seconds,"first_png_seconds":preparation+processing["first_png_seconds"].as_f64().unwrap_or(0.),"processing":processing,"readers":readers,"transfer_streams":streams,"reader_workers_per_stream":reader_workers,"transferred_bytes":control_bytes+readers.iter().map(|r|r.1).sum::<usize>(),"reader_binary_bytes":fs::metadata(reader)?.len(),"live_device":&serial(),"game_version":version,"source_root":source_root,"full_apk_copied":false,"python_runtime":false,"intermediate_files":0,"ready_images_queue_bound":32,"os_and_emulator_caches_flushed":false});
    fs::write(
        output.join("stream-report.json"),
        serde_json::to_vec_pretty(&report)?,
    )?;
    println!("{report}");
    Ok(())
}

struct IndexRow {
    hash: String,
    content: u64,
    bytes: usize,
    key: u64,
}
fn index_records(data: &[u8]) -> Result<HashMap<String, IndexRow>> {
    ensure!(data.get(4..8) == Some(b"ALI2"), "index is not ALI2");
    let root = le32(data, 0)? as usize;
    let delta = le32(data, root)? as i32;
    let vt = (root as isize - delta as isize) as usize;
    let vector = |field: usize| -> Result<usize> {
        let p = root + le16(data, vt + 4 + field * 2)? as usize;
        Ok(p + le32(data, p)? as usize)
    };
    let rows = vector(0)?;
    let names = vector(2)?;
    let n = le32(data, rows)? as usize;
    ensure!(n == le32(data, names)? as usize, "index counts differ");
    let mut out = HashMap::with_capacity(n);
    for i in 0..n {
        let p = names + 4 + i * 4;
        let p = p + le32(data, p)? as usize;
        let len = le32(data, p)? as usize;
        let name =
            std::str::from_utf8(data.get(p + 4..p + 4 + len).context("index string")?)?.to_owned();
        let p = rows + 4 + i * 48;
        out.insert(
            name,
            IndexRow {
                hash: format!("{:016x}", le64(data, p + 16)?),
                content: le64(data, p)?,
                bytes: le64(data, p + 24)? as usize,
                key: le64(data, p + 32)?,
            },
        );
    }
    Ok(out)
}
pub fn master_run(args: &[String]) -> Result<()> {
    let remote = remote();
    let start = Instant::now();
    let cfg: serde_json::Value = serde_json::from_slice(&fs::read(&args[2])?)?;
    let output = Path::new(&args[3]);
    fs::create_dir_all(output)?;
    let languages: Vec<&str> = match args[4].as_str() {
        "raw" => vec![],
        "all" => vec![
            "de_DE", "en_US", "es_ES", "fr_FR", "it_IT", "ja_JP", "ko_KR", "pt_BR", "zh_TW",
        ],
        list => list.split(',').collect(),
    };
    let package = String::from_utf8(adb(&format!(
        "pm path {PACKAGE}; dumpsys package {PACKAGE} | grep versionName="
    ))?)?;
    let version = cfg["client"]["appVersion"]
        .as_str()
        .context("version missing")?;
    ensure!(
        package.contains(&format!("versionName={version}")),
        "game version differs"
    );
    let masterhash = cfg["client"]["masterMemoryAladdinHash"]
        .as_str()
        .context("master index missing")?;
    let defaulthash = cfg["client"]["androidAssetAladdinHash"]
        .as_str()
        .context("asset index missing")?;
    let empty = Plan {
        outputs: vec![],
        blobs: HashMap::new(),
    };
    let (master_index, controls) = std::thread::scope(|scope| {
        let a = scope.spawn(|| {
            adb(&format!(
                "cat {remote}/Sharin.Resources/aladin/index/{}/{masterhash}.aladin",
                &masterhash[..2]
            ))
        });
        let b=scope.spawn(||->Result<_>{if languages.is_empty(){return Ok(None);}
            Ok(Some(tar_files(&format!("tar -cf - -C {} DefaultMasterData/blob Sharin.Resources/Default/index/{}/{defaulthash}.aladin",quote(&remote),&defaulthash[..2]),&empty)?))});
        (a.join().unwrap(), b.join().unwrap())
    });
    let master_index = master_index?;
    let mut transferred = master_index.len();
    let master = index_records(&master_index)?
        .remove("MasterMemory.bytes")
        .context("MasterMemory missing")?;
    let mut jobs = vec![("master", "aladin", master)];
    let mut key = None;
    if let Some((_, k, index, bytes)) = controls? {
        transferred += bytes;
        key = k;
        let mut records = index_records(&index.context("asset index missing")?)?;
        for language in &languages {
            jobs.push((
                *language,
                "Default",
                records
                    .remove(&format!("Common/Locale/{language}_bundles"))
                    .context("locale bundle missing")?,
            ));
        }
    }
    let controls_seconds = start.elapsed().as_secs_f64();
    let fetch = Instant::now();
    let pool = rayon::ThreadPoolBuilder::new().num_threads(4).build()?;
    let prefix = format!("/data/local/tmp/ptcgp-master-{}.bin", std::process::id());
    let reader = std::env::var("PTCGP_READER").context("native reader missing")?;
    let _remote_reader = RemoteFiles {
        paths: vec![prefix.clone()],
    };
    push(Path::new(&reader), &prefix)?;
    adb(&format!("chmod 700 {}", quote(&prefix)))?;
    let payloads = pool.install(|| {
        jobs.par_iter()
            .map(|(name, namespace, row)| -> Result<_> {
                let command = format!(
                    "{} read {} {} {} {} {}",
                    quote(&prefix),
                    quote(&remote),
                    quote(namespace),
                    quote(&row.hash),
                    row.bytes,
                    apks()
                        .iter()
                        .map(|p| quote(p))
                        .collect::<Vec<_>>()
                        .join(" ")
                );
                let encrypted = adb(&command)?;
                ensure!(
                    encrypted.len() == row.bytes,
                    "master payload size mismatch {name}"
                );
                let plain = if row.key == 0 {
                    encrypted
                } else if *namespace == "aladin" {
                    crypto::decrypt_master(row.content, &encrypted)
                } else {
                    crypto::decrypt_cached(
                        key.as_ref().context("default key missing")?,
                        row.content,
                        &encrypted,
                    )
                };
                Ok((*name, plain))
            })
            .collect::<Result<Vec<_>>>()
    })?;
    transferred += jobs.iter().map(|(_, _, r)| r.bytes).sum::<usize>();
    let fetch_seconds = fetch.elapsed().as_secs_f64();
    let extract = Instant::now();
    let mut master = None;
    let mut locales = Vec::new();
    for (name, data) in payloads {
        if name == "master" {
            master = Some(data);
        } else {
            let bundle = crate::unity_fast::unpack(&data)?;
            let assets = bundle.text_assets()?;
            ensure!(
                assets.len() == 6 && assets.iter().all(|(_, b)| b.starts_with(b"KVRF")),
                "invalid locale assets"
            );
            locales.push((name.to_owned(), assets));
        }
    }
    let extract_seconds = extract.elapsed().as_secs_f64();
    let export = crate::masterdata::export_memory(
        &master.context("master payload missing")?,
        &locales,
        &output.join("output"),
    )?;
    let report = serde_json::json!({"total_seconds":start.elapsed().as_secs_f64(),"controls_seconds":controls_seconds,"fetch_decrypt_seconds":fetch_seconds,"extract_textassets_seconds":extract_seconds,"native_export":export,"transferred_bytes":transferred,"languages":languages,"python_runtime":false,"intermediate_files":0,"live_device":&serial(),"os_and_emulator_caches_flushed":false});
    fs::write(
        output.join("master-report.json"),
        serde_json::to_vec_pretty(&report)?,
    )?;
    println!("{report}");
    Ok(())
}

fn adb_process() -> Command {
    let mut cmd = Command::new("adb");
    #[cfg(windows)]
    {
        use std::os::windows::process::CommandExt;
        cmd.creation_flags(0x08000000);
    }
    cmd
}
