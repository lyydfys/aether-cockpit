/* 自有组件：全部返回原生 DOM，行为与样式都在本仓库内。 */
import { h, clear, append } from './dom.js';
import { icon } from './icons.js';

export const chip = (text, kind) => h('span', { class: 'chip' + (kind ? ' ' + kind : '') }, text);
export const dotChip = (text, kind) => h('span', { class: 'chip' + (kind ? ' ' + kind : '') }, h('i', { class: 'dot' }), text);

export function btn(label, opts = {}) {
  const b = h('button', {
    class: 'btn' + (opts.primary ? ' primary' : '') + (opts.danger ? ' danger' : '') + (opts.ghost ? ' ghost' : '') + (opts.class ? ' ' + opts.class : ''),
    type: 'button',
    title: opts.title || '',
    disabled: opts.disabled,
    onclick: opts.onClick,
  }, opts.icon ? icon(opts.icon, 14) : null, label ? h('span', null, label) : null);
  return b;
}

export function iconBtn(name, title, onClick, opts = {}) {
  const b = h('button', {
    class: 'icon-btn' + (opts.primary ? ' primary' : '') + (opts.danger ? ' danger' : ''),
    type: 'button', title, 'aria-label': title, disabled: opts.disabled, onclick: onClick,
  }, icon(name, opts.size || 15));
  return b;
}

export function card(title, opts = {}) {
  const c = h('section', { class: 'card' + (opts.class ? ' ' + opts.class : '') });
  const head = h('div', { class: 'card-h' });
  head.append(h('b', { role: 'heading', 'aria-level': '2' }, title));
  if (opts.chips) for (const ch of opts.chips) head.append(ch);
  head.append(h('span', { class: 'grow', style: 'flex:1' }));
  if (opts.actions) for (const a of opts.actions) head.append(a);
  c.append(head);
  return { el: c, head };
}

export function meter(pct, tone) {
  return h('div', { class: 'meter' + (tone ? ' ' + tone : '') }, h('i', { style: 'width:' + Math.max(0, Math.min(100, Number(pct) || 0)) + '%' }));
}

export function field(label, control) {
  const mark = (el) => {
    if ((el.matches('input,select,textarea') || el.classList.contains('toggle')) && !el.getAttribute('aria-label') && !el.getAttribute('aria-labelledby')) {
      el.setAttribute('aria-label', label);
    }
  };
  if (control && control.matches) {
    if (control.matches('input,select,textarea,.toggle')) mark(control);
    else control.querySelectorAll('input,select,textarea,.toggle').forEach(mark);
  }
  return h('div', { class: 'field' }, h('label', null, label), control);
}

export function input(opts = {}) {
  return h('input', {
    class: 'input', type: opts.type || 'text', value: opts.value || '', placeholder: opts.placeholder || '',
    'aria-label': opts.label || opts.ariaLabel || null,
    readonly: opts.readonly, disabled: opts.disabled, oninput: opts.onInput,
  });
}

export function textarea(opts = {}) {
  return h('textarea', { class: 'textarea', placeholder: opts.placeholder || '', 'aria-label': opts.label || opts.ariaLabel || null, oninput: opts.onInput }, opts.value || '');
}

export function select(options, value, onChange, opts = {}) {
  const s = h('select', { class: 'select', disabled: opts.disabled, 'aria-label': opts.label || opts.ariaLabel || null, onchange: (e) => onChange(e.target.value) });
  for (const o of options) {
    const opt = h('option', { value: o.value, selected: String(o.value) === String(value) }, o.label);
    s.append(opt);
  }
  return s;
}

export function toggle(on, onChange, opts = {}) {
  const b = h('button', {
    class: 'toggle' + (on ? ' on' : ''), type: 'button', role: 'switch',
    'aria-checked': on ? 'true' : 'false', disabled: opts.disabled, title: opts.title || '', 'aria-label': opts.label || opts.title || null,
    // 自己维护视觉状态：弹窗里的开关没有二次渲染，不能等调用方重绘。
    onclick: () => {
      if (opts.disabled) return;
      const next = !b.classList.contains('on');
      b.classList.toggle('on', next);
      b.setAttribute('aria-checked', next ? 'true' : 'false');
      onChange(next);
    },
  }, h('i'));
  return b;
}

export function tabs(items, active, onPick) {
  const box = h('div', { class: 'tabs' });
  for (const it of items) {
    const b = h('button', { class: 'tab' + (it.id === active ? ' on' : ''), type: 'button', onclick: () => onPick(it.id) }, it.label);
    box.append(b);
  }
  return box;
}

