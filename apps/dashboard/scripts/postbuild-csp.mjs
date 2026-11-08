// Adds a strict per-page Content-Security-Policy <meta> to every exported HTML file.
// Static export cannot use per-request nonces, so every inline <script> is allow-listed by SHA-256 hash.
import { createHash } from "node:crypto";
import { readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const OUT = new URL("../out/", import.meta.url).pathname.replace(/^\/([A-Za-z]:)/, "$1");

function apiOrigin() {
  const u = process.env.NEXT_PUBLIC_API_URL || "";
  try {
    const o = new URL(u);
    return o.origin;
  } catch {
    return "";
  }
}

function walk(dir) {
  return readdirSync(dir).flatMap((n) => {
    const p = join(dir, n);
    return statSync(p).isDirectory() ? walk(p) : p.endsWith(".html") ? [p] : [];
  });
}

const SCRIPT = /<script(?![^>]*\ssrc=)([^>]*)>([\s\S]*?)<\/script>/g;
let pages = 0;
for (const file of walk(OUT)) {
  let html = readFileSync(file, "utf8");
  if (html.includes('http-equiv="Content-Security-Policy"')) continue;
  const hashes = new Set();
  for (const m of html.matchAll(SCRIPT)) {
    if (m[1].includes("application/json") || m[1].includes("application/ld+json")) continue;
    if (m[2].trim() === "") continue;
    hashes.add(`'sha256-${createHash("sha256").update(m[2], "utf8").digest("base64")}'`);
  }
  const connect = ["'self'", apiOrigin()].filter(Boolean).join(" ");
  const csp = [
    "default-src 'self'",
    `script-src 'self' ${[...hashes].join(" ")}`.trim(),
    "style-src 'self'",
    "style-src-attr 'unsafe-inline'",
    "img-src 'self' data:",
    "font-src 'self'",
    `connect-src ${connect}`,
    "object-src 'none'",
    "base-uri 'self'",
    "form-action 'self'",
  ].join("; ");
  html = html.replace(/<head>/, `<head><meta http-equiv="Content-Security-Policy" content="${csp}"/>`);
  writeFileSync(file, html);
  pages++;
}
console.log(`CSP injected into ${pages} pages`);
