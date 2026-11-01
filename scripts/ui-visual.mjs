#!/usr/bin/env node
/* 像素级视觉回归：对「静态且确定」的界面基线做逐像素比对。
   为什么不用数据页：账号数、时间戳每次都在变，diff 必然失败；组件文档与命令面板是确定的。
   用法：
     node scripts/ui-visual.mjs            # 比对，超出阈值退出 1
     node scripts/ui-visual.mjs --update   # 重建基线
*/
import { createRequire } from 'node:module';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
const { chromium } = require('playwright');
const pixelmatch = require('pixelmatch').default || require('pixelmatch');
const { PNG } = require('pngjs');

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(HERE, '..');
const BASE = path.join(ROOT, 'docs', 'design', 'visual-baseline');
const DIFF = path.join(ROOT, 'docs', 'design', 'visual-diff');
const DOC = 'file:///' + path.join(ROOT, 'docs', 'design', 'components.html').replace(/\\/g, '/');
const APP = process.env.UI_ORIGIN || 'http://127.0.0.1:7999';
const update = process.argv.includes('--update');
const THRESHOLD = 0.001; // 允许 0.1% 的像素差异（抗锯齿）

const CHROME = process.env.CHROME_PATH
  || (process.env.LOCALAPPDATA ? process.env.LOCALAPPDATA + '\\ms-playwright\\chromium-1234\\chrome-win64\\chrome.exe' : undefined);

fs.mkdirSync(BASE, { recursive: true });
fs.mkdirSync(DIFF, { recursive: true });

const browser = await chromium.launch(CHROME ? { executablePath: CHROME } : {});

async function settle(page) {
  await page.evaluate(() => document.fonts.ready);
  await page.addStyleTag({ content: '*{animation:none!important;transition:none!important;caret-color:transparent!important}' });
  await page.waitForTimeout(350);
}

const shots = [];

// 1) 组件文档：浅色 / 深色（整页）
for (const theme of ['light', 'dark']) {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 1 });
  await page.goto(DOC, { waitUntil: 'networkidle' });
  await page.evaluate((t) => { document.documentElement.dataset.theme = t; }, theme);
  await settle(page);
  const buf = await page.screenshot({ fullPage: true });
  shots.push({ name: 'components-' + theme, buf });
  await page.close();
}

// 2) 命令面板：固定首项与固定搜索词
{
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 1 });
  await page.goto(APP + '/#/channels', { waitUntil: 'domcontentloaded', timeout: 20000 });
  await page.waitForTimeout(1200);
  await page.keyboard.press('Control+K');
  await page.waitForTimeout(300);
  await page.keyboard.type('账号');
  await settle(page);
  // 只截面板本身：背后的账号数据每次都在变，整屏截图不稳定
  const buf = await page.locator('.cmdk').screenshot();
  shots.push({ name: 'palette', buf });
  await page.close();
}

await browser.close();

let failed = 0;
for (const shot of shots) {
  const file = path.join(BASE, shot.name + '.png');
  if (update || !fs.existsSync(file)) {
    fs.writeFileSync(file, shot.buf);
    console.log('BASE  ' + shot.name + (update ? '' : '（首次生成）'));
    continue;
  }
  const cur = PNG.sync.read(shot.buf);
  const old = PNG.sync.read(fs.readFileSync(file));
  if (cur.width !== old.width || cur.height !== old.height) {
    console.log(`FAIL  ${shot.name}  尺寸变化 ${old.width}×${old.height} → ${cur.width}×${cur.height}`);
    failed++;
    continue;
  }
  const diff = new PNG({ width: cur.width, height: cur.height });
  const n = pixelmatch(old.data, cur.data, diff.data, cur.width, cur.height, { threshold: 0.1 });
  const ratio = n / (cur.width * cur.height);
  if (ratio > THRESHOLD) {
    fs.writeFileSync(path.join(DIFF, shot.name + '.diff.png'), PNG.sync.write(diff));
    console.log(`FAIL  ${shot.name}  ${n} px 不同（${(ratio * 100).toFixed(2)}%）→ docs/design/visual-diff/${shot.name}.diff.png`);
    failed++;
  } else {
    console.log(`PASS  ${shot.name}  ${(ratio * 100).toFixed(3)}%`);
  }
}

if (update) console.log(`\n已写入 ${shots.length} 张基线到 docs/design/visual-baseline/`);
else console.log(`\n${shots.length - failed} 通过 · ${failed} 失败`);
process.exit(update ? 0 : failed ? 1 : 0);
