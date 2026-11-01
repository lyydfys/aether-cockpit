/* 2FA 管理：本机 TOTP 验证码（对标 local-vault）。密钥加密落盘，页面只见验证码。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { btn, chip, drawer, empty, errorBox, field, input, select, skeleton, toast, copyText, confirmDialog, iconBtn } from '../components.js';

export default {
  title: '2FA 管理',
  subtitle: '本机 TOTP 验证码，密钥加密存储',

  mount(host) {
    host.append(skeleton(4));
    let entries = [];
    let fetchedAt = 0;
    let timer = 0;

    load();

    async function load() {
      try {
        const r = await api.get('2fa/codes');
        entries = r.entries || [];
        fetchedAt = Date.now();
        render();
        startTimer();
      } catch (e) {
        clear(host);
        host.append(errorBox('读取 2FA 库失败：' + (e.message || e), load));
      }
    }

    function startTimer() {
      if (timer) clearInterval(timer);
      timer = setInterval(() => {
        if (!host.isConnected) { clearInterval(timer); timer = 0; return; }
        const elapsed = Math.floor((Date.now() - fetchedAt) / 1000);
        let stale = false;
        for (const e of entries) {
          if (e.error || !e.code) continue;
          const remain = Math.max(0, (e.remaining || 0) - elapsed);
          const codeEl = host.querySelector('[data-code="' + e.id + '"]');
          const remainEl = host.querySelector('[data-remain="' + e.id + '"]');
          const barEl = host.querySelector('[data-bar="' + e.id + '"]');
          if (codeEl) codeEl.textContent = e.code;
          if (remainEl) remainEl.textContent = remain + 's';
          if (barEl) barEl.style.width = Math.max(0, Math.min(100, (remain / (e.period || 30)) * 100)) + '%';
          if (remain <= 0) stale = true;
        }
        if (stale) load();
      }, 1000);
    }

    function render() {
      clear(host);
      const bar = h('div', { class: 'batchbar' },
        chip(entries.length + ' 个条目', 'plain'),
        h('span', { class: 'meta' }, '验证码只在本机计算；列表接口不回传密钥。'),
        h('span', { class: 'grow' }),
        btn('添加 2FA', { primary: true, icon: 'plus', onClick: () => addDialog() }));
      host.append(bar);
      if (!entries.length) {
        host.append(empty('还没有 2FA 条目', '把平台的 TOTP 密钥加进来，验证码会在这里自动刷新。', btn('添加第一个', { primary: true, icon: 'plus', onClick: () => addDialog() })));
        return;
      }
      const grid = h('div', { class: 'grid-3' });
      for (const e of entries) grid.append(entryCard(e));
      host.append(grid);
    }

    function entryCard(e) {
      const title = e.issuer || e.account || '未命名平台';
      const card = h('section', { class: 'card', style: 'display:flex;flex-direction:column;gap:10px' });
      const head = h('div', { class: 'acct-head' },
        h('span', { class: 'brand-tile', style: 'width:30px;height:30px;box-shadow:none;background:var(--accent-soft)' },
          h('span', { style: 'color:var(--accent-strong);font-weight:700;font-size:13px' }, String(title).slice(0, 1))),
        h('span', { class: 'name' }, title),
        e.account ? chip(e.account, 'plain') : null,
        h('span', { class: 'grow' }),
        chip((e.algorithm || 'SHA1') + ' · ' + (e.period || 30) + 's', 'plain'));
      card.append(head);
      if (e.error) {
        card.append(h('div', { class: 'mono', style: 'color:var(--red);font-size:12px' }, e.error));
      } else {
        const code = h('div', { class: 'mono', 'data-code': e.id, style: 'font-size:30px;letter-spacing:.18em;font-variant-numeric:tabular-nums' }, e.code || '------');
        const remain = h('span', { class: 'meta', 'data-remain': e.id }, (e.remaining || 0) + 's');
        const barInner = h('i', { 'data-bar': e.id, style: 'display:block;height:100%;width:' + Math.round(((e.remaining || 0) / (e.period || 30)) * 100) + '%;background:var(--accent);transition:width .9s linear;border-radius:99px' });
        card.append(
          h('div', { class: 'inline' }, code, btn('复制', { ghost: true, icon: 'copy', onClick: () => copyText(e.code || '') })),
          h('div', { class: 'inline' }, h('div', { style: 'flex:1;height:5px;border-radius:99px;background:var(--surface-2);overflow:hidden' }, barInner), remain));
      }
      if (e.note) card.append(h('div', { class: 'acct-note' }, e.note));
      const foot = h('div', { class: 'acct-foot' }, h('span', { class: 'meta' }, '密钥尾号 ' + (e.secret_tail || '••••')),
        h('span', { class: 'grow' }),
        iconBtn('trash', '删除', async () => {
          if (!(await confirmDialog('删除「' + title + '」？', '只删本机记录，平台上的 2FA 不受影响。', { danger: true, confirmText: '删除' }))) return;
          try { await api.del('2fa/' + encodeURIComponent(e.id)); toast('已删除'); await load(); }
          catch (err) { toast('删除失败：' + (err.message || err)); }
        }, { danger: true }));
      card.append(foot);
      return card;
    }

    function addDialog() {
      const issuer = input({ placeholder: '例如 GitHub', label: '平台' });
      const account = input({ placeholder: '例如 dev@example.com', label: '账号' });
      const secret = input({ type: 'password', placeholder: 'TOTP 密钥（Base32）', label: '密钥' });
      const digits = select([{ value: '6', label: '6 位' }, { value: '8', label: '8 位' }], '6', () => {}, { label: '位数' });
      const period = select([{ value: '30', label: '30 秒' }, { value: '60', label: '60 秒' }], '30', () => {}, { label: '周期' });
      const algorithm = select([{ value: 'SHA1', label: 'SHA1' }, { value: 'SHA256', label: 'SHA256' }, { value: 'SHA512', label: 'SHA512' }], 'SHA1', () => {}, { label: '算法' });
      const note = input({ placeholder: '备注（可选）', label: '备注' });
      const d = drawer('添加 2FA', (body) => {
        body.append(
          field('平台', issuer),
          field('账号', account),
          field('密钥 *', secret),
          h('div', { class: 'inline', style: 'gap:10px;align-items:flex-start' }, field('位数', digits), field('周期', period), field('算法', algorithm)),
          field('备注', note),
          h('div', { class: 'meta' }, '密钥只在本机加密保存；支持 base32，可带空格。'),
          h('div', { class: 'inline', style: 'justify-content:flex-end' },
            btn('取消', { onClick: () => d.close() }),
            btn('保存', { primary: true, onClick: async () => {
              if (!secret.value.trim()) { toast('请填写密钥'); return; }
              try {
                await api.post('2fa', {
                  issuer: issuer.value.trim(), account: account.value.trim(), secret: secret.value.trim(),
                  digits: Number(digits.value), period: Number(period.value), algorithm: algorithm.value, note: note.value.trim(),
                });
                d.close(); toast('2FA 条目已添加'); await load();
              } catch (e) { toast('保存失败：' + (e.message || e)); }
            } })),
        );
      });
    }
  },
};