export function empty(title, hint, action) {
  const box = h('div', { class: 'empty' }, h('b', null, title));
  if (hint) box.append(h('div', { class: 'meta' }, hint));
  if (action) box.append(h('div', { style: 'margin-top:12px' }, action));
  return box;
}

export function errorBox(message, retry) {
  const box = h('div', { class: 'error-box' }, message || '读取失败');
  if (retry) box.append(' ', h('button', { class: 'btn ghost', type: 'button', style: 'margin-left:8px;height:24px;padding:0 8px', onclick: retry }, '重试'));
  return box;
}

export function skeleton(lines = 3) {
  const box = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
  for (let i = 0; i < lines; i++) box.append(h('div', { class: 'skeleton', style: 'width:' + Math.max(28, 78 - i * 10) + '%' }));
  return box;
}

/* ---------- 播报 ---------- */
export function announce(text) {
  const el = document.getElementById('status');
  if (!el) return;
  el.textContent = '';
  requestAnimationFrame(() => { el.textContent = text; });
}

/* ---------- 全局浮层 ---------- */
let toastTimer = 0;
export function toast(message) {
  let el = document.querySelector('.toast');
  if (!el) { el = h('div', { class: 'toast', role: 'status' }); document.body.append(el); }
  el.textContent = message;
  requestAnimationFrame(() => el.classList.add('on'));
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => el.classList.remove('on'), 3200);
}

function closeMenu() { document.querySelectorAll('.menu').forEach((m) => m.remove()); }
export function menu(anchor, items) {
  closeMenu();
  const m = h('div', { class: 'menu', role: 'menu' });
  for (const it of items) {
    if (it.sep) { m.append(h('div', { class: 'sep' })); continue; }
    m.append(h('button', {
      class: (it.on ? 'on' : '') + (it.danger ? ' danger' : ''), type: 'button', role: 'menuitem',
      onclick: () => { closeMenu(); it.run(); },
    }, (it.on ? '✓ ' : '') + it.label));
  }
  document.body.append(m);
  const r = anchor.getBoundingClientRect();
  m.style.left = Math.max(8, Math.min(window.innerWidth - m.offsetWidth - 8, r.left)) + 'px';
  // 下方放不下时上翻，保证最后几项（如「删除」）永远可点。
  const below = r.bottom + 6;
  const maxTop = Math.max(8, window.innerHeight - m.offsetHeight - 8);
  m.style.top = Math.min(below, maxTop) + 'px';
  const onKey = (e) => {
    if (e.key === 'Escape') { e.preventDefault(); closeMenu(); document.removeEventListener('keydown', onKey, true); }
  };
  document.addEventListener('keydown', onKey, true);
  setTimeout(() => {
    const off = (e) => { if (!m.contains(e.target)) { closeMenu(); document.removeEventListener('pointerdown', off, true); } };
    document.addEventListener('pointerdown', off, true);
  }, 0);
  m.querySelector('button')?.focus();
  m.addEventListener('keydown', (e) => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    e.preventDefault();
    const items2 = [...m.querySelectorAll('button')];
    const i = items2.indexOf(document.activeElement);
    const next = e.key === 'ArrowDown' ? (i + 1) % items2.length : (i - 1 + items2.length) % items2.length;
    items2[next]?.focus();
  });
}

/** @returns {HTMLElement[]} */
function focusables(root) {
  return /** @type {HTMLElement[]} */ ([...root.querySelectorAll('a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])')])
    .filter((el) => el.offsetParent !== null || el === document.activeElement);
}

/* 把键盘焦点限制在浮层内，关闭时还给原来的元素。
   preferred 指定打开时优先聚焦的元素（如命令面板的搜索框），缺省聚焦第一个可输入控件。 */
/**
 * @param {HTMLElement} root
 * @param {HTMLElement} [preferred]
 */
export function trapFocus(root, preferred) {
  const prev = document.activeElement;
  const onKey = (e) => {
    if (e.key !== 'Tab') return;
    const list = focusables(root);
    if (!list.length) { e.preventDefault(); return; }
    const first = list[0], last = list[list.length - 1];
    const inside = root.contains(document.activeElement);
    if (e.shiftKey && (!inside || document.activeElement === first)) { e.preventDefault(); last.focus(); }
    else if (!e.shiftKey && (!inside || document.activeElement === last)) { e.preventDefault(); first.focus(); }
  };
  root.addEventListener('keydown', onKey);
  const firstControl = /** @type {HTMLElement|null} */ (preferred || root.querySelector('input:not([disabled]),textarea:not([disabled]),select:not([disabled])') || focusables(root)[0]);
  firstControl?.focus();
  return () => {
    root.removeEventListener('keydown', onKey);
    const prevEl = /** @type {HTMLElement|null} */ (prev);
    if (prevEl && prevEl.focus && document.contains(prevEl)) prevEl.focus();
  };
}

