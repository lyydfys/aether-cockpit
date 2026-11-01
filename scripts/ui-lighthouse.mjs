#!/usr/bin/env node
/* Lighthouse 审计：桌面端 preset，四个类别全跑，输出分数与未达标项。
   用法：node scripts/ui-lighthouse.mjs [url] [--mobile]
*/
import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
const lighthouse = require('lighthouse').default || require('lighthouse');
const chromeLauncher = require('chrome-launcher');

const url = (process.argv[2] && !process.argv[2].startsWith('--') ? process.argv[2] : 'http://127.0.0.1:7999').replace(/\/+$/, '');
const mobile = process.argv.includes('--mobile');
const CHROME = process.env.CHROME_PATH
  || (process.env.LOCALAPPDATA ? process.env.LOCALAPPDATA + '\\ms-playwright\\chromium-1234\\chrome-win64\\chrome.exe' : undefined);

const chrome = await chromeLauncher.launch({
  chromePath: CHROME,
  chromeFlags: ['--headless=new', '--no-sandbox', '--disable-gpu'],
});

try {
  const runnerResult = await lighthouse(url, {
    port: chrome.port,
    output: 'json',
    logLevel: 'error',
    onlyCategories: ['performance', 'accessibility', 'best-practices', 'seo'],
    formFactor: mobile ? 'mobile' : 'desktop',
    screenEmulation: mobile
      ? { mobile: true, width: 412, height: 823, deviceScaleFactor: 1.75, disabled: false }
      : { mobile: false, width: 1440, height: 900, deviceScaleFactor: 1, disabled: false },
    throttling: mobile
      ? { rttMs: 150, throughputKbps: 1638, cpuSlowdownMultiplier: 4, requestLatencyMs: 562, downloadThroughputKbps: 1474, uploadThroughputKbps: 675 }
      : { rttMs: 40, throughputKbps: 10240, cpuSlowdownMultiplier: 1, requestLatencyMs: 0, downloadThroughputKbps: 0, uploadThroughputKbps: 0 },
  }, { extends: 'lighthouse:default', settings: { onlyAudits: null } });

  const { lhr } = runnerResult;
  const line = (k) => `${k.padEnd(16)} ${Math.round((lhr.categories[k]?.score ?? 0) * 100)}`;
  console.log('== Lighthouse ' + (mobile ? '移动' : '桌面') + ' ==');
  for (const k of ['performance', 'accessibility', 'best-practices', 'seo']) console.log(line(k));

  const m = lhr.audits;
  console.log('\n核心指标');
  for (const k of ['first-contentful-paint', 'largest-contentful-paint', 'total-blocking-time', 'cumulative-layout-shift', 'speed-index']) {
    if (m[k]) console.log(`${k.padEnd(28)} ${m[k].displayValue}`);
  }

  const bad = Object.values(m)
    .filter((a) => a.score !== null && a.score < 1 && a.scoreDisplayMode !== 'informative' && a.scoreDisplayMode !== 'notApplicable')
    .sort((a, b) => (a.score ?? 1) - (b.score ?? 1));
  console.log('\n未满分的审计');
  for (const a of bad.slice(0, 12)) {
    console.log(`  [${a.score}] ${a.id} — ${a.title}`);
    if (a.displayValue) console.log(`        ${a.displayValue}`);
  }
  console.log(`\n共 ${bad.length} 项未满分`);
} finally {
  await chrome.kill();
}
