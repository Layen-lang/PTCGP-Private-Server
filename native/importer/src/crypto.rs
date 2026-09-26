const GLOBAL_KEY: [u8; 32] = [
    0xcf, 0xd3, 0xf5, 0xcb, 0x76, 0x0b, 0x81, 0xe7, 0x3b, 0x3d, 0x3b, 0x41, 0x17, 0x3f, 0x11, 0x5f,
    0xe0, 0x42, 0x94, 0x9d, 0xc4, 0x60, 0x42, 0x74, 0x9e, 0xcc, 0x87, 0x7d, 0x58, 0xd2, 0x29, 0x6c,
];
const MASTER_KEY: [u8; 32] = [
    0xf9, 0x61, 0x06, 0x6d, 0x49, 0xfe, 0xd6, 0xf5, 0x96, 0x39, 0xea, 0x91, 0x7e, 0x28, 0x66, 0x02,
    0xf5, 0x72, 0x99, 0x8e, 0xd1, 0x6b, 0x73, 0xf9, 0xc6, 0x51, 0x74, 0x90, 0xba, 0x87, 0xe6, 0x1b,
];
fn nonce(hash: u64, tail: u32) -> [u8; 12] {
    let mut n = [0; 12];
    n[..8].copy_from_slice(&hash.to_le_bytes());
    n[8..].copy_from_slice(&tail.to_le_bytes());
    n
}
fn decrypt_blob(data: &[u8], key: &[u8; 32], hash: u64) -> Vec<u8> {
    transform(key, nonce(hash, 0x6368_6368), data)
}
fn transform(key: &[u8; 32], nonce: [u8; 12], data: &[u8]) -> Vec<u8> {
    let initial = state(key, &nonce);
    let turns = turns(key, &nonce);
    let mut out = Vec::with_capacity(data.len());
    for (counter, chunk) in data.chunks(64).enumerate() {
        let ks = block(initial, counter as u32, turns);
        out.extend(chunk.iter().zip(ks).map(|(a, b)| a ^ b));
    }
    out
}
fn word(data: &[u8], off: usize) -> u32 {
    u32::from_le_bytes(data[off..off + 4].try_into().unwrap())
}
fn state(key: &[u8; 32], nonce: &[u8; 12]) -> [u32; 16] {
    let mut s = [0; 16];
    for (i, v) in b"A3AxwtfWD<PbxMx$".chunks(4).enumerate() {
        s[i] = word(v, 0)
    }
    for i in 0..8 {
        s[4 + i] = word(key, i * 4)
    }
    s[13] = word(nonce, 0);
    s[14] = word(nonce, 4);
    s[15] = word(nonce, 8);
    s
}
fn turns(key: &[u8; 32], n: &[u8; 12]) -> usize {
    let u = ((word(key, 20).wrapping_add(word(key, 0))) ^ word(key, 28))
        .wrapping_add((word(n, 4).wrapping_add(word(n, 0))) ^ word(n, 8));
    [6, 5, 6, 5, 5, 6, 5, 6, 6, 6, 5, 5, 5, 6, 6, 5]
        [(((u >> 7) & 2) | ((u >> 2) & 1) | ((u >> 13) & 4) | ((u >> 2) & 8)) as usize] as usize
}
fn quarter(s: &mut [u32; 16], a: usize, b: usize, c: usize, d: usize) {
    s[a] = s[a].wrapping_add(s[b]);
    s[d] = (s[d] ^ s[a]).rotate_left(16);
    s[c] = s[c].wrapping_add(s[d]);
    s[b] = (s[b] ^ s[c]).rotate_left(12);
    s[a] = s[a].wrapping_add(s[b]);
    s[d] = (s[d] ^ s[a]).rotate_left(8);
    s[c] = s[c].wrapping_add(s[d]);
    s[b] = (s[b] ^ s[c]).rotate_left(7)
}
fn block(initial: [u32; 16], counter: u32, turns: usize) -> [u8; 64] {
    let counter = counter.wrapping_add(1);
    let mut s = initial;
    s[12] = counter;
    for _ in 0..turns {
        quarter(&mut s, 0, 4, 8, 12);
        quarter(&mut s, 1, 5, 9, 13);
        quarter(&mut s, 2, 6, 10, 14);
        quarter(&mut s, 3, 7, 11, 15);
        quarter(&mut s, 0, 5, 10, 15);
        quarter(&mut s, 1, 6, 11, 12);
        quarter(&mut s, 2, 7, 8, 13);
        quarter(&mut s, 3, 4, 9, 14)
    }
    for i in 0..16 {
        s[i] = s[i].wrapping_add(if i == 12 { counter } else { initial[i] })
    }
    let mut out = [0; 64];
    for (i, v) in s.iter().enumerate() {
        out[i * 4..i * 4 + 4].copy_from_slice(&v.to_le_bytes())
    }
    out
}

