import { chromium } from "playwright";

const url = process.argv[2] || "http://127.0.0.1:8140/index.html";
const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});

const fails = [];
const sizes = [
  [390, 844, "phone"],
  [768, 1024, "tablet"],
  [1024, 800, "small-laptop"],
  [1440, 900, "desktop"],
];

for (const theme of ["dark", "light", "glass"]) {
  for (const [w, h, tag] of sizes) {
    const ctx = await browser.newContext({
      viewport: { width: w, height: h },
      isMobile: w < 500,
    });
    await ctx.addInitScript((t) => {
      try { localStorage.setItem("aether-theme", t); } catch (e) {}
    }, theme);
    const page = await ctx.newPage();
    await page.goto(url, { waitUntil: "load" });
    await page.waitForFunction(
      () => document.documentElement.classList.contains("is-ready"),
      null,
      { timeout: 15000 }
    );
    await page.waitForTimeout(500);

    const m = await page.evaluate(() => {
      const h1 = document.querySelector(".hero-title");
      const r = h1 ? h1.getBoundingClientRect() : null;
      const copy = document.querySelector(".hero-copy");
      const copyBox = copy ? copy.getBoundingClientRect() : null;
      return {
        overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth,
        // 竖排 bug 的特征：标题宽度远小于容器宽度
        titleW: r ? Math.round(r.width) : 0,
        copyW: copyBox ? Math.round(copyBox.width) : 0,
        heroH: Math.round(document.querySelector(".hero").getBoundingClientRect().height),
      };
    });

    // 标题宽度至少要达到内容区的一半，否则就是被挤成了窄列
    const ok = m.titleW >= m.copyW * 0.6;
    console.log(
      `${theme.padEnd(5)} ${tag.padEnd(13)} w=${w} titleW=${m.titleW} copyW=${m.copyW} ` +
        `heroH=${m.heroH} overflow=${m.overflow} ${ok ? "OK" : "NARROW!"}`
    );
    if (!ok) fails.push(`${theme}/${tag}: 标题被挤成窄列 (${m.titleW}/${m.copyW})`);
    if (m.overflow > 0) fails.push(`${theme}/${tag}: 横向溢出 ${m.overflow}px`);
    await ctx.close();
  }
}

await browser.close();
if (fails.length) {
  console.log("\nFAIL\n - " + fails.join("\n - "));
  process.exit(1);
}
console.log("\n通过：三主题 × 四尺寸，标题均正常横排且无横向溢出");
