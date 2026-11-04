/* 自定义渠道抽屉：运行时把「地址 + 模型名」注册成一个渠道。
   上半列出已保存的渠道（名称 / Base URL / 协议 / 模型数 / 编辑 / 删除），
   下半是新建与编辑共用的一份表单，列表里的「编辑」直接回填它。

   两个刻意的取舍（与后端 custom_channels.go 的注释一致）：
   - 密钥不进渠道配置：探测用的 API Key 只活在这个抽屉的内存里，保存时不提交；
     密钥归账号（加密存储），删渠道不会顺带删掉账号的密钥。
   - 探测是显式动作：多数中转的 /models 要鉴权，静默探测只会刷失败记录，
     所以只在用户点「探测模型」时请求一次，失败原因直接写回表单。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { btn, chip, field, input, select, textarea, drawer, toast, errorBox, confirmDialog } from '../components.js';
import { openProvider } from './onboard.js';

/* 协议决定网关走哪个上游端点；顺序按使用频率排。 */
const PROTOCOLS = [
  { value: 'openai', label: 'OpenAI Chat（/chat/completions）' },
  { value: 'anthropic', label: 'Anthropic Messages（/messages）' },
  { value: 'responses', label: 'OpenAI Responses（/responses）' },
];

/* 徽章里只留协议名，括号里的路径说明在表单里已经写过了。 */
function protocolLabel(raw) {
  const v = String(raw || 'openai').toLowerCase();
  if (v === 'anthropic' || v === 'messages') return 'Anthropic';
  if (v === 'responses') return 'Responses';
  return 'OpenAI';
}

/* 模型清单：逗号（含全角）或换行都算分隔；去重后保持输入顺序。 */
function splitModels(text) {
  const out = [];
  for (const part of String(text || '').split(/[\n,，、]+/)) {
    const name = part.trim();
    if (name && !out.includes(name)) out.push(name);
  }
  return out;
}

/* 额外请求头：每行 `Key: Value`，`#` 开头按注释跳过。
   值里允许出现冒号（如 `Authorization: Bearer x`），所以只按第一个冒号切。 */
function parseHeaders(text) {
  const out = {};
  for (const line of String(text || '').split('\n')) {
    const raw = line.trim();
    if (!raw || raw.startsWith('#')) continue;
    const i = raw.indexOf(':');
    if (i <= 0) continue;
    const key = raw.slice(0, i).trim();
    const value = raw.slice(i + 1).trim();
    if (key) out[key] = value;
  }
  return out;
}

/* 反解：编辑已有渠道时把 headers 摊回文本行。 */
function headersToText(headers) {
  return Object.entries(headers || {}).map(([k, v]) => k + ': ' + v).join('\n');
}

/**
 * 打开「自定义渠道」抽屉。
 * @param {{ getProviders?: () => any[], reload?: () => any }} [opts]
 *   getProviders/reload 由渠道页注入：保存成功后要能拿到新的 provider 对象，
 *   才能直接走 openProvider 的接入流程（拿不到就提示用户去渠道列表点接入）。
 */
