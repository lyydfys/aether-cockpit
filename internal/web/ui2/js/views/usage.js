/* 用量看板：总量、渠道/模型/调用方分类、缓存率、面积/柱状趋势与日历。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { card, chip, btn, empty, errorBox, skeleton, meter, fmtTokens, fmtCost, toast } from '../components.js';
import { createTrend } from '../trend.js';
import { icon } from '../icons.js';

const WD = ['一', '二', '三', '四', '五', '六', '日'];
const MONTHS = ['1月', '2月', '3月', '4月', '5月', '6月', '7月', '8月', '9月', '10月', '11月', '12月'];
const CLASSES = [['provider', '渠道'], ['model', '模型'], ['agent', '调用方']];

export default {
  title: '用量',
  subtitle: '总量、分类、缓存命中与成本趋势',

  mount(host, ctx) {
    const state = { days: 7, heat: null, view: null, pricing: { currency: 'USD', usd_cny: 7.2 }, mode: 'bars', klass: 'provider', chartBy: 'total', ro: null };
    host.append(skeleton(6));
    load();

    async function load() {
      clear(host);
      try {
        const period = state.days === 1 ? 'today' : state.days === 30 ? '30d' : '7d';
        const [heat, view, pricing] = await Promise.all([
          api.get('usage/heatmap').catch(() => ({ days: [] })),
          api.get('usage/requests?period=' + period + (state.days === 1 ? '&bucket=hour' : '') + '&limit=1').catch(() => ({ series: [], by: {} })),
          api.get('pricing').catch(() => ({ currency: 'USD', usd_cny: 7.2 })),
        ]);
        state.heat = heat || { days: [] };
        state.view = view || { series: [], by: {} };
        state.pricing = { currency: pricing.currency || 'USD', usd_cny: Number(pricing.usd_cny) || 7.2 };
        render();
      } catch (e) {
        clear(host);
        host.append(errorBox('用量读取失败：' + (e.message || e), load));
      }
    }

    const money = (n) => fmtCost(n, state.pricing.currency, state.pricing.usd_cny);

    function render() {
      // 每次重绘先拆掉上一轮的尺寸监听，否则会越攒越多（旧节点已脱离文档但 observer 还活着）
      if (state.ro) { state.ro.disconnect(); state.ro = null; }
      clear(host);
      host.append(currencyBar());
      host.append(h('div', { class: 'kpis' }, ...kpis()));
      host.append(trendCard());
      host.append(breakdownCard());
      host.append(heatCard());
    }

    function periodLabel() {
      return state.days === 1 ? '今日' : '近 ' + state.days + ' 天';
    }

    function currencyBar() {
      const period = h('div', { class: 'segbar', style: 'margin:0' }, h('div', { class: 'segbar__tabs' }));
      for (const [n, label] of [[1, '今日'], [7, '近 7 天'], [30, '近 30 天']]) {
        period.firstChild.append(h('button', {
          type: 'button', class: state.days === n ? 'on' : '',
          onclick: () => {
            if (state.days === n) return;
            state.days = Number(n);
            load();
          },
        }, label));
      }
      const bar = h('div', { class: 'inline', style: 'justify-content:flex-end;padding:0 0 8px' },
        period,
        h('span', { class: 'grow' }),
        h('span', { class: 'meta' }, '计价币种'),
        ...['USD', 'CNY'].map((c) => btn(c === 'USD' ? '$ USD' : '¥ CNY', {
          class: 'sm' + (state.pricing.currency === c ? ' on' : ''),
          onClick: async () => {
            if (state.pricing.currency === c) return;
            try {
              const p = await api.post('pricing/save', { price_currency: c });
              state.pricing = { currency: p.currency || c, usd_cny: Number(p.usd_cny) || state.pricing.usd_cny };
              render();
            } catch (e) { toast('切换币种失败：' + (e.message || e)); }
          },
        })),
        h('span', { class: 'meta' }, '汇率 1 USD = ' + state.pricing.usd_cny.toFixed(2) + ' CNY'));
      return bar;
    }

    function kpis() {
      const v = state.view || {};
      const inputTotal = (v.input || 0) + (v.cache_read || 0) + (v.cache_write || 0);
      const totalTokens = inputTotal + (v.output || 0);
      const cacheRate = inputTotal ? ((v.cache_read || 0) / inputTotal) * 100 : 0;
      const okRate = v.calls ? ((v.calls - (v.errors || 0)) / v.calls) * 100 : 0;
      // 四张卡，和概览同一节奏：原来 5 张塞进 4 列栅格，第 5 张独占一行、右边空四分之三。
      // 缓存命中率并入 Token 的说明行，它本来就是这个数的注解。
      return [
        kpi('请求数', v.calls || 0, periodLabel()),
        kpi('成功率', okRate.toFixed(1) + '%', '失败 ' + (v.errors || 0) + ' 次'),
        kpi('Token', fmtTokens(totalTokens), '缓存 ' + fmtTokens(v.cache_read || 0) + ' · 命中 ' + cacheRate.toFixed(1) + '%'),
        kpi('费用', money(v.cost || 0), v.unpriced ? '未计价 ' + v.unpriced + ' 次' : '按当前价格表'),
      ];
    }

    function trendCard() {
      const v = state.view || {};
      const series = v.series || [];
      const n = state.days;
      const hourly = n === 1;
      const byDay = new Map(series.map((x) => [String(x.time), x]));
      const days = [];
      if (hourly) {
        const now = new Date();
        const iso = now.getFullYear() + '-' + String(now.getMonth() + 1).padStart(2, '0') + '-' + String(now.getDate()).padStart(2, '0');
        for (let h = 0; h < 24; h++) {
          const hh = String(h).padStart(2, '0');
          const key = iso + 'T' + hh;
          const x = byDay.get(key);
          days.push(x
            ? { ...x, label: hh + ':00' }
            : { time: key, label: hh + ':00', calls: 0, input: 0, output: 0, cache_read: 0, cache_write: 0, cost: 0 });
        }
      } else {
        const end = new Date();
        for (let i = n - 1; i >= 0; i--) {
          const t = new Date(end);
          t.setDate(t.getDate() - i);
          const iso = t.getFullYear() + '-' + String(t.getMonth() + 1).padStart(2, '0') + '-' + String(t.getDate()).padStart(2, '0');
          days.push(byDay.get(iso) || { time: iso, calls: 0, input: 0, output: 0, cache_read: 0, cache_write: 0, cost: 0 });
        }
      }
      const callsOf = (d) => d.calls || 0;
      const costOf = (d) => d.cost || 0;
      const tokOf = (d) => (d.input || 0) + (d.output || 0) + (d.cache_read || 0) + (d.cache_write || 0);
      const totalCalls = days.reduce((a, d) => a + callsOf(d), 0);
      const totalCost = days.reduce((a, d) => a + costOf(d), 0);

      const byModel = state.chartBy === 'model';
      // 后端 chartBy.model 给的是「按调用量 top4 + 其他」的逐日序列，这里按日期对表
      const modelSeries = byModel ? ((v.chartBy || {}).model || []) : [];
      // 按模型：多序列面积图、不堆叠、点隐藏（对齐 relay-panel 的 VChart area spec）
      const calls = byModel
        ? modelSeries.map((m, i) => {
          const at = new Map((m.points || []).map((p) => [p.t, p.c || 0]));
          return {
            key: 'm' + i, label: m.name, axis: 'left', kind: state.mode === 'bars' ? 'bars' : 'area',
            color: 'k' + Math.min(i + 1, 10), values: days.map((d) => at.get(d.time) || 0),
          };
        })
        : [
          { key: 'calls', label: '调用', axis: 'left', kind: state.mode === 'bars' ? 'bars' : 'area', color: 'c1', values: days.map(callsOf) },
          { key: 'cost', label: '成本', axis: 'right', kind: 'line', color: 'c2', values: days.map(costOf) },
        ];

      const c = card('趋势', { chips: [] });
      // 卡头：图标徽标 + 标题 + 「总计: X」，右上角两组分段控件（对齐 relay-panel 消耗分布）
      c.head.querySelector('b').before(h('span', { class: 'card-badge' }, icon('usage', 14)));
      c.head.querySelector('b').after(h('span', { class: 'card-total' },
        '总计: ' + totalCalls.toLocaleString() + ' 次' + (byModel ? '' : ' · ' + money(totalCost))));
      c.head.append(h('span', { style: 'flex:1' }));
      const segBtn = (on, label, iconName, onClick) => h('button', {
        class: on ? 'on' : '', type: 'button', onclick: onClick,
      }, iconName ? icon(iconName, 13) : null, h('span', null, label));
      c.head.append(h('div', { class: 'seg' },
        segBtn(!byModel, '总计', null, () => { if (byModel) { state.chartBy = 'total'; render(); } }),
        segBtn(byModel, '按模型', null, () => { if (!byModel) { state.chartBy = 'model'; render(); } })));
      c.head.append(h('div', { class: 'seg', style: 'margin-left:6px' },
        segBtn(state.mode === 'bars', '柱状图', 'usage', () => { if (state.mode !== 'bars') { state.mode = 'bars'; render(); } }),
        segBtn(state.mode === 'area', '面积图', 'area', () => { if (state.mode !== 'area') { state.mode = 'area'; render(); } })));

      /* 图表本体在 js/trend.js：图例常驻左上、双序列双轴（调用左轴 / 成本右轴）、
         按模型时切换成堆叠面积、十字线读数。这里只负责喂数据与格式。 */
      const chart = createTrend({
        days,
        mode: state.mode,
        label: periodLabel() + (byModel ? '按模型调用构成' : '调用与成本趋势') + '，共 ' + days.length + (hourly ? ' 小时' : ' 天'),
        series: calls,
        formatLeft: (x) => fmtTokens(Math.round(x)),
        formatRight: (x) => money(x),
        tooltipOf: (d) => {
          if (byModel) {
            const i = days.indexOf(d);
            // 与 relay-panel 的 dimension tooltip 一致：总计在最前，其余按数值降序
            const items = calls
              .map((s) => ({ key: s.key, label: s.label, v: s.values[i] || 0, color: s.color }))
              .filter((x) => x.v > 0)
              .sort((a, b) => b.v - a.v);
            const sum = items.reduce((a, x) => a + x.v, 0);
            return {
              rows: [
                { label: '总计', value: sum.toLocaleString() + ' 次', on: true },
                ...items.map((x) => ({ key: x.key, label: x.label, value: x.v.toLocaleString() + ' 次', color: x.color })),
              ],
            };
          }
          return {
            rows: [
              { key: 'calls', label: '调用', value: (d.calls || 0).toLocaleString() + ' 次', color: 'c1', on: true },
              { key: 'cost', label: '成本', value: money(d.cost || 0), color: 'c2', on: true },
              { label: 'Token', value: fmtTokens(tokOf(d)) },
            ],
          };
        },
      });
      c.el.append(chart.el);
      // 首帧之后再量：卡片此刻还没进文档，量出来是 0
      const fit = () => chart.resize(chart.el.clientWidth);
      requestAnimationFrame(fit);
      state.ro = new ResizeObserver(fit);
      state.ro.observe(chart.el);
      return c.el;
    }

    function breakdownCard() {
      const v = state.view || {};
      const rows = ((v.by || {})[state.klass] || []).slice().sort((a, b) => (b.cost || 0) - (a.cost || 0) || (b.calls || 0) - (a.calls || 0));
      const c = card('用量分布', { chips: [chip('按成本占比', 'plain'), chip(rows.length + ' 类', 'plain')] });
      c.head.append(h('span', { style: 'flex:1' }));
      for (const [id, label] of CLASSES) c.head.append(btn(label, { class: 'sm' + (state.klass === id ? ' on' : ''), onClick: () => { state.klass = id; render(); } }));
      if (!rows.length) {
        c.el.append(empty('还没有用量数据', '发出请求后，这里会按渠道、模型和调用方分类。', btn('去接入一个渠道', { onClick: () => ctx.navigate('onboard') })));
        return c.el;
      }
      const maxCost = Math.max(1, ...rows.map((r) => r.cost || 0));
      const box = h('div', { class: 'usage-rows' });
      const totalInput = (v.input || 0) + (v.cache_read || 0) + (v.cache_write || 0);
      const totalCache = totalInput ? ((v.cache_read || 0) / totalInput) * 100 : 0;
      box.append(rankRow('合计', v.calls || 0, v.errors || 0, (v.input || 0) + (v.output || 0) + (v.cache_read || 0) + (v.cache_write || 0), totalCache, v.cost || 0, 100, true));
      for (const r of rows.slice(0, 12)) {
        const input = (r.input || 0) + (r.cache_read || 0) + (r.cache_write || 0);
        const rate = input ? ((r.cache_read || 0) / input) * 100 : 0;
        const pct = maxCost ? ((r.cost || 0) / maxCost) * 100 : 0;
        const name = r.name || r.id || '未归属';
        box.append(rankRow(name, r.calls || 0, r.errors || 0, (r.input || 0) + (r.output || 0) + (r.cache_read || 0) + (r.cache_write || 0), rate, r.cost || 0, pct, false));
      }
      c.el.append(box);
      return c.el;
    }

    function rankRow(name, calls, errors, tokens, cacheRate, cost, pct, total) {
      const head = h('div', { class: 'usage-row' },
        h('span', { class: 'name', style: total ? 'font-weight:600' : '' }, name),
        errors ? h('span', { class: 'usage-err' }, '失败 ' + errors) : null,
        h('span', { class: 'grow' }),
        h('span', { class: 'meta' }, calls + ' 次 · ' + fmtTokens(tokens) + ' Token'),
        h('span', { class: 'meta', style: 'margin-left:8px' }, '缓存 ' + cacheRate.toFixed(1) + '%'),
        h('span', { class: 'mono', style: 'margin-left:12px;font-variant-numeric:tabular-nums' }, money(cost)));
      const bar = h('div', { class: 'usage-bar-row' }, meter(pct, 'primary'), h('span', { class: 'v' }, pct.toFixed(0) + '%'));
      return h('div', { style: total ? 'border-bottom:1px solid var(--line);padding-bottom:4px;margin-bottom:4px' : '' }, head, bar);
    }

    function heatCard() {
      const d = state.heat || {};
      const c = card('用量日历', { chips: [chip((d.from && d.to) ? d.from + ' → ' + d.to : '近一年', 'plain')] });
      const raw = d.days || [];
      if (!raw.length) { c.el.append(empty('还没有请求记录', '发出第一个请求后，这里会按天亮起来。')); return c.el; }
      const byDay = new Map(raw.map((x) => [x.date, x]));
      const days = [];
      if (d.from && d.to) {
        for (let t = new Date(d.from + 'T00:00:00'); t <= new Date(d.to + 'T00:00:00'); t.setDate(t.getDate() + 1)) {
          const iso = t.getFullYear() + '-' + String(t.getMonth() + 1).padStart(2, '0') + '-' + String(t.getDate()).padStart(2, '0');
          days.push(byDay.get(iso) || { date: iso, calls: 0, tokens: 0, cost: 0 });
        }
      } else days.push(...raw);
      const calls = days.reduce((n, x) => n + (x.calls || 0), 0);
      const tokens = days.reduce((n, x) => n + (x.tokens || 0), 0);
      const cost = days.reduce((n, x) => n + (x.cost || 0), 0);
      const active = days.filter((x) => (x.calls || 0) > 0).length;
      const max = Math.max(1, ...days.map((x) => x.calls || 0));
      const level = (n) => (n <= 0 ? 0 : n <= max * 0.08 ? 1 : n <= max * 0.25 ? 2 : n <= max * 0.55 ? 3 : 4);
      const first = new Date(days[0].date + 'T00:00:00');
      const startPad = (first.getDay() + 6) % 7;
      const cols = [];
      let col = new Array(startPad).fill(null);
      for (const x of days) {
        col.push(x);
        if (col.length === 7) { cols.push(col); col = []; }
      }
      if (col.length) cols.push(col);
      // 热力图只有 7 行高，浮层不管往哪边放都会盖住网格或冲出卡片；
      // 改成"悬停即改写右侧汇总"，读数位置固定、数字更大、也不会遮住任何东西
      const grid = h('div', { class: 'heat-grid' });
      const dateLine = h('div', { class: 'heat-date' }, '合计');
      const bigNum = h('b', null, String(calls));
      const meta1 = h('div', { class: 'heat-meta' }, fmtTokens(tokens) + ' tokens · 活跃 ' + active + ' 天');
      const meta2 = h('div', { class: 'heat-meta' }, '成本 ' + money(cost));
      let hot = '';
      const side = h('div', { class: 'heat-side' },
        dateLine,
        h('div', { class: 'heat-big' }, bigNum, h('span', null, '次请求')),
        meta1, meta2);
      function heatReset() {
        if (!hot) return;
        hot = '';
        dateLine.textContent = '合计';
        bigNum.textContent = String(calls);
        meta1.textContent = fmtTokens(tokens) + ' tokens · 活跃 ' + active + ' 天';
        meta2.textContent = '成本 ' + money(cost);
      }
      grid.addEventListener('pointermove', (e) => {
        const cell = e.target.closest('.heat-cell');
        const d = cell && cell.dataset.date ? cell.dataset : null;
        if (!d) { heatReset(); return; }
        if (d.date === hot) return;
        hot = d.date;
        const wd = WD[(new Date(d.date + 'T00:00:00').getDay() + 6) % 7];
        dateLine.textContent = d.date.slice(5).replace('-', '/') + ' ' + wd;
        bigNum.textContent = d.calls;
        meta1.textContent = fmtTokens(Number(d.tokens) || 0) + ' tokens';
        meta2.textContent = '成本 ' + money(Number(d.cost) || 0);
      });
      grid.addEventListener('pointerleave', heatReset);
      const months = h('div', { class: 'heat-months' });
      let lastMonth = -1;
      for (const week of cols) {
        const wc = h('div', { class: 'heat-col' });
        for (let r = 0; r < 7; r++) {
          const x = week[r];
          if (!x) { wc.append(h('span', { class: 'heat-cell', style: 'visibility:hidden' })); continue; }
          wc.append(h('span', {
            class: 'heat-cell' + (level(x.calls || 0) ? ' l' + level(x.calls || 0) : ''),
            title: x.date + ' · ' + (x.calls || 0) + ' 次 · ' + fmtTokens(x.tokens || 0) + ' tokens · ' + money(x.cost || 0),
            'data-date': x.date, 'data-calls': String(x.calls || 0),
            'data-tokens': String(x.tokens || 0), 'data-cost': String(x.cost || 0),
          }));
        }
        grid.append(wc);
      }
      for (const week of cols) {
        const x = week.find(Boolean);
        const m = x ? new Date(x.date + 'T00:00:00').getMonth() : -1;
        if (m >= 0 && m !== lastMonth && (months.childElementCount === 0 || months.lastElementChild.textContent !== MONTHS[m])) {
          months.append(h('span', null, MONTHS[m]));
          lastMonth = m;
        } else months.append(h('span', null, ''));
      }
      const left = h('div', { class: 'heat-left' }, h('div', { class: 'inline', style: 'gap:6px;align-items:flex-start;flex-wrap:nowrap' },
        h('div', { style: 'display:grid;grid-template-rows:repeat(7,11px);gap:3px;font-size:10px;color:var(--faint);padding-top:15px' }, WD.slice(0, 7).map((w, i) => h('span', { style: 'line-height:11px;opacity:' + (i % 2 ? '0' : '1') }, w))),
        h('div', { style: 'display:flex;flex-direction:column;gap:3px' }, months, grid)));
      const legend = h('div', { class: 'inline', style: 'gap:4px;margin-top:4px;font-size:10px;color:var(--faint)' }, h('span', null, '少'));
      for (let i = 0; i < 5; i++) legend.append(h('span', { class: 'heat-cell' + (i ? ' l' + i : '') }));
      legend.append(h('span', null, '多'));
      side.append(legend);
      c.el.append(h('div', { class: 'heat-body' }, left, side));
      return c.el;
    }

    function kpi(k, v, d) {
      return h('section', { class: 'kpi' }, h('div', { class: 'k' }, k), h('div', { class: 'v' }, String(v)), h('div', { class: 'd' }, d));
    }
  },
};
