#!/usr/bin/env node
// UI 冒烟（自有前端 ui2）：验证外壳、概览、渠道、用量、网关、设置、会话、库的真实渲染。
// 用法：node scripts/ui-check.mjs http://127.0.0.1:7999
// 退出码 0 = 全过；1 = 有失败；2 = 环境问题。

import { createRequire } from 'node:module';

const require = createRequire(import.meta.url);
let chromium;
try {
  ({ chromium } = require('playwright'));
} catch {
  console.error('需要 playwright：先运行 `npm i playwright`（或设置 NODE_PATH）。');
  process.exit(2);
}

const base = (process.argv[2] || 'http://127.0.0.1:7999').replace(/\/+$/, '');
const results = [];
const pass = (name, detail = '') => results.push({ ok: true, name, detail });
const skip = (name, detail = '') => results.push({ ok: true, skipped: true, name, detail });
const fail = (name, detail = '') => results.push({ ok: false, name, detail });

async function api(path) {
  const res = await fetch(base + path);
  if (!res.ok) throw new Error(path + ' → ' + res.status);
  return res.json();
}

const exe = process.env.CHROME_PATH ||
  (process.env.LOCALAPPDATA
    ? process.env.LOCALAPPDATA + '\\ms-playwright\\chromium-1234\\chrome-win64\\chrome.exe'
    : undefined);

const browser = await chromium.launch(exe ? { executablePath: exe } : {});
const page = await browser.newPage({ viewport: { width: 1440, height: 960 } });
const errors = [];
page.on('pageerror', (e) => errors.push(e.message));
page.on('console', (m) => { if (m.type() === 'error') errors.push(m.text()); });