export function openCustomChannels(opts = {}) {
  const state = { channels: [], editing: null };

  const listBox = h('div', { class: 'list' });
  const nameIn = input({ placeholder: '例如：公司中转', label: '渠道名称' });
  const baseIn = input({ placeholder: 'https://api.example.com/v1', label: 'Base URL' });
  const protoIn = select(PROTOCOLS, 'openai', () => {}, { label: '协议' });
  const keyIn = input({ type: 'password', placeholder: 'sk-…（只用于本次探测，不会保存）', label: '探测用 API Key' });
  const modelsIn = textarea({ label: '模型清单', placeholder: '一行一个，或用逗号分隔；留空表示任意模型都透传' });
  const headersIn = textarea({ label: '额外请求头', placeholder: 'X-Custom-Header: value\nAuthorization: Bearer {{api_key}}' });
  const probeBox = h('div');
  const probeBtn = btn('探测模型', { icon: 'refresh', onClick: () => probe() });
  const attachBtn = btn('接入账号', { ghost: true, disabled: true, onClick: () => attach() });
  const saveBtn = btn('保存', { primary: true, onClick: () => save() });

  const d = drawer('自定义渠道', (body, foot) => {
    body.append(
      h('div', { class: 'meta' }, '把「地址 + 模型名」注册成渠道。密钥放在账号里（加密存储），删渠道不会删账号。'),
      field('已保存', listBox),
      field('名称', nameIn),
      field('Base URL', baseIn),
      field('协议', protoIn),
      field('探测模型', h('div', { class: 'inline' }, keyIn, probeBtn)),
      probeBox,
      field('模型清单', modelsIn),
      h('div', { class: 'meta' }, '一行一个或用逗号分隔；留空表示任意模型名都透传（按渠道 id 前缀对外暴露）。'),
      field('额外请求头（可选）', headersIn),
      h('div', { class: 'meta' }, '每行 Key: Value；值里可以用 {{api_key}} 引用账号密钥。'),
    );
    foot.append(
      h('span', { class: 'grow' }),
      btn('新建渠道', { ghost: true, onClick: () => resetForm() }),
      attachBtn,
      btn('关闭', { onClick: () => d.close() }),
      saveBtn,
    );
  });

  loadChannels();
  refreshAttach();

  /* ---------- 列表 ---------- */

  async function loadChannels() {
    try {
      const out = await api.get('custom-channels');
      state.channels = (out && out.channels) || [];
    } catch (e) {
      state.channels = [];
      clear(listBox);
      listBox.append(h('div', { class: 'row' }, errorBox('读取自定义渠道失败：' + (e.message || e), loadChannels)));
      return;
    }
    paintChannels();
  }

  function paintChannels() {
    clear(listBox);
    if (!state.channels.length) {
      listBox.append(h('div', { class: 'row meta' }, '还没有自定义渠道；填下面的表单保存一个。'));
      return;
    }
    for (const ch of state.channels) listBox.append(channelRow(ch));
  }

  function channelRow(ch) {
    const modelCount = (ch.models || []).length;
    const headerCount = Object.keys(ch.headers || {}).length;
    return h('div', { class: 'row' },
      h('span', { class: 'name' }, ch.name || ch.id),
      h('span', { class: 'meta' }, ch.base_url || ''),
      chip(protocolLabel(ch.protocol), 'plain'),
      chip(modelCount ? modelCount + ' 个模型' : '任意模型', 'plain'),
      headerCount ? chip(headerCount + ' 个请求头', 'plain') : null,
      h('span', { class: 'grow' }),
      btn('编辑', { ghost: true, class: 'sm', onClick: () => editChannel(ch) }),
      btn('删除', { ghost: true, danger: true, class: 'sm', onClick: () => removeChannel(ch) }));
  }

  /* ---------- 表单 ---------- */

  function editChannel(ch) {
    state.editing = ch;
    nameIn.value = ch.name || '';
    baseIn.value = ch.base_url || '';
    protoIn.value = ch.protocol || 'openai';
    modelsIn.value = (ch.models || []).join('\n');
    headersIn.value = headersToText(ch.headers);
    keyIn.value = '';
    setProbe(null);
    refreshAttach();
    nameIn.focus();
  }

  function resetForm() {
    state.editing = null;
    nameIn.value = '';
    baseIn.value = '';
    protoIn.value = 'openai';
    modelsIn.value = '';
    headersIn.value = '';
    keyIn.value = '';
    setProbe(null);
    refreshAttach();
    nameIn.focus();
  }

  /* 「接入账号」只在当前编辑的渠道确实存在时可用；没有就说明还没保存。 */
  function refreshAttach() {
    const ready = !!(state.editing && state.editing.id);
    attachBtn.disabled = !ready;
    attachBtn.title = ready ? '为这个渠道添加账号' : '先保存渠道，才能为它添加账号';
  }

  function setProbe(msg) {
    clear(probeBox);
    if (!msg) return;
    if (msg.tone === 'err') {
      probeBox.append(errorBox(msg.text));
      return;
    }
    probeBox.append(h('div', { class: 'meta', style: 'color:var(--' + (msg.tone === 'warn' ? 'amber' : 'green') + ')' }, msg.text));
  }

  /* ---------- 探测 ---------- */

  async function probe() {
    const base = String(baseIn.value || '').trim();
    if (!base) { toast('先填 Base URL 再探测'); baseIn.focus(); return; }
    setProbe(null);
    probeBtn.disabled = true;
    try {
      // probe 走 200 + {ok:false} 表达"上游拒绝"，所以不能只看 HTTP 状态。
      const out = await api.post('custom-channels/probe', { base_url: base, api_key: String(keyIn.value || '').trim() });
      if (out && out.ok) {
        const models = out.models || [];
        if (!models.length) {
          setProbe({ tone: 'warn', text: '探测成功，但上游没有返回模型清单；请手工填写。' });
        } else {
          modelsIn.value = models.join('\n');
          setProbe({ tone: 'ok', text: '探测到 ' + models.length + ' 个模型，已填入下面的模型清单。' });
        }
        return;
      }
      const detail = (out && out.error) || '上游没有返回可用结果';
      setProbe({ tone: 'err', text: detail + (out && out.status ? '（HTTP ' + out.status + '）' : '') });
    } catch (e) {
      setProbe({ tone: 'err', text: '探测失败：' + (e.message || e) });
    } finally {
      probeBtn.disabled = false;
    }
  }

  /* ---------- 保存 / 删除 / 接入 ---------- */

  async function save() {
    const name = String(nameIn.value || '').trim();
    const base = String(baseIn.value || '').trim();
    if (!name) { toast('先给渠道起个名字'); nameIn.focus(); return; }
    if (!base) { toast('Base URL 不能为空'); baseIn.focus(); return; }

    const payload = {
      name,
      base_url: base,
      protocol: protoIn.value,
      models: splitModels(modelsIn.value),
      headers: parseHeaders(headersIn.value),
    };
    // 有 id 就是更新同一条渠道；首次保存由后端按名称生成 slug。
    if (state.editing && state.editing.id) payload.id = state.editing.id;

    saveBtn.disabled = true;
    try {
      const out = await api.post('custom-channels', payload);
      const ch = (out && out.channel) || null;
      if (ch) state.editing = ch;
      refreshAttach();
      toast('自定义渠道已保存');
      setProbe({ tone: 'ok', text: '已保存「' + ((ch && ch.name) || name) + '」，渠道 ID：' + ((ch && ch.id) || '—') });
      await loadChannels();
      // 让渠道页重新拉一次 provider 列表，新的 custom-<id> 才会出现。
      if (opts.reload) await opts.reload();
    } catch (e) {
      toast('保存失败：' + (e.message || e));
      setProbe({ tone: 'err', text: '保存失败：' + (e.message || e) });
    } finally {
      saveBtn.disabled = false;
    }
  }

  async function removeChannel(ch) {
    const ok = await confirmDialog('删除自定义渠道「' + (ch.name || ch.id) + '」？',
      '只删除渠道定义；已接入的账号不会被删，但会显示成「渠道不存在」。', { danger: true, confirmText: '删除' });
    if (!ok) return;
    try {
      await api.del('custom-channels/' + encodeURIComponent(ch.id));
      toast('已删除 ' + (ch.name || ch.id));
      if (state.editing && state.editing.id === ch.id) resetForm();
      await loadChannels();
      if (opts.reload) await opts.reload();
    } catch (e) {
      toast('删除失败：' + (e.message || e));
    }
  }

  /* 保存后直接进接入向导：provider 是网关注册出来的 custom-<id>，
     刚保存时渠道页的 state.providers 还是旧的，所以先 reload 再看。 */
  async function attach() {
    const id = state.editing && state.editing.id;
    if (!id) { toast('先保存渠道，再接入账号'); return; }
    const find = () => ((opts.getProviders && opts.getProviders()) || []).find((p) => p && p.id === 'custom-' + id);
    let p = find();
    if (!p && opts.reload) {
      await opts.reload();
      p = find();
    }
    if (!p) {
      toast('渠道已保存；请关闭本窗口，在渠道列表里点它的「接入」添加账号');
      return;
    }
    d.close();
    openProvider(p, opts.reload);
  }
}
