/* 网关与探针（自有实现）：接入信息、协议片段、渠道探测、版本与探针。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { card, chip, dotChip, btn, iconBtn, select, field, input, textarea, toggle, confirmDialog, secretDialog, secretRow, copyText, empty, errorBox, skeleton, toast, fmtDate, fmtTokens, fmtMoney, drawer } from '../components.js';

const PROTOCOLS = [
  { id: 'openai', label: 'OpenAI', path: '/v1' },
  { id: 'responses', label: 'Responses', path: '/v1' },
  { id: 'anthropic', label: 'Anthropic', path: '' },
  { id: 'gemini', label: 'Gemini', path: '/v1beta' },
];

export default {
  title: '网关与探针',
  subtitle: '监听、探针、指标与版本',

  mount(host, ctx) {
    const state = { proto: 'openai', status: { entries: [], checkedAt: 0 }, data: null, probing: false, agents: {}, agentsAt: 0 };
    host.append(skeleton(8));
    load();

    async function load() {
      try {
        const [runtime, settings, keys, clients, health, status, ver, providers] = await Promise.all([
          api.get('runtime').catch(() => ({})),
          api.get('settings').catch(() => ({})),
          api.get('caller-keys').catch(() => ({ keys: [] })),
          api.get('clients').catch(() => ({ clients: [] })),
          api.get('health').catch(() => ({})),
          api.get('channels/status').catch(() => ({ entries: [] })),
          api.version().catch(() => ({})),
          api.get('providers').catch(() => ({ providers: [] })),
        ]);
        state.data = { runtime, settings, keys: keys.keys || keys.caller_keys || [], clients: clients.clients || [], health, ver, providers: providers.providers || providers || [], gateway: providers.gateway || {} };
        state.status = status || { entries: [] };
        render();
        // 额度提醒：窗口快到期但还剩很多 / 快用完。后台取，不阻塞首屏。
        api.get('alerts').then((r) => {
          state.alerts = (r && r.alerts) || [];
          if (host.isConnected && state.alerts.length) render();
        }).catch(() => {});
        // 版本探测要扫描本机 CLI，实测 5s+；放到后台，不阻塞首屏
        api.get('agents/cli').then((r) => {
          state.agents = r.agents || {};
          state.agentsAt = r.checked_at || 0;
          if (host.isConnected) render();
        }).catch(() => {});
      } catch (e) {
        clear(host);
        host.append(errorBox('网关读取失败：' + (e.message || e), load));
      }
    }

    function baseURL() {
      const d = state.data;
      return d.runtime.public_addr || d.gateway.publicURL || location.origin;
    }
    function sampleModel() {
      const p = state.data.providers.find((x) => !x.off && (x.models || []).some((m) => m.on));
      const m = p && (p.models || []).find((x) => x.on);
      return m ? p.id + '/' + m.id : 'provider/model';
    }
    function snippet() {
      const base = baseURL() + (PROTOCOLS.find((p) => p.id === state.proto)?.path || '');
      return [
        '# OpenAI 兼容',
        '$env:OPENAI_BASE_URL="' + base + '"',
        '$env:OPENAI_API_KEY="aether"',
        '',
        'curl ' + base + '/chat/completions \\',
        '  -H "Content-Type: application/json" \\',
        '  -d \'{"model":"' + sampleModel() + '","messages":[{"role":"user","content":"hi"}]}\'',
      ].join('\n');
    }

    function render() {
      const d = state.data;
      clear(host);
      const gw = d.gateway || {};
      const lan = !!(gw.lan || d.settings.lan);
      const keyMasked = d.settings.api_key || '';

      // 额度提醒：订阅窗口"快到期但还剩很多"是最容易白扔钱的一条，
      // 放在首页最上面，比藏在用量页深处有用得多。
      const alerts = state.alerts || [];
      if (alerts.length) {
        const box = h('div', { style: 'display:flex;flex-direction:column;gap:6px;padding:10px 12px;border:1px solid var(--amber);border-radius:8px;margin-bottom:10px' });
        box.append(h('div', { class: 'inline', style: 'gap:8px' },
          h('span', { class: 'name' }, '额度提醒'),
          chip(String(alerts.length) + ' 条', 'plain'),
          h('span', { class: 'grow' }),
          btn('去用量页', { ghost: true, onClick: () => ctx.navigate('usage') })));
        for (const a of alerts.slice(0, 3)) {
          box.append(h('div', { class: 'row', style: 'gap:8px' },
            chip(a.kind === 'expiring' ? '即将重置' : (a.kind === 'exhausted' ? '已用尽' : '额度偏低'),
              a.kind === 'expiring' ? 'amber' : 'red'),
            h('span', { class: 'meta', style: 'flex:1' }, a.message || '')));
        }
        host.append(box);
      }
      const service = card('API 服务', { chips: [chip('实例网关'), dotChip(d.health.status === 'ok' ? '运行中' : '未就绪', d.health.status === 'ok' ? 'green' : 'red')] });
      service.head.append(h('span', { style: 'flex:1' }));
      service.head.append(btn('复制接入片段', { onClick: () => copyText(snippet()) }));
      const info = h('div', { class: 'api-cols' },
        h('div', { class: 'inset' },
          field('入口', h('div', { class: 'inline' }, h('span', { class: 'mono' }, baseURL()), iconBtn('copy', '复制入口', () => copyText(baseURL())))),
          field('监听范围', h('div', { class: 'inline' }, h('span', null, lan ? '局域网（需密钥）' : '仅本机'), h('span', { class: 'meta' }, lan ? '外部可达' : '外部不可达'))),
          field('密钥', keyMasked ? secretRow(keyMasked, { maskedOnly: true }) : h('div', { class: 'inline' }, h('span', { class: 'meta' }, '未设置'))),
          field('调用方密钥', h('div', { class: 'inline' }, chip(d.keys.length + ' 把'), h('span', { class: 'meta' }, '也可以在这里生成和管理'))),
        ),
        h('div', { class: 'inset' },
          h('div', { class: 'row' }, h('span', { class: 'name' }, '客户端接入'), h('span', { class: 'grow' })),
          h('div', { class: 'inline', style: 'flex-wrap:wrap;padding-bottom:8px' }, d.clients.length ? d.clients.map((c) => chip(c.name || c, 'plain')) : h('span', { class: 'meta' }, '未检测到')),
          h('div', { class: 'api-note' }, '密钥以掩码展示，需要时用「复制」取完整值；只保存在本机设置里。')));

      const ingress = card('接入片段', { chips: [chip('五种 API · 一个地址', 'plain')] });
      const tabs = h('div', { class: 'tabs', style: 'margin-bottom:10px' });
      for (const p of PROTOCOLS) tabs.append(h('button', { class: 'tab' + (state.proto === p.id ? ' on' : ''), type: 'button', onclick: () => { state.proto = p.id; render(); } }, p.label));
      const pre = h('pre', { class: 'code-block' }, snippet());
      ingress.el.append(tabs, pre);

      const probes = card('渠道探测', { chips: [chip(state.status.checkedAt ? '上次 ' + fmtDate(state.status.checkedAt, false) : '未探测', 'plain')] });
      probes.head.append(h('span', { style: 'flex:1' }));
      probes.head.append(btn(state.probing ? '探测中…' : '全部探测', { primary: true, disabled: state.probing, onClick: () => probeAll() }));
      const rowsBox = h('div');
      renderProbeRows(rowsBox);
      probes.el.append(rowsBox);

      const version = card('版本与探针');
      version.el.append(
        h('div', { class: 'row' }, h('span', { class: 'name' }, '版本'), h('span', { class: 'grow' }), h('span', { class: 'mono meta' }, d.ver.version || 'dev')),
        h('div', { class: 'row' }, h('span', { class: 'name' }, '构建时间'), h('span', { class: 'grow' }), h('span', { class: 'mono meta' }, d.ver.build_time || '—')),
        h('div', { class: 'row' }, h('span', { class: 'name' }, '存活 / 就绪'), h('span', { class: 'grow' }), btn('查看 /healthz', { onClick: () => openEndpoint('/healthz') }), btn('查看 /readyz', { onClick: () => openEndpoint('/readyz') })),
        h('div', { class: 'row' }, h('span', { class: 'name' }, '指标'), h('span', { class: 'grow' }), btn('查看 /metrics', { onClick: () => openEndpoint('/metrics') })));

      host.append(service.el, ingress.el, callerKeysCard(), identityCard(), h('div', { class: 'grid-2' }, probes.el, version.el), agentVersionCard());
      const infoWrap = h('div', { class: 'card' });
      service.el.append(info);
    }

    function identityCard() {
      const c = card('Codex 请求身份对照', { chips: [chip('官方 CLI 形状', 'plain')] });
      const box = h('div');
      c.el.append(box);
      async function load() {
        try {
          const r = await api.get('providers/codex/identity?limit=5');
          clear(box);
          const items = r.items || [];
          if (!items.length) {
            box.append(h('div', { class: 'meta' }, '还没有收到 Responses 请求；客户端发起一次对话后这里会出现身份对照。'));
            return;
          }
          box.append(h('div', { class: 'meta' }, '期望：User-Agent codex_cli_rs/<版本> · originator codex_cli_rs · session_id'));
          for (const it of items) {
            const bits = [];
            if (it.user_agent) bits.push('UA ' + it.user_agent);
            if (it.originator) bits.push('originator ' + it.originator);
            if (it.session_id) bits.push('session ' + String(it.session_id).slice(0, 8));
            const row = h('div', { class: 'row' },
              h('span', { class: 'name', style: 'flex:1' }, bits.join(' · ') || '（无身份头）'),
              it.official_like ? chip('官方形状', 'green') : chip('有差异', 'red'));
            if (it.mismatches && it.mismatches.length) row.append(h('span', { class: 'meta', style: 'margin-left:8px' }, it.mismatches.join('；')));
            box.append(row);
          }
        } catch (e) {
          clear(box);
          box.append(errorBox('读取失败：' + (e.message || e), load));
        }
      }
      c.head.append(h('span', { style: 'flex:1' }));
      c.head.append(btn('刷新', { onClick: load }));
      load();
      return c.el;
    }

    function agentVersionCard() {
      const agents = Object.values(state.agents || {});
      if (!agents.length) return h('div');
      const installed = agents.filter((a) => a.installed).length;
      const c = card('本机 Agent 版本', { chips: [chip(installed + ' / ' + agents.length + ' 已安装', 'plain'), chip(state.agentsAt ? '巡检于 ' + fmtDate(state.agentsAt) : '未巡检', 'plain')] });
      c.head.append(h('span', { style: 'flex:1' }));
      c.head.append(iconBtn('refresh', '重新巡检', async () => {
        try { await api.post('agents/cli', {}); } catch (e) { toast('巡检失败：' + (e.message || e)); }
        await load();
      }));
      for (const a of agents) {
        const row = h('div', { class: 'row' });
        row.append(h('span', { class: 'name' }, a.name || a.id));
        row.append(a.installed ? chip(a.version || '已安装', 'green') : chip('未安装', 'plain'));
        if (a.installed && a.method) row.append(chip(a.method, 'plain'));
        const meta = a.installed ? (a.path || '') : (a.install_cmd || '');
        row.append(h('span', { class: 'meta', style: 'min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap', title: meta }, meta));
        row.append(h('span', { class: 'grow' }));
        const cmd = a.installed ? a.update_cmd : a.install_cmd;
        if (cmd) row.append(btn(a.installed ? '复制升级命令' : '复制安装命令', { ghost: true, class: 'sm', onClick: () => copyText(cmd) }));
        c.el.append(row);
      }
      return c.el;
    }

    function callerKeysCard() {
      const d = state.data;
      const c = card('调用方密钥', { chips: [chip(d.keys.length + ' 把', 'plain')] });
      const name = input({ placeholder: '名称，例如 ci-bot', label: '密钥名称' });
      const rpm = input({ type: 'number', placeholder: 'RPM（可选）', label: '每分钟请求数' });
      async function makeKey() {
        const nm = name.value.trim();
        if (!nm) { toast('先填名称'); name.focus(); return; }
        const body = { name: nm };
        if (rpm.value.trim()) body.limit = { rpm: Number(rpm.value) };
        try {
          const out = await api.post('caller-keys/add-key', body);
          const secret = out.secret || out.key || out.value || '';
          if (secret) await secretDialog('调用方密钥已生成', secret);
          else toast('已生成');
          name.value = ''; rpm.value = '';
          await load();
        } catch (e) { toast('生成失败：' + (e.message || e)); }
      }
      const form = h('div', { class: 'inline', style: 'max-width:520px;margin:0 auto;width:100%' }, name, rpm, btn('生成', { primary: true, onClick: makeKey }));
      if (!d.keys.length) {
        // 空态里直接给表单：不是「告诉你没有」，而是「在这里生成第一把」
        c.el.append(empty('还没有调用方密钥', '生成一把给 CI 或其它电脑用；本机回环可免密钥。', form));
      } else {
        c.head.append(h('span', { style: 'flex:1' }), form);
      }
      for (const k of d.keys) {
        const id = k.id || k.key || k.name;
        const lim = k.limit || {};
        const limBits = [];
        if (lim.rpm) limBits.push('RPM ' + lim.rpm);
        if (lim.tokens) limBits.push(fmtTokens(lim.tokens) + ' tokens');
        if (lim.cost) limBits.push(fmtMoney(lim.cost));
        if ((k.models || []).length) limBits.push((k.models.length) + ' 个模型');
        const sw = toggle(!k.off, async (on) => {
          try { await api.post('caller-keys/' + (on ? 'on-key' : 'off-key'), { key: id }); toast(on ? '已启用' : '已停用'); await load(); }
          catch (e) { toast('切换失败：' + (e.message || e)); }
        });
        c.el.append(h('div', { class: 'row' },
          h('span', { class: 'name' }, k.name || id),
          h('span', { class: 'mono meta' }, k.masked || ''),
          h('span', { class: 'meta' }, limBits.join(' · ') || '未限额'),
          h('span', { class: 'grow' }), sw,
          iconBtn('shield', '限额与模型', () => keyPolicy(k)),
          iconBtn('refresh', '轮换密钥', async () => {
            if (!(await confirmDialog('轮换「' + (k.name || id) + '」的密钥？', '旧密钥会立刻失效。', { confirmText: '轮换' }))) return;
            try { const out = await api.post('caller-keys/rotate-key', { key: id }); const secret = out.secret || out.key || ''; if (secret) await secretDialog('调用方密钥已轮换', secret); await load(); }
            catch (e) { toast('轮换失败：' + (e.message || e)); }
          }),
          iconBtn('trash', '删除密钥', async () => {
            if (!(await confirmDialog('删除「' + (k.name || id) + '」？', '使用它的客户端会立刻无法接入。', { danger: true, confirmText: '删除' }))) return;
            try { await api.post('caller-keys/remove', { key: id }); toast('已删除'); await load(); } catch (e) { toast('删除失败：' + (e.message || e)); }
          }, { danger: true })));
      }
      return c.el;
    }

    function keyPolicy(k) {
      const id = k.id || k.key || k.name;
      const lim = k.limit || {};
      const tokens = input({ type: 'number', value: lim.tokens ? String(lim.tokens) : '', placeholder: '总 token 上限（空/0 = 不限）' });
      const cost = input({ type: 'number', value: lim.cost ? String(lim.cost) : '', placeholder: '总花费上限（美元）' });
      const rpm = input({ type: 'number', value: lim.rpm ? String(lim.rpm) : '', placeholder: '每分钟请求数' });
      const models = textarea({ value: (k.models || []).join('\n'), placeholder: '允许的模型，每行或逗号分隔；留空 = 不限制' });
      models.rows = 4;
      const used = k.used || {};
      const d = drawer('限额与模型 · ' + (k.name || id), (body) => {
        body.append(h('div', { class: 'meta' }, '已用 ' + fmtTokens(used.tokens || 0) + ' tokens · ' + fmtMoney(used.cost || 0)));
        body.append(field('Token 上限', tokens), field('花费上限（USD）', cost), field('RPM', rpm), field('模型白名单', models));
        body.append(h('div', { class: 'inline', style: 'justify-content:flex-end' },
          btn('清除限额', { ghost: true, onClick: async () => {
            try { await api.post('caller-keys/reset-limit-key', { key: id }); toast('限额已清除'); d.close(); await load(); }
            catch (e) { toast('清除失败：' + (e.message || e)); }
          } }),
          btn('取消', { onClick: () => d.close() }),
          btn('保存', { primary: true, onClick: async () => {
            const limit = { tokens: Number(tokens.value) || 0, cost: Number(cost.value) || 0, rpm: Number(rpm.value) || 0 };
            const list = models.value.split(/[\n,]+/).map((x) => x.trim()).filter(Boolean);
            try {
              await api.post('caller-keys/limit-key', { key: id, limit });
              await api.post('caller-keys/models-key', { key: id, models: list });
              d.close(); toast('策略已保存'); await load();
            } catch (e) { toast('保存失败：' + (e.message || e)); }
          } })));
      });
    }

    function renderProbeRows(box) {
      clear(box);
      const entries = state.status.entries || [];
      if (!entries.length) { box.append(empty('还没有探测结果', '点「全部探测」发一次最小请求；会消耗真实账号额度。')); return; }
      for (const e of entries) {
        box.append(h('div', { class: 'row' },
          h('span', { class: 'name' }, e.provider + (e.label ? ' · ' + e.label : '')),
          h('span', { class: 'grow' }),
          chip(e.ok ? String(e.code || 200) : (e.code ? String(e.code) : '失败'), e.ok ? 'green' : 'red'),
          h('span', { class: 'meta' }, e.ok ? e.ms + ' ms' : (e.error || '').slice(0, 48)),
          h('span', { class: 'meta' }, fmtDate(e.at, true))));
      }
    }

    async function probeAll() {
      state.probing = true;
      render();
      try {
        await api.post('channels/check', {});
        for (let i = 0; i < 40; i++) {
          await new Promise((r) => setTimeout(r, 800));
          const s = await api.get('channels/status').catch(() => null);
          if (s && !s.reading) { state.status = s; break; }
        }
        toast('探测完成');
      } catch (e) { toast('探测失败：' + (e.message || e)); }
      state.probing = false;
      render();
    }

    // 探针端点改在抽屉里看：桌面端 window.open 会单开一个没有标题栏、也没有
    // Wails 运行时的裸窗口，看完还得先关掉才能回主界面。
    function openEndpoint(path) {
      const status = h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap' });
      const out = h('pre', { class: 'code-block', style: 'max-height:52vh;overflow:auto' }, '读取中…');
      const reload = btn('重新读取', { onClick: () => read() });
      drawer('探针 · ' + path, (body, foot) => {
        body.append(status, out);
        foot.append(btn('复制', { onClick: () => copyText(out.textContent || '') }), reload);
      });
      read();

      async function read() {
        reload.disabled = true;
        status.replaceChildren(chip('读取中…', 'plain'));
        try {
          // 直接 fetch 而不是 api.get：/readyz 未就绪时也要如实展示状态码与
          // 响应体，不能当成异常吞掉。
          const res = await fetch(path, { cache: 'no-store' });
          const text = await res.text();
          const ct = res.headers.get('content-type') || '';
          const json = parseJSON(text, ct);
          out.textContent = json ? JSON.stringify(json, null, 2) : ((text || '').trim() || '（空响应）');
          // /readyz 用 200 + {"ready":false,"reasons":[...]} 表达"未就绪"，
          // 只看状态码会误报绿灯，这里并上主体里的结论。
          const degraded = !!(json && json.ready === false);
          const reason = degraded && Array.isArray(json.reasons) && json.reasons.length ? String(json.reasons[0]) : '';
          status.replaceChildren(
            chip(res.status + (res.statusText ? ' ' + res.statusText : ''), !res.ok ? 'red' : (degraded ? 'amber' : 'green')),
            reason ? chip(reason, 'plain') : null,
            h('span', { class: 'meta mono' }, path));
        } catch (e) {
          status.replaceChildren(chip('请求失败', 'red'), h('span', { class: 'meta mono' }, path));
          out.textContent = '无法读取 ' + path + '：' + ((e && e.message) || e);
        } finally {
          reload.disabled = false;
        }
      }
    }

    // JSON 端点（/healthz、/readyz）解析成对象供缩进展示；Prometheus 文本原样。
    function parseJSON(text, contentType) {
      const raw = (text || '').trim();
      if (!contentType.includes('json') && !raw.startsWith('{') && !raw.startsWith('[')) return null;
      try { return JSON.parse(raw); } catch { return null; }
    }
  },
};
