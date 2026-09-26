use crate::{card_mesh, crypto, unity_fast};
use anyhow::{ensure, Context, Result};
use image::{
    codecs::png::{CompressionType, FilterType, PngEncoder},
    ImageEncoder,
};
use rayon::prelude::*;
use serde::Deserialize;
use std::{
    collections::{HashMap, HashSet},
    fs,
    path::{Path, PathBuf},
    sync::atomic::{AtomicUsize, Ordering},
    time::Instant,
};

#[derive(Deserialize)]
pub struct Plan {
    pub outputs: Vec<Item>,
    pub blobs: HashMap<String, Blob>,
}
#[derive(Deserialize, Clone)]
pub struct Item {
    pub output: String,
    pub category: String,
    pub source: String,
    pub blobs: Vec<String>,
}
#[derive(Deserialize)]
pub struct Blob {
    pub address: String,
    pub content: String,
    pub bytes: usize,
    pub key: u64,
}

pub type ImageJob = Result<(usize, Vec<Vec<u8>>)>;
pub fn stream(
    plan: &Plan,
    key: &[u8; 32],
    output: &Path,
    workers: usize,
    mode: &str,
    png_filter: &str,
    input: std::sync::mpsc::Receiver<ImageJob>,
) -> Result<serde_json::Value> {
    let start = Instant::now();
    let dirs: HashSet<PathBuf> = plan
        .outputs
        .iter()
        .map(|i| {
            output
                .join("images")
                .join(&i.output)
                .parent()
                .unwrap()
                .to_owned()
        })
        .collect();
    for dir in dirs {
        fs::create_dir_all(dir)?;
    }
    let mesh = card_mesh::CardMesh::bundled().plan(367, 512);
    let filter = mesh.prepare_filter();
    let preparation = start.elapsed().as_secs_f64();
    let count = AtomicUsize::new(0);
    let first = std::sync::atomic::AtomicU64::new(0);
    let pool = rayon::ThreadPoolBuilder::new()
        .num_threads(workers)
        .build()?;
    let results=pool.install(||input.into_iter().par_bridge().map(|job|->Result<(usize,u64,[f64;5])>{
        let (index,data)=job?;let item=plan.outputs.get(index).context("bad streaming image index")?;
        ensure!(data.len()==item.blobs.len(),"incomplete streaming image");
        let mut timings=[0.;5];let mut textures=Vec::new();
        for ((hash,name),encrypted) in item.blobs.iter().zip(item.source.split(" + ")).zip(data){
            let row=&plan.blobs[hash];let t=Instant::now();
            ensure!(encrypted.len()==row.bytes,"streaming blob length mismatch");
            let plain=if row.key==0{encrypted}else{crypto::decrypt_cached(key,u64::from_str_radix(&row.content,16)?,&encrypted)};timings[0]+=t.elapsed().as_secs_f64();
            let t=Instant::now();let bundle=unity_fast::unpack(&plain)?;timings[1]+=t.elapsed().as_secs_f64();
            let t=Instant::now();let expected=Path::new(name).file_stem().context("missing texture name")?.to_str().context("name UTF8")?;
            textures.push(bundle.texture(expected)?.0);timings[2]+=t.elapsed().as_secs_f64();
        }
        let t=Instant::now();
        let result=if item.category=="cards"{
            ensure!(textures.len()==2,"card needs two textures");let top=textures.pop().unwrap();let mut base=textures.pop().unwrap();ensure!(base.dimensions()==(367,512)&&top.dimensions()==base.dimensions(),"card dimensions differ");
            for (b,t) in base.as_mut().chunks_exact_mut(4).zip(top.as_raw().chunks_exact(4)){
                let alpha=t[3] as u16;for c in 0..3{b[c]=((b[c] as u16*alpha+t[c] as u16*(255-alpha)+127)/255) as u8;}b[3]=255;
            }
            if mode=="exact"{mesh.render(&base)}else{filter.render(&base)}
        }else{ensure!(textures.len()==1,"expected one texture");textures.pop().unwrap()};
        timings[3]+=t.elapsed().as_secs_f64();
        let t=Instant::now();let mut png=Vec::with_capacity(result.len()/2);
        let filter=match png_filter{"sub"=>FilterType::Sub,"up"=>FilterType::Up,"none"=>FilterType::NoFilter,_=>FilterType::Adaptive};
        PngEncoder::new_with_quality(&mut png,CompressionType::Fast,filter).write_image(result.as_raw(),result.width(),result.height(),image::ExtendedColorType::Rgba8)?;
        let target=output.join("images").join(&item.output);let temporary=target.with_extension("png.partial");fs::write(&temporary,&png)?;fs::rename(&temporary,&target)?;println!("{}",serde_json::json!({"phase":"file","path":item.output}));timings[4]+=t.elapsed().as_secs_f64();
        let _=first.compare_exchange(0,start.elapsed().as_micros() as u64,Ordering::Relaxed,Ordering::Relaxed);
        let n=count.fetch_add(1,Ordering::Relaxed)+1;if n%500==0{println!("{}",serde_json::json!({"phase":"images","completed":n,"total":plan.outputs.len()}));}
        Ok((index,png.len() as u64,timings))
    }).collect::<Vec<_>>());
    let mut seen = HashSet::new();
    let mut times = [0.; 5];
    let mut bytes = 0;
    let mut errors = Vec::new();
    for result in results {
        match result {
            Ok((i, b, t)) => {
                ensure!(seen.insert(i), "duplicate streamed image");
                bytes += b;
                for j in 0..5 {
                    times[j] += t[j];
                }
            }
            Err(e) => errors.push(format!("{e:#}")),
        }
    }
    let report = serde_json::json!({"wall_seconds_including_stream_wait":start.elapsed().as_secs_f64(),"preparation_seconds":preparation,"first_png_seconds":first.load(Ordering::Relaxed) as f64/1e6,"outputs":plan.outputs.len(),"successes":seen.len(),"bytes":bytes,"workers":workers,"render_mode":mode,"png_filter":png_filter,"errors":errors,
        "worker_seconds_sum":{"decrypt":times[0],"unpack":times[1],"texture_decode":times[2],"compose_render":times[3],"encode_write":times[4]},"intermediate_files":0});
    fs::write(
        output.join("stream-processing.json"),
        serde_json::to_vec_pretty(&report)?,
    )?;
    ensure!(
        errors.is_empty() && seen.len() == plan.outputs.len(),
        "stream processing incomplete: {} successes; errors {:?}",
        seen.len(),
        errors.first()
    );
    Ok(report)
}
