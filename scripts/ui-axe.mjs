#!/usr/bin/env node
/* 无障碍审计：用 axe-core 而不是自写规则，覆盖 WCAG 2.1 A/AA。
   输出按 impact 排序；serious / critical 视为失败。
   用法：node scripts/ui-axe.mjs [http://127.0.0.1:7999]
*/
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
let chromium, axePath;
try {
  ({ chromium } = require('playwright'));
  axePath = require.resolve('axe-core/axe.min.js');
} catch { console.error('需要 playwright 与 axe-core：npm i'); process.exit(2); }

const base = (process.argv[2] || 'http://127.0.0.1:7999').replace(/\/+$/, '');
const CHROME = process.env.CHROME_PATH
  || (process.env.LOCALAPPDATA ? process.env.LOCALAPPDATA + '\\ms-playwright\\chromium-1234\\chrome-win64\\chrome.exe' : undefined);

const PAGES = [
  ['overview', '概览'], ['onboard', '接入渠道'], ['channels', '账号与额度'], ['usage', '用量'],
  ['scheduler', '唤醒任务'], ['launcher', '应用多开'], ['sessions', '会话'], ['library', '库'],
  ['logs', '请求日志'], ['gateway', '网关与探针'], ['twofa', '2FA 管理'], ['settings', '设置'],
  ['agents', 'Agent 接入'], ['routing', '路由'], ['masking', '脱敏'], ['plugins', '插件'],
];

const browser = await chromium.launch(CHROME ? { executablePath: CHROME } : {});
const page = await browser.newPage({ viewport: { width: 1440, height: 900 } });
// 页面 CSP 是 script-src 'self'，不能内联注入；把 axe 伪装成同源脚本再加载
await page.route('**/axe.min.js', (route) => route.fulfill({
  path: axePath, contentType: 'application/javascript',
}));
const findings = [];
let checked = 0;

async function runPages(theme, pages) {
  await page.evaluate((t) => localStorage.setItem('aether.theme', t), theme);
  for (const [route, title] of pages) {
    await page.goto(base + '/#/' + route, { waitUntil: 'domcontentloaded', timeout: 20000 });
    await page.waitForTimeout(900);
    await page.addScriptTag({ url: '/axe.min.js' });
    const res = await page.evaluate(async () => {
      const r = await axe.run(document, {
        runOnly: { type: 'tag', values: ['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa'] },
        resultTypes: ['violations'],
      });
      return r.violations.map((v) => ({
        id: v.id, impact: v.impact, help: v.help,
        nodes: v.nodes.slice(0, 3).map((n) => n.target.join(' ')),
        count: v.nodes.length,
      }));
    });
    checked++;
    for (const v of res) findings.push({ theme, page: title, ...v });
    const bad = res.filter((v) => v.impact === 'serious' || v.impact === 'critical');
    console.log(`${bad.length ? 'FAIL' : 'PASS'}  ${theme === 'dark' ? '暗色 · ' : ''}${title}` + (bad.length ? '   ' + bad.map((v) => v.id + '×' + v.count).join(', ') : ''));
  }
}

await page.goto(base + '/#/overview', { waitUntil: 'domcontentloaded', timeout: 20000 });
await page.waitForTimeout(900);
await runPages('light', PAGES);
await runPages('dark', PAGES.slice(0, 6));
await page.evaluate(() => localStorage.setItem('aether.theme', 'auto'));
await browser.close();

const order = { critical: 0, serious: 1, moderate: 2, minor: 3 };
findings.sort((a, b) => order[a.impact] - order[b.impact]);
if (findings.length) {
  console.log('\n违规明细');
  for (const f of findings) {
    console.log(`  [${f.impact}] ${f.page}${f.theme === 'dark' ? '（暗色）' : ''} · ${f.id} ×${f.count}`);
    console.log(`      ${f.help}`);
    for (const t of f.nodes) console.log(`      → ${t}`);
  }
}
const severe = findings.filter((f) => f.impact === 'serious' || f.impact === 'critical').length;
console.log(`\n检查 ${checked} 个页面状态 · 违规类型 ${findings.length} · 严重 ${severe}`);
process.exit(severe ? 1 : 0);
