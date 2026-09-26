//! Bounded selective reader for the installed Unity 6000 Texture2D schema.
//! Other schemas fail explicitly; this is not a general Unity asset exporter.
use anyhow::{bail, ensure, Context, Result};
use image::RgbaImage;

struct Reader<'a> {
    data: &'a [u8],
    pos: usize,
    be: bool,
}
impl<'a> Reader<'a> {
    fn new(data: &'a [u8], be: bool) -> Self {
        Self { data, pos: 0, be }
    }
    fn take(&mut self, n: usize) -> Result<&'a [u8]> {
        let end = self.pos.checked_add(n).context("offset overflow")?;
        let out = self
            .data
            .get(self.pos..end)
            .context("truncated Unity data")?;
        self.pos = end;
        Ok(out)
    }
    fn skip(&mut self, n: usize) -> Result<()> {
        self.take(n)?;
        Ok(())
    }
    fn align(&mut self, n: usize) -> Result<()> {
        self.skip((n - self.pos % n) % n)
    }
    fn u8(&mut self) -> Result<u8> {
        Ok(self.take(1)?[0])
    }
    fn u16(&mut self) -> Result<u16> {
        let be = self.be;
        let v = self.take(2)?.try_into()?;
        Ok(if be {
            u16::from_be_bytes(v)
        } else {
            u16::from_le_bytes(v)
        })
    }
    fn u32(&mut self) -> Result<u32> {
        let be = self.be;
        let v = self.take(4)?.try_into()?;
        Ok(if be {
            u32::from_be_bytes(v)
        } else {
            u32::from_le_bytes(v)
        })
    }
    fn u64(&mut self) -> Result<u64> {
        let be = self.be;
        let v = self.take(8)?.try_into()?;
        Ok(if be {
            u64::from_be_bytes(v)
        } else {
            u64::from_le_bytes(v)
        })
    }
    fn cstr(&mut self) -> Result<String> {
        let n = self.data[self.pos..]
            .iter()
            .position(|v| *v == 0)
            .context("unterminated string")?;
        let s = std::str::from_utf8(self.take(n)?)?.to_owned();
        self.skip(1)?;
        Ok(s)
    }
    fn string(&mut self) -> Result<String> {
        let n = self.u32()? as usize;
        let s = std::str::from_utf8(self.take(n)?)?.to_owned();
        self.align(4)?;
        Ok(s)
    }
    fn blob(&mut self) -> Result<&'a [u8]> {
        let n = self.u32()? as usize;
        let b = self.take(n)?;
        self.align(4)?;
        Ok(b)
    }
}
fn decompress(data: &[u8], size: usize, flags: u32) -> Result<Vec<u8>> {
    ensure!(size <= 256 * 1024 * 1024, "oversize Unity block");
    match flags & 63 {
        0 => {
            ensure!(data.len() == size, "block length mismatch");
            Ok(data.to_vec())
        }
        2 | 3 => Ok(lz4_flex::block::decompress(data, size)?),
        v => bail!("unsupported bundle compression {v}"),
    }
}
struct Node {
    name: String,
    offset: usize,
    size: usize,
    flags: u32,
}
pub struct Bundle {
    data: Vec<u8>,
    nodes: Vec<Node>,
}
pub fn unpack(data: &[u8]) -> Result<Bundle> {
    let mut r = Reader::new(data, true);
    ensure!(r.cstr()? == "UnityFS", "not UnityFS");
    let version = r.u32()?;
    ensure!(
        (6..=8).contains(&version),
        "unsupported UnityFS version {version}"
    );
    r.cstr()?;
    r.cstr()?;
    let size = r.u64()? as usize;
    ensure!(size == data.len(), "bundle length mismatch");
    let compressed = r.u32()? as usize;
    let uncompressed = r.u32()? as usize;
    let flags = r.u32()?;
    if version >= 7 {
        r.align(16)?;
    }
    let info = if flags & 128 != 0 {
        data.get(data.len().checked_sub(compressed).context("bad footer")?..)
            .context("bad footer")?
    } else {
        r.take(compressed)?
    };
    let info = decompress(info, uncompressed, flags)?;
    let mut metadata = Reader::new(&info, true);
    metadata.skip(16)?;
    let n = metadata.u32()? as usize;
    ensure!(n < 100000, "too many Unity blocks");
    let mut blocks = Vec::with_capacity(n);
    for _ in 0..n {
        blocks.push((
            metadata.u32()? as usize,
            metadata.u32()? as usize,
            metadata.u16()? as u32,
        ));
    }
    let n = metadata.u32()? as usize;
    ensure!(n < 100000, "too many Unity nodes");
    let mut nodes = Vec::with_capacity(n);
    for _ in 0..n {
        nodes.push(Node {
            offset: metadata.u64()? as usize,
            size: metadata.u64()? as usize,
            flags: metadata.u32()?,
            name: metadata.cstr()?,
        });
    }
    if flags & 512 != 0 {
        r.align(16)?;
    }
    let mut plain = Vec::with_capacity(blocks.iter().map(|b| b.0).sum());
    for (size, compressed, flags) in blocks {
        plain.extend(decompress(r.take(compressed)?, size, flags)?);
    }
    for node in &nodes {
        ensure!(
            node.offset
                .checked_add(node.size)
                .is_some_and(|end| end <= plain.len()),
            "invalid node range"
        );
    }
    Ok(Bundle { data: plain, nodes })
}
struct Object<'a> {
    class: u32,
    schema: [u8; 16],
    data: &'a [u8],
}
fn objects(data: &[u8]) -> Result<Vec<Object<'_>>> {
    let mut r = Reader::new(data, true);
    r.skip(8)?;
    let version = r.u32()?;
    r.skip(4)?;
    ensure!(version == 22, "unsupported serialized version {version}");
    let endian = r.u8()?;
    ensure!(endian == 0, "unexpected serialized endianness");
    r.skip(3)?;
    r.u32()?;
    let _size = r.u64()?;
    let offset = r.u64()? as usize;
    r.u64()?;
    r.be = false;
    let _unity = r.cstr()?;
    r.u32()?;
    let tree = r.u8()? != 0;
    let count = r.u32()? as usize;
    ensure!(count <= 4096, "too many serialized types");
    let mut types = Vec::with_capacity(count);
    for _ in 0..count {
        let class = r.u32()?;
        r.u8()?;
        r.u16()?;
        if class == 114 {
            r.skip(16)?;
        }
        let schema = r.take(16)?.try_into()?;
        if tree {
            let n = r.u32()? as usize;
            let strings = r.u32()? as usize;
            r.skip(n.checked_mul(32).context("tree overflow")?)?;
            r.skip(strings)?;
            let deps = r.u32()? as usize;
            r.skip(deps.checked_mul(4).context("dependencies overflow")?)?;
        }
        types.push((class, schema));
    }
    let count = r.u32()? as usize;
    ensure!(count <= 100000, "too many serialized objects");
    let mut output = Vec::new();
    for _ in 0..count {
        r.align(4)?;
        r.u64()?;
        let start = (r.u64()? as usize)
            .checked_add(offset)
            .context("object offset overflow")?;
        let len = r.u32()? as usize;
        let id = r.u32()? as usize;
        let (class, schema) = *types.get(id).context("invalid type id")?;
        if class == 28 || class == 49 {
            output.push(Object {
                class,
                schema,
                data: data
                    .get(start..start.checked_add(len).context("object size overflow")?)
                    .context("invalid object range")?,
            });
        }
    }
    Ok(output)
}

