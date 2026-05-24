const fs = require("fs");
const path = require("path");
const zlib = require("zlib");

const root = path.resolve(__dirname, "..");
const outBrand = path.join(root, "assets", "brand");
const outBuild = path.join(root, "cmd", "client-windows", "build");
const outWindows = path.join(outBuild, "windows");
for (const dir of [outBrand, outBuild, outWindows]) fs.mkdirSync(dir, { recursive: true });

function crc32(buf) {
  let c = ~0;
  for (let i = 0; i < buf.length; i++) {
    c ^= buf[i];
    for (let k = 0; k < 8; k++) c = (c >>> 1) ^ (0xedb88320 & -(c & 1));
  }
  return ~c >>> 0;
}

function chunk(type, data) {
  const t = Buffer.from(type);
  const len = Buffer.alloc(4);
  len.writeUInt32BE(data.length, 0);
  const crc = Buffer.alloc(4);
  crc.writeUInt32BE(crc32(Buffer.concat([t, data])), 0);
  return Buffer.concat([len, t, data, crc]);
}

function writePNG(file, size) {
  const width = size;
  const height = size;
  const raw = Buffer.alloc((width * 4 + 1) * height);
  for (let y = 0; y < height; y++) {
    const row = y * (width * 4 + 1);
    raw[row] = 0;
    for (let x = 0; x < width; x++) {
      const p = row + 1 + x * 4;
      const nx = (x + 0.5) / width;
      const ny = (y + 0.5) / height;
      const d = roundedRectAlpha(nx, ny, 0.07, 0.07, 0.86, 0.86, 0.21);
      const bg = gradient(nx, ny);
      let r = bg[0], g = bg[1], b = bg[2], a = Math.round(255 * d);

      const ring1 = arc(nx, ny, 0.50, 0.45, 0.34, 0.047, 204, 251, 230, -0.95, -2.19);
      const ring2 = arc(nx, ny, 0.50, 0.45, 0.21, 0.043, 246, 211, 101, -0.90, -2.24);
      [r, g, b, a] = over(r, g, b, a, ring1);
      [r, g, b, a] = over(r, g, b, a, ring2);

      const orb = circle(nx, ny, 0.50, 0.44, 0.057, 255, 245, 184, 1);
      [r, g, b, a] = over(r, g, b, a, orb);
      const orbCore = circle(nx, ny, 0.50, 0.44, 0.024, 23, 107, 135, 1);
      [r, g, b, a] = over(r, g, b, a, orbCore);

      const screenOuter = roundedRect(nx, ny, 0.24, 0.48, 0.52, 0.32, 0.055, 13, 23, 38, 1);
      [r, g, b, a] = over(r, g, b, a, screenOuter);
      const screenInner = roundedRect(nx, ny, 0.28, 0.52, 0.44, 0.215, 0.033, 232, 246, 255, 1);
      [r, g, b, a] = over(r, g, b, a, screenInner);
      const line1 = pill(nx, ny, 0.36, 0.63, 0.28, 0.028, 23, 107, 135, 0.72);
      const line2 = pill(nx, ny, 0.41, 0.685, 0.18, 0.023, 15, 118, 110, 0.52);
      [r, g, b, a] = over(r, g, b, a, line1);
      [r, g, b, a] = over(r, g, b, a, line2);
      const stand = pill(nx, ny, 0.43, 0.84, 0.24, 0.04, 13, 23, 38, 1);
      const stem = roundedRect(nx, ny, 0.47, 0.78, 0.12, 0.085, 0.015, 13, 23, 38, 1);
      [r, g, b, a] = over(r, g, b, a, stem);
      [r, g, b, a] = over(r, g, b, a, stand);

      raw[p] = clamp(r);
      raw[p + 1] = clamp(g);
      raw[p + 2] = clamp(b);
      raw[p + 3] = clamp(a);
    }
  }
  const signature = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(width, 0);
  ihdr.writeUInt32BE(height, 4);
  ihdr[8] = 8;
  ihdr[9] = 6;
  const png = Buffer.concat([
    signature,
    chunk("IHDR", ihdr),
    chunk("IDAT", zlib.deflateSync(raw, { level: 9 })),
    chunk("IEND", Buffer.alloc(0)),
  ]);
  fs.writeFileSync(file, png);
  return png;
}

