#!/usr/bin/env node
/* 排版体检：横向溢出、文本重叠、非省略式裁切。
   与 ui-audit 的分工：audit 管对比度/命名/溢出，这里只管「元素互相压住」。
   用法：node scripts/ui-layout.mjs [url]
*/
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { chromium } = require('playwright');

const base = (process.argv[2] || 'http://127.0.0.1:7999').replace(/\/+$/, '');
const CHROME = process.env.CHROME_PATH
  || (process.env.LOCALAPPDATA ? process.env.LOCALAPPDATA + '\\ms-playwright\\chromium-1234\\chrome-win64\\chrome.exe' : undefined);
const ROUTES = ['overview', 'channels', 'usage', 'settings', 'gateway', 'logs', 'sessions', 'library', 'scheduler', 'twofa'];
const WIDTHS = [1440, 1080, 390];

const CHECK = () => {
  const visible = (el) => {
    if (el.closest('[aria-hidden="true"], .sr-only, script, style')) return false;
    const cs = getComputedStyle(el);
    if (cs.display === 'none' || cs.visibility === 'hidden' || Number(cs.opacity) === 0) return false;
    const r = el.getBoundingClientRect();
    return r.width > 1 && r.height > 1;
  };
  const textEls = [...document.querySelectorAll('body *')].filter((el) => {
    if (!visible(el)) return false;
    if (el.closest('svg, canvas')) return false;
    const onlyText = el.childNodes.length === 1 && el.firstChild.nodeType === 3 && el.textContent.trim().length > 0;
    if (!onlyText) return false;
    const cs = getComputedStyle(el);
    return cs.position !== 'absolute' && cs.position !== 'fixed';
  });
  const clipped = [];
  for (const el of textEls) {
    // 只有「被容器裁掉」才算问题；overflow: visible 只是溢出，交给重叠检测
    const cs = getComputedStyle(el);
    const hides = cs.overflowX === 'hidden' || cs.overflowX === 'clip';
    if (hides && cs.textOverflow !== 'ellipsis' && el.scrollWidth > el.clientWidth + 2) {
      clipped.push(`${el.tagName.toLowerCase()}.${(el.className || '').toString().split(' ')[0]} 「${el.textContent.trim().slice(0, 12)}」 ${el.clientWidth}→${el.scrollWidth}`);
    }
  }
  const overlaps = [];
  for (let i = 0; i < textEls.length; i++) {
    for (let j = i + 1; j < textEls.length; j++) {
      const a = textEls[i], b = textEls[j];
      if (a.contains(b) || b.contains(a)) continue;
      const ra = a.getBoundingClientRect(), rb = b.getBoundingClientRect();
      const w = Math.min(ra.right, rb.right) - Math.max(ra.left, rb.left);
      const h = Math.min(ra.bottom, rb.bottom) - Math.max(ra.top, rb.top);
      if (w <= 1 || h <= 1) continue;
      const inter = w * h;
      const minArea = Math.min(ra.width * ra.height, rb.width * rb.height);
      if (inter / minArea > 0.35) {
        overlaps.push(`「${a.textContent.trim().slice(0, 10)}」×「${b.textContent.trim().slice(0, 10)}」`);
      }
    }
  }
  return {
    overflow: Math.max(0, document.documentElement.scrollWidth - innerWidth),
    clipped: clipped.slice(0, 4),
    overlaps: overlaps.slice(0, 4),
  };
};

const browser = await chromium.launch(CHROME ? { executablePath: CHROME } : {});
const page = await browser.newPage();
let bad = 0;
for (const width of WIDTHS) {
  await page.setViewportSize({ width, height: width === 390 ? 844 : 900 });
  for (const route of ROUTES) {
    await page.goto(`${base}/#/${route}`, { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(900);
    const r = await page.evaluate(CHECK);
    const issues = [];
    if (r.overflow > 1) issues.push(`溢出 ${r.overflow}px`);
    if (r.clipped.length) issues.push('裁切 ' + r.clipped.join(' / '));
    if (r.overlaps.length) issues.push('重叠 ' + r.overlaps.join(' / '));
    if (issues.length) {
      bad++;
      console.log(`FAIL  ${width}px · ${route}   ${issues.join(' ; ')}`);
    }
  }
}
await browser.close();
console.log(`\n${WIDTHS.length * ROUTES.length - bad} 通过 · ${bad} 失败`);
process.exit(bad ? 1 : 0);
