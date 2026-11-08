import { chromium, devices } from "playwright";
import fs from "node:fs/promises";
import path from "node:path";

const URL = "https://aether.mac-api.xyz/";
await fs.mkdir(".dl-check", { recursive: true });

const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});
const ctx = await browser.newContext({
  viewport: { width: 1440, height: 900 },
  deviceScaleFactor: 2,
});
const page = await ctx.newPage();
const problems = [];
page.on("pageerror", (e) => problems.push(e.message));
page.on("console", (m) => m.type() === "error" && problems.push("console: " + m.text()));

await page.goto(URL, { waitUntil: "domcontentloaded", timeout: 90000 });
await page.waitForFunction(
  () => document.documentElement.classList.contains("is-ready"),
  null,
  { timeout: 20000 }
);
await page.waitForTimeout(1200);

const m = await page.evaluate(() => {
  const btns = [...document.querySelectorAll("[data-dl-btn]")].map((a) => ({
    kind: a.dataset.dlBtn,
    href: a.getAttribute("href"),
    text: a.textContent.trim(),
    download: a.hasAttribute("download"),
  }));
  const versions = [...document.querySelectorAll("[data-dl-version]")].map((e) => e.textContent.trim());
  const sizes = [...document.querySelectorAll("[data-dl-size]")].map((e) => e.textContent.trim());
  const line = document.querySelector("[data-dl-version-line]");
  const files = [...document.querySelectorAll("[data-dl-file] code")].map((e) => e.textContent.trim());
  return { btns, versions, sizes, line: line?.textContent.trim(), files };
});

console.log("下载按钮:");
m.btns.forEach((b) => console.log(`  ${b.kind.padEnd(8)} ${b.text.padEnd(18)} download=${b.download}  ${b.href}`));
console.log("\n版本:", [...new Set(m.versions)], " 版本行:", m.line);
console.log("大小:", m.sizes);
console.log("文件名:", m.files);

const fails = [];
if (m.btns.length !== 3) fails.push(`按钮数 ${m.btns.length}（应 3）`);
if (!m.btns.every((b) => b.href && b.href.startsWith("/download/"))) fails.push("按钮未指向 /download/");
if (!m.btns.every((b) => b.download)) fails.push("缺少 download 属性");
if (!m.versions.every((v) => v === "v0.4.1" || v === "0.4.1")) fails.push(`版本显示异常: ${m.versions}`);
if (!m.sizes.every((s) => /\d/.test(s))) fails.push("大小未填充");
if (!m.line || !m.line.includes("0.4.1")) fails.push(`版本行异常: ${m.line}`);

// 抓下载区截图
await page.evaluate(() => {
  document.querySelector("#download").scrollIntoView({ block: "start" });
});
await page.waitForTimeout(700);
await page.screenshot({ path: path.join(".dl-check", "download-section.png") });

// 实际点击验证是否触发下载
const [download] = await Promise.all([
  page.waitForEvent("download", { timeout: 40000 }).catch(() => null),
  page.click('[data-dl-btn="android"]'),
]);
if (download) {
  console.log("\n点击下载触发:", download.suggestedFilename());
  const p = await download.path();
  if (p) {
    const st = await fs.stat(p);
    console.log(`  下载大小 ${(st.size / 1048576).toFixed(1)} MB`);
    if (st.size < 20 * 1048576) fails.push(`下载文件过小 ${st.size}`);
  }
} else {
  fails.push("点击未触发下载");
}

await browser.close();
console.log("\n运行期错误:", problems.length ? [...new Set(problems)].join(" | ") : "无");
console.log(fails.length ? "FAIL\n - " + fails.join("\n - ") : "\n下载区验证通过");
process.exit(fails.length || problems.length ? 1 : 0);