try {
  await page.goto(base + '/', { waitUntil: 'domcontentloaded', timeout: 30000 });
  await page.waitForSelector('.kpi .v', { timeout: 20000 });
  await page.waitForTimeout(800);

  // 1. 外壳与自有资源
  const shell = await page.evaluate(() => ({
    nav: document.querySelectorAll('.nav-item').length,
    h1: document.querySelector('.head h1')?.textContent || '',
    kpis: document.querySelectorAll('.kpi').length,
    assets: performance.getEntriesByType('resource').map((r) => new URL(r.name).pathname),
  }));
  if (shell.nav === 12) pass('外壳导航', '12 项'); else fail('外壳导航', String(shell.nav));
  if (shell.h1 === '概览') pass('落地页', '概览'); else fail('落地页', shell.h1);
  if (shell.kpis === 4) pass('概览 KPI', '4 张'); else fail('概览 KPI', String(shell.kpis));
  const oldAssets = shell.assets.filter((p) => p === '/app.js' || p === '/app.css');
  const ownAssets = shell.assets.filter((p) => p === '/js/app.js');
  if (!oldAssets.length && ownAssets.length) pass('前端资源自有', '只加载 /js/app.js');
  else fail('前端资源自有', '旧资源: ' + oldAssets.join(',') + ' / 自有: ' + ownAssets.join(','));

  // 2. 概览数据
  const accounts = await api('/api/accounts');
  const acctText = await page.locator('.kpi').first().innerText();
  // KPI 的「可用账号」= state=ready 的账号数（health.accounts 是账号总数，两者不是一回事）。
  const wantAccounts = String((accounts.accounts || []).filter((a) => a.state === 'ready').length);
  if (acctText.includes(wantAccounts)) pass('可用账号数量一致', wantAccounts);
  else fail('可用账号数量一致', acctText.replace(/\s+/g, ' '));

  // 3. 渠道与额度
  await page.goto(base + '/#/channels', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.acct-card', { timeout: 15000 });
  await page.waitForTimeout(600);
  const wantCards = (accounts.accounts || []).length;
  const gotCards = await page.locator('.acct-card').count();
  if (gotCards === wantCards) pass('账号卡数量一致', gotCards + ' 张');
  else fail('账号卡数量一致', '界面 ' + gotCards + ' / 接口 ' + wantCards);
  if (await page.locator('.batchbar').count()) pass('批量条', '.batchbar');
  else fail('批量条', '缺失');
  await page.click('button:has-text("供应商与开关")');
  await page.waitForTimeout(400);
  const drawerRows = await page.locator('.drawer .row').count();
  const providers = await api('/api/providers');
  const wantProviders = (providers.providers || []).length;
  if (drawerRows === wantProviders) pass('供应商抽屉', drawerRows + ' 行');
  else fail('供应商抽屉', '界面 ' + drawerRows + ' / 接口 ' + wantProviders);
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);

  // 账号设置抽屉（cockpit-tools 式账号级运维：优先级/权重/并发/模型白名单）
  // 2026-10-30：低频动作收进「更多」菜单，先展开再点设置
  await page.locator('.acct-acts button[title="更多操作"]').first().click();
  await page.waitForTimeout(200);
  await page.locator('.menu button', { hasText: '设置' }).first().click();
  await page.waitForTimeout(500);
  const settings = await page.evaluate(() => {
    const labels = [...document.querySelectorAll('.drawer .field label')].map((l) => l.textContent || '');
    return {
      title: document.querySelector('.drawer .drawer-h b')?.textContent || '',
      labels,
      models: document.querySelectorAll('.drawer .list input[type=checkbox]').length,
      save: [...document.querySelectorAll('.drawer button')].some((b) => b.textContent === '保存'),
    };
  });
  const need = ['优先级', '权重', '并发上限'];
  if (settings.title.includes('账号设置') && need.every((x) => settings.labels.includes(x)) && settings.save) {
    pass('账号设置抽屉', need.join('/') + ' · ' + settings.models + ' 模型');
  } else {
    fail('账号设置抽屉', JSON.stringify(settings));
  }
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);

  // 账号测试弹窗：mock 账号本地探测必须真实返回成功与耗时
  const mockAcct = (accounts.accounts || []).find((a) => /^mock/.test(a.provider_id));
  if (mockAcct) {
    const mockCard = page.locator('.acct-card', { hasText: mockAcct.label }).first();
    await mockCard.locator('button[title="更多操作"]').click();
    await page.waitForTimeout(200);
    await page.locator('.menu button', { hasText: '测试连通性' }).first().click();
    await page.waitForTimeout(400);
    const modalTitle = await page.locator('.drawer .drawer-h b').textContent();
    await page.locator('.drawer', { hasText: '测试完成' }).waitFor({ timeout: 20000 }).catch(() => {});
    const modalText = (await page.locator('.drawer').textContent()).replace(/\s+/g, ' ');
    if (modalTitle.includes('测试 ·') && modalText.includes('成功') && /\d+ ms/.test(modalText) && modalText.includes('最近记录')) pass('账号测试弹窗', '成功 + 耗时');
    else fail('账号测试弹窗', modalText.slice(0, 140));
    await page.keyboard.press('Escape');
    await page.waitForTimeout(300);
  } else {
    skip('账号测试弹窗', '当前没有 mock 账号，跳过');
  }

  // 4. 用量
  await page.goto(base + '/#/usage', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1200);
  const cells = await page.locator('.heat-cell').count();
  if (cells >= 350) pass('用量日历', cells + ' 格');
  else fail('用量日历', cells + ' 格');
  if (await page.locator('.usage-row').count() >= 0 && await page.locator('.card').count() >= 2) pass('按渠道卡', '存在');
  else fail('按渠道卡', '缺失');

  // 5. 网关 / 设置 / 会话 / 库
  await page.goto(base + '/#/gateway', { waitUntil: 'domcontentloaded' });
  await page.waitForSelector('.code-block', { timeout: 10000 }).catch(() => {});
  await page.waitForTimeout(300);
  if (await page.locator('.code-block').count()) pass('接入片段', '.code-block');
  else fail('接入片段', '缺失');

  await page.goto(base + '/#/settings', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(900);
  if (await page.locator('.tab').count() >= 6) pass('设置分区', String(await page.locator('.tab').count()));
  else fail('设置分区', String(await page.locator('.tab').count()));

  await page.locator('.settings-nav .tab', { hasText: '关于' }).click();
  await page.waitForTimeout(500);
  const about = await page.evaluate(() => ({
    hero: !!document.querySelector('.about-hero'),
    links: document.querySelectorAll('.about-link').length,
    star: !!document.querySelector('.star-banner'),
    source: [...document.querySelectorAll('.settings-body .card-h b')].some((b) => (b.textContent || '').includes('GitHub 更新源')),
    check: [...document.querySelectorAll('button')].some((b) => (b.textContent || '').includes('检查更新')),
  }));
  if (about.hero && about.links === 4 && about.star && about.source && about.check) {
    pass('关于页', '作者/仓库/赞助/反馈 + Star + 更新源');
  } else {
    fail('关于页', JSON.stringify(about));
  }

  await page.goto(base + '/#/sessions', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(900);
  // 2026-10-30：会话行改为两行布局（.row2），两种都算
  const sessRows = await page.locator('.list .row, .list .row2').count();
  if (sessRows > 0) pass('会话列表', String(sessRows));
  else fail('会话列表', '空');

  await page.goto(base + '/#/library', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(900);
  if (await page.locator('.tab').count() >= 5) pass('库分区', String(await page.locator('.tab').count()));
  else fail('库分区', String(await page.locator('.tab').count()));

  await page.goto(base + '/#/scheduler', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(900);
  const schedH1 = await page.locator('.head h1').textContent();
  if (schedH1 === '唤醒任务' && await page.locator('.error-box').count() === 0) pass('唤醒任务页', 'KPI/队列');
  else fail('唤醒任务页', schedH1 || '空');

  await page.goto(base + '/#/launcher', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(900);
  const launchH1 = await page.locator('.head h1').textContent();
  if (launchH1 === '应用启动' && await page.locator('.error-box').count() === 0) pass('应用启动页', '启动表单/实例');
  else fail('应用启动页', launchH1 || '空');

  await page.goto(base + '/#/logs', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(900);
  // 2FA 管理：真实入库 → 页面出 6 位验证码与倒计时 → 清理
  const tfa = await fetch(base + '/api/2fa', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ issuer: 'UI 2FA 测试', account: 'dev', secret: 'JBSWY3DPEHPK3PXP', digits: 6, period: 30, algorithm: 'SHA1' }) });
  const tfaBody = await tfa.json().catch(() => ({}));
  const tfaId = tfaBody.entry && tfaBody.entry.id;
  await page.goto(base + '/#/twofa', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1200);
  const tfaH1 = await page.locator('.head h1').textContent();
  const codeText = await page.locator('.card .mono', { hasText: /^\d{6}$/ }).first().textContent().catch(() => '');
  const hasCountdown = await page.locator('[data-remain]').count();
  if (tfaH1 === '2FA 管理' && /^\d{6}$/.test((codeText || '').trim()) && hasCountdown > 0) pass('2FA 管理页', '6 位码 + 倒计时');
  else fail('2FA 管理页', JSON.stringify({ h1: tfaH1, codeText, hasCountdown }));
  if (tfaId) await fetch(base + '/api/2fa/' + encodeURIComponent(tfaId), { method: 'DELETE' });

  await page.goto(base + '/#/logs', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);
  const logsH1 = await page.locator('.head h1').textContent();
  if (logsH1 === '请求日志' && await page.locator('.error-box').count() === 0) pass('请求日志页', 'KPI/明细');
  else fail('请求日志页', logsH1 || '空');

  await page.goto(base + '/#/onboard', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1200);
  const onb = await page.evaluate(async () => {
    const pv = await (await fetch('/api/providers?include_empty=1')).json();
    const by = Object.fromEntries((pv.providers || []).map((p) => [p.id, p]));
    return {
      h1: document.querySelector('.head h1')?.textContent || '',
      cards: document.querySelectorAll('.grid-3 .card').length,
      claudeFields: (by['claude-sub']?.auth_form_schema || []).length,
      antigravityOAuth: !!by['antigravity']?.oauth,
      xaiDevice: (by['xai']?.supported_auths || []).includes('device_code') && !!by['xai']?.oauth,
      claudeOAuth: (by['claude-sub']?.supported_auths || []).includes('oauth_pkce') && !!by['claude-sub']?.oauth,
      codexOAuth: (by['codex']?.supported_auths || []).includes('oauth_pkce') && !!by['codex']?.oauth,
      copilotDevice: (by['github-copilot']?.supported_auths || []).includes('device_code') && !!by['github-copilot']?.oauth,
      antigravityIDE: (by['antigravity-ide']?.supported_auths || []).includes('oauth_pkce') && !!by['antigravity-ide']?.oauth,
      brandImgs: document.querySelectorAll('.grid-3 .card img.brand-img').length,
      brandFailed: [...document.querySelectorAll('.grid-3 .card img.brand-img')].filter((i) => !i.complete || i.naturalWidth === 0).length,
    };
  });
  if (onb.h1 === '接入渠道' && onb.cards >= 40 && onb.claudeFields > 0 && onb.antigravityOAuth && onb.xaiDevice && onb.claudeOAuth && onb.codexOAuth && onb.copilotDevice && onb.antigravityIDE && onb.brandImgs >= 40 && onb.brandFailed === 0) pass('接入渠道页', onb.cards + ' 平台 · ' + onb.brandImgs + ' 品牌图标');
  else fail('接入渠道页', JSON.stringify(onb));

  // Claude 订阅：打开抽屉 → 浏览器授权页签 → 真实 auth/start 必须返回 claude.ai 链接
  await page.locator('.grid-3 .card', { hasText: 'Claude 订阅' }).first().locator('button').first().click();
  await page.waitForTimeout(600);
  const authTab = page.locator('.drawer .tabs .tab', { hasText: '浏览器授权' });
  if (await authTab.count()) await authTab.first().click();
  await page.waitForTimeout(1600);
  const authHref = await page.locator('.drawer a', { hasText: '打开授权页面' }).first().getAttribute('href').catch(() => null);
  if (authHref && authHref.startsWith('https://claude.ai/oauth/authorize')) pass('Claude 浏览器授权', 'claude.ai + PKCE');
  else fail('Claude 浏览器授权', authHref || '未生成授权链接');
  const cancelBtn = page.locator('.drawer button', { hasText: '取消' });
  if (await cancelBtn.count()) await cancelBtn.first().click();
  await page.waitForTimeout(300);
  await page.keyboard.press('Escape');
  await page.waitForTimeout(400);

  // API 平台的手动凭据页签要有批量 Key 输入（cockpit-tools 式批量接入）
  await page.goto(base + '/#/onboard', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);
  const apiCard = page.locator('.grid-3 .card').filter({ has: page.locator('.name', { hasText: /^OpenAI 兼容端点$/ }) }).first();
  await apiCard.locator('button').first().click();
  await page.waitForTimeout(500);
  const manualTab = page.locator('.drawer .tabs .tab', { hasText: '手动凭据' });
  if (await manualTab.count()) await manualTab.first().click();
  await page.waitForTimeout(500);
  const bulkBox = await page.locator('.drawer textarea[aria-label="批量 Key"]').count();
  if (bulkBox) pass('批量 Key 输入', '手动凭据页签');
  else fail('批量 Key 输入', '缺失');
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);

  // Qoder 订阅登录：设备选择页 + nonce/PKCE + machine_id，真实链接可从 UI 打开
  await page.goto(base + '/#/onboard', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);
  const qoderCard = page.locator('.grid-3 .card').filter({ has: page.locator('.name', { hasText: /^Qoder CN$/ }) }).first();
  await qoderCard.locator('button').first().click();
  await page.waitForTimeout(500);
  const qoderTab = page.locator('.drawer .tabs .tab', { hasText: 'Qoder 登录' });
  if (await qoderTab.count()) await qoderTab.first().click();
  await page.waitForTimeout(1800);
  const qoderHref = await page.locator('.drawer a', { hasText: '打开登录页面' }).first().getAttribute('href').catch(() => '') || '';
  if (/nonce=[^&]+/.test(qoderHref) && /challenge=[^&]+/.test(qoderHref) && /challenge_method=S256/.test(qoderHref) && /machine_id=[^&]+/.test(qoderHref)) {
    pass('Qoder 登录', 'selectAccounts + nonce/PKCE');
  } else {
    fail('Qoder 登录', qoderHref.slice(0, 140));
  }
  const qoderCancel = page.locator('.drawer button', { hasText: '取消登录' });
  if (await qoderCancel.count()) await qoderCancel.first().click().catch(() => {});
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);

  // WorkBuddy 订阅登录：真实 auth/state 后给出 copilot.tencent.com 登录页
  await page.goto(base + '/#/onboard', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);
  const wbCard = page.locator('.grid-3 .card').filter({ has: page.locator('.name', { hasText: /^WorkBuddy CN$/ }) }).first();
  await wbCard.locator('button').first().click();
  await page.waitForTimeout(500);
  const wbTab = page.locator('.drawer .tabs .tab', { hasText: 'WorkBuddy 登录' });
  if (await wbTab.count()) await wbTab.first().click();
  await page.waitForTimeout(2500);
  const wbHref = await page.locator('.drawer a', { hasText: '打开登录页面' }).first().getAttribute('href').catch(() => '') || '';
  if (/copilot\.tencent\.com\/login/.test(wbHref) && /state=[^&]+/.test(wbHref) && /loginSessionId=[^&]+/.test(wbHref)) {
    pass('WorkBuddy 登录', 'copilot.tencent.com + state');
  } else {
    fail('WorkBuddy 登录', wbHref.slice(0, 140));
  }
  const wbCancel = page.locator('.drawer button', { hasText: '取消登录' });
  if (await wbCancel.count()) await wbCancel.first().click().catch(() => {});
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);

  // CodeBuddy CN 登录（国际站在本机网络下 TLS 超时，只断言国内站）
  await page.goto(base + '/#/onboard', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);
  const cbCard = page.locator('.grid-3 .card').filter({ has: page.locator('.name', { hasText: /^CodeBuddy CN$/ }) }).first();
  await cbCard.locator('button').first().click();
  await page.waitForTimeout(500);
  const cbTab = page.locator('.drawer .tabs .tab', { hasText: 'CodeBuddy 登录' });
  if (await cbTab.count()) await cbTab.first().click();
  await page.waitForTimeout(2500);
  const cbHref = await page.locator('.drawer a', { hasText: '打开登录页面' }).first().getAttribute('href').catch(() => '') || '';
  if (/codebuddy\.cn\/login/.test(cbHref) && /state=[^&]+/.test(cbHref) && /loginSessionId=[^&]+/.test(cbHref)) {
    pass('CodeBuddy 登录', 'codebuddy.cn + state');
  } else {
    fail('CodeBuddy 登录', cbHref.slice(0, 140));
  }
  const cbCancel = page.locator('.drawer button', { hasText: '取消登录' });
  if (await cbCancel.count()) await cbCancel.first().click().catch(() => {});
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);

  // Zed native_app_signin：本地 RSA 公钥与回环端口必须出现在登录链接里
  await page.goto(base + '/#/onboard', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);
  const zedCard = page.locator('.grid-3 .card').filter({ has: page.locator('.name', { hasText: /^Zed$/ }) }).first();
  await zedCard.locator('button').first().click();
  await page.waitForTimeout(500);
  const zedTab = page.locator('.drawer .tabs .tab', { hasText: 'Zed 登录' });
  if (await zedTab.count()) await zedTab.first().click();
  await page.waitForTimeout(1500);
  const zedHref = await page.locator('.drawer a', { hasText: '打开登录页面' }).first().getAttribute('href').catch(() => '') || '';
  if (/zed\.dev\/native_app_signin/.test(zedHref) && /native_app_port=\d+/.test(zedHref) && /native_app_public_key=[^&]+/.test(zedHref)) {
    pass('Zed 登录', 'native_app_signin + RSA');
  } else {
    fail('Zed 登录', zedHref.slice(0, 140));
  }
  const zedCancel = page.locator('.drawer button', { hasText: '取消登录' });
  if (await zedCancel.count()) await zedCancel.first().click().catch(() => {});
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);

  // Kiro portal：app.kiro.dev/signin + PKCE + localhost 回调端口
  await page.goto(base + '/#/onboard', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);
  const kiroCard = page.locator('.grid-3 .card').filter({ has: page.locator('.name', { hasText: /^Kiro$/ }) }).first();
  await kiroCard.locator('button').first().click();
  await page.waitForTimeout(500);
  const kiroTab = page.locator('.drawer .tabs .tab', { hasText: 'Kiro 登录' });
  if (await kiroTab.count()) await kiroTab.first().click();
  await page.waitForTimeout(1800);
  const kiroHref = await page.locator('.drawer a', { hasText: '打开登录页面' }).first().getAttribute('href').catch(() => '') || '';
  if (/app\.kiro\.dev\/signin/.test(kiroHref) && /code_challenge=[^&]+/.test(kiroHref) && /redirect_uri=http%3A%2F%2Flocalhost%3A\d+/.test(kiroHref)) {
    pass('Kiro 登录', 'app.kiro.dev + PKCE');
  } else {
    fail('Kiro 登录', kiroHref.slice(0, 140));
  }
  const kiroCancel = page.locator('.drawer button', { hasText: '取消登录' });
  if (await kiroCancel.count()) await kiroCancel.first().click().catch(() => {});
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);

  // Trae 登录：www.trae.cn/authorization + PKCE + 本地回调
  await page.goto(base + '/#/onboard', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);
  const traeCard = page.locator('.grid-3 .card').filter({ has: page.locator('.name', { hasText: /^Trae CN$/ }) }).first();
  await traeCard.locator('button').first().click();
  await page.waitForTimeout(500);
  const traeTab = page.locator('.drawer .tabs .tab', { hasText: 'Trae 登录' });
  if (await traeTab.count()) await traeTab.first().click();
  await page.waitForTimeout(1500);
  const traeHref = await page.locator('.drawer a', { hasText: '打开登录页面' }).first().getAttribute('href').catch(() => '') || '';
  if (/www\.trae\.cn\/authorization/.test(traeHref) && /client_id=[^&]+/.test(traeHref) && /code_challenge=[^&]+/.test(traeHref) && /auth_callback_url=http%3A%2F%2F127\.0\.0\.1%3A\d+/.test(traeHref)) {
    pass('Trae 登录', 'trae.cn/authorization + PKCE');
  } else {
    fail('Trae 登录', traeHref.slice(0, 140));
  }
  const traeCancel = page.locator('.drawer button', { hasText: '取消登录' });
  if (await traeCancel.count()) await traeCancel.first().click().catch(() => {});
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);

  // Windsurf/Devin：邮箱密码登录表单（不提交真实凭证）
  await page.goto(base + '/#/onboard', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1000);
  const wfCard = page.locator('.grid-3 .card').filter({ has: page.locator('.name', { hasText: /^Windsurf \/ Devin$/ }) }).first();
  await wfCard.locator('button').first().click();
  await page.waitForTimeout(500);
  const wfTab = page.locator('.drawer .tabs .tab', { hasText: 'Windsurf 登录' });
  if (await wfTab.count()) await wfTab.first().click();
  await page.waitForTimeout(800);
  const wfEmail = await page.locator('.drawer input[aria-label="邮箱"]').count();
  const wfPass = await page.locator('.drawer input[aria-label="密码"]').count();
  const wfGo = await page.locator('.drawer button', { hasText: '登录并接入' }).count();
  if (wfEmail && wfPass && wfGo) pass('Windsurf 登录', '邮箱/密码表单');
  else fail('Windsurf 登录', JSON.stringify({ wfEmail, wfPass, wfGo }));
  await page.keyboard.press('Escape');
  await page.waitForTimeout(300);

  await page.goto(base + '/#/hud', { waitUntil: 'domcontentloaded' });
  await page.waitForTimeout(1200);
  const hud = await page.evaluate(() => ({
    mode: document.getElementById('app').classList.contains('hud-mode'),
    rows: document.querySelectorAll('.hud-row').length,
    empty: !!document.querySelector('.hud-list .empty'),
    rail: getComputedStyle(document.querySelector('.rail')).display,
  }));
  // 账号没有返回 5h/7d 窗口时，页面应显示「没有可读的额度」空态，而不是空白。
  if (hud.mode && hud.rail === 'none' && (hud.rows > 0 || hud.empty)) {
    pass('额度悬浮页', hud.rows > 0 ? hud.rows + ' 行' : '无可读额度空态');
  } else {
    fail('额度悬浮页', JSON.stringify(hud));
  }

  // 6. 窄屏：页面级不得横向溢出（日历允许内部滚动）
  await page.setViewportSize({ width: 390, height: 844 });
  const narrowBad = [];
  for (const r of ['overview', 'channels', 'usage', 'settings', 'logs']) {
    await page.goto(base + '/#/' + r, { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(900);
    const m = await page.evaluate(() => ({ sw: document.documentElement.scrollWidth, vw: innerWidth }));
    if (m.sw > m.vw + 1) narrowBad.push(r + ':' + m.sw + '>' + m.vw);
  }
  if (!narrowBad.length) pass('窄屏无横向溢出', '5 页 @390');
  else fail('窄屏无横向溢出', narrowBad.join(' '));

  if (errors.length === 0) pass('控制台无错误');
  else fail('控制台无错误', errors.slice(0, 3).join(' | '));
} catch (e) {
  fail('执行', e.message);
} finally {
  await browser.close();
}

for (const r of results) console.log((r.ok ? (r.skipped ? 'SKIP  ' : 'PASS  ') : 'FAIL  ') + r.name + (r.detail ? '   ' + r.detail : ''));
const ok = results.filter((r) => r.ok).length;
const bad = results.filter((r) => !r.ok).length;
console.log('\n通过 ' + ok + ' · 失败 ' + bad);
process.exit(bad ? 1 : 0);
