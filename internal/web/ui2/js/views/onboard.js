/* 接入渠道（自有实现）：多平台、多登录方式。
   方式来自渠道 Manifest 的 supported_auths：本机导入 / OAuth PKCE / 设备码 / 手动凭据。
   每个动作都走真实接口，不伪造进度。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { icon } from '../icons.js';
import { chip, btn, iconBtn, drawer, field, input, select, textarea, toggle, toast, skeleton, empty, errorBox, fmtDate, copyText } from '../components.js';
import { brandTile } from '../brandicons.js';
import { openInBrowser } from '../links.js';

const METHOD_LABEL = {
  local_import: '本机导入',
  oauth_pkce: '浏览器授权',
  device_code: '设备码',
  manual_input: '手动凭据',
  webview_login: '浏览器登录',
  minimax_device: 'MiniMax 设备码',
  qoder_device: 'Qoder 登录',
  workbuddy_device: 'WorkBuddy 登录',
  codebuddy_device: 'CodeBuddy 登录',
  zed_device: 'Zed 登录',
  kiro_device: 'Kiro 登录',
  trae_device: 'Trae 登录',
  windsurf_login: 'Windsurf 登录',
  token_json: 'Token / JSON',
};
const isTest = (id) => /^mock/.test(id);
const FAMILY_LABEL = {
  claude: 'Claude', codex: 'Codex', gemini: 'Gemini', google: 'Google', antigravity: 'Antigravity',
  qoder: 'Qoder', trae: 'Trae', kiro: 'Kiro', workbuddy: 'WorkBuddy', minimax: 'MiniMax',
  opencode: 'OpenCode', azure: 'Azure', vertex: 'Vertex', typesafe: 'TypeSafe', generic: '其他',
  xai: 'xAI / Grok', deepseek: 'DeepSeek', moonshot: 'Kimi', zhipu: 'GLM', groq: 'Groq', mistral: 'Mistral',
  cline: 'Cline', zai: 'Z.ai', volcengine: '火山引擎', tencent: '腾讯云', bedrock: 'Bedrock', openai: 'OpenAI',
  kimi: 'Kimi', qwen: 'Qwen', baichuan: '百川', yi: 'Yi', siliconflow: 'SiliconFlow',
};
const familyLabel = (f) => FAMILY_LABEL[f] || f || '其他';
const hasLogin = (p) => (p.supported_auths || []).some((m) => m !== 'manual_input') || /^minimax-code/.test(p.id);

export default {
  title: '接入渠道',
  subtitle: '多平台登录：本机导入、浏览器授权、设备码、手动凭据',

  mount(host) {
    const state = { providers: [], accounts: [], found: {}, q: '', group: 'all', family: 'all', busy: false };
    let paintGrid = () => {};
    host.append(skeleton(8));
    load();

    async function load() {
      try {
        const [pv, av] = await Promise.all([api.get('providers?include_empty=1'), api.get('accounts')]);
        state.providers = (pv.providers || pv || []).filter((p) => !isTest(p.id)).sort((a, b) => {
          const rank = (p) => ((p.supported_auths || []).some((m) => m !== 'manual_input') ? 0 : 1);
          return rank(a) - rank(b) || String(a.display_name).localeCompare(String(b.display_name));
        });
        state.accounts = av.accounts || [];
        render();
      } catch (e) {
        clear(host);
        host.append(errorBox('渠道读取失败：' + (e.message || e), load));
      }
    }

    async function discover() {
      if (state.busy) return;
      state.busy = true;
      const b = host.querySelector('[data-act="scan"]');
      if (b) { b.disabled = true; b.textContent = '扫描中…'; }
      try {
        const list = await api.get('importapps');
        const found = {};
        for (const s of list || []) {
          if (s.found) found[s.id] = (s.items || []).filter((x) => !x.skip).length || 1;
        }
        state.found = found;
        toast(Object.keys(found).length ? '发现 ' + Object.keys(found).length + ' 个平台的本机登录态' : '没有扫描到本机登录态');
        paintGrid();
      } catch (e) { toast('扫描失败：' + (e.message || e)); }
      finally { state.busy = false; }
    }

    function almaImport() {
      const d = drawer('从 Alma 导入', (body) => {
        const status = h('div', { class: 'meta' }, '读取 Alma…');
        const box = h('div', { class: 'list', style: 'gap:6px' });
        const picked = new Set();
        const actions = h('div', { class: 'inline', style: 'justify-content:flex-end' },
          btn('关闭', { onClick: () => d.close() }),
          btn('导入选中', { primary: true, onClick: async () => {
            if (!picked.size) { toast('没有勾选'); return; }
            try {
              const r = await api.post('import/alma', { ids: [...picked] });
              toast('已导入 ' + (r.count || 0) + ' 个账号');
              d.close();
              await load();
            } catch (e) { toast('导入失败：' + (e.message || e)); }
          } }));
        body.append(status, box, actions);
        api.get('import/alma').then((r) => {
          status.textContent = r.available ? ('Alma 在 ' + r.api + ' 运行；只导入 type=openai 且启用的 provider。') : 'Alma 未运行（打开 Alma 后重试）。';
          clear(box);
          for (const e of r.entries || []) {
            const ck = h('input', { type: 'checkbox', checked: !e.skip, disabled: !!e.skip });
            if (!e.skip) picked.add(e.id);
            ck.addEventListener('change', () => { if (ck.checked) picked.add(e.id); else picked.delete(e.id); });
            box.append(h('label', { class: 'row', style: 'cursor:pointer;gap:8px' }, ck,
              h('span', { class: 'name', style: 'flex:1' }, e.label || e.name || e.id),
              h('span', { class: 'meta' }, e.base_url || ''),
              e.skip ? chip(e.skip, 'plain') : chip('可导入', 'green')));
          }
        }).catch((e) => { status.textContent = '读取失败：' + (e.message || e); });
      });
    }

    function countOf(pid) {
      const list = state.accounts.filter((a) => a.provider_id === pid);
      return { total: list.length, ready: list.filter((a) => a.state === 'ready').length };
    }

    function render() {
      clear(host);
      const bar = h('div', { class: 'inline', style: 'margin-bottom:12px;flex-wrap:wrap' },
        h('label', { class: 'search', style: 'flex:0 1 320px' }, icon('search', 16), h('input', { type: 'search', placeholder: '搜索平台 / 渠道…', value: state.q, oninput: (e) => { state.q = e.target.value; paintGrid(); } })),
        h('span', { class: 'grow', style: 'flex:1' }),
        btn('扫描本机登录态', { icon: 'folder', onClick: discover, class: 'scan' }),
        btn('从 Alma 导入', { icon: 'link', onClick: almaImport }),
        iconBtn('refresh', '刷新渠道', load));
      bar.querySelector('.scan')?.setAttribute('data-act', 'scan');
      host.append(bar);
      const loginCount = state.providers.filter(hasLogin).length;
      const groupTabs = h('div', { class: 'tabs', style: 'margin-bottom:8px;flex-wrap:wrap' },
        ...[['all', '全部 ' + state.providers.length], ['login', '登录态 ' + loginCount], ['api', 'API 平台 ' + (state.providers.length - loginCount)]].map(([g, label]) =>
          h('button', { class: 'tab' + (state.group === g ? ' on' : ''), type: 'button', 'data-group': g, onclick: () => pickGroup(g) }, label)));
      host.append(groupTabs);
      const famRow = h('div', { class: 'tabs', style: 'margin-bottom:12px;flex-wrap:wrap' });
      host.append(famRow);
      function loginFams() {
        const set = new Map();
        for (const p of state.providers.filter(hasLogin)) {
          const f = p.family || 'generic';
          set.set(f, (set.get(f) || 0) + 1);
        }
        return [...set.entries()].sort((a, b) => familyLabel(a[0]).localeCompare(familyLabel(b[0])));
      }
      function paintFams() {
        clear(famRow);
        if (state.group === 'api') return;
        const fams = loginFams();
        const total = fams.reduce((n, x) => n + x[1], 0);
        famRow.append(h('button', { class: 'tab' + (state.family === 'all' ? ' on' : ''), type: 'button', onclick: () => pickFamily('all') }, '全部登录态 ' + total));
        for (const [f, n] of fams) {
          famRow.append(h('button', { class: 'tab' + (state.family === f ? ' on' : ''), type: 'button', 'data-family': f, onclick: () => pickFamily(f) }, familyLabel(f) + ' ' + n));
        }
      }
      function pickGroup(g) {
        state.group = g;
        state.family = 'all';
        [...groupTabs.children].forEach((b) => b.classList.toggle('on', b.dataset.group === g));
        paintFams();
        paintGrid();
      }
      function pickFamily(f) {
        state.family = f;
        [...famRow.children].forEach((b) => b.classList.toggle('on', (b.dataset.family || 'all') === f));
        paintGrid();
      }
      paintFams();
      const grid = h('div', { class: 'grid-3' });
      host.append(grid);
      paintGrid = () => {
        clear(grid);
        const q = state.q.trim().toLowerCase();
        const list = state.providers.filter((p) => {
          if (state.group === 'login' && !hasLogin(p)) return false;
          if (state.group === 'api' && hasLogin(p)) return false;
          if (state.group !== 'api' && state.family !== 'all' && (p.family || 'generic') !== state.family) return false;
          return !q || [p.display_name, p.family, p.id, p.description].join(' ').toLowerCase().includes(q);
        });
        if (!list.length) { grid.append(empty('没有匹配的渠道', '换个关键词试试。')); return; }
        for (const p of list) {
        const c = countOf(p.id);
        const methods = (p.supported_auths || []).map((m) => METHOD_LABEL[m]).filter(Boolean);
        const card = h('section', { class: 'card', style: 'display:flex;flex-direction:column;gap:10px' },
          h('div', { class: 'acct-head' },
            brandTile(p, 30),
            h('span', { class: 'name' }, p.display_name || p.id),
            p.region_tag ? chip(p.region_tag, 'plain') : null,
            h('span', { class: 'grow', style: 'flex:1' }),
            c.total ? chip('已接入 ' + c.total, c.ready ? 'green' : 'amber') : chip('未接入', 'plain')),
          h('div', { class: 'meta', style: 'min-height:36px;line-height:1.5' }, (p.description || '').slice(0, 90) || '—'),
          h('div', { class: 'inline', style: 'gap:6px' },
            ...methods.map((m) => chip(m, 'plain')),
            state.found[p.id] ? chip('本机 ' + state.found[p.id] + ' 个可导入', 'green') : null),
          h('div', { class: 'inline', style: 'justify-content:flex-end;margin-top:auto' },
            btn(c.total ? '再加一个' : '接入', { primary: true, icon: 'plus', onClick: () => openProvider(p, load) })));
          grid.append(card);
        }
      };
      paintGrid();
      host.append(h('div', { class: 'meta', style: 'margin-top:12px' }, '接入方式与字段由渠道 Manifest 声明；本机导入只读本机凭据，浏览器授权走本机回环回调，令牌不会回显到页面。'));
    }
  },
};

/* 打开某渠道的接入向导；channels 页也复用它。 */
export function openProvider(p, done) {
  const methods = (p.supported_auths || []).slice();
  if (/^minimax-code/.test(p.id)) methods.push('minimax_device');
  if (/^qoder-/.test(p.id)) methods.push('qoder_device');
  if (/^workbuddy-/.test(p.id)) methods.push('workbuddy_device');
  if (/^codebuddy-/.test(p.id)) methods.push('codebuddy_device');
  if (p.id === 'zed') methods.push('zed_device');
  if (p.id === 'kiro') methods.push('kiro_device');
  if (/^trae-/.test(p.id)) methods.push('trae_device');
  if (p.id === 'windsurf') methods.push('windsurf_login');
  if (!methods.length) methods.push('manual_input');
  let active = methods[0];
  let timer = 0;
  let session = null;
  const stop = () => { if (timer) clearInterval(timer); timer = 0; };

  const d = drawer('接入 · ' + (p.display_name || p.id), (body) => {
    const label = input({ placeholder: '账号备注名（可选），例如 主力号', label: '账号备注' });
    const tabs = h('div', { class: 'tabs' });
    const content = h('div', { style: 'display:flex;flex-direction:column;gap:10px' });
    const tokenCta = h('button', { class: 'token-json-cta', type: 'button', onclick: () => {
      stop(); active = 'token_json'; renderMethod();
    } }, icon('key', 16), h('span', null, 'Token / JSON'));
    body.append(h('div', { class: 'meta' }, p.description || ''), field('账号备注', label), tabs, tokenCta, content);
    for (const m of methods) {
      tabs.append(h('button', { class: 'tab' + (m === active ? ' on' : ''), type: 'button', onclick: () => {
        stop(); active = m; paintTabs(); renderMethod();
      } }, METHOD_LABEL[m] || m));
    }
    function paintTabs() { [...tabs.children].forEach((b, i) => b.classList.toggle('on', methods[i] === active)); }
    function finish(text) { toast(text); d.close(); done && done(); }
    function renderMethod() {
      stop(); clear(content);
      tokenCta.style.display = active === 'token_json' ? 'none' : '';
      if (active === 'token_json') renderTokenJSON();
      else if (active === 'local_import') renderLocal();
      else if (active === 'manual_input') renderManual();
      else if (active === 'oauth_pkce' || active === 'device_code') renderAuth(active);
      else if (active === 'minimax_device') renderMiniMax();
      else if (active === 'qoder_device') renderQoder();
      else if (active === 'workbuddy_device') renderWorkBuddy();
      else if (active === 'codebuddy_device') renderCodeBuddy();
      else if (active === 'zed_device') renderZed();
      else if (active === 'kiro_device') renderKiro();
      else if (active === 'trae_device') renderTrae();
      else if (active === 'windsurf_login') renderWindsurf();
      else renderWebview();
    }

    function renderLocal() {
      const listBox = h('div', { style: 'display:flex;flex-direction:column;gap:6px' });
      const picked = new Set();
      let sources = [];
      const actions = h('div', { class: 'inline', style: 'justify-content:flex-end' },
        btn('重新扫描', { onClick: scan }), btn('导入选中', { primary: true, icon: 'plus', onClick: doImport }));
      content.append(h('div', { class: 'meta' }, '扫描本机已登录的客户端；同一账号已存在时会标记跳过。导入后直接入库，页面不显示明文。'), listBox, actions);
      scan();
      async function scan() {
        clear(listBox); listBox.append(h('div', { class: 'meta' }, '扫描本机登录态…'));
        try {
          sources = await api.get('importapps');
          const src = (sources || []).find((x) => x.id === p.id);
          const items = (src && src.items) || [];
          clear(listBox);
          picked.clear();
          if (!items.length) {
            listBox.append(empty('没有扫描到登录态', '先在本机客户端登录一次，再回来扫描。'));
            return;
          }
          if (src.path) listBox.append(h('div', { class: 'meta mono' }, '来源 ' + src.path));
          for (const it of items) {
            const box = h('input', { type: 'checkbox', checked: !it.skip, disabled: !!it.skip });
            if (!it.skip) picked.add(it.ref);
            box.addEventListener('change', () => { if (box.checked) picked.add(it.ref); else picked.delete(it.ref); });
            listBox.append(h('label', { class: 'row', style: 'cursor:pointer;gap:8px' },
              box,
              h('span', { class: 'name', style: 'flex:1' }, it.from || it.name || '账号'),
              it.skip ? chip('已导入', 'plain') : chip('可导入', 'green')));
          }
        } catch (e) { clear(listBox); listBox.append(errorBox('扫描失败：' + (e.message || e), scan)); }
      }
      async function doImport() {
        if (!picked.size) { toast('没有勾选账号'); return; }
        try {
          const picks = [...picked].map((ref) => ({ source: p.id, ref, mode: 'add' }));
          const r = await api.post('importapps', { picks });
          finish('已导入 ' + (r.added || []).length + ' 个账号');
        } catch (e) { toast('导入失败：' + (e.message || e)); }
      }
    }

    function renderManual() {
      const schema = (p.auth_form_schema && p.auth_form_schema.length) ? p.auth_form_schema : [
        { key: 'api_key', label: 'API Key', type: 'password', required: true, placeholder: 'sk-…' },
        { key: 'base_url', label: 'Base URL（可选）', type: 'text' },
      ];
      const controls = {};
      const form = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
      for (const f of schema) {
        let ctl;
        if (f.type === 'select') ctl = select((f.options || []).map((o) => ({ value: o, label: o })), (f.options || [])[0] || '', () => {}, { label: f.label });
        else if (f.type === 'textarea') ctl = textarea({ placeholder: f.placeholder || '', label: f.label });
        else ctl = input({ type: f.type === 'password' ? 'password' : 'text', placeholder: f.placeholder || '', label: f.label });
        controls[f.key] = ctl;
        form.append(field(f.label + (f.required ? ' *' : ''), ctl));
        if (f.help) form.append(h('div', { class: 'meta', style: 'margin-top:-6px' }, f.help));
      }
      content.append(form);
      // local-vault 式批量接入：每行一个 Key，公共字段沿用上面的表单。
      const bulk = schema.some((f) => f.key === 'api_key')
        ? textarea({ placeholder: '每行一个 API Key（批量接入时填这里）', label: '批量 Key' })
        : null;
      if (bulk) content.append(h('div', { class: 'meta', style: 'margin-top:4px' }, '批量粘贴（可选，每行一个 Key）'), bulk);

      content.append(h('div', { class: 'inline', style: 'justify-content:flex-end' },
        btn('接入', { primary: true, icon: 'plus', onClick: async () => {
          const keys = bulk ? bulk.value.split(/\r?\n/).map((x) => x.trim()).filter(Boolean) : [];
          const base = {};
          for (const f of schema) {
            if (f.key === 'api_key' && keys.length) continue;
            const v = (controls[f.key].value || '').trim();
            if (f.required && f.key !== 'api_key' && !v) { toast('请填写「' + f.label + '」'); return; }
            if (v) base[f.key] = v;
          }
          if (keys.length) {
            const name = label.value.trim() || p.display_name || p.id;
            let ok = 0;
            const failed = [];
            for (let i = 0; i < keys.length; i++) {
              const creds = { ...base, api_key: keys[i] };
              try {
                await api.post('accounts', { provider_id: p.id, label: name + (keys.length > 1 ? ' #' + (i + 1) : ''), credentials: creds });
                ok++;
              } catch (e) { failed.push(keys[i].slice(0, 8) + '…: ' + (e.message || e)); }
            }
            if (!ok) { toast('批量接入失败：' + (failed[0] || '未知错误')); return; }
            finish('已接入 ' + ok + ' / ' + keys.length + (failed.length ? '（失败 ' + failed.length + '）' : ''));
            return;
          }
          const apiKey = controls['api_key'] ? (controls['api_key'].value || '').trim() : '';
          if (schema.some((f) => f.key === 'api_key' && f.required) && !apiKey) { toast('请填写「API Key」'); return; }
          const creds = { ...base };
          if (apiKey) creds['api_key'] = apiKey;
          try {
            await api.post('accounts', { provider_id: p.id, label: label.value.trim() || p.display_name || p.id, credentials: creds });
            finish('账号已添加');
          } catch (e) { toast('添加失败：' + (e.message || e)); }
        } })));
    }

    function renderTokenJSON() {
      const schema = (p.auth_form_schema && p.auth_form_schema.length) ? p.auth_form_schema : [];
      const box = textarea({ placeholder: '粘贴 access token / API Key，或完整 JSON（例如 Codex auth.json）' });
      box.rows = 8;
      const file = h('input', { type: 'file', accept: '.json,application/json,text/plain', style: 'display:none' });
      file.addEventListener('change', async () => {
        const f = file.files && file.files[0];
        if (!f) return;
        try { box.value = await f.text(); } catch (e) { toast('读取文件失败：' + (e.message || e)); }
      });
      const hint = h('div', { class: 'meta' },
        'Token 会按渠道表单落到 ' + tokenKeyOf() + '；JSON 可包含 access_token / refresh_token / api_key / account_id 等字段，也支持 {tokens:{...}} 包装。');
      const go = btn('导入并接入', { primary: true, icon: 'plus', onClick: async () => {
        const rawText = box.value.trim();
        if (!rawText) { toast('先粘贴 Token 或 JSON'); return; }
        let items = [];
        if (rawText.startsWith('{') || rawText.startsWith('[')) {
          let parsed;
          try { parsed = JSON.parse(rawText); } catch (e) { toast('JSON 不合法：' + (e.message || e)); return; }
          items = Array.isArray(parsed) ? parsed : [parsed];
        } else {
          items = [rawText];
        }
        if (!items.length) { toast('没有可导入的内容'); return; }
        go.disabled = true;
        let ok = 0;
        const failed = [];
        for (let i = 0; i < items.length; i++) {
          const item = items[i];
          let creds;
          if (typeof item === 'string') {
            const key = tokenKeyOf();
            creds = { [key]: item.trim() };
            if (!creds[key]) continue;
          } else if (item && typeof item === 'object') {
            creds = normalizeCreds(item);
          } else {
            failed.push('第 ' + (i + 1) + ' 项不是对象');
            continue;
          }
          const missing = schema.filter((f) => f.required && !creds[f.key]).map((f) => f.label || f.key);
          if (missing.length) { failed.push('第 ' + (i + 1) + ' 项缺少 ' + missing.join('、')); continue; }
          const name = label.value.trim() || p.display_name || p.id;
          try {
            await api.post('accounts', {
              provider_id: p.id,
              label: name + (items.length > 1 ? ' #' + (i + 1) : ''),
              credentials: creds,
            });
            ok++;
          } catch (e) { failed.push('第 ' + (i + 1) + ' 项：' + (e.message || e)); }
        }
        go.disabled = false;
        if (!ok) { toast('导入失败：' + (failed[0] || '未知错误')); return; }
        finish('已接入 ' + ok + ' 个账号' + (failed.length ? '（失败 ' + failed.length + '）' : ''));
      } });
      content.append(
        box,
        h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap' },
          btn('选择 JSON 文件', { icon: 'folder', onClick: () => file.click() }),
          hint),
        file,
        h('div', { class: 'inline', style: 'justify-content:flex-end' }, go));
    }

    function tokenKeyOf() {
      const schema = (p.auth_form_schema && p.auth_form_schema.length) ? p.auth_form_schema : [];
      for (const key of ['api_key', 'access_token', 'token', 'session_key', 'cookie']) {
        if (schema.some((f) => f.key === key)) return key;
      }
      const pw = schema.find((f) => f.type === 'password');
      return pw ? pw.key : 'access_token';
    }

    function normalizeCreds(obj) {
      const creds = {};
      const walk = (o) => {
        for (const [k, v] of Object.entries(o || {})) {
          if (v && typeof v === 'object' && !Array.isArray(v)) {
            if (['tokens', 'credentials', 'auth', 'data'].includes(k)) walk(v);
            else creds[k] = JSON.stringify(v);
          } else if (v != null && v !== '') {
            creds[k] = String(v);
          }
        }
      };
      walk(obj);
      const alias = (from, to) => { if (!creds[to] && creds[from]) creds[to] = creds[from]; };
      alias('accessToken', 'access_token');
      alias('refreshToken', 'refresh_token');
      alias('idToken', 'id_token');
      alias('apiKey', 'api_key');
      alias('OPENAI_API_KEY', 'api_key');
      alias('openai_api_key', 'api_key');
      if (!creds.access_token && creds.token) creds.access_token = creds.token;
      return creds;
    }

    async function renderAuth(method) {
      const status = h('div', { class: 'meta' }, '');
      const detail = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
      const actions = h('div', { class: 'inline', style: 'justify-content:flex-end' });
      content.append(status, detail, actions);
      async function start() {
        clear(detail); actions.replaceChildren();
        status.textContent = '正在发起授权…';
        try {
          const r = await api.post('auth/start', { provider_id: p.id, method });
          session = r.session || r;
          if (session.status === 'success') { onSuccess(); return; }
          detail.append(h('div', { class: 'meta' }, '1. 打开授权页　2. 在浏览器完成登录　3. 回到这里自动完成'));
          if (session.user_code) {
            detail.append(h('div', { class: 'meta' }, '在浏览器里确认这段设备码：'),
              h('div', { class: 'inline' }, h('div', { class: 'mono', style: 'font-size:20px;letter-spacing:.12em' }, session.user_code),
                btn('复制设备码', { ghost: true, onClick: () => copyText(session.user_code) })),
              session.verification_uri ? btn('打开授权页面', { primary: true, icon: 'external', onClick: () => openInBrowser(session.verification_uri) }) : null);
          } else if (session.authorize_url) {
            detail.append(h('div', { class: 'meta' }, '没有自动打开时，复制链接到浏览器：'),
              h('div', { class: 'inline' }, btn('打开授权页面', { primary: true, icon: 'external', onClick: () => openInBrowser(session.authorize_url) }),
                btn('复制链接', { ghost: true, onClick: () => copyText(session.authorize_url) })));
          }
          if (session.authorize_url && !session.user_code) {
            const cb = input({ placeholder: '粘贴完整回调地址，例如 http://localhost:1455/auth/callback?code=…&state=…', label: '回调地址' });
            const manual = btn('我已授权，继续', { icon: 'check', onClick: async () => {
              const value = cb.value.trim();
              if (!value) { toast('先粘贴回调地址或 code'); return; }
              manual.disabled = true;
              manual.textContent = '校验中…';
              try {
                const out = await api.post('auth/' + encodeURIComponent(session.id) + '/callback', { callback_url: value });
                const s = out.session || out;
                if (s.status === 'success') { stop(); onSuccess(); }
                else { toast('尚未成功：' + (s.error || s.status)); }
              } catch (e) { toast('授权失败：' + (e.message || e)); }
              finally {
                manual.disabled = false;
                manual.textContent = '我已授权，继续';
              }
            } });
            detail.append(h('div', { class: 'detail-field', style: 'align-items:flex-start' },
              h('label', null, '手动输入回调地址'),
              h('div', { style: 'width:100%;display:flex;flex-direction:column;gap:6px' },
                h('div', { class: 'inline', style: 'width:100%' }, cb, manual),
                h('div', { class: 'meta' }, '浏览器无法回调本机时，把地址栏里的完整地址粘贴到这里；也可以只粘贴 code。'))));
          }
          status.textContent = '等待授权…';
          actions.append(btn('取消', { ghost: true, onClick: () => cancel() }));
          timer = setInterval(poll, 2200);
        } catch (e) {
          status.textContent = '';
          detail.append(errorBox('无法发起授权：' + (e.message || e), start));
          actions.append(btn('重试', { onClick: start }));
        }
      }
      async function poll() {
        if (!d.el.isConnected) { stop(); return; }
        try {
          const r = await api.get('auth/' + encodeURIComponent(session.id));
          const s = r.session || r;
          session = { ...session, ...s };
          if (s.status === 'success') { stop(); onSuccess(); }
          else if (s.status === 'error' || s.error) { stop(); status.textContent = '授权失败：' + (s.error || '未知错误'); actions.append(btn('重试', { onClick: start })); }
        } catch (e) { stop(); status.textContent = '轮询失败：' + (e.message || e); }
      }
      function onSuccess() {
        status.textContent = '授权成功，确认一下备注名即可入库。';
        clear(detail); actions.replaceChildren();
        actions.append(btn('完成接入', { primary: true, onClick: async () => {
          try { await api.post('auth/' + encodeURIComponent(session.id) + '/commit', { label: label.value.trim() }); finish('账号已接入'); }
          catch (e) { toast('入库失败：' + (e.message || e)); }
        } }));
      }
      function cancel() {
        stop();
        if (session && session.id) api.post('auth/' + encodeURIComponent(session.id) + '/cancel', {}).catch(() => {});
        status.textContent = '已取消'; clear(detail); actions.replaceChildren();
      }
      start();
    }

    async function renderMiniMax() {
      const status = h('div', { class: 'meta' }, '正在申请设备码…');
      const code = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
      content.append(status, code);
      let id = '';
      let t = 0;
      const stopLocal = () => { if (t) clearInterval(t); t = 0; };
      try {
        const out = await api.post('provider/minimax/device', { site: p.id });
        id = out.id || '';
        status.textContent = '在浏览器里用 MiniMax 账号确认这段代码：';
        code.append(h('div', { class: 'mono', style: 'font-size:20px;letter-spacing:.12em' }, out.user_code || ''),
          out.verification_uri ? btn('打开授权页面', { primary: true, icon: 'external', onClick: () => openInBrowser(out.verification_uri) }) : null);
        const wait = Math.max(3, Number(out.interval) || 5) * 1000;
        t = setInterval(async () => {
          if (!d.el.isConnected) { stopLocal(); return; }
          try {
            const r = await api.get('provider/minimax/device/' + encodeURIComponent(id));
            if (r.state === 'done') { stopLocal(); finish('MiniMax 账号已接入'); }
            else if (r.state === 'failed' || r.state === 'expired' || r.state === 'unknown') { stopLocal(); status.textContent = '登录失败：' + (r.error || r.state); }
          } catch (e) { stopLocal(); status.textContent = '轮询失败：' + (e.message || e); }
        }, wait);
      } catch (e) { status.textContent = '无法开始设备码登录：' + (e.message || e); }
    }

    function renderWindsurf() {
      const status = h('div', { class: 'meta' }, '使用 Windsurf / Devin 账号邮箱密码登录（密码只用于本次请求，不保存）');
      const email = input({ type: 'email', placeholder: '邮箱', label: '邮箱' });
      const pass = input({ type: 'password', placeholder: '密码', label: '密码' });
      const go = btn('登录并接入', { primary: true, onClick: async () => {
        if (!email.value.trim() || !pass.value) { toast('请填写邮箱和密码'); return; }
        go.disabled = true;
        status.textContent = '正在登录并交换 token…';
        try {
          const r = await api.post('provider/windsurf/login', { email: email.value.trim(), password: pass.value });
          finish('Windsurf 账号已接入' + (r.email ? ' · ' + r.email : ''));
        } catch (e) {
          status.textContent = '登录失败：' + (e.message || e);
          go.disabled = false;
        }
      } });
      content.append(
        h('div', { class: 'meta' }, '登录链路：密码 → auth1 → PostAuth → OneTimeAuthToken → RegisterUser。'),
        field('邮箱', email), field('密码', pass),
        h('div', { class: 'inline', style: 'justify-content:flex-end' }, go),
        status);
    }

    async function renderTrae() {
      const status = h('div', { class: 'meta' }, '正在创建 Trae 登录会话…');
      const box = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
      content.append(status, box);
      let id = '';
      let t = 0;
      let stopped = false;
      const stop = () => { stopped = true; if (t) clearInterval(t); t = 0; };
      try {
        const out = await api.post('provider/trae/device', { site: p.id });
        id = out.id || '';
        status.textContent = '在浏览器完成 Trae 登录，这里会自动接入';
        box.append(
          h('div', { class: 'meta' }, '1. 打开 Trae 登录页并授权　2. 浏览器会回调本机完成接入'),
          h('div', { class: 'inline' }, btn('打开登录页面', { primary: true, icon: 'external', onClick: () => openInBrowser(out.login_url) }),
            btn('复制链接', { ghost: true, onClick: () => copyText(out.login_url) })),
          h('div', { class: 'meta' }, '等待浏览器登录…'));
        const wait = Math.max(2, Number(out.interval) || 1) * 1000;
        t = setInterval(async () => {
          if (stopped || !d.el.isConnected) { stop(); return; }
          try {
            const r = await api.get('provider/trae/device/' + encodeURIComponent(id));
            if (r.state === 'done') { stop(); finish('Trae 账号已接入'); }
            else if (r.state === 'failed' || r.state === 'expired') { stop(); status.textContent = '登录失败：' + (r.error || r.state); }
          } catch (e) { stop(); status.textContent = '轮询失败：' + (e.message || e); }
        }, wait);
        box.append(btn('取消登录', { ghost: true, onClick: () => { stop(); api.post('provider/trae/device/' + encodeURIComponent(id) + '/cancel', {}).catch(() => {}); status.textContent = '已取消'; } }));
      } catch (e) {
        status.textContent = '无法开始 Trae 登录：' + (e.message || e);
      }
    }

    async function renderKiro() {
      const status = h('div', { class: 'meta' }, '正在启动 Kiro 回调监听…');
      const box = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
      content.append(status, box);
      let id = '';
      let t = 0;
      let stopped = false;
      const stop = () => { stopped = true; if (t) clearInterval(t); t = 0; };
      try {
        const out = await api.post('provider/kiro/device', {});
        id = out.id || '';
        status.textContent = '在浏览器完成 Kiro 登录，这里会自动接入';
        box.append(
          h('div', { class: 'meta' }, '1. 打开 Kiro 登录页并授权　2. 浏览器会回调本机完成接入'),
          h('div', { class: 'inline' }, btn('打开登录页面', { primary: true, icon: 'external', onClick: () => openInBrowser(out.login_url) }),
            btn('复制链接', { ghost: true, onClick: () => copyText(out.login_url) })),
          h('div', { class: 'meta' }, '等待浏览器登录…'));
        const wait = Math.max(2, Number(out.interval) || 1) * 1000;
        t = setInterval(async () => {
          if (stopped || !d.el.isConnected) { stop(); return; }
          try {
            const r = await api.get('provider/kiro/device/' + encodeURIComponent(id));
            if (r.state === 'done') { stop(); finish('Kiro 账号已接入'); }
            else if (r.state === 'failed' || r.state === 'expired') { stop(); status.textContent = '登录失败：' + (r.error || r.state); }
          } catch (e) { stop(); status.textContent = '轮询失败：' + (e.message || e); }
        }, wait);
        box.append(btn('取消登录', { ghost: true, onClick: () => { stop(); api.post('provider/kiro/device/' + encodeURIComponent(id) + '/cancel', {}).catch(() => {}); status.textContent = '已取消'; } }));
      } catch (e) {
        status.textContent = '无法开始 Kiro 登录：' + (e.message || e);
      }
    }

    async function renderZed() {
      const status = h('div', { class: 'meta' }, '正在生成 Zed 本机密钥…');
      const box = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
      content.append(status, box);
      let id = '';
      let t = 0;
      let stopped = false;
      const stop = () => { stopped = true; if (t) clearInterval(t); t = 0; };
      try {
        const out = await api.post('provider/zed/device', {});
        id = out.id || '';
        status.textContent = '在浏览器完成 Zed 登录，这里会自动接入';
        box.append(
          h('div', { class: 'meta' }, '1. 打开 Zed 登录页并授权　2. 浏览器会回调本机完成接入'),
          h('div', { class: 'inline' }, btn('打开登录页面', { primary: true, icon: 'external', onClick: () => openInBrowser(out.login_url) }),
            btn('复制链接', { ghost: true, onClick: () => copyText(out.login_url) })),
          h('div', { class: 'meta' }, '等待浏览器登录…'));
        const wait = Math.max(2, Number(out.interval) || 1) * 1000;
        t = setInterval(async () => {
          if (stopped || !d.el.isConnected) { stop(); return; }
          try {
            const r = await api.get('provider/zed/device/' + encodeURIComponent(id));
            if (r.state === 'done') { stop(); finish('Zed 账号已接入'); }
            else if (r.state === 'failed' || r.state === 'expired') { stop(); status.textContent = '登录失败：' + (r.error || r.state); }
          } catch (e) { stop(); status.textContent = '轮询失败：' + (e.message || e); }
        }, wait);
        box.append(btn('取消登录', { ghost: true, onClick: () => { stop(); api.post('provider/zed/device/' + encodeURIComponent(id) + '/cancel', {}).catch(() => {}); status.textContent = '已取消'; } }));
      } catch (e) {
        status.textContent = '无法开始 Zed 登录：' + (e.message || e);
      }
    }

    async function renderCodeBuddy() {
      const status = h('div', { class: 'meta' }, '正在创建登录会话…');
      const box = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
      content.append(status, box);
      let id = '';
      let t = 0;
      let stopped = false;
      const stop = () => { stopped = true; if (t) clearInterval(t); t = 0; };
      try {
        const out = await api.post('provider/codebuddy/device', { site: p.id });
        id = out.id || '';
        status.textContent = '在浏览器完成 CodeBuddy 登录，这里会自动接入';
        box.append(
          h('div', { class: 'meta' }, '1. 打开登录页并完成登录　2. 完成后自动入库'),
          h('div', { class: 'inline' }, btn('打开登录页面', { primary: true, icon: 'external', onClick: () => openInBrowser(out.login_url) }),
            btn('复制链接', { ghost: true, onClick: () => copyText(out.login_url) })),
          h('div', { class: 'meta' }, '等待浏览器登录…'));
        const wait = Math.max(2, Number(out.interval) || 2) * 1000;
        t = setInterval(async () => {
          if (stopped || !d.el.isConnected) { stop(); return; }
          try {
            const r = await api.get('provider/codebuddy/device/' + encodeURIComponent(id));
            if (r.state === 'done') { stop(); finish('CodeBuddy 账号已接入'); }
            else if (r.state === 'failed' || r.state === 'expired') { stop(); status.textContent = '登录失败：' + (r.error || r.state); }
          } catch (e) { stop(); status.textContent = '轮询失败：' + (e.message || e); }
        }, wait);
        box.append(btn('取消登录', { ghost: true, onClick: () => { stop(); api.post('provider/codebuddy/device/' + encodeURIComponent(id) + '/cancel', {}).catch(() => {}); status.textContent = '已取消'; } }));
      } catch (e) {
        status.textContent = '无法开始 CodeBuddy 登录：' + (e.message || e);
      }
    }

    async function renderWorkBuddy() {
      const status = h('div', { class: 'meta' }, '正在创建登录会话…');
      const box = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
      content.append(status, box);
      let id = '';
      let t = 0;
      let stopped = false;
      const stop = () => { stopped = true; if (t) clearInterval(t); t = 0; };
      try {
        const out = await api.post('provider/workbuddy/device', { site: p.id });
        id = out.id || '';
        status.textContent = '在浏览器完成 WorkBuddy 登录，这里会自动接入';
        box.append(
          h('div', { class: 'meta' }, '1. 打开登录页并完成登录　2. 完成后自动入库'),
          h('div', { class: 'inline' }, btn('打开登录页面', { primary: true, icon: 'external', onClick: () => openInBrowser(out.login_url) }),
            btn('复制链接', { ghost: true, onClick: () => copyText(out.login_url) })),
          h('div', { class: 'meta' }, '等待浏览器登录…'));
        const wait = Math.max(2, Number(out.interval) || 2) * 1000;
        t = setInterval(async () => {
          if (stopped || !d.el.isConnected) { stop(); return; }
          try {
            const r = await api.get('provider/workbuddy/device/' + encodeURIComponent(id));
            if (r.state === 'done') { stop(); finish('WorkBuddy 账号已接入'); }
            else if (r.state === 'failed' || r.state === 'expired') { stop(); status.textContent = '登录失败：' + (r.error || r.state); }
          } catch (e) { stop(); status.textContent = '轮询失败：' + (e.message || e); }
        }, wait);
        box.append(btn('取消登录', { ghost: true, onClick: () => { stop(); api.post('provider/workbuddy/device/' + encodeURIComponent(id) + '/cancel', {}).catch(() => {}); status.textContent = '已取消'; } }));
      } catch (e) {
        status.textContent = '无法开始 WorkBuddy 登录：' + (e.message || e);
      }
    }

    async function renderQoder() {
      const status = h('div', { class: 'meta' }, '正在创建登录会话…');
      const box = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
      content.append(status, box);
      let id = '';
      let t = 0;
      let stopped = false;
      const stop = () => { stopped = true; if (t) clearInterval(t); t = 0; };
      try {
        const out = await api.post('provider/qoder/device', { site: p.id });
        id = out.id || '';
        status.textContent = '在浏览器完成 Qoder 登录，这里会自动接入';
        box.append(
          h('div', { class: 'meta' }, '1. 打开登录页并选择账号　2. 完成后自动入库'),
          h('div', { class: 'inline' }, btn('打开登录页面', { primary: true, icon: 'external', onClick: () => openInBrowser(out.login_url) }),
            btn('复制链接', { ghost: true, onClick: () => copyText(out.login_url) })),
          h('div', { class: 'meta' }, '等待浏览器登录…'));
        const wait = Math.max(2, Number(out.interval) || 1) * 1000;
        t = setInterval(async () => {
          if (stopped || !d.el.isConnected) { stop(); return; }
          try {
            const r = await api.get('provider/qoder/device/' + encodeURIComponent(id));
            if (r.state === 'done') { stop(); finish('Qoder 账号已接入'); }
            else if (r.state === 'failed' || r.state === 'expired') { stop(); status.textContent = '登录失败：' + (r.error || r.state); }
          } catch (e) { stop(); status.textContent = '轮询失败：' + (e.message || e); }
        }, wait);
        const cancelBtn = btn('取消登录', { ghost: true, onClick: () => { stop(); api.post('provider/qoder/device/' + encodeURIComponent(id) + '/cancel', {}).catch(() => {}); status.textContent = '已取消'; } });
        box.append(cancelBtn);
      } catch (e) {
        status.textContent = '无法开始 Qoder 登录：' + (e.message || e);
      }
    }

    function renderWebview() {
      if (p.webview_login_url) {
        content.append(h('div', { class: 'meta' }, '该渠道使用网页登录；若未自动完成，请在浏览器登录后改用「手动凭据」。'),
          btn('打开登录页面', { primary: true, icon: 'external', onClick: () => openInBrowser(p.webview_login_url) }));
      } else {
        content.append(empty('该渠道暂未提供网页登录', '请使用手动凭据。'));
      }
    }
    renderMethod();
  });
  const closeBtn = d.el.querySelector('.drawer-h .icon-btn');
  if (closeBtn) closeBtn.addEventListener('click', stop);
  d.el.addEventListener('click', (e) => { if (e.target.closest('.backdrop')) stop(); });
  return d;
}
