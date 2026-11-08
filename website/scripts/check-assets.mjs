import { chromium } from "playwright";

const url = process.argv[2] || "http://127.0.0.1:8141/index.html";
const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});
const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const page = await ctx.newPage();

const problems = [];
page.on("pageerror", (e) => problems.push("pageerror: " + e.message));
page.on("console", (m) => m.type() === "error" && problems.push("console: " + m.text()));

await page.goto(url, { waitUntil: "load" });
await page.waitForFunction(
  () => document.documentElement.classList.contains("is-ready"),
  null,
  { timeout: 15000 }
);

// 慢滚一遍触发所有图片
await page.evaluate(async () => {
  const step = innerHeight * 0.7;
  for (let y = 0; y <= document.body.scrollHeight; y += step) {
    scrollTo(0, y);
    await new Promise((r) => setTimeout(r, 260));
  }
  scrollTo(0, 0);
});
await page.waitForTimeout(1000);

const m = await page.evaluate(() => {
  const imgs = [...document.images];
  return {
    total: imgs.length,
    broken: imgs.filter((i) => i.complete && i.naturalWidth === 0).map((i) => i.src.split("/").pop()),
    webp: imgs.filter((i) => i.src.endsWith(".webp")).length,
    zeroSize: imgs.filter((i) => i.complete && i.naturalHeight === 0).length,
  };
});

console.log(JSON.stringify(m, null, 1));
console.log("problems:", problems.length ? [...new Set(problems)].join(" | ") : "none");

await browser.close();
if (m.broken.length || problems.length) process.exit(1);
