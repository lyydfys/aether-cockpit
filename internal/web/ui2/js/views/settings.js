/* 设置（自有实现）：外观、常规、网络、隐私、备份、关于。写回 /api/settings 的字段与后端一致。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { card, btn, chip, toggle, select, input, field, toast, confirmDialog, fmtDate, drawer, skeleton, textarea, copyText, empty, iconBtn, trapFocus } from '../components.js';
import { icon, brandMark } from '../icons.js';
import { openInBrowser } from '../links.js';

const fmtBytes = (n) => (n >= 1 << 20 ? ((n / (1 << 20)).toFixed(1) + ' MB') : (n >= 1 << 10 ? (n / (1 << 10)).toFixed(1) + ' KB' : n + ' B'));

const UPDATE_REPO_FALLBACK = 'lyydfys/aether-cockpit';
const UPDATE_SKIP_KEY = 'aether.update.skip';
const STAR_DISMISS_KEY = 'aether.star.dismissed';
const repoHome = (repo) => 'https://github.com/' + (repo || UPDATE_REPO_FALLBACK);
const storage = {
  get(key) { try { return localStorage.getItem(key) || ''; } catch { return ''; } },
  set(key, value) { try { localStorage.setItem(key, value); } catch { /* 隐私模式下忽略 */ } },
};