impl Bundle {
    pub fn texture(&self, expected: &str) -> Result<(RgbaImage, u32)> {
        for node in self.nodes.iter().filter(|n| n.flags & 4 != 0) {
            for object in objects(&self.data[node.offset..node.offset + node.size])? {
                if object.class != 28 {
                    continue;
                }
                let mut r = Reader::new(object.data, false);
                let name = r.string()?;
                if name != expected {
                    continue;
                }
                ensure!(
                    object.schema
                        == [
                            0x46, 0x46, 0xd2, 0xd1, 0xd5, 0x6c, 0x29, 0x68, 0xfe, 0x29, 0x69, 0x7e,
                            0x1c, 0xbf, 0xf2, 0x0c
                        ],
                    "unsupported Texture2D schema for {name}: {:02x?}",
                    object.schema
                );
                r.u8()?;
                r.align(4)?;
                let w = r.u32()? as usize;
                let h = r.u32()? as usize;
                ensure!(
                    w > 0 && h > 0 && w <= 8192 && h <= 8192,
                    "invalid image dimensions"
                );
                r.u32()?;
                r.u32()?;
                let format = r.u32()?;
                r.u32()?;
                r.skip(3)?;
                r.align(4)?;
                r.string()?;
                r.u8()?;
                r.align(4)?;
                r.skip(12 + 24 + 8)?;
                r.blob()?;
                let inline = r.blob()?;
                let offset = r.u64()? as usize;
                let size = r.u32()? as usize;
                let path = r.string()?;
                let bytes = if size > 0 {
                    let basename = path.rsplit('/').next().unwrap();
                    let stream = self
                        .nodes
                        .iter()
                        .find(|n| n.name == basename)
                        .with_context(|| format!("missing texture stream {path}"))?;
                    ensure!(
                        offset.checked_add(size).is_some_and(|n| n <= stream.size),
                        "invalid texture stream range"
                    );
                    &self.data[stream.offset + offset..stream.offset + offset + size]
                } else {
                    inline
                };
                return Ok((decode_texture(bytes, w, h, format)?, format));
            }
        }
        bail!("Texture2D {expected} not found")
    }
    pub fn text_assets(&self) -> Result<Vec<(String, Vec<u8>)>> {
        let mut out = Vec::new();
        for node in self.nodes.iter().filter(|n| n.flags & 4 != 0) {
            for obj in objects(&self.data[node.offset..node.offset + node.size])? {
                if obj.class != 49 {
                    continue;
                }
                let mut r = Reader::new(obj.data, false);
                let name = r.string()?;
                let n = r.u32()? as usize;
                out.push((name, r.take(n)?.to_vec()));
            }
        }
        Ok(out)
    }
}

