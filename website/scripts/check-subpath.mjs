import { chromium } from "playwright";
import http from "node:http";
import fs from "node:fs";
import path from "node:path";

/* 模拟 GitHub Pages 子路径部署：http://127.0.0.1:8142/aether-cockpit/
   —— 这是最容易上线后才发现问题的地方，必须实测。 */

const ROOT = process.cwd();
const PREFIX = "/aether-cockpit";
const PORT = 8142;

const MIME = {
  ".html": "text/html; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".png": "image/png",
  ".webp": "image/webp",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
  ".ico": "image/x-icon",
  ".json": "application/json",
};

const server = http.createServer((req, res) => {
  let url = decodeURIComponent(req.url.split("?")[0]);
  if (!url.startsWith(PREFIX)) {
    res.writeHead(404).end("outside prefix");
    return;
  }
  let rel = url.slice(PREFIX.length);
  if (rel === "" || rel === "/") rel = "/index.html";
  const file = path.join(ROOT, rel);

  if (!file.startsWith(ROOT) || !fs.existsSync(file) || fs.statSync(file).isDirectory()) {
    res.writeHead(404).end("not found: " + rel);
    return;
  }
  res.writeHead(200, { "content-type": MIME[path.extname(file)] || "application/octet-stream" });
  fs.createReadStream(file).pipe(res);
});

await new Promise((r) => server.listen(PORT, "127.0.0.1", r));
const url = `http://127.0.0.1:${PORT}${PREFIX}/`;
console.log("子路径地址:", url);

const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const page = await ctx.newPage();

const problems = [];
const failed = [];
page.on("pageerror", (e) => problems.push(e.message));
page.on("console", (m) => m.type() === "error" && problems.push("console: " + m.text()));
page.on("response", (r) => {
  if (r.status() >= 400) failed.push(`${r.status()} ${r.url()}`);
});

await page.goto(url, { waitUntil: "load" });
await page.waitForFunction(
  () => document.documentElement.classList.contains("is-ready"),
  null,
  { timeout: 20000 }
);
await page.waitForTimeout(1000);

const m = await page.evaluate(() => ({
  theme: document.documentElement.dataset.theme,
  broken: [...document.images].filter((i) => i.complete && i.naturalWidth === 0).length,
  imgs: document.images.length,
  css: getComputedStyle(document.body).backgroundImage !== "none",
  icons: [...document.querySelectorAll('link[rel*="icon"]')].map((l) => l.href),
}));

console.log(JSON.stringify(m, null, 1));

// 字体是否在子路径下也加载成功
const fontsOk = await page.evaluate(async () => {
  await document.fonts.ready;
  const list = [];
  document.fonts.forEach((f) => list.push(f.status));
  return { total: list.length, loaded: list.filter((s) => s === "loaded").length };
});
console.log("字体:", JSON.stringify(fontsOk));

await browser.close();
server.close();

console.log("\n失败请求:", failed.length ? failed.join(" | ") : "无");
console.log("运行期错误:", problems.length ? [...new Set(problems)].join(" | ") : "无");

const ok =
  m.theme === "glass" &&
  m.broken === 0 &&
  m.css &&
  failed.length === 0 &&
  problems.length === 0 &&
  fontsOk.loaded === fontsOk.total;
console.log(ok ? "\n通过：子路径部署正常" : "\nFAIL");
process.exit(ok ? 0 : 1);
