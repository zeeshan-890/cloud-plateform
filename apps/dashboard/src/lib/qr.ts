/**
 * Minimal self-contained QR Code encoder (byte mode, error correction level M,
 * versions 1-10 = up to 213 bytes). Enough for otpauth:// URIs. No dependencies.
 */

// [ecc codewords per block, blocks in group 1, data cw per block g1, blocks in group 2, data cw per block g2]
const M_TABLE: Record<number, [number, number, number, number, number]> = {
  1: [10, 1, 16, 0, 0],
  2: [16, 1, 28, 0, 0],
  3: [26, 1, 44, 0, 0],
  4: [18, 2, 32, 0, 0],
  5: [24, 2, 43, 0, 0],
  6: [16, 4, 27, 0, 0],
  7: [18, 4, 31, 0, 0],
  8: [22, 2, 38, 2, 39],
  9: [22, 3, 36, 2, 37],
  10: [26, 4, 43, 1, 44],
};

const ALIGN: Record<number, number[]> = {
  1: [],
  2: [6, 18],
  3: [6, 22],
  4: [6, 26],
  5: [6, 30],
  6: [6, 34],
  7: [6, 22, 38],
  8: [6, 24, 42],
  9: [6, 26, 46],
  10: [6, 28, 50],
};

const MAX_VERSION = 10;

// GF(256) tables, primitive polynomial 0x11d
const EXP = new Uint8Array(512);
const LOG = new Uint8Array(256);
(() => {
  let x = 1;
  for (let i = 0; i < 255; i++) {
    EXP[i] = x;
    LOG[x] = i;
    x <<= 1;
    if (x & 0x100) x ^= 0x11d;
  }
  for (let i = 255; i < 512; i++) EXP[i] = EXP[i - 255];
})();

function gfMul(a: number, b: number): number {
  return a === 0 || b === 0 ? 0 : EXP[LOG[a] + LOG[b]];
}

function rsGenerator(degree: number): number[] {
  let poly = [1];
  for (let i = 0; i < degree; i++) {
    const next = new Array<number>(poly.length + 1).fill(0);
    for (let j = 0; j < poly.length; j++) {
      next[j] ^= poly[j];
      next[j + 1] ^= gfMul(poly[j], EXP[i]);
    }
    poly = next;
  }
  return poly; // highest degree first, leading 1
}

function rsRemainder(data: number[], degree: number): number[] {
  const gen = rsGenerator(degree);
  const rem = new Array<number>(degree).fill(0);
  for (const b of data) {
    const factor = b ^ rem.shift()!;
    rem.push(0);
    for (let i = 0; i < degree; i++) rem[i] ^= gfMul(gen[i + 1], factor);
  }
  return rem;
}

function dataCapacity(version: number): number {
  const [, b1, d1, b2, d2] = M_TABLE[version];
  return b1 * d1 + b2 * d2;
}

function utf8(text: string): number[] {
  return Array.from(new TextEncoder().encode(text));
}

function buildCodewords(bytes: number[], version: number): number[] {
  const bits: number[] = [];
  const push = (val: number, len: number) => {
    for (let i = len - 1; i >= 0; i--) bits.push((val >>> i) & 1);
  };
  push(0b0100, 4);
  push(bytes.length, version >= 10 ? 16 : 8);
  bytes.forEach((b) => push(b, 8));
  const cap = dataCapacity(version) * 8;
  push(0, Math.min(4, cap - bits.length));
  while (bits.length % 8) bits.push(0);
  const data: number[] = [];
  for (let i = 0; i < bits.length; i += 8) data.push(parseInt(bits.slice(i, i + 8).join(""), 2));
  for (let pad = 0xec; data.length < dataCapacity(version); pad ^= 0xec ^ 0x11) data.push(pad);

  const [ecLen, b1, d1, b2, d2] = M_TABLE[version];
  const blocks: number[][] = [];
  let off = 0;
  const sizes = [...Array(b1).fill(d1), ...Array(b2).fill(d2)] as number[];
  for (const s of sizes) {
    const chunk = data.slice(off, off + s);
    off += s;
    blocks.push([...chunk, ...rsRemainder(chunk, ecLen)]);
  }
  const out: number[] = [];
  const maxLen = Math.max(d1, d2 || 0);
  for (let i = 0; i < maxLen; i++) sizes.forEach((s, j) => i < s && out.push(blocks[j][i]));
  for (let i = 0; i < ecLen; i++) blocks.forEach((blk, j) => out.push(blk[sizes[j] + i]));
  return out;
}

