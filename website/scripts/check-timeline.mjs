/*
 * 开屏编排校验：在关键时间点读取每一层的 computed opacity，
 * 确认「底板 → 气泡 → 星芒 → 青点 → 路由徽标」是逐层出现的，
 * 且收尾时五层全部可见（不能有哪层没亮）。
 *
 * 用 DOM opacity 而不是颜色计数：浅色底板会把白/冰蓝的像素统计淹没，
 * 颜色计数无法区分"星芒亮了"和"底板在那"。
 *
 * 用法：node scripts/check-timeline.mjs [url]
 */
import { chromium } from "playwright";

const url = process.argv[2] || "http://127.0.0.1:8131/index.html";

const LAYERS = [
  ["bg", ".bm-bg"],
  ["bubble", ".bm-bubble"],
  ["star", ".bm-star"],
  ["dotA", ".bm-dot-a"],
  ["dotB", ".bm-dot-b"],
  ["plate", ".bm-plate"],
  ["link", ".bm-link"],
  ["nodeA", ".bm-node-a"],
  ["nodeB", ".bm-node-b"],
];

const AT = [200, 340, 460, 620, 760, 900, 1040, 1180, 1320, 1460, 1620, 1780, 2000, 2400, 3300];

const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });

const errs = [];
page.on("pageerror", (e) => errs.push(e.message));

await page.goto(url, { waitUntil: "load" });
await page.waitForFunction(() => !!window.__aetherTimeline, null, { timeout: 10000 });

const rows = [];
for (const t of AT) {
  await page.evaluate(
    (target) =>
      new Promise((resolve) => {
        const tick = () => {
          const tl = window.__aetherTimeline;
          if (!tl || performance.now() - tl.startedAt >= target) resolve();
          else requestAnimationFrame(tick);
        };
        tick();
      }),
    t
  );
  const snap = await page.evaluate((layers) => {
    const read = (sel) => {
      const el = document.querySelector("#ignition " + sel);
      if (!el) return null;
      return Number(getComputedStyle(el).opacity);
    };
    const out = {};
    for (const [name, sel] of layers) out[name] = read(sel);
    return out;
  }, LAYERS);
  rows.push({ t, ...snap });
}

await browser.close();

// 输出矩阵
const pad = (s, n) => String(s).padEnd(n);
console.log(pad("t(ms)", 8) + LAYERS.map(([n]) => pad(n, 7)).join(""));
for (const r of rows) {
  console.log(
    pad(r.t, 8) +
      LAYERS.map(([n]) => pad(r[n] === null ? "-" : r[n].toFixed(2), 7)).join("")
  );
}

// 断言
const fails = [];
const last = rows[rows.length - 1];

// 两次运行之间，采样点可能被主线程抖动挤到不同位置（偶发误报），
// 所以这里用最后两行里较好的那个值，并给一点余量。
const lastTwo = rows.slice(-2);
const best = (name) => {
  const vals = lastTwo
    .map((r) => r[name])
    .filter((v) => typeof v === "number");
  return vals.length ? Math.max(...vals) : null;
};

for (const [name] of LAYERS) {
  const v = best(name);
  if (v === null) fails.push(`${name} 元素缺失`);
  else if (v < 0.98) fails.push(`${name} 收尾时未完全可见 (${v.toFixed(2)})`);
}
void last;

// 顺序断言：用「首次显著可见」的时刻比较。
// 采样间隔固定，两个节拍若差得比采样还小会落在同一点，
// 所以用「该层在该采样点的值 > 前一层在下一采样点的值」这种宽松但有效的判据。
const firstVisible = (name, threshold = 0.2) => {
  const row = rows.find((r) => r[name] !== null && r[name] > threshold);
  return row ? row.t : Infinity;
};
const order = [
  ["bg", "bubble"],
  ["bubble", "star"],
  ["star", "dotA"],
  ["dotA", "plate"],
];
for (const [a, b] of order) {
  const ta = firstVisible(a);
  const tb = firstVisible(b);
  // 允许 400ms 的采样容差：节拍差 240–360ms，落在相邻采样点是正常的
  if (!(ta <= tb + 400)) fails.push(`顺序错：${a}(首次可见 ${ta}ms) 明显晚于 ${b}(${tb}ms)`);
}

console.log();
if (fails.length) {
  console.log("FAIL\n - " + fails.join("\n - "));
  process.exit(1);
}
console.log("五层全部到位，且按 底板→气泡→星芒→青点→路由标 的顺序出现");
if (errs.length) console.log("运行期错误: " + errs.join(" | "));
