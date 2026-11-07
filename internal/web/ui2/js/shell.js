/* 自有外壳：左轨 + 页面头 + 分段条 / 工具条 + 视图容器。
 *
 * 修订（2026-10-30）：
 *   1. 工具条只构建一次，筛选时只更新状态，不再重建 DOM —— 修掉「搜索框输入第二个字就失焦」；
 *   2. 视图入场动画只在路由切换时播放，筛选不再整页重播；
 *   3. 筛选条按路由裁剪：概览 / 用量不出现「列表 / 网格 / 套餐 / 标签 / 配额」；
 *   4. 页面头去掉版本号与构建时间，换成状态、命令面板与刷新；
 *   5. 新增 ⌘K 命令面板、g 序列快捷键、窄屏抽屉导航。 */
import { h, clear, isMac, setText } from './dom.js';
import { icon, brandMark } from './icons.js';
import * as api from './api.js';
import { dotChip, iconBtn, menu, toast } from './components.js';
import { openPalette, closePalette, paletteOpen } from './cmdk.js';
import { enterHUD, exitHUD, hasWindowRuntime } from './win.js';
import { getLocale, setLocale } from './i18n.js';

const NAV = [
  { group: '工作', items: [
    { id: 'overview', label: '概览', icon: 'overview', badge: 'accounts', keys: 'g o' },
    { id: 'onboard', label: '接入渠道', icon: 'link', keys: 'g i' },
    { id: 'channels', label: '账号与额度', icon: 'channels', keys: 'g a' },
    { id: 'usage', label: '用量', icon: 'usage', keys: 'g u' },
    { id: 'scheduler', label: '唤醒任务', icon: 'clock' },
    { id: 'launcher', label: '应用启动', icon: 'play' },
  ] },
  { group: '资产', items: [
    { id: 'sessions', label: '会话', icon: 'sessions', badge: 'sessions' },
    { id: 'library', label: '库（指令/技能/MCP）', icon: 'library' },
  ] },
  { group: '系统', items: [
    { id: 'logs', label: '请求日志', icon: 'logs', keys: 'g l' },
    { id: 'gateway', label: '网关与探针', icon: 'gateway' },
    { id: 'twofa', label: '2FA 管理', icon: 'shield' },
    { id: 'settings', label: '设置', icon: 'settings', keys: 'g s' },
  ] },
];

const MORE = [
  { id: 'agents', label: 'Agent 接入' },
  { id: 'routing', label: '路由' },
  { id: 'masking', label: '脱敏' },
  { id: 'plugins', label: '插件' },
];

const KEY_ROUTES = { o: 'overview', i: 'onboard', a: 'channels', u: 'usage', l: 'logs', s: 'settings' };

