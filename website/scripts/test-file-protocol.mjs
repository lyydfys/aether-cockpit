/* 直接以 file:// 打开时的冒烟检查：字体、图片、开屏收尾。 */
import { chromium } from "playwright";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const target = pathToFileURL(path.resolve(here, "..", "index.html")).href;

const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const errs = [];
page.on("pageerror", (e) => errs.push(e.message));
page.on("console", (m) => m.type() === "error" && errs.push("console: " + m.text()));

await page.goto(target, { waitUntil: "load" });
await page.waitForFunction(
  () => document.documentElement.classList.contains("is-ready"),
  null,
  { timeout: 12000 }
);
await page.waitForTimeout(1200);

const report = await page.evaluate(() => ({
  href: location.href.slice(0, 32),
  fonts: document.fonts.status,
  inter: document.fonts.check('16px "Aether Sans"'),
  noto: document.fonts.check('16px "Aether Sans SC"'),
  broken: [...document.images].filter((i) => i.complete && i.naturalWidth === 0).length,
  images: document.images.length,
  navMark: getComputedStyle(document.querySelector(".site-nav .nav-mark-slot svg")).opacity,
  scrolled: document.body.scrollHeight > innerHeight,
}));

await browser.close();
console.log(JSON.stringify(report, null, 1));
console.log("errors:", errs.length ? [...new Set(errs)].join(" | ") : "none");
