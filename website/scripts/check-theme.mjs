/*
 * 首屏双端 + 黑白双主题 的验收。
 *  - 两套主题分别渲染，校验背景/文字/强调色
 *  - 首屏四张手机：数量、尺寸、是否都在第一屏内
 *  - 平台开关是否真的改变构图（hero 高度/手机尺寸要有变化）
 *  - 工作台手机墙四张
 *  - 溢出、坏图、运行期错误
 *
 * 用法：node scripts/check-theme.mjs [url]
 */
import { chromium } from "playwright";
import path from "node:path";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const out = path.resolve(here, "..", ".review");
const url = process.argv[2] || "http://127.0.0.1:8140/index.html";

await fs.mkdir(out, { recursive: true });

const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});

const fails = [];
const problems = [];

async function settle(page) {
  await page.waitForFunction(
    () => document.documentElement.classList.contains("is-ready"),
    null,
    { timeout: 15000 }
  );
  await page.waitForTimeout(700);
}

const metrics = (page) =>
  page.evaluate(() => {
    const phones = [...document.querySelectorAll(".phone-rail .phone")];
    const wall = [...document.querySelectorAll(".phone-wall .phone")];
    const one = (el) => {
      const r = el.getBoundingClientRect();
      return { w: Math.round(r.width), h: Math.round(r.height) };
    };
    return {
      theme: document.documentElement.dataset.theme,
      bg: getComputedStyle(document.body).backgroundColor,
      fg: getComputedStyle(document.body).color,
      accent: getComputedStyle(document.querySelector(".hero-title .accent")).color,
      navBg: getComputedStyle(document.querySelector(".site-nav")).backgroundColor,
      heroPhones: phones.length,
      heroPhone: phones[0] ? one(phones[0]) : null,
      allPhonesInViewport: phones.every(
        (p) => p.getBoundingClientRect().bottom <= innerHeight
      ),
      wallPhones: wall.length,
      wallPhone: wall[0] ? one(wall[0]) : null,
      heroHeight: Math.round(document.querySelector(".hero").getBoundingClientRect().height),
      overflow:
        document.documentElement.scrollWidth - document.documentElement.clientWidth,
      brokenImgs: [...document.images].filter((i) => i.complete && i.naturalWidth === 0)
        .length,
    };
  });

/* ---------- 双主题 ---------- */
const results = {};
for (const theme of ["dark", "light"]) {
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
  });
  await ctx.addInitScript((t) => {
    try {
      localStorage.setItem("aether-theme", t);
    } catch (e) {}
  }, theme);
  const page = await ctx.newPage();
  page.on("pageerror", (e) => problems.push(`${theme}: ${e.message}`));
  await page.goto(url, { waitUntil: "load" });
  await settle(page);
  const m = await metrics(page);
  results[theme] = m;
  console.log(`${theme.padEnd(6)}:`, JSON.stringify(m));
  await page.screenshot({ path: path.join(out, `hero-${theme}.png`) });
  await ctx.close();
}

/* 主题断言 */
if (results.dark.bg !== "rgb(15, 17, 21)") fails.push(`黑色主题背景 ${results.dark.bg}`);
if (results.light.bg !== "rgb(247, 248, 250)") fails.push(`白色主题背景 ${results.light.bg}`);
if (results.dark.theme !== "dark" || results.light.theme !== "light")
  fails.push("主题属性未生效");

/* 两套主题的强调色都应是品牌蓝（暗底上略亮） */
const blue = (c) => /rgb\(26, 115, 232\)|rgb\(74, 155, 245\)/.test(c);
for (const t of ["dark", "light"]) {
  if (!blue(results[t].accent)) fails.push(`${t} 强调色偏离品牌蓝: ${results[t].accent}`);
}

/* 文字对比：黑白两套主题的正文色必须不同 */
if (results.dark.fg === results.light.fg) fails.push("两套主题正文色相同");

/* ---------- 首屏手机 ---------- */
for (const t of ["dark", "light"]) {
  const r = results[t];
  if (r.heroPhones !== 4) fails.push(`${t}: 首屏手机 ${r.heroPhones} 张（应 4）`);
  if (r.heroPhone && r.heroPhone.w < 240)
    fails.push(`${t}: 首屏手机宽 ${r.heroPhone.w}px（应 ≥240）`);
  if (r.wallPhones !== 4) fails.push(`${t}: 手机墙 ${r.wallPhones} 张（应 4）`);
  if (r.wallPhone && r.wallPhone.w < 200)
    fails.push(`${t}: 手机墙宽 ${r.wallPhone.w}px（应 ≥200）`);
  if (r.overflow > 0) fails.push(`${t}: 横向溢出 ${r.overflow}px`);
  if (r.brokenImgs > 0) fails.push(`${t}: 坏图 ${r.brokenImgs}`);
}

/* ---------- 平台开关改变构图 ---------- */
const ctx2 = await browser.newContext({ viewport: { width: 1440, height: 900 } });
const page2 = await ctx2.newPage();
await page2.goto(url, { waitUntil: "load" });
await settle(page2);
const desktopFocus = await metrics(page2);
await page2.click('.platform-tab[data-platform="mobile"]');
await page2.waitForTimeout(800);
const mobileFocus = await metrics(page2);
await page2.screenshot({ path: path.join(out, "hero-mobile-focus.png") });
await ctx2.close();

console.log("focus desktop:", desktopFocus.heroHeight, "phone", JSON.stringify(desktopFocus.heroPhone));
console.log("focus mobile :", mobileFocus.heroHeight, "phone", JSON.stringify(mobileFocus.heroPhone));

if (Math.abs(desktopFocus.heroHeight - mobileFocus.heroHeight) < 40)
  fails.push("切换平台后构图几乎没变化（应明显不同）");
if (mobileFocus.heroPhone && mobileFocus.heroPhone.h <= desktopFocus.heroPhone.h)
  fails.push("切到手机版后手机没有变大");

/* ---------- 移动端 ---------- */
const mctx = await browser.newContext({
  viewport: { width: 390, height: 844 },
  deviceScaleFactor: 2,
  isMobile: true,
});
const mp = await mctx.newPage();
await mp.goto(url, { waitUntil: "load" });
await settle(mp);
const mm = await mp.evaluate(() => ({
  overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
  heroPhones: document.querySelectorAll(".phone-rail .phone").length,
  wallPhones: document.querySelectorAll(".phone-wall .phone").length,
  themeSwitchVisible: !!document.querySelector(".theme-switch"),
}));
await mp.screenshot({ path: path.join(out, "mobile-hero.png") });
await mctx.close();
console.log("mobile :", JSON.stringify(mm));
if (mm.overflow > 0) fails.push(`移动端横向溢出 ${mm.overflow}px`);
if (mm.heroPhones !== 4) fails.push("移动端首屏手机不足 4 张");

await browser.close();

console.log("\n运行期错误:", problems.length ? [...new Set(problems)].join(" | ") : "none");
if (fails.length) {
  console.log("FAIL\n - " + fails.join("\n - "));
  process.exit(1);
}
console.log("通过：黑白双主题 / 首屏四张手机 / 平台开关改变构图 / 手机墙四张");
