use image::{Rgba, RgbaImage};

const RENDER_SUPERSAMPLE: u32 = 3;

#[derive(Debug, Clone)]
pub(super) struct CardMesh {
    vertices: &'static [Vertex],
    indices: &'static [u16],
}

#[derive(Debug, Clone)]
pub(super) struct CardRenderPlan {
    width: u32,
    height: u32,
    scale: u32,
    samples: Vec<PlannedSample>,
}

#[derive(Debug, Clone, Copy)]
struct PlannedSample {
    u: f32,
    v: f32,
    covered: bool,
}

#[derive(Debug, Clone, Copy)]
struct Vertex {
    x: f32,
    y: f32,
    u: f32,
    v: f32,
}

#[derive(Debug, Clone, Copy)]
struct ScreenVertex {
    x: f32,
    y: f32,
    u: f32,
    v: f32,
}

impl CardMesh {
    pub(super) fn bundled() -> Self {
        Self {
            vertices: BUILTIN_VERTICES,
            indices: BUILTIN_INDICES,
        }
    }

    pub(super) fn plan(&self, width: u32, height: u32) -> CardRenderPlan {
        let scale = RENDER_SUPERSAMPLE;
        let high_width = width * scale;
        let high_height = height * scale;
        let high_len = (high_width * high_height) as usize;
        let mut samples = vec![
            PlannedSample {
                u: 0.0,
                v: 0.0,
                covered: false
            };
            high_len
        ];

        let bounds = self.bounds();
        for triangle in self.triangles() {
            let [a, b, c] = triangle.map(|vertex| project(vertex, bounds, high_width, high_height));
            rasterize_triangle(a, b, c, high_width, high_height, &mut samples);
        }
        CardRenderPlan {
            width,
            height,
            scale,
            samples,
        }
    }

    fn bounds(&self) -> Bounds {
        let mut bounds = Bounds {
            min_x: f32::INFINITY,
            max_x: f32::NEG_INFINITY,
            min_y: f32::INFINITY,
            max_y: f32::NEG_INFINITY,
        };
        for vertex in self.vertices {
            bounds.min_x = bounds.min_x.min(vertex.x);
            bounds.max_x = bounds.max_x.max(vertex.x);
            bounds.min_y = bounds.min_y.min(vertex.y);
            bounds.max_y = bounds.max_y.max(vertex.y);
        }
        bounds
    }

    fn triangles(&self) -> impl Iterator<Item = [Vertex; 3]> + '_ {
        self.indices.chunks_exact(3).map(|triangle| {
            [
                self.vertices[triangle[0] as usize],
                self.vertices[triangle[1] as usize],
                self.vertices[triangle[2] as usize],
            ]
        })
    }
}

