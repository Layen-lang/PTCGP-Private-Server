#![allow(dead_code)]
use anyhow::{ensure, Result};
use std::{fs, path::Path};
mod card_mesh;
mod crypto;
mod fast_pipeline;
mod live_source;
mod masterdata;
mod unity_fast;
mod support {
    pub mod json {
        pub fn write_json<T: serde::Serialize>(
            path: &std::path::Path,
            value: &T,
        ) -> anyhow::Result<()> {
            if let Some(p) = path.parent() {
                std::fs::create_dir_all(p)?;
            }
            std::fs::write(path, serde_json::to_vec(value)?)?;
            Ok(())
        }
    }
}
fn run() -> Result<()> {
    let a: Vec<String> = std::env::args().collect();
    ensure!(
        a.len() == 7,
        "usage: ptcgp-importer <images|master> PLAN CONFIG OUTPUT READER WORKERS"
    );
    std::env::set_var("PTCGP_READER", &a[5]);
    let workers: usize = a[6].parse()?;
    ensure!((1..=16).contains(&workers), "workers outside 1..16");
    let plan: fast_pipeline::Plan = serde_json::from_slice(&fs::read(&a[2])?)?;
    for item in &plan.outputs {
        let p = Path::new(&item.output);
        ensure!(
            !p.is_absolute()
                && p.components()
                    .all(|c| matches!(c, std::path::Component::Normal(_)))
                && !item.output.contains('\\')
                && !item.output.contains(':'),
            "unsafe output path"
        );
        ensure!(
            item.blobs.len() == item.source.split(" + ").count(),
            "texture dependencies differ"
        );
        for hash in &item.blobs {
            ensure!(
                hash.len() == 16
                    && hash.bytes().all(|b| b.is_ascii_hexdigit())
                    && plan.blobs.contains_key(hash),
                "invalid dependency"
            );
        }
    }
    match a[1].as_str() {
        "images" => {
            if !plan.outputs.is_empty() {
                live_source::stream_run(&vec![
                    a[0].clone(),
                    "images".into(),
                    a[2].clone(),
                    a[3].clone(),
                    a[4].clone(),
                    a[6].clone(),
                    "filter".into(),
                    "adaptive".into(),
                    a[5].clone(),
                    "1".into(),
                    workers.min(8).to_string(),
                ])?;
            }
        }
        "master" => live_source::master_run(&vec![
            a[0].clone(),
            "master".into(),
            a[3].clone(),
            a[4].clone(),
            "all".into(),
        ])?,
        _ => anyhow::bail!("unknown import operation"),
    }
    println!("{}", serde_json::json!({"phase":"complete"}));
    Ok(())
}
fn main() {
    if let Err(e) = run() {
        eprintln!("{e:#}");
        std::process::exit(1)
    }
}