export function confirmDialog(title, text, opts = {}) {
  return new Promise((resolve) => {
    const wrap = h('div', { class: 'modal-wrap' });
    const close = (v) => { release(); wrap.remove(); document.removeEventListener('keydown', onKey, true); resolve(v); };
    const box = h('div', { class: 'modal', role: 'dialog', 'aria-modal': 'true' },
      h('h2', null, title), text ? h('p', null, text) : null,
      h('div', { class: 'modal-actions' },
        h('button', { class: 'btn', type: 'button', onclick: () => close(false) }, '取消'),
        h('button', { class: 'btn ' + (opts.danger ? 'danger' : 'primary'), type: 'button', onclick: () => close(true) }, opts.confirmText || '确定')));
    wrap.append(box);
    const onKey = (e) => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(false); } };
    document.addEventListener('keydown', onKey, true);
    document.body.append(wrap);
    requestAnimationFrame(() => wrap.classList.add('on'));
    const release = trapFocus(box);
    box.querySelector('.modal-actions .btn')?.focus();
  });
}

/* 输入对话框：替代原生 prompt（原生弹窗不可样式化、且会被浏览器标成站点域名） */
export function promptDialog(title, opts = {}) {
  return new Promise((resolve) => {
    const wrap = h('div', { class: 'modal-wrap' });
    const input = h('input', {
      class: 'input', type: 'text', value: opts.value || '',
      placeholder: opts.placeholder || '', 'aria-label': opts.label || title,
    });
    const finish = (v) => {
      release();
      wrap.classList.remove('on');
      document.removeEventListener('keydown', onKey, true);
      setTimeout(() => wrap.remove(), 120);
      resolve(v);
    };
    const submit = () => {
      const v = input.value.trim();
      if (!v && opts.required !== false) { input.focus(); return; }
      finish(v);
    };
    const box = h('div', { class: 'modal', role: 'dialog', 'aria-modal': 'true', 'aria-label': title },
      h('h2', null, title),
      opts.desc ? h('p', null, opts.desc) : null,
      h('div', { class: 'modal-field' }, input),
      h('div', { class: 'modal-actions' },
        h('button', { class: 'btn', type: 'button', onclick: () => finish(null) }, '取消'),
        h('button', { class: 'btn primary', type: 'button', onclick: submit }, opts.confirmText || '确定')));
    input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') { e.preventDefault(); submit(); }
    });
    wrap.append(box);
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); finish(null); }
    };
    document.addEventListener('keydown', onKey, true);
    document.body.append(wrap);
    requestAnimationFrame(() => { wrap.classList.add('on'); input.focus(); input.select(); });
    const release = trapFocus(box);
  });
}

/* 一次性密钥：居中展示、可一键复制、明确「关闭后只留末四位」 */
export function secretDialog(title, secret, opts = {}) {
  return new Promise((resolve) => {
    const wrap = h('div', { class: 'modal-wrap' });
    const finish = () => {
      release();
      wrap.classList.remove('on');
      document.removeEventListener('keydown', onKey, true);
      setTimeout(() => wrap.remove(), 120);
      resolve();
    };
    const copyBtn = h('button', {
      class: 'btn', type: 'button',
      onclick: async () => {
        const ok = await copyText(secret);
        copyBtn.textContent = ok ? '已复制' : '复制失败';
        setTimeout(() => { copyBtn.textContent = '复制密钥'; }, 1500);
      },
    }, '复制密钥');
    const box = h('div', { class: 'modal', role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': 'secret-title' },
      h('h2', { id: 'secret-title' }, title),
      h('p', null, opts.desc || '这是唯一一次显示完整密钥。关闭后界面只保留末四位，请现在保存。'),
      h('div', { class: 'secret-box' }, h('code', null, secret)),
      h('div', { class: 'secret-note' }, h('span', { class: 'dot' }), '离开本窗口后无法再次查看'),
      h('div', { class: 'modal-actions' },
        copyBtn,
        h('button', { class: 'btn primary', type: 'button', onclick: finish }, '我已保存')));
    wrap.append(box);
    const onKey = (e) => {
      if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); finish(); }
    };
    document.addEventListener('keydown', onKey, true);
    document.body.append(wrap);
    requestAnimationFrame(() => { wrap.classList.add('on'); copyBtn.focus(); });
    const release = trapFocus(box);
  });
}