impl CardRenderPlan {
    pub(super) fn prepare_filter(&self) -> FilterPlan {
        let mut pixels = Vec::with_capacity((self.width * self.height) as usize);
        let mut weights = Vec::new();
        let high_width = self.width * self.scale;
        for y in 0..self.height {
            for x in 0..self.width {
                let mut sources: Vec<(u32, f64)> = Vec::with_capacity(9);
                let mut count = 0;
                for yy in 0..self.scale {
                    for xx in 0..self.scale {
                        let p = self.samples
                            [((y * self.scale + yy) * high_width + x * self.scale + xx) as usize];
                        if !p.covered {
                            continue;
                        }
                        count += 1;
                        let u = p.u.clamp(0., 1.) * (self.width - 1) as f32;
                        let v = (1. - p.v.clamp(0., 1.)) * (self.height - 1) as f32;
                        let x0 = u.floor() as u32;
                        let y0 = v.floor() as u32;
                        let x1 = (x0 + 1).min(self.width - 1);
                        let y1 = (y0 + 1).min(self.height - 1);
                        let tx = (u - x0 as f32) as f64;
                        let ty = (v - y0 as f32) as f64;
                        for (i, w) in [
                            (y0 * self.width + x0, (1. - tx) * (1. - ty)),
                            (y0 * self.width + x1, tx * (1. - ty)),
                            (y1 * self.width + x0, (1. - tx) * ty),
                            (y1 * self.width + x1, tx * ty),
                        ] {
                            if w == 0. {
                                continue;
                            }
                            if let Some(v) = sources.iter_mut().find(|v| v.0 == i) {
                                v.1 += w;
                            } else {
                                sources.push((i, w));
                            }
                        }
                    }
                }
                let start = weights.len();
                if count > 0 {
                    for &(index, weight) in &sources {
                        weights.push(FilterWeight {
                            index: index as usize * 4,
                            weight: (weight / count as f64 * 65536.).round() as u32,
                        });
                    }
                    let sum: i64 = weights[start..].iter().map(|w| w.weight as i64).sum();
                    let largest = weights[start..]
                        .iter()
                        .enumerate()
                        .max_by_key(|(_, w)| w.weight)
                        .unwrap()
                        .0
                        + start;
                    weights[largest].weight = (weights[largest].weight as i64 + 65536 - sum) as u32;
                }
                pixels.push(FilterPixel {
                    start: start as u32,
                    len: sources.len() as u8,
                    alpha: ((count * 255 + self.scale * self.scale / 2) / (self.scale * self.scale))
                        as u8,
                });
            }
        }
        FilterPlan {
            width: self.width,
            height: self.height,
            pixels,
            weights,
        }
    }
    pub(super) fn render(&self, texture: &RgbaImage) -> RgbaImage {
        let mut output = RgbaImage::new(self.width, self.height);
        let high_width = self.width * self.scale;
        let samples_per_pixel = self.scale * self.scale;
        for y in 0..self.height {
            for x in 0..self.width {
                let mut count = 0_u32;
                let mut color = [0_u32; 4];
                for yy in 0..self.scale {
                    for xx in 0..self.scale {
                        let high_x = x * self.scale + xx;
                        let high_y = y * self.scale + yy;
                        let planned = self.samples[(high_y * high_width + high_x) as usize];
                        if !planned.covered {
                            continue;
                        }
                        count += 1;
                        let sample = sample(texture, planned.u, planned.v);
                        for channel in 0..4 {
                            color[channel] += sample[channel] as u32;
                        }
                    }
                }
                let pixel = output.get_pixel_mut(x, y);
                if count == 0 {
                    *pixel = Rgba([0, 0, 0, 0]);
                } else {
                    *pixel = Rgba([
                        ((color[0] + count / 2) / count) as u8,
                        ((color[1] + count / 2) / count) as u8,
                        ((color[2] + count / 2) / count) as u8,
                        ((count * 255 + samples_per_pixel / 2) / samples_per_pixel) as u8,
                    ]);
                }
            }
        }
        output
    }
}

pub(super) struct FilterPlan {
    width: u32,
    height: u32,
    pixels: Vec<FilterPixel>,
    weights: Vec<FilterWeight>,
}
struct FilterPixel {
    start: u32,
    len: u8,
    alpha: u8,
}
struct FilterWeight {
    index: usize,
    weight: u32,
}
impl FilterPlan {
    pub(super) fn render(&self, texture: &RgbaImage) -> RgbaImage {
        let input = texture.as_raw();
        let mut output = vec![0; self.width as usize * self.height as usize * 4];
        for (pixel, out) in self.pixels.iter().zip(output.chunks_exact_mut(4)) {
            let mut color = [32768u32; 3];
            for w in &self.weights[pixel.start as usize..pixel.start as usize + pixel.len as usize]
            {
                color[0] += input[w.index] as u32 * w.weight;
                color[1] += input[w.index + 1] as u32 * w.weight;
                color[2] += input[w.index + 2] as u32 * w.weight;
            }
            out[0] = (color[0] >> 16) as u8;
            out[1] = (color[1] >> 16) as u8;
            out[2] = (color[2] >> 16) as u8;
            out[3] = pixel.alpha;
        }
        RgbaImage::from_raw(self.width, self.height, output).unwrap()
    }
}

#[derive(Debug, Clone, Copy)]
struct Bounds {
    min_x: f32,
    max_x: f32,
    min_y: f32,
    max_y: f32,
}

fn project(vertex: Vertex, bounds: Bounds, width: u32, height: u32) -> ScreenVertex {
    ScreenVertex {
        x: (vertex.x - bounds.min_x) / (bounds.max_x - bounds.min_x) * (width - 1) as f32,
        y: (bounds.max_y - vertex.y) / (bounds.max_y - bounds.min_y) * (height - 1) as f32,
        u: vertex.u,
        v: vertex.v,
    }
}

