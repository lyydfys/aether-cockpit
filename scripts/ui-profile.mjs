#!/usr/bin/env node
/* 前端瓶颈画像：逐页输出 Layout/Style 耗时、CSS 规则命中率、LCP 元素。
   用法：node scripts/ui-profile.mjs [url]
*/
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { chromium } = require('playwright');

const base = (process.argv[2] || 'http://127.0.0.1:7999').replace(/\/+$/, '');
const CHROME = process.env.CHROME_PATH
  || (process.env.LOCALAPPDATA ? process.env.LOCALAPPDATA + '\\ms-playwright\\chromium-1234\\chrome-win64\\chrome.exe' : undefined);
const ROUTES = ['overview', 'channels', 'usage', 'settings', 'gateway', 'logs'];

const browser = await chromium.launch(CHROME ? { executablePath: CHROME } : {});

async function measure(route) {
  const ctx = await browser.newContext({ viewport: { width: 412, height: 823 }, storageState: undefined });
  const page = await ctx.newPage();
  const cdp = await ctx.newCDPSession(page);
  await cdp.send('Performance.enable');
  await cdp.send('Emulation.setCPUThrottlingRate', { rate: 4 });
  await page.addInitScript(() => {
    window.__lcp = [];
    new PerformanceObserver((l) => {
      for (const e of l.getEntries()) {
        window.__lcp.push({ t: Math.round(e.startTime), size: e.size, el: (e.element?.className || e.element?.tagName || '').toString().slice(0, 24) });
      }
    }).observe({ type: 'largest-contentful-paint', buffered: true });
  });
  await page.goto(`${base}/#/${route}`, { waitUntil: 'load' });
  await page.waitForTimeout(3000);
  const m = await cdp.send('Performance.getMetrics');
  const g = (n) => Math.round((m.metrics.find((x) => x.name === n)?.value || 0) * 1000);
  const info = await page.evaluate(() => ({
    nodes: document.querySelectorAll('*').length,
    lcp: (window.__lcp || []).slice(-1)[0] || null,
  }));
  await ctx.close();
  return { route, layout: g('LayoutDuration'), style: g('RecalcStyleDuration'), ...info };
}

console.log('页面'.padEnd(12) + 'Layout'.padStart(9) + 'Style'.padStart(8) + '节点'.padStart(6) + '  LCP 元素');
const rows = [];
for (const route of ROUTES) {
  const r = await measure(route);
  rows.push(r);
  console.log(
    route.padEnd(12) + String(r.layout + 'ms').padStart(9) + String(r.style + 'ms').padStart(8)
    + String(r.nodes).padStart(6) + '  ' + (r.lcp ? `${r.lcp.el} @${r.lcp.t}ms (${r.lcp.size}px²)` : '—'),
  );
}

// CSS 规则命中率：同一页面里把六个路由走一遍
const ctx = await browser.newContext({ viewport: { width: 412, height: 823 } });
const page = await ctx.newPage();
const cdp = await ctx.newCDPSession(page);
await cdp.send('DOM.enable');
await cdp.send('CSS.enable');
await page.goto(`${base}/#/overview`, { waitUntil: 'load' });
await page.waitForTimeout(800);
await cdp.send('CSS.startRuleUsageTracking');
for (const route of ROUTES) {
  await page.evaluate((x) => { location.hash = '#/' + x; }, route);
  await page.waitForTimeout(700);
}
const { ruleUsage } = await cdp.send('CSS.stopRuleUsageTracking');
const used = ruleUsage.filter((r) => r.used).length;
console.log(`\nCSS 规则 ${used}/${ruleUsage.length} 命中（${Math.round((used / ruleUsage.length) * 100)}%）`);

const avgLayout = Math.round(rows.reduce((a, r) => a + r.layout, 0) / rows.length);
console.log(`平均 Layout ${avgLayout}ms（4× CPU 降速）——远高于脚本耗时时，瓶颈在布局而非 JS`);

await browser.close();