fn decode_texture(data: &[u8], w: usize, h: usize, format: u32) -> Result<RgbaImage> {
    let mut pixels = vec![0u32; w * h];
    let decoded = match format {
        48..=59 => {
            let b = [4, 5, 6, 8, 10, 12][((format - 48) % 6) as usize];
            texture2ddecoder::decode_astc(data, w, h, b, b, &mut pixels)
        }
        10 => texture2ddecoder::decode_bc1(data, w, h, &mut pixels),
        12 => texture2ddecoder::decode_bc3(data, w, h, &mut pixels),
        34 => texture2ddecoder::decode_etc1(data, w, h, &mut pixels),
        45 => texture2ddecoder::decode_etc2_rgb(data, w, h, &mut pixels),
        46 => texture2ddecoder::decode_etc2_rgba1(data, w, h, &mut pixels),
        47 => texture2ddecoder::decode_etc2_rgba8(data, w, h, &mut pixels),
        1 | 3 | 4 | 14 => Ok(()),
        v => bail!("unsupported texture format {v}"),
    };
    decoded.map_err(|e| anyhow::anyhow!("texture decode: {e}"))?;
    let mut out = vec![0u8; w * h * 4];
    for y in 0..h {
        for x in 0..w {
            let source = (h - 1 - y) * w + x;
            let target = (y * w + x) * 4;
            let rgba = match format {
                1 => [
                    255,
                    255,
                    255,
                    *data.get(source).context("short alpha texture")?,
                ],
                3 => {
                    let p = data
                        .get(source * 3..source * 3 + 3)
                        .context("short rgb texture")?;
                    [p[0], p[1], p[2], 255]
                }
                4 => data
                    .get(source * 4..source * 4 + 4)
                    .context("short rgba texture")?
                    .try_into()?,
                14 => {
                    let p = data
                        .get(source * 4..source * 4 + 4)
                        .context("short bgra texture")?;
                    [p[2], p[1], p[0], p[3]]
                }
                _ => {
                    let p = pixels[source];
                    [(p >> 16) as u8, (p >> 8) as u8, p as u8, (p >> 24) as u8]
                }
            };
            out[target..target + 4].copy_from_slice(&rgba);
        }
    }
    Ok(RgbaImage::from_raw(w as u32, h as u32, out).context("invalid decoded image")?)
}
