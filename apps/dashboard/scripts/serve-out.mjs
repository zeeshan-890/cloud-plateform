// Tiny static server for the exported console (local preview and Playwright runs).
// Mirrors nginx: real files first, then <path>.html, then <path>/index.html, then 404.html.
import { createServer } from "node:http";
import { existsSync, readFileSync, statSync } from "node:fs";
import { extname, join, normalize } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../out/", import.meta.url));
const port = Number(process.env.PORT || 3000);
const types = { ".html": "text/html; charset=utf-8", ".js": "text/javascript", ".css": "text/css", ".svg": "image/svg+xml", ".woff2": "font/woff2", ".json": "application/json", ".txt": "text/plain", ".ico": "image/x-icon" };

createServer((req, res) => {
  const url = new URL(req.url || "/", "http://x");
  let p = normalize(decodeURIComponent(url.pathname)).split("..").join("");
  const candidates = [p, p + ".html", join(p, "index.html")].map((c) => join(root, c));
  let file = candidates.find((c) => existsSync(c) && statSync(c).isFile());
  let status = 200;
  if (!file) {
    file = join(root, "404.html");
    status = 404;
  }
  res.writeHead(status, {
    "Content-Type": types[extname(file)] || "application/octet-stream",
    "X-Frame-Options": "DENY",
    "Referrer-Policy": "same-origin",
    "X-Content-Type-Options": "nosniff",
  });
  res.end(readFileSync(file));
}).listen(port, () => console.log(`serving out/ on http://localhost:${port}`));