fn rasterize_triangle(
    a: ScreenVertex,
    b: ScreenVertex,
    c: ScreenVertex,
    width: u32,
    height: u32,
    samples: &mut [PlannedSample],
) {
    let area = edge(a, b, c.x, c.y);
    if area.abs() < f32::EPSILON {
        return;
    }
    let min_x = a.x.min(b.x).min(c.x).floor().max(0.0) as u32;
    let max_x = a.x.max(b.x).max(c.x).ceil().min((width - 1) as f32) as u32;
    let min_y = a.y.min(b.y).min(c.y).floor().max(0.0) as u32;
    let max_y = a.y.max(b.y).max(c.y).ceil().min((height - 1) as f32) as u32;
    for y in min_y..=max_y {
        for x in min_x..=max_x {
            let px = x as f32 + 0.5;
            let py = y as f32 + 0.5;
            let w0 = edge(b, c, px, py) / area;
            let w1 = edge(c, a, px, py) / area;
            let w2 = edge(a, b, px, py) / area;
            if w0 >= -0.00001 && w1 >= -0.00001 && w2 >= -0.00001 {
                let index = (y * width + x) as usize;
                if samples[index].covered {
                    continue;
                }
                samples[index] = PlannedSample {
                    u: w0 * a.u + w1 * b.u + w2 * c.u,
                    v: w0 * a.v + w1 * b.v + w2 * c.v,
                    covered: true,
                };
            }
        }
    }
}

fn edge(a: ScreenVertex, b: ScreenVertex, x: f32, y: f32) -> f32 {
    (x - a.x) * (b.y - a.y) - (y - a.y) * (b.x - a.x)
}

fn sample(texture: &RgbaImage, u: f32, v: f32) -> Rgba<u8> {
    let width = texture.width();
    let height = texture.height();
    let x = u.clamp(0.0, 1.0) * (width - 1) as f32;
    let y = (1.0 - v.clamp(0.0, 1.0)) * (height - 1) as f32;
    let x0 = x.floor() as u32;
    let y0 = y.floor() as u32;
    let x1 = (x0 + 1).min(width - 1);
    let y1 = (y0 + 1).min(height - 1);
    let tx = x - x0 as f32;
    let ty = y - y0 as f32;
    let mut out = [0_u8; 4];
    for channel in 0..4 {
        let top = lerp(
            texture.get_pixel(x0, y0)[channel] as f32,
            texture.get_pixel(x1, y0)[channel] as f32,
            tx,
        );
        let bottom = lerp(
            texture.get_pixel(x0, y1)[channel] as f32,
            texture.get_pixel(x1, y1)[channel] as f32,
            tx,
        );
        out[channel] = lerp(top, bottom, ty).round().clamp(0.0, 255.0) as u8;
    }
    Rgba(out)
}

fn lerp(a: f32, b: f32, t: f32) -> f32 {
    a * (1.0 - t) + b * t
}