function writeICO(file, images) {
  const header = Buffer.alloc(6);
  header.writeUInt16LE(0, 0);
  header.writeUInt16LE(1, 2);
  header.writeUInt16LE(images.length, 4);
  const entries = [];
  let offset = 6 + images.length * 16;
  for (const img of images) {
    const entry = Buffer.alloc(16);
    entry[0] = img.size >= 256 ? 0 : img.size;
    entry[1] = img.size >= 256 ? 0 : img.size;
    entry[2] = 0;
    entry[3] = 0;
    entry.writeUInt16LE(1, 4);
    entry.writeUInt16LE(32, 6);
    entry.writeUInt32LE(img.data.length, 8);
    entry.writeUInt32LE(offset, 12);
    offset += img.data.length;
    entries.push(entry);
  }
  fs.writeFileSync(file, Buffer.concat([header, ...entries, ...images.map((img) => img.data)]));
}

function gradient(x, y) {
  const t = Math.max(0, Math.min(1, (x + y) / 2));
  const a = [15, 118, 110], b = [23, 107, 135], c = [41, 51, 92];
  return t < 0.52 ? mix(a, b, t / 0.52) : mix(b, c, (t - 0.52) / 0.48);
}

function mix(a, b, t) { return a.map((v, i) => v + (b[i] - v) * t); }
function clamp(v) { return Math.max(0, Math.min(255, Math.round(v))); }
function smoothstep(e0, e1, x) { const t = Math.max(0, Math.min(1, (x - e0) / (e1 - e0))); return t * t * (3 - 2 * t); }
function roundedRectAlpha(x, y, rx, ry, rw, rh, rr) {
  const cx = Math.max(rx + rr, Math.min(x, rx + rw - rr));
  const cy = Math.max(ry + rr, Math.min(y, ry + rh - rr));
  const dist = Math.hypot(x - cx, y - cy);
  return 1 - smoothstep(rr - 0.004, rr + 0.004, dist);
}
function roundedRect(x, y, rx, ry, rw, rh, rr, r, g, b, a) {
  return [r, g, b, a * roundedRectAlpha(x, y, rx, ry, rw, rh, rr)];
}
function circle(x, y, cx, cy, radius, r, g, b, a) {
  return [r, g, b, a * (1 - smoothstep(radius - 0.004, radius + 0.004, Math.hypot(x - cx, y - cy)))];
}
function pill(x, y, rx, cy, rw, rh, r, g, b, a) {
  return roundedRect(x, y, rx, cy - rh / 2, rw, rh, rh / 2, r, g, b, a);
}
function arc(x, y, cx, cy, radius, thick, r, g, b, start, end) {
  const dx = x - cx, dy = y - cy;
  const d = Math.hypot(dx, dy);
  const angle = Math.atan2(dy, dx);
  const withinAngle = angle < start && angle > end ? 1 : 0;
  const alpha = withinAngle * (1 - smoothstep(thick * 0.5, thick * 0.5 + 0.006, Math.abs(d - radius)));
  return [r, g, b, alpha];
}
function over(br, bg, bb, ba, top) {
  const [tr, tg, tb, ta0] = top;
  const ta = Math.max(0, Math.min(1, ta0));
  const baseA = ba / 255;
  const outA = ta + baseA * (1 - ta);
  if (outA <= 0) return [0, 0, 0, 0];
  return [
    (tr * ta + br * baseA * (1 - ta)) / outA,
    (tg * ta + bg * baseA * (1 - ta)) / outA,
    (tb * ta + bb * baseA * (1 - ta)) / outA,
    outA * 255,
  ];
}

const sizes = [16, 32, 48, 64, 128, 256, 512];
const images = sizes.map((size) => ({
  size,
  data: writePNG(path.join(outBrand, `beacondesk-icon-${size}.png`), size),
}));
const appicon = writePNG(path.join(outBuild, "appicon.png"), 1024);
fs.writeFileSync(path.join(outBrand, "beacondesk-icon.png"), appicon);
writeICO(path.join(outWindows, "icon.ico"), images.filter((img) => [16, 32, 48, 64, 128, 256].includes(img.size)));
console.log("Generated BeaconDesk icons.");
