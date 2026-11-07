/* 渠道与额度（自有实现）：账号卡 + 内嵌额度区 + 批量条 + 渠道开关抽屉 + 五段详情。 */
import { h, clear } from '../dom.js';
import { usedPct } from '../format.js';
import * as api from '../api.js';
import { icon } from '../icons.js';
import { chip, btn, iconBtn, card, meter, drawer, empty, errorBox, skeleton, toast, copyText, confirmDialog, promptDialog, input, select, field, toggle, menu, fmtTokens, fmtDate, fmtReset } from '../components.js';
import { openProvider } from './onboard.js';
import { openCustomChannels } from './customchannels.js';
import { channelNetFields } from './channelnet.js';
import { brandIcon } from '../brandicons.js';
import { accountRefreshMode } from '../capabilities.js';
import { openInBrowser } from '../links.js';

// 自动关闭的渠道：原因和恢复时间写在一枚 chip 里，免得用户对着灰掉的开关
// 猜"是我关的，还是它自己关的"。
const autoOffLabel = (p) => {
  const why = p.auto_off_reason === 'invalid' ? '登录失效' : '额度用尽';
  const when = p.auto_off_until ? ' · ' + fmtDate(p.auto_off_until, true) + ' 恢复' : ' · 需重新登录';
  return why + ' · 已自动关闭' + when;
};

const POOL = { 'Claude and GPT models': 'Claude', 'Gemini Models': 'Gemini' };
const isTest = (id) => /^mock/.test(id) || id === 'openai-compat';

