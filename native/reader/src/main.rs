use anyhow::{ensure, Context, Result};
use serde::Deserialize;
use std::{
    fs::{self, File},
    io::{BufWriter, Read, Write},
    path::Path,
    sync::{
        atomic::{AtomicBool, AtomicUsize, Ordering},
        mpsc::sync_channel,
    },
};
#[derive(Deserialize)]
struct Manifest {
    root: String,
    apk: String,
    #[serde(default)]
    apks: Vec<String>,
    index: String,
    #[serde(default = "enabled")]
    controls: bool,
    jobs: Vec<Job>,
}
fn enabled() -> bool {
    true
}
#[derive(Deserialize)]
struct Job {
    hash: String,
    bytes: usize,
}
fn record(out: &mut impl Write, tag: u8, id: &str, data: &[u8]) -> Result<()> {
    out.write_all(&[tag])?;
    out.write_all(&(id.len() as u16).to_le_bytes())?;
    out.write_all(&(data.len() as u64).to_le_bytes())?;
    out.write_all(id.as_bytes())?;
    out.write_all(data)?;
    Ok(())
}
fn run() -> Result<()> {
    let args: Vec<String> = std::env::args().collect();
    if args.get(1).map(String::as_str) == Some("read") {
        ensure!(
            args.len() >= 7,
            "usage: read ROOT NAMESPACE HASH SIZE APK..."
        );
        let hash = &args[4];
        ensure!(
            hash.len() == 16 && hash.bytes().all(|b| b.is_ascii_hexdigit()),
            "invalid blob hash"
        );
        ensure!(
            matches!(args[3].as_str(), "Default" | "aladin"),
            "invalid namespace"
        );
        let size: usize = args[5].parse()?;
        ensure!(size <= 128 * 1024 * 1024, "blob too large");
        let path = Path::new(&args[2]).join(format!(
            "Sharin.Resources/{}/blob/{}/{}.aladin",
            args[3],
            &hash[..2],
            hash
        ));
        let data = if path.is_file() {
            fs::read(path)?
        } else {
            let mut found = None;
            let name = format!("assets/assetpack/blob/{}/{}.aladin", &hash[..2], hash);
            for apk in &args[6..] {
                let mut archive = zip::ZipArchive::new(File::open(apk)?)?;
                if let Ok(mut entry) = archive.by_name(&name) {
                    ensure!(entry.size() as usize == size, "APK size mismatch");
                    let mut data = Vec::with_capacity(size);
                    entry.read_to_end(&mut data)?;
                    found = Some(data);
                    break;
                };
            }
            found.context("blob missing from installed APKs")?
        };
        ensure!(data.len() == size, "blob size mismatch");
        std::io::stdout().lock().write_all(&data)?;
        return Ok(());
    }
    let manifest: Manifest = serde_json::from_slice(&fs::read(&args[1])?)?;
    let workers: usize = args.get(2).map(|s| s.parse()).transpose()?.unwrap_or(4);
    ensure!((1..=16).contains(&workers), "invalid workers");
    let root = Path::new(&manifest.root);
    let mut out = BufWriter::with_capacity(1024 * 1024, std::io::stdout().lock());
    out.write_all(b"PTCGP001")?;
    let mut todo = if manifest.controls {
        vec![root.join("DefaultMasterData/blob")]
    } else {
        vec![]
    };
    let mut found = 0;
    while let Some(dir) = todo.pop() {
        for file in fs::read_dir(dir)? {
            let file = file?;
            let m = file.metadata()?;
            if m.is_dir() {
                todo.push(file.path());
            } else if m.len() == 32 {
                let id = file
                    .path()
                    .file_stem()
                    .context("key name")?
                    .to_str()
                    .context("key UTF8")?
                    .to_owned();
                record(&mut out, b'K', &id, &fs::read(file.path())?)?;
                found += 1;
            }
        }
    }
    if manifest.controls {
        ensure!(found == 1, "expected one default key");
        record(&mut out, b'I', "", &fs::read(root.join(&manifest.index))?)?;
    }
    let next = AtomicUsize::new(0);
    let cancelled = AtomicBool::new(false);
    let (send, recv) = sync_channel::<Result<(String, Vec<u8>)>>(workers * 2);
    std::thread::scope(|scope| -> Result<()> {
        for _ in 0..workers {
            let send = send.clone();
            let next = &next;
            let manifest = &manifest;
            let cancelled = &cancelled;
            scope.spawn(move || {
                let work = || -> Result<()> {
                    let paths = if manifest.apks.is_empty() {
                        vec![manifest.apk.clone()]
                    } else {
                        manifest.apks.clone()
                    };
                    let mut archives = paths
                        .iter()
                        .map(|p| Ok(zip::ZipArchive::new(File::open(p)?)?))
                        .collect::<Result<Vec<_>>>()?;
                    loop {
                        if cancelled.load(Ordering::Relaxed) {
                            break;
                        }
                        let i = next.fetch_add(1, Ordering::Relaxed);
                        let Some(job) = manifest.jobs.get(i) else {
                            break;
                        };
                        ensure!(
                            job.hash.len() == 16
                                && job.hash.bytes().all(|b| b.is_ascii_hexdigit())
                                && job.bytes <= 128 * 1024 * 1024,
                            "invalid manifest entry"
                        );
                        let path = root.join(format!(
                            "Sharin.Resources/Default/blob/{}/{}.aladin",
                            &job.hash[..2],
                            job.hash
                        ));
                        let data = match fs::read(path) {
                            Ok(data) => data,
                            Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                                let name = format!(
                                    "assets/assetpack/blob/{}/{}.aladin",
                                    &job.hash[..2],
                                    job.hash
                                );
                                let mut found = None;
                                for archive in &mut archives {
                                    if let Ok(mut entry) = archive.by_name(&name) {
                                        ensure!(
                                            entry.size() as usize == job.bytes,
                                            "APK size mismatch"
                                        );
                                        let mut data = Vec::with_capacity(job.bytes);
                                        entry.read_to_end(&mut data)?;
                                        found = Some(data);
                                        break;
                                    }
                                }
                                found.context("asset missing from installed APKs")?
                            }
                            Err(e) => return Err(e.into()),
                        };
                        ensure!(data.len() == job.bytes, "blob size mismatch");
                        if send.send(Ok((job.hash.clone(), data))).is_err() {
                            break;
                        }
                    }
                    Ok(())
                };
                if let Err(e) = work() {
                    let _ = send.send(Err(e));
                }
            });
        }
        drop(send);
        let mut count = 0;
        let mut error = None;
        for item in recv.iter() {
            match item {
                Ok((id, data)) => {
                    if error.is_none() {
                        if let Err(e) = record(&mut out, b'B', &id, &data) {
                            error = Some(e);
                            cancelled.store(true, Ordering::Relaxed);
                        }
                    }
                    count += 1;
                }
                Err(e) => {
                    if error.is_none() {
                        error = Some(e);
                    }
                    cancelled.store(true, Ordering::Relaxed);
                }
            }
        }
        if let Some(error) = error {
            return Err(error);
        }
        ensure!(count == manifest.jobs.len(), "missing reader jobs");
        Ok(())
    })?;
    record(&mut out, b'Z', "", &[])?;
    out.flush()?;
    Ok(())
}
fn main() {
    if let Err(e) = run() {
        eprintln!("{e:#}");
        std::process::exit(1);
    }
}
