#!/usr/bin/env node
// 前端纯逻辑单测（node:test）：格式化、状态容器、hash 路由、窗口能力探测。
import test from 'node:test';
import assert from 'node:assert/strict';
import { fmtTokens, fmtMoney, fmtDate, fmtReset } from '../internal/web/ui2/js/format.js';
import { createStore } from '../internal/web/ui2/js/store.js';
import { parseHash } from '../internal/web/ui2/js/router.js';
import { hasWindowRuntime } from '../internal/web/ui2/js/win.js';
import { accountRefreshMode } from '../internal/web/ui2/js/capabilities.js';

test('fmtTokens 千位到十亿级缩写', () => {
  assert.equal(fmtTokens(0), '0');
  assert.equal(fmtTokens(999), '999');
  assert.equal(fmtTokens(1000), '1.0K');
  assert.equal(fmtTokens(31400), '31.4K');
  assert.equal(fmtTokens(1500000), '1.5M');
  assert.equal(fmtTokens(2400000000), '2.4B');
  assert.equal(fmtTokens(undefined), '0');
});

test('fmtMoney 两位小数', () => {
  assert.equal(fmtMoney(0), '$0.00');
  assert.equal(fmtMoney(3.671), '$3.67');
  assert.equal(fmtMoney('1.2'), '$1.20');
  assert.equal(fmtMoney(null), '$0.00');
});

test('fmtDate 本地时间与空值', () => {
  const unix = new Date(2026, 0, 2, 3, 4).getTime() / 1000;
  assert.equal(fmtDate(unix), '2026/01/02 03:04');
  assert.equal(fmtDate(unix, true, true), '2026/01/02 03:04:00');
  assert.equal(fmtDate(unix, false), '2026/01/02');
  assert.equal(fmtDate(0), '—');
});

test('fmtReset 接受 ISO 字符串', () => {
  const iso = new Date(2026, 9, 30, 8, 5).toISOString();
  assert.equal(fmtReset(iso), '10/30 08:05');
  assert.equal(fmtReset(''), '');
});

test('createStore 合并、函数式更新与退订', () => {
  const s = createStore({ a: 1, b: 2 });
  const seen = [];
  const off = s.subscribe((v) => seen.push(v.a));
  s.set({ a: 3 });
  s.set((v) => ({ ...v, a: v.a + 1 }));
  off();
  s.set({ a: 99 });
  assert.deepEqual(seen, [3, 4]);
  assert.equal(s.get().a, 99);
  assert.equal(s.get().b, 2);
});

test('parseHash 默认页与查询参数', () => {
  assert.equal(parseHash('').id, 'overview');
  assert.equal(parseHash('#/').id, 'overview');
  const r = parseHash('#/channels?tag=%E4%B8%BB%E5%8A%9B&x=1');
  assert.equal(r.id, 'channels');
  assert.equal(r.params.get('tag'), '主力');
  assert.equal(r.params.get('x'), '1');
  assert.equal(parseHash('#/logs').id, 'logs');
});

test('hasWindowRuntime 在无 Wails 环境下为 false', () => {
  assert.equal(hasWindowRuntime(), false);
});

test('账号刷新操作只展示渠道支持的能力', () => {
  assert.equal(accountRefreshMode({ quota: true, refresh: true }), 'quota');
  assert.equal(accountRefreshMode({ quota: false, refresh: true }), 'credentials');
  assert.equal(accountRefreshMode({ quota: false, refresh: false }), null);
  assert.equal(accountRefreshMode({ quota: false, refresh: false }, true), 'quota');
});
