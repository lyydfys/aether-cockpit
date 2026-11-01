#!/usr/bin/env node
/* UI 质量门禁：把「设计上说过的话」变成可执行断言。
   检查四类：横向溢出 / 文本对比度 / 控件可访问名 / 焦点与 live region。

   用法：node scripts/ui-audit.mjs [http://127.0.0.1:7999]
   退出码 0 = 全过；1 = 有失败；2 = 环境问题。
*/
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
let chromium;
try { ({ chromium } = require('playwright')); }
catch { console.error('需要 playwright：npm i playwright'); process.exit(2); }

const base = (process.argv[2] || 'http://127.0.0.1:7999').replace(/\/+$/, '');
const CHROME = process.env.CHROME_PATH
  || (process.env.LOCALAPPDATA ? process.env.LOCALAPPDATA + '\\ms-playwright\\chromium-1234\\chrome-win64\\chrome.exe' : undefined);

const PAGES = [
  ['overview', '概览'],
  ['onboard', '接入渠道'],
  ['channels', '账号与额度'],
  ['usage', '用量'],
  ['scheduler', '唤醒任务'],
  ['launcher', '应用多开'],
  ['sessions', '会话'],
  ['library', '库'],
  ['logs', '请求日志'],
  ['gateway', '网关与探针'],
  ['twofa', '2FA 管理'],
  ['settings', '设置'],
  ['agents', 'Agent 接入'],
  ['routing', '路由'],
  ['masking', '脱敏'],
  ['plugins', '插件'],
];
const WIDTHS = [[1440, 900, '桌面'], [768, 900, '平板'], [390, 844, '手机']];

const results = [];
const pass = (n, d = '') => results.push({ ok: true, n, d });
const fail = (n, d = '') => results.push({ ok: false, n, d });

const browser = await chromium.launch(CHROME ? { executablePath: CHROME } : {});
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
const errors = [];
page.on('pageerror', (e) => errors.push(e.message));
page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });

/* 对比度：只测「单文本节点 + 可见 + 非 SVG + 非禁用」的元素 */
const CONTRAST_JS = () => {
  const parse = (s) => (s.match(/[\d.]+/g) || []).map(Number);
  const lum = ([r, g, b]) => {
    const f = (c) => { c /= 255; return c <= 0.03928 ? c / 12.92 : Math.pow((c + 0.055) / 1.055, 2.4); };
    return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
  };
  // 逐层合成半透明背景，避免把 chip 的 alpha 底当作白底来算
  const bgOf = (el) => {
    const layers = [];
    let n = el;
    while (n) {
      const c = parse(getComputedStyle(n).backgroundColor);
      if (c.length >= 3 && (c[3] === undefined || c[3] > 0)) {
        layers.push(c);
        if (c[3] === undefined || c[3] >= 1) break;
      }
      n = n.parentElement;
    }
    if (!layers.length) return [255, 255, 255];
    let out = [255, 255, 255];
    for (let i = layers.length - 1; i >= 0; i--) {
      const [r, g, b, a = 1] = layers[i];
      out = [r * a + out[0] * (1 - a), g * a + out[1] * (1 - a), b * a + out[2] * (1 - a)];
    }
    return out;
  };
  const ratio = (a, b) => {
    const l1 = lum(a), l2 = lum(b);
    return (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05);
  };
  const bad = [];
  for (const el of document.querySelectorAll('body *')) {
    if (el.closest('svg')) continue;
    if (el.childNodes.length !== 1 || el.firstChild.nodeType !== 3) continue;
    const text = el.textContent.trim();
    if (!text) continue;
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.display === 'none') continue;
    if (el.closest('[disabled]') || Number(cs.opacity) < 0.6) continue;
    const rect = el.getBoundingClientRect();
    if (rect.width < 2 || rect.height < 2) continue;
    const size = parseFloat(cs.fontSize);
    const bold = Number(cs.fontWeight) >= 600;
    const min = size >= 24 || (bold && size >= 18.66) ? 3 : 4.5;
    const r = ratio(parse(cs.color), bgOf(el));
    if (r < min) bad.push(`${text.slice(0, 14)} ${size}px ${r.toFixed(2)}:1 < ${min}`);
  }
  return bad;
};