const BUILTIN_VERTICES: &[Vertex] = &[
    Vertex {
        x: 0.266_f32,
        y: 0.35444435_f32,
        u: 0.92236328_f32,
        v: 0.90283203_f32,
    },
    Vertex {
        x: 0.266_f32,
        y: 0.02374938_f32,
        u: 0.92236328_f32,
        v: 0.52685547_f32,
    },
    Vertex {
        x: 0.0_f32,
        y: 0.02374938_f32,
        u: 0.5_f32,
        v: 0.52685547_f32,
    },
    Vertex {
        x: 0.0_f32,
        y: 0.35444435_f32,
        u: 0.5_f32,
        v: 0.90283203_f32,
    },
    Vertex {
        x: -0.266_f32,
        y: 0.02374938_f32,
        u: 0.07775879_f32,
        v: 0.52685547_f32,
    },
    Vertex {
        x: -0.266_f32,
        y: 0.35444435_f32,
        u: 0.07775879_f32,
        v: 0.90283203_f32,
    },
    Vertex {
        x: -0.31500003_f32,
        y: -0.30000001_f32,
        u: 0.0_f32,
        v: 0.15905762_f32,
    },
    Vertex {
        x: -0.28000003_f32,
        y: -0.30000001_f32,
        u: 0.05554199_f32,
        v: 0.15905762_f32,
    },
    Vertex {
        x: -0.31500003_f32,
        y: -0.40500003_f32,
        u: 0.0_f32,
        v: 0.0397644_f32,
    },
    Vertex {
        x: -0.31500003_f32,
        y: -0.2_f32,
        u: 0.0_f32,
        v: 0.27270508_f32,
    },
    Vertex {
        x: -0.28000003_f32,
        y: -0.40500003_f32,
        u: 0.05554199_f32,
        v: 0.0397644_f32,
    },
    Vertex {
        x: -0.31500003_f32,
        y: -0.4196938_f32,
        u: 0.0_f32,
        v: 0.02307129_f32,
    },
    Vertex {
        x: -0.31297308_f32,
        y: -0.42540237_f32,
        u: 0.0032177_f32,
        v: 0.0165863_f32,
    },
    Vertex {
        x: -0.30973503_f32,
        y: -0.43030095_f32,
        u: 0.00835419_f32,
        v: 0.01102448_f32,
    },
    Vertex {
        x: -0.3053689_f32,
        y: -0.43488383_f32,
        u: 0.01528931_f32,
        v: 0.0058136_f32,
    },
    Vertex {
        x: -0.29978639_f32,
        y: -0.43810341_f32,
        u: 0.0241394_f32,
        v: 0.0021553_f32,
    },
    Vertex {
        x: -0.29400548_f32,
        y: -0.43995205_f32,
        u: 0.0333252_f32,
        v: 0.00005448_f32,
    },
    Vertex {
        x: -0.28000003_f32,
        y: -0.44000003_f32,
        u: 0.05554199_f32,
        v: 0.0_f32,
    },
    Vertex {
        x: -0.266_f32,
        y: -0.44000003_f32,
        u: 0.07775879_f32,
        v: 0.0_f32,
    },
    Vertex {
        x: -0.266_f32,
        y: -0.40500003_f32,
        u: 0.07775879_f32,
        v: 0.0397644_f32,
    },
    Vertex {
        x: -0.2_f32,
        y: -0.44000003_f32,
        u: 0.18249512_f32,
        v: 0.0_f32,
    },
    Vertex {
        x: -0.28000003_f32,
        y: -0.2_f32,
        u: 0.05554199_f32,
        v: 0.27270508_f32,
    },
    Vertex {
        x: -0.31500003_f32,
        y: -0.1_f32,
        u: 0.0_f32,
        v: 0.38647461_f32,
    },
    Vertex {
        x: -0.2_f32,
        y: -0.40500003_f32,
        u: 0.18249512_f32,
        v: 0.0397644_f32,
    },
    Vertex {
        x: -0.1_f32,
        y: -0.44000003_f32,
        u: 0.34130859_f32,
        v: 0.0_f32,
    },
    Vertex {
        x: -0.266_f32,
        y: -0.2_f32,
        u: 0.07775879_f32,
        v: 0.27270508_f32,
    },
    Vertex {
        x: -0.28000003_f32,
        y: -0.1_f32,
        u: 0.05554199_f32,
        v: 0.38647461_f32,
    },
    Vertex {
        x: -0.31500003_f32,
        y: 0.0_f32,
        u: 0.0_f32,
        v: 0.5_f32,
    },
    Vertex {
        x: -0.1_f32,
        y: -0.40500003_f32,
        u: 0.34130859_f32,
        v: 0.0397644_f32,
    },
    Vertex {
        x: 0.0_f32,
        y: -0.44000003_f32,
        u: 0.5_f32,
        v: 0.0_f32,
    },
    Vertex {
        x: -0.2_f32,
        y: -0.2_f32,
        u: 0.18249512_f32,
        v: 0.27270508_f32,
    },
    Vertex {
        x: -0.28000003_f32,
        y: 0.0_f32,
        u: 0.05554199_f32,
        v: 0.5_f32,
    },
    Vertex {
        x: -0.31500003_f32,
        y: 0.02374938_f32,
        u: 0.0_f32,
        v: 0.52685547_f32,
    },
    Vertex {
        x: -0.266_f32,
        y: 0.0_f32,
        u: 0.07775879_f32,
        v: 0.5_f32,
    },
    Vertex {
        x: -0.28000003_f32,
        y: 0.02374938_f32,
        u: 0.05554199_f32,
        v: 0.52685547_f32,
    },
    Vertex {
        x: -0.31500003_f32,
        y: 0.1_f32,
        u: 0.0_f32,
        v: 0.61376953_f32,
    },
    Vertex {
        x: 0.0_f32,
        y: -0.40500003_f32,
        u: 0.5_f32,
        v: 0.0397644_f32,
    },
    Vertex {
        x: 0.1_f32,
        y: -0.44000003_f32,
        u: 0.65869141_f32,
        v: 0.0_f32,
    },
    Vertex {
        x: 0.0_f32,
        y: -0.2_f32,
        u: 0.5_f32,
        v: 0.27270508_f32,
    },
    Vertex {
        x: 0.1_f32,
        y: -0.40500003_f32,
        u: 0.65869141_f32,
        v: 0.0397644_f32,
    },
    Vertex {
        x: 0.2_f32,
        y: -0.44000003_f32,
        u: 0.81738281_f32,
        v: 0.0_f32,
    },
    Vertex {
        x: -0.2_f32,
        y: 0.0_f32,
        u: 0.18249512_f32,
        v: 0.5_f32,
    },
    Vertex {
        x: -0.28000003_f32,
        y: 0.1_f32,
        u: 0.05554199_f32,
        v: 0.61376953_f32,
    },
    Vertex {
        x: -0.31500003_f32,
        y: 0.2_f32,
        u: 0.0_f32,
        v: 0.72705078_f32,
    },
    Vertex {
        x: -0.266_f32,
        y: 0.02374938_f32,
        u: 0.07775879_f32,
        v: 0.52685547_f32,
    },
    Vertex {
        x: -0.28000003_f32,
        y: 0.2_f32,
        u: 0.05554199_f32,
        v: 0.72705078_f32,
    },
    Vertex {
        x: -0.266_f32,
        y: 0.35444435_f32,
        u: 0.07775879_f32,
        v: 0.90283203_f32,
    },
    Vertex {
        x: -0.31500003_f32,
        y: 0.30000001_f32,
        u: 0.0_f32,
        v: 0.84082031_f32,
    },
    Vertex {
        x: 0.0_f32,
        y: 0.02374938_f32,
        u: 0.5_f32,
        v: 0.52685547_f32,
    },
    Vertex {
        x: -0.28000003_f32,
        y: 0.30000001_f32,
        u: 0.05554199_f32,
        v: 0.84082031_f32,
    },
    Vertex {
        x: -0.31500003_f32,
        y: 0.35444435_f32,
        u: 0.0_f32,
        v: 0.90283203_f32,
    },
    Vertex {
        x: -0.28000003_f32,
        y: 0.35444435_f32,
        u: 0.05554199_f32,
        v: 0.90283203_f32,
    },
    Vertex {
        x: -0.31500003_f32,
        y: 0.40500003_f32,
        u: 0.0_f32,
        v: 0.96044922_f32,
    },
    Vertex {
        x: -0.28000003_f32,
        y: 0.40500003_f32,
        u: 0.05554199_f32,
        v: 0.96044922_f32,
    },
    Vertex {
        x: -0.31500003_f32,
        y: 0.4196938_f32,
        u: 0.0_f32,
        v: 0.97705078_f32,
    },
    Vertex {
        x: -0.31297308_f32,
        y: 0.42540237_f32,
        u: 0.0032177_f32,
        v: 0.98339844_f32,
    },
    Vertex {
        x: -0.30973503_f32,
        y: 0.43030095_f32,
        u: 0.00835419_f32,
        v: 0.98876953_f32,
    },
    Vertex {
        x: -0.3053689_f32,
        y: 0.43488383_f32,
        u: 0.01528931_f32,
        v: 0.99414063_f32,
    },
    Vertex {
        x: -0.29978639_f32,
        y: 0.43810341_f32,
        u: 0.0241394_f32,
        v: 0.99804688_f32,
    },
    Vertex {
        x: -0.29400548_f32,
        y: 0.43995205_f32,
        u: 0.0333252_f32,
        v: 1.0_f32,
    },
    Vertex {
        x: -0.28000003_f32,
        y: 0.44000003_f32,
        u: 0.05554199_f32,
        v: 1.0_f32,
    },
    Vertex {
        x: -0.266_f32,
        y: 0.40500003_f32,
        u: 0.07775879_f32,
        v: 0.96044922_f32,
    },
    Vertex {
        x: -0.266_f32,
        y: 0.44000003_f32,
        u: 0.07775879_f32,
        v: 1.0_f32,
    },
    Vertex {
        x: -0.2_f32,
        y: 0.40500003_f32,
        u: 0.18249512_f32,
        v: 0.96044922_f32,
    },
    Vertex {
        x: -0.2_f32,
        y: 0.44000003_f32,
        u: 0.18249512_f32,
        v: 1.0_f32,
    },
    Vertex {
        x: -0.1_f32,
        y: 0.40500003_f32,
        u: 0.34130859_f32,
        v: 0.96044922_f32,
    },
    Vertex {
        x: -0.1_f32,
        y: 0.44000003_f32,
        u: 0.34130859_f32,
        v: 1.0_f32,
    },
    Vertex {
        x: 0.0_f32,
        y: 0.35444435_f32,
        u: 0.5_f32,
        v: 0.90283203_f32,
    },
    Vertex {
        x: 0.0_f32,
        y: 0.40500003_f32,
        u: 0.5_f32,
        v: 0.96044922_f32,
    },
    Vertex {
        x: 0.0_f32,
        y: 0.44000003_f32,
        u: 0.5_f32,
        v: 1.0_f32,
    },
    Vertex {
        x: 0.1_f32,
        y: 0.40500003_f32,
        u: 0.65869141_f32,
        v: 0.96044922_f32,
    },
    Vertex {
        x: 0.1_f32,
        y: 0.44000003_f32,
        u: 0.65869141_f32,
        v: 1.0_f32,
    },
    Vertex {
        x: 0.266_f32,
        y: 0.35444435_f32,
        u: 0.92236328_f32,
        v: 0.90283203_f32,
    },
    Vertex {
        x: 0.2_f32,
        y: 0.40500003_f32,
        u: 0.81738281_f32,
        v: 0.96044922_f32,
    },
    Vertex {
        x: 0.2_f32,
        y: 0.44000003_f32,
        u: 0.81738281_f32,
        v: 1.0_f32,
    },
    Vertex {
        x: 0.266_f32,
        y: 0.40500003_f32,
        u: 0.92236328_f32,
        v: 0.96044922_f32,
    },
    Vertex {
        x: 0.266_f32,
        y: 0.44000003_f32,
        u: 0.92236328_f32,
        v: 1.0_f32,
    },
    Vertex {
        x: 0.28000003_f32,
        y: 0.40500003_f32,
        u: 0.94433594_f32,
        v: 0.96044922_f32,
    },
    Vertex {
        x: 0.28000003_f32,
        y: 0.44000003_f32,
        u: 0.94433594_f32,
        v: 1.0_f32,
    },
    Vertex {
        x: 0.29400548_f32,
        y: 0.43995205_f32,
        u: 0.96679688_f32,
        v: 1.0_f32,
    },
    Vertex {
        x: 0.29978639_f32,
        y: 0.43810341_f32,
        u: 0.97558594_f32,
        v: 0.99804688_f32,
    },
    Vertex {
        x: 0.3053689_f32,
        y: 0.43488383_f32,
        u: 0.984375_f32,
        v: 0.99414063_f32,
    },
    Vertex {
        x: 0.30973503_f32,
        y: 0.43030095_f32,
        u: 0.99169922_f32,
        v: 0.98876953_f32,
    },
    Vertex {
        x: 0.31297308_f32,
        y: 0.42540237_f32,
        u: 0.99658203_f32,
        v: 0.98339844_f32,
    },
    Vertex {
        x: 0.31500003_f32,
        y: 0.4196938_f32,
        u: 1.0_f32,
        v: 0.97705078_f32,
    },
    Vertex {
        x: 0.31500003_f32,
        y: 0.40500003_f32,
        u: 1.0_f32,
        v: 0.96044922_f32,
    },
    Vertex {
        x: 0.31500003_f32,
        y: 0.35444435_f32,
        u: 1.0_f32,
        v: 0.90283203_f32,
    },
    Vertex {
        x: 0.28000003_f32,
        y: 0.35444435_f32,
        u: 0.94433594_f32,
        v: 0.90283203_f32,
    },
    Vertex {
        x: 0.31500003_f32,
        y: 0.30000001_f32,
        u: 1.0_f32,
        v: 0.84082031_f32,
    },
    Vertex {
        x: 0.28000003_f32,
        y: 0.30000001_f32,
        u: 0.94433594_f32,
        v: 0.84082031_f32,
    },
    Vertex {
        x: 0.31500003_f32,
        y: 0.2_f32,
        u: 1.0_f32,
        v: 0.72705078_f32,
    },
    Vertex {
        x: 0.28000003_f32,
        y: 0.2_f32,
        u: 0.94433594_f32,
        v: 0.72705078_f32,
    },
    Vertex {
        x: 0.31500003_f32,
        y: 0.1_f32,
        u: 1.0_f32,
        v: 0.61376953_f32,
    },
    Vertex {
        x: 0.266_f32,
        y: 0.02374938_f32,
        u: 0.92236328_f32,
        v: 0.52685547_f32,
    },
    Vertex {
        x: 0.28000003_f32,
        y: 0.1_f32,
        u: 0.94433594_f32,
        v: 0.61376953_f32,
    },
    Vertex {
        x: 0.31500003_f32,
        y: 0.02374938_f32,
        u: 1.0_f32,
        v: 0.52685547_f32,
    },
    Vertex {
        x: 0.28000003_f32,
        y: 0.02374938_f32,
        u: 0.94433594_f32,
        v: 0.52685547_f32,
    },
    Vertex {
        x: 0.31500003_f32,
        y: 0.0_f32,
        u: 1.0_f32,
        v: 0.5_f32,
    },
    Vertex {
        x: 0.28000003_f32,
        y: 0.0_f32,
        u: 0.94433594_f32,
        v: 0.5_f32,
    },
    Vertex {
        x: 0.31500003_f32,
        y: -0.1_f32,
        u: 1.0_f32,
        v: 0.38647461_f32,
    },
    Vertex {
        x: 0.28000003_f32,
        y: -0.1_f32,
        u: 0.94433594_f32,
        v: 0.38647461_f32,
    },
    Vertex {
        x: 0.31500003_f32,
        y: -0.2_f32,
        u: 1.0_f32,
        v: 0.27270508_f32,
    },
    Vertex {
        x: 0.266_f32,
        y: 0.0_f32,
        u: 0.92236328_f32,
        v: 0.5_f32,
    },
    Vertex {
        x: 0.28000003_f32,
        y: -0.2_f32,
        u: 0.94433594_f32,
        v: 0.27270508_f32,
    },
    Vertex {
        x: 0.31500003_f32,
        y: -0.30000001_f32,
        u: 1.0_f32,
        v: 0.15905762_f32,
    },
    Vertex {
        x: 0.2_f32,
        y: 0.0_f32,
        u: 0.81738281_f32,
        v: 0.5_f32,
    },
    Vertex {
        x: 0.266_f32,
        y: -0.2_f32,
        u: 0.92236328_f32,
        v: 0.27270508_f32,
    },
    Vertex {
        x: 0.28000003_f32,
        y: -0.30000001_f32,
        u: 0.94433594_f32,
        v: 0.15905762_f32,
    },
    Vertex {
        x: 0.31500003_f32,
        y: -0.40500003_f32,
        u: 1.0_f32,
        v: 0.0397644_f32,
    },
    Vertex {
        x: 0.28000003_f32,
        y: -0.40500003_f32,
        u: 0.94433594_f32,
        v: 0.0397644_f32,
    },
    Vertex {
        x: 0.31507012_f32,
        y: -0.4196938_f32,
        u: 1.0_f32,
        v: 0.02307129_f32,
    },
    Vertex {
        x: 0.31297308_f32,
        y: -0.42540237_f32,
        u: 0.99658203_f32,
        v: 0.0165863_f32,
    },
    Vertex {
        x: 0.30973503_f32,
        y: -0.43030095_f32,
        u: 0.99169922_f32,
        v: 0.01102448_f32,
    },
    Vertex {
        x: 0.3053689_f32,
        y: -0.43488383_f32,
        u: 0.984375_f32,
        v: 0.0058136_f32,
    },
    Vertex {
        x: 0.29978639_f32,
        y: -0.43810341_f32,
        u: 0.97558594_f32,
        v: 0.0021553_f32,
    },
    Vertex {
        x: 0.29400548_f32,
        y: -0.43995205_f32,
        u: 0.96679688_f32,
        v: 0.00005448_f32,
    },
    Vertex {
        x: 0.28000003_f32,
        y: -0.44000003_f32,
        u: 0.94433594_f32,
        v: 0.0_f32,
    },
    Vertex {
        x: 0.266_f32,
        y: -0.40500003_f32,
        u: 0.92236328_f32,
        v: 0.0397644_f32,
    },
    Vertex {
        x: 0.266_f32,
        y: -0.44000003_f32,
        u: 0.92236328_f32,
        v: 0.0_f32,
    },
    Vertex {
        x: 0.2_f32,
        y: -0.2_f32,
        u: 0.81738281_f32,
        v: 0.27270508_f32,
    },
    Vertex {
        x: 0.2_f32,
        y: -0.40500003_f32,
        u: 0.81738281_f32,
        v: 0.0397644_f32,
    },
    Vertex {
        x: 0.0_f32,
        y: 0.0_f32,
        u: 0.5_f32,
        v: 0.5_f32,
    },
];

