#!/usr/bin/env node
/* 跨内核检查：同一个页面在 Chromium / Firefox 下加载、布局与关键样式是否一致。
   用法：node scripts/ui-cross-browser.mjs [url]
*/
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const { chromium, firefox } = require('playwright');

const base = (process.argv[2] || 'http://127.0.0.1:7999').replace(/\/+$/, '');
const ROUTES = [
  ['overview', '概览'],
  ['channels', '账号与额度'],
  ['usage', '用量'],
  ['settings', '设置'],
  ['logs', '请求日志'],
];

const results = [];
const pass = (n, d = '') => results.push({ ok: true, n, d });
const fail = (n, d = '') => results.push({ ok: false, n, d });

for (const [name, type] of [['chromium', chromium], ['firefox', firefox]]) {
  let browser;
  try {
    const CHROME = process.env.CHROME_PATH
      || (process.env.LOCALAPPDATA ? process.env.LOCALAPPDATA + '\\ms-playwright\\chromium-1234\\chrome-win64\\chrome.exe' : undefined);
    browser = await type.launch(name === 'chromium' && CHROME ? { executablePath: CHROME } : {});
  } catch (e) {
    fail(`${name} 启动`, e.message.split('\n')[0]);
    continue;
  }
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });

  for (const [route, title] of ROUTES) {
    await page.goto(`${base}/#/${route}`, { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(900);
    const info = await page.evaluate(() => {
      const q = (sel) => document.querySelector(sel);
      const cs = (sel) => { const el = q(sel); return el ? getComputedStyle(el) : null; };
      const card = cs('.card') || cs('.acct-card');
      const btn = q('.btn:not(.sm)');
      const meter = q('.meter');
      return {
        h1: document.querySelector('.head h1')?.textContent || '',
        overflow: Math.max(0, document.documentElement.scrollWidth - innerWidth),
        radius: card ? Math.round(parseFloat(card.borderTopLeftRadius)) : null,
        btnH: btn ? Math.round(btn.getBoundingClientRect().height) : null,
        meterH: meter ? Math.round(meter.getBoundingClientRect().height) : null,
        inter: getComputedStyle(document.body).fontFamily.includes('Inter'),
      };
    });
    const label = `${name} · ${title}`;
    const issues = [];
    if (info.overflow > 1) issues.push(`溢出 ${info.overflow}px`);
    if (!info.h1) issues.push('无标题');
    if (info.radius !== null && info.radius !== 16) issues.push(`卡片圆角 ${info.radius}≠16`);
    if (info.btnH !== null && info.btnH !== 32) issues.push(`按钮高 ${info.btnH}≠32`);
    if (info.meterH !== null && info.meterH !== 6) issues.push(`进度条高 ${info.meterH}≠6`);
    if (info.h1 !== title) issues.push(`标题「${info.h1}」≠「${title}」`);
    if (issues.length) fail(label, issues.join(' / '));
    else pass(label);
  }

  // 命令面板与语言按钮
  await page.goto(`${base}/#/overview`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(700);
  await page.keyboard.press('Control+K');
  await page.waitForTimeout(300);
  const palette = await page.locator('.cmdk').count();
  if (palette === 1) pass(`${name} · 命令面板`);
  else fail(`${name} · 命令面板`, '未打开');
  await page.keyboard.press('Escape');

  if (errors.length) fail(`${name} · 控制台`, errors.slice(0, 2).join(' / '));
  else pass(`${name} · 控制台无错误`);

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`${base}/#/channels`, { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(800);
  const narrow = await page.evaluate(() => Math.max(0, document.documentElement.scrollWidth - innerWidth));
  if (narrow <= 1) pass(`${name} · 390px 无溢出`);
  else fail(`${name} · 390px 溢出`, narrow + 'px');

  await browser.close();
}

for (const r of results) console.log((r.ok ? 'PASS  ' : 'FAIL  ') + r.n + (r.d ? '   ' + r.d : ''));
const bad = results.filter((r) => !r.ok).length;
console.log(`\n通过 ${results.length - bad} · 失败 ${bad}`);
process.exit(bad ? 1 : 0);
