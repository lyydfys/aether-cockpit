/* 自有的极简 DOM 构造器：不依赖任何框架，也不需要模板编译。 */
import { translateText, translateAttr } from './i18n.js';

const RAW_TAGS = new Set(['CODE', 'PRE', 'TEXTAREA', 'SCRIPT', 'STYLE']);

const SVG_NS = 'http://www.w3.org/2000/svg';

export function h(tag, props, ...children) {
  const el = document.createElement(tag);
  if (props) {
    for (const [k, v] of Object.entries(props)) {
      if (v == null || v === false) continue;
      if (k === 'class' || k === 'className') el.className = v;
      else if (k === 'style') {
        if (typeof v === 'string') el.style.cssText = v;
        else for (const [sk, sv] of Object.entries(v)) if (sv != null) el.style.setProperty(sk, String(sv));
      } else if (k === 'dataset') Object.assign(el.dataset, v);
      else if (k === 'text') el.textContent = RAW_TAGS.has(el.tagName) ? String(v) : translateText(v);
      else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2).toLowerCase(), v);
      else if (k === 'ref' && typeof v === 'function') v(el);
      else if (k === 'value') el.value = v;
      else if (k === 'checked' || k === 'disabled' || k === 'readonly' || k === 'selected') el[k] = !!v;
      else el.setAttribute(k, translateAttr(k, v === true ? '' : String(v), tag));
    }
  }
  append(el, children);
  return el;
}

export function append(el, children) {
  const raw = RAW_TAGS.has(el.tagName);
  for (const child of children.flat(Infinity)) {
    if (child == null || child === false || child === true) continue;
    el.append(child instanceof Node ? child : document.createTextNode(raw ? String(child) : translateText(child)));
  }
  return el;
}

export function frag(...children) {
  const f = document.createDocumentFragment();
  append(f, children);
  return f;
}

export function clear(el) { while (el.firstChild) el.removeChild(el.firstChild); return el; }

/* 运行时改文案走这里：与 h() 使用同一套翻译，避免绕过 i18n */
export function setText(el, value) {
  if (!el) return el;
  el.textContent = translateText(value);
  return el;
}

export function svg(paths, size = 16, viewBox = '0 0 20 20', stroke = 1.5) {
  const s = document.createElementNS(SVG_NS, 'svg');
  s.setAttribute('class', 'ico');
  s.setAttribute('viewBox', viewBox);
  s.setAttribute('width', String(size));
  s.setAttribute('height', String(size));
  s.setAttribute('fill', 'none');
  s.setAttribute('stroke', 'currentColor');
  s.setAttribute('stroke-width', String(stroke));
  s.setAttribute('stroke-linecap', 'round');
  s.setAttribute('stroke-linejoin', 'round');
  s.setAttribute('aria-hidden', 'true');
  s.innerHTML = paths;
  return s;
}

export const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
