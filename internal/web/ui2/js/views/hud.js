/* 额度悬浮（自有实现）：260×300 的紧凑额度视图；桌面端会缩窗并置顶。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { iconBtn, meter, skeleton, errorBox, empty, toast, fmtReset } from '../components.js';
import { brandMark } from '../icons.js';
import { hasWindowRuntime } from '../win.js';

function readAtISO(value) {
  let n = Number(value);
  if (!Number.isFinite(n) || n <= 0) return '';
  if (n < 1e12) n *= 1000; // 兼容秒与毫秒
  return new Date(n).toISOString();
}

function fmtBalance(value, unit) {
  const n = Number(value);
  if (!Number.isFinite(n)) return String(value);
  const text = Math.abs(n) >= 1000 ? n.toLocaleString('zh-CN', { maximumFractionDigits: 1 }) : String(Math.round(n * 100) / 100);
  return unit === 'credits' ? text + ' credits' : (unit ? text + ' ' + unit : text);
}

export default {
  title: '额度悬浮',
  subtitle: '最关键的额度窗口',

  mount(host, ctx) {
    const state = { rows: [], at: 0 };
    host.append(skeleton(4));
    load();

    async function load() {
      try {
        const data = await api.get('usage/quotas');
        // 优先展示 5h/7d 窗口；没有窗口时退回 Credits 等任意窗口，
        // 再没有就展示余额（Trae 这类只返回 credits 余额的渠道）。
        const worst = new Map();
        for (const p of Array.isArray(data) ? data : []) {
          const windows = (p.windows || []).filter((w) => typeof w.used === 'number');
          const preferred = windows.filter((w) => w.name === '5 hours' || w.name === '7 days');
          const pool = preferred.length ? preferred : windows;
          let picked = null;
          for (const w of pool) {
            let used = w.used;
            if (used > 100 && Number(w.limit) > 0) used = used / Number(w.limit) * 100;
            used = Math.max(0, Math.min(100, used));
            if (!picked || used > picked.used) picked = { w, used };
          }
          const key = (p.provider || p.name || '') + '|' + (p.user || '');
          if (picked) {
            const label = String(picked.w.pool || '');
            worst.set(key, {
              provider: p.name || p.provider, window: picked.w.name, used: picked.used,
              reset: picked.w.resetsAt, balance: p.balance, unit: p.balance_unit || '',
              pool: label.includes('Claude') ? 'Claude' : (label.includes('Gemini') ? 'Gemini' : ''),
            });
          } else if (p.balance != null && String(p.balance).trim() !== '') {
            worst.set(key, {
              provider: p.name || p.provider, window: '余额', used: null,
              balance: p.balance, unit: p.balance_unit || '', readAt: p.readAt,
            });
          }
        }
        const rows = [...worst.values()].sort((a, b) => (b.used ?? -1) - (a.used ?? -1));
        state.rows = rows.slice(0, 3);
        state.at = Date.now();
        render();
      } catch (e) {
        clear(host);
        host.append(errorBox('额度读取失败：' + (e.message || e), load));
      }
    }

    function render() {
      clear(host);
      const wrap = h('div', { class: 'hud' });
      wrap.append(h('div', { class: 'hud-h' },
        brandMark(20),
        h('b', null, '额度'),
        h('span', { class: 'grow', style: 'flex:1' }),
        iconBtn('refresh', '刷新额度', load, { size: 13 }),
        iconBtn('x', '退出悬浮', () => ctx.navigate('overview'), { size: 13 })));
      const list = h('div', { class: 'hud-list' });
      if (!state.rows.length) {
        list.append(empty('没有可读的额度', '渠道账号还没有返回额度或余额。'));
      } else {
        for (const r of state.rows) {
          const isBalance = r.used == null;
          const left = isBalance ? null : Math.max(0, Math.min(100, 100 - r.used));
          const tone = isBalance ? '' : (left <= 10 ? 'red' : (left <= 40 ? 'amber' : ''));
          list.append(h('div', { class: 'hud-row' },
            h('div', { class: 'hud-line' },
              h('span', { class: 'hud-name' }, r.provider),
              h('span', { class: 'hud-win' }, r.window + (r.pool ? ' · ' + r.pool : '')),
              isBalance
                ? h('span', { class: 'hud-pct balance' }, fmtBalance(r.balance, r.unit))
                : h('span', { class: 'hud-pct ' + (tone || 'ok') }, Math.round(left) + '%')),
            isBalance ? null : meter(left, tone),
            h('div', { class: 'hud-reset' }, isBalance
              ? (r.readAt ? '读取 ' + fmtReset(readAtISO(r.readAt)) : '—')
              : (r.reset ? fmtReset(r.reset) : (r.balance != null && String(r.balance).trim() !== '' ? '余额 ' + fmtBalance(r.balance, r.unit) : '—')))));
        }
      }
      wrap.append(list);
      wrap.append(h('div', { class: 'hud-foot' }, hasWindowRuntime() ? '置顶 · 退出即还原窗口' : '浏览器预览模式'));
      host.append(wrap);
    }
  },
};