/* 密钥行：掩码 → 眼睛切换明文，右侧复制；可选轮换按钮 */
// 后端为了不泄露明文，只回显打码值（如 "sk-………2026"）。
// secretRow 必须能识别这种情况：否则"眼睛"和"复制"会把掩码当成完整密钥，
// 用户点了复制拿到一串省略号，还会以为自己的密钥坏了。
const MASKED_VALUE_RE = /…|\.\.\.|\*{3,}/;

export function isMaskedSecret(v) {
  return MASKED_VALUE_RE.test(String(v || ''));
}

export function secretRow(secret, opts = {}) {
  const val = h('span', { class: 'key-val mono' });
  // 打码值：不提供"显示/复制"，只提示去设置页重新生成或轮换。
  const maskedOnly = opts.maskedOnly != null ? !!opts.maskedOnly : isMaskedSecret(secret);
  if (maskedOnly) {
    val.textContent = String(secret || '');
    val.title = '只回显打码值；完整密钥不会再次展示';
    const row = h('div', { class: 'key-row' }, val,
      h('span', { class: 'meta' }, '已设置（仅显示打码）'),
      opts.onRotate ? btn(opts.rotateLabel || '轮换', { class: 'sm', onClick: opts.onRotate }) : null);
    return row;
  }
  let shown = false;
  const mask = () => secret.slice(0, 3) + '•'.repeat(8) + secret.slice(-4);
  const eye = iconBtn('eye', '显示完整密钥', () => { shown = !shown; paint(); });
  const paint = () => {
    val.textContent = shown ? secret : mask();
    val.classList.toggle('shown', shown);
    val.title = shown ? '点击眼睛可隐藏' : '已隐藏；点眼睛查看完整密钥';
    eye.replaceChildren(icon(shown ? 'eye-off' : 'eye'));
    eye.setAttribute('aria-label', shown ? '隐藏密钥' : '显示完整密钥');
    eye.setAttribute('aria-pressed', shown ? 'true' : 'false');
  };
  const row = h('div', { class: 'key-row' }, val, eye,
    iconBtn('copy', '复制密钥', () => copyText(secret)),
    opts.onRotate ? btn(opts.rotateLabel || '轮换', { class: 'sm', onClick: opts.onRotate }) : null);
  paint();
  return row;
}

/* 右侧抽屉：返回 { el, close, open }
   buildBody(body, foot)：正文进 body（可滚动），动作按钮进 foot（沉底）。
   单选一条备注这类轻量动作传 { compact: true }，别用 560px 的整高容器装一个输入框。 */
export function drawer(title, buildBody, opts = {}) {
  const back = h('div', { class: 'backdrop' });
  const body = h('div', { class: 'drawer-body' });
  const foot = h('div', { class: 'drawer-foot' });
  const closeBtn = h('button', { class: 'icon-btn', type: 'button', 'aria-label': '关闭', onclick: () => close() }, icon('x', 16));
  const el = h('section', {
    class: 'drawer' + (opts.compact ? ' drawer--compact' : ''), role: 'dialog', 'aria-modal': 'true', 'aria-label': title,
  }, h('div', { class: 'drawer-h' }, h('b', null, title), h('span', { class: 'grow' }), closeBtn), body, foot);
  document.body.append(back, el);
  buildBody(body, foot);
  const release = trapFocus(el);
  const onKey = (e) => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); } };
  const close = () => { release(); el.classList.remove('on'); back.classList.remove('on'); document.removeEventListener('keydown', onKey, true); setTimeout(() => { el.remove(); back.remove(); }, 280); };
  document.addEventListener('keydown', onKey, true);
  back.addEventListener('click', close);
  requestAnimationFrame(() => { back.classList.add('on'); el.classList.add('on'); });
  return { el, close, body, foot };
}

export async function copyText(text) {
  try { await navigator.clipboard.writeText(text); toast('已复制'); return true; }
  catch { toast('复制失败：浏览器拒绝访问剪贴板'); return false; }
}

export { fmtTokens, fmtMoney, fmtCost, fmtDate, fmtReset } from './format.js';