function maskFn(mask: number): (x: number, y: number) => boolean {
  switch (mask) {
    case 0: return (x, y) => (x + y) % 2 === 0;
    case 1: return (_x, y) => y % 2 === 0;
    case 2: return (x) => x % 3 === 0;
    case 3: return (x, y) => (x + y) % 3 === 0;
    case 4: return (x, y) => (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0;
    case 5: return (x, y) => ((x * y) % 2) + ((x * y) % 3) === 0;
    case 6: return (x, y) => (((x * y) % 2) + ((x * y) % 3)) % 2 === 0;
    default: return (x, y) => (((x + y) % 2) + ((x * y) % 3)) % 2 === 0;
  }
}

function penalty(m: boolean[][]): number {
  const n = m.length;
  let score = 0;
  // rule 1: runs of 5+ in rows and columns
  for (let dir = 0; dir < 2; dir++) {
    for (let a = 0; a < n; a++) {
      let run = 1;
      for (let b = 1; b < n; b++) {
        const cur = dir === 0 ? m[a][b] : m[b][a];
        const prev = dir === 0 ? m[a][b - 1] : m[b - 1][a];
        if (cur === prev) {
          run++;
          if (run === 5) score += 3;
          else if (run > 5) score++;
        } else run = 1;
      }
    }
  }
  // rule 2: 2x2 blocks
  for (let y = 0; y < n - 1; y++)
    for (let x = 0; x < n - 1; x++) if (m[y][x] === m[y][x + 1] && m[y][x] === m[y + 1][x] && m[y][x] === m[y + 1][x + 1]) score += 3;
  // rule 3: finder-like 1:1:3:1:1 patterns
  const pat = [true, false, true, true, true, false, true, false, false, false, false];
  const rev = [...pat].reverse();
  const match = (get: (i: number) => boolean, p: boolean[]) => p.every((v, i) => get(i) === v);
  for (let a = 0; a < n; a++)
    for (let b = 0; b <= n - 11; b++) {
      if (match((i) => m[a][b + i], pat) || match((i) => m[a][b + i], rev)) score += 40;
      if (match((i) => m[b + i][a], pat) || match((i) => m[b + i][a], rev)) score += 40;
    }
  // rule 4: dark/light balance
  let dark = 0;
  m.forEach((row) => row.forEach((v) => v && dark++));
  const pct = (dark * 100) / (n * n);
  score += Math.floor(Math.abs(pct - 50) / 5) * 10;
  return score;
}

export function encodeQr(text: string): boolean[][] {
  const bytes = utf8(text);
  let version = 1;
  while (version <= MAX_VERSION && dataCapacity(version) - (version >= 10 ? 3 : 2) < bytes.length) version++;
  if (version > MAX_VERSION) throw new Error("Text too long for the built-in QR encoder");

  const size = version * 4 + 17;
  const modules: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  const isFn: boolean[][] = Array.from({ length: size }, () => new Array<boolean>(size).fill(false));
  const setFn = (x: number, y: number, v: boolean) => {
    if (x < 0 || y < 0 || x >= size || y >= size) return;
    modules[y][x] = v;
    isFn[y][x] = true;
  };

  for (let i = 0; i < size; i++) {
    setFn(6, i, i % 2 === 0);
    setFn(i, 6, i % 2 === 0);
  }
  const finder = (cx: number, cy: number) => {
    for (let dy = -4; dy <= 4; dy++)
      for (let dx = -4; dx <= 4; dx++) {
        const d = Math.max(Math.abs(dx), Math.abs(dy));
        setFn(cx + dx, cy + dy, d !== 2 && d !== 4);
      }
  };
  finder(3, 3);
  finder(size - 4, 3);
  finder(3, size - 4);
  const pos = ALIGN[version];
  pos.forEach((cx, i) =>
    pos.forEach((cy, j) => {
      if ((i === 0 && j === 0) || (i === 0 && j === pos.length - 1) || (i === pos.length - 1 && j === 0)) return;
      for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) setFn(cx + dx, cy + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
    }),
  );

  const drawFormat = (mask: number) => {
    const data = (0b00 << 3) | mask; // level M
    let rem = data;
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    const bits = ((data << 10) | rem) ^ 0x5412;
    const bit = (i: number) => ((bits >>> i) & 1) === 1;
    for (let i = 0; i <= 5; i++) setFn(8, i, bit(i));
    setFn(8, 7, bit(6));
    setFn(8, 8, bit(7));
    setFn(7, 8, bit(8));
    for (let i = 9; i < 15; i++) setFn(14 - i, 8, bit(i));
    for (let i = 0; i < 8; i++) setFn(size - 1 - i, 8, bit(i));
    for (let i = 8; i < 15; i++) setFn(8, size - 15 + i, bit(i));
    setFn(8, size - 8, true);
  };
  drawFormat(0);
  if (version >= 7) {
    let rem = version;
    for (let i = 0; i < 12; i++) rem = (rem << 1) ^ ((rem >>> 11) * 0x1f25);
    const bits = (version << 12) | rem;
    for (let i = 0; i < 18; i++) {
      const v = ((bits >>> i) & 1) === 1;
      const a = size - 11 + (i % 3);
      const b = Math.floor(i / 3);
      setFn(a, b, v);
      setFn(b, a, v);
    }
  }

  const codewords = buildCodewords(bytes, version);
  let bitIdx = 0;
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5;
    for (let vert = 0; vert < size; vert++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j;
        const upward = ((right + 1) & 2) === 0;
        const y = upward ? size - 1 - vert : vert;
        if (!isFn[y][x] && bitIdx < codewords.length * 8) {
          modules[y][x] = ((codewords[bitIdx >>> 3] >>> (7 - (bitIdx & 7))) & 1) === 1;
          bitIdx++;
        }
      }
    }
  }

  let best: boolean[][] | null = null;
  let bestScore = Infinity;
  for (let mask = 0; mask < 8; mask++) {
    const fn = maskFn(mask);
    const trial = modules.map((row) => row.slice());
    for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) if (!isFn[y][x] && fn(x, y)) trial[y][x] = !trial[y][x];
    // draw the format bits for this mask on the trial copy
    const data = (0b00 << 3) | mask;
    let rem = data;
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    const bits = ((data << 10) | rem) ^ 0x5412;
    const bit = (i: number) => ((bits >>> i) & 1) === 1;
    const set = (x: number, y: number, v: boolean) => {
      trial[y][x] = v;
    };
    for (let i = 0; i <= 5; i++) set(8, i, bit(i));
    set(8, 7, bit(6));
    set(8, 8, bit(7));
    set(7, 8, bit(8));
    for (let i = 9; i < 15; i++) set(14 - i, 8, bit(i));
    for (let i = 0; i < 8; i++) set(size - 1 - i, 8, bit(i));
    for (let i = 8; i < 15; i++) set(8, size - 15 + i, bit(i));
    set(8, size - 8, true);
    const s = penalty(trial);
    if (s < bestScore) {
      bestScore = s;
      best = trial;
    }
  }
  return best!;
}
