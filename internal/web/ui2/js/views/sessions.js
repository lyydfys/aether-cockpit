/* 会话：本机客户端本地记录与真实用量（只读）。
   用量完全来自各 Agent 自己写的 JSONL，不依赖官方额度或网关请求账本。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { chip, btn, iconBtn, card, empty, errorBox, skeleton, drawer, toast, select, fmtDate, fmtCost } from '../components.js';

const AGENT_LABEL = {
  'claude-code': 'Claude Code',
  'codex': 'Codex CLI',
  'codex-cli': 'Codex CLI',
  'gemini-cli': 'Gemini CLI',
  'qoder': 'Qoder',
  'opencode': 'OpenCode',
  'trae': 'Trae',
  'trae-intl': 'Trae Intl',
  'codebuddy': 'CodeBuddy',
  'workbuddy': 'WorkBuddy',
  'cursor': 'Cursor',
  'mimo-code': 'MiMo Code',
};

const fmtSize = (n) => {
  const v = Number(n) || 0;
  if (v >= 1 << 20) return (v / (1 << 20)).toFixed(1) + ' MB';
  if (v >= 1 << 10) return (v / (1 << 10)).toFixed(1) + ' KB';
  return v + ' B';
};

const fmtBig = (n) => {
  const v = Number(n) || 0;
  if (v >= 1e8) return (v / 1e8).toFixed(2) + ' 亿';
  if (v >= 1e4) return (v / 1e4).toFixed(2) + ' 万';
  return String(Math.round(v));
};

const agentLabel = (id) => AGENT_LABEL[id] || id || '未知客户端';

export default {
  title: '会话',
  subtitle: '本机客户端的本地会话记录与真实用量（只读）',

  mount(host, ctx) {
    const state = {
      data: null, q: '', agent: null, loading: false,
      view: 'list',
      usage: null, usageDays: 7, usageAgent: '', usageMetric: 'tokens',
      usagePricing: { currency: 'USD', usd_cny: 7.2 },
      usageLoading: false, usageError: '',
    };
    host.append(skeleton(8));
    load();

    const money = (n) => fmtCost(n, state.usagePricing.currency, state.usagePricing.usd_cny);

    async function load() {
      state.loading = true;
      try {
        const [first, stats, pricing] = await Promise.all([
          api.get('sessions/manage'),
          api.get('sessions/stats').catch(() => ({})),
          api.get('pricing').catch(() => null),
        ]);
        if (pricing) {
          state.usagePricing = { currency: pricing.currency || 'USD', usd_cny: Number(pricing.usd_cny) || 7.2 };
        }
        const agents = first.agents || [];
        const all = [...(first.sessions || [])];
        // /sessions/manage 默认只返回一个 agent 的会话；逐个补齐其余 agent，
        // 否则页签切到别的客户端永远是空的。
        const rest = await Promise.all(agents
          .filter((a) => a.id !== first.agent)
          .map((a) => api.get('sessions/manage?agent=' + encodeURIComponent(a.id)).catch(() => ({ sessions: [] }))));
        for (const r of rest) all.push(...(r.sessions || []));
        all.sort((a, b) => (b.last || 0) - (a.last || 0));
        state.data = { ...first, agents, sessions: all, stats: stats || {} };
        state.loading = false;
        render();
      } catch (e) {
        state.loading = false;
        clear(host);
        host.append(errorBox('会话读取失败：' + (e.message || e), load));
      }
    }

    async function loadUsage() {
      state.usageLoading = true;
      state.usageError = '';
      render();
      try {
        const qs = 'days=' + state.usageDays + (state.usageAgent ? '&agent=' + encodeURIComponent(state.usageAgent) : '');
        const [usage, pricing] = await Promise.all([
          api.get('sessions/usage?' + qs),
          api.get('pricing').catch(() => null),
        ]);
        state.usage = usage || {};
        if (pricing) {
          state.usagePricing = { currency: pricing.currency || 'USD', usd_cny: Number(pricing.usd_cny) || 7.2 };
        }
        state.usageLoading = false;
      } catch (e) {
        state.usageLoading = false;
        state.usageError = e.message || String(e);
      }
      render();
    }

    function render() {
      clear(host);
      host.append(viewSwitch());
      if (state.view === 'usage') {
        renderUsage();
        return;
      }
      renderList();
    }

    function viewSwitch() {
      const bar = h('div', { class: 'segbar', style: 'margin-bottom:10px' }, h('div', { class: 'segbar__tabs' }));
      for (const [id, label] of [['list', '会话列表'], ['usage', '会话用量']]) {
        bar.firstChild.append(h('button', {
          type: 'button', class: state.view === id ? 'on' : '',
          onclick: () => {
            if (state.view === id) return;
            state.view = id;
            if (id === 'usage' && !state.usage && !state.usageLoading) loadUsage();
            else render();
          },
        }, label));
      }
      return bar;
    }

    /* ---------- 会话列表 ---------- */

    function renderList() {
      const d = state.data || {};
      const all = d.sessions || [];
      const stats = d.stats || {};
      const known = new Map();
      for (const [id, name] of Object.entries(stats.agents || {})) known.set(id, { id, name, count: 0, last: 0 });
      for (const a of d.agents || []) {
        const x = known.get(a.id) || { id: a.id, name: a.name, count: 0, last: 0 };
        x.count = a.count || 0;
        x.last = a.last || 0;
        known.set(a.id, x);
      }
      const agents = [...known.values()];
      const activeEntries = (stats.days || []).reduce((n, day) => n + (day.active || []).length, 0);
      const q = state.q.trim().toLowerCase();
      const list = all.filter((s) => (!state.agent || s.agent === state.agent) && (!q || [s.title, s.name, s.cwd, s.agent].join(' ').toLowerCase().includes(q)));

      const kpis = h('div', { class: 'kpis' },
        kpi('会话总数', all.length, '本机可读取的会话文件'),
        kpi('客户端', agents.length, agents.map((a) => agentLabel(a.id)).join(' · ') || '—'),
        kpi('近 7 天活跃', activeEntries, '活跃记录 · 最近 7 天'),
        kpi('当前筛选', list.length, state.agent ? agentLabel(state.agent) : '全部客户端'));
      host.append(kpis);

      const c = card('会话列表', { chips: [chip(all.length + ' 个', 'plain')] });
      // 占满第二行：挤在标题右侧时，搜索一收缩就把刷新按钮顶到自己一行
      const bar = h('div', { class: 'inline', style: 'padding-bottom:10px;flex-wrap:wrap;gap:8px;flex:1 1 100%' });
      const search = h('label', { class: 'search', style: 'flex:1 1 260px' },
        h('input', {
          type: 'search', placeholder: '搜索标题、项目路径或客户端…', value: state.q, 'aria-label': '搜索会话',
          oninput: (e) => {
            state.q = e.target.value;
            const pos = e.target.selectionStart;
            render();
            const next = host.querySelector('.search input');
            if (next) { next.focus(); next.setSelectionRange(pos, pos); }
          },
        }));
      bar.append(search);
      bar.append(h('button', { class: 'tab' + (!state.agent ? ' on' : ''), type: 'button', onclick: () => { state.agent = null; render(); } }, '全部 ' + all.length));
      for (const a of agents) {
        bar.append(h('button', { class: 'tab' + (state.agent === a.id ? ' on' : ''), type: 'button', onclick: () => { state.agent = a.id; render(); } },
          agentLabel(a.id) + ' ' + (a.count || 0)));
      }
      bar.append(h('span', { class: 'grow' }));
      bar.append(iconBtn('refresh', '刷新', load));
      c.head.append(bar);

      const rows = h('div', { class: 'list sessions-list' });
      if (!all.length) {
        rows.append(empty('还没有可读取的会话', '本机 Claude Code / Codex CLI 的会话文件出现后，这里会自动显示。'));
      } else if (!list.length) {
        rows.append(empty('没有匹配的会话', '换一个关键词，或切换客户端筛选。'));
      }
      for (const s of list.slice(0, 300)) {
        const title = s.title || s.name || s.id;
        const agent = agentLabel(s.agent);
        rows.append(h('button', { class: 'row2', type: 'button', onclick: () => openSession(s) },
          h('span', { class: 't' }, title),
          h('span', { class: 'm' },
            chip(agent, 'plain'),
            s.wsl ? chip('WSL ' + s.wsl, 'plain') : null,
            h('span', { class: 'mono', title: s.cwd || '' }, s.cwd || '未记录项目路径')),
          h('span', { class: 'r' },
            h('span', null, fmtSize(s.size)),
            h('span', null, fmtDate(Math.round((s.last || 0) / 1000), false)))));
      }
      c.el.append(rows);
      host.append(c.el);
      host.append(h('div', { class: 'meta', style: 'padding:8px 2px' }, '会话内容只从本机客户端写入的本地文件读取，不修改原始记录；Gemini CLI 历史会话展示最近 90 天。'));
    }

    /* ---------- 会话用量 ---------- */

    function renderUsage() {
      const toolbar = usageToolbar();
      host.append(toolbar);
      if (state.usageError) {
        host.append(errorBox('会话用量读取失败：' + state.usageError, loadUsage));
        return;
      }
      if (state.usageLoading || !state.usage) {
        host.append(skeleton(6));
        return;
      }
      const u = state.usage;
      const inputTotal = (u.input || 0) + (u.cache_read || 0) + (u.cache_write || 0);
      const tokens = u.tokens || (inputTotal + (u.output || 0));
      const cacheRate = inputTotal ? ((u.cache_read || 0) / inputTotal) * 100 : 0;

      host.append(h('div', { class: 'kpis', style: 'margin-bottom:12px' },
        kpi('合计 Tokens', fmtBig(tokens), '输入 ' + fmtBig(inputTotal) + ' · 输出 ' + fmtBig(u.output || 0)),
        kpi('请求数', u.requests || 0, (u.sessions || 0) + ' 个会话 · 本窗口'),
        kpi('估算费用', money(u.cost || 0), u.unpriced ? '未计价 ' + u.unpriced + ' 次' : '按当前价格表'),
        kpi('输入 Tokens', fmtBig(inputTotal), '不含缓存 ' + fmtBig(u.input || 0)),
        kpi('缓存输入 Tokens', fmtBig(u.cache_read || 0), '命中率 ' + cacheRate.toFixed(1) + '%'),
        kpi('输出 Tokens', fmtBig(u.output || 0), '含推理输出')));

      host.append(usageChartCard(u));

      host.append(usageTable('按模型', ['模型', '输入 Tokens', '缓存 Tokens', '输出 Tokens', '合计 Tokens', '请求', '估算费用'],
        (u.models || []).map((m) => [
          m.name, fmtBig((m.input || 0) + (m.cache_read || 0) + (m.cache_write || 0)), fmtBig(m.cache_read || 0),
          fmtBig(m.output || 0), fmtBig(m.tokens || 0), String(m.requests || 0), money(m.cost || 0),
        ])));

      host.append(usageTable('按实例', ['实例', '会话', '输入 Tokens', '缓存 Tokens', '输出 Tokens', '合计 Tokens', '请求', '估算费用'],
        (u.agents || []).map((a) => [
          a.name, String(a.sessions || 0), fmtBig((a.input || 0) + (a.cache_read || 0) + (a.cache_write || 0)),
          fmtBig(a.cache_read || 0), fmtBig(a.output || 0), fmtBig(a.tokens || 0), String(a.requests || 0), money(a.cost || 0),
        ])));

      const dayRows = (u.series || []).slice().sort((a, b) => String(b.date).localeCompare(String(a.date))).map((d) => [
        d.date, fmtBig((d.input || 0) + (d.cache_read || 0) + (d.cache_write || 0)), fmtBig(d.cache_read || 0),
        fmtBig(d.output || 0), fmtBig(d.tokens || 0), String(d.requests || 0), money(d.cost || 0),
      ]);
      host.append(usageTable('按日期', ['日期', '输入 Tokens', '缓存 Tokens', '输出 Tokens', '合计 Tokens', '请求', '估算费用'], dayRows));

      host.append(h('div', { class: 'meta', style: 'padding:8px 2px' },
        '从本机各 Agent 的会话 JSONL 汇总，不依赖官方额度或网关请求日志；Codex 开启「统一会话历史」后，官方与第三方会话都会计入。费用按当前价格表估算。'));
    }

    function usageToolbar() {
      const wrap = h('div', { class: 'card', style: 'margin-bottom:12px' });
      const row = h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap' });
      row.append(h('span', { class: 'meta' }, '时间范围'));
      const period = h('div', { class: 'segbar', style: 'margin:0' }, h('div', { class: 'segbar__tabs' }));
      for (const [d, label] of [[1, '今日'], [7, '近 7 天'], [30, '近 30 天']]) {
        period.firstChild.append(h('button', {
          type: 'button', class: state.usageDays === d ? 'on' : '',
          onclick: () => { if (state.usageDays === d) return; state.usageDays = Number(d); loadUsage(); },
        }, label));
      }
      row.append(period);
      row.append(h('span', { class: 'meta', style: 'margin-left:8px' }, '实例'));
      const options = [['', '全部实例']];
      const seen = new Set(['']);
      for (const a of state.data?.agents || []) {
        if (!seen.has(a.id)) { options.push([a.id, a.name || agentLabel(a.id)]); seen.add(a.id); }
      }
      for (const id of ['codex', 'claude-code', 'gemini-cli']) {
        if (!seen.has(id)) { options.push([id, agentLabel(id)]); seen.add(id); }
      }
      row.append(select(options.map(([value, label]) => ({ value, label })), state.usageAgent, (v) => {
        state.usageAgent = v;
        loadUsage();
      }, { label: '实例' }));
      row.append(h('span', { class: 'grow' }));
      const u = state.usage || {};
      if (u.files != null) {
        row.append(h('span', { class: 'meta' },
          '已扫描 ' + u.files + ' 个会话文件 · 本窗口 ' + (u.sessions || 0) + ' 个会话 · ' + (u.requests || 0) + ' 次请求' +
          (u.last_sync ? ' · ' + new Date(u.last_sync * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' }) : '')));
      }
      row.append(iconBtn('refresh', '重新扫描', loadUsage));
      wrap.append(row);
      return wrap;
    }

    function usageChartCard(u) {
      const c = card('会话用量', { chips: [chip(fmtBig(u.tokens || 0) + ' Token', 'plain')] });
      c.head.append(h('span', { style: 'flex:1' }));
      for (const [id, label] of [['tokens', '合计 Tokens'], ['requests', '请求数']]) {
        c.head.append(btn(label, { class: 'sm' + (state.usageMetric === id ? ' on' : ''), onClick: () => { state.usageMetric = id; render(); } }));
      }
      const series = u.series || [];
      if (!series.length) {
        c.el.append(empty('还没有会话用量', '当前时间范围内没有可读的会话记录。'));
        return c.el;
      }
      const n = series.length;
      const values = series.map((d) => state.usageMetric === 'requests' ? (Number(d.requests) || 0) : (Number(d.tokens) || 0));
      const max = Math.max(1, ...values);
      const W = 760, H = 210, L = 58, R = 16, T = 20, B = 28;
      const x = (i) => L + ((W - L - R) * i) / Math.max(1, n - 1);
      const y = (v) => T + (H - T - B) * (1 - v / max);
      const pts = values.map((v, i) => [x(i), y(v)]);
      const line = pts.map((p, i) => (i ? 'L' : 'M') + p[0].toFixed(1) + ' ' + p[1].toFixed(1)).join(' ');
      const area = line + ' L ' + x(n - 1).toFixed(1) + ' ' + (H - B) + ' L ' + L + ' ' + (H - B) + ' Z';
      const fmtVal = (v) => state.usageMetric === 'requests' ? String(Math.round(v)) : fmtBig(v);
      const ticks = [0, 0.5, 1].map((k) => {
        const v = max * k;
        return '<line class="grid" x1="' + L + '" x2="' + (W - R) + '" y1="' + y(v).toFixed(1) + '" y2="' + y(v).toFixed(1) + '"/>'
          + '<text x="' + (L - 8) + '" y="' + (y(v) + 3).toFixed(1) + '" text-anchor="end">' + fmtVal(v) + '</text>';
      }).join('');
      const step = Math.max(1, Math.ceil(n / 7));
      const labels = series.map((d, i) => (i % step === 0 || i === n - 1)
        ? '<text x="' + x(i).toFixed(1) + '" y="' + (H - 8) + '" text-anchor="middle">' + String(d.date).slice(5).replace('-', '/') + '</text>' : '').join('');
      const dots = pts.map((p, i) => '<circle class="dot" cx="' + p[0].toFixed(1) + '" cy="' + p[1].toFixed(1) + '" r="2.6"><title>'
        + series[i].date + ' · ' + fmtVal(values[i]) + (state.usageMetric === 'requests' ? ' 次' : ' Token') + '</title></circle>').join('');
      const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
      svg.setAttribute('class', 'trend-svg');
      svg.setAttribute('viewBox', '0 0 ' + W + ' ' + H);
      svg.setAttribute('role', 'img');
      svg.setAttribute('aria-label', '会话用量趋势');
      svg.innerHTML = ticks + '<path class="area" d="' + area + '"/><path class="line" d="' + line + '"/>' + dots + labels;
      c.el.append(h('div', { class: 'trend-wrap' }, svg));
      return c.el;
    }

    function usageTable(title, headers, rows) {
      const c = card(title, { chips: [chip(rows.length + ' 行', 'plain')] });
      if (!rows.length) {
        c.el.append(empty('暂无数据', '当前时间范围内没有对应的会话用量。'));
        return c.el;
      }
      const thead = h('thead');
      const htr = h('tr');
      for (const head of headers) htr.append(h('th', null, head));
      thead.append(htr);
      const tbody = h('tbody');
      for (const row of rows) {
        const tr = h('tr');
        for (const cell of row) tr.append(h('td', null, cell));
        tbody.append(tr);
      }
      const table = h('table', { class: 'sess-table' }, thead, tbody);
      c.el.append(h('div', { class: 'sess-table-wrap' }, table));
      host.append(c.el);
      return c.el;
    }

    /* ---------- 单个会话 ---------- */

    async function openSession(s) {
      const body = h('div', { style: 'display:flex;flex-direction:column;gap:10px' });
      const d = drawer(s.title || s.name || s.id, (b) => { b.append(body); });
      const meta = [agentLabel(s.agent), s.cwd, s.last ? fmtDate(Math.round(s.last / 1000), true) : '', s.size ? fmtSize(s.size) : '', s.wsl ? 'WSL ' + s.wsl : ''].filter(Boolean);
      body.append(h('div', { class: 'inline', style: 'flex-wrap:wrap;gap:6px' }, ...meta.map((x) => chip(x, 'plain'))));
      const mdURL = '/api/sessions/markdown?agent=' + encodeURIComponent(s.agent) + '&id=' + encodeURIComponent(s.id);
      const actions = h('div', { class: 'inline', style: 'gap:6px;flex-wrap:wrap' },
        // 用 anchor 直接下载（服务端已带 Content-Disposition）：window.open 在桌面端
        // 会先开一个空白 WebView 窗口再触发下载，等于多弹一次窗。
        btn('下载 Markdown', { onClick: () => h('a', { href: mdURL }).click() }));
      if (s.resume) actions.append(btn('复制恢复命令', { onClick: () => navigator.clipboard.writeText(s.resume).then(() => toast('已复制'), () => toast('复制失败')) }));
      body.append(actions);
      const usageBox = h('div');
      body.append(usageBox);
      const parts = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
      parts.append(skeleton(4));
      body.append(parts);
      api.get('sessions/one?key=' + encodeURIComponent(s.agent + ':' + s.id))
        .then((one) => {
          const total = (one.input || 0) + (one.cache_read || 0) + (one.cache_write || 0) + (one.output || 0);
          if (!total && !one.cost) return;
          usageBox.append(h('div', { class: 'inset' },
            h('div', { class: 'inline', style: 'flex-wrap:wrap;gap:6px' },
              chip('合计 ' + fmtBig(total) + ' Token', 'plain'),
              chip('输入 ' + fmtBig((one.input || 0) + (one.cache_read || 0) + (one.cache_write || 0)), 'plain'),
              chip('缓存 ' + fmtBig(one.cache_read || 0), (one.cache_read || 0) ? 'green' : 'plain'),
              chip('输出 ' + fmtBig(one.output || 0), 'plain'),
              chip(one.unpriced ? '部分未计价' : '估算 ' + money(one.cost || 0), one.unpriced ? 'amber' : 'plain'),
              chip((one.requests || 0) + ' 次请求', 'plain')),
            one.models && one.models.length ? h('div', { class: 'meta', style: 'margin-top:6px' }, '模型：' + one.models.map((m) => m.model).join(' · ')) : null));
        })
        .catch(() => {});
      try {
        const r = await api.get('sessions/transcript?agent=' + encodeURIComponent(s.agent) + '&id=' + encodeURIComponent(s.id));
        clear(parts);
        const list = r.parts || [];
        if (!list.length) {
          parts.append(empty('这里没有可读的正文', r.source === 'gateway' ? '网关自己的会话没有落文件。' : '会话文件为空或格式不同。'));
          return;
        }
        for (const p of list.slice(0, 300)) {
          const text = p.text || p.content || p.body || '';
          if (!text) continue;
          const role = p.role || p.kind || '记录';
          parts.append(h('div', { class: 'inset' },
            h('div', { class: 'inline', style: 'margin-bottom:4px;gap:6px' }, chip(role, 'plain'), p.at ? h('span', { class: 'meta' }, fmtDate(p.at)) : null),
            h('div', { style: 'white-space:pre-wrap;overflow-wrap:anywhere;font-size:12.5px;line-height:1.6' }, String(text).slice(0, 6000))));
        }
      } catch (e) {
        clear(parts);
        parts.append(errorBox('读取会话失败：' + (e.message || e)));
      }
    }

    function kpi(k, v, d) {
      return h('section', { class: 'kpi' }, h('div', { class: 'k' }, k), h('div', { class: 'v' }, String(v)), h('div', { class: 'd' }, d));
    }
  },
};
