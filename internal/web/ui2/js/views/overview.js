/* 概览（自有实现）：KPI + API 服务 + 渠道健康 + 额度临期，全部读真实接口。 */
import { h, clear, setText } from '../dom.js';
import * as api from '../api.js';
import { icon } from '../icons.js';
import { chip, dotChip, btn, iconBtn, card, meter, select, field, skeleton, errorBox, empty, toast, copyText, confirmDialog, secretRow, fmtTokens, fmtMoney, fmtReset } from '../components.js';
import { brandIcon } from '../brandicons.js';
import { usedPct } from '../format.js';

const POOL = { 'Claude and GPT models': 'Claude', 'Gemini Models': 'Gemini' };

/* 上次成功加载的快照：二次访问先渲染它，再静默刷新。
   24 小时过期；写入失败（隐私模式/配额）静默忽略。 */
const SNAP_KEY = 'aether.overview.snapshot.v1';
const SNAP_TTL = 24 * 3600 * 1000;
function readSnapshot() {
  try {
    const raw = localStorage.getItem(SNAP_KEY);
    if (!raw) return null;
    const snap = JSON.parse(raw);
    if (!snap || snap.v !== 1 || !snap.data || !snap.at) return null;
    if (Date.now() - snap.at > SNAP_TTL) return null;
    return snap;
  } catch { return null; }
}
function writeSnapshot(data) {
  try {
    localStorage.setItem(SNAP_KEY, JSON.stringify({ v: 1, at: Date.now(), data }));
  } catch { /* 配额或隐私模式：忽略 */ }
}
const isTest = (id) => /^mock/.test(id) || id === 'openai-compat';

// liveState 指向当前挂载实例的 state，供 unmount 清理定时器用。
let liveState = null;

