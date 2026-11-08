import { chromium, devices } from "playwright";
import path from "node:path";
import fs from "node:fs/promises";

const url = process.argv[2] || "http://127.0.0.1:8140/index.html";
const out = path.resolve(".review-m");
await fs.mkdir(out, { recursive: true });

const browser = await chromium.launch({
  executablePath: process.env.AETHER_CHROME || undefined,
});

/* 真实手机设备描述符 */
const DEVICES = [
  ["iPhone SE", devices["iPhone SE"]],
  ["iPhone 13", devices["iPhone 13"]],
  ["iPhone 15 Pro Max", devices["iPhone 15 Pro Max"]],
  ["Pixel 7", devices["Pixel 7"]],
  ["Galaxy S9+", devices["Galaxy S9+"]],
  ["iPad Mini", devices["iPad Mini"]],
];

const fails = [];
const report = [];

for (const [name, device] of DEVICES) {
  const ctx = await browser.newContext({ ...device });
  const page = await ctx.newPage();
  const errs = [];
  page.on("pageerror", (e) => errs.push(e.message));

  await page.goto(url, { waitUntil: "load" });
  await page.waitForFunction(
    () => document.documentElement.classList.contains("is-ready"),
    null,
    { timeout: 15000 }
  );
  await page.waitForTimeout(600);

  const m = await page.evaluate(() => {
    const de = document.documentElement;
    const vw = de.clientWidth;

    // 触控目标尺寸检查（44px 是移动端的常见下限）
    const small = [];
    const controls = document.querySelectorAll(
      ".btn, .theme-switch button, .platform-tab, .stage-tab, .route-item, .copy-btn, .faq summary, .nav-links a"
    );
    controls.forEach((el) => {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) return; // 隐藏元素
      if (r.height < 36) {
        small.push({
          cls: el.className.toString().slice(0, 40),
          text: (el.textContent || "").trim().slice(0, 14),
          h: Math.round(r.height),
        });
      }
    });

    // 小字号检查（<12px 难读）
    const tiny = [];
    document.querySelectorAll("p, span, a, li, small, figcaption").forEach((el) => {
      const cs = getComputedStyle(el);
      const fs = parseFloat(cs.fontSize);
      if (el.getBoundingClientRect().width > 0 && fs < 12) {
        tiny.push({ fs, text: (el.textContent || "").trim().slice(0, 12) });
      }
    });

    // 横向溢出元素定位
    const overflowers = [];
    if (de.scrollWidth > de.clientWidth) {
      document.querySelectorAll("*").forEach((el) => {
        const r = el.getBoundingClientRect();
        if (r.right > vw + 1 && r.width > 8) {
          overflowers.push({
            el: el.tagName.toLowerCase() + "." + el.className.toString().slice(0, 30),
            right: Math.round(r.right),
          });
        }
      });
    }

    return {
      theme: de.dataset.theme,
      vw,
      scrollW: de.scrollWidth,
      overflowX: de.scrollWidth - de.clientWidth,
      heroH: Math.round(document.querySelector(".hero").getBoundingClientRect().height),
      titleW: Math.round(document.querySelector(".hero-title").getBoundingClientRect().width),
      phones: document.querySelectorAll(".phone-rail .phone").length,
      wallPhones: document.querySelectorAll(".phone-wall .phone").length,
      navMenuHidden: getComputedStyle(document.querySelector(".nav-links")).display,
      themeSwitchVisible: document
        .querySelector(".theme-switch")
        .getBoundingClientRect().width > 0,
      smallTargets: small.slice(0, 8),
      smallCount: small.length,
      tinyCount: tiny.length,
      tinySample: tiny.slice(0, 5),
      overflowers: overflowers.slice(0, 6),
    };
  });

  report.push({ name, ...m });
  console.log(
    `${name.padEnd(18)} vw=${m.vw} overflow=${m.overflowX} \n` +
      `  titleW=${m.titleW} heroH=${m.heroH} phones=${m.phones} wall=${m.wallPhones}\n` +
      `  navMenu=${m.navMenuHidden} themeSwitch=${m.themeSwitchVisible}\n` +
      `  小目标(${m.smallCount}) 小字(${m.tinyCount}) 溢出元素(${m.overflowers.length})`
  );
  if (m.smallCount) console.log("   small:", JSON.stringify(m.smallTargets));
  if (m.tinyCount) console.log("   tiny:", JSON.stringify(m.tinySample));
  if (m.overflowers.length) console.log("   overflow:", JSON.stringify(m.overflowers));
  if (errs.length) console.log("   ERR:", errs.join(" | "));

  if (m.overflowX > 0) fails.push(`${name}: 横向溢出 ${m.overflowX}px`);
  if (m.smallCount > 0) fails.push(`${name}: ${m.smallCount} 个触控目标过小`);
  if (errs.length) fails.push(`${name}: ${errs.join("; ")}`);

  await page.screenshot({ path: path.join(out, `m-${name.replace(/\s/g, "-")}.png`) });
  await ctx.close();
}

await browser.close();
console.log("\n" + (fails.length ? "FAIL\n - " + fails.join("\n - ") : "全部通过"));