export function createShell({ navigate }) {
  const app = document.getElementById('app');
  const railScroll = h('div', { class: 'rail-scroll' });
  const navEl = h('nav', { class: 'nav', 'aria-label': '主导航' });
  const railFoot = h('div', { class: 'rail-foot' });
  const rail = h('aside', { class: 'rail', id: 'rail' }, railScroll, railFoot);
  const scrim = h('div', { class: 'scrim', onclick: () => closeNav() });
  const head = h('header', { class: 'head' });
  const chromeHost = h('div', { class: 'chrome-host' });
  let viewHost = h('main', { class: 'view', id: 'view', tabindex: '-1' });
  const main = h('div', { class: 'main' }, head, chromeHost, viewHost);
  const skip = h('button', {
    class: 'skip', type: 'button',
    onclick: () => { viewHost.focus(); viewHost.scrollIntoView({ block: 'start' }); },
  }, '跳到主内容');
  app.append(skip, rail, scrim, main);

  // 桌面端：无边框窗口 + 自绘 44px 标题栏（拖拽区、页面名、窗口控制、应用菜单）。
  let tb = null;
  if (hasWindowRuntime()) {
    document.body.classList.add('framed');
    tb = buildTitlebar();
    document.body.append(tb.el);
  }

  let meta = { version: '', built: '', ready: false, accounts: 0, sessions: 0, accountsList: [], providers: [] };
  let filter = { seg: 'all', query: '', layout: 'grid', sortQuota: false, tag: null, more: { quota: false, calls: false, off: false } };
  let currentId = '';
  let currentView = null;
  let queryTimer = 0;
  let chrome = null;

  function buildTitlebar() {
    const pageName = h('span', { class: 'tb-page' }, '概览');
    const status = h('span', { class: 'tb-status', title: '网关就绪' });
    const more = h('button', { class: 'tb-btn', type: 'button', 'aria-label': '应用菜单', title: '应用菜单' }, icon('more', 16));
    const min = tbBtn('minimize', '最小化', () => window.runtime.WindowMinimise());
    let isMax = false;
    const toggleMax = () => {
      if (isMax) window.runtime.WindowUnmaximise();
      else window.runtime.WindowMaximise();
    };
    const max = tbBtn('maximize', '最大化', toggleMax);
    // macOS 的红色关闭按钮只关窗，应用继续留在菜单栏/托盘；Windows 保持退出。
    const close = tbBtn('x', '关闭', () => isMac ? window.runtime.WindowHide() : window.runtime.Quit());
    max.classList.add('tb-max');
    max.replaceChildren(icon('maximize', 12, 1.1), icon('restore', 12, 1.1));
    max.firstChild.classList.add('i-max');
    max.lastChild.classList.add('i-restore');
    close.classList.add('close');
    const bar = h('header', { class: 'titlebar' },
      brandMark(18), h('b', { class: 'tb-title' }, 'Aether Cockpit'), h('span', { class: 'tb-sep' }), pageName,
      h('span', { class: 'tb-grow' }), status, more, min, max, close);

    more.onclick = () => menu(more, [
      { label: '复制网关地址', run: async () => {
        try {
          const s = await api.get('settings');
          const addr = s?.addr || location.host;
          await navigator.clipboard.writeText('http://' + addr);
          toast('已复制 http://' + addr);
        } catch { toast('复制失败：浏览器拒绝访问剪贴板'); }
      } },
      { label: '一键全部签到', run: async () => {
        try { const r = await api.post('scheduler/run', {}); toast('已入队 ' + (r.queued ?? 0) + ' 个任务'); }
        catch (e) { toast('入队失败：' + (e.message || e)); }
      } },
      { sep: true },
      { label: '退出', run: () => window.runtime.Quit() },
    ]);
    bar.addEventListener('dblclick', (e) => {
      if (!e.target.closest('button')) toggleMax();
    });
    let timer = 0;
    async function syncMax() {
      try {
        const m = await window.runtime.WindowIsMaximised();
        isMax = !!m;
        max.classList.toggle('maxed', isMax);
        max.setAttribute('aria-label', m ? '还原' : '最大化');
        max.title = m ? '还原' : '最大化';
      } catch {}
    }
    addEventListener('resize', () => { clearTimeout(timer); timer = setTimeout(syncMax, 120); });
    syncMax();
    return { el: bar, pageName, status, syncMax };
  }

  function tbBtn(name, label, onClick) {
    // 窗口控制键按 Windows 惯例用 12px 细线：这里描边单独降到 1.1，不套用 16px 的字重
    return h('button', { class: 'tb-btn', type: 'button', 'aria-label': label, title: label, onclick: onClick }, icon(name, 12, 1.1));
  }

  /* ---------- 左轨 ---------- */
  const brand = h('div', { class: 'brand' },
    h('span', { class: 'brand-tile' }, brandMark(24)),
    h('b', null, 'Aether Cockpit', h('small', null, '算力聚合网关')));
  railScroll.append(brand, navEl);
  for (const g of NAV) {
    navEl.append(h('span', { class: 'nav-gl' }, g.group));
    for (const it of g.items) {
      const b = h('button', {
        class: 'nav-item', type: 'button', 'data-route': it.id,
        onclick: () => { navigate(it.id); closeNav(); },
      }, icon(it.icon, 17), h('span', null, it.label), it.badge ? h('span', { class: 'badge', 'data-badge': it.badge }, '') : null);
      navEl.append(b);
    }
  }
  railFoot.append(
    h('button', { class: 'rail-link', type: 'button', onclick: async () => { await enterHUD(); navigate('hud'); } }, icon('eye', 16), h('span', null, '额度悬浮')),
    h('button', { class: 'rail-link', type: 'button', onclick: () => { refreshAll(); toast('正在重新检测…'); } }, icon('refresh', 16), h('span', null, '重新检测')),
    h('button', { class: 'rail-link', type: 'button', onclick: () => navigate('settings', { tab: 'about' }) }, icon('shield', 16), h('span', null, '帮助')),
  );

  function paintNav() {
    for (const b of navEl.querySelectorAll('[data-route]')) {
      const on = b.dataset.route === currentId;
      b.classList.toggle('on', on);
      if (on) b.setAttribute('aria-current', 'page');
      else b.removeAttribute('aria-current');
    }
    if (tb) {
      tb.pageName.textContent = (currentView && currentView.title) || '';
      tb.status.classList.toggle('off', !meta.ready);
      tb.status.title = meta.ready ? '网关就绪' : '网关未就绪';
    }
    const ov = navEl.querySelector('[data-badge="accounts"]');
    if (ov) setText(ov, meta.accounts ? meta.accounts + ' 渠道' : '');
    const se = navEl.querySelector('[data-badge="sessions"]');
    if (se) se.textContent = meta.sessions ? String(meta.sessions) : '';
  }

  function openNav() { rail.classList.add('on'); scrim.classList.add('on'); }
  function closeNav() { rail.classList.remove('on'); scrim.classList.remove('on'); }
  function navIsOpen() { return rail.classList.contains('on'); }

  /* ---------- 页面头 ---------- */
  function paintHead(view) {
    clear(head);
    const toggle = h('button', {
      class: 'icon-btn nav-toggle', type: 'button', 'aria-label': '打开导航', 'aria-expanded': 'false',
      'aria-controls': 'rail',
      onclick: () => {
        navIsOpen() ? closeNav() : openNav();
        toggle.setAttribute('aria-expanded', String(navIsOpen()));
      },
    }, icon('menu', 16));
    head.append(
      toggle,
      h('div', { class: 'head-l' }, h('h1', null, view.title || ''),
        view.subtitle ? h('div', { class: 'sub' }, view.subtitle) : null),
      h('div', { class: 'right' },
        dotChip((meta.ready ? '就绪' : '未就绪') + (meta.accounts ? ' · ' + meta.accounts + ' 账号' : ''), meta.ready ? 'green' : 'red'),
        langToggle(),
        paletteTrigger(),
        iconBtn('refresh', '刷新当前页', () => { refreshAll(); toast('已刷新'); }),
      ));
  }

  function langToggle() {
    const zh = getLocale() !== 'en';
    return h('button', {
      class: 'btn sm', type: 'button', 'aria-label': zh ? 'Switch to English' : '切换到中文',
      title: zh ? 'Switch to English' : '切换到中文',
      // setLocale 会持久化并重载页面；按钮文案由重载后的首次渲染决定
      onclick: () => { setLocale(getLocale() === 'en' ? 'zh-CN' : 'en'); },
    }, zh ? '中' : 'EN');
  }

  function paletteTrigger() {
    return h('button', {
      class: 'btn palette-btn', type: 'button',
      // 可访问名必须包含可见文字（WCAG 2.5.3），否则语音控制无法用界面上看到的词点中它
      'aria-label': '搜索与命令 ' + (isMac ? '⌘K' : 'Ctrl K'),
      'aria-keyshortcuts': isMac ? 'Meta+K' : 'Control+K',
      style: 'gap:8px;color:var(--muted);padding:0 6px 0 10px',
      onclick: () => openCommandPalette(),
    }, icon('search', 16), h('span', null, '搜索与命令'),
      h('span', { class: 'kbd', style: 'border:0;background:transparent;color:var(--faint)' }, isMac ? '⌘K' : 'Ctrl K'));
  }

  /* ---------- 命令面板 ---------- */
  function paletteItems() {
    const items = [];
    for (const g of NAV) for (const it of g.items) {
      items.push({ label: it.label, hint: it.keys || '', icon: it.icon, keywords: g.group + ' ' + it.id, run: () => navigate(it.id) });
    }
    for (const m of MORE) items.push({ label: m.label, hint: '', icon: 'plugin', keywords: '更多 ' + m.id, run: () => navigate(m.id) });
    items.push(
      { label: '刷新全部额度', icon: 'refresh', keywords: 'refresh quota', run: () => { refreshAll(); toast('已刷新'); } },
      { label: '刷新全部模型目录', icon: 'refresh', keywords: 'refresh models catalog', run: refreshModelCatalogs },
      { label: '切换主题（浅色 / 深色 / 跟随系统）', icon: 'eye', keywords: 'theme dark light', run: cycleTheme },
      { label: '复制网关地址', icon: 'copy', keywords: 'copy url base', run: copyOrigin },
      { label: '导出账号（脱敏）', icon: 'export', keywords: 'export json', run: exportAccounts },
    );
    return items;
  }

  function openCommandPalette() { openPalette(paletteItems()); }

  async function copyOrigin() {
    try {
      const s = await api.get('settings');
      await navigator.clipboard.writeText('http://' + (s?.addr || location.host));
      toast('已复制网关地址');
    } catch { toast('复制失败：浏览器拒绝访问剪贴板'); }
  }
  function exportAccounts() {
    const a = document.createElement('a');
    a.href = '/api/accounts/export';
    a.download = 'accounts-export.json';
    a.click();
  }
  function cycleTheme() {
    const order = ['auto', 'light', 'dark'];
    let cur = 'auto';
    try { cur = localStorage.getItem('aether.theme') || 'auto'; } catch {}
    const next = order[(order.indexOf(cur) + 1) % order.length];
    try { localStorage.setItem('aether.theme', next); } catch {}
    document.documentElement.dataset.theme = next;
    toast('主题：' + ({ auto: '跟随系统', light: '浅色', dark: '深色' }[next]));
  }

  /* ---------- 分段条 / 工具条：只建一次 ---------- */
  function accountTags() {
    const set = new Set();
    for (const a of meta.accountsList) for (const t of a.tags || []) set.add(t);
    return [...set].sort((a, b) => a.localeCompare(b));
  }

  function buildChrome() {
    const segTabs = h('div', { class: 'segbar__tabs', role: 'tablist', 'aria-label': '筛选' });
    const seg = h('div', { class: 'segbar' });
    const segBtn = (id, label, opts = {}) => h('button', {
      class: '', type: 'button', role: 'tab', 'aria-selected': 'false',
      'data-seg': id, disabled: opts.disabled, title: opts.title || '',
      onclick: () => setFilter({ seg: id }),
    }, label);
    segTabs.append(segBtn('all', '全部'), segBtn('real', '真实'));
    seg.append(segTabs);

    const moreBtn = h('button', { class: 'more', type: 'button' }, '更多 ▾');
    moreBtn.onclick = () => menu(moreBtn, [
      { label: '只看有额度窗口', on: filter.more.quota, run: () => setFilter({ more: { ...filter.more, quota: !filter.more.quota } }) },
      { label: '只看近 24h 有调用', on: filter.more.calls, run: () => setFilter({ more: { ...filter.more, calls: !filter.more.calls } }) },
      { label: '只看已停用', on: filter.more.off, run: () => setFilter({ more: { ...filter.more, off: !filter.more.off } }) },
      { sep: true },
      ...MORE.map((m) => ({ label: m.label, run: () => navigate(m.id) })),
    ]);
    seg.append(moreBtn);

    const tools = h('div', { class: 'toolbar' });
    const search = h('label', { class: 'search' });
    search.append(icon('search', 16));
    const searchInput = h('input', {
      type: 'search', placeholder: '搜索账号、渠道、模型…', value: filter.query,
      'aria-label': '搜索', autocomplete: 'off', spellcheck: 'false', id: 'global-search',
    });
    searchInput.oninput = () => {
      clearTimeout(queryTimer);
      queryTimer = setTimeout(() => setFilter({ query: searchInput.value }), 140);
    };
    searchInput.onkeydown = (e) => {
      if (e.key === 'Escape') { searchInput.value = ''; setFilter({ query: '' }); searchInput.blur(); }
    };
    search.append(searchInput);

    const listBtn = h('button', { class: 'btn sm channel-only', type: 'button', onclick: () => setFilter({ layout: 'list' }) }, '列表');
    const gridBtn = h('button', { class: 'btn sm channel-only', type: 'button', onclick: () => setFilter({ layout: 'grid' }) }, '网格');
    const plans = h('button', { class: 'btn sm channel-only', type: 'button', onclick: resetFilter }, '全部套餐');
    const tagBtn = h('button', { class: 'btn sm channel-only', type: 'button' }, '标签');
    tagBtn.onclick = () => menu(tagBtn, [
      { label: '全部标签', on: !filter.tag, run: () => setFilter({ tag: null }) },
      ...accountTags().map((t) => ({ label: t, on: filter.tag === t, run: () => setFilter({ tag: t }) })),
    ]);
    const sortBtn = h('button', { class: 'btn sm channel-only', type: 'button', onclick: () => setFilter({ sortQuota: !filter.sortQuota }) }, '按配额');
    const channelOnly = [listBtn, gridBtn, plans, tagBtn, sortBtn];
    tools.append(seg, search, ...channelOnly, h('span', { class: 'grow' }));
    tools.append(
      iconBtn('plus', '添加账号 / 供应商', () => {
        if (currentId === 'channels' && typeof window.__aetherAddProvider === 'function') { window.__aetherAddProvider(); return; }
        try { sessionStorage.setItem('aether.addProvider', '1'); } catch {}
        navigate('channels');
      }, { primary: true }),
      iconBtn('refresh', '刷新全部额度', async (e) => {
        const b = e.currentTarget;
        const prevTitle = b.title;
        // 加载态：icon-btn 没有独立的 disabled 样式，显式压暗并标 aria-busy。
        b.disabled = true;
        b.style.opacity = '0.45';
        b.setAttribute('aria-busy', 'true');
        b.title = '正在刷新额度…';
        toast('正在刷新额度…');
        try {
          // 真刷新是 POST /api/usage/quotas/refresh（同步读上游、返回最新卡片）。
          // 不要用 GET ?refresh=1：GET 只认 asked（后台补读），refresh 会被忽略，
          // 看起来"刷新成功"其实只重读了缓存。
          const cards = await api.post('usage/quotas/refresh');
          await refreshAll();
          // 单账号读取失败不会让 POST 失败（handler 内部吞掉），但会以 error 卡返回；
          // 有错误卡就如实提示，不假报"全部刷新成功"。
          const failed = Array.isArray(cards) ? cards.filter((c) => c && c.error).length : 0;
          toast(failed ? '额度已刷新（' + failed + ' 个账号读取失败）' : '额度已刷新');
        } catch (err) {
          toast('刷新失败：' + (err.message || err));
        } finally {
          b.disabled = false;
          b.style.opacity = '';
          b.removeAttribute('aria-busy');
          b.title = prevTitle;
        }
      }),
      iconBtn('refresh', '刷新全部模型目录', refreshModelCatalogs),
      iconBtn('export', '导出账号（脱敏）', exportAccounts),
    );

    chromeHost.append(tools);
    chrome = { seg, tools, searchInput, listBtn, gridBtn, plans, tagBtn, sortBtn, channelOnly };
  }

  function syncChrome() {
    if (!chrome) buildChrome();
    const usable = ['overview', 'channels', 'usage'].includes(currentId);
    chromeHost.hidden = !usable;
    if (!usable) return;
    for (const b of chrome.seg.querySelectorAll('[data-seg]')) {
      const on = filter.seg === b.dataset.seg;
      b.classList.toggle('on', on);
      b.setAttribute('aria-selected', on ? 'true' : 'false');
    }
    const isChannels = currentId === 'channels';
    for (const el of chrome.channelOnly) el.hidden = !isChannels;
    chrome.listBtn.classList.toggle('on', filter.layout === 'list');
    chrome.gridBtn.classList.toggle('on', filter.layout === 'grid');
    chrome.sortBtn.classList.toggle('on', filter.sortQuota);
    setText(chrome.plans, '全部套餐 (' + (meta.accounts || 0) + ')');
    setText(chrome.tagBtn, filter.tag ? '标签：' + filter.tag : '标签');
  }

  function resetFilter() {
    setFilter({ seg: 'all', query: '', tag: null, sortQuota: false, more: { quota: false, calls: false, off: false } });
    if (chrome) chrome.searchInput.value = '';
  }

  /* 筛选只更新状态 + 重挂视图，不动工具条，因此输入焦点不会被抢走 */
  function setFilter(patch) {
    filter = { ...filter, ...patch };
    syncChrome();
    mountView(currentId, currentView, false);
  }

  /* ---------- 视图 ---------- */
  function mountView(id, view, animate) {
    if (!view) return;
    const routeChanged = id !== currentId;
    const leavingHUD = currentId === 'hud' && id !== 'hud';
    currentId = id;
    currentView = view;
    app.classList.toggle('hud-mode', id === 'hud');
    document.body.classList.toggle('hud-mode', id === 'hud');
    if (leavingHUD) exitHUD();
    if (routeChanged) {
      paintNav();
      paintHead(view);
      syncChrome();
    }
    // 每次挂载换新容器：旧视图里晚到的异步结果只会写进已脱离文档的节点
    const nextHost = h('main', { class: 'view', id: 'view', tabindex: '-1' });
    if (animate && routeChanged) nextHost.classList.add('view--enter');
    viewHost.replaceWith(nextHost);
    viewHost = nextHost;
    const ctx = {
      store: { get: () => ({ filter, meta }), set: setFilter },
      navigate,
      refresh: () => mountView(currentId, currentView, false),
      refreshAll,
      toast,
    };
    try {
      view.mount(viewHost, ctx);
    } catch (e) {
      console.error(e);
      viewHost.append(h('div', { class: 'error-box' }, '页面渲染失败：' + (e.message || e)));
    }
  }

  /* ---------- 数据 ---------- */
  async function loadMeta() {
    try {
      const [v, health, stats, accounts, providers] = await Promise.all([
        api.version().catch(() => ({})),
        api.get('health').catch(() => ({})),
        api.get('sessions/stats').catch(() => ({})),
        api.get('accounts').catch(() => ({ accounts: [] })),
        api.get('providers').catch(() => ({ providers: [] })),
      ]);
      meta.version = v.version || '';
      meta.built = v.build_time || '';
      meta.ready = health.status === 'ok';
      meta.accounts = health.accounts ?? (accounts.accounts || []).length;
      meta.sessions = stats.total ?? stats.count ?? 0;
      meta.accountsList = accounts.accounts || [];
      meta.providers = providers.providers || providers || [];
    } catch (e) {
      console.error('meta', e);
    }
  }

  async function refreshModelCatalogs() {
    try {
      const r = await api.post('providers/models/refresh-all', {});
      toast('模型目录：' + (r.refreshed || 0) + ' 个渠道成功' + (r.failed ? '，' + r.failed + ' 个失败' : ''));
      await refreshAll();
    } catch (err) { toast('刷新失败：' + (err.message || err)); }
  }

  async function refreshAll() {
    await loadMeta();
    paintNav();
    if (currentView) paintHead(currentView);
    syncChrome();
    mountView(currentId, currentView, false);
  }

  function onRoute(r, view) {
    if (!view) return;
    mountView(r.id, view, true);
  }

  /* ---------- 全局键盘 ---------- */
  function typingInField(t) {
    if (!t) return false;
    const tag = t.tagName;
    return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT' || t.isContentEditable;
  }

  let gAt = 0;
  addEventListener('keydown', (e) => {
    const mod = isMac ? e.metaKey : e.ctrlKey;
    if (mod && e.key.toLowerCase() === 'k') {
      e.preventDefault();
      paletteOpen() ? closePalette() : openCommandPalette();
      return;
    }
    if (e.key === '/' && !typingInField(e.target) && !paletteOpen()) {
      const input = chrome && chrome.searchInput;
      if (input && !chromeHost.hidden) { e.preventDefault(); input.focus(); input.select(); }
      return;
    }
    if (e.key === 'Escape' && navIsOpen()) { closeNav(); return; }
    if (typingInField(e.target) || paletteOpen() || mod || e.altKey) return;
    if (e.key.toLowerCase() === 'g') { gAt = Date.now(); return; }
    if (Date.now() - gAt < 900 && KEY_ROUTES[e.key.toLowerCase()]) {
      e.preventDefault();
      navigate(KEY_ROUTES[e.key.toLowerCase()]);
      gAt = 0;
    }
  });

  /* 窄屏点击导航后关闭抽屉；窗口变宽时清掉状态，避免遮罩残留 */
  addEventListener('resize', () => { if (innerWidth > 900 && navIsOpen()) closeNav(); });

  loadMeta().then(() => {
    paintNav();
    if (currentView) {
      paintHead(currentView);
      syncChrome();
      mountView(currentId, currentView, false);
    }
  });
  addEventListener('aether:refresh', () => refreshAll());

  return { onRoute, refreshAll };
}
