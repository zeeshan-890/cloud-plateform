import jsQR from "jsqr";
import { describe, expect, it } from "vitest";
import { encodeQr } from "./qr";

function decode(matrix: boolean[][]): string | null {
  const scale = 6;
  const quiet = 4;
  const n = matrix.length;
  const size = (n + quiet * 2) * scale;
  const data = new Uint8ClampedArray(size * size * 4).fill(255);
  for (let y = 0; y < n; y++)
    for (let x = 0; x < n; x++) {
      if (!matrix[y][x]) continue;
      for (let dy = 0; dy < scale; dy++)
        for (let dx = 0; dx < scale; dx++) {
          const i = (((y + quiet) * scale + dy) * size + (x + quiet) * scale + dx) * 4;
          data[i] = data[i + 1] = data[i + 2] = 0;
        }
    }
  return jsQR(data, size, size)?.data ?? null;
}

describe("QR encoder", () => {
  it("round-trips short text", () => {
    expect(decode(encodeQr("hello"))).toBe("hello");
  });

  it("round-trips an otpauth URI of realistic length", () => {
    const uri = "otpauth://totp/JP%20Cloud:ada%40example.com?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=JP+Cloud&algorithm=SHA1&digits=6&period=30";
    expect(decode(encodeQr(uri))).toBe(uri);
  });

  it("covers every version we support (1 to 10)", () => {
    for (const len of [5, 20, 40, 60, 80, 100, 120, 150, 175, 200]) {
      const text = Array.from({ length: len }, (_, i) => String.fromCharCode(97 + ((i * 7) % 26))).join("");
      expect(decode(encodeQr(text)), `length ${len}`).toBe(text);
    }
  });

  it("handles non-ASCII text", () => {
    expect(decode(encodeQr("zażółć gęślą"))).toBe("zażółć gęślą");
  });

  it("refuses text beyond capacity instead of drawing a broken code", () => {
    expect(() => encodeQr("x".repeat(400))).toThrow();
  });
});
