/* 趋势图：形态与交互对齐 new-api 数据看板。
 *
 *   - 多序列面积图，序列不堆叠（对应 VChart `type:'area'` + `stack:false`）；
 *     面积填充 0.08、线宽 2、单调曲线、点默认隐藏；
 *   - 图例常驻左上角，**单选**语义：点一下只看这一条，再点一下恢复全部；
 *   - 十字线 + 读数卡（日期头 + 按值降序的序列行 + 总计）；
 *   - 指针悬停与 ←/→/Home/End 键盘游标。
 *
 * 两个刻意的取舍：
 *   1. viewBox 按容器实测宽度重算，而不是把固定视框拉宽 —— 图里的刻度始终是 11px。
 *   2. 颜色全部走 CSS 类（`--sc` 变量），不写在 SVG presentation attribute 上。
 */
import { h } from './dom.js';

const NS = 'http://www.w3.org/2000/svg';
const H = 196;      // 与 views.css 的 .trend-svg height 对齐
const L = 52;       // 左轴留白
const R = 66;       // 右轴留白（总计模式的金额刻度）
const T = 14;
const B = 30;
const WD = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

/* 轴步长：峰值三等分后向上取到 1/2/5，得到 0 / 50 / 100 / 150 这种能读的格子。 */
function niceStep(v) {
  if (!(v > 0)) return 1;
  const exp = Math.floor(Math.log10(v));
  const base = 10 ** exp;
  const n = v / base;
  // 候选乘数比常见的 1/2/5 多两档：六等分时 1/2/5 太粗，
  // 数据 137 会被顶成 250 的上限、只占 55% 高度；加入 2.5 / 3 后是 150，占 91%。
  const mult = n <= 1 ? 1 : n <= 2 ? 2 : n <= 2.5 ? 2.5 : n <= 3 ? 3 : n <= 5 ? 5 : 10;
  return mult * base;
}

/* Fritsch–Carlson 单调三次插值（同 d3.curveMonotoneX / VChart 的 curveType:'monotone'）：
   曲线顺滑但不会过冲到 0 以下，适合"每天的量"这类不能为负的数据。 */
function smoothPath(pts) {
  const n = pts.length;
  if (n === 0) return '';
  if (n === 1) return `M${pts[0][0].toFixed(1)} ${pts[0][1].toFixed(1)}`;
  if (n === 2) return `M${pts[0][0].toFixed(1)} ${pts[0][1].toFixed(1)}L${pts[1][0].toFixed(1)} ${pts[1][1].toFixed(1)}`;

  const dx = [];
  const dy = [];
  const slope = [];
  for (let i = 0; i < n - 1; i++) {
    dx[i] = pts[i + 1][0] - pts[i][0];
    dy[i] = pts[i + 1][1] - pts[i][1];
    slope[i] = dx[i] === 0 ? 0 : dy[i] / dx[i];
  }
  const t = new Array(n);
  t[0] = slope[0];
  t[n - 1] = slope[n - 2];
  for (let i = 1; i < n - 1; i++) {
    t[i] = slope[i - 1] * slope[i] <= 0 ? 0 : (slope[i - 1] + slope[i]) / 2;
  }
  for (let i = 0; i < n - 1; i++) {
    if (slope[i] === 0) { t[i] = 0; t[i + 1] = 0; continue; }
    const a = t[i] / slope[i];
    const b = t[i + 1] / slope[i];
    const s = a * a + b * b;
    if (s > 9) {
      const k = 3 / Math.sqrt(s);
      t[i] = k * a * slope[i];
      t[i + 1] = k * b * slope[i];
    }
  }
  let d = `M${pts[0][0].toFixed(1)} ${pts[0][1].toFixed(1)}`;
  for (let i = 0; i < n - 1; i++) {
    const [x0, y0] = pts[i];
    const [x1, y1] = pts[i + 1];
    const hh = (x1 - x0) / 3;
    d += `C${(x0 + hh).toFixed(1)} ${(y0 + t[i] * hh).toFixed(1)} ${(x1 - hh).toFixed(1)} ${(y1 - t[i + 1] * hh).toFixed(1)} ${x1.toFixed(1)} ${y1.toFixed(1)}`;
  }
  return d;
}

function svgEl(name, attrs) {
  const e = document.createElementNS(NS, name);
  for (const k in attrs) e.setAttribute(k, String(attrs[k]));
  return e;
}