export default {
  title: '概览',
  subtitle: '今天哪些渠道能用、还剩多少、什么时候恢复',

  // 切页时停掉未触发的额度重试定时器：
  // 否则页面已经离开，2.5s/6s 后的重试仍会打 usage/quotas。
  unmount() {
    if (!liveState) return;
    for (const t of liveState.timers) clearTimeout(t);
    liveState.timers.length = 0;
    liveState = null;
  },

  mount(host, ctx) {
    const state = { data: null, timers: [], at: 0, cached: false };
    liveState = state;
    // 单个接口不应把首屏拖住（会话扫描在冷启动/多 WSL 环境下可能很慢）。
    const withTimeout = (p, ms) => Promise.race([p, new Promise((res) => setTimeout(() => res({}), ms))]);

    const snap = readSnapshot();
    if (snap) {
      state.data = snap.data;
      state.at = snap.at;
      state.cached = true;
      render();
    } else {
      host.append(loading());
    }
    load();

    async function load() {
      try {
        // 首屏分两批：先拿"决定页面骨架"的关键数据并立刻渲染，
        // 再补统计类数据。
        //
        // 以前 12 个接口一个 Promise.all 全等：其中 sessions/stats、
        // usage/quotas、channels/status 都可能慢（要扫盘/探上游），
        // 任何一个慢都会把整页拖住。现在关键数据一到就先画出来。
        const [health, accounts, providers, settings, runtime] = await Promise.all([
          api.get('health').catch(() => ({})),
          api.get('accounts').catch(() => ({ accounts: [] })),
          api.get('providers').catch(() => ({ providers: [] })),
          api.get('settings').catch(() => ({})),
          api.get('runtime').catch(() => ({})),
        ]);
        state.data = {
          health, accounts: accounts.accounts || [],
          providers: providers.providers || providers || [], gateway: providers.gateway || {},
          usage: null, ledger: null, quotas: [], stats: {}, settings,
          keys: [], clients: [], status: { entries: [], checkedAt: 0 }, runtime,
        };
        state.at = Date.now();
        state.cached = false;
        render();

        // 第二批：统计与补充信息，到了再刷新一次。
        const [usage, ledger, quotas, stats, keys, clients, status] = await Promise.all([
          api.get('usage?days=1').catch(() => ({ accounts: [] })),
          api.get('usage/requests?period=today&limit=1').catch(() => null),
          api.get('usage/quotas').catch(() => []),
          withTimeout(api.get('sessions/stats').catch(() => ({})), 3000),
          api.get('caller-keys').catch(() => ({ keys: [] })),
          api.get('clients').catch(() => ({ clients: [] })),
          api.get('channels/status').catch(() => ({ entries: [], checkedAt: 0 })),
        ]);
        Object.assign(state.data, {
          usage, ledger, quotas: Array.isArray(quotas) ? quotas : [], stats,
          keys: keys.keys || keys.caller_keys || [], clients: clients.clients || [], status,
        });
        state.at = Date.now();
        state.cached = false;
        writeSnapshot(state.data);
        render();
      } catch (e) {
        if (state.data) {
          // 有快照就继续展示，只提示刷新失败
          toast('刷新失败，显示的是 ' + clock(state.at) + ' 的快照');
          state.cached = true;
          render();
          return;
        }
        clear(host);
        host.append(errorBox('概览读取失败：' + (e.message || e), load));
      }
    }

    function filteredAccounts() {
      const { filter } = ctx.store.get();
      let list = state.data.accounts.slice();
      if (filter.seg === 'real') list = list.filter((a) => !isTest(a.provider_id));
      if (filter.seg === 'wsl') list = [];
      if (filter.tag) list = list.filter((a) => (a.tags || []).includes(filter.tag));
      if (filter.more.quota) list = list.filter((a) => ((a.quota && a.quota.windows) || []).length > 0);
      if (filter.more.off) list = list.filter((a) => providerOf(a.provider_id)?.off);
      const q = filter.query.trim().toLowerCase();
      if (q) {
        list = list.filter((a) => {
          const p = providerOf(a.provider_id);
          return [a.label, a.account_id, a.provider_id, p?.name, ...(a.tags || []), ...((p && p.models) || []).map((m) => m.id)].join(' ').toLowerCase().includes(q);
        });
      }
      return list;
    }
    const providerOf = (id) => state.data.providers.find((p) => p.id === id);

    /* 概览与用量页统一口径：优先逐请求账本（同一价格表、同一时间窗），
       只有账本不可用时才退回按天聚合的 usage.json。
       过滤条只排除被隐藏的账号；未归属账号的请求（id 为空）始终计入。 */
    function todayUsage() {
      const d = state.data;
      const allowedIds = new Set(filteredAccounts().map((a) => a.account_id));
      const allIds = new Set((d.accounts || []).map((a) => a.account_id));
      const led = d.ledger;
      if (led && typeof led.calls === 'number') {
        const excluded = new Set([...allIds].filter((id) => !allowedIds.has(id)));
        const out = {
          calls: led.calls || 0, errors: led.errors || 0,
          input: led.input || 0, output: led.output || 0,
          cache_read: led.cache_read || 0, cache_write: led.cache_write || 0,
          cost: led.cost || 0, unpriced: led.unpriced || 0,
        };
        for (const row of (led.by && led.by.account) || []) {
          if (!excluded.has(row.id)) continue;
          for (const k of ['calls', 'errors', 'input', 'output', 'cache_read', 'cache_write', 'cost']) {
            out[k] -= row[k] || 0;
          }
        }
        for (const k of ['calls', 'errors', 'input', 'output', 'cache_read', 'cache_write', 'cost']) {
          out[k] = Math.max(0, out[k]);
        }
        return out;
      }
      const use = ((d.usage && d.usage.accounts) || []).filter((a) => allowedIds.has(a.id) || !a.id);
      const sum = (k) => use.reduce((n, x) => n + (x[k] || 0), 0);
      return {
        calls: sum('calls'), errors: sum('errors'), input: sum('input'), output: sum('output'),
        cache_read: sum('cache_read'), cache_write: sum('cache_write'), cost: sum('cost'), unpriced: 0,
      };
    }

    function render() {
      const d = state.data;
      clear(host);
      const allowed = filteredAccounts();
      const provIds = new Set(allowed.map((a) => a.provider_id));
      const usage = todayUsage();
      const calls = usage.calls, errs = usage.errors, cost = usage.cost, cache = usage.cache_read;
      // Token 统一口径：输入（含缓存读写）+ 输出，与用量页一致。
      const inputTotal = usage.input + usage.cache_read + usage.cache_write;
      const tokens = inputTotal + usage.output;
      const cacheRate = inputTotal ? (cache / inputTotal) * 100 : 0;
      const ready = allowed.filter((a) => a.state === 'ready').length;
      const testN = allowed.filter((a) => isTest(a.provider_id)).length;
      const realN = allowed.length - testN;
      const sessions = d.stats.total ?? d.stats.count ?? 0;
      const rate = calls ? (((calls - errs) / calls) * 100).toFixed(1) : null;

      const kpis = h('div', { class: 'kpis' },
        kpi('可用账号', ready, realN + ' 个真实登录态 · ' + testN + ' 个测试渠道'),
        kpi('今日请求', calls, '失败 ' + errs + ' · 成功率 ' + (rate == null ? '—' : rate + '%')),
        kpi('今日 Token', fmtTokens(tokens), [
          '缓存 ' + fmtTokens(cache) + ' · 命中 ' + cacheRate.toFixed(1) + '%',
          '成本 ' + fmtMoney(cost) + (usage.unpriced ? ' · 未计价 ' + usage.unpriced + ' 次' : ''),
        ]),
        kpi('会话', sessions, '本机记录 · 只读'));

      // 信息层级：首启引导 → 状态 → 聚合链路 → 余量 → 健康 → 接入配置
      if (!state.data.accounts.length) host.append(onboardingCard());
      // 「更新于」并进聚合链路卡的头部，不再靠负 margin 漂在两张卡之间的空白里
      const topo = topoCard(d, allowed);
      const status = statusLine();
      if (status) topo.querySelector('.card-h')?.append(status);
      host.append(kpis, topo, h('div', { class: 'ov-grid' }, quotaCard(d.quotas, provIds), healthCard(d.status, provIds)), apiCard());
    }

    // 首次启动（账号库为空）时的三步引导；有账号后不再出现。
    function onboardingCard() {
      const step = (n, title, desc, action) => h('div', { class: 'row' },
        h('span', { class: 'mono meta', style: 'min-width:18px' }, String(n)),
        h('div', { style: 'min-width:0;flex:1' }, h('div', { class: 'name' }, title), h('div', { class: 'meta' }, desc)),
        action);
      const c = card('三步接入', { chips: [chip('首次使用', 'green')] });
      c.el.append(
        step(1, '设置网关密钥', '本机回环可免；开放局域网必填', btn('去设置', { onClick: () => ctx.navigate('settings') })),
        step(2, '添加第一个渠道账号', '订阅渠道按提示登录，或添加 OpenAI 兼容账号', btn('去接入', { primary: true, onClick: () => ctx.navigate('onboard') })),
        step(3, '复制接入片段', '把网关地址与密钥填进客户端', btn('去网关页', { onClick: () => ctx.navigate('gateway') })),
        h('div', { class: 'meta', style: 'margin-top:8px' }, '数据默认保存在可执行文件旁的 data/ 目录；版本与构建时间见「设置 → 关于」。'));
      return c.el;
    }

    function clock(ts) {
      const d = new Date(ts);
      return String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0');
    }

    function statusLine() {
      if (!state.at) return null;
      return h('div', { class: 'ov-status' },
        h('span', { class: 'ov-dot' + (state.cached ? ' wait' : '') }),
        h('span', null, state.cached ? '快照 ' + clock(state.at) : '更新于 ' + clock(state.at)),
        state.cached ? h('span', null, ' · 正在刷新…') : null);
    }

    function kpi(label, value, desc) {
      // desc 传数组 = 按语义分好行，避免汉字被自动折行切成「成 / 本」
      const d = h('div', { class: 'd' }, Array.isArray(desc)
        ? desc.map((line) => h('span', { class: 'kpi-line' }, line))
        : desc);
      return h('section', { class: 'kpi' }, h('div', { class: 'k' }, label), h('div', { class: 'v num' }, String(value)), d);
    }

    /* 聚合链路：把「N 个渠道收敛成 1 个端点」画出来，这是产品唯一无可替代的那句话 */
    function topoCard(d, allowed) {
      const baseURL = d.runtime.public_addr || d.gateway?.publicURL || location.origin;
      const byProv = new Map();
      for (const a of allowed) {
        const id = a.provider_id;
        if (!byProv.has(id)) byProv.set(id, 0);
        byProv.set(id, byProv.get(id) + 1);
      }
      // 全部渠道都画出来：7-10 个自动紧凑；>10 固定高度 + 滚轮。
      const rows = [...byProv.entries()];
      const scrollMode = rows.length > 10;
      const nodeH = rows.length > 6 ? 28 : 34;
      const nodeGap = rows.length > 6 ? 4 : 6;
      const c = card('聚合链路', {
        chips: [chip(byProv.size + ' 个渠道 → 3 个端点', 'plain')],
        class: 'span-2',
      });

      const src = h('div', { class: 'topo-src' });
      const nodes = [];
      const paths = [];
      const EP_LABEL = { openai: 'OpenAI', anthropic: 'Anthropic', gemini: 'Gemini' };
      let selected = null;

      if (!rows.length) src.append(h('div', { class: 'meta' }, '还没有可路由的渠道，先接入一个。'));

      // 连线用像素坐标：每条渠道汇入 x=84 的竖直总线，再按三行端点的中心（20/66/112）引出支线
      const nodeStep = nodeH + nodeGap;
      const busX = 84;
      const epH = 40;
      const epGap = 6;
      const epPad = 14; // .topo-out 的上内边距
      const epCenters = [0, 1, 2].map((i) => epPad + epH / 2 + i * (epH + epGap));
      const busTop = epCenters[0];
      const busBottom = epCenters[2];
      const busMid = epCenters[1];
      const viewH = scrollMode ? 6 * nodeStep - nodeGap : rows.length * nodeStep + 6;
      const svgH = Math.max(epCenters[2] + epH / 2, viewH, 120);

      const scrollBox = h('div', { class: 'topo-src-scroll' + (scrollMode ? ' scrollable' : '') });
      scrollBox.style.gap = nodeGap + 'px';
      if (scrollMode) scrollBox.style.maxHeight = viewH + 'px';
      src.append(scrollBox);

      const lines = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      lines.setAttribute('class', 'topo-lines');
      lines.setAttribute('viewBox', `0 0 96 ${svgH}`);
      lines.setAttribute('preserveAspectRatio', 'none');
      lines.setAttribute('aria-hidden', 'true');
      lines.style.height = svgH + 'px';

      for (let i = 0; i < rows.length; i++) {
        const y = i * nodeStep + nodeH / 2;
        const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
        path.setAttribute('d', `M0 ${y} C42 ${y} 62 ${busMid} ${busX} ${busMid}`);
        path.setAttribute('fill', 'none');
        path.setAttribute('stroke', 'currentColor');
        path.setAttribute('stroke-width', '1');
        path.setAttribute('opacity', '0.5');
        paths.push(path);
        lines.append(path);
      }

      const bus = document.createElementNS('http://www.w3.org/2000/svg', 'path');
      bus.setAttribute('d', `M${busX} ${busTop} L${busX} ${busBottom}`);
      bus.setAttribute('class', 'topo-bus');
      bus.setAttribute('fill', 'none');
      bus.setAttribute('stroke', 'currentColor');
      bus.setAttribute('stroke-width', '1');
      bus.setAttribute('opacity', '0.5');
      lines.append(bus);

      for (const y of epCenters) {
        const stub = document.createElementNS('http://www.w3.org/2000/svg', 'path');
        stub.setAttribute('d', `M${busX} ${y} L96 ${y}`);
        stub.setAttribute('class', 'topo-bus');
        stub.setAttribute('fill', 'none');
        stub.setAttribute('stroke', 'currentColor');
        stub.setAttribute('stroke-width', '1');
        stub.setAttribute('opacity', '0.5');
        lines.append(stub);
      }

      const out = h('div', { class: 'topo-out' });
      const eps = [
        ['openai', 'OpenAI Base', baseURL + '/v1'],
        ['anthropic', 'Anthropic Base', baseURL],
        ['gemini', 'Gemini Base', baseURL + '/v1beta'],
      ];
      const epEls = new Map();
      for (const [key, label, url] of eps) {
        const row = h('div', { class: 'ep', 'data-ep': key },
          h('div', { class: 't' }, label),
          h('div', { class: 'u', title: url }, url));
        const copy = iconBtn('copy', '复制 ' + label, () => copyText(url));
        copy.style.marginLeft = 'auto';
        row.style.display = 'flex';
        row.style.alignItems = 'center';
        row.style.gap = '8px';
        row.append(copy);
        epEls.set(key, row);
        out.append(row);
      }
      out.append(h('div', { class: 'm' }, '端点随监听端口自动适配 · 客户端只需一个 base_url'));
      const detail = h('div', { class: 'topo-detail', 'aria-live': 'polite' });
      out.append(detail);

      for (const [id, count] of rows) {
        const p = (d.providers || []).find((x) => x.id === id);
        const bi = brandIcon(id, 14);
        const node = h('button', {
          class: 'topo-node', type: 'button', 'data-provider': id, 'aria-pressed': 'false',
          title: '查看它汇入的端点与余量',
          onclick: () => select(id),
        }, bi || icon('channels', 16), h('span', { class: 'n' }, p?.name || p?.display_name || id),
          h('span', { class: 'c' }, count + ' 账号'));
        node.style.height = nodeH + 'px';
        nodes.push({ id, node });
        scrollBox.append(node);
      }
      if (rows.length) src.append(h('div', { class: 'topo-hint' },
        scrollMode
          ? '全部 ' + rows.length + ' 个渠道 · 滚轮查看更多 · 点渠道看余量'
          : rows.length > 6
            ? '全部 ' + rows.length + ' 个渠道 · 点渠道看它汇入哪个端点、还剩多少'
            : '点渠道 → 看它汇入哪个端点、还剩多少'));

      // >10 时连线随滚动重绘：SVG 固定在视口，节点滚动，路径按节点当前屏幕位置更新。
      const layoutPaths = () => {
        const boxRect = scrollBox.getBoundingClientRect();
        paths.forEach((path, i) => {
          const node = nodes[i]?.node;
          if (!node) return;
          const r = node.getBoundingClientRect();
          const y = r.top - boxRect.top + r.height / 2;
          const visible = !scrollMode || (y >= -nodeH && y <= viewH + nodeH);
          path.style.display = visible ? '' : 'none';
          if (visible) path.setAttribute('d', `M0 ${y} C42 ${y} 62 ${busMid} ${busX} ${busMid}`);
        });
        if (scrollMode) {
          const max = scrollBox.scrollHeight - scrollBox.clientHeight;
          scrollBox.classList.toggle('can-up', scrollBox.scrollTop > 1);
          scrollBox.classList.toggle('can-down', scrollBox.scrollTop < max - 1);
        }
      };
      // 行数很少，滚动时直接重绘；不依赖 rAF（后台标签页会节流）。
      scrollBox.addEventListener('scroll', layoutPaths, { passive: true });
      if (typeof ResizeObserver !== 'undefined') new ResizeObserver(layoutPaths).observe(scrollBox);
      requestAnimationFrame(layoutPaths);
      setTimeout(layoutPaths, 120);

      // 渠道能服务哪些端点：由模型 id 推断（claude* → Anthropic，gemini* → Gemini，其余 OpenAI）
      function familiesOf(provider) {
        const fams = new Set();
        for (const m of provider?.models || []) {
          const id = String(m.id || '').toLowerCase();
          if (id.includes('claude')) fams.add('anthropic');
          else if (id.includes('gemini')) fams.add('gemini');
          else fams.add('openai');
        }
        if (!fams.size) fams.add('openai');
        return fams;
      }

      function renderDetail(id, provider, fams) {
        const qs = (d.quotas || []).filter((q) => (q.provider || q.name) === id);
        const wins = [];
        for (const q of qs) for (const w of q.windows || []) wins.push({ ...w, user: q.user });
        let rem = null;
        let urgent = null;
        for (const w of wins) {
          const used = Math.max(0, Math.min(1, Number(w.used) || 0));
          const left = Math.round((1 - used) * 100);
          if (rem === null || left < rem) { rem = left; urgent = w; }
        }
        const name = provider?.name || provider?.display_name || id;
        detail.replaceChildren(
          h('div', { class: 'topo-detail-h' },
            h('b', null, name),
            chip(byProv.get(id) + ' 账号', 'plain'),
            h('span', { class: 'grow' }),
            btn('查看账号', {
              class: 'sm',
              onClick: () => {
                ctx.store.set({ query: name });
                ctx.navigate('channels');
              },
            })),
          h('div', { class: 'topo-detail-m' },
            '汇入 ' + [...fams].map((f) => EP_LABEL[f]).join(' / ')
            + (wins.length ? ' · ' + wins.length + ' 个额度窗口' : '')),
          rem === null
            ? h('div', { class: 'meta' }, '该渠道没有可读的额度窗口。')
            : h('div', null,
              meter(rem, rem <= 10 ? 'red' : rem <= 40 ? 'amber' : ''),
              h('div', {
                class: 'meta',
                style: 'margin-top:5px' + (rem <= 10 ? ';color:var(--red)' : rem <= 40 ? ';color:var(--amber)' : ''),
              },
              '最低剩余 ' + rem + '% · ' + (urgent?.name || '窗口')
              + (urgent?.resetsAt ? ' · ' + fmtReset(urgent.resetsAt) : ''))),
        );
      }

      function select(id) {
        selected = selected === id ? null : id;
        if (scrollMode) {
          const target = nodes.find((item) => item.id === id)?.node;
          if (target) {
            const top = target.offsetTop;
            if (top < scrollBox.scrollTop) scrollBox.scrollTo({ top, behavior: 'smooth' });
            else if (top + nodeH > scrollBox.scrollTop + viewH) scrollBox.scrollTo({ top: top + nodeH - viewH, behavior: 'smooth' });
          }
        }
        for (const item of nodes) {
          const on = item.id === selected;
          item.node.classList.toggle('on', on);
          item.node.classList.toggle('dim', !!selected && !on);
          item.node.setAttribute('aria-pressed', on ? 'true' : 'false');
        }
        paths.forEach((path, i) => {
          const on = rows[i]?.[0] === selected;
          path.classList.toggle('on', on);
          path.classList.toggle('dim', !!selected && !on);
        });
        for (const row of epEls.values()) row.classList.remove('on', 'dim');
        if (!selected) {
          detail.classList.remove('on');
          detail.replaceChildren();
          return;
        }
        const provider = (d.providers || []).find((x) => x.id === selected);
        const fams = familiesOf(provider);
        for (const [key, row] of epEls) {
          const on = fams.has(key);
          row.classList.toggle('on', on);
          row.classList.toggle('dim', !on);
        }
        renderDetail(selected, provider, fams);
        detail.classList.add('on');
      }

      c.el.append(h('div', { class: 'topo' }, src, lines, out));
      return c.el;
    }

    /* 接入卡：只留「入口 + 密钥状态 + 调用方」，配置动作收进网关页 */
    function apiCard() {
      const d = state.data;
      const gw = d.gateway || {};
      const baseURL = d.runtime.public_addr || d.gateway?.publicURL || location.origin;
      const lanURL = (gw.lanURLs && gw.lanURLs[0]) || '';
      const lan = !!(gw.lan || d.settings.lan);
      const keyMasked = d.settings.api_key || '';
      const c = card('接入', {
        chips: [dotChip(d.health.status === 'ok' ? '运行中' : '未就绪', d.health.status === 'ok' ? 'green' : 'red')],
        class: 'span-2',
      });
      const right = h('div', { class: 'right' });
      right.append(btn('网关与探针', { onClick: () => ctx.navigate('gateway') }));
      c.head.append(right);

      const entryVal = h('span', {
        class: 'entry-url mono', title: lan && lanURL ? lanURL : baseURL,
      }, lan && lanURL ? lanURL : baseURL);
      // 这个下拉框必须**真的切换监听**，不能只换显示文本。
      //
      // 过去选"局域网"只是把文本换成 lanURL（而 lanURL 为空时又退回 baseURL），
      // 于是地址纹丝不动、后端 lan 仍是 false —— 用户以为开了，其实没开。
      // 现在选"局域网"会先调 /api/settings 打开监听，再用后端返回的真实地址回填。
      const entrySel = select(
        [{ value: 'local', label: '本机' }, { value: 'lan', label: '局域网' }],
        lan ? 'lan' : 'local',
        (v) => { void (async () => {
          if (v === 'local') {
            entryVal.textContent = baseURL;
            entryVal.title = baseURL;
            return;
          }
          // 局域网地址要等监听切过去才有（端口是主端口+1），先给个可读提示，
          // 免得用户对着一个没变的地址以为没生效。
          entryVal.textContent = '正在开启局域网监听…';
          try {
            // 用专门的 /api/settings/lan（不是 /api/settings）：
            // 只有它才会真正去 SetLAN（重新绑定 0.0.0.0:主端口+1）。
            await api.post('settings/lan', { on: true });
            await load();
            const urls = (state.data.gateway && state.data.gateway.lanURLs) || [];
            const next = urls[0] || '';
            if (next) {
              entryVal.textContent = next;
              entryVal.title = next;
              toast('局域网已开启 · ' + next);
            } else {
              entryVal.textContent = baseURL;
              entryVal.title = baseURL;
              toast('已请求开启局域网，但没拿到网卡地址；请检查网络或防火墙');
            }
          } catch (e) {
            // 最常见的原因是"还没设密钥"——远程请求会被全拒，后端直接拒绝开启。
            // 把用户导到设置页去做这一步，而不是让他对着一个失败 toast 猜。
            entryVal.textContent = baseURL;
            entryVal.title = baseURL;
            const msg = String(e.message || e);
            if (/密钥|api key|caller key/i.test(msg)) {
              toast('开启局域网前需要先设密钥，请到设置页创建');
              ctx.navigate('settings', { tab: 'network' });
            } else {
              toast('开启局域网失败：' + msg);
            }
          }
        })().catch((e) => toast('开启局域网失败：' + (e.message || e))); }, { label: '入口' });
      const copyBtn = iconBtn('copy', '复制入口', () => copyText(entryVal.textContent));

      const generateKey = async () => {
        const exists = !!keyMasked;
        const ok = await confirmDialog(
          exists ? '轮换网关密钥？' : '生成网关密钥？',
          exists ? '使用旧密钥的客户端会立刻无法接入，需要更新配置。' : '其它机器接入时必须带上新密钥；本机回环可免密钥。',
          { danger: exists, confirmText: exists ? '轮换' : '生成' },
        );
        if (!ok) return;
        // 统一走服务端生成（crypto/rand + 立即持久化），
        // 与设置页的「生成」是同一个接口、同一套形态。
        try {
          const r = await api.post('settings/gateway-key', {});
          toast(exists ? '密钥已轮换' : '密钥已生成');
          if (r && r.api_key) copyText(r.api_key);
          await load();
        } catch (e) { toast('生成失败：' + (e.message || e)); }
      };

      // 掩码 → 眼睛看明文 → 复制；轮换走破坏性确认
      // keyMasked 已是后端打码值：secretRow 会据此隐藏"显示/复制"，只留轮换。
      const keyRow = secretRow(keyMasked, { onRotate: generateKey, rotateLabel: '轮换', maskedOnly: !!keyMasked });

      // 用 field() 保留 aria-label 关联，再叠成「标签在上、控件在下」——窄宽度下 URL 才读得全
      const entryField = h('div', { class: 'kv-row' },
        h('span', { class: 'kv-k' }, '入口'),
        h('div', { class: 'kv-v' }, entrySel, entryVal, copyBtn));
      const keyField = h('div', { class: 'kv-row' },
        h('span', { class: 'kv-k' }, '密钥'),
        h('div', { class: 'kv-v' }, keyRow));
      const left = h('div', { class: 'inset' }, entryField, keyField);

      const keysHead = h('div', { class: 'row' },
        h('span', { class: 'name' }, '调用方密钥'),
        h('span', { class: 'grow' }),
        chip(d.keys.length + ' 把', d.keys.length ? 'plain' : 'amber'),
        btn('管理', { onClick: () => ctx.navigate('gateway') }));
      const rightCol = h('div', { class: 'inset' }, keysHead);
      if (!d.keys.length) {
        rightCol.append(h('div', { class: 'meta' }, '还没有调用方密钥：在「网关与探针」里生成，客户端用它接入。'));
      } else {
        for (const k of d.keys.slice(0, 3)) {
          rightCol.append(h('div', { class: 'row' },
            h('span', { class: 'name' }, k.name || k.id || 'key'),
            h('span', { class: 'meta' }, k.rpm ? 'RPM ' + k.rpm : 'RPM —'),
            h('span', { class: 'grow' }),
            h('span', { class: 'meta' }, (k.used || 0) + ' 次')));
        }
      }
      const clientRow = h('div', { class: 'row row-wrap' }, h('span', { class: 'name' }, '客户端'), h('span', { class: 'grow' }));
      if (!d.clients.length) clientRow.append(h('span', { class: 'meta' }, '未检测到'));
      else {
        const wrap = h('div', { class: 'chips-wrap' });
        for (const cl of d.clients.slice(0, 5)) wrap.append(chip(cl.name || cl, 'plain'));
        clientRow.append(wrap);
      }
      rightCol.append(clientRow);
      rightCol.append(h('div', { class: 'api-note' }, '密钥只回掩码：明文只在生成时出现一次，界面不回显、不落日志。'));

      c.el.append(h('div', { class: 'api-cols' }, left, rightCol));
      return c.el;
    }

    function healthCard(status, provIds) {
      const checked = status.checkedAt || 0;
      const c = card('渠道健康', { chips: [chip(checked ? '刚测' : '未探测', checked ? 'green' : 'plain')] });
      const entries = (status.entries || []).filter((e) => provIds.has(e.provider)).slice(0, 8);
      if (!entries.length) { c.el.append(empty('还没有探测结果', '在「网关与探针」里发起一次探测。', btn('去探测', { onClick: () => ctx.navigate('gateway') }))); return c.el; }
      for (const e of entries) {
        // 同一页里拓扑用 display_name、健康卡用 provider_id 会读成两个东西，统一走显示名
        const p = (state.data.providers || []).find((x) => x.id === e.provider);
        const name = p?.name || p?.display_name || e.provider;
        const right = h('span', { class: 'inline' },
          chip(e.ok ? String(e.code || 200) : (e.code ? String(e.code) : '失败'), e.ok ? 'green' : 'red'),
          e.ok ? h('span', { class: 'meta' }, (e.ms / 1000).toFixed(2) + ' s') : h('span', { class: 'meta' }, (e.error || '').slice(0, 40)));
        c.el.append(h('div', { class: 'row' }, h('span', { class: 'name' }, name + (e.label ? ' · ' + e.label : '')), h('span', { class: 'grow' }), right));
      }
      return c.el;
    }

    function quotaCard(quotas, provIds) {
      const collect = (list) => {
        const rows = [];
        for (const q of list || []) {
          if (!provIds.has(q.provider)) continue;
          const wins = q.windows || [];
          const poolRows = new Set(wins.filter((w) => !w.family && w.pool).map((w) => w.pool));
          for (const w of wins) {
            if (w.family && w.pool && poolRows.has(w.pool)) continue;
            const pct = usedPct(w);
            const rem = Math.round((100 - pct) * 10) / 10;
            if (pct > 0) rows.push({ name: (q.name || q.provider) + ' · ' + (w.pool ? (POOL[w.pool] || w.pool) + ' · ' : '') + w.name, rem, reset: w.resetsAt || '' });
          }
        }
        rows.sort((a, b) => a.rem - b.rem);
        return rows;
      };
      const rows = collect(quotas);
      const c = card('额度余量', { chips: [chip('剩余', 'plain'), chip(rows.length ? rows.length + ' 条' : '暂无', 'plain')] });
      const body = h('div');
      c.el.append(body);
      const fill = (list) => {
        clear(body);
        const cs = c.head.querySelectorAll('.chip');
        if (cs[1]) setText(cs[1], list.length ? list.length + ' 条' : '暂无');
        if (!list.length) { body.append(empty('暂无可读的额度窗口', '额度读数还没回来，稍后自动重试。')); return false; }
        for (const r of list.slice(0, 6)) {
          const tone = r.rem <= 10 ? 'red' : r.rem <= 40 ? 'amber' : '';
          body.append(h('div', { class: 'quota-item' },
            h('div', { class: 'lbl' }, h('span', { class: 'name' }, r.name),
              h('span', { class: 'pct', style: 'color:var(--' + (r.rem <= 10 ? 'red' : r.rem <= 40 ? 'amber' : 'green') + ')' }, Math.round(r.rem) + '%')),
            meter(r.rem, tone),
            h('div', { class: 'meta' }, r.reset ? fmtReset(r.reset) : '未提供重置时间')));
        }
        return true;
      };
      if (!fill(rows)) {
        for (const delay of [2500, 6000]) {
          const t = setTimeout(async () => {
            if (!body.isConnected || body.querySelector('.meter')) return;
            try { const q = await api.get('usage/quotas'); const again = collect(Array.isArray(q) ? q : []); if (again.length) fill(again); } catch {}
          }, delay);
          state.timers.push(t);
        }
      }
      return c.el;
    }

    function loading() {
      const box = h('div');
      box.append(h('div', { class: 'kpis' }, [0, 1, 2, 3].map(() => h('div', { class: 'kpi' }, skeleton(3)))));
      box.append(h('div', { class: 'ov-grid' },
        h('div', { class: 'card span-2' }, skeleton(6)),
        h('div', { class: 'card' }, skeleton(4)),
        h('div', { class: 'card' }, skeleton(4))));
      return box;
    }
  },
};
