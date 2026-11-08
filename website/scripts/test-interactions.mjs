/*
 * 交互回归：标签页切换、路由策略切换、复制按钮、FAQ 展开、跳过开屏、减少动效降级。
 * 用法：node scripts/test-interactions.mjs [url]
 */
import { chromium } from "playwright";

const url = process.argv[2] || "http://127.0.0.1:8125/index.html";
const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});

const fails = [];
const ok = (cond, label, extra = "") => {
  if (!cond) fails.push(`${label} ${extra}`.trim());
  console.log(`${cond ? "PASS" : "FAIL"}  ${label}${extra ? "  " + extra : ""}`);
};

/* ---------- 1. 正常路径 ---------- */
{
  const page = await browser.newPage({
    viewport: { width: 1440, height: 1000 },
    permissions: ["clipboard-read", "clipboard-write"],
  });
  const errs = [];
  page.on("pageerror", (e) => errs.push(e.message));
  await page.goto(url, { waitUntil: "load" });

  // 开屏会自动结束
  await page.waitForFunction(
    () => document.documentElement.classList.contains("is-ready"),
    null,
    { timeout: 12000 }
  );
  await page.waitForTimeout(900);
  const ignitionGone = await page.evaluate(() => {
    const el = document.getElementById("ignition");
    return el.style.display === "none" || getComputedStyle(el).visibility === "hidden";
  });
  ok(ignitionGone, "开屏自动结束并让位");
  ok(
    await page.evaluate(() => getComputedStyle(document.querySelector(".site-nav .nav-mark-slot svg")).opacity === "1"),
    "导航标记在开屏后可见"
  );

  // 标签页
  await page.click("#tab-usage");
  await page.waitForTimeout(200);
  const usageOn = await page.evaluate(() => {
    const shot = document.querySelector('.stage-shot[data-shot="usage"]');
    const cap = document.querySelector('[data-caption-for="usage"]');
    return shot.dataset.active === "true" && !cap.hidden;
  });
  ok(usageOn, "工作台标签切到「用量」");

  await page.keyboard.press("ArrowLeft"); // 焦点在用量 tab 上
  await page.waitForTimeout(200);
  const backToChannels = await page.evaluate(
    () => document.querySelector("#tab-channels").getAttribute("aria-selected") === "true"
  );
  ok(backToChannels, "方向键切换标签页");

  // 路由策略
  await page.click('[data-strategy="quota"]');
  await page.waitForTimeout(200);
  const quota = await page.evaluate(() => ({
    note: document.getElementById("route-note").textContent.trim(),
    hitB: document.querySelector('[data-node="a"], [data-node="b"]') && null,
    live: [...document.querySelectorAll(".route-svg path[data-live='true']")].length,
  }));
  ok(quota.note.includes("quota-aware"), "路由策略切换更新文案", quota.note);
  ok(quota.live === 1, "同时只高亮一条链路", `live=${quota.live}`);

  // 复制
  await page.click(".endpoint-row .copy-btn");
  await page.waitForTimeout(200);
  const clip = await page.evaluate(() => navigator.clipboard.readText().catch(() => ""));
  ok(clip.includes("127.0.0.1:7860"), "复制端点写入剪贴板", JSON.stringify(clip));

  // FAQ
  const faqBefore = await page.evaluate(() => document.querySelectorAll(".faq details[open]").length);
  await page.click(".faq details:nth-of-type(2) summary");
  await page.waitForTimeout(150);
  const faqAfter = await page.evaluate(() => document.querySelectorAll(".faq details[open]").length);
  ok(faqAfter > faqBefore, "FAQ 可展开", `${faqBefore} → ${faqAfter}`);

  // 锚点导航
  await page.click('.nav-links a[href="#download"]');
  await page.waitForTimeout(900);
  const nearDownload = await page.evaluate(() => {
    const y = document.getElementById("download").getBoundingClientRect().top;
    return Math.abs(y - 76) < 60;
  });
  ok(nearDownload, "锚点跳转补偿固定导航高度");

  ok(errs.length === 0, "无运行期错误", errs.join("; "));
  await page.close();
}

/* ---------- 2. 跳过开屏 ---------- */
{
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } });
  await page.goto(url, { waitUntil: "load" });
  await page.waitForTimeout(500);
  await page.keyboard.press("Escape");
  await page.waitForFunction(
    () => document.documentElement.classList.contains("is-ready"),
    null,
    { timeout: 4000 }
  );
  ok(true, "Esc 可跳过开屏");
  await page.waitForTimeout(1000);
  ok(
    await page.evaluate(() => document.getElementById("ignition").style.display === "none"),
    "跳过之后开屏层被移除"
  );
  await page.close();
}

/* ---------- 3. 减少动效 ---------- */
{
  const page = await browser.newPage({
    viewport: { width: 1280, height: 900 },
    reducedMotion: "reduce",
  });
  await page.goto(url, { waitUntil: "load" });
  await page.waitForFunction(
    () => document.documentElement.classList.contains("is-ready"),
    null,
    { timeout: 8000 }
  );
  ok(true, "prefers-reduced-motion 下开屏仍能结束");
  await page.waitForTimeout(400);
  ok(
    await page.evaluate(() => {
      const c = document.querySelector(".stage-shot[data-shot='overview']");
      return getComputedStyle(c).opacity === "1";
    }),
    "降级模式下内容正常渲染"
  );
  await page.close();
}

/* ---------- 4. 无 JS ---------- */
{
  const ctx = await browser.newContext({ javaScriptEnabled: false });
  const page = await ctx.newPage();
  await page.goto(url, { waitUntil: "load" });
  const visible = await page.evaluate(() => {
    const el = document.getElementById("ignition");
    return getComputedStyle(el).display === "none";
  });
  ok(visible, "<noscript> 兜底隐藏开屏");
  await ctx.close();
}

await browser.close();
console.log(fails.length ? `\n${fails.length} 项未通过` : "\n全部通过");
process.exit(fails.length ? 1 : 0);
