import { chromium, devices } from "playwright";
import fs from "node:fs/promises";
import path from "node:path";

await fs.mkdir(".cl-check", { recursive: true });
const URL = "https://aether.mac-api.xyz/changelog.html";

const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});
const problems = [];
const fails = [];

function chk(name, ok, detail = "") {
  console.log(`  ${ok ? "OK  " : "FAIL"} ${name} ${detail}`);
  if (!ok) fails.push(name);
}

/* 桌面 */
const ctx = await browser.newContext({
  viewport: { width: 1440, height: 1000 },
  deviceScaleFactor: 2,
});
const page = await ctx.newPage();
page.on("pageerror", (e) => problems.push(e.message));
page.on("console", (m) => m.type() === "error" && problems.push("console: " + m.text()));

await page.goto(URL, { waitUntil: "load", timeout: 45000 });
await page.waitForSelector(".cl-rel", { timeout: 20000 });
await page.waitForTimeout(900);

const m = await page.evaluate(() => {
  const rels = [...document.querySelectorAll(".cl-rel")];
  const first = rels[0];
  return {
    theme: document.documentElement.dataset.theme,
    count: rels.length,
    latestMarked: first?.classList.contains("cl-rel--latest"),
    firstVer: first?.querySelector(".cl-ver")?.textContent.trim(),
    firstBlocks: first?.querySelectorAll(".cl-flow > *").length,
    dlLinks: first ? first.querySelectorAll(".cl-dl a").length : 0,
    dlHref: first?.querySelector(".cl-dl a")?.getAttribute("href"),
    dlText: first?.querySelector(".cl-dl a")?.textContent.trim(),
    hasH3: !!document.querySelector(".cl-flow h4"),
    // 正文里不应残留 Markdown 反引号（说明转换漏了）
    rawBackticks: (document.querySelector(".cl-flow")?.textContent || "").includes("`"),
    // 反斜杠必须原样保留，不能丢字符（曾出现 \b 被解释成退格吃掉 b）
    hasBaseUrl: (document.querySelector(".cl-flow")?.textContent || "").includes("base_url"),
    meta: document.getElementById("cl-meta")?.textContent.trim(),
    emptyHidden: document.getElementById("cl-empty")?.hidden,
    overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
    // 用首页同款类名，并且校验它真的渲染出来了
    // （site.css 有 .site-nav .nav-mark-slot > svg { opacity: 0 }，
    //  本页没有开屏动画，忘了覆盖就会一直是透明的）
    navMark: (() => {
      const svg = document.querySelector(".nav-mark-slot svg");
      if (!svg) return false;
      const r = svg.getBoundingClientRect();
      const o = parseFloat(getComputedStyle(svg).opacity);
      return r.width >= 24 && r.height >= 24 && o > 0.5;
    })(),
    skeletonsLeft: document.querySelectorAll(".cl-skeleton").length,
  };
});

console.log("=== 桌面端 ===");
console.log("  meta:", m.meta);
chk("版本卡渲染", m.count === 5, `(${m.count} 个)`);
chk("最新版标记", m.latestMarked === true);
chk("最新版本号", m.firstVer === "0.4.1", `(${m.firstVer})`);
chk("正文块渲染", m.firstBlocks > 3, `(${m.firstBlocks} 块)`);
chk("无裸反引号", m.rawBackticks === false);
chk("关键文本未丢字", m.hasBaseUrl);
chk("含小节标题", m.hasH3);
chk("下载按钮", m.dlLinks === 2, `(${m.dlLinks} 个)`);
chk("下载指向本站", m.dlHref?.startsWith("/download/"), `(${m.dlHref})`);
chk("骨架已清除", m.skeletonsLeft === 0);
chk("空态隐藏", m.emptyHidden === true);
chk("无横向溢出", m.overflow === 0, `(${m.overflow}px)`);
chk("图标正常", m.navMark);
chk("主题玻璃", m.theme === "glass", `(${m.theme})`);

await page.screenshot({ path: path.join(".cl-check", "desktop-top.png") });
await page.screenshot({ path: path.join(".cl-check", "desktop-full.png"), fullPage: true });

// 主题切换
await page.click('.theme-switch button[data-theme-set="dark"]');
await page.waitForTimeout(500);
const dark = await page.evaluate(() => document.documentElement.dataset.theme);
chk("可切主题", dark === "dark", `(${dark})`);
await page.screenshot({ path: path.join(".cl-check", "desktop-dark.png") });

// 从更新日志点下载
const [dl] = await Promise.all([
  page.waitForEvent("download", { timeout: 45000 }).catch(() => null),
  page.click(".cl-dl a"),
]);
chk("日志页可下载", !!dl, dl ? `(${dl.suggestedFilename()})` : "");

await ctx.close();

/* 移动 */
const mctx = await browser.newContext({ ...devices["iPhone 13"] });
const mp = await mctx.newPage();
await mp.goto(URL, { waitUntil: "load", timeout: 45000 });
await mp.waitForSelector(".cl-rel", { timeout: 20000 });
await mp.waitForTimeout(800);

const mm = await mp.evaluate(() => ({
  overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
  count: document.querySelectorAll(".cl-rel").length,
  toggleVisible: (() => {
    const t = document.querySelector(".nav-toggle");
    return t ? getComputedStyle(t).display !== "none" : false;
  })(),
}));
console.log("\n=== 移动端 ===");
chk("无横向溢出", mm.overflow === 0, `(${mm.overflow}px)`);
chk("版本卡渲染", mm.count === 5);
chk("汉堡可见", mm.toggleVisible);
await mp.screenshot({ path: path.join(".cl-check", "mobile.png") });
await mctx.close();

await browser.close();
console.log("\n运行期错误:", problems.length ? [...new Set(problems)].join(" | ") : "无");
console.log(fails.length ? `\n失败 ${fails.length} 项` : "\n更新日志页验证通过");
process.exit(fails.length || problems.length ? 1 : 0);
