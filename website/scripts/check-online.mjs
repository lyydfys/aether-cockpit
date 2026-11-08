import { chromium, devices } from "playwright";
import fs from "node:fs/promises";
import path from "node:path";

const URL = "https://aether.mac-api.xyz/";
await fs.mkdir(".online-check", { recursive: true });

const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});

const problems = [];
const failed = [];
const pass = [];
const fail = [];

function check(name, ok, detail = "") {
  if (ok) {
    console.log(`  OK   ${name} ${detail}`);
    pass.push(name);
  } else {
    console.log(`  FAIL ${name} ${detail}`);
    fail.push(name);
  }
}

/* ---------- 桌面端 ---------- */
{
  const ctx = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    deviceScaleFactor: 2,
    ignoreHTTPSErrors: false,
  });
  const page = await ctx.newPage();
  page.on("pageerror", (e) => problems.push("pageerror: " + e.message));
  page.on("console", (m) => m.type() === "error" && problems.push("console: " + m.text()));
  page.on("response", (r) => {
    if (r.status() >= 400) failed.push(`${r.status()} ${r.url()}`);
  });

  const t0 = Date.now();
  await page.goto(URL, { waitUntil: "load", timeout: 45000 });
  const loadMs = Date.now() - t0;

  // 开屏应当在跑
  const ign = await page.evaluate(() => !!document.getElementById("ignition"));

  await page.waitForFunction(
    () => document.documentElement.classList.contains("is-ready"),
    null,
    { timeout: 20000 }
  );
  await page.waitForTimeout(900);

  const m = await page.evaluate(() => {
    const cs = getComputedStyle(document.body);
    return {
      theme: document.documentElement.dataset.theme,
      title: document.title,
      h1: document.querySelector(".hero-title")?.textContent?.trim().slice(0, 20),
      bgImg: cs.backgroundImage !== "none",
      navMark: !!document.querySelector("#nav-mark-slot svg, #nav-mark-slot use"),
      phones: document.querySelectorAll(".phone-rail .phone").length,
      broken: [...document.images].filter((i) => i.complete && i.naturalWidth === 0).length,
      imgs: document.images.length,
      overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
      themeBtns: document.querySelectorAll(".theme-switch button").length,
      canonical: document.querySelector('link[rel="canonical"]')?.href,
      ogImage: document.querySelector('meta[property="og:image"]')?.content,
      webp: [...document.images].filter((i) => i.currentSrc.endsWith(".webp")).length,
      fonts: document.fonts.status,
    };
  });

  console.log("\n=== 桌面端 ===");
  console.log("  load:", loadMs, "ms");
  check("开屏存在", ign);
  check("主题为柔光玻璃", m.theme === "glass", `(${m.theme})`);
  check("背景渐变生效", m.bgImg);
  check("导航标记可见", m.navMark);
  check("首屏四张手机", m.phones === 4, `(${m.phones})`);
  check("无坏图", m.broken === 0, `(${m.imgs} 张, webp ${m.webp})`);
  check("无横向溢出", m.overflow === 0, `(${m.overflow}px)`);
  check("主题开关三项", m.themeBtns === 3);
  check("canonical 正确", m.canonical === "https://aether.mac-api.xyz/", `(${m.canonical})`);
  check("og:image 正确", m.ogImage === "https://aether.mac-api.xyz/assets/img/og-cover.png");
  check("字体加载完成", m.fonts === "loaded", `(${m.fonts})`);
  console.log("  h1:", m.h1);
  console.log("  title:", m.title);

  await page.screenshot({ path: path.join(".online-check", "desktop-glass.png") });
  await page.screenshot({ path: path.join(".online-check", "desktop-full.png"), fullPage: true });

  // 切主题
  await page.click('.theme-switch button[data-theme-set="dark"]');
  await page.waitForTimeout(500);
  const dark = await page.evaluate(() => document.documentElement.dataset.theme);
  check("可切到纯黑", dark === "dark", `(${dark})`);
  await page.screenshot({ path: path.join(".online-check", "desktop-dark.png") });

  await ctx.close();
}

/* ---------- 移动端 ---------- */
{
  const ctx = await browser.newContext({ ...devices["iPhone 13"] });
  const page = await ctx.newPage();
  page.on("pageerror", (e) => problems.push("mobile: " + e.message));
  await page.goto(URL, { waitUntil: "load", timeout: 45000 });
  await page.waitForFunction(
    () => document.documentElement.classList.contains("is-ready"),
    null,
    { timeout: 20000 }
  );
  await page.waitForTimeout(800);

  const m = await page.evaluate(() => ({
    overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    toggle: !!document.querySelector(".nav-toggle"),
    toggleVisible: (() => {
      const el = document.querySelector(".nav-toggle");
      return el ? getComputedStyle(el).display !== "none" : false;
    })(),
    theme: document.documentElement.dataset.theme,
  }));

  console.log("\n=== 移动端 (iPhone 13) ===");
  check("无横向溢出", m.overflow === 0, `(${m.overflow}px)`);
  check("汉堡菜单可见", m.toggle && m.toggleVisible);
  check("主题玻璃", m.theme === "glass");

  // 打开抽屉
  await page.click(".nav-toggle");
  await page.waitForTimeout(400);
  const drawer = await page.evaluate(() => {
    const d = document.getElementById("nav-drawer");
    const as = [...d.querySelectorAll("a")];
    return {
      hidden: d.hidden,
      links: as.length,
      hrefs: as.map((a) => a.getAttribute("href") || ""),
    };
  });
  check(
    "抽屉可展开",
    !drawer.hidden && drawer.links >= 8,
    `(${drawer.links} 个入口)`
  );
  check("抽屉含更新日志", drawer.hrefs.some((h) => h.includes("changelog")), "");

  await page.screenshot({ path: path.join(".online-check", "mobile-drawer.png") });
  await ctx.close();
}

/* ---------- 404 ---------- */
{
  const ctx = await browser.newContext();
  const page = await ctx.newPage();
  const resp = await page.goto(URL + "nonexistent-path", { waitUntil: "load", timeout: 30000 });
  const txt = await page.evaluate(() => document.body.innerText.slice(0, 60));
  console.log("\n=== 404 ===");
  check("状态码 404", resp.status() === 404, `(${resp.status()})`);
  check("自定义 404 内容", txt.includes("这个地址没有内容"));
  await ctx.close();
}

await browser.close();

console.log("\n失败请求:", failed.length ? [...new Set(failed)].join(" | ") : "无");
console.log("运行期错误:", problems.length ? [...new Set(problems)].join(" | ") : "无");

const ok = fail.length === 0 && failed.length === 0 && problems.length === 0;
console.log(`\n通过 ${pass.length} 项，失败 ${fail.length} 项`);
console.log(ok ? "线上验证全部通过" : "存在失败项");
process.exit(ok ? 0 : 1);