export default {
  title: '账号与额度',
  subtitle: '账号、模型与余量，一眼看到底',

  mount(host, ctx) {
    const state = { accounts: [], providers: [], usage: {}, settings: {}, presets: null, picked: new Set(), timers: [] };
    host.append(skeleton(8));
    load();

    async function load() {
      try {
        const [a, p, u, s, pr] = await Promise.all([
          api.get('accounts').catch(() => ({ accounts: [] })),
          api.get('providers').catch(() => ({ providers: [] })),
          api.get('usage?days=1').catch(() => ({ accounts: [] })),
          api.get('settings').catch(() => ({})),
          api.get('presets').catch(() => ({ presets: [], provider_presets: {}, route_presets: {} })),
        ]);
        state.accounts = a.accounts || [];
        state.providers = p.providers || p || [];
        state.usage = u || {};
        state.settings = s || {};
        state.presets = pr || { presets: [], provider_presets: {}, route_presets: {} };
        // 官转率（按响应 id 前缀推定）单独取，失败不影响渠道页。
        state.routing = await api.get('routing-stats').catch(() => ({ providers: {} }));
        // WorkBuddy 出网身份：账号卡片要展示的摘要。必须在这里预取——
        // acctCard 是同步渲染函数，里面不能 await（否则整个模块语法错误、页面回退到概览）。
        if (state.accounts.some((x) => String(x.provider_id || '').startsWith('workbuddy'))) {
          state.wbIdentity = await api.get('providers/workbuddy/identity').catch(() => null);
        }
        render();
        try {
          if (sessionStorage.getItem('aether.addProvider') === '1') { sessionStorage.removeItem('aether.addProvider'); customChannelsDrawer(); }
        } catch {}
        window.__aetherAddProvider = customChannelsDrawer;
        // 首帧的额度窗口可能还在后台读，补一次
        if (!state.late) {
          state.late = true;
          setTimeout(() => { if (host.isConnected && state.accounts.some((a) => a.quota_supported !== false && !((a.quota && a.quota.windows) || []).length)) load(); }, 3000);
        }
      } catch (e) {
        clear(host);
        host.append(errorBox('账号读取失败：' + (e.message || e), load));
      }
    }

    const providerOf = (id) => state.providers.find((p) => p.id === id);
    const usageOf = (id) => (state.usage.accounts || []).find((x) => x.id === id);
    const isActive = (pid) => (providerOf(pid)?.agents || []).some((a) => a.current);
    // 官转率统计：{ providers: { <provider_id>: {total, official, relay, unknown, share} } }
    const routingOf = (pid) => ((state.routing && state.routing.providers) || {})[pid];
    // 官转率只统计 DeepSeek V4.1 Flash：上游 id 是 deepseek-v4.1-flash，
    // 界面显示名可能写成 DeepSeek-Flash / DeepSeek V4.1 Flash，两种都认。
    function isFlashModel(p, upstream) {
      const norm = (s) => String(s || '').toLowerCase().replace(/[\s_]+/g, '-');
      const id = norm(upstream);
      if (id === 'deepseek-v4.1-flash' || id === 'deepseek-flash') return true;
      if (id.endsWith('/deepseek-v4.1-flash') || id.endsWith('/deepseek-flash')) return true;
      const label = norm(modelLabel(p, upstream)).replace(/-/g, '');
      return label === 'deepseekflash' || label === 'deepseekv41flash';
    }
    // 把上游模型名（deepseek-v4.1-flash）映射成渠道里配置的显示名
    // （DeepSeek V4.1 Flash）；找不到就原样返回，保证徽章永远有字可显示。
    function modelLabel(p, upstream) {
      const list = (p && p.models) || [];
      for (const m of list) {
        const id = String(m.id || '');
        const up = String(m.upstream_model || m.upstream || '');
        if (up === upstream || id === upstream ||
            id === (p.id + '/' + upstream) || id.endsWith('/' + upstream)) {
          return m.name || m.display_name || upstream;
        }
      }
      return upstream;
    }

    function remaining(w) { return 100 - usedPct(w); }
    // 可用积分：优先 total/balance 原始数值；只有窗口时按 limit×(1-used%) 折算。
    function creditsAvailable(q) {
      if (!q) return '—';
      const num = (v) => (v === null || v === undefined || v === '' || Number.isNaN(Number(v)) ? null : Number(v));
      const balance = num(q.balance);
      const remaining = num(q.remaining);
      const total = num(q.total);
      const text = (v, unit) => v.toLocaleString(undefined, { maximumFractionDigits: 2 }) + (unit ? ' ' + unit : '');
      // 余额/剩余才是“可用”；total 只在没有剩余字段时兜底，避免把总量当可用量。
      if (balance !== null && balance > 0) return text(balance, q.balance_unit || q.unit);
      if (remaining !== null) return text(remaining, q.unit || q.balance_unit);
      if (balance !== null) return text(balance, q.balance_unit || q.unit);
      if (total !== null && total > 0) return total.toLocaleString() + (q.unit ? ' ' + q.unit : '');
      const wins = (q.windows || []).filter((w) => /credit/i.test(String(w.unit || '')) && Number(w.limit) > 0);
      if (wins.length) {
        const remain = wins.reduce((s, w) => s + Number(w.limit) * (1 - usedPct(w) / 100), 0);
        return Math.round(remain).toLocaleString() + ' ' + (wins[0].unit || 'credits');
      }
      return '—';
    }
    function tone(rem) { return rem <= 10 ? 'red' : rem <= 40 ? 'amber' : ''; }
    function pools(acct) {
      const ws = (acct.quota && acct.quota.windows) || [];
      const rank = (k) => ({ 'Claude and GPT models': 0, 'Gemini Models': 1 }[k] ?? (k === 'Chat' ? 2 : k === 'Tab' ? 3 : 4));
      const map = new Map();
      for (const w of ws) {
        if (w.family && w.pool) continue;
        const key = w.pool || w.family || '';
        if (!map.has(key)) map.set(key, []);
        map.get(key).push(w);
      }
      return [...map.entries()].sort((x, y) => rank(x[0]) - rank(y[0])).map(([k, wins]) => ({ name: POOL[k] || k || '剩余额度', wins }));
    }

    function filtered() {
      const { filter } = ctx.store.get();
      let list = state.accounts.slice();
      if (filter.seg === 'real') list = list.filter((a) => !isTest(a.provider_id));
      if (filter.seg === 'wsl') list = [];
      if (filter.tag) list = list.filter((a) => (a.tags || []).includes(filter.tag));
      if (filter.more.quota) list = list.filter((a) => ((a.quota && a.quota.windows) || []).length > 0);
      if (filter.more.calls) list = list.filter((a) => (usageOf(a.account_id)?.calls || 0) > 0);
      if (filter.more.off) list = list.filter((a) => providerOf(a.provider_id)?.off);
      const q = filter.query.trim().toLowerCase();
      if (q) list = list.filter((a) => {
        const p = providerOf(a.provider_id);
        return [a.label, a.account_id, a.provider_id, p?.name, ...(a.tags || []), ...((p && p.models) || []).map((m) => m.id)].join(' ').toLowerCase().includes(q);
      });
      const score = (a) => {
        const ws = (a.quota && a.quota.windows) || [];
        const pool = ws.filter((w) => !w.family);
        return pool.length ? Math.max(...pool.map((w) => Number(w.used) || 0)) : 0;
      };
      if (filter.sortQuota) list.sort((a, b) => score(b) - score(a));
      else list.sort((a, b) => Number(b.state === 'ready') - Number(a.state === 'ready') || String(a.label).localeCompare(String(b.label)));
      return list;
    }

    /* ---------- 渲染 ---------- */
    function render() {
      clear(host);
      const { filter } = ctx.store.get();
      host.append(batchBar(), gridView(filter));
      syncPicked();
    }

    function batchBar() {
      const box = h('input', { type: 'checkbox', 'aria-label': '全选', onchange: (e) => {
        const list = filtered();
        if (e.target.checked) list.forEach((a) => state.picked.add(a.account_id));
        else list.forEach((a) => state.picked.delete(a.account_id));
        render();
      } });
      box.id = 'acctAll';
      const count = chip(state.accounts.length + ' 个账号', 'plain');
      const groups = h('div', { class: 'inline' });
      const tags = [...new Set(state.accounts.flatMap((a) => a.tags || []))].sort((a, b) => a.localeCompare(b));
      const addTag = (label, tag, n) => h('button', { class: 'chip ' + (ctx.store.get().filter.tag === tag ? '' : 'plain'), type: 'button', style: 'border:0;cursor:pointer', onclick: () => ctx.store.set({ tag }) }, label + ' ' + n);
      groups.append(addTag('全部', null, state.accounts.length));
      for (const t of tags) groups.append(addTag(t, t, state.accounts.filter((a) => (a.tags || []).includes(t)).length));
      const plus = iconBtn('plus', '给已选账号加分组标签', async () => {
        if (!state.picked.size) { toast('先勾选账号，再加分组'); return; }
        const v = await promptDialog('新分组标签', { desc: '会加到已选的 ' + state.picked.size + ' 个账号', placeholder: '例如：备用', confirmText: '添加' });
        if (!v) return;
        const ids = [...state.picked];
        (async () => {
          for (const id of ids) {
            const a = state.accounts.find((x) => x.account_id === id);
            if (!a) continue;
            const tags2 = [...new Set([...(a.tags || []), v.trim()])].filter(Boolean);
            await api.post('accounts/' + encodeURIComponent(id) + '/tags', { tags: tags2 }).catch(() => {});
          }
          toast('分组已更新');
          await load();
        })();
      });
      const del = iconBtn('trash', '批量删除', async () => {
        const ids = [...state.picked];
        if (!ids.length) return;
        if (!(await confirmDialog('删除选中的 ' + ids.length + ' 个账号？', '只移除本机记录，渠道本身不动。', { danger: true, confirmText: '删除' }))) return;
        try {
          const out = await api.post('accounts/batch', { action: 'delete', ids });
          ids.forEach((id) => state.picked.delete(id));
          toast('已删除 ' + (out.ok ?? ids.length) + ' / ' + (out.total ?? ids.length));
          await load();
        } catch (e) { toast('删除失败：' + (e.message || e)); }
      }, { danger: true });
      const bulk = iconBtn('settings', '批量设置已选账号', () => batchSettings());
      const bar = h('div', { class: 'batchbar' }, h('label', { class: 'inline', style: 'font-size:12px;color:var(--muted)' }, box, h('span', null, '全选')), count, h('span', { id: 'pickedLabel', class: 'meta' }), groups, plus, bulk, h('span', { class: 'grow' }), btn('接入渠道', { icon: 'plus', onClick: () => ctx.navigate('onboard') }), btn('＋ 自定义渠道', { icon: 'plugin', onClick: customChannelsDrawer }), btn('渠道开关', { icon: 'database', onClick: supplierDrawer }), del);
      return bar;
    }

    function gridView(filter) {
      const list = filtered();
      if (!list.length) return empty(state.accounts.length ? '没有符合筛选条件的账号' : '还没有账号', state.accounts.length ? '调整搜索或筛选条件。' : '点上方「接入渠道」接入内置渠道，或用「＋ 自定义渠道」注册一个第三方中转站。');
      const grid = h('div', { class: 'acct-grid', style: filter.layout === 'list' ? 'grid-template-columns:1fr' : '' });
      for (const a of list) grid.append(acctCard(a));
      // 账号少于 3 个时补一张虚线卡：auto-fill 会留出半格到一整格的死区，顺手把「接入」放在手边
      if (filter.layout !== 'list' && list.length < 3) {
        grid.append(h('button', {
          class: 'acct-add', type: 'button', onclick: () => ctx.navigate('onboard'),
        }, icon('plus', 20), h('span', null, '接入新渠道')));
        // 紧挨着「接入新渠道」：自定义渠道同样是"加一个渠道"，只是配置来自用户而不是内置清单。
        // 注意这只是空态补充，顶部批量条里的「＋ 自定义渠道」是常驻入口（不依赖账号数量）。
        grid.append(h('button', {
          class: 'acct-add', type: 'button', onclick: customChannelsDrawer,
        }, icon('plugin', 20), h('span', null, '自定义渠道')));
      }
      return grid;
    }

    function syncPicked() {
      const all = document.getElementById('acctAll');
      const list = filtered();
      if (all) {
        const input = /** @type {HTMLInputElement} */ (all);
        const sel = list.filter((a) => state.picked.has(a.account_id)).length;
        input.checked = list.length > 0 && sel === list.length;
        input.indeterminate = sel > 0 && sel < list.length;
      }
      const lab = document.getElementById('pickedLabel');
      if (lab) lab.textContent = state.picked.size ? '已选 ' + state.picked.size : '';
    }

    /* ---------- 账号卡 ---------- */
    function acctCard(a) {
      const p = providerOf(a.provider_id);
      const refreshMode = accountRefreshMode(p?.capabilities, !!p?.accountBalances?.[a.account_id]);
      const u = usageOf(a.account_id);
      const el = h('article', { class: 'acct-card' + (state.picked.has(a.account_id) ? ' is-picked' : '') });
      const head = h('div', { class: 'acct-head' });
      const pick = h('input', { type: 'checkbox', 'aria-label': '选择 ' + a.label, checked: state.picked.has(a.account_id), onchange: (e) => {
        if (e.target.checked) state.picked.add(a.account_id); else state.picked.delete(a.account_id);
        el.classList.toggle('is-picked', e.target.checked);
        syncPicked();
      } });
      const headParts = [pick];
      const bi = brandIcon(a.provider_id, 16);
      if (bi) headParts.push(bi);
      headParts.push(h('span', { class: 'name', role: 'heading', 'aria-level': '3' }, a.label));
      head.append(...headParts);
      if (isActive(a.provider_id)) head.append(chip('当前', 'green'));
      if (a.disabled) head.append(chip('已停用', 'plain'));
      if (a.duplicate_of) head.append(h('span', { title: '与「' + a.duplicate_of + '」凭证相同' }, chip('重复', 'amber')));
      for (const t of a.tags || []) head.append(chip(t, 'plain'));
      // 官转率：按响应 id 前缀推定的后端族群占比（点开有口径说明）。
      // 样本太少不显示，避免 1 次请求就宣布"100% 官方"。
      const rs = routingOf(a.provider_id);
      if (rs) {
        // 官转率是**按模型**统计的：同一渠道里 flash 和 pro 会落在完全不同的
        // 后端，渠道级平均会变成没意义的数字。优先展示样本最多的那个模型，
        // 徽章上带模型名，避免误读成整个渠道的比例。
        const models = rs.models || {};
        const rows = Object.keys(models)
          .map((m) => ({ model: m, ...models[m] }))
          // 只展示 DeepSeek V4.1 Flash 的官转率：它和 pro 落在不同后端，
          // 混在一起看没有意义；用户只关心这个模型到底走没走官方。
          .filter((x) => isFlashModel(p, x.model))
          .filter((x) => x.total >= 10)
          .sort((x, y) => y.total - x.total);
        const top = rows[0];
        if (top) {
          const pct = Math.round((top.share || 0) * 100);
          const label = modelLabel(p, top.model);
          const detail = rows.map((x) => modelLabel(p, x.model) + ' ' +
            Math.round((x.share || 0) * 100) + '%（官方 ' + x.official + ' · 中转 ' + x.relay +
            ' · 未知 ' + x.unknown + '）').join('\n');
          head.append(h('span', {
            title: '官转率（推定，按模型统计）\n' + detail +
              '\n口径：按上游响应 id 前缀推定，chatcmpl-/纯数字=官方，gw-=中转；仅作趋势参考',
          }, chip(label + ' 官转 ' + pct + '%', pct >= 90 ? 'green' : pct >= 60 ? 'amber' : 'plain')));
        }
        // 只统计 DeepSeek V4.1 Flash：其它模型或只有渠道级老数据时一律不显示，
        // 避免用一个混合口径的百分比误导判断。
      }
      head.append(h('span', { class: 'grow' }));
      head.append(btn(a.note ? '备注' : '加备注', { ghost: true, class: 'sm acct-note-btn', icon: 'edit', title: a.note || '给这个账号加一条备注', onClick: () => noteDialog(a) }));
      el.append(head);
      if (a.note) el.append(h('div', { class: 'acct-note' }, a.note));

      const inset = h('div', { class: 'acct-inset' });
      const groups = pools(a);
      const creditsText = creditsAvailable(a.quota);
      const balanceOnly = !groups.length && creditsText !== '—';
      if (groups.length) {
        const cols = h('div', { class: 'acct-cols' });
        for (const g of groups) {
          const col = h('div', { class: 'acct-col' }, h('div', { class: 'acct-pool' }, g.name));
          for (const w of g.wins) {
            const rem = remaining(w);
            // 剩余 0% 时上游会直接拒绝请求（Qoder 的主额度、WorkBuddy 的 credits 都是
            // 这个行为），所以必须显式标出"已用尽"，不能只给一个 0% 让人猜。
            const exhausted = rem <= 0.5;
            col.append(h('div', { class: 'acct-win' },
              h('div', { class: 'row-h' }, h('span', { class: 'lbl' }, w.name),
                exhausted ? h('span', { class: 'chip red', title: '该额度已用完：上游会直接拒绝请求（不一定支持只用加量包）', style: 'margin-left:6px;font-size:11px' }, '已用尽') : null,
                h('span', { class: 'pct', title: '剩余 ' + (Math.round(rem * 10) / 10) + '%', style: 'color:var(--' + (rem <= 10 ? 'red' : rem <= 40 ? 'amber' : 'green') + ')' }, Math.round(rem) + '%')),
              meter(rem, tone(rem)),
              w.resets_at ? h('div', { class: 'reset' }, fmtReset(new Date(w.resets_at * 1000).toISOString()) + ' 重置') : null));
          }
          cols.append(col);
        }
        inset.append(cols);
      } else if (balanceOnly) {
        // 积分制渠道（Trae 等）只有余额、没有重置窗口，单独画一行余额，
        // 不能落到"没有可读的额度窗口"的空白提示里。
        const readAt = a.quota && a.quota.read_at ? fmtDate(a.quota.read_at, true, true) : '';
        const balZero = /^0(\.0+)?\s/.test(creditsText) || creditsText === '0';
        inset.append(h('div', { class: 'acct-win' },
          h('div', { class: 'row-h' }, h('span', { class: 'lbl' }, '积分余额'),
            balZero ? h('span', { class: 'chip red', title: '余额为 0：上游会直接拒绝请求', style: 'margin-left:6px;font-size:11px' }, '已用尽') : null,
            h('span', { class: 'pct', style: 'color:var(--' + (balZero ? 'red' : 'green') + ')' }, creditsText)),
          h('div', { class: 'reset' }, '积分制渠道 · 没有重置窗口' + (readAt ? ' · 读于 ' + readAt : ''))));
      } else {
        // 没有额度接口的渠道（额度只在厂商控制台里）不能显示成"读取失败"：
        // 那会让人以为是我们的 bug，实际是根本不存在的接口。直接给出控制台入口。
        const unsupported = a.quota_supported === false;
        const box = h('div', { class: 'acct-empty' });
        if (unsupported) {
          box.append('该渠道没有额度接口，额度在厂商控制台');
          if (p && p.keys_url) {
            box.append(' ', h('a', {
              href: p.keys_url,
              rel: 'noopener',
              onclick: (e) => { e.preventDefault(); openInBrowser(p.keys_url); },
            }, '去查看'));
          }
        } else {
          box.append(a.quota_error
            ? ('额度读取失败：' + a.quota_error)
            : ((a.quota && a.quota.message) || '这个账号没有可读的额度窗口。'));
        }
        inset.append(box);
      }
      // 只有真的读到了积分才占一行；「可用 AI 积分 —」在 6 张卡上重复 6 次，是纯噪音
      if (!balanceOnly && creditsText !== '—') {
        inset.append(h('div', { class: 'acct-credits' }, h('span', null, '可用 AI 积分'), h('span', { class: 'v' }, creditsText)));
      }
      if (a.checkin_supported || p?.capabilities?.check_in) inset.append(checkinRow(a));
      el.append(inset);

      const metaBits = [];
      if (a.priority) metaBits.push('优先级 ' + a.priority);
      if ((a.weight || 1) !== 1) metaBits.push('权重 ' + a.weight);
      if (a.concurrency != null) metaBits.push('并发 ' + a.concurrency);
      
      // WorkBuddy 出网身份摘要（仅 workbuddy 系渠道）。
      // 数据来自 load() 的预取：这里必须同步，不能 await。
      let wbIdentitySummary = null;
      if (p && String(p.id).startsWith('workbuddy') && state.wbIdentity) {
        const st = state.wbIdentity;
        const titleMap = { cli: '官方 CLI', desktop: '桌面端', quiet: '安静', browser: '浏览器' };
        const displayName = titleMap[st.identity] || st.identity || '未知';
        const latency = st.pace_ms ? st.pace_ms + 'ms' : '—';
        wbIdentitySummary = h('div', {
          class: 'wb-identity-summary clickable',
          style: 'color:var(--primary);cursor:pointer;font-size:0.85em;margin-top:4px;',
          title: '点开设置里的 WorkBuddy 出网身份',
          onclick: () => ctx.navigate('settings', { tab: 'network' }),
        }, ['出网身份 · ', displayName, ' · ', latency]);
      }
      
      // 光秃秃一个日期没人知道是什么：补上语义，并去掉时分（入库看日期就够，完整时间留给 title）
      const foot = h('div', { class: 'acct-foot' },
        h('span', { title: a.created_at ? '账号入库时间 ' + fmtDate(a.created_at) : '没有入库时间记录' },
          a.created_at ? '入库 ' + fmtDate(a.created_at, false) : fmtDate(a.created_at)),
        metaBits.length ? h('span', { class: 'meta', style: 'margin-left:8px' }, metaBits.join(' · ')) : null);
      if (wbIdentitySummary) foot.append(wbIdentitySummary);
      foot.append(h('span', { class: 'grow' }));
      const acts = h('span', { class: 'acct-acts' });
      const moreBtn = iconBtn('more', '更多操作', () => menu(moreBtn, [
        { label: '详情', run: () => openDetail(a) },
        { label: '设置：优先级 / 权重 / 并发', run: () => accountSettings(a) },
        { label: '预设', run: () => providerPresetDialog(p) },
        { label: '标签', run: () => tagDialog(a) },
        { label: '复制配置', run: () => copyText(JSON.stringify({ base_url: location.origin + '/v1', provider: a.provider_id, model: (p?.models || []).find((m) => m.on)?.id || '', account: a.label }, null, 2)) },
        { label: '测试连通性', run: () => testModal(a) },
        ...((p?.capabilities?.local_inject) ? [{ label: '写入本机 IDE（一键切号）', run: () => injectToLocal(a) }] : []),
        { label: '导出…', run: () => exportDialog() },
        { sep: true },
        { label: '删除账号', danger: true, run: async () => {
          if (!(await confirmDialog('删除账号「' + a.label + '」？', '只移除本机记录，渠道本身不动。', { danger: true, confirmText: '删除' }))) return;
          try { await api.del('accounts/' + encodeURIComponent(a.account_id)); state.picked.delete(a.account_id); toast('已删除'); await load(); }
          catch (err) { toast('删除失败：' + (err.message || err)); }
        } },
      ]));
      const channelOn = !p?.off;
      const channelSwitch = h('button', {
        class: 'toggle' + (channelOn ? ' on' : ''),
        type: 'button', role: 'switch', 'aria-checked': channelOn ? 'true' : 'false',
        title: channelOn ? '渠道已开启，点击关闭' : '渠道已关闭，点击开启',
        style: 'background:' + (channelOn ? 'var(--green-fill)' : 'var(--red-fill)') + ';border-color:' + (channelOn ? 'var(--green-fill)' : 'var(--red-fill)'),
        onclick: async () => {
          try {
            await api.post('provider/' + (channelOn ? 'off' : 'on'), { id: p?.id || a.provider_id });
            toast(channelOn ? '渠道已关闭' : '渠道已开启');
            await load();
          } catch (e) { toast('切换失败：' + (e.message || e)); }
        },
      }, h('i'));
      acts.append(...[
        channelSwitch,
        refreshMode ? iconBtn('refresh', refreshMode === 'quota' ? '刷新额度' : '刷新凭据', async (e) => {
          // e.currentTarget 过了 await 就是 null，必须先抓住
          const el = e.currentTarget;
          el.disabled = true;
          try {
            const r = await api.post('accounts/' + encodeURIComponent(a.account_id) + '/refresh', {});
            await load();
            if (r && r.quota_supported === false) {
              if (r.refreshed) toast('凭据已刷新');
              else if (r.warning) toast('刷新失败：' + r.warning);
              else toast('没有可刷新的凭据');
            } else if (r && r.quota_error) toast('额度刷新失败：' + r.quota_error);
            else if (r && r.warning) toast('额度已刷新（' + r.warning + '）');
            else toast('额度已刷新');
          } catch (err) { toast('刷新失败：' + (err.message || err)); }
          if (el.isConnected) el.disabled = false;
        }) : null,
        (p?.capabilities?.chat) ? iconBtn('bolt', '立即唤醒（发一个最小请求，提前触发额度重置）', (e) => wakeOne(a, e.currentTarget)) : null,
        moreBtn,
      ].filter(Boolean)); // 原生 append 会把 null 序列化成字面量 "null"，先过滤
      foot.append(acts);
      el.append(foot);
      return el;
    }

    /* ---------- 每日签到（积分渠道） ---------- */

    function checkinOf(a) {
      if (a.checkin) return a.checkin;
      const r = (a.checkin_today || {}).daily_checkin;
      if (!r) return null;
      return {
        outcome: r.outcome || (r.success ? 'claimed' : 'failed'),
        credit: r.credit || 0, streak: r.streak || 0,
        msg: r.message || '', again: false,
      };
    }

    function checkinRow(a) {
      const ci = checkinOf(a);
      const outcome = ci?.outcome || '';
      const provisionalDone = outcome === 'done' && ci?.again;
      const settled = outcome === 'claimed' || (outcome === 'done' && !ci?.again);
      let status;
      if (!outcome) status = chip('今日未签到', 'plain');
      else if (outcome === 'claimed') status = chip('已签到', 'green');
      else if (outcome === 'done') status = chip('今天已签', 'green');
      else if (outcome === 'own-app') status = chip('需在官方 App 签到', 'amber');
      else if (outcome === 'inactive') status = chip('今天没有活动', 'plain');
      else if (outcome === 'ineligible') status = chip('今天不可签', 'amber');
      else status = chip('签到失败', 'red');
      const credit = Number(ci?.credit) || 0;
      const bt = btn(settled ? '已签到' : (provisionalDone ? '再确认' : '立即签到'), {
        primary: !settled, class: 'sm', disabled: settled || a.disabled,
        onClick: (e) => doCheckin(a, e.currentTarget),
      });
      return h('div', { class: 'acct-checkin' },
        h('span', { class: 'meta' }, '每日签到'),
        status,
        credit ? h('span', { class: 'meta' }, '+' + credit.toLocaleString() + ' 积分') : null,
        ci?.streak ? h('span', { class: 'meta' }, '连续 ' + ci.streak + ' 天') : null,
        ci?.msg ? h('span', { class: 'meta acct-checkin-msg', title: ci.msg }, ci.msg) : null,
        h('span', { class: 'grow' }),
        bt);
    }

    async function doCheckin(a, el) {
      if (el) { el.disabled = true; el.textContent = '签到中…'; }
      try {
        const out = await api.post('accounts/' + encodeURIComponent(a.account_id) + '/checkin', {});
        const c = out.checkin || checkinOf(a) || {};
        if (out.already) toast('今天已经签到过了');
        else if (c.outcome === 'claimed' || c.outcome === 'done') {
          toast('签到成功' + (c.credit ? '，+' + Number(c.credit).toLocaleString() + ' 积分' : ''));
        } else if (c.outcome === 'own-app') toast('需要在官方 App 内签到');
        else if (c.outcome === 'inactive') toast('今天没有签到活动');
        else if (c.outcome === 'ineligible') toast('这个账号今天不可签到');
        else toast('签到失败' + (c.msg ? '：' + c.msg : ''));
        await load();
      } catch (e) {
        toast('签到失败：' + (e.message || e));
        await load();
      } finally {
        if (el && el.isConnected) { el.disabled = false; el.textContent = '立即签到'; }
      }
    }

    // --- 唤醒任务：账号级定时最小请求，提前触发上游额度重置周期 ---

    async function wakeOne(a, el) {
      if (el) el.disabled = true;
      try {
        const out = await api.post('wake/run', { account_id: a.account_id });
        if (out && out.ok) toast('已唤醒 · ' + (out.model || a.provider_id) + ' · ' + (out.ms || 0) + ' ms');
        else toast('唤醒失败：' + ((out && out.result) || '未知原因'));
      } catch (err) { toast('唤醒失败：' + (err.message || err)); }
      if (el) el.disabled = false;
    }

    function injectToLocal(a) {
      confirmDialog('写入本机 IDE？', '把账号「' + a.label + '」的登录态写入本机 Antigravity（系统凭据 gemini:antigravity + jetski 回退文件）。会覆盖本机当前登录；写入后需重启 IDE 生效。', { confirmText: '写入' }).then((ok) => {
        if (!ok) return;
        api.post('accounts/' + encodeURIComponent(a.account_id) + '/inject', {}).then((res) => {
          const r = res.result || {};
          const warn = (r.warnings || []).length ? '（' + r.warnings.join('；') + '）' : '';
          toast('已写入 ' + (r.target || '本机 IDE') + warn);
        }).catch((e) => toast('写入失败：' + (e.message || e)));
      });
    }

    function noteDialog(a) {
      const ta = h('textarea', { class: 'textarea', placeholder: '例如：主力号，额度月底重置', 'aria-label': '备注' }, a.note || '');
      const d = drawer('备注 · ' + a.label, (body, foot) => {
        body.append(ta);
        foot.append(
          btn('取消', { onClick: () => d.close() }),
          btn('保存', { primary: true, onClick: async () => {
            try { await api.post('accounts/' + encodeURIComponent(a.account_id) + '/note', { note: ta.value.trim() }); d.close(); toast('备注已保存'); await load(); }
            catch (e) { toast('保存失败：' + (e.message || e)); }
          } }));
      }, { compact: true });
    }

    function tagDialog(a) {
      const inp = h('input', { class: 'input', value: (a.tags || []).join(', '), placeholder: '逗号分隔，最多 8 个', 'aria-label': '标签' });
      const d = drawer('标签 · ' + a.label, (body, foot) => {
        body.append(inp);
        foot.append(
          btn('取消', { onClick: () => d.close() }),
          btn('保存', { primary: true, onClick: async () => {
            const tags = inp.value.split(/[,，\s]+/).map((s) => s.trim()).filter(Boolean).slice(0, 8);
            try { await api.post('accounts/' + encodeURIComponent(a.account_id) + '/tags', { tags }); d.close(); toast('标签已保存'); await load(); }
            catch (e) { toast('保存失败：' + (e.message || e)); }
          } }));
      }, { compact: true });
    }

    function accountSettings(a) {
      const p = providerOf(a.provider_id);
      const fact = { disabled: !!a.disabled };
      const dis = toggle(fact.disabled, (v) => { fact.disabled = v; }, { title: '停用后不再参与路由' });
      const pri = input({ type: 'number', value: String(a.priority || 0), label: '优先级', placeholder: '0 = 默认，1 = 首选' });
      const wt = input({ type: 'number', value: String(a.weight || 1), label: '权重', placeholder: '1 - 1000' });
      const conc = input({ type: 'number', value: a.concurrency == null ? '' : String(a.concurrency), label: '并发上限', placeholder: '留空 = 跟随全局' });
      // 窗口配额闸：用到上游窗口的百分之几就先把这号按住。
      // 订阅型渠道的限流按 5h/7d 窗口计，打满后整个窗口都发不出去；
      // 提前留一点余量，可以把"用满即挂"变成"平滑用光"。
      const capShares = (a.window_caps && a.window_caps.all) || 0;
      const capAll = input({
        type: 'number',
        value: capShares > 0 && capShares < 100 ? String(capShares) : '',
        label: '窗口配额闸',
        placeholder: '留空 = 不限制；例：90 表示用到 90% 就暂停这个号',
      });
      const exposed = ((p && p.models) || []).filter((m) => m.on).map((m) => m.id);
      const modelBox = h('div', { class: 'list', style: 'max-height:220px;overflow:auto;gap:2px' });
      if (!exposed.length) {
        modelBox.append(h('div', { class: 'meta' }, '该渠道尚未暴露模型；请到渠道「模型」页勾选。'));
      }
      for (const id of exposed) {
        const m = (p.models || []).find((x) => x.id === id);
        modelBox.append(h('div', { class: 'row', style: 'gap:8px' },
          h('span', { class: 'mono', style: 'font-size:12px' }, id),
          m && m.credit_rate ? chip('×' + m.credit_rate, 'plain') : null));
      }
      const binding = p?.capabilities?.official_binding ? bindingSection(a, p) : null;
      const d = drawer('账号设置 · ' + a.label, (body) => {
        body.append(
          field('状态', dis),
          h('div', { class: 'inline', style: 'gap:10px;align-items:flex-start' }, field('优先级', pri), field('权重', wt)),
          field('并发上限', conc),
          field('窗口配额闸', capAll),
          ...(binding ? [binding] : []),
          h('div', { class: 'meta' }, '可用模型由渠道「模型」页统一维护；账号级白名单不再单独设置。'),
          modelBox,
          h('div', { class: 'inline', style: 'justify-content:flex-end' },
            btn('取消', { onClick: () => d.close() }),
            btn('保存', { primary: true, onClick: async () => {
              const payload = {
                disabled: fact.disabled,
                priority: Math.max(0, Number(pri.value) || 0),
                weight: Math.min(1000, Math.max(1, Number(wt.value) || 1)),
                concurrency: conc.value.trim() === '' ? -1 : Math.max(0, Number(conc.value) || 0),
                // 窗口配额闸：留空或 >=100 表示不限制（传 0）。
                window_caps: { all: Math.min(100, Math.max(0, Number(capAll.value) || 0)) },
                // 账号白名单与渠道模型选择合并：清掉旧账号级限制，统一看渠道勾选。
                allowed_models: [],
              };
              try { await api.post('accounts/' + encodeURIComponent(a.account_id) + '/settings', payload); d.close(); toast('账号设置已保存'); await load(); }
              catch (e) { toast('保存失败：' + (e.message || e)); }
            } })),
        );
      });
      return d;
    }

    function bindingSection(a, p) {
      const box = h('div', { class: 'list', style: 'gap:4px' });
      const info = h('div', { class: 'meta' }, '读取官方绑定…');
      const acts = h('div', { class: 'inline' });
      box.append(h('div', { class: 'meta' }, '官方授权绑定'), info, acts);
      api.get('accounts/' + encodeURIComponent(a.account_id) + '/binding').then((r) => {
        const b = r.binding || {};
        const bits = [];
        if (b.account_id) bits.push('账号 ' + b.account_id);
        if (b.email) bits.push(b.email);
        if (b.plan) bits.push('套餐 ' + b.plan);
        if (b.expires_at) bits.push('凭证到期 ' + fmtDate(new Date(b.expires_at * 1000).toISOString()));
        info.textContent = (b.official ? '官方订阅' : '第三方中转') + (bits.length ? ' · ' + bits.join(' · ') : '');
        if (r.paused) info.textContent += ' · 官方通道已暂停' + (r.pause_reason ? '（' + r.pause_reason + '）' : '');
        if (b.official) {
          acts.append(btn('重新授权', { onClick: () => rebind(a, p, acts) }));
          if (r.paused) {
            acts.append(btn('恢复官方通道', { onClick: async (e) => {
              const el = e.currentTarget;
              el.disabled = true;
              try { await api.post('accounts/' + encodeURIComponent(a.account_id) + '/official/resume', {}); toast('官方通道已恢复'); await load(); }
              catch (err) { toast('恢复失败：' + (err.message || err)); }
              if (el.isConnected) el.disabled = false;
            } }));
            acts.append(btn('检查授权', { ghost: true, onClick: async (e) => {
              const el = e.currentTarget;
              el.disabled = true;
              try {
                const out = await api.post('accounts/' + encodeURIComponent(a.account_id) + '/official/check', {});
                toast(out.ok ? '授权有效，官方通道已恢复' : '授权检查失败：' + (out.error || '未知原因'));
                await load();
              } catch (err) { toast('检查失败：' + (err.message || err)); }
              if (el.isConnected) el.disabled = false;
            } }));
          } else {
            acts.append(btn('暂停官方通道', { ghost: true, onClick: async (e) => {
              const el = e.currentTarget;
              el.disabled = true;
              try { await api.post('accounts/' + encodeURIComponent(a.account_id) + '/official/pause', { reason: '手动暂停' }); toast('官方通道已暂停'); await load(); }
              catch (err) { toast('暂停失败：' + (err.message || err)); }
              if (el.isConnected) el.disabled = false;
            } }));
          }
        }
      }).catch((e) => { info.textContent = '绑定信息读取失败：' + (e.message || e); });
      return box;
    }

    async function rebind(a, p, host) {
      host.replaceChildren(h('div', { class: 'meta' }, '正在发起授权…'));
      try {
        const r = await api.post('auth/start', { provider_id: p.id, method: 'oauth_pkce' });
        const s = r.session || r;
        host.replaceChildren(
          h('div', { class: 'meta' }, '1. 打开授权页　2. 完成登录　3. 回到这里自动替换凭证'),
          h('div', { class: 'inline' },
            btn('打开授权页面', { primary: true, icon: 'external', onClick: () => openInBrowser(s.authorize_url) }),
            btn('复制链接', { ghost: true, onClick: () => copyText(s.authorize_url) })));
        const timer = setInterval(async () => {
          if (!host.isConnected) { clearInterval(timer); return; }
          try {
            const rr = await api.get('auth/' + encodeURIComponent(s.id));
            const ss = rr.session || rr;
            if (ss.status === 'success') {
              clearInterval(timer);
              await api.post('auth/' + encodeURIComponent(s.id) + '/commit', { rebind_account_id: a.account_id });
              toast('凭证已更新');
              await load();
            } else if (ss.status === 'error' || ss.error) {
              clearInterval(timer);
              host.append(h('div', { class: 'meta' }, '授权失败：' + (ss.error || '未知错误')));
            }
          } catch (e) {
            clearInterval(timer);
            host.append(h('div', { class: 'meta' }, '轮询失败：' + (e.message || e)));
          }
        }, 2200);
      } catch (e) {
        host.replaceChildren(h('div', { class: 'meta' }, '无法发起授权：' + (e.message || e)));
      }
    }

    function batchSettings() {
      const ids = [...state.picked];
      if (!ids.length) { toast('先勾选账号'); return; }
      const act = select([{ value: '', label: '不修改' }, { value: 'on', label: '启用' }, { value: 'off', label: '停用' }], '', () => {});
      const pri = input({ type: 'number', placeholder: '不改就留空', label: '优先级' });
      const wt = input({ type: 'number', placeholder: '不改就留空', label: '权重' });
      const d = drawer('批量设置 · ' + ids.length + ' 个账号', (body) => {
        body.append(
          field('状态', act),
          h('div', { class: 'inline', style: 'gap:10px;align-items:flex-start' }, field('优先级', pri), field('权重', wt)),
          h('div', { class: 'inline', style: 'justify-content:flex-end' },
            btn('取消', { onClick: () => d.close() }),
            btn('应用', { primary: true, onClick: async () => {
              const payload = {};
              if (act.value) payload.disabled = act.value === 'off';
              if (pri.value.trim() !== '') payload.priority = Math.max(0, Number(pri.value) || 0);
              if (wt.value.trim() !== '') payload.weight = Math.min(1000, Math.max(1, Number(wt.value) || 1));
              if (!Object.keys(payload).length) { toast('没有要修改的项'); return; }
              try {
                for (const id of ids) await api.post('accounts/' + encodeURIComponent(id) + '/settings', payload);
                d.close(); toast('已更新 ' + ids.length + ' 个账号'); await load();
              } catch (e) { toast('批量设置失败：' + (e.message || e)); }
            } })),
        );
      });
      return d;
    }

    function testModal(a) {
      const p = providerOf(a.provider_id);
      const status = h('div', { class: 'meta' }, '准备测试…');
      const result = h('div', { class: 'test-result' });
      const historyBox = h('div', { class: 'list', style: 'gap:4px' });
      let timer = 0;
      let closed = false;
      const stop = () => { if (timer) clearInterval(timer); timer = 0; };
      const runBtn = btn('重新测试', { icon: 'play', onClick: () => run() });
      const d = drawer('测试 · ' + a.label, (body) => {
        const head = h('div', { class: 'inline' });
        const bi = brandIcon(a.provider_id, 18);
        if (bi) head.append(bi);
        head.append(h('span', { class: 'name' }, a.label), chip((p && (p.display_name || p.name)) || a.provider_id, 'plain'));
        body.append(head, status, result, h('div', { class: 'meta', style: 'margin-top:4px' }, '最近记录'), historyBox,
          h('div', { class: 'inline', style: 'justify-content:flex-end' }, btn('关闭', { onClick: () => d.close() }), runBtn));
        run();
      });
      const origClose = d.close;
      d.close = () => { closed = true; stop(); origClose(); };

      function paintHistory(entry) {
        clear(historyBox);
        const hist = (entry && entry.history) || [];
        if (!hist.length) { historyBox.append(h('div', { class: 'meta' }, '还没有测试记录')); return; }
        for (const it of hist.slice(0, 6)) {
          historyBox.append(h('div', { class: 'row', style: 'gap:8px' },
            h('span', { class: 'dot', style: 'width:7px;height:7px;border-radius:50%;background:var(--' + (it.ok ? 'green' : 'red') + ')' }),
            h('span', { class: 'meta mono' }, new Date(it.at * 1000).toLocaleString()),
            h('span', { class: 'meta' }, it.ok ? '成功' : '失败'),
            h('span', { class: 'meta' }, (it.ms || 0) + ' ms'),
            h('span', { class: 'grow' }),
            it.error ? h('span', { class: 'meta', title: it.error, style: 'max-width:46%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap' }, it.error) : null));
        }
      }

      function paintResult(entry) {
        clear(result);
        const ok = !!entry.ok;
        const box = h('div', { class: 'card', style: 'padding:12px;display:flex;flex-direction:column;gap:6px;box-shadow:none' });
        box.append(h('div', { class: 'inline' },
          chip(ok ? '成功' : '失败', ok ? 'green' : 'red'),
          entry.code ? chip(String(entry.code), 'plain') : null,
          h('span', { class: 'grow' }),
          h('b', { style: 'font-variant-numeric:tabular-nums' }, (entry.ms || 0) + ' ms')));
        if (entry.model) box.append(h('div', { class: 'meta' }, '模型 ' + entry.model));
        if (entry.error) box.append(h('div', { class: 'mono', style: 'color:var(--red);font-size:12px;white-space:pre-wrap;word-break:break-word' }, entry.error));
        result.append(box);
      }

      async function run() {
        stop();
        runBtn.disabled = true;
        clear(result);
        status.textContent = '正在按成本从低到高发起探测…';
        const started = Math.floor(Date.now() / 1000) - 2;
        try {
          await api.post('channels/check', { provider: a.provider_id, account: a.account_id });
        } catch (e) {
          status.textContent = '无法发起测试：' + (e.message || e);
          runBtn.disabled = false;
          return;
        }
        let tries = 0;
        timer = setInterval(async () => {
          if (closed || !d.el.isConnected) { stop(); return; }
          tries++;
          try {
            const s = await api.get('channels/status');
            const entry = (s.entries || []).find((x) => x.account === a.account_id && x.provider === a.provider_id && x.at >= started);
            if (entry) {
              stop();
              status.textContent = '测试完成';
              paintResult(entry);
              paintHistory(entry);
              runBtn.disabled = false;
              return;
            }
            if (tries >= 30) {
              stop();
              status.textContent = '20 秒内没有返回结果（后台可能仍在进行）';
              runBtn.disabled = false;
            }
          } catch (e) {
            stop();
            status.textContent = '查询结果失败：' + (e.message || e);
            runBtn.disabled = false;
          }
        }, 700);
      }
      return d;
    }

    /* ---------- 自定义渠道抽屉 ---------- */
    // 把渠道页的数据入口交给抽屉：保存后它要能拿到新的 custom-<id> provider，
    // 才能直接走 openProvider 的接入流程；重新 load 一次也能让卡片立刻出现。
    // 这是**唯一**新建渠道入口：顶部常驻按钮、空态虚线卡、深链 aether.addProvider 都走它。
    function customChannelsDrawer() {
      openCustomChannels({ getProviders: () => state.providers, reload: load });
    }

    /* ---------- 渠道开关抽屉（只剩开关与预设：新建入口已并入「自定义渠道」） ---------- */
    function supplierDrawer() {
      const d = drawer('渠道开关', (body) => {
        const list = h('div', { class: 'list' });
        for (const p of state.providers) {
          const sw = h('button', { class: 'toggle' + (p.off ? '' : ' on'), type: 'button', role: 'switch', 'aria-checked': p.off ? 'false' : 'true', onclick: async () => {
            try { await api.post('provider/' + (p.off ? 'on' : 'off'), { id: p.id }); await load(); d.close(); toast(p.name + (p.off ? ' 已开启' : ' 已关闭')); }
            catch (e) { toast('切换失败：' + (e.message || e)); }
          } }, h('i'));
          const attached = (((state.presets || {}).provider_presets || {})[p.id] || []).length;
          list.append(h('div', { class: 'row' }, brandIcon(p.id, 16) || icon('channels', 16), h('span', { class: 'name' }, p.name || p.id), h('span', { class: 'meta' }, (p.models || []).filter((m) => m.on).length + ' 个模型'), attached ? chip(attached + ' 预设', 'plain') : null, p.auto_off ? chip(autoOffLabel(p), 'amber') : null, h('span', { class: 'grow' }), btn('预设', { ghost: true, onClick: () => { d.close(); providerPresetDialog(p); } }), btn('接入', { ghost: true, onClick: () => { d.close(); openProvider(p, load); } }), sw));
        }
        body.append(list);
        body.append(h('div', { class: 'inline', style: 'justify-content:flex-end;padding-top:10px' },
          btn('管理预设', { ghost: true, onClick: () => { d.close(); ctx.navigate('settings', { tab: 'presets' }); } })));
      });
    }

    function providerPresetDialog(p) {
      const src = state.presets || { presets: [], provider_presets: {} };
      const attached = new Set((src.provider_presets || {})[p.id] || []);
      const boxes = [];
      let directConcat = !!p.concatSystem;
      const directPrompt = h('textarea', { class: 'textarea', rows: 4, placeholder: '直接输入系统提示词并保存到当前渠道；不创建、也不挂载预设', 'aria-label': '系统提示词' });
      directPrompt.value = p.systemPrompt || '';
      const directConcatBtn = toggle(directConcat, (on) => { directConcat = on; }, { label: '提示词拼接' });
      const saveDirectBtn = btn('保存到渠道（不保存预设）', { onClick: async () => {
        try {
          await api.post('provider/save', { id: p.id, systemPrompt: directPrompt.value, concatSystem: !!directConcat });
          toast('已填入渠道');
          d.close();
          await load();
        } catch (e) { toast('保存失败：' + (e.message || e)); }
      } });
      const d = drawer('渠道预设 · ' + (p.name || p.id), (body) => {
        const list = h('div', { class: 'preset-picker' });
        if (!(src.presets || []).length) {
          list.append(h('div', { class: 'preset-empty meta' }, '还没有预设；可以先直接填入系统提示词，或到 设置 → 预设 新建。'));
        }
        for (const preset of src.presets || []) {
          const box = h('input', { type: 'checkbox', checked: attached.has(preset.id), 'aria-label': '挂载预设 ' + (preset.name || preset.id) });
          boxes.push({ id: preset.id, box });
          list.append(h('label', { class: 'preset-item' }, box,
            h('span', { class: 'preset-item-main' },
              h('span', { class: 'inline', style: 'gap:6px' },
                h('span', { class: 'name' }, preset.name || preset.id),
                preset.enabled ? null : chip('停用', 'plain'),
                preset.system_prompt ? chip('提示词', 'green') : null),
              h('span', { class: 'meta' }, 'Body ' + Object.keys(preset.body || {}).length + ' · Headers ' + Object.keys(preset.headers || {}).length + (preset.description ? ' · ' + preset.description : '')))));
        }
        body.append(
          h('div', { class: 'preset-form' },
            field('系统提示词', directPrompt),
            field('提示词拼接', h('div', { class: 'inline' }, directConcatBtn, h('span', { class: 'meta' }, '开启后与用户首条消息拼接'))),
            h('div', { class: 'field' }, h('span'), h('div', { class: 'inline', style: 'justify-content:flex-end' }, saveDirectBtn))),
          field('已保存预设', list),
          h('div', { class: 'meta preset-help' }, '直接填入不会创建预设；上方勾选的是该渠道挂载的已保存预设。渠道静态 extraBody/headers 仍然生效。'),
          h('div', { class: 'inline', style: 'justify-content:flex-end;padding-top:4px' },
            btn('管理预设', { ghost: true, onClick: () => { d.close(); ctx.navigate('settings', { tab: 'presets' }); } }),
            btn('取消', { onClick: () => d.close() }),
            btn('保存', { primary: true, onClick: async () => {
              const ids = boxes.filter((b) => b.box.checked).map((b) => b.id);
              try {
                state.presets = await api.post('presets/attach', { scope: 'provider', key: p.id, ids });
                d.close();
                toast('渠道预设已保存');
                await load();
              } catch (e) { toast('保存失败：' + (e.message || e)); }
            } })));
      });
    }

    // `addProviderDialog` 已删除：它和 customchannels.js 的抽屉是同一件事
    // （后端 provider_api.go:206-252 对未注册 id 都会 upsert CustomChannel 再建账号）。
    // 现在唯一的入口是 customChannelsDrawer。

    /* ---------- 导出（脱敏 / 含凭证加密） ---------- */
    function exportDialog() {
      let includeCreds = false;
      const status = h('div', { class: 'meta' });
      const pass = input({ type: 'password', placeholder: '至少 8 位；请与文件分开保存' });
      const passField = field('口令', pass);
      passField.style.display = 'none';
      const note = h('div', { class: 'meta' }, '脱敏清单只含账号元数据，不含任何凭证，不能用于恢复账号。');
      const pick = (checked, text, onPick) => h('label', { class: 'inline', style: 'gap:8px;align-items:center' },
        h('input', { type: 'radio', name: 'export-mode', checked, onchange: onPick }),
        h('span', null, text));
      const redact = pick(true, '仅元数据（脱敏，不含凭证）', () => {
        includeCreds = false;
        passField.style.display = 'none';
        note.textContent = '脱敏清单只含账号元数据，不含任何凭证，不能用于恢复账号。';
      });
      const full = pick(false, '包含凭证（加密账号包，全部账号）', () => {
        includeCreds = true;
        passField.style.display = '';
        note.textContent = '账号包包含全部账号凭证，AES-256-GCM 口令加密；导入时需同一口令，账号会回到原渠道。';
      });
      const go = btn('导出', { primary: true, onClick: async (e) => {
        const el = e.currentTarget;
        if (!includeCreds) {
          const a = document.createElement('a');
          a.href = '/api/accounts/export';
          a.download = 'accounts-export.json';
          a.click();
          status.textContent = '已导出脱敏清单（不含凭证）';
          return;
        }
        const phrase = (pass.value || '').trim();
        if (phrase.length < 8) { toast('口令至少 8 个字符'); return; }
        el.disabled = true;
        try {
          const r = await api.post('accounts/export/bundle', { passphrase: phrase });
          const bin = atob(r.data || '');
          const bytes = new Uint8Array(bin.length);
          for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
          const a = document.createElement('a');
          a.href = URL.createObjectURL(new Blob([bytes], { type: 'application/octet-stream' }));
          a.download = r.filename || 'aether-accounts.aether-accounts';
          a.click();
          setTimeout(() => URL.revokeObjectURL(a.href), 3000);
          status.textContent = '已导出 ' + Number(r.accounts || 0) + ' 个账号（含凭证，已加密）；请与口令分开保存。';
          toast('账号包已导出');
        } catch (err) {
          status.textContent = '导出失败：' + (err.message || err);
          toast(status.textContent);
        }
        if (el.isConnected) el.disabled = false;
      } });
      drawer('导出账号', (body) => {
        body.append(
          h('div', { class: 'meta' }, '选择导出内容：'),
          redact, full, passField, note,
          h('div', { class: 'inline', style: 'justify-content:flex-end' }, go),
          status,
        );
      });
    }

    /* ---------- 详情（右侧抽屉，可编辑） ---------- */
    // endpointOf 读渠道某个协议端点的 Base URL。
    //
    // p.endpoints 是后端新给的地址集合；老回包没有这个字段时返回空串，
    // 让输入框显示占位符——绝不再把 p.chat（布尔）当 URL 用。
    function endpointOf(p, kind) {
      const eps = p && p.endpoints;
      const v = eps && eps[kind];
      return typeof v === 'string' ? v : '';
    }

    function openDetail(acct) {
      const a = state.accounts.find((x) => x.account_id === (acct.account_id || acct));
      const p = a && providerOf(a.provider_id);
      if (!a || !p) { toast('渠道信息已变化，请刷新后重试'); return; }
      const d = {
        name: p.display_name || p.name || p.id,
        // 端点地址统一从 p.endpoints 读：p.chat 是"能否对话"的布尔能力，
        // 不是 URL（历史上混用过，导致保存时把 true 当 Base URL 发出去直接失败）。
        chat: endpointOf(p, 'chat'),
        responses: endpointOf(p, 'responses'),
        anthropic: endpointOf(p, 'anthropic'),
        gemini: endpointOf(p, 'gemini'),
        key: '', models: (p.models || []).map((m) => m.id).join(', '),
        modelMode: p.models_mode || 'merge',
        passUserAgent: !!p.passUserAgent, unlisted: !!p.unlisted, insecureTLS: !!p.insecureTLS,
        proxy: '', fallback: (p.fallback || []).join(', '),
        priceRate: p.priceRate == null ? '' : String(p.priceRate),
        systemPrompt: p.systemPrompt || '', concatSystem: !!p.concatSystem,
        maxConcurrency: p.maxConcurrency == null ? '' : String(p.maxConcurrency),
        maxRPM: p.maxRPM == null ? '' : String(p.maxRPM),
        queueLimit: p.queueLimit == null ? '' : String(p.queueLimit),
        queueWait: p.queueWait == null ? '' : String(p.queueWait),
        headers: JSON.stringify(p.headers || {}, null, 2),
        extraBody: p.extraBody ? JSON.stringify(p.extraBody, null, 2) : '',
      };
      // 模型选择与倍率：以后端回显的 on/credit_rate 为初始值。
      const modelState = new Map();
      for (const m of p.models || []) modelState.set(m.id, { on: !!m.on, rate: Number(m.credit_rate) || 0, ctx: Number(m.context || m.max) || 0, costKnown: !!m.cost_known, costIn: Number(m.cost_in) || 0, costOut: Number(m.cost_out) || 0, efforts: m.efforts || [], free: !!m.free, manual: !!m.manual, name: m.name || m.id });
      // 只有用户真的动过模型勾选/倍率才提交 models，避免在 fallback 模式
      // 下列表只剩实时目录时，一次无关保存就把静态补缺的模型选择覆盖掉。
      let modelsDirty = false;
      let ratesDirty = false;
      const local = { tab: 'basic' };
      let panel = null;
      const dr = drawer('详情 · ' + (p.name || p.id), (body) => { panel = body; draw(); });

      function draw() {
        clear(panel);
        panel.append(
          h('div', { class: 'inline', style: 'justify-content:space-between;gap:8px;flex-wrap:wrap' },
            h('span', { class: 'meta' }, state.accounts.filter((x) => x.provider_id === p.id).length + ' 个账号 · ' + (p.name || p.id)),
            h('div', { class: 'inline', style: 'gap:8px' },
              btn('接入账号', { onClick: () => { dr.close(); openProvider(p, load); } }),
              btn('保存修改', { primary: true, onClick: saveEditor }))),
          h('div', { class: 'tabs' }, [['basic', '基础'], ['models', '模型'], ['limits', '限流'], ['net', '网络'], ['advanced', '高级'], ['headers', '请求头 / UA']].map(([id, label]) =>
            h('button', { class: 'tab' + (local.tab === id ? ' on' : ''), type: 'button', onclick: () => { local.tab = id; draw(); } }, label))),
          buildFields());
      }

      function buildFields() {
        const fields = h('div', { class: 'detail-fields' });
        const text = (label, key, placeholder) => {
          const inp = h('input', { class: 'input', value: d[key], placeholder: placeholder || '', oninput: (e) => { d[key] = e.target.value; } });
          fields.append(h('div', { class: 'detail-field' }, h('label', null, label), inp));
        };
        const area = (label, key, rows) => {
          const ta = h('textarea', { class: 'textarea', rows: rows || 4, oninput: (e) => { d[key] = e.target.value; } });
          ta.value = d[key];
          fields.append(h('div', { class: 'detail-field', style: 'align-items:flex-start' }, h('label', null, label), ta));
        };
        const sw = (label, key) => {
          const b = h('button', { class: 'toggle' + (d[key] ? ' on' : ''), type: 'button', role: 'switch', 'aria-checked': d[key] ? 'true' : 'false', onclick: () => { d[key] = !d[key]; b.classList.toggle('on', d[key]); b.setAttribute('aria-checked', d[key] ? 'true' : 'false'); } }, h('i'));
          fields.append(h('div', { class: 'detail-field' }, h('label', null, label), h('div', { class: 'inline' }, b, h('span', { class: 'meta' }, d[key] ? '开' : '关'))));
        };
        if (local.tab === 'basic') {
          text('名称', 'name');
          text('OpenAI Chat Base URL', 'chat', 'https://api.example.com/v1');
          text('Responses Base URL', 'responses', '');
          text('Anthropic Base URL', 'anthropic', '');
          text('Gemini Base URL', 'gemini', '');
          const keyInp = h('input', { class: 'input', type: 'password', placeholder: '留空不改；填了会覆盖这个渠道的密钥', oninput: (e) => { d.key = e.target.value; } });
          fields.append(h('div', { class: 'detail-field' }, h('label', null, 'API Key'), keyInp));
          fields.append(h('div', { class: 'detail-field' }, h('label', null, '账号'), h('span', { class: 'meta' }, state.accounts.filter((x) => x.provider_id === p.id).length + ' 个')));
        } else if (local.tab === 'models') {
          const mode = d.modelMode || 'merge';
          const modePending = mode !== (p.models_mode || 'merge');
          const modeNote = {
            merge: '上游实时 + 静态清单，live 优先、静态补缺',
            fallback: '上游实时优先，拉取失败或为空时用静态清单',
            allowlist: '只用静态清单，不访问上游 /models',
          }[mode] || '';
          const srcLabel = mode === 'allowlist'
            ? '来源：静态目录（allowlist 模式）'
            : p.models_source === 'live'
              ? (mode === 'merge' ? '来源：上游实时 + 静态补缺' : '来源：上游实时（fallback 模式）') + (p.models_fetched ? ' · ' + fmtDate(p.models_fetched) + ' 更新' : '')
              : '来源：静态目录（' + (mode === 'fallback' ? '上游无可用目录' : '没有上游模型列表') + '）';
          const modeSel = select(
            [{ value: 'merge', label: '合并（推荐）' }, { value: 'fallback', label: '回退' }, { value: 'allowlist', label: '白名单' }],
            mode,
            (v) => { d.modelMode = v; draw(); },
            { label: '目录模式' },
          );
          const refreshBtn = p.models_live_supported && mode !== 'allowlist' ? btn('从上游刷新', { ghost: true, class: 'sm', onClick: async (e) => {
            const el = e.currentTarget;
            el.disabled = true;
            try {
              const r = await api.post('providers/' + encodeURIComponent(p.id) + '/models/refresh', {});
              toast('已刷新 ' + (r.models || 0) + ' 个模型');
              await load();
              dr.close();
              const a2 = state.accounts.find((x) => x.account_id === acct.account_id);
              if (a2) openDetail(a2);
            } catch (err) { toast('刷新失败：' + (err.message || err)); }
            if (el.isConnected) el.disabled = false;
          } }) : null;
          fields.append(h('div', { class: 'detail-field', style: 'align-items:flex-start' },
            h('label', null, '目录模式'),
            h('div', { style: 'width:100%;display:flex;flex-direction:column;gap:6px' },
              h('div', { class: 'inline', style: 'gap:8px;align-items:center;flex-wrap:wrap' }, modeSel, refreshBtn),
              h('span', { class: 'meta' }, srcLabel + (modeNote ? ' · ' + modeNote : '') + (modePending ? ' · 保存后生效' : '')))));
          async function reloadAfterModelChange() {
            await load();
            dr.close();
            const a2 = state.accounts.find((x) => x.account_id === acct.account_id);
            if (a2) openDetail(a2);
          }
          async function changeExtraModel(model, action) {
            await api.post('providers/' + encodeURIComponent(p.id) + '/models/extra', { model, action });
            toast(action === 'remove' ? '已移除 ' + model : '已添加 ' + model);
            await reloadAfterModelChange();
          }
          const extraInput = h('input', { class: 'input', placeholder: '输入上游模型名，例如 deepseek-v4.1-flash', style: 'flex:1;min-width:180px' });
          const addExtra = async () => {
            const model = extraInput.value.trim();
            if (!model) { toast('请输入模型名'); return; }
            try { await changeExtraModel(model, 'add'); } catch (err) { toast('添加失败：' + (err.message || err)); }
          };
          extraInput.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); addExtra(); } });
          fields.append(h('div', { class: 'detail-field', style: 'align-items:flex-start' },
            h('label', null, '添加缺失模型'),
            h('div', { style: 'width:100%;display:flex;flex-direction:column;gap:6px' },
              h('div', { class: 'inline', style: 'gap:8px' }, extraInput, btn('添加', { ghost: true, class: 'sm', onClick: addExtra })),
              h('span', { class: 'meta' }, '上游 /models 漏报或厂商不实现 /models 时用；添加后参与路由与 /v1/models。V4 Flash 与 V4.1 Flash 是两个模型，请按实际上游名分别添加。'))));
          const list = h('div', { class: 'list', style: 'max-height:320px;overflow:auto;gap:2px' });
          const fmtCtx = (n) => (!n ? '' : n >= 1e6 ? (Math.round(n / 1e5) / 10) + 'M' : n >= 1000 ? Math.round(n / 1000) + 'K' : String(n));
          const fmtPrice = (n) => String(n >= 0.1 ? Math.round(n * 100) / 100 : n);
          const priceText = (st) => (st.free ? '免费' : !st.costKnown ? '' : (st.costIn || st.costOut) ? '$' + fmtPrice(st.costIn) + '/' + fmtPrice(st.costOut) : '免费');
          const drawList = () => {
            clear(list);
            const ids = [...modelState.keys()];
            if (!ids.length) { list.append(h('div', { class: 'meta' }, '该渠道没有登记模型。')); return; }
            for (const id of ids) {
              const st = modelState.get(id);
              const rate = h('input', { class: 'input', type: 'number', step: '0.1', min: '0', placeholder: '×1', style: 'width:88px;flex:none', value: st.rate ? String(st.rate) : '', oninput: (e) => { st.rate = Math.max(0, Number(e.target.value) || 0); ratesDirty = true; } });
              const price = priceText(st);
              const main = h('div', { style: 'flex:1;min-width:0;display:flex;flex-direction:column;gap:2px' },
                h('span', { class: 'mono', style: 'font-size:12px;white-space:normal;word-break:break-word;line-height:1.35', title: id }, st.name || id),
                h('span', { class: 'meta', style: 'display:flex;gap:10px;flex-wrap:wrap' },
                  st.manual ? chip('手动', 'plain') : null,
                  price ? h('span', { title: '美元 / 百万 tokens（输入/输出）' }, price) : null,
                  fmtCtx(st.ctx) ? h('span', null, fmtCtx(st.ctx)) : null));
              const del = st.manual ? btn('删除', { ghost: true, class: 'sm', onClick: async (e) => {
                e.preventDefault();
                e.stopPropagation();
                try { await changeExtraModel(id, 'remove'); } catch (err) { toast('移除失败：' + (err.message || err)); }
              } }) : null;
              list.append(h('label', { class: 'row', style: 'cursor:pointer;gap:8px;align-items:flex-start', title: st.efforts.length ? '推理档位：' + st.efforts.join(' / ') : '' },
                h('input', { type: 'checkbox', checked: st.on, style: 'margin-top:2px', onchange: (e) => { st.on = e.target.checked; modelsDirty = true; } }),
                main,
                rate,
                del));
            }
          };
          drawList();
          fields.append(h('div', { class: 'detail-field', style: 'align-items:flex-start' }, h('label', null, '暴露的模型'),
            h('div', { style: 'width:100%' }, list,
              h('div', { class: 'inline', style: 'margin-top:6px' }, btn('全选', { ghost: true, class: 'sm', onClick: () => { for (const st of modelState.values()) st.on = true; modelsDirty = true; drawList(); } }),
                btn('清空', { ghost: true, class: 'sm', onClick: () => { for (const st of modelState.values()) st.on = false; modelsDirty = true; drawList(); } }),
                h('span', { class: 'meta' }, '右侧为该模型的积分消耗倍率（留空=未配置）')))));
          fields.append(h('div', { class: 'meta', style: 'padding:6px 0' }, '只有勾选的模型会出现在 /v1/models；未接入账号的渠道整体不出现。'));
        } else if (local.tab === 'net') {
          // 连接预热 / 在途上限 / 边缘优选：这些是"这个上游地址"的属性，
          // 所以放在渠道详情里，而不是塞进全局设置。
          fields.append(channelNetFields(p.id, d.chat));
        } else if (local.tab === 'limits') {
          text('计费倍率', 'priceRate', '1');
          text('渠道并发上限', 'maxConcurrency', '跟随全局');
          text('每分钟请求', 'maxRPM', '不限');
          text('队列长度', 'queueLimit', '跟随全局');
          text('队列等待（秒）', 'queueWait', '跟随全局');
        } else if (local.tab === 'advanced') {
          sw('模型 UA 透传', 'passUserAgent');
          sw('只在路由分组里使用', 'unlisted');
          sw('不校验 TLS 证书', 'insecureTLS');
          text('代理', 'proxy', '留空跟随全局，direct 直连');
          text('兜底顺序', 'fallback', 'provider/model, provider/model');
          area('额外请求体（JSON）', 'extraBody', 4);
          const promptArea = h('textarea', { class: 'textarea', rows: 4, placeholder: '直接输入系统提示词（用户提示词优先）' });
          promptArea.value = d.systemPrompt;
          promptArea.addEventListener('input', () => { d.systemPrompt = promptArea.value; });
          const concatBtn = toggle(!!d.concatSystem, (on) => { d.concatSystem = on; });
          const setConcat = (on) => {
            d.concatSystem = !!on;
            concatBtn.classList.toggle('on', d.concatSystem);
            concatBtn.setAttribute('aria-checked', d.concatSystem ? 'true' : 'false');
          };
          const presetFill = select(
            [{ value: '', label: '从预设填入（不挂载）' }, ...((state.presets?.presets || []).filter((x) => x.system_prompt).map((x) => ({ value: x.id, label: x.name })))],
            '', (id) => {
              const preset = (state.presets?.presets || []).find((x) => x.id === id);
              if (!preset) return;
              d.systemPrompt = preset.system_prompt || '';
              promptArea.value = d.systemPrompt;
              setConcat(!!preset.concat_system);
            }, { label: '从预设填入系统提示词' });
          fields.append(h('div', { class: 'detail-field', style: 'align-items:flex-start' }, h('label', null, '系统提示词'),
            h('div', { style: 'width:100%;display:flex;flex-direction:column;gap:8px' }, promptArea,
              h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap' }, presetFill, h('span', { class: 'meta' }, '系统提示词连接'), concatBtn))));
          fields.append(h('div', { class: 'meta', style: 'padding:4px 0' }, '直接输入并保存即可生效，不需要新建或挂载预设；从预设填入只复制内容，不会挂载预设。'));
        } else {
          area('自定义请求头（JSON）', 'headers', 6);
          fields.append(h('div', { class: 'meta', style: 'padding:6px 0' }, '值可以是字符串；保存前会校验 JSON 格式。'));
        }
        return fields;
      }

      function num(v) { const n = Number(v); return v === '' || Number.isNaN(n) ? null : n; }

      async function saveEditor() {
        const selected = [...modelState.entries()].filter(([, st]) => st.on).map(([id]) => id);
        const rates = {};
        for (const [id, st] of modelState.entries()) if (st.rate > 0) rates[id] = st.rate;
        const payload = { id: p.id, name: d.name, modelMode: d.modelMode, passUserAgent: d.passUserAgent, unlisted: d.unlisted, insecureTLS: d.insecureTLS, systemPrompt: d.systemPrompt, concatSystem: !!d.concatSystem, fallback: d.fallback.split(',').map((x) => x.trim()).filter(Boolean), priceRate: num(d.priceRate), maxConcurrency: num(d.maxConcurrency), maxRPM: num(d.maxRPM), queueLimit: num(d.queueLimit), queueWait: num(d.queueWait) };
        // 只有模型勾选/倍率真的被改动时才提交，避免无关保存覆盖既有选择。
        if (modelsDirty || ratesDirty) { payload.models = selected; payload.model_rates = rates; }
        // 只提交真正填好的端点。空串不提交（保持后端旧值），非字符串一律跳过：
        // 一旦把布尔/undefined 塞进 payload，后端会判定 Base URL 非法而整单保存失败。
        for (const k of ['chat', 'responses', 'anthropic', 'gemini']) {
          const v = d[k];
          if (typeof v === 'string' && v.trim()) payload[k] = v.trim();
        }
        if (d.key) payload.key = d.key;
        if (d.proxy.trim()) payload.proxy = d.proxy.trim();
        try { payload.headers = d.headers.trim() ? JSON.parse(d.headers) : {}; } catch { toast('请求头不是合法 JSON'); return; }
        try { payload.extraBody = d.extraBody.trim() ? JSON.parse(d.extraBody) : null; } catch { toast('额外请求体不是合法 JSON'); return; }
        try { await api.post('provider/save', payload); dr.close(); toast('已保存'); await load(); }
        catch (e) { toast('保存失败：' + (e.message || e)); }
      }
    }


  },
};