pub fn decrypt(mode: &str, hash: u64, key: Option<&std::path::Path>, data: &[u8]) -> Vec<u8> {
    if mode == "key" {
        return transform(&GLOBAL_KEY, nonce(hash, 0), data);
    }
    let key: [u8; 32] = if let Some(path) = key {
        std::fs::read(path).unwrap().try_into().unwrap()
    } else {
        MASTER_KEY
    };
    decrypt_blob(data, &key, hash)
}

pub fn decrypt_cached(key: &[u8; 32], hash: u64, data: &[u8]) -> Vec<u8> {
    #[cfg(target_arch = "x86_64")]
    if std::arch::is_x86_feature_detected!("avx2") {
        // Runtime dispatch guarantees AVX2; the scalar path remains portable.
        return unsafe { transform_avx2(key, nonce(hash, 0x6368_6368), data) };
    }
    decrypt_blob(data, key, hash)
}
pub fn decrypt_master(hash: u64, data: &[u8]) -> Vec<u8> {
    decrypt_cached(&MASTER_KEY, hash, data)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn optimized_cipher_matches_scalar_across_block_boundaries() {
        let key = [0x47; 32];
        for size in [0, 1, 63, 64, 65, 511, 512, 513, 4096, 4099] {
            let input: Vec<u8> = (0..size).map(|i| (i * 29) as u8).collect();
            for hash in [0, 1, 0xf123456789abcdef] {
                assert_eq!(
                    decrypt_cached(&key, hash, &input),
                    decrypt_blob(&input, &key, hash)
                );
            }
        }
    }
}

#[cfg(target_arch = "x86_64")]
#[target_feature(enable = "avx2")]
unsafe fn transform_avx2(key: &[u8; 32], nonce: [u8; 12], data: &[u8]) -> Vec<u8> {
    use std::arch::x86_64::*;
    let initial = state(key, &nonce);
    let rounds = turns(key, &nonce);
    let mut output = vec![0; data.len()];
    let batches = data.len() / 512;
    for batch in 0..batches {
        let mut base = [_mm256_setzero_si256(); 16];
        for i in 0..16 {
            base[i] = _mm256_set1_epi32(initial[i] as i32);
        }
        let c = (batch * 8 + 1) as i32;
        base[12] = _mm256_setr_epi32(c, c + 1, c + 2, c + 3, c + 4, c + 5, c + 6, c + 7);
        let mut s = base;
        macro_rules! quarter8 {
            ($a:expr,$b:expr,$c:expr,$d:expr) => {{
                s[$a] = _mm256_add_epi32(s[$a], s[$b]);
                s[$d] = _mm256_xor_si256(s[$d], s[$a]);
                s[$d] = _mm256_or_si256(
                    _mm256_slli_epi32::<16>(s[$d]),
                    _mm256_srli_epi32::<16>(s[$d]),
                );
                s[$c] = _mm256_add_epi32(s[$c], s[$d]);
                s[$b] = _mm256_xor_si256(s[$b], s[$c]);
                s[$b] = _mm256_or_si256(
                    _mm256_slli_epi32::<12>(s[$b]),
                    _mm256_srli_epi32::<20>(s[$b]),
                );
                s[$a] = _mm256_add_epi32(s[$a], s[$b]);
                s[$d] = _mm256_xor_si256(s[$d], s[$a]);
                s[$d] = _mm256_or_si256(
                    _mm256_slli_epi32::<8>(s[$d]),
                    _mm256_srli_epi32::<24>(s[$d]),
                );
                s[$c] = _mm256_add_epi32(s[$c], s[$d]);
                s[$b] = _mm256_xor_si256(s[$b], s[$c]);
                s[$b] = _mm256_or_si256(
                    _mm256_slli_epi32::<7>(s[$b]),
                    _mm256_srli_epi32::<25>(s[$b]),
                );
            }};
        }
        for _ in 0..rounds {
            quarter8!(0, 4, 8, 12);
            quarter8!(1, 5, 9, 13);
            quarter8!(2, 6, 10, 14);
            quarter8!(3, 7, 11, 15);
            quarter8!(0, 5, 10, 15);
            quarter8!(1, 6, 11, 12);
            quarter8!(2, 7, 8, 13);
            quarter8!(3, 4, 9, 14);
        }
        let mut words = [[0u32; 8]; 16];
        for i in 0..16 {
            // Each destination is exactly eight u32 values (32 bytes).
            _mm256_storeu_si256(
                words[i].as_mut_ptr().cast(),
                _mm256_add_epi32(s[i], base[i]),
            );
        }
        for lane in 0..8 {
            for word in 0..16 {
                let offset = batch * 512 + lane * 64 + word * 4;
                let value = u32::from_le_bytes(data[offset..offset + 4].try_into().unwrap())
                    ^ words[word][lane];
                output[offset..offset + 4].copy_from_slice(&value.to_le_bytes());
            }
        }
    }
    for (i, chunk) in data[batches * 512..].chunks(64).enumerate() {
        let stream = block(initial, (batches * 8 + i) as u32, rounds);
        let offset = batches * 512 + i * 64;
        for j in 0..chunk.len() {
            output[offset + j] = chunk[j] ^ stream[j];
        }
    }
    output
}