const BUILTIN_INDICES: &[u16] = &[
    0, 1, 2, 3, 0, 2, 3, 2, 4, 5, 3, 4, 6, 7, 8, 6, 9, 7, 8, 7, 10, 10, 11, 8, 12, 11, 10, 10, 13,
    12, 13, 10, 14, 10, 15, 14, 15, 10, 16, 10, 17, 16, 17, 10, 18, 10, 19, 18, 10, 7, 19, 20, 18,
    19, 9, 21, 7, 21, 9, 22, 23, 20, 19, 24, 20, 23, 19, 7, 25, 7, 21, 25, 23, 19, 25, 26, 21, 22,
    25, 21, 26, 22, 27, 26, 28, 24, 23, 29, 24, 28, 30, 23, 25, 28, 23, 30, 27, 31, 26, 27, 32, 31,
    33, 25, 26, 26, 31, 33, 30, 25, 33, 32, 34, 31, 33, 31, 34, 34, 32, 35, 36, 29, 28, 37, 29, 36,
    28, 30, 38, 36, 28, 38, 39, 37, 36, 36, 38, 39, 40, 37, 39, 41, 30, 33, 30, 41, 38, 42, 34, 35,
    35, 43, 42, 44, 33, 34, 34, 42, 44, 33, 44, 41, 43, 45, 42, 42, 45, 44, 45, 46, 44, 45, 43, 47,
    48, 41, 44, 49, 45, 47, 45, 49, 46, 47, 50, 49, 50, 51, 49, 46, 49, 51, 50, 52, 51, 52, 53, 51,
    51, 53, 46, 54, 53, 52, 55, 53, 54, 53, 55, 56, 56, 57, 53, 53, 57, 58, 58, 59, 53, 59, 60, 53,
    61, 53, 60, 53, 61, 46, 62, 61, 60, 61, 62, 63, 61, 63, 46, 62, 64, 63, 63, 65, 46, 63, 64, 65,
    64, 66, 65, 65, 67, 46, 65, 66, 68, 65, 68, 67, 66, 69, 68, 68, 70, 67, 68, 69, 70, 69, 71, 70,
    67, 70, 72, 70, 71, 73, 70, 73, 72, 71, 74, 73, 73, 74, 75, 72, 73, 75, 74, 76, 75, 77, 75, 76,
    78, 77, 76, 77, 78, 79, 80, 77, 79, 77, 80, 81, 82, 77, 81, 77, 82, 83, 83, 84, 77, 77, 84, 85,
    85, 86, 77, 75, 77, 87, 86, 87, 77, 72, 75, 87, 87, 86, 88, 89, 87, 88, 87, 89, 72, 88, 90, 89,
    89, 91, 72, 90, 91, 89, 91, 90, 92, 91, 93, 72, 94, 91, 92, 91, 94, 93, 92, 95, 94, 95, 96, 94,
    94, 96, 93, 95, 97, 96, 97, 98, 96, 96, 98, 93, 98, 97, 99, 100, 98, 99, 99, 101, 100, 98, 102,
    93, 98, 100, 102, 101, 103, 100, 103, 101, 104, 102, 105, 93, 93, 105, 48, 106, 102, 100, 103,
    106, 100, 105, 102, 106, 107, 103, 104, 103, 107, 106, 104, 108, 107, 108, 109, 107, 110, 109,
    108, 111, 109, 110, 109, 111, 112, 112, 113, 109, 109, 113, 114, 114, 115, 109, 115, 116, 109,
    109, 117, 107, 117, 109, 116, 117, 106, 107, 118, 117, 116, 106, 117, 119, 119, 105, 106, 117,
    118, 120, 117, 120, 119, 118, 40, 120, 120, 40, 39, 39, 119, 120, 38, 119, 39, 121, 105, 119,
    38, 121, 119, 105, 121, 48, 41, 121, 38, 121, 41, 48,
];