const FORM_JS = () => {
  const bad = [];
  for (const el of document.querySelectorAll('input,select,textarea')) {
    if (el.type === 'hidden') continue;
    if (el.getAttribute('aria-label') || el.getAttribute('aria-labelledby') || el.labels?.length || el.title) continue;
    bad.push(el.outerHTML.slice(0, 60));
  }
  return bad;
};

const BTN_JS = () => [...document.querySelectorAll('button')]
  .filter((b) => !b.getAttribute('aria-label') && !b.textContent.trim())
  .map((b) => b.outerHTML.slice(0, 60));

try {
  for (const [w, h, label] of WIDTHS) {
    await page.setViewportSize({ width: w, height: h });
    for (const [route, title] of PAGES) {
      await page.goto(base + '/#/' + route, { waitUntil: 'networkidle' });
      await page.waitForTimeout(1000);

      const over = await page.evaluate(() => Math.max(0, document.documentElement.scrollWidth - innerWidth));
      if (over <= 1) pass(`${label} · ${title} 无横向溢出`);
      else fail(`${label} · ${title} 横向溢出`, over + 'px');

      if (w === 1440) {
        const bad = await page.evaluate(CONTRAST_JS);
        if (!bad.length) pass(`${title} 对比度`);
        else fail(`${title} 对比度`, bad.slice(0, 4).join(' / ') + (bad.length > 4 ? ` …共 ${bad.length}` : ''));

        const forms = await page.evaluate(FORM_JS);
        if (!forms.length) pass(`${title} 控件命名`);
        else fail(`${title} 控件命名`, forms.join(' | '));

        const btns = await page.evaluate(BTN_JS);
        if (!btns.length) pass(`${title} 按钮命名`);
        else fail(`${title} 按钮命名`, btns.join(' | '));
      }
    }
  }

  // 暗色对比度：主题存在 localStorage，逐页导航会重新读取
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.evaluate(() => localStorage.setItem('aether.theme', 'dark'));
  for (const [route, title] of PAGES.slice(0, 6)) {
    await page.goto(base + '/#/' + route, { waitUntil: 'networkidle' });
    await page.waitForTimeout(600);
    const bad = await page.evaluate(CONTRAST_JS);
    if (!bad.length) pass(`${title} 暗色对比度`);
    else fail(`${title} 暗色对比度`, bad.slice(0, 3).join(' / '));
  }
  await page.evaluate(() => localStorage.setItem('aether.theme', 'auto'));

  await page.goto(base + '/#/overview', { waitUntil: 'networkidle' });
  await page.waitForTimeout(600);

  const live = await page.evaluate(() => ({
    app: document.getElementById('app')?.getAttribute('aria-live'),
    regions: document.querySelectorAll('[aria-live]').length,
  }));
  if (!live.app && live.regions <= 2) pass('live region 收敛', String(live.regions) + ' 处');
  else fail('live region 收敛', 'app=' + live.app + ' regions=' + live.regions);

  await page.evaluate(() => document.activeElement?.blur());
  await page.keyboard.press('Tab');
  const first = await page.evaluate(() => (document.activeElement.className || '') + '|' + (document.activeElement.textContent || '').trim().slice(0, 8));
  if (first.startsWith('skip|跳到主内容')) pass('焦点起点', first);
  else fail('焦点起点', first);

  if (!errors.length) pass('控制台无错误');
  else fail('控制台无错误', errors.slice(0, 2).join(' / '));
} finally {
  await browser.close();
}

for (const r of results) console.log((r.ok ? 'PASS  ' : 'FAIL  ') + r.n + (r.d ? '   ' + r.d : ''));
const bad = results.filter((r) => !r.ok).length;
console.log(`\n通过 ${results.length - bad} · 失败 ${bad}`);
process.exit(bad ? 1 : 0);
