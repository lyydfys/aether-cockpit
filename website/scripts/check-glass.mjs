import { chromium } from "playwright";

const url = process.argv[2] || "http://127.0.0.1:8140/index.html";
const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});

const fails = [];
const problems = [];

const ctx = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  deviceScaleFactor: 2,
});
await ctx.addInitScript(() => {
  try { localStorage.setItem("aether-theme", "glass"); } catch (e) {}
});
const page = await ctx.newPage();
page.on("pageerror", (e) => problems.push(e.message));
page.on("console", (m) => m.type() === "error" && problems.push("console: " + m.text()));

await page.goto(url, { waitUntil: "load" });
await page.waitForFunction(
  () => document.documentElement.classList.contains("is-ready"),
  null,
  { timeout: 15000 }
);
await page.waitForTimeout(800);

/* 1) 主题确实生效，且背景是渐变而非实色 */
const info = await page.evaluate(() => {
  const body = getComputedStyle(document.body);
  const cap = document.querySelector(".cap");
  const capCs = cap ? getComputedStyle(cap) : null;
  const nav = document.querySelector(".site-nav");
  return {
    theme: document.documentElement.dataset.theme,
    hasGradient: body.backgroundImage !== "none",
    isGlassClass: document.body.classList.contains("is-glass"),
    atmos: getComputedStyle(document.querySelector(".g-atmos")).display,
    capBackdrop: capCs ? capCs.backdropFilter || capCs.webkitBackdropFilter : null,
    capInsetHighlight: capCs ? /inset/.test(capCs.boxShadow) : false,
    gloss: document.querySelectorAll(".g-gloss").length,
    overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    broken: [...document.images].filter((i) => i.complete && i.naturalWidth === 0).length,
  };
});
console.log("glass info:", JSON.stringify(info));

if (info.theme !== "glass") fails.push("玻璃主题未生效");
if (!info.hasGradient) fails.push("玻璃主题缺少底色渐变");
if (info.atmos !== "block") fails.push("氛围层未显示");
if (!info.capBackdrop || !info.capBackdrop.includes("blur")) fails.push("卡片缺少 backdrop blur");
if (!info.capInsetHighlight) fails.push("卡片缺少内高光");
if (info.gloss < 10) fails.push(`高光节点过少: ${info.gloss}`);
if (info.overflow > 0) fails.push(`横向溢出 ${info.overflow}px`);
if (info.broken > 0) fails.push(`坏图 ${info.broken}`);

/* 2) 交互光效：高光位置随指针变化 */
await page.hover(".cap");
await page.mouse.move(200, 400);
await page.waitForTimeout(200);
const gx1 = await page.evaluate(() => {
  const el = document.querySelector(".cap");
  return el ? el.style.getPropertyValue("--gx") : "";
});
await page.mouse.move(600, 600);
await page.waitForTimeout(200);
const gx2 = await page.evaluate(() => {
  const el = document.querySelector(".cap");
  return el ? el.style.getPropertyValue("--gx") : "";
});
console.log("gloss pos:", JSON.stringify({ gx1, gx2 }));
if (!gx1 && !gx2) fails.push("指针移动未更新高光位置");

/* 3) 三态循环 */
const cycle = await page.evaluate(async () => {
  const out = [];
  for (const t of ["light", "dark", "glass"]) {
    document.querySelector(`.theme-switch button[data-theme-set="${t}"]`).click();
    await new Promise((r) => setTimeout(r, 350));
    out.push({
      clicked: t,
      theme: document.documentElement.dataset.theme,
      bgImg: getComputedStyle(document.body).backgroundImage !== "none",
      bgColor: getComputedStyle(document.body).backgroundColor,
    });
  }
  return out;
});
console.log("cycle:", JSON.stringify(cycle));
for (const c of cycle) {
  if (c.theme !== c.clicked) fails.push(`切到 ${c.clicked} 后主题是 ${c.theme}`);
}
if (cycle.find((c) => c.clicked === "glass").bgImg !== true) fails.push("玻璃主题切回后没有渐变");
if (cycle.find((c) => c.clicked === "dark").bgImg !== false) fails.push("黑色主题不应有渐变");

/* 4) 移动端 */
const mctx = await browser.newContext({
  viewport: { width: 390, height: 844 },
  deviceScaleFactor: 2,
  isMobile: true,
});
await mctx.addInitScript(() => {
  try { localStorage.setItem("aether-theme", "glass"); } catch (e) {}
});
const mp = await mctx.newPage();
await mp.goto(url, { waitUntil: "load" });
await mp.waitForFunction(
  () => document.documentElement.classList.contains("is-ready"),
  null,
  { timeout: 15000 }
);
await mp.waitForTimeout(600);
const mm = await mp.evaluate(() => ({
  overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
  themeSwitch: !!document.querySelector(".theme-switch"),
  buttons: document.querySelectorAll(".theme-switch button").length,
}));
console.log("mobile:", JSON.stringify(mm));
if (mm.overflow > 0) fails.push(`移动端横向溢出 ${mm.overflow}px`);
if (mm.buttons !== 3) fails.push(`主题开关按钮 ${mm.buttons} 个（应 3）`);
await mctx.close();
await ctx.close();

await browser.close();
console.log("\nproblems:", problems.length ? [...new Set(problems)].join(" | ") : "none");
if (fails.length) {
  console.log("FAIL\n - " + fails.join("\n - "));
  process.exit(1);
}
console.log("通过：柔光玻璃主题（渐变底 / backdrop blur / 内高光 / 光点跟随 / 三态切换）");
