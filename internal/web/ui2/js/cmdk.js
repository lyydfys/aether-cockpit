/* 命令面板：⌘K / Ctrl+K 打开，↑↓ 选择，Enter 执行，Esc 关闭。
   只做键盘与焦点两件事，业务动作由调用方注入。 */
import { h } from './dom.js';
import { icon } from './icons.js';
import { trapFocus } from './components.js';

let closeCurrent = null;

export function paletteOpen() { return !!closeCurrent; }
export function closePalette() { if (closeCurrent) closeCurrent(); }

/**
 * @param {{label:string, hint?:string, icon?:string, keywords?:string, run:()=>void}[]} items
 */
export function openPalette(items) {
  closePalette();
  const list = h('div', { class: 'cmdk-list', role: 'listbox', 'aria-label': '命令列表' });
  const input = h('input', {
    type: 'text', role: 'combobox', 'aria-expanded': 'true', 'aria-controls': 'cmdk-list',
    'aria-autocomplete': 'list', placeholder: '搜索页面或执行动作…', spellcheck: 'false', 'aria-label': '搜索命令',
  });
  const panel = h('div', { class: 'cmdk', role: 'dialog', 'aria-modal': 'true', 'aria-label': '命令面板' },
    h('div', { class: 'cmdk-input' }, icon('search', 16), input),
    list,
    h('div', { class: 'cmdk-foot' },
      h('span', null, h('kbd', { class: 'kbd' }, '↑'), h('kbd', { class: 'kbd' }, '↓'), ' 选择'),
      h('span', null, h('kbd', { class: 'kbd' }, '↵'), ' 执行'),
      h('span', null, h('kbd', { class: 'kbd' }, 'esc'), ' 关闭')));
  const wrap = h('div', { class: 'cmdk-wrap' }, panel);

  let rows = [];
  let index = 0;

  function paint(q) {
    const needle = q.trim().toLowerCase();
    const matched = items.filter((it) => !needle
      || (it.label + ' ' + (it.keywords || '') + ' ' + (it.hint || '')).toLowerCase().includes(needle));
    rows = matched;
    index = 0;
    list.replaceChildren();
    if (!matched.length) {
      list.append(h('div', { class: 'cmdk-empty' }, '没有匹配的页面或动作'));
      return;
    }
    matched.forEach((it, i) => {
      const b = h('button', {
        class: 'cmdk-item', type: 'button', role: 'option', id: 'cmdk-' + i,
        'aria-selected': i === 0 ? 'true' : 'false',
        onmousemove: () => setIndex(i),
        onclick: () => pick(i),
      }, it.icon ? icon(it.icon, 16) : null, h('span', { class: 'lbl' }, it.label),
        it.hint ? h('span', { class: 'k' }, it.hint) : null);
      list.append(b);
    });
    list.setAttribute('id', 'cmdk-list');
    input.setAttribute('aria-activedescendant', 'cmdk-0');
  }

  function setIndex(i) {
    if (!rows.length) return;
    index = (i + rows.length) % rows.length;
    [...list.children].forEach((el, k) => el.setAttribute('aria-selected', k === index ? 'true' : 'false'));
    input.setAttribute('aria-activedescendant', 'cmdk-' + index);
    const el = list.children[index];
    if (el && el.scrollIntoView) el.scrollIntoView({ block: 'nearest' });
  }

  function pick(i) {
    const it = rows[i];
    if (!it) return;
    close();
    it.run();
  }

  const release = trapFocus(panel, input);
  const prevOverflow = document.body.style.overflow;
  document.body.style.overflow = 'hidden';

  function close() {
    if (!wrap.isConnected) return;
    release();
    document.removeEventListener('keydown', onKey, true);
    wrap.remove();
    document.body.style.overflow = prevOverflow;
    closeCurrent = null;
  }
  closeCurrent = close;

  function onKey(e) {
    if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); return; }
    if (e.key === 'ArrowDown') { e.preventDefault(); setIndex(index + 1); return; }
    if (e.key === 'ArrowUp') { e.preventDefault(); setIndex(index - 1); return; }
    if (e.key === 'Enter') { e.preventDefault(); pick(index); }
  }

  input.addEventListener('input', () => paint(input.value));
  wrap.addEventListener('mousedown', (e) => { if (e.target === wrap) close(); });
  document.addEventListener('keydown', onKey, true);

  paint('');
  document.body.append(wrap);
  requestAnimationFrame(() => input.focus());
}