const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v));
const weekday = (iso) => {
  const d = new Date(String(iso).slice(0, 10) + 'T00:00:00');
  return Number.isNaN(d.getTime()) ? '' : WD[d.getDay()];
};

/**
 * @param {{
 *   days: any[],
 *   series: {key:string,label:string,values:number[],axis?:'left'|'right',kind?:'area'|'line'|'bars',color:string}[],
 *   mode: string,
 *   formatLeft: (v:number)=>string,
 *   formatRight: (v:number)=>string,
 *   label: string,
 *   tooltipOf: (d:any)=>{title?:string, rows:{key?:string,label:string,value:string,color?:string,on?:boolean}[]},
 * }} opts
 */
export function createTrend(opts) {
  const wrap = document.createElement('div');
  wrap.className = 'trend-wrap';

  const legend = document.createElement('div');
  legend.className = 'trend-legend';
  legend.setAttribute('role', 'group');
  legend.setAttribute('aria-label', '图例，点击只看该序列');

  const svg = svgEl('svg', { class: 'trend-svg', role: 'img', tabindex: '0' });
  const tip = document.createElement('div');
  tip.className = 'trend-tip';
  tip.setAttribute('role', 'status');
  tip.setAttribute('aria-live', 'polite');
  const emptyNote = h('div', { class: 'trend-empty' }, '该区间没有用量');
  // 图例在底部居中：VChart 的 legends 默认位置，消耗分布页就是这样
  wrap.append(svg, legend, tip, emptyNote);

  /** 单选隔离：null = 全部；否则只看这一条（对齐 VChart legends.selectMode:'single'） */
  let isolated = null;
  let W = 760;
  let active = -1;
  let geo = null;

  const visible = () => (isolated ? opts.series.filter((s) => s.key === isolated) : opts.series);

  function paintLegend() {
    legend.replaceChildren(...opts.series.map((s) => h('button', {
      class: 'trend-lg' + (isolated && isolated !== s.key ? ' off' : ''),
      type: 'button',
      'aria-pressed': isolated === s.key ? 'true' : 'false',
      title: isolated === s.key ? '再点一次看全部' : '只看 ' + s.label,
      onclick: () => {
        isolated = isolated === s.key ? null : s.key;
        paintLegend();
        paint();
      },
    }, h('i', { class: 'trend-lg-dot ' + s.color }), h('span', null, s.label))));
  }

  function bounds() {
    const vis = visible();
    let leftMax = 0;
    let rightMax = 0;
    const stackedBars = vis.filter((s) => s.kind === 'bars').length > 1;
    if (stackedBars) {
      // 堆叠柱：轴上限取"每天各段之和"的峰值
      for (let i = 0; i < opts.days.length; i++) {
        let sum = 0;
        for (const s of vis) if (s.kind === 'bars') sum += s.values[i] || 0;
        leftMax = Math.max(leftMax, sum);
      }
      for (const s of vis) {
        if (s.kind === 'bars') continue;
        const m = Math.max(0, ...s.values);
        if (s.axis === 'right') rightMax = Math.max(rightMax, m);
        else leftMax = Math.max(leftMax, m);
      }
    } else {
      for (const s of vis) {
        const m = Math.max(0, ...s.values);
        if (s.axis === 'right') rightMax = Math.max(rightMax, m);
        else leftMax = Math.max(leftMax, m);
      }
    }
    // 参考图的纵轴是「0 + 5 档」共 6 条线，这里照做
    const lStep = niceStep(leftMax / 5);
    const rStep = niceStep(rightMax / 5);
    const hasRight = vis.some((s) => s.axis === 'right');
    return { lStep, rStep, lTop: lStep * 5, rTop: rStep * 5, hasRight, empty: leftMax <= 0 && rightMax <= 0 };
  }

  function layout() {
    const b = bounds();
    const count = opts.days.length;
    const rawStep = (W - L - R) / Math.max(1, count - 1);
    const bw = clamp(rawStep * 0.55, 6, 44);
    // 有柱子时两侧各让半个柱宽：否则第一根柱子会压住左轴的 "0"
    const hasBars = visible().some((s) => s.kind === 'bars');
    const left = hasBars ? L + bw / 2 : L;
    const right = hasBars ? W - R - bw / 2 : W - R;
    const plotW = Math.max(40, right - left);
    const plotH = H - T - B;
    const stepX = plotW / Math.max(1, count - 1);
    const x = count === 1 ? () => (left + right) / 2 : (i) => left + stepX * i;
    return {
      ...b, bw, left, right, plotW, plotH, stepX, x,
      yLeft: (v) => T + plotH * (1 - v / (b.lTop || 1)),
      yRight: (v) => T + plotH * (1 - v / (b.rTop || 1)),
    };
  }

  function xLabels(g) {
    const maxLabels = Math.max(2, Math.floor(g.plotW / 74));
    const stride = Math.max(1, Math.ceil(opts.days.length / maxLabels));
    const idx = [];
    for (let i = 0; i < opts.days.length; i += stride) idx.push(i);
    if (idx[idx.length - 1] !== opts.days.length - 1) idx.push(opts.days.length - 1);
    return idx.map((i) => {
      const d = opts.days[i] || {};
      const label = d.label || String(d.time || '').slice(5);
      return `<text x="${g.x(i).toFixed(1)}" y="${H - 9}" text-anchor="middle">${label}</text>`;
    }).join('');
  }

  function seriesMarks(g) {
    const vis = visible();
    if (!vis.length) return '';
    const out = [];

    // 柱：多条时按天累加成堆叠柱（对应 VChart 柱状 spec 的 stack:true）
    const bars = vis.filter((s) => s.kind === 'bars');
    if (bars.length) {
      const acc = new Array(opts.days.length).fill(0);
      for (const s of bars) {
        s.values.forEach((raw, i) => {
          const v = raw || 0;
          if (v <= 0) return;
          const low = g.yLeft(acc[i]);
          const high = g.yLeft(acc[i] + v);
          acc[i] += v;
          out.push(`<rect class="bar ${s.color}" x="${(g.x(i) - g.bw / 2).toFixed(1)}" y="${high.toFixed(1)}" width="${g.bw.toFixed(1)}" height="${Math.max(1, low - high).toFixed(1)}"/>`);
        });
      }
    }

    for (const s of vis) {
      if (s.kind === 'bars') continue;
      const toY = s.axis === 'right' ? g.yRight : g.yLeft;
      const pts = s.values.map((v, i) => [g.x(i), toY(v)]);
      const line = smoothPath(pts);
      if (s.kind === 'area') {
        const area = `${line} L ${pts[pts.length - 1][0].toFixed(1)} ${H - B} L ${pts[0][0].toFixed(1)} ${H - B} Z`;
        out.push(`<path class="area ${s.color}" d="${area}"/>`);
      }
      out.push(`<path class="line ${s.color}" d="${line}"/>`);
      // 点多线密时逐点画会糊成一片：只在末点留一个标记（对应 point.visible:false）
      const last = pts[pts.length - 1];
      out.push(`<circle class="dot-end ${s.color}" cx="${last[0].toFixed(1)}" cy="${last[1].toFixed(1)}" r="3"/>`);
    }
    return out.join('');
  }

  function paint() {
    geo = layout();
    const g = geo;
    const leftTicks = [0, 1, 2, 3, 4, 5].map((k) => {
      const v = g.lStep * k;
      const yy = g.yLeft(v);
      return `<line class="grid" x1="${L}" x2="${W - R}" y1="${yy.toFixed(1)}" y2="${yy.toFixed(1)}"/>`
        + `<text class="tick" x="${L - 12}" y="${(yy + 4).toFixed(1)}" text-anchor="end">${g.empty && k === 0 ? '0' : opts.formatLeft(v)}</text>`;
    }).join('');
    const rightTicks = g.hasRight
      ? [0, 1, 2, 3, 4, 5].map((k) => {
        const v = g.rStep * k;
        const yy = g.yRight(v);
        return `<text class="tick tick-r" x="${W - R + 12}" y="${(yy + 4).toFixed(1)}">${opts.formatRight(v)}</text>`;
      }).join('')
      : '';

    svg.setAttribute('viewBox', `0 0 ${W} ${H}`);
    svg.setAttribute('aria-label', opts.label);
    svg.innerHTML = `
      <defs>
        <linearGradient id="trend-fill" x1="0" y1="0" x2="0" y2="1">
          <stop class="fill-a" offset="0"/><stop class="fill-b" offset="1"/>
        </linearGradient>
      </defs>
      ${leftTicks}
      <line class="axis" x1="${L}" x2="${W - R}" y1="${H - B}" y2="${H - B}"/>
      <g class="marks">${seriesMarks(g)}</g>
      ${rightTicks}
      <line class="guide" x1="0" x2="0" y1="${T}" y2="${H - B}"/>
      <circle class="cursor" r="4.5" cx="-99" cy="-99"/>
      <rect class="hit" x="${L - 6}" y="${T}" width="${(W - R) - (L - 6)}" height="${g.plotH}" />
      ${xLabels(g)}`;
    emptyNote.hidden = !g.empty;
    paintActive();
  }

  function paintActive() {
    if (!geo) return;
    const guide = svg.querySelector('.guide');
    const cur = svg.querySelector('.cursor');
    const g = geo;
    if (active < 0 || !opts.days[active]) {
      guide.style.opacity = '0';
      cur.style.opacity = '0';
      tip.classList.remove('on');
      return;
    }
    const day = opts.days[active];
    const px = g.x(active);
    guide.setAttribute('x1', px.toFixed(1));
    guide.setAttribute('x2', px.toFixed(1));
    guide.style.opacity = '1';

    const vis = visible();
    const keys = new Set(vis.map((s) => s.key));
    const data = opts.tooltipOf(day) || { rows: [] };
    const rows = (data.rows || []).filter((r) => !r.key || keys.has(r.key));

    // 游标落在最上面那条序列的当前值上（与 new-api 的 dimension 指示一致）
    const top = vis.find((s) => s.kind !== 'bars') || vis[0];
    const py = top
      ? (top.axis === 'right' ? g.yRight(top.values[active]) : g.yLeft(top.values[active]))
      : T + g.plotH / 2;
    cur.setAttribute('cx', px.toFixed(1));
    cur.setAttribute('cy', py.toFixed(1));
    cur.style.opacity = '1';

    const title = data.title || (day.label || (String(day.time || '').slice(5) + ' ' + weekday(day.time)).trim());
    tip.replaceChildren(
      h('div', { class: 'tt-head' }, title),
      ...rows.map((r) => h('div', { class: 'tt-row' + (r.on ? ' on' : '') },
        // 没有颜色的行（总计、Token）给一个占位，保持文字左对齐，但不假冒序列
        h('i', { class: r.color ? 'tt-dot ' + r.color : 'tt-dot is-blank' }),
        h('span', { class: 'k' }, r.label),
        h('span', { class: 'v' }, r.value))),
    );
    tip.classList.add('on');
    const tw = tip.offsetWidth || 148;
    const th = tip.offsetHeight || 84;
    const pad = 8;
    let tx = px + 14;
    let anchor = 'translate(0,-50%)';
    if (tx + tw > W - pad) {
      tx = px - 14;
      anchor = 'translate(-100%,-50%)';
      if (tx - tw < pad) {
        tx = Math.min(W - pad - tw, Math.max(pad, px + 14));
        anchor = 'translate(0,-50%)';
      }
    }
    tip.style.transform = anchor;
    tip.style.left = tx.toFixed(1) + 'px';
    tip.style.top = clamp(py, T + th / 2, H - B - th / 2).toFixed(1) + 'px';

    svg.setAttribute('aria-label', `${opts.label}；当前 ${title}，${rows.map((r) => r.label + ' ' + r.value).join('，')}`);
  }

  function nearest(px) {
    if (!geo) return -1;
    return clamp(Math.round((px - geo.left) / (geo.stepX || 1)), 0, opts.days.length - 1);
  }

  function setActive(i) {
    if (i === active) return;
    active = i;
    paintActive();
  }

  svg.addEventListener('pointermove', (e) => {
    const r = svg.getBoundingClientRect();
    if (!r.width) return;
    setActive(nearest((e.clientX - r.left) * (W / r.width)));
  });
  svg.addEventListener('pointerleave', () => {
    if (document.activeElement !== svg) setActive(-1);
  });

  svg.addEventListener('focus', () => { if (active < 0) setActive(opts.days.length - 1); });
  svg.addEventListener('blur', () => setActive(-1));
  svg.addEventListener('keydown', (e) => {
    const n = opts.days.length;
    if (e.key === 'ArrowRight' || e.key === 'ArrowLeft') {
      e.preventDefault();
      setActive(clamp((active < 0 ? n - 1 : active) + (e.key === 'ArrowRight' ? 1 : -1), 0, n - 1));
    } else if (e.key === 'Home') { e.preventDefault(); setActive(0); }
    else if (e.key === 'End') { e.preventDefault(); setActive(n - 1); }
    else if (e.key === 'Escape' && active >= 0) { e.preventDefault(); setActive(-1); }
  });

  paintLegend();
  paint();

  return {
    el: wrap,
    resize(w) {
      if (w > 240 && Math.abs(w - W) > 4) { W = w; paint(); }
    },
  };
}
