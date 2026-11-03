/* 请求日志：读取持久化账本，单请求详情含错误解释、耗时分解与 Token/计费。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { card, chip, btn, iconBtn, input, empty, errorBox, skeleton, toast, fmtDate, fmtTokens, fmtCost, drawer, copyText } from '../components.js';

const promptTokens = (e) => (e.input_tokens || 0) + (e.cache_read || 0) + (e.cache_write || 0);

const protocolLabel = (p) => {
  const raw = String(p || 'http');
  if (raw === 'openai_chat') return 'chat';
  if (raw === 'anthropic_messages') return 'anthropic';
  if (raw === 'openai_responses' || raw === 'responses') return 'responses';
  return raw.replace(/^openai_/, '').replace(/_messages$/, '');
};

// 输出速率：有首字节时只算首字节之后的解码时间；没有 TTFT 时退回总耗时。
const outputSpeed = (e) => {
  const out = Number(e.output_tokens) || 0;
  const decode = e.first_byte_ms > 0 && e.duration_ms > e.first_byte_ms
    ? e.duration_ms - e.first_byte_ms
    : e.duration_ms;
  return out > 0 && decode > 0 ? (out / decode) * 1000 : 0;
};
const RANGES = [['today', '今日'], ['7d', '近 7 天'], ['30d', '近 30 天']];
const PAGE_SIZE = 20;

// 账本里的 err_type 是传输层类型（rate/auth/shape/other），错误正文才是具体原因；
// 这里把两者合成用户能看懂的解释与下一步建议。
const ERRORS = {
  auth: { title: '渠道鉴权失败', why: '账号凭证过期、被撤销，或与当前渠道不匹配。', fix: '到「账号与额度」检查该账号登录态，重新导入或续期后再试。' },
  rate: { title: '上游限流 / 额度耗尽', why: '账号触发了上游的速率或额度限制，网关已把账号放入冷却。', fix: '等额度重置、换账号，或用虚拟路由组把请求分散到其他渠道。' },
  noAccount: { title: '没有可用账号', why: '该渠道的账号都在冷却、耗尽或失效状态。', fix: '刷新额度、重新登录，或接入其他渠道。' },
  noProvider: { title: '没有渠道提供该模型', why: '模型名没有匹配到任何已接入渠道的模型清单。', fix: '用 /v1/models 里的完整 ID，或在渠道详情勾选该模型。' },
  badRequest: { title: '请求被上游拒绝', why: '模型名、参数或账号类型不被上游接受（例如免费账号选了 Pro 模型）。', fix: '换用账号支持的同族模型，或使用另一个渠道。' },
  unsupported: { title: '渠道不支持该能力', why: '该渠道没有实现当前请求需要的协议或能力。', fix: '换用支持该协议的渠道，或调整客户端协议。' },
  privacy: { title: '隐私遮蔽不可用', why: '隐私遮蔽已开启但规则引擎不可用，网关拒绝发送。', fix: '到「设置 → 隐私遮蔽」修复规则，或临时关闭。' },
  internal: { title: '网关内部错误', why: '网关在组装或转发请求时出错。', fix: '查看 data/logs/aether.log，必要时重启网关。' },
  upstream: { title: '上游请求失败', why: '上游返回 5xx、连接中断或响应不符合协议。', fix: '网关会自动尝试其他渠道；持续失败时检查上游状态或账号额度。' },
  other: { title: '请求失败', why: '上游或网关返回了错误。', fix: '展开原始错误，结合渠道与账号状态排查。' },
};

function explainError(e) {
  const msg = String(e.err_msg || '').toLowerCase();
  const status = Number(e.http_status) || 0;
  if (/无可用账号|no_account|尚未添加任何账号|全部处于冷却/.test(msg)) return ERRORS.noAccount;
  if (/没有渠道提供|no_provider/.test(msg)) return ERRORS.noProvider;
  if (/隐私|privacy/.test(msg)) return ERRORS.privacy;
  if (/额度|quota|usage limit|free usage|insufficient|余额不足/.test(msg)) return ERRORS.rate;
  if (/capability|unsupported/.test(msg)) return ERRORS.unsupported;
  if (e.err_type === 'auth' || status === 401 || status === 403) return ERRORS.auth;
  if (e.err_type === 'rate' || status === 429) return ERRORS.rate;
  if (e.err_type === 'shape' || status === 400 || status === 422) return ERRORS.badRequest;
  if (status >= 500) return ERRORS.upstream;
  if (e.err_type === 'internal') return ERRORS.internal;
  return ERRORS.other;
}

function fromLedger(r) {
  const status = Number(r.status) || 0;
  return {
    raw: r,
    id: r.id || '',
    time: Math.floor((Number(r.t) || 0) / 1000),
    protocol: r.kind || 'http',
    model: r.model || r.req || '—',
    requested: r.req || '',
    provider_id: r.providerId || '',
    provider_name: r.providerName || '',
    account_id: r.accountId || '',
    account_name: r.accountName || '',
    agent: r.agentName || r.agent || '',
    session: r.session || '',
    stream: !!r.stream,
    http_status: status,
    status: status >= 400 ? 'error' : 'ok',
    err_type: r.err_type || '',
    err_msg: r.err || '',
    duration_ms: Number(r.ms) || 0,
    first_byte_ms: Number(r.ttft_ms) || 0,
    input_tokens: Number(r.in) || 0,
    output_tokens: Number(r.out) || 0,
    est_tokens: Number(r.est_tokens) || 0,
    // 上游实际处理的量远小于我们发出去的：上下文被上游静默截断，
    // 模型只看到末尾碎片，表现就是"突然失忆、开始复读"。
    upstream_truncated: !!r.upstream_truncated,
    cache_read: Number(r.cache_read) || 0,
    cache_write: Number(r.cache_write) || 0,
    reasoning_tokens: Number(r.reasoning) || 0,
    cost: Number(r.cost) || 0,
    priced: !!r.priced,
    pricing_model: r.pricing_model || '',
    tries: Number(r.tries) || 0,
    handed_on: !!r.handed_on,
    retried: !!r.retried,
    route_id: Number(r.route_id) || 0,
    fast_path: !!r.fast_path,
    items: Number(r.items) || 0,
    previous_id: !!r.previous_id,
    auto_continued: !!r.auto_continued,
    trimmed_items: Number(r.trimmed_items) || 0,
    access: r.access || '',
  };
}

export default {
  title: '请求日志',
  subtitle: '持久化账本 · 错误解释与耗时、Token 明细',

  mount(host) {
    const state = { range: '7d', rows: [], view: null, q: '', only: 'all', page: 0, timer: 0, live: false, pricing: { currency: 'USD', usd_cny: 7.2 } };
    host.append(skeleton(8));
    load();

    const rangeLabel = () => (RANGES.find(([id]) => id === state.range) || ['', ''])[1];

    async function load(silent) {
      if (!silent) { clear(host); host.append(skeleton(8)); }
      try {
        const [data, pricing] = await Promise.all([
          api.get('usage/requests?period=' + state.range + '&limit=500'),
          api.get('pricing').catch(() => ({ currency: 'USD', usd_cny: 7.2 })),
        ]);
        state.view = data || {};
        state.rows = (data?.rows || []).map(fromLedger);
        state.pricing = { currency: pricing.currency || 'USD', usd_cny: Number(pricing.usd_cny) || 7.2 };
        render();
      } catch (e) {
        clear(host);
        host.append(errorBox('日志读取失败：' + (e.message || e), load));
      }
    }

    function matches(e) {
      if (state.only === 'ok' && e.status !== 'ok') return false;
      if (state.only === 'error' && e.status === 'ok') return false;
      const q = state.q.trim().toLowerCase();
      if (!q) return true;
      return [e.model, e.requested, e.provider_id, e.provider_name, e.account_id, e.account_name,
        e.protocol, e.agent, e.session, e.err_type, e.err_msg, e.id, String(e.route_id)]
        .join(' ').toLowerCase().includes(q);
    }

    function render() {
      clear(host);
      const v = state.view || {};
      const rows = state.rows;
      const calls = Number(v.calls) || rows.length;
      const errors = Number(v.errors) || rows.filter((e) => e.status !== 'ok').length;
      const tokens = (Number(v.input) || 0) + (Number(v.output) || 0) + (Number(v.cache_read) || 0) + (Number(v.cache_write) || 0);
      const money = (n) => fmtCost(n, state.pricing.currency, state.pricing.usd_cny);

      host.append(h('div', { class: 'kpis logs-kpis' },
        kpi('请求数', calls, '已持久化 · ' + rangeLabel()),
        kpi('成功 / 失败', (calls - errors) + ' / ' + errors, calls ? '失败率 ' + ((errors / calls) * 100).toFixed(1) + '%' : '—'),
        kpi('平均延迟', (v.avg_ms || 0) + ' ms', 'P95 ' + (v.p95_ms || 0) + ' ms'),
        kpi('Token', fmtTokens(tokens), '缓存读 ' + fmtTokens(v.cache_read || 0)),
        kpi('费用', money(v.cost || 0), v.unpriced ? '未计价 ' + v.unpriced + ' 次' : '按当前价格表')));

      const countChip = chip('共 ' + rows.length + ' 条', 'plain');
      const c = card('请求明细', { chips: [countChip] });
      const rangeBar = h('div', { class: 'tabs' },
        ...RANGES.map(([id, label]) => h('button', {
          class: 'tab' + (state.range === id ? ' on' : ''), type: 'button',
          onclick: () => { if (state.range !== id) { state.range = id; state.page = 0; load(); } },
        }, label)));
      const search = input({ placeholder: '搜索模型 / 渠道 / 账号 / 错误 / 会话…', value: state.q, label: '搜索日志' });
      search.classList.add('log-search');
      search.addEventListener('input', () => { state.q = search.value; state.page = 0; paintList(); });
      const seg = h('div', { class: 'tabs' });
      for (const [id, label] of [['all', '全部'], ['ok', '成功'], ['error', '失败']]) {
        seg.append(h('button', { class: 'tab' + (state.only === id ? ' on' : ''), type: 'button', onclick: () => { state.only = id; state.page = 0; render(); } }, label));
      }
      const live = h('button', { class: 'tab' + (state.live ? ' on' : ''), type: 'button', title: '每 5 秒刷新' }, state.live ? '自动刷新 · 开' : '自动刷新 · 关');
      live.addEventListener('click', () => {
        state.live = !state.live;
        if (state.live) {
          state.timer = setInterval(() => {
            if (!host.isConnected) { clearInterval(state.timer); state.timer = 0; return; }
            load(true);
          }, 5000);
        } else if (state.timer) { clearInterval(state.timer); state.timer = 0; }
        live.className = 'tab' + (state.live ? ' on' : '');
        live.textContent = state.live ? '自动刷新 · 开' : '自动刷新 · 关';
        toast(state.live ? '每 5 秒刷新日志' : '已关闭自动刷新');
      });
      c.head.append(h('span', { style: 'flex:1' }), rangeBar, search, seg, live, iconBtn('refresh', '刷新', () => load(true)));
      const box = h('div', { class: 'list' });
      c.el.append(box);
      host.append(c.el);
      host.append(h('div', { class: 'api-note' }, '日志来自本机账本 data/ledger（按天 JSONL）；不保存请求/回复正文，但错误信息可能含上游返回内容，分享前请先检查并脱敏。'));
      paintList();
      function paintList() {
        clear(box);
        const list = rows.filter(matches);
        const pages = Math.max(1, Math.ceil(list.length / PAGE_SIZE));
        if (state.page >= pages) state.page = pages - 1;
        if (state.page < 0) state.page = 0;
        const start = state.page * PAGE_SIZE;
        const end = Math.min(start + PAGE_SIZE, list.length);
        countChip.textContent = '共 ' + list.length + ' 条';
        if (!list.length) {
          box.append(empty(rows.length ? '没有符合条件的请求' : '还没有请求', rows.length ? '换一个筛选。' : '接入客户端并发一次请求后，这里会出现审计记录。'));
          return;
        }
        box.append(h('div', { class: 'log-head' },
          h('span', null, '时间'),
          h('span', { class: 'log-proto' }, '协议'),
          h('span', null, '模型 · 渠道'),
          h('span', { class: 'num' }, '首字节 / 总耗时'),
          h('span', { class: 'num' }, '输入 / 输出'),
          h('span', { class: 'num' }, '输出速率'),
          h('span', { class: 'num' }, '费用'),
          h('span', { class: 'num' }, '状态')));
        for (const e of list.slice(start, end)) {
          const err = e.status !== 'ok';
          const inputTokens = promptTokens(e);
          const outTokens = Number(e.output_tokens) || 0;
          const speed = outputSpeed(e);
          const ex = err ? explainError(e) : null;
          const providerLine = [e.provider_name || e.provider_id, e.account_name || e.account_id, e.agent, e.fast_path ? '快速直通' : ''].filter(Boolean).join(' · ') || '—';
          const ttft = e.first_byte_ms ? e.first_byte_ms + ' / ' : '— / ';
          const tokenText = (inputTokens || outTokens) ? fmtTokens(inputTokens) + ' / ' + fmtTokens(outTokens) : '—';
          const costText = e.priced ? money(e.cost) : '未计价';
          const node = h('div', {
            class: 'log-row', role: 'button', tabindex: '0',
            onclick: () => openDetail(e),
            onkeydown: (ev) => { if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); openDetail(e); } },
          },
            h('span', { class: 'log-time mono' }, fmtDate(e.time, true, true).slice(5)),
            h('span', { class: 'log-proto', title: e.protocol || 'http' }, chip(protocolLabel(e.protocol), 'plain')),
            h('div', { class: 'log-model' },
              h('div', { class: 'log-title' }, h('span', { class: 'name' }, e.model || '—'), e.stream ? chip('流式', 'plain') : null, e.upstream_truncated ? chip('上游截断', 'amber') : null),
              h('div', { class: 'log-sub', title: providerLine }, providerLine),
              ex ? h('div', { class: 'log-err' }, ex.title + (e.err_type ? ' · ' + e.err_type : '')) : null),
            h('span', { class: 'log-num log-lat', 'data-label': '首字节 / 总耗时' }, ttft + e.duration_ms + ' ms'),
            h('span', { class: 'log-num log-tokens', 'data-label': '输入 / 输出', title: '输入 ' + fmtTokens(inputTokens) + ' · 输出 ' + fmtTokens(outTokens) }, tokenText),
            h('span', { class: 'log-num log-speed' + (speed ? '' : ' muted'), 'data-label': '输出速率' }, speed ? speed.toFixed(1) + ' tok/s' : '—'),
            h('span', { class: 'log-num log-cost' + (e.priced ? '' : ' muted'), 'data-label': '费用', title: '计价模型：' + (e.pricing_model || '—') }, costText),
            h('div', { class: 'log-status' },
              chip(err ? '失败 ' + (e.http_status || '') : '成功 ' + (e.http_status || ''), err ? 'red' : 'green'),
              iconBtn('external', '详情', (ev) => { ev.stopPropagation(); openDetail(e); })));
          box.append(node);
        }
        if (pages > 1) {
          const go = (n) => {
            if (n < 0 || n >= pages || n === state.page) return;
            state.page = n;
            paintList();
            box.scrollIntoView({ behavior: 'smooth', block: 'start' });
          };
          box.append(h('div', { class: 'log-pager', role: 'navigation', 'aria-label': '日志分页' },
            h('span', { class: 'meta' }, '显示 ' + (start + 1) + '–' + end + ' / 共 ' + list.length + ' 条'),
            h('span', { class: 'grow' }),
            btn('首页', { ghost: true, disabled: state.page === 0, onClick: () => go(0) }),
            btn('上一页', { ghost: true, disabled: state.page === 0, onClick: () => go(state.page - 1) }),
            h('span', { class: 'meta' }, '第 ' + (state.page + 1) + ' / ' + pages + ' 页'),
            btn('下一页', { ghost: true, disabled: state.page >= pages - 1, onClick: () => go(state.page + 1) }),
            btn('末页', { ghost: true, disabled: state.page >= pages - 1, onClick: () => go(pages - 1) })));
        }
      }
    }

    function openDetail(e) {
      const money = (n) => fmtCost(n, state.pricing.currency, state.pricing.usd_cny);
      const err = e.status !== 'ok';
      const inputTotal = promptTokens(e);
      const total = inputTotal + (e.output_tokens || 0);
      const ex = err ? explainError(e) : null;
      const tps = outputSpeed(e);
      const d = drawer('日志详情 · ' + (e.model || '请求'), (body) => {
        body.append(
          h('div', { class: 'inline' },
            chip(err ? '失败' : '成功', err ? 'red' : 'green'),
            chip('HTTP ' + (e.http_status || '—'), 'plain'),
            chip(e.protocol || 'http', 'plain'),
            e.stream ? chip('流式', 'plain') : chip('一次性', 'plain')),
        );
        if (ex) {
          body.append(h('div', { class: 'log-error' },
            h('div', { class: 'log-error-title' }, h('b', null, ex.title), chip(e.err_type || 'error', 'red')),
            h('div', { class: 'meta' }, ex.why),
            h('div', { class: 'meta' }, '建议：' + ex.fix),
            e.err_msg ? h('div', { class: 'log-error-raw mono' }, e.err_msg) : null));
        }
        body.append(
          section('请求', [
            row('请求 ID', e.id || '—'),
            row('时间', fmtDate(e.time, true, true)),
            row('模型', e.model || '—'),
            row('请求模型', e.requested && e.requested !== e.model ? e.requested : '（与模型同名）'),
            row('客户端', e.agent || '—'),
            row('会话', e.session || '—'),
          ]),
          section('路由与渠道', [
            row('渠道', [e.provider_name, e.provider_id].filter(Boolean).join(' · ') || '—'),
            row('账号', [e.account_name, e.account_id].filter(Boolean).join(' · ') || '—'),
            row('路由 ID', e.route_id ? '#' + e.route_id : '—'),
            row('尝试次数', (e.tries || 0) + ' 次' + (e.handed_on ? ' · 曾失败转移' : e.retried ? ' · 重试成功' : '')),
            row('上下文', '条目 ' + (e.items || 0) + (e.previous_id ? ' · 续接上一轮' : '') + (e.auto_continued ? ' · 自动续接（裁剪 ' + (e.trimmed_items || 0) + '）' : '')),
            row('路径', e.fast_path ? 'Fast-Path 零损直通' : 'IR 转换'),
          ]),
          section('耗时', [
            row('首字节', e.first_byte_ms ? e.first_byte_ms + ' ms' : '—'),
            row('总耗时', e.duration_ms + ' ms'),
            row('输出速度', tps ? tps.toFixed(1) + ' tok/s' : '—'),
          ]),
          section('Token 明细', [
            row('输入 Token', fmtTokens(e.input_tokens || 0)),
            row('缓存读取', fmtTokens(e.cache_read || 0)),
            row('缓存写入', fmtTokens(e.cache_write || 0)),
            row('输出 Token', fmtTokens(e.output_tokens || 0)),
            row('推理 Token', fmtTokens(e.reasoning_tokens || 0)),
            row('总 Token', fmtTokens(total)),
          ]),
          section('计费详情', [
            row('计价模型', e.pricing_model || '—'),
            row('计价状态', e.priced ? '按 Token' : '未匹配价格'),
            row('总费用', e.priced ? money(e.cost) : '—'),
          ]),
        );
        body.append(h('div', { class: 'inline', style: 'justify-content:flex-end;gap:8px' },
          btn('复制原始记录', { ghost: true, onClick: () => copyText(JSON.stringify(e.raw, null, 2)) }),
          btn('关闭', { onClick: () => d.close() })));
      });
    }

    function section(title, rows) {
      const box = h('section', { class: 'detail' }, h('div', { class: 'card-h' }, h('b', null, title)));
      const body = h('div', { class: 'detail-fields' });
      for (const r of rows) body.append(r);
      box.append(body);
      return box;
    }
    function row(label, value, tone) {
      return h('div', { class: 'detail-field', style: 'grid-template-columns:140px 1fr' },
        h('label', null, label),
        h('span', { class: 'mono', style: 'font-size:12px;color:' + (tone === 'red' ? 'var(--red)' : 'var(--text)') }, value));
    }

    function kpi(k, v, d) {
      return h('section', { class: 'kpi' }, h('div', { class: 'k' }, k), h('div', { class: 'v' }, String(v)), h('div', { class: 'd' }, d));
    }
  },
};
