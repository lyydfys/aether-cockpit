import { chromium, devices } from "playwright";

const url = process.argv[2] || "http://127.0.0.1:8140/index.html";
const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});

const fails = [];
const rows = [];

/* WCAG 相对亮度与对比度 */
function parseRgb(str) {
  const m = str.match(/rgba?\((\d+)[,\s]+(\d+)[,\s]+(\d+)/i);
  if (m) return [Number(m[1]), Number(m[2]), Number(m[3])];
  const h = str.match(/^#([0-9a-f]{6})$/i);
  if (h) {
    const v = h[1];
    return [
      parseInt(v.slice(0, 2), 16),
      parseInt(v.slice(2, 4), 16),
      parseInt(v.slice(4, 6), 16),
    ];
  }
  return null;
}
function lum(rgb) {
  const f = (c) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : Math.pow((s + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * f(rgb[0]) + 0.7152 * f(rgb[1]) + 0.0722 * f(rgb[2]);
}
function contrast(a, b) {
  const la = lum(a);
  const lb = lum(b);
  const hi = Math.max(la, lb);
  const lo = Math.min(la, lb);
  return (hi + 0.05) / (lo + 0.05);
}

for (const theme of ["glass", "dark", "light"]) {
  for (const [tag, conf] of [
    ["desk", { viewport: { width: 1440, height: 900 } }],
    ["phone", devices["iPhone 13"]],
  ]) {
    const ctx = await browser.newContext({ ...conf });
    await ctx.addInitScript((t) => {
      try { localStorage.setItem("aether-theme", t); } catch (e) {}
    }, theme);
    const page = await ctx.newPage();
    await page.goto(url, { waitUntil: "load" });
    await page.waitForFunction(() => !!window.__aetherTimeline, null, { timeout: 10000 });
    await page.evaluate(
      () =>
        new Promise((resolve) => {
          const tick = () => {
            const tl = window.__aetherTimeline;
            if (!tl || performance.now() - tl.startedAt >= 1650) resolve();
            else requestAnimationFrame(tick);
          };
          tick();
        })
    );
    await page.waitForTimeout(260);

    const m = await page.evaluate(() => {
      const cs = (el) => (el ? getComputedStyle(el) : null);
      return {
        stillInIgnition: document.getElementById("ignition").style.display !== "none",
        bg: cs(document.getElementById("ignition")).backgroundColor,
        nameColor: cs(document.querySelector(".ignition-name"))?.color,
        nameSize: parseFloat(cs(document.querySelector(".ignition-name"))?.fontSize || "0"),
        tagColor: cs(document.querySelector(".ignition-tag"))?.color,
        tagSize: parseFloat(cs(document.querySelector(".ignition-tag"))?.fontSize || "0"),
      };
    });

    const bg = parseRgb(m.bg);
    const nameC = parseRgb(m.nameColor);
    const tagC = parseRgb(m.tagColor);
    const ratioName = bg && nameC ? contrast(nameC, bg) : 0;
    const ratioTag = bg && tagC ? contrast(tagC, bg) : 0;

    // 字标 19-26px 属大字（≥18.66px 且 bold），AA 门槛 3:1；这里要求 ≥4.5
    const okName = ratioName >= 4.5;
    // 副标 <18.66px 属小字，AA 门槛 4.5:1
    const okTag = ratioTag >= 4.5;

    rows.push({ theme, tag, ...m, ratioName, ratioTag, okName, okTag });

    console.log(
      `${theme.padEnd(5)} ${tag.padEnd(6)} bg=${m.bg}\n` +
        `   name ${m.nameColor} ${m.nameSize}px  contrast=${ratioName.toFixed(2)} ${okName ? "OK" : "LOW"}\n` +
        `   tag  ${m.tagColor} ${m.tagSize}px  contrast=${ratioTag.toFixed(2)} ${okTag ? "OK" : "LOW"}`
    );

    if (!okName) fails.push(`${theme}/${tag} 主标题对比度 ${ratioName.toFixed(2)} < 4.5`);
    if (!okTag) fails.push(`${theme}/${tag} 副标对比度 ${ratioTag.toFixed(2)} < 4.5`);
    if (!(m.nameSize >= 19)) fails.push(`${theme}/${tag} 主标题字号 ${m.nameSize}px < 19`);
    if (!(m.tagSize >= 12.5)) fails.push(`${theme}/${tag} 副标字号 ${m.tagSize}px < 12.5`);

    await ctx.close();
  }
}

await browser.close();
console.log("\n" + (fails.length ? "FAIL\n - " + fails.join("\n - ") : "通过：三主题 × 桌面/手机，对比度与字号均达标"));
process.exit(fails.length ? 1 : 0);