// 发布说明只做最小 Markdown 结构渲染（标题/列表/段落），全部走 textContent。
function noteNodes(text) {
  const clean = (s) => String(s).replace(/\*\*(.+?)\*\*/g, '$1').replace(/`([^`]+)`/g, '$1');
  const nodes = [];
  for (const raw of String(text || '').split(/\r?\n/)) {
    const line = raw.trim();
    if (!line) continue;
    if (/^#{1,6}\s+/.test(line)) nodes.push(h('h4', null, clean(line.replace(/^#{1,6}\s+/, ''))));
    else if (/^[-*]\s+/.test(line)) nodes.push(h('div', { class: 'update-li' }, clean(line.replace(/^[-*]\s+/, ''))));
    else nodes.push(h('p', null, clean(line)));
  }
  return nodes.length ? nodes : [h('p', null, '该版本没有填写发布说明。')];
}

// GitHub 返回 ISO 时间，旧清单可能给 Unix 秒；两种都接受。
function fmtPublished(value) {
  if (!value) return '';
  const n = Number(value);
  if (Number.isFinite(n) && n > 0) return fmtDate(n);
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '';
  const p2 = (x) => String(x).padStart(2, '0');
  return d.getFullYear() + '/' + p2(d.getMonth() + 1) + '/' + p2(d.getDate()) + ' ' + p2(d.getHours()) + ':' + p2(d.getMinutes());
}

const TABS = [
  ['general', '常规'], ['network', '网络与共享'], ['tools', '工具服务'], ['presets', '预设'],
  ['pricing', '定价'], ['models', '模型目录'], ['privacy', '隐私'], ['observability', '可观测'], ['backup', '同步与备份'], ['about', '关于'],
];

export default {
  title: '设置',
  subtitle: '账号与安全 · 工具服务 · 可观测 · 高级',

  mount(host, ctx) {
    const state = { tab: (ctx.store.get().routeParams || {}).tab || 'general', s: null, v: {}, p: null, pricing: null, redirects: null, error: '', update: null, updateBusy: false };
    host.append(skeleton(6));
    load();

    async function load() {
      try {
        const [s, v, p, pricing, autoImport, redirects] = await Promise.all([
          api.get('settings'),
          api.version().catch(() => ({})),
          api.get('presets').catch(() => ({ presets: [], provider_presets: {}, route_presets: {} })),
          api.get('pricing').catch(() => ({ currency: 'USD', usd_cny: 7.2, defaults: [], overrides: {} })),
          api.get('importapps/auto').catch(() => ({})),
          api.get('model-redirects').catch(() => ({ redirects: {} })),
        ]);
        state.s = s;
        state.v = v || {};
        state.p = p || { presets: [], provider_presets: {}, route_presets: {} };
        state.pricing = pricing || { currency: 'USD', usd_cny: 7.2, defaults: [], overrides: {} };
        state.autoImport = autoImport || {};
        state.redirects = redirects || { redirects: {} };
        render();
      } catch (e) {
        clear(host);
        host.append(h('div', { class: 'error-box' }, '设置读取失败：' + (e.message || e)));
      }
    }

    async function save(patch, label) {
      try { await api.post('settings', patch); toast((label || '设置') + '已保存'); Object.assign(state.s, patch); }
      catch (e) { toast('保存失败：' + (e.message || e)); }
    }

    function render() {
      clear(host);
      const s = state.s;
      const tabs = h('aside', { class: 'settings-nav', role: 'tablist', 'aria-label': '设置分组' });
      for (const [id, label] of TABS) {
        tabs.append(h('button', {
          class: 'tab' + (state.tab === id ? ' on' : ''), type: 'button', role: 'tab',
          'aria-selected': state.tab === id ? 'true' : 'false',
          onclick: () => { state.tab = id; render(); },
        }, label));
      }
      const body = h('div', { class: 'settings-body', role: 'tabpanel' });
      host.append(h('div', { class: 'settings-layout' }, tabs, body));
      if (state.tab === 'general') body.append(...general(s));
      else if (state.tab === 'network') body.append(...network(s));
      else if (state.tab === 'tools') body.append(...toolsTab(s));
      else if (state.tab === 'presets') body.append(...presetsTab());
      else if (state.tab === 'pricing') body.append(...pricingTab());
      else if (state.tab === 'models') body.append(...modelsTab());
      else if (state.tab === 'privacy') body.append(...privacy(s));
      else if (state.tab === 'observability') body.append(...observability(s));
      else if (state.tab === 'backup') body.append(...backup(s));
      else body.append(...about(s));
    }

    function section(title, rows) {
      const c = card(title);
      for (const r of rows) c.el.append(r);
      return c.el;
    }
    function line(label, desc, control) {
      if (control && control.querySelectorAll) {
        const targets = control.matches('input,select,textarea,.toggle') ? [control] : [...control.querySelectorAll('input,select,textarea,.toggle')];
        for (const el of targets) {
          if (!el.getAttribute('aria-label') && !el.getAttribute('aria-labelledby')) el.setAttribute('aria-label', label);
        }
      }
      return h('div', { class: 'set-row' },
        h('div', { class: 'l' },
          h('div', { class: 't' }, label),
          desc ? h('div', { class: 'h' }, desc) : null),
        h('div', { class: 'c' }, control));
    }

    function general(s) {
      const theme = (() => { try { return localStorage.getItem('aether.theme') || 'auto'; } catch { return 'auto'; } })();
      const themeSeg = h('div', { class: 'tabs' }, [['auto', '跟随系统'], ['light', '浅色'], ['dark', '深色']].map(([v, l]) =>
        h('button', { class: 'tab' + (theme === v ? ' on' : ''), type: 'button', onclick: () => {
          try { localStorage.setItem('aether.theme', v); } catch {}
          document.documentElement.dataset.theme = v;
          save({ ui: { ...(s.ui || {}), theme: v } }, '主题');
          render();
        } }, l)));
      const auto = s.autostart || {};
      const autoDesc = auto.enabled && auto.command
        ? '登录 Windows 时自动启动：' + auto.command
        : (auto.supported ? '登录 Windows 时自动启动（使用当前安装位置）' : '仅桌面版可用；无头模式请用系统服务/计划任务');
      const autoToggle = auto.supported
        ? toggle(!!auto.enabled, async (on) => {
            try {
              const r = await api.post('settings/autostart', { on });
              toast((r.autostart && r.autostart.enabled) ? '已开启开机自启' : '已关闭开机自启');
              await load();
            } catch (e) { toast('设置失败：' + (e.message || e)); }
          })
        : h('span', { class: 'meta' }, '不可用');
      const ai = state.autoImport || {};
      const autoImportSwitch = toggle(!!ai.enabled, async (on) => {
        try {
          state.autoImport = await api.post('importapps/auto', { enabled: on }) || {};
          toast(on ? '已开启本机账号自动导入' : '已关闭本机账号自动导入');
          render();
          if (on) setTimeout(async () => {
            try { state.autoImport = await api.get('importapps/auto') || {}; render(); } catch {}
          }, 1500);
        } catch (e) { toast('设置失败：' + (e.message || e)); }
      });
      const scanBtn = btn('立即扫描', { icon: 'refresh', onClick: async (e) => {
        const el = e.currentTarget;
        el.disabled = true;
        try {
          state.autoImport = await api.post('importapps/auto/scan', {}) || {};
          const n = (state.autoImport.last_added || []).length;
          toast(n ? '已自动导入 ' + n + ' 个新账号' : '扫描完成，没有新的本机账号');
          render();
        } catch (err) {
          toast('扫描失败：' + (err.message || err));
          if (el.isConnected) el.disabled = false;
        }
      } });
      const resetBtn = btn('清除记录', { ghost: true, onClick: async () => {
        if (!(await confirmDialog('清除自动导入记录？', '只清除“哪些本机身份已处理过”的记录，不会删除现有账号。清除后下次扫描可能重新导入本机仍在登录的账号。', { confirmText: '清除' }))) return;
        try {
          state.autoImport = await api.post('importapps/auto/reset', {}) || {};
          toast('自动导入记录已清除');
          render();
        } catch (e) { toast('清除失败：' + (e.message || e)); }
      } });
      const aiState = ai.running
        ? '正在扫描本机登录态…'
        : (ai.last_scan ? '上次扫描 ' + fmtDate(ai.last_scan, true, true) + ' · 已记录 ' + Number(ai.seen || 0) + ' 个身份' : '尚未扫描');
      const aiStatus = h('div', { style: 'display:flex;flex-direction:column;gap:6px;align-items:flex-end' },
        h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap;justify-content:flex-end' }, h('span', { class: 'meta' }, aiState), scanBtn, resetBtn),
        ai.last_added && ai.last_added.length ? h('div', { class: 'meta' }, '上次新增：' + ai.last_added.join('、')) : null,
        ai.last_error ? h('div', { class: 'meta', style: 'color:var(--amber)' }, '最近失败：' + ai.last_error) : null);

      return [
        section('外观', [line('主题', '跟随系统，或固定浅色 / 深色', themeSeg)]),
        section('常规', [
          line('开机自启', autoDesc, autoToggle),
          line('WSL 接入', '关闭后不枚举发行版、不访问 WSL 里的家目录', toggle(!s.wsl_disabled, (on) => save({ wsl_disabled: !on }, 'WSL 接入'))),
          line('防止休眠', 'Aether Cockpit 运行时让电脑保持唤醒', toggle(!!s.keep_awake, (on) => save({ keep_awake: on }, '防止休眠'))),
          line('渠道自动探测', '会真实消耗账号额度，默认关闭', monitorSelect(s)),
        ]),
        section('导入', [
          line('本机账号自动导入', '每 60 秒扫描本机已登录的客户端（Codex / Qoder / Trae / Claude / Kiro 等），新账号自动入库；已删除的账号不会复活。', autoImportSwitch),
          line('自动导入状态', '身份按账号 ID / 邮箱 / 长期密钥去重；token 轮换不会重复导入。', aiStatus),
        ]),
      ];
    }

    function monitorSelect(s) {
      const v = Number(s.monitor_minutes || 0);
      return select([[0, '关闭'], [5, '每 5 分钟'], [15, '每 15 分钟'], [30, '每 30 分钟'], [60, '每 60 分钟']].map(([value, label]) => ({ value, label })), v, (val) => save({ monitor_minutes: Number(val) }, '自动探测'));
    }

    function network(s) {
      return [
        section('监听', [
          line('局域网访问', '其它电脑可访问这个网关（仍需密钥或调用方密钥）', toggle(!!s.lan, (on) => save({ lan: on }, '局域网访问'))),
          line('监听地址', '只读；端口在启动参数里设置', h('span', { class: 'mono meta' }, s.addr || '—')),
          line('数据目录', '账号、设置与账本都在这里', h('span', { class: 'mono meta' }, s.dir || '—')),
        ]),
        section('网络', [
          line('全局代理', '如 socks5://127.0.0.1:7890；留空直连', proxyControl(s)),
          line('CORS 来源', '留空表示任意网页来源；填写后仅允许这些来源', listControl(s, 'cors_origins', 'https://example.com, https://app.example.com')),
          line('允许的 IP', '非本机请求只能来自这些 IP / CIDR；留空不限', listControl(s, 'allow_ips', '192.168.1.0/24, 10.0.0.5')),
        ]),
        channelNetSection(),
        workbuddyIdentitySection(),
      ];
    }

    // workbuddyIdentitySection 暴露 WorkBuddy 渠道的「出网身份」档位。
    //
    // 这是渠道级能力（UA / CLI 段 / Stainless 头 / 请求间隔整组切换），手机端早就有；
    // 桌面端此前只能手改 settings.json 或环境变量。保存只落盘，**重启应用后生效**，
    // 所以这里必须同时显示「运行中真实在用的档位」，否则用户会以为切换没成功。
    function workbuddyIdentitySection() {
      const TITLES = {
        cli: '官方 CLI（双段 UA + Stainless）',
        desktop: '桌面端（单段 UA）',
        quiet: '桌面端 · 安静（请求间隔 900ms）',
        browser: '浏览器 UA',
      };
      const body = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
      const box = section('WorkBuddy 出网身份', [
        h('div', { class: 'meta', style: 'line-height:1.6' },
          '同一时刻只用一档：UA、CLI 段、Stainless 头、请求间隔整组切换，不会出现「UA 说桌面、头却发 CLI」的跨档组合。'),
        body,
      ]);
      const render = (st) => {
        clear(body);
        if (!st) {
          body.append(h('div', { class: 'meta' }, '当前网关不支持该能力（或读取失败）。'));
          return;
        }
        const saved = st.saved || st.identity;
        const available = st.available && st.available.length ? st.available : [st.identity];
        const ctl = select(available.map((k) => ({ value: k, label: TITLES[k] || k })), saved, async (val) => {
          await save({ workbuddy_identity: val }, 'WorkBuddy 出网身份');
          await refresh();
        });
        body.append(line('身份档位', '保存后需重启应用才会生效', ctl));
        body.append(line('运行中 UA', '当前进程实际发出的 User-Agent',
          h('span', { class: 'mono meta', style: 'word-break:break-all' }, st.ua || '—')));
        body.append(line('Stainless', '仅 CLI 档发送这一族头',
          h('span', { class: 'mono meta' },
            st.stainless
              ? 'v' + (st.stainless_version || '?') + ' · ' + (st.cli_version || '?')
              : '不发（仅 CLI 档）')));
        body.append(line('设备标识头', 'X-Machine-ID / X-Session-ID 默认不发；可按账号 device_headers 开启',
          h('span', { class: 'mono meta' }, st.machine_headers ? '发送' : '不发')));
        body.append(line('同账号请求间隔', '防止并发突刺被判定为脚本',
          h('span', { class: 'mono meta' }, (st.pace_ms || 0) + 'ms')));
        if (st.saved && st.identity && st.saved !== st.identity) {
          body.append(h('div', { class: 'meta', style: 'line-height:1.6;color:var(--amber, #b45309)' },
            '已保存 ' + (TITLES[st.saved] || st.saved) + '，当前运行仍是 ' +
            (TITLES[st.identity] || st.identity) + '。重启应用后生效。'));
        }
      };

      const refresh = async () => {
        try {
          const st = await api.get('providers/workbuddy/identity');
          state.wbIdentity = st;
          render(st);
        } catch {
          render(state.wbIdentity || null);
        }
      };
      refresh();
      return box;
    }

    // channelNetSection 是「慢链路优化」的**全局总览**。
    //
    // 具体调整放在渠道详情 → 网络：预热间隔、并发上限、边缘优选都是"这个
    // 上游地址"的属性，放在全局设置里既不好找，也容易误以为对所有渠道生效。
    // 这里只回答一个问题：现在有哪些渠道开着优化、各自什么参数。
    function channelNetSection() {
      const chips = h('div', { class: 'inline', style: 'gap:6px;flex-wrap:wrap' });
      const note = h('div', { class: 'meta', style: 'line-height:1.6' },
        '预热 = 每隔 N 秒打一次 /models（不消耗 token）把 TCP+TLS 保持热态；'
        + '在途上限 = 同时最多几条流，防止并发互相拖慢。调整入口：渠道 → 详情 → 网络。');
      const box = section('慢链路优化', [
        line('已开启优化的渠道', '仅对跨境 / 中转渠道默认开启；本地渠道保持原样', chips),
        h('div', { style: 'padding:2px 0 6px' }, note),
      ]);
      load();

      async function load() {
        clear(chips);
        try {
          const r = await api.get('channels/net');
          const rows = r.channels || [];
          if (!rows.length) {
            chips.append(h('span', { class: 'meta' }, '暂无'));
            return;
          }
          for (const row of rows) {
            chips.append(chip(
              (row.provider_name || row.provider_id) + ' · 预热 ' + (row.prewarm_sec || 0) + 's / 在途 ' + (row.max_inflight || 0),
              (row.inflight || 0) > 0 ? 'green' : 'plain'));
          }
        } catch {
          chips.append(h('span', { class: 'meta' }, '读取失败'));
        }
      }
      return box;
    }

    function proxyControl(s) {
      const inp = input({ value: s.proxy || '', placeholder: 'socks5://127.0.0.1:7890' });
      return h('div', { class: 'inline' }, inp, btn('保存', { onClick: () => save({ proxy: inp.value.trim() }, '代理') }));
    }

    function listControl(s, key, placeholder) {
      const inp = input({ value: (s[key] || []).join(', '), placeholder });
      return h('div', { class: 'inline' }, inp, btn('保存', { onClick: () => save({ [key]: inp.value.split(',').map((x) => x.trim()).filter(Boolean) }, key) }));
    }

    function toolsTab(s) {
      const t = s.tools || {};
      const wsCfg = t.web_search || {};
      const wsProv = select([
        { value: 'tavily', label: 'Tavily' },
        { value: 'serper', label: 'Serper' },
        { value: 'searxng', label: 'SearXNG' },
        { value: 'brave', label: 'Brave' },
        { value: 'exa', label: 'Exa' },
        { value: 'firecrawl', label: 'Firecrawl' },
        { value: 'deepseek', label: 'DeepSeek' },
        { value: 'you', label: 'You.com' },
      ], wsCfg.provider || 'tavily', () => {});
      const wsEP = input({ value: wsCfg.endpoint || '', placeholder: '自定义地址（可选；SearXNG 必填，如 http://127.0.0.1:8080）' });
      const wsKey = input({ type: 'password', value: '', placeholder: wsCfg.key_count ? '已保存 ' + wsCfg.key_count + ' 个 Key（留空保留）' : 'API Key（You.com 可留空走免费搜索）' });
      const wsKeys = textarea({ value: '', placeholder: '多 Key：每行一个；填写会替换现有 Key，留空保留' });
      const wsMax = input({ type: 'number', value: wsCfg.max_results || 5, placeholder: '5' });
      const wsOrder = select([
        { value: 'rotate', label: '多 Key 轮换使用' },
        { value: 'order', label: '按顺序优先' },
      ], wsCfg.key_order || 'rotate', () => {});
      const wsStatus = h('div', { class: 'meta' }, '');
      const wsTest = btn('测试搜索', { onClick: async () => {
        wsTest.disabled = true;
        wsStatus.textContent = '测试中…';
        try {
          const r = await api.post('tools/web-search', { query: 'Aether Cockpit', max_results: 3 });
          const first = (r.results || [])[0];
          wsStatus.textContent = '搜索成功 · ' + (r.provider || '') + ' · ' + (r.results || []).length + ' 条' + (first ? ' · ' + first.title : '');
        } catch (e) { wsStatus.textContent = '搜索失败：' + (e.message || e); }
        wsTest.disabled = false;
      } });
      const im = t.image || {};
      const au = t.audio || {};
      const imBase = input({ value: im.base_url || '', placeholder: 'https://api.openai.com/v1' });
      const imKey = input({ type: 'password', value: im.api_key || '', placeholder: 'sk-…（已存则显示 ******）' });
      const imModel = input({ value: im.model || '', placeholder: 'gpt-image-1（可选）' });
      const auBase = input({ value: au.base_url || '', placeholder: 'https://api.openai.com/v1' });
      const auKey = input({ type: 'password', value: au.api_key || '', placeholder: 'sk-…（已存则显示 ******）' });
      const auModel = input({ value: au.model || '', placeholder: 'whisper-1 / tts-1（可选）' });
      const vd = t.video || {};
      const vdBase = input({ value: vd.base_url || '', placeholder: 'https://api.x.ai/v1' });
      const vdKey = input({ type: 'password', value: vd.api_key || '', placeholder: 'xai-…（已存则显示 ******）' });
      const vdModel = input({ value: vd.model || '', placeholder: 'grok-imagine-video（可选）' });
      const rt = t.realtime || {};
      const rtBase = input({ value: rt.base_url || '', placeholder: 'https://api.openai.com/v1' });
      const rtKey = input({ type: 'password', value: rt.api_key || '', placeholder: 'sk-…（已存则显示 ******）' });
      const rtModel = input({ value: rt.model || '', placeholder: 'gpt-4o-realtime-preview（可选）' });
      const status = h('div', { class: 'meta' }, '图像、音频、视频、实时语音均使用兼容接口；密钥打码保存。');
      const saveBtn = btn('保存工具服务', { primary: true, onClick: async () => {
        const webSearch = {
          provider: wsProv.value,
          endpoint: wsEP.value.trim(),
          max_results: Math.max(1, Math.min(20, Number(wsMax.value) || 5)),
          key_order: wsOrder.value,
        };
        const keys = wsKeys.value.split(String.fromCharCode(10)).map((x) => x.trim()).filter(Boolean);
        if (keys.length) {
          webSearch.api_keys = keys;
        } else if (wsKey.value.trim()) {
          webSearch.api_key = wsKey.value.trim();
        } else if (wsCfg.key_count) {
          // 留空：不带 api_key，服务端沿用已保存的 Key。
          webSearch.api_key = '******';
        }
        const payload = {
          tools: {
            web_search: webSearch,
            image: { base_url: imBase.value.trim(), api_key: imKey.value.trim(), model: imModel.value.trim() },
            audio: { base_url: auBase.value.trim(), api_key: auKey.value.trim(), model: auModel.value.trim() },
            video: { base_url: vdBase.value.trim(), api_key: vdKey.value.trim(), model: vdModel.value.trim() },
            realtime: { base_url: rtBase.value.trim(), api_key: rtKey.value.trim(), model: rtModel.value.trim() },
          },
        };
        try {
          await api.post('settings', payload);
          toast('工具服务已保存');
          await load();
        } catch (e) { toast('保存失败：' + (e.message || e)); }
      } });
      const testBtn = btn('测试图像', { onClick: async () => {
        testBtn.disabled = true;
        try {
          const r = await api.post('tools/image-test', {});
          status.textContent = '图像测试：' + (r.summary || '成功');
        } catch (e) { status.textContent = '图像测试失败：' + (e.message || e); }
        testBtn.disabled = false;
      } });
      return [
        section('网络搜索', [
          line('供应商', '给模型和 /v1/tools/web_search 提供联网搜索', wsProv),
          line('地址', '留空用供应商默认地址', wsEP),
          line('API Key', '单 Key；与多 Key 二选一', wsKey),
          line('多 Key', '每行一个；填写会替换现有 Key', wsKeys),
          line('返回条数', '1–20，默认 5', wsMax),
          line('Key 顺序', '轮换或固定顺序', wsOrder),
          line('测试', '用当前配置搜一次', h('div', { class: 'inline' }, wsTest, wsStatus)),
        ]),
        section('图像服务', [
          line('Base URL', 'OpenAI 兼容 /images/generations 与 /images/edits', imBase),
          line('API Key', '只保存在本机设置里', imKey),
          line('默认模型', '请求未带 model 时使用', imModel),
        ]),
        section('音频服务', [
          line('Base URL', 'OpenAI 兼容 /audio/transcriptions 与 /audio/speech', auBase),
          line('API Key', '只保存在本机设置里', auKey),
          line('默认模型', '转写 / 合成未带 model 时使用', auModel),
        ]),
        section('视频服务', [
          line('Base URL', 'xAI 用 https://api.x.ai/v1（/videos/generations）', vdBase),
          line('API Key', '只保存在本机设置里', vdKey),
          line('默认模型', '如 grok-imagine-video；未带 model 时使用', vdModel),
        ]),
        section('实时语音', [
          line('Base URL', 'OpenAI Realtime 兼容；网关提供 /v1/realtime WebSocket 桥', rtBase),
          line('API Key', '只保存在本机设置里', rtKey),
          line('默认模型', '连接未带 model 时使用', rtModel),
        ]),
        section('保存与测试', [
          line('保存', '图像、音频、视频、实时配置一次写入', h('div', { class: 'inline' }, saveBtn, testBtn)),
          line('状态', ' ', status),
        ]),
      ];
    }

    function modelsTab() {
      const redirects = (state.redirects && state.redirects.redirects) || {};
      const c = card('模型重定向', { chips: [chip('虚拟名 → 渠道上游名', 'plain')] });
      const ta = textarea({ value: JSON.stringify(redirects, null, 2), label: '模型重定向 JSON' });
      ta.rows = 12;
      ta.style.width = '100%';
      ta.style.fontFamily = 'ui-monospace, SFMono-Regular, Menlo, Consolas, monospace';
      const saveBtn = btn('保存重定向', {
        primary: true,
        onClick: async () => {
          let parsed;
          try { parsed = ta.value.trim() ? JSON.parse(ta.value) : {}; }
          catch { toast('JSON 格式不正确'); return; }
          if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
            toast('需要对象：{"虚拟名":{"渠道":"上游模型名"}}');
            return;
          }
          try {
            state.redirects = await api.post('model-redirects', { redirects: parsed });
            toast('模型重定向已保存');
            render();
          } catch (e) { toast('保存失败：' + (e.message || e)); }
        },
      });
      const refreshBtn = btn('刷新全部模型目录', {
        ghost: true, icon: 'refresh',
        onClick: async (e) => {
          const el = e.currentTarget;
          el.disabled = true;
          try {
            const r = await api.post('providers/models/refresh-all', {});
            toast('模型目录：' + (r.refreshed || 0) + ' 个渠道成功' + (r.failed ? '，' + r.failed + ' 个失败' : ''));
            await load();
          } catch (e2) { toast('刷新失败：' + (e2.message || e2)); }
          finally { if (el.isConnected) el.disabled = false; }
        },
      });
      c.el.append(
        h('div', { class: 'meta', style: 'padding:4px 0 8px' },
          '客户端只写虚拟名；路由按渠道顺序尝试，转发时使用该渠道配置的上游名。重定向优先于同名裸模型与路由组，显式 provider/model 仍然直达。'),
        ta,
        h('div', { class: 'inline', style: 'justify-content:flex-end;gap:8px;margin-top:8px' }, refreshBtn, saveBtn),
        h('div', { class: 'meta', style: 'padding:8px 0' },
          '示例：{"deepseek-v4.1-flash":{"qoder-cn":"DeepSeek-Flash","workbuddy-intl":"deepseek-v4.1-flash-sg"}}。V4 Flash 与 V4.1 Flash 是两个模型，不会被自动合并；只有这里明确写出的映射才会生效。'),
      );
      return [c.el, h('div', { class: 'meta', style: 'padding:8px 2px' }, '渠道级模型目录模式（merge / fallback / allowlist）在「账号与额度 → 渠道详情 → 模型」里配置；这里管的是跨渠道虚拟名。')];
    }

    function privacy(s) {
      const hooksCfg = s.hooks || {};
      const listText = (v) => (Array.isArray(v) ? v.join(', ') : '');
      const clampInput = input({
        type: 'number', value: hooksCfg.clamp_output ? String(hooksCfg.clamp_output) : '',
        placeholder: '留空 = 不限制',
      });
      const blockInput = input({ value: listText(hooksCfg.block_models), placeholder: '例如 gpt-4*,o1-*' });
      const guardInput = input({ value: listText(hooksCfg.guard_providers), placeholder: '例如 expensive-channel' });
      return [
        section('脱敏', [
          line('请求脱敏', '请求离开这台电脑前，把密钥/邮箱/手机号换成占位符', toggle(!!s.redact, (on) => save({ redact: on }, '请求脱敏'))),
          // 服务端返回的键是 redactPersonal（camelCase）；以前写成红下的
          // s.redact_personal，开关永远是"关闭"状态，看着像没生效。
          line('个人信息', '在脱敏里包含邮箱、手机号、证件、银行卡等个人字段', toggle(s.redactPersonal !== false, (on) => save({ redactPersonal: on }, '个人信息'))),
          line('保留格式的假值', '密钥类改成"同形假值"（sk-… 仍是 sk-…），上游更难识别', toggle(!!s.redactShape, (on) => save({ redactShape: on }, '保留格式的假值'))),
          line('响应回填', '上游响应里的占位符换回原文', toggle(!!(s.privacy_mask && s.privacy_mask.enabled), (on) => save({ privacy_mask: { enabled: on } }, '响应回填'))),
          h('div', { class: 'meta', style: 'padding:8px 2px' }, '按类型处置、细项开关、自定义规则、白名单与记录详情在「洞察 → 脱敏」页。'),
        ]),
        // 请求管线钩子：进程内扩展点（不是 library 里那种客户端 hook）。
        section('请求钩子', [
          line('丢弃推理增量', '部分客户端会把 reasoning 当正文显示；打开后从流里过滤掉',
            toggle(!!hooksCfg.drop_reasoning, (on) => saveHooks({ ...hooksCfg, drop_reasoning: on }, '丢弃推理增量'))),
          line('输出上限', '客户端不设 max_tokens 时按这个值封顶，防止一次跑掉整天额度',
            h('div', { class: 'inline', style: 'gap:8px' },
              clampInput,
              btn('保存', { ghost: true, onClick: () => saveHooks({ ...hooksCfg, clamp_output: Number(clampInput.value) || 0 }, '输出上限') }))),
          line('拦截模型', '按前缀拦截（逗号分隔，* 结尾表示前缀），被拦的请求直接 4xx',
            h('div', { class: 'inline', style: 'gap:8px' },
              blockInput,
              btn('保存', { ghost: true, onClick: () => saveHooks({ ...hooksCfg, block_models: splitList(blockInput.value) }, '拦截模型') }))),
          line('跳过渠道', '这些渠道不参与本次请求（逗号分隔的渠道 ID）',
            h('div', { class: 'inline', style: 'gap:8px' },
              guardInput,
              btn('保存', { ghost: true, onClick: () => saveHooks({ ...hooksCfg, guard_providers: splitList(guardInput.value) }, '跳过渠道') }))),
          h('div', { class: 'meta', style: 'padding:8px 2px' },
            '钩子跑在网关进程内（不启动任何外部进程）；单个钩子 panic 或超时只跳过它自己，不影响请求。'),
        ]),
      ];
    }

    // splitList 把逗号/换行分隔的文本切成数组（去空、去重）。
    function splitList(text) {
      const out = [];
      for (const part of String(text || '').split(/[,，\n]/)) {
        const v = part.trim();
        if (v && !out.includes(v)) out.push(v);
      }
      return out;
    }

    async function saveHooks(cfg, label) {
      try { await api.post('settings', { hooks: cfg }); toast(label + ' 已保存'); }
      catch (e) { toast('保存失败：' + (e.message || e)); }
    }

    function observability(s) {
      const otlp = s.otlp || {};
      const masked = !!(otlp.headers && otlp.headers.___masked___);
      const endpoint = input({ value: otlp.endpoint || '', placeholder: 'http://127.0.0.1:4318' });
      const headers = textarea({
        value: masked ? '' : Object.entries(otlp.headers || {}).map(([k, v]) => k + ': ' + v).join('\n'),
        placeholder: masked ? '已保存的请求头（留空保留）' : '请求头，每行 Key: Value',
      });
      headers.rows = 3;
      const batch = input({ type: 'number', value: otlp.batch_size ? String(otlp.batch_size) : '', placeholder: '32' });
      const flush = input({ type: 'number', value: otlp.flush_interval_seconds ? String(otlp.flush_interval_seconds) : '', placeholder: '5' });
      const queue = input({ type: 'number', value: otlp.queue_max ? String(otlp.queue_max) : '', placeholder: '128' });
      const saveAll = btn('保存', { primary: true, onClick: () => {
        const hdr = {};
        for (const row of headers.value.split('\n')) {
          const lineText = row.trim();
          if (!lineText) continue;
          const i = lineText.indexOf(':');
          if (i > 0) hdr[lineText.slice(0, i).trim()] = lineText.slice(i + 1).trim();
        }
        const next = { endpoint: endpoint.value.trim(), headers: Object.keys(hdr).length ? hdr : (masked ? { ___masked___: 'true' } : {}) };
        if (batch.value) next.batch_size = Number(batch.value);
        if (flush.value) next.flush_interval_seconds = Number(flush.value);
        if (queue.value) next.queue_max = Number(queue.value);
        save({ otlp: next }, 'OTLP');
      } });
      return [
        section('OpenTelemetry', [
          line('OTLP 导出地址', '非空即启用', h('div', { class: 'inline' }, endpoint, saveAll)),
          line('请求头', '鉴权头等；GET 设置时只回掩码，留空即保留原值', headers),
          line('攒批 / 周期 / 队列', '默认 32 条、5 秒、128 条', h('div', { class: 'inline' }, batch, flush, queue)),
        ]),
        section('探针', [
          line('存活 / 就绪', '给监控系统用的轻量探针', h('div', { class: 'inline' }, btn('打开 /healthz'), btn('打开 /readyz'))),
        ]),
      ];
    }

    function backup(s) {
      const file = h('input', { type: 'file', accept: 'application/json,.json', style: 'display:none', onchange: async (e) => {
        const f = e.target.files && e.target.files[0];
        if (!f) return;
        if (!(await confirmDialog('导入备份？', '会合并设置、分组、spec 与账号标签；账号凭证不会从备份恢复。', { confirmText: '导入' }))) { e.target.value = ''; return; }
        try {
          const text = await f.text();
          const res = await fetch('/api/backup/import', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: text });
          if (!res.ok) throw new Error(await res.text());
          toast('备份已导入'); await load();
        } catch (err) { toast('导入失败：' + (err.message || err)); }
        e.target.value = '';
      } });
      function bundleDialog(mode) {
        const pass = input({ type: 'password', placeholder: mode === 'export' ? '设置一个至少 8 位的口令' : '输入导出时设置的口令', label: '口令' });
        const file = mode === 'import' ? h('input', { type: 'file', accept: '.aether-accounts,application/octet-stream', style: 'max-width:100%' }) : null;
        const status = h('div', { class: 'meta' }, mode === 'export'
          ? '账号包包含全部凭证与账号元数据；使用 AES-256-GCM 口令加密，口令不会上传或保存。'
          : '支持 .aether-accounts 文件；同渠道同身份会跳过，不会产生重复账号。');
        let dlg = null;
        const go = btn(mode === 'export' ? '导出账号包' : '导入账号包', { primary: true, onClick: async (e) => {
          const el = e.currentTarget;
          const phrase = (pass.value || '').trim();
          if (phrase.length < 8) { toast('口令至少 8 个字符'); return; }
          el.disabled = true;
          try {
            if (mode === 'export') {
              const r = await api.post('accounts/export/bundle', { passphrase: phrase });
              const bin = atob(r.data || '');
              const bytes = new Uint8Array(bin.length);
              for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
              const blob = new Blob([bytes], { type: 'application/octet-stream' });
              const a = document.createElement('a');
              a.href = URL.createObjectURL(blob);
              a.download = r.filename || 'aether-accounts.aether-accounts';
              a.click();
              setTimeout(() => URL.revokeObjectURL(a.href), 3000);
              status.textContent = '已导出 ' + Number(r.accounts || 0) + ' 个账号；请与口令分开保存。';
              toast('账号包已导出');
            } else {
              const f = file.files && file.files[0];
              if (!f) { toast('请选择账号包文件'); el.disabled = false; return; }
              const buf = new Uint8Array(await f.arrayBuffer());
              let bin = '';
              for (let i = 0; i < buf.length; i++) bin += String.fromCharCode(buf[i]);
              const r = await api.post('accounts/import/bundle', { passphrase: phrase, data: btoa(bin) });
              const skipped = (r.skipped || []).length;
              const failed = (r.failed || []).length;
              status.textContent = '已导入 ' + (r.imported || []).length + ' 个账号' + (skipped ? '，跳过重复 ' + skipped : '') + (failed ? '，失败 ' + failed : '');
              toast('账号包已导入 ' + (r.imported || []).length + ' 个账号');
              if (dlg) dlg.close();
              await load();
            }
          } catch (err) {
            status.textContent = (mode === 'export' ? '导出失败：' : '导入失败：') + (err.message || err);
            toast(status.textContent);
          }
          if (el.isConnected) el.disabled = false;
        } });
        dlg = drawer(mode === 'export' ? '导出加密账号包' : '导入加密账号包', (body) => {
          body.append(field('口令', pass));
          if (file) body.append(field('账号包文件', file));
          body.append(h('div', { class: 'inline', style: 'justify-content:flex-end' }, go), status);
        });
      }

      return [
        section('本机备份', [
          line('导出备份', '设置、分组、spec、账号标签；不含账号凭证', btn('导出 JSON', { onClick: async () => {
            try {
              const res = await fetch('/api/backup/export', { method: 'POST' });
              if (!res.ok) throw new Error(await res.text());
              const blob = await res.blob();
              const a = document.createElement('a');
              a.href = URL.createObjectURL(blob);
              a.download = 'aether-backup.json';
              a.click();
              setTimeout(() => URL.revokeObjectURL(a.href), 3000);
            } catch (e) { toast('导出失败：' + (e.message || e)); }
          } })),
          line('导入备份', '选择之前导出的 JSON；导入后账号可能需要补密钥', h('div', { class: 'inline' }, file, btn('选择文件', { onClick: () => file.click() }))),
        ]),
        section('账号迁移', [
          line('导出加密账号包', '包含账号凭证、标签、备注、优先级与权重；跨机迁移用', btn('导出账号包', { onClick: () => bundleDialog('export') })),
          line('导入加密账号包', '同渠道同身份自动跳过；不同账号恢复原标签/备注/优先级', btn('导入账号包', { onClick: () => bundleDialog('import') })),
        ]),
        section('云同步', [
          line('WebDAV / S3 / GitHub', '把同一份备份推到云端；设置、分组、spec、账号标签，不含凭证', h('div', { class: 'inline' }, btn('配置云同步', { onClick: davDialog }))),
        ]),
        section('目录', [line('数据目录', '备份、账本与账号都在这', h('span', { class: 'mono meta' }, s.dir || '—'))]),
      ];
    }

    async function davDialog() {
      let v = {};
      try { v = await api.get('davsync'); }
      catch (e) { toast('读取云同步配置失败：' + (e.message || e)); return; }
      const kind = select([{ value: 'webdav', label: 'WebDAV' }, { value: 's3', label: 'S3' }, { value: 'github', label: 'GitHub' }], v.kind || 'webdav', () => paint());
      const url = input({ value: v.url || '', placeholder: 'WebDAV 目录 URL / s3://bucket/前缀 / github://owner/repo/目录' });
      const user = input({ value: v.user || '', placeholder: '用户名 / Access Key' });
      const pass = input({ type: 'password', placeholder: v.passwordSet ? '已保存，留空保留' : '密码 / Secret / Token' });
      const branch = input({ value: v.branch || '', placeholder: 'GitHub 分支（可选）' });
      const endpoint = input({ value: v.endpoint || '', placeholder: 'S3 Endpoint（可选）' });
      const region = input({ value: v.region || '', placeholder: 'S3 Region（可选）' });
      const auto = input({ type: 'number', value: v.auto ? String(v.auto) : '0', placeholder: '每 N 分钟自动上传，0=关' });
      const phrase = input({ type: 'password', placeholder: v.passphraseSet ? '已设置，留空保留' : '至少 8 位，用于加密云端备份' });
      const extra = h('div', { style: 'display:flex;flex-direction:column;gap:12px' });
      const status = h('div', { class: 'meta' });
      function paintStatus(view) {
        const bits = [];
        bits.push(view.on ? '已配置 ' + view.kind : '未配置');
        bits.push(view.passphraseSet ? '口令已加密' : '未设口令');
        if (view.remote) bits.push('远端 ' + view.remote);
        if (view.last) bits.push('上次 ' + fmtDate(Math.floor(view.last / 1000), true));
        if (view.bytes) bits.push(fmtBytes(view.bytes));
        if (view.error) bits.push('错误：' + view.error);
        status.textContent = bits.join(' · ');
      }
      function paint() {
        clear(extra);
        extra.append(field('地址', url), field('账号', user), field('密钥', pass));
        if (kind.value === 'github') extra.append(field('分支', branch));
        if (kind.value === 's3') extra.append(field('Endpoint', endpoint), field('Region', region));
        extra.append(field('备份口令', phrase), h('div', { class: 'meta' }, '口令只保存在本机，不会上传；忘记口令将无法从云端恢复。'));
        extra.append(field('自动上传间隔（分钟）', auto));
      }
      paint();
      paintStatus(v);
      const d = drawer('云同步', (body) => {
        body.append(status, field('类型', kind), extra);
        body.append(h('div', { class: 'inline', style: 'justify-content:flex-end;flex-wrap:wrap' },
          btn('关闭同步', { ghost: true, onClick: async () => {
            if (!(await confirmDialog('关闭云同步？', '只删掉同步配置，本机数据不动。', { danger: true, confirmText: '关闭' }))) return;
            try { const out = await api.post('davsync/off', {}); paintStatus(out); toast('已关闭'); }
            catch (e) { toast('关闭失败：' + (e.message || e)); }
          } }),
          btn('从云端恢复', { onClick: async () => {
            if (!(await confirmDialog('从云端恢复？', '会用远端备份覆盖本机设置与分组（账号凭证不恢复），恢复前会留撤销快照。', { danger: true, confirmText: '恢复' }))) return;
            try { const out = await api.post('davsync/restore', {}); paintStatus(out); toast(out.error ? '恢复失败：' + out.error : '已从云端恢复'); await load(); }
            catch (e) { toast('恢复失败：' + (e.message || e)); }
          } }),
          btn('立即上传', { primary: true, onClick: async () => {
            try {
              const out = await api.post('davsync/now', {});
              paintStatus(out);
              toast(out.error ? '上传失败：' + out.error : '已上传 ' + (out.remote || ''));
            } catch (e) { toast('上传失败：' + (e.message || e)); }
          } }),
          btn('保存', { onClick: async () => {
            const phraseValue = phrase.value.trim();
            if (!v.passphraseSet && phraseValue.length < 8) { toast('云同步必须设置至少 8 位备份口令'); return; }
            if (phraseValue && phraseValue.length < 8) { toast('口令至少 8 个字符'); return; }
            const body = { kind: kind.value, url: url.value.trim(), user: user.value.trim(), password: pass.value, passphrase: phraseValue };
            if (kind.value === 'github') body.branch = branch.value.trim();
            if (kind.value === 's3') { body.endpoint = endpoint.value.trim(); body.region = region.value.trim(); }
            try {
              const out = await api.post('davsync/save', body);
              await api.post('davsync/auto', { minutes: Number(auto.value) || 0 });
              paintStatus(out);
              pass.value = '';
              phrase.value = '';
              v.passphraseSet = out.passphraseSet;
              toast('已保存');
            } catch (e) { toast('保存失败：' + (e.message || e)); }
          } })));
      });
    }

    function pricingTab() {
      const p = state.pricing || { currency: 'USD', usd_cny: 7.2, defaults: [], overrides: {} };
      const cur = p.currency === 'CNY' ? 'CNY' : 'USD';
      const rate = Number(p.usd_cny) || 7.2;
      const symbol = cur === 'CNY' ? '¥' : '$';
      const disp = (usd) => (cur === 'CNY' ? (Number(usd) || 0) * rate : (Number(usd) || 0));
      const fmt = (usd) => {
        const v = disp(usd);
        if (!v) return symbol + '0';
        if (v < 0.01) return symbol + v.toFixed(4);
        if (v < 1) return symbol + v.toFixed(3);
        return symbol + v.toFixed(2);
      };
      const overrides = p.overrides || {};
      const defaults = p.defaults || [];
      const catalog = new Map(defaults.map((x) => [x.pattern, x.price]));
      for (const k of Object.keys(overrides)) if (!catalog.has(k)) catalog.set(k, overrides[k]);
      const patterns = [...catalog.keys()].sort();
      const c = card('模型定价', { chips: [chip('官方价 + 自定义覆盖', 'plain')] });
      c.head.append(h('span', { style: 'flex:1' }));

      const currency = h('div', { class: 'inline' },
        ...['USD', 'CNY'].map((x) => btn(x === 'USD' ? '$ USD' : '¥ CNY', {
          class: 'sm' + (cur === x ? ' on' : ''),
          onClick: () => savePricing({ price_currency: x }, '币种'),
        })),
        h('span', { class: 'meta' }, '1 USD ='),
        (() => {
          const inp = input({ value: String(rate), label: '美元人民币汇率' });
          inp.style.width = '74px';
          inp.addEventListener('change', () => savePricing({ usd_cny: Math.max(0.01, Number(inp.value) || rate) }, '汇率'));
          return inp;
        })(),
        h('span', { class: 'meta' }, 'CNY'));
      c.head.append(currency);
      c.head.append(btn('新增自定义价格', { primary: true, icon: 'plus', onClick: () => priceDialog('', null, null) }));

      if (!patterns.length) c.el.append(empty('没有价格数据', '后端未返回定价目录。'));
      const list = h('div', { class: 'list', style: 'max-height:520px;overflow:auto;gap:2px' });
      for (const pattern of patterns) {
        const base = catalog.get(pattern) || {};
        const override = overrides[pattern];
        const price = override || base;
        const tags = [];
        if (override) tags.push(chip('自定义', 'green'));
        else tags.push(chip('官方', 'plain'));
        if (price.peak) tags.push(chip('峰谷', 'plain'));
        if (price.tiers && price.tiers.length) tags.push(chip('上下文档位', 'plain'));
        const extra = [];
        if (price.peak) extra.push('高峰 ' + fmt(price.peak.input_per_m) + ' / ' + fmt(price.peak.output_per_m));
        if (price.tiers && price.tiers.length) {
          for (const t of price.tiers) extra.push('≥' + Math.round((t.min_input_tokens || 0) / 1000) + 'K：' + fmt(t.input_per_m) + ' / ' + fmt(t.output_per_m));
        }
        list.append(h('div', { class: 'row', style: 'align-items:flex-start;flex-wrap:wrap' },
          h('span', { class: 'mono', style: 'min-width:210px;font-size:12px' }, pattern),
          ...tags,
          h('span', { class: 'meta' }, '输入 ' + fmt(price.input_per_m) + ' · 输出 ' + fmt(price.output_per_m)),
          h('span', { class: 'meta' }, '缓存读 ' + fmt(price.cached_read_per_m || 0)),
          h('span', { class: 'grow' }),
          btn('编辑', { ghost: true, onClick: () => priceDialog(pattern, base, override || null) }),
          override ? btn('重置', { ghost: true, onClick: () => resetPrice(pattern) }) : null));
        if (extra.length) list.append(h('div', { class: 'meta', style: 'margin-top:-4px;padding-left:2px' }, extra.join(' · ') + (price.note ? ' · ' + price.note : '')));
      }
      c.el.append(list);
      return [c.el, h('div', { class: 'meta', style: 'padding:8px 2px' }, '价格单位：每百万 tokens；内部统一按美元存储，展示按所选币种与汇率换算。DeepSeek 官方价按峰谷时段自动切换；GPT / Gemini 长上下文按档位自动加价。')];
    }

    async function savePricing(patch, label) {
      try {
        state.pricing = await api.post('pricing/save', patch);
        toast(label + '已保存');
        render();
      } catch (e) { toast('保存失败：' + (e.message || e)); }
    }

    function resetPrice(pattern) {
      const next = { ...((state.pricing || {}).overrides || {}) };
      delete next[pattern];
      savePricing({ model_prices: next }, '定价');
    }

    function priceDialog(pattern, base, override) {
      const p = state.pricing || { currency: 'USD', usd_cny: 7.2, overrides: {} };
      const cur = p.currency === 'CNY' ? 'CNY' : 'USD';
      const rate = Number(p.usd_cny) || 7.2;
      const symbol = cur === 'CNY' ? '¥' : '$';
      const toUSD = (v) => (cur === 'CNY' ? v / rate : v);
      const fromUSD = (v) => (cur === 'CNY' ? v * rate : v);
      const editing = !!pattern;
      const patternInput = input({ value: pattern || '', placeholder: '例如 gpt-5.4* 或 deepseek-*' });
      if (editing) { patternInput.disabled = true; }
      const basePrice = override || base || {};
      const fields = {};
      for (const f of [['input_per_m', '输入'], ['output_per_m', '输出'], ['cached_read_per_m', '缓存读取'], ['cached_write_per_m', '缓存写入']]) {
        const [key, label] = f;
        const el = input({ value: String(Number(fromUSD(basePrice[key] || 0)).toFixed(6)), label: label + '价格' });
        fields[key] = { el, label };
      }
      const d = drawer(editing ? '编辑价格 · ' + pattern : '新增自定义价格', (box) => {
        const body = h('div', { class: 'detail-fields' });
        body.append(field('模型 / 通配前缀', patternInput));
        for (const { el, label } of Object.values(fields)) body.append(field(label + '（' + symbol + ' / 百万 tokens）', el));
        box.append(
          body,
          h('div', { class: 'meta' }, '修改基础价会保留该模型的峰谷与上下文档位规则；新增通配符时请以 * 结尾，例如 gpt-5.4*。'),
          h('div', { class: 'inline', style: 'justify-content:flex-end' },
            btn('取消', { onClick: () => d.close() }),
            btn('保存', { primary: true, onClick: async () => {
              const pat = patternInput.value.trim();
              if (!pat) { toast('模型或通配前缀不能为空'); return; }
              const next = { ...((state.pricing || {}).overrides || {}) };
              const price = { ...(override || base || {}) };
              for (const [key, { el, label }] of Object.entries(fields)) {
                const v = Number(el.value);
                if (!Number.isFinite(v) || v < 0) { toast(label + '价格不合法'); return; }
                price[key] = toUSD(v);
              }
              next[pat] = price;
              try {
                state.pricing = await api.post('pricing/save', { model_prices: next });
                d.close();
                toast('价格已保存');
                render();
              } catch (e) { toast('保存失败：' + (e.message || e)); }
            } })));
      });
    }

    function presetsTab() {
      const p = state.p || { presets: [], provider_presets: {}, route_presets: {} };
      const usage = (id) => {
        let providers = 0;
        let routes = 0;
        for (const ids of Object.values(p.provider_presets || {})) if ((ids || []).includes(id)) providers++;
        for (const ids of Object.values(p.route_presets || {})) if ((ids || []).includes(id)) routes++;
        return { providers, routes };
      };
      const c = card('请求预设', { chips: [chip(p.presets.length + ' 个', 'plain')] });
      c.head.append(h('span', { style: 'flex:1' }));
      // 酒馆预设导入：文件只在本地读，解析交给网关（POST /api/presets/import/preview，只读）。
      const importInput = h('input', {
        type: 'file', accept: 'application/json,.json', style: 'display:none',
        onchange: async (e) => {
          const file = e.target.files && e.target.files[0];
          e.target.value = '';
          if (!file) return;
          try {
            const text = await file.text();
            const preview = await api.post('presets/import/preview', { format: 'auto', json: text });
            presetImportDialog(preview, file.name);
          } catch (err) { toast('导入失败：' + (err.message || err)); }
        },
      });
      c.head.append(importInput);
      c.head.append(btn('导入预设', { ghost: true, onClick: () => importInput.click() }));
      c.head.append(btn('新建预设', { primary: true, icon: 'plus', onClick: () => presetDialog(null) }));
      if (!p.presets.length) {
        c.el.append(empty('还没有预设', '新建一个 Body / Headers 预设，然后在渠道或路由组里勾选。'));
      }
      for (const preset of p.presets) {
        const u = usage(preset.id);
        const bodyCount = Object.keys(preset.body || {}).length;
        const headerCount = Object.keys(preset.headers || {}).length;
        const attached = [u.providers ? u.providers + ' 渠道' : '', u.routes ? u.routes + ' 路由' : ''].filter(Boolean).join(' · ') || '未挂载';
        const enabledSwitch = toggle(!!preset.enabled, async (on) => {
          try {
            state.p = await api.post('presets/save', { ...preset, enabled: on });
            toast(on ? '预设已启用' : '预设已停用');
            render();
          } catch (e) { toast('切换失败：' + (e.message || e)); }
        }, { label: '启用预设 ' + (preset.name || preset.id) });
        c.el.append(h('div', { class: 'row', style: 'align-items:flex-start;flex-wrap:wrap' },
          h('span', { class: 'name' }, preset.name || preset.id),
          enabledSwitch,
          chip('Body ' + bodyCount, 'plain'),
          chip('Headers ' + headerCount, 'plain'),
          preset.system_prompt ? chip('提示词', 'green') : null,
          h('span', { class: 'grow' }),
          h('span', { class: 'meta' }, attached),
          btn('编辑', { ghost: true, onClick: () => presetDialog(preset) }),
          btn('删除', { danger: true, ghost: true, onClick: async () => {
            if (!(await confirmDialog('删除预设「' + (preset.name || preset.id) + '」？', '渠道/路由上的挂载会一并移除。', { danger: true, confirmText: '删除' }))) return;
            try {
              state.p = await api.post('presets/delete', { id: preset.id });
              toast('已删除');
              render();
            } catch (e) { toast('删除失败：' + (e.message || e)); }
          } })));
        if (preset.description) c.el.append(h('div', { class: 'meta', style: 'margin-top:-4px' }, preset.description));
      }
      return [c.el, h('div', { class: 'meta', style: 'padding:8px 2px' }, '预设只做静态 Body / Headers 合并；停用或没有挂载时不会生效。')];
    }

    function presetDialog(preset) {
      const editing = !!preset;
      const name = input({ value: preset?.name || '', placeholder: '例如：Codex 缓存' });
      const desc = input({ value: preset?.description || '', placeholder: '可选' });
      let enabled = preset ? !!preset.enabled : true;
      const enabledBtn = toggle(enabled, (on) => { enabled = on; });
      let concatSystem = !!preset?.concat_system;
      const concatBtn = toggle(concatSystem, (on) => { concatSystem = on; });
      const body = textarea({ value: JSON.stringify(preset?.body || {}, null, 2), placeholder: '{\n  "prompt_cache_key": ""\n}' });
      const headers = textarea({ value: JSON.stringify(preset?.headers || {}, null, 2), placeholder: '{\n  "x-client": "codex"\n}' });
      const systemPrompt = textarea({ value: preset?.system_prompt || '', placeholder: '输入系统提示词（用户提示词优先）' });
      const d = drawer(editing ? '编辑预设 · ' + (preset.name || preset.id) : '新建预设', (box) => {
        box.append(
          field('名称', name),
          field('说明（可选）', desc),
          field('启用', enabledBtn),
          field('系统提示词', systemPrompt),
          field('提示词拼接', h('div', { class: 'inline' }, concatBtn, h('span', { class: 'meta' }, '开启后与用户提示词拼接'))),
          field('Body（JSON 对象）', body),
          field('Headers（JSON 对象）', headers),
          h('div', { class: 'meta' }, 'Body 不能覆盖 model / stream / messages / tools 等网关自管字段；不要放长期密钥；Headers 里的敏感值会打码回显；同名 Header 不会覆盖渠道代码已设置的头。'),
          h('div', { class: 'inline', style: 'justify-content:flex-end' },
            btn('取消', { onClick: () => d.close() }),
            btn('保存', { primary: true, onClick: async () => {
              const n = name.value.trim();
              if (!n) { toast('名称不能为空'); return; }
              let b;
              let hd;
              try { b = JSON.parse(body.value.trim() || '{}'); } catch { toast('Body 不是合法 JSON'); return; }
              try { hd = JSON.parse(headers.value.trim() || '{}'); } catch { toast('Headers 不是合法 JSON'); return; }
              if (!b || typeof b !== 'object' || Array.isArray(b)) { toast('Body 必须是 JSON 对象'); return; }
              if (!hd || typeof hd !== 'object' || Array.isArray(hd)) { toast('Headers 必须是 JSON 对象'); return; }
              try {
                state.p = await api.post('presets/save', {
                  id: preset?.id || '', name: n, description: desc.value.trim(), enabled,
                  body: b, headers: hd,
                  system_prompt: systemPrompt.value, concat_system: !!concatSystem,
                });
                d.close();
                toast('预设已保存');
                render();
              } catch (e) { toast('保存失败：' + (e.message || e)); }
            } })));
      });
    }

    /* 酒馆预设导入预览：列出将写入的采样参数与将丢弃的字段（分组 + 原因），
       提示词可单独开关；超过 32KB 上限时只允许导入采样参数。 */
    function presetImportDialog(preview, fileName) {
      const overflow = !!preview.prompt_overflow;
      const promptText = (preview.system_prompt || '').trim();
      const hasPrompt = promptText.length > 0;
      let includePrompt = hasPrompt && !overflow;
      const includeBtn = toggle(includePrompt, (on) => { includePrompt = on; }, { label: '导入提示词' });
      const mapped = Array.isArray(preview.mapped) ? preview.mapped : [];
      const dropped = Array.isArray(preview.dropped) ? preview.dropped : [];
      const groups = [
        ['upstream', '上游不通用（发了会 400 或被忽略）'],
        ['client', '客户端行为（不是请求参数）'],
        ['gateway', '网关自管（写进预设会被拒绝）'],
        ['unknown', '未识别字段'],
      ];
      const promptLine = !hasPrompt
        ? h('div', { class: 'meta' }, '该文件没有可导入的提示词（prompts 里没有启用且有内容的段落）。')
        : overflow
          ? h('div', { class: 'meta', style: 'color:var(--danger,#c0392b)' },
            '提示词 ' + (preview.prompt_chars || 0) + ' 字超过预设 32KB 上限，本次只能导入采样参数（不截断）。')
          : h('div', { class: 'inline' }, includeBtn,
            h('span', { class: 'meta' },
              '将写入 system_prompt：' + (preview.prompt_segments || 0) + ' 段（共 ' + (preview.prompt_chars || 0) + ' 字）'));
      const box = h('div', { style: 'display:flex;flex-direction:column;gap:6px' },
        h('div', { class: 'meta' }, '来源格式：' + (preview.format === 'chat' ? 'Chat Completion 预设'
          : preview.format === 'text' ? 'Text Generation 预设' : '未识别格式')),
        promptLine,
        mapped.length ? h('div', { class: 'meta' }, '将写入采样参数（' + mapped.length + '）') : null,
        ...mapped.map((item) => h('div', { class: 'meta' }, '· ' + item.key + ' ← ' + (item.source || item.key))),
        ...groups.flatMap((group) => {
          const items = dropped.filter((item) => (item.group || 'unknown') === group[0]);
          if (!items.length) return [];
          return [
            h('div', { class: 'meta', style: 'margin-top:4px' }, group[1] + '（' + items.length + '）'),
            ...items.map((item) => h('div', { class: 'meta' }, '· ' + item.key + ' — ' + (item.reason || '未说明'))),
          ];
        }),
        h('div', { class: 'meta' }, '导入后可以在列表里继续编辑；Body 里不会包含提示词。'));
      const d = drawer('导入预设 · ' + (preview.name || fileName || '未命名'), (content) => {
        content.append(box);
        content.append(h('div', { class: 'inline', style: 'justify-content:flex-end' },
          btn('取消', { onClick: () => d.close() }),
          btn('导入', { primary: true, onClick: async () => {
            const payload = {
              name: (preview.name || fileName || '导入的预设').replace(/\.json$/i, ''),
              description: '从酒馆预设导入（' + (preview.format || 'unknown') + '）',
              enabled: true,
              body: preview.body || {},
              headers: {},
              system_prompt: includePrompt && !overflow ? promptText : '',
              concat_system: false,
            };
            try {
              state.p = await api.post('presets/save', payload);
              d.close();
              toast('预设已导入');
              render();
            } catch (e) { toast('保存失败：' + (e.message || e)); }
          } })));
      });
    }

    async function checkUpdate(opts = {}) {
      if (state.updateBusy) return;
      state.updateBusy = true;
      if (state.tab === 'about') render();
      try {
        const [v, u] = await Promise.all([api.version(), api.get('update').catch(() => ({}))]);
        state.v = { ...state.v, ...v };
        state.update = u;
        state.updateBusy = false;
        if (state.tab === 'about') render();
        if (u.available) {
          const skipped = storage.get(UPDATE_SKIP_KEY) === String(u.latest);
          if (opts.silent) return;
          if (skipped) toast('已跳过 v' + u.latest + '；可点「查看 v' + u.latest + '」重新打开');
          else openUpdateModal(u, 'update');
        } else if (u.reason) {
          if (!opts.silent) toast('未检查更新：' + u.reason);
        } else if (!opts.silent) {
          toast('已是最新版本 ' + (u.latest || u.current || v.version || ''));
        }
      } catch (e) {
        state.updateBusy = false;
        if (state.tab === 'about') render();
        if (!opts.silent) toast('检查失败：' + (e.message || e));
      }
    }

    async function openChangelog() {
      let u = state.update;
      if (!u) {
        try {
          u = await api.get('update');
          state.update = u;
          render();
        } catch (e) { toast('更新记录读取失败：' + (e.message || e)); return; }
      }
      if (u && u.notes) openUpdateModal(u, 'notes');
      else openInBrowser(repoHome(u && u.repo) + '/releases');
    }

    function aboutLink(iconName, title, desc, onClick) {
      return h('button', { class: 'about-link', type: 'button', onclick: onClick },
        h('span', { class: 'about-ic' }, icon(iconName, 19)),
        h('b', null, title),
        h('span', { class: 'meta' }, desc));
    }

    function starBanner(repo) {
      if (storage.get(STAR_DISMISS_KEY) === '1') return null;
      const banner = h('div', { class: 'star-banner' },
        h('span', { class: 'star-ic' }, icon('star', 18)),
        h('div', { class: 'star-copy' },
          h('b', null, '如果 Aether Cockpit 帮到了你'),
          h('span', { class: 'meta' }, '请在 GitHub 点个 Star，这对我们意义重大')),
        btn('去点 Star', { icon: 'github', onClick: () => openInBrowser(repoHome(repo)) }),
        iconBtn('x', '关闭 Star 提示', () => { storage.set(STAR_DISMISS_KEY, '1'); banner.remove(); }));
      return banner;
    }

    function updateSourceCard(s) {
      const repoInp = input({ value: s.update_repo || '', placeholder: UPDATE_REPO_FALLBACK, label: 'GitHub 仓库' });
      const tokenInp = input({ type: 'password', value: '', placeholder: s.update_token ? '已保存（打码）' : '私有仓库需要；公开仓库可留空', label: 'GitHub 访问令牌' });
      const saveBtn = btn('保存更新源', { onClick: async () => {
        try {
          await api.post('settings', { update_repo: repoInp.value.trim(), update_token: tokenInp.value.trim() });
          state.update = null;
          toast('更新源已保存');
          await load();
        } catch (e) { toast('保存失败：' + (e.message || e)); }
      } });
      const clearBtn = s.update_token ? btn('清除令牌', { ghost: true, onClick: async () => {
        try {
          await api.post('settings', { update_token: '-' });
          state.update = null;
          toast('令牌已清除');
          await load();
        } catch (e) { toast('清除失败：' + (e.message || e)); }
      } }) : null;
      return section('GitHub 更新源', [
        line('仓库', 'owner/repo；留空使用 ' + UPDATE_REPO_FALLBACK, repoInp),
        line('访问令牌', '仅私有仓库需要；保存在本机 settings.json，回读打码', tokenInp),
        line('操作', '检查更新会读取 GitHub Releases 的最新版本与发布说明', h('div', { class: 'inline' }, saveBtn, clearBtn)),
      ]);
    }

    function sponsorDialog(repo) {
      const donateURL = repoHome(UPDATE_REPO_FALLBACK) + '/blob/master/docs/DONATE.md';
      const wrap = h('div', { class: 'modal-wrap' });
      const close = () => {
        release();
        wrap.classList.remove('on');
        document.removeEventListener('keydown', onKey, true);
        setTimeout(() => wrap.remove(), 160);
      };
      const box = h('div', { class: 'modal donate-modal', role: 'dialog', 'aria-modal': 'true', 'aria-label': '赞助支持' },
        h('div', { class: 'update-head' },
          h('div', { class: 'about-brand sm' }, brandMark(26)),
          h('div', { class: 'grow' },
            h('h2', null, '赞助支持'),
            h('p', null, '如果 Aether Cockpit 帮到了你')),
          iconBtn('x', '关闭', close)),
        h('div', { class: 'donate-body' },
          h('p', { class: 'donate-intro' }, '开源项目的持续维护离不开每一份支持。可以点 Star、分享项目，或前往 GitHub 赞助页支持持续开发。'),
          h('div', { class: 'donate-actions' },
            btn('去赞助', { primary: true, icon: 'heart', onClick: () => openInBrowser(donateURL) }),
            btn('去点 Star', { icon: 'github', onClick: () => openInBrowser(repoHome(repo)) }),
            btn('分享项目', { icon: 'link', onClick: async () => {
              const url = repoHome(repo);
              if (navigator.share) {
                try { await navigator.share({ title: 'Aether Cockpit', url }); return; } catch { /* 用户取消 */ }
              }
              await copyText(url);
            } }),
            btn('意见反馈', { icon: 'message', onClick: () => openInBrowser(repoHome(repo) + '/issues') })),
          h('div', { class: 'donate-note' }, '赞助完全自愿，不影响任何功能。感谢每一份支持！')),
        h('div', { class: 'donate-foot' },
          btn('关闭', { onClick: close })));
      wrap.append(box);
      document.body.append(wrap);
      const onKey = (e) => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); } };
      document.addEventListener('keydown', onKey, true);
      const release = trapFocus(box);
      requestAnimationFrame(() => wrap.classList.add('on'));
    }

    function openUpdateModal(u, mode = 'update') {
      const isUpdate = mode !== 'notes';
      const repo = u.repo || state.s?.update_repo || UPDATE_REPO_FALLBACK;
      const wrap = h('div', { class: 'modal-wrap' });
      const close = () => {
        release();
        wrap.classList.remove('on');
        document.removeEventListener('keydown', onKey, true);
        setTimeout(() => wrap.remove(), 160);
      };
      const downloadBtn = btn(u.downloadable ? '立即更新' : '打开下载页', { primary: true, icon: u.downloadable ? 'download' : 'external' });
      const status = h('span', { class: 'update-status' });
      const actions = h('div', { class: 'update-actions' });
      if (isUpdate) {
        downloadBtn.addEventListener('click', async () => {
          if (!u.downloadable) {
            openInBrowser(u.html_url || repoHome(repo) + '/releases');
            return;
          }
          downloadBtn.disabled = true;
          downloadBtn.textContent = '下载中…';
          status.textContent = '';
          try {
            const r = await api.post('update/download', {
              url: u.url, sha256: u.sha256 || '', name: u.asset_name || '', github: u.github === true,
            });
            downloadBtn.textContent = '已下载';
            status.textContent = '已保存到 ' + r.path + (r.verified ? '（SHA256 已校验）' : '');
            toast('更新包已下载');
          } catch (e) {
            downloadBtn.disabled = false;
            downloadBtn.textContent = '立即更新';
            toast('下载失败：' + (e.message || e));
          }
        });
        actions.append(
          btn('取消', { onClick: close }),
          btn('跳过此版本', { onClick: () => {
            storage.set(UPDATE_SKIP_KEY, String(u.latest || ''));
            close();
            toast('已跳过 v' + u.latest);
            if (state.tab === 'about') render();
          } }),
          downloadBtn);
      } else {
        actions.append(
          btn('关闭', { onClick: close }),
          u.html_url ? btn('打开 GitHub', { primary: true, icon: 'external', onClick: () => openInBrowser(u.html_url) }) : null);
      }
      const box = h('div', { class: 'modal update-modal', role: 'dialog', 'aria-modal': 'true', 'aria-label': isUpdate ? '发现新版本' : '更新记录' },
        h('div', { class: 'update-head' },
          h('div', { class: 'about-brand sm' }, brandMark(26)),
          h('div', { class: 'grow' },
            h('h2', null, isUpdate ? '发现新版本' : '更新记录'),
            h('p', null, u.latest
              ? (isUpdate ? '当前版本 v' + (u.current || state.v.version || 'dev') + '，新版本 v' + u.latest + ' 已可用。' : 'GitHub Releases · v' + u.latest)
              : 'GitHub Releases')),
          iconBtn('x', '关闭', close)),
        h('div', { class: 'update-body' },
          h('div', { class: 'update-ver' }, 'v' + (u.latest || '—')),
          u.published_at && fmtPublished(u.published_at) ? h('div', { class: 'update-meta' }, '发布于 ' + fmtPublished(u.published_at)) : null,
          h('div', { class: 'update-sep' }),
          h('h3', null, '更新内容'),
          h('div', { class: 'update-notes' }, ...noteNodes(u.notes))),
        h('div', { class: 'update-foot' }, status, actions));
      wrap.append(box);
      document.body.append(wrap);
      const onKey = (e) => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); } };
      document.addEventListener('keydown', onKey, true);
      const release = trapFocus(box);
      requestAnimationFrame(() => wrap.classList.add('on'));
    }

    function updateStatus(u, v, repo) {
      if (state.updateBusy) return h('div', { class: 'meta about-status' }, '正在检查 GitHub Releases…');
      if (!u) return h('div', { class: 'meta about-status' }, '更新源：GitHub · ' + repo);
      if (u.available) {
        // 与手机端同一套口径：把"这个 Release 有没有本平台安装包"说清楚。
        // u.downloadable 由 Go 侧 pickGitHubAsset(runtime.GOOS) 给出（update_api.go:258）。
        const pkg = u.downloadable === false ? ' · 本平台无安装包' : ' · 含本平台安装包';
        return h('div', { class: 'inline about-status' },
          chip('可更新 v' + u.latest, 'green'),
          h('span', { class: 'meta' }, '当前 v' + (u.current || v.version || 'dev') + pkg));
      }
      if (u.reason) return h('div', { class: 'meta about-status' }, u.reason);
      return h('div', { class: 'meta about-status' }, '已是最新 v' + (u.latest || u.current || v.version || '—'));
    }

    function about(s) {
      const v = state.v || {};
      const u = state.update || null;
      const repo = (u && u.repo) || s.update_repo || UPDATE_REPO_FALLBACK;
      const owner = repo.split('/')[0] || 'lyydfys';
      const star = starBanner(repo);
      return [
        h('div', { class: 'about-hero' },
          h('div', { class: 'about-brand' }, brandMark(46)),
          h('div', { class: 'about-name' }, 'Aether Cockpit'),
          h('div', { class: 'about-badges' },
            h('span', { class: 'about-version' }, 'v' + (v.version || 'dev')),
            btn('检查更新', { icon: 'refresh', onClick: () => checkUpdate() }),
            btn('更新记录', { icon: 'logs', ghost: true, onClick: openChangelog }),
            u && u.available ? btn('查看 v' + u.latest, { primary: true, onClick: () => openUpdateModal(u, 'update') }) : null),
          h('div', { class: 'about-sub' }, 'AI 算力聚合网关 · 本地优先 · 数据不出本机'),
          updateStatus(u, v, repo)),
        h('div', { class: 'about-links' },
          aboutLink('users', '主作者', owner, () => openInBrowser('https://github.com/' + owner)),
          aboutLink('github', '开源仓库', repo, () => openInBrowser(repoHome(repo))),
          aboutLink('heart', '赞助支持', '支持项目持续开发', () => sponsorDialog(repo)),
          aboutLink('message', '意见反馈', '报告问题或提交建议', () => openInBrowser(repoHome(repo) + '/issues'))),
        ...(star ? [star] : []),
        updateSourceCard(s),
        section('构建信息', [
          line('版本号', '构建时从仓库根 VERSION 注入', h('span', { class: 'mono meta' }, v.version || 'dev')),
          line('构建时间', '', h('span', { class: 'mono meta' }, fmtPublished(v.build_time) || '—')),
          line('版本来源', 'ldflags = 发布构建；vcs-commit = 本地 go build', h('span', { class: 'mono meta' }, v.build_time_source || 'unknown')),
          line('Git 修订', '', h('span', { class: 'mono meta' }, v.revision || '—')),
          line('监听地址', '', h('span', { class: 'mono meta' }, s.addr || '—')),
        ]),
        section('数据与日志', [
          line('数据目录', '账号、设置、用量与唤醒任务；删除即重置本机数据', h('span', { class: 'mono meta' }, 'data/')),
          line('日志文件', '滚动日志：单文件 5 MB，保留一份历史', h('span', { class: 'mono meta' }, 'data/logs/aether.log')),
          line('发布包', 'scripts/release.sh 生成 zip + SHA256SUMS', h('span', { class: 'mono meta' }, 'dist/')),
        ]),
      ];
    }
  },
};
