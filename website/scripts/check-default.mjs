import { chromium } from "playwright";
import path from "node:path";
import fs from "node:fs/promises";

const url = process.argv[2] || "http://127.0.0.1:8140/index.html";
const out = path.resolve(".review");
await fs.mkdir(out, { recursive: true });

const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});

const fails = [];
const problems = [];

/* 1) 全新访客（无 localStorage）→ 应为玻璃 */
{
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  page.on("pageerror", (e) => problems.push(e.message));
  await page.goto(url, { waitUntil: "load" });
  await page.waitForFunction(
    () => document.documentElement.classList.contains("is-ready"),
    null,
    { timeout: 15000 }
  );
  await page.waitForTimeout(700);

  const m = await page.evaluate(() => {
    const body = getComputedStyle(document.body);
    return {
      theme: document.documentElement.dataset.theme,
      bgImg: body.backgroundImage !== "none",
      bgColor: body.backgroundColor,
      pressed: [...document.querySelectorAll(".theme-switch button")]
        .filter((b) => b.getAttribute("aria-pressed") === "true")
        .map((b) => b.dataset.themeSet),
      order: [...document.querySelectorAll(".theme-switch button")].map(
        (b) => b.dataset.themeSet
      ),
      themeColor: document
        .querySelector('meta[name="theme-color"]')
        ?.getAttribute("content"),
      isGlass: document.body.classList.contains("is-glass"),
      atmos: getComputedStyle(document.querySelector(".g-atmos")).display,
    };
  });
  console.log("fresh visitor:", JSON.stringify(m));

  if (m.theme !== "glass") fails.push(`默认主题是 ${m.theme}（应为 glass）`);
  if (!m.bgImg) fails.push("默认主题没有渐变底");
  if (m.pressed.join() !== "glass") fails.push(`默认选中 ${m.pressed}`);
  if (m.order.join() !== "glass,light,dark")
    fails.push(`开关顺序 ${m.order}`);
  if (m.themeColor !== "#eef2fa") fails.push(`theme-color ${m.themeColor}`);
  if (!m.isGlass) fails.push("body 缺少 is-glass");
  if (m.atmos !== "block") fails.push("氛围层未显示");

  await page.screenshot({ path: path.join(out, "default-fresh.png") });
  await ctx.close();
}

/* 2) 系统偏好为深色时，默认仍应是玻璃 */
{
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    colorScheme: "dark",
  });
  const page = await ctx.newPage();
  await page.goto(url, { waitUntil: "load" });
  await page.waitForFunction(
    () => document.documentElement.classList.contains("is-ready"),
    null,
    { timeout: 15000 }
  );
  const m = await page.evaluate(() => document.documentElement.dataset.theme);
  console.log("system=dark:", m);
  if (m !== "glass") fails.push(`系统深色时默认是 ${m}（应为 glass）`);
  await ctx.close();
}

/* 3) 有存档时尊重存档 */
{
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  await ctx.addInitScript(() => {
    try { localStorage.setItem("aether-theme", "dark"); } catch (e) {}
  });
  const page = await ctx.newPage();
  await page.goto(url, { waitUntil: "load" });
  await page.waitForFunction(
    () => document.documentElement.classList.contains("is-ready"),
    null,
    { timeout: 15000 }
  );
  const m = await page.evaluate(() => document.documentElement.dataset.theme);
  console.log("saved=dark:", m);
  if (m !== "dark") fails.push(`存档为 dark 时显示 ${m}`);
  await ctx.close();
}

/* 4) 切走再切回玻璃 */
{
  const ctx = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const page = await ctx.newPage();
  await page.goto(url, { waitUntil: "load" });
  await page.waitForFunction(
    () => document.documentElement.classList.contains("is-ready"),
    null,
    { timeout: 15000 }
  );
  await page.click('.theme-switch button[data-theme-set="dark"]');
  await page.waitForTimeout(400);
  const dark = await page.evaluate(() => ({
    theme: document.documentElement.dataset.theme,
    stored: localStorage.getItem("aether-theme"),
  }));
  await page.click('.theme-switch button[data-theme-set="glass"]');
  await page.waitForTimeout(400);
  const glass = await page.evaluate(() => ({
    theme: document.documentElement.dataset.theme,
    stored: localStorage.getItem("aether-theme"),
    bgImg: getComputedStyle(document.body).backgroundImage !== "none",
  }));
  console.log("switch:", JSON.stringify({ dark, glass }));
  if (dark.theme !== "dark" || dark.stored !== "dark") fails.push("切到 dark 未持久化");
  if (glass.theme !== "glass" || glass.stored !== "glass") fails.push("切回 glass 未持久化");
  if (!glass.bgImg) fails.push("切回玻璃后没有渐变");
  await ctx.close();
}

/* 5) 移动端默认 */
{
  const ctx = await browser.newContext({
    viewport: { width: 390, height: 844 },
    isMobile: true,
    deviceScaleFactor: 2,
  });
  const page = await ctx.newPage();
  await page.goto(url, { waitUntil: "load" });
  await page.waitForFunction(
    () => document.documentElement.classList.contains("is-ready"),
    null,
    { timeout: 15000 }
  );
  await page.waitForTimeout(600);
  const m = await page.evaluate(() => ({
    theme: document.documentElement.dataset.theme,
    overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
  }));
  console.log("mobile default:", JSON.stringify(m));
  if (m.theme !== "glass") fails.push(`移动端默认 ${m.theme}`);
  if (m.overflow > 0) fails.push(`移动端溢出 ${m.overflow}px`);
  await page.screenshot({ path: path.join(out, "default-mobile.png") });
  await ctx.close();
}

await browser.close();
console.log("\nproblems:", problems.length ? [...new Set(problems)].join(" | ") : "none");
if (fails.length) {
  console.log("FAIL\n - " + fails.join("\n - "));
  process.exit(1);
}
console.log("通过：默认柔光玻璃，存档可覆盖，系统深色不干扰");
