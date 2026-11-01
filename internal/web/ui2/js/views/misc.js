/* 次级页面（自有实现）：Agent 接入 / 路由 / 脱敏 / 插件。只读优先，动作均走真实接口。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { card, chip, btn, iconBtn, select, field, input, textarea, toggle, drawer, empty, errorBox, skeleton, toast, confirmDialog, copyText, fmtDate } from '../components.js';

/* ---------- Agent 接入 ---------- */
export const agents = {
  title: 'Agent 接入',
  subtitle: '本机 agent 的接入与模型',
  mount(host, ctx) {
    host.append(skeleton(6));
    load();
    async function load() {
      clear(host);
      try {
        const st = await api.get('state');
        const list = st.agents || [];
        if (!list.length) { host.append(empty('没有检测到 agent', '安装 Claude Code / Codex CLI 等后刷新。')); return; }
        const grid = h('div', { class: 'grid-2' });
        await Promise.all(list.map(async (a) => {
          const c = card(a.name || a.id);
          const model = (a.fields || []).find((f) => f.key === 'model');
          let models = [];
          try { models = (await api.get('agent-models/' + encodeURIComponent(a.id))).models || []; } catch { /* 读不到就只显示当前值 */ }
          const visible = models.filter((m) => !m.hidden);
          const pick = select(
            (visible.length ? visible : models).map((m) => ({ value: m.id, label: m.name + (m.inUse ? '（当前）' : '') })),
            model?.value || '', () => {}, { label: '模型' });
          if (!model?.value) pick.prepend(h('option', { value: '' }, '选择模型…'));
          const applyBtn = btn('应用模型', { primary: true, onClick: async () => {
            const target = pick.value;
            if (!target) { toast('先选一个模型'); return; }
            if (!(await confirmDialog('把「' + (a.name || a.id) + '」切到 ' + target + '？', '会改写该 agent 的配置文件；正在运行的进程需要重启后读取。', { confirmText: '写入' }))) return;
            try { await api.post('clients/' + encodeURIComponent(a.id) + '/apply', { model: target }); toast('已写入配置'); await load(); }
            catch (e) { toast('写入失败：' + (e.message || e)); }
          } });
          c.el.append(
            h('div', { class: 'row' }, h('span', { class: 'name' }, '配置文件'), h('span', { class: 'grow' }), h('span', { class: 'mono meta', style: 'max-width:60%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap' }, a.path || '—')),
            h('div', { class: 'row' }, h('span', { class: 'name' }, '当前模型'), h('span', { class: 'grow' }), chip(model?.value || '未设置', model?.value ? 'green' : 'plain')),
            h('div', { class: 'row' }, h('span', { class: 'name' }, '模型清单'), h('span', { class: 'grow' }), h('span', { class: 'meta' }, (a.models?.shown ?? visible.length) + ' 个可选（共 ' + (a.models?.listed ?? models.length) + '）')),
            h('div', { class: 'row', style: 'gap:8px;flex-wrap:wrap' }, h('span', { class: 'grow' }), pick, applyBtn),
            h('div', { class: 'row', style: 'gap:8px;flex-wrap:wrap' },
              h('span', { class: 'grow' }),
              btn('复制配置路径', { onClick: () => copyText(a.path || '') }),
              btn('断开（还原配置）', { ghost: true, onClick: async () => {
                if (!(await confirmDialog('还原「' + (a.name || a.id) + '」的配置？', '会撤掉网关地址，恢复接入前的文件。', { danger: true, confirmText: '还原' }))) return;
                try { await api.post('clients/' + encodeURIComponent(a.id) + '/restore', {}); toast('已还原'); await load(); }
                catch (e) { toast('还原失败：' + (e.message || e)); }
              } }),
              btn('重新连接', { onClick: async () => {
                try { await api.post('agents/connect/' + encodeURIComponent(a.id), {}); toast('已写入网关地址'); await load(); }
                catch (e) { toast('连接失败：' + (e.message || e)); }
              } })));
          grid.append(c.el);
        }));
        host.append(grid);
      } catch (e) { host.append(errorBox('读取失败：' + (e.message || e), load)); }
    }
  },
};

/* ---------- 路由 ---------- */
export const routing = {
  title: '路由',
  subtitle: '分组、顺序与失败回退',
  mount(host, ctx) {
    host.append(skeleton(6));
    let presets = { presets: [], route_presets: {} };
    load();
    async function load() {
      clear(host);
      try {
        const [d, pr] = await Promise.all([
          api.get('groups'),
          api.get('presets').catch(() => ({ presets: [], route_presets: {} })),
        ]);
        presets = pr || { presets: [], route_presets: {} };
        const groups = d.groups || [];
        host.append(h('div', { class: 'inline', style: 'padding-bottom:8px' },
          h('span', { class: 'meta' }, '路由组预设只在该组被请求命中时生效；不勾选 = 不带。'),
          h('span', { class: 'grow' }),
          btn('管理预设', { ghost: true, onClick: () => ctx.navigate('settings', { tab: 'presets' }) })));
        if (!groups.length) { host.append(empty('还没有路由组', '在渠道页把多个渠道放进同一组后，这里会出现。')); return; }
        const list = h('div', { class: 'list' });
        for (const g of groups) {
          const members = (g.memberInfo || []).map((m) => chip(m.providerName + ' · ' + m.name + (m.credit_rate ? '（×' + m.credit_rate + '）' : ''), 'plain'));
          const ids = (presets.route_presets || {})[g.id] || [];
          const names = ids.map((id) => {
            const p = (presets.presets || []).find((x) => x.id === id);
            return p ? (p.name || p.id) : id;
          });
          list.append(h('div', { class: 'row', style: 'align-items:flex-start;flex-wrap:wrap' },
            h('span', { class: 'mono', style: 'min-width:0;max-width:100%;overflow:hidden;text-overflow:ellipsis;font-size:12px' }, g.id),
            g.auto ? chip('自动', 'green') : chip('手动', 'plain'),
            h('span', { class: 'grow', style: 'display:flex;flex-wrap:wrap;gap:4px' }, members.slice(0, 12)),
            h('span', { class: 'meta' }, names.length ? '预设: ' + names.slice(0, 3).join('、') + (names.length > 3 ? '…' : '') : '无预设'),
            btn('预设', { ghost: true, onClick: () => routePresetDialog(g) }),
            h('span', { class: 'meta' }, members.length + ' 个成员')));
        }
        host.append(list);
      } catch (e) { host.append(errorBox('读取失败：' + (e.message || e), load)); }
    }

    function routePresetDialog(g) {
      const attached = new Set((presets.route_presets || {})[g.id] || []);
      const boxes = [];
      const d = drawer('路由预设 · ' + g.id, (body) => {
        const list = h('div', { class: 'preset-picker' });
        if (!(presets.presets || []).length) {
          list.append(h('div', { class: 'preset-empty meta' }, '还没有预设；先到 设置 → 预设 新建。'));
        }
        for (const preset of presets.presets || []) {
          const box = h('input', { type: 'checkbox', checked: attached.has(preset.id), 'aria-label': '挂载预设 ' + (preset.name || preset.id) });
          boxes.push({ id: preset.id, box });
          list.append(h('label', { class: 'preset-item' }, box,
            h('span', { class: 'preset-item-main' },
              h('span', { class: 'inline', style: 'gap:6px' },
                h('span', { class: 'name' }, preset.name || preset.id),
                preset.enabled ? null : chip('停用', 'plain'),
                preset.system_prompt ? chip('提示词', 'green') : null),
              h('span', { class: 'meta' }, 'Body ' + Object.keys(preset.body || {}).length + ' · Headers ' + Object.keys(preset.headers || {}).length + (preset.description ? ' · ' + preset.description : '')))));
        }
        body.append(
          field('已挂载预设', list),
          h('div', { class: 'meta preset-help' }, '勾选后在该路由组命中时生效；不勾选 = 不带路由预设，渠道预设仍会生效。'),
          h('div', { class: 'inline', style: 'justify-content:flex-end;padding-top:4px' },
            btn('管理预设', { ghost: true, onClick: () => { d.close(); ctx.navigate('settings', { tab: 'presets' }); } }),
            btn('取消', { onClick: () => d.close() }),
            btn('保存', { primary: true, onClick: async () => {
              const ids = boxes.filter((b) => b.box.checked).map((b) => b.id);
              try {
                presets = await api.post('presets/attach', { scope: 'route', key: g.id, ids });
                d.close();
                toast('路由预设已保存');
                await load();
              } catch (e) { toast('保存失败：' + (e.message || e)); }
            } })));
      });
    }
  },
};

/* ---------- 脱敏 ---------- */
export const masking = {
  title: '脱敏',
  subtitle: '请求离开电脑前的脱敏记录',
  mount(host) {
    host.append(skeleton(6));
    load();
    async function load() {
      clear(host);
      try {
        const [sum, rec] = await Promise.all([api.get('masking/summary'), api.get('masking/records')]);
        const kpis = h('div', { class: 'kpis' },
          kpi('已脱敏请求', sum.requests ?? sum.total ?? 0),
          kpi('拦下的字段', sum.blocked ?? 0),
          kpi('不同值', sum.distinct ?? (sum.values || []).length),
          kpi('按规则 / 模型', (sum.by?.rule ?? 0) + ' / ' + (sum.by?.model ?? 0)));
        host.append(kpis);
        const c = card('最近的脱敏记录', { actions: [btn('清空记录', { danger: true, onClick: async () => {
          if (!(await confirmDialog('清空脱敏记录？', '只删本机保留的脱敏历史，不改规则。', { danger: true, confirmText: '清空' }))) return;
          try { await api.post('masking/clear', {}); toast('已清空'); load(); } catch (e) { toast('清空失败：' + (e.message || e)); }
        } })] });
        const records = rec.records || [];
        if (!records.length) c.el.append(empty('还没有脱敏过任何内容', '开启设置里的「请求脱敏」后，这里会按请求出现。'));
        else for (const r of records.slice(0, 50)) c.el.append(h('div', { class: 'row' }, h('span', { class: 'name' }, r.kind || r.field || '字段'), h('span', { class: 'meta' }, r.sample || r.value || ''), h('span', { class: 'grow' }), h('span', { class: 'meta' }, fmtDate(r.at))));
        host.append(c.el);
      } catch (e) { host.append(errorBox('读取失败：' + (e.message || e), load)); }
    }
    function kpi(k, v) { return h('div', { class: 'kpi' }, h('div', { class: 'k' }, k), h('div', { class: 'v' }, String(v))); }
  },
};

/* ---------- 插件 ---------- */
export const plugins = {
  title: '插件',
  subtitle: '请求改写规则与内置能力',
  mount(host) {
    host.append(skeleton(6));
    load();
    async function load() {
      clear(host);
      try {
        const d = await api.get('plugins');
        const builtin = d.builtin || [];
        const rules = d.plugins || [];
        const grid = h('div', { class: 'grid-2' });
        const b = card('内置能力', { chips: [chip(builtin.length + ' 项', 'plain')] });
        for (const x of builtin) b.el.append(h('div', { class: 'row' }, h('span', { class: 'name' }, x.name), h('span', { class: 'meta', style: 'flex:1' }, x.desc || ''), chip('内置', 'plain')));
        if (!builtin.length) b.el.append(empty('没有内置能力'));
        grid.append(b.el);
        const p = card('改写规则', { chips: [chip(rules.length + ' 条', 'plain'), btn('新建', { primary: true, icon: 'plus', onClick: () => editRule(null) })] });
        for (const x of rules) {
          const detail = x.kind === 'model_map' ? (x.from || '') + ' → ' + (x.to || '')
            : x.kind === 'word_guard' ? ((x.words || []).concat(x.patterns || []).join(', ') || '无关键词')
            : x.kind === 'param_override' ? [x.temperature != null ? 'temp ' + x.temperature : '', x.top_p != null ? 'top_p ' + x.top_p : '', x.max_tokens != null ? 'max_tokens ' + x.max_tokens : ''].filter(Boolean).join(' · ')
            : (x.prompt || '').slice(0, 80);
          p.el.append(h('div', { class: 'row' },
            h('span', { class: 'name' }, x.name || x.id),
            chip(x.kind, 'plain'),
            h('span', { class: 'meta', style: 'flex:1' }, detail),
            toggle(x.enabled !== false, async (on) => {
              try { await api.post('plugins', { ...x, enabled: on }); toast(on ? '已启用' : '已停用'); await load(); }
              catch (e) { toast('切换失败：' + (e.message || e)); }
            }, { label: '启用规则 ' + (x.name || x.id) }),
            iconBtn('edit', '编辑规则', () => editRule(x)),
            iconBtn('trash', '删除规则', async () => {
              if (!(await confirmDialog('删除规则「' + (x.name || x.id) + '」？', '', { danger: true, confirmText: '删除' }))) return;
              try { await api.del('plugins/' + encodeURIComponent(x.id)); toast('已删除'); await load(); }
              catch (e) { toast('删除失败：' + (e.message || e)); }
            }, { danger: true })));
        }
        if (!rules.length) p.el.append(empty('还没有改写规则', '规则在请求发出前生效：参数覆盖、模型映射、关键词拦截、系统提示注入。'));
        grid.append(p.el);
        host.append(grid);
      } catch (e) { host.append(errorBox('读取失败：' + (e.message || e), load)); }
    }

    function editRule(it) {
      const kinds = [['param_override', '参数覆盖'], ['model_map', '模型映射'], ['word_guard', '关键词拦截'], ['system_prompt', '系统提示注入']];
      const id = input({ value: it?.id || '', placeholder: 'id，例如 force-temp' });
      const kind = select(kinds.map(([value, label]) => ({ value, label })), it?.kind || 'param_override', () => renderFields());
      const name = input({ value: it?.name || '', placeholder: '显示名' });
      const note = input({ value: it?.note || '', placeholder: '说明（可选）' });
      const providers = input({ value: (it?.providers || []).join(','), placeholder: '限定渠道，逗号分隔；空=全部' });
      const models = input({ value: (it?.models || []).join(','), placeholder: '限定模型，支持 *；空=全部' });
      const extra = h('div', { style: 'display:flex;flex-direction:column;gap:12px' });
      const values = {
        temperature: it?.temperature != null ? String(it.temperature) : '',
        top_p: it?.top_p != null ? String(it.top_p) : '',
        max_tokens: it?.max_tokens != null ? String(it.max_tokens) : '',
        from: it?.from || '', to: it?.to || '',
        words: (it?.words || []).join(','), patterns: (it?.patterns || []).join('\n'), message: it?.message || '',
        prompt: it?.prompt || '', position: it?.position || 'prepend',
      };
      function renderFields() {
        clear(extra);
        const k = kind.value;
        if (k === 'param_override') {
          extra.append(field('temperature', input({ value: values.temperature, onInput: (e) => values.temperature = e.target.value })),
            field('top_p', input({ value: values.top_p, onInput: (e) => values.top_p = e.target.value })),
            field('max_tokens', input({ value: values.max_tokens, onInput: (e) => values.max_tokens = e.target.value })));
        } else if (k === 'model_map') {
          extra.append(field('From（支持 *）', input({ value: values.from, onInput: (e) => values.from = e.target.value })),
            field('To', input({ value: values.to, onInput: (e) => values.to = e.target.value })));
        } else if (k === 'word_guard') {
          extra.append(field('关键词（逗号分隔）', input({ value: values.words, onInput: (e) => values.words = e.target.value })),
            field('正则（每行一个）', textarea({ value: values.patterns, onInput: (e) => values.patterns = e.target.value })),
            field('拦截说明', input({ value: values.message, onInput: (e) => values.message = e.target.value })));
        } else {
          extra.append(field('提示词', textarea({ value: values.prompt, onInput: (e) => values.prompt = e.target.value })),
            field('位置', select([{ value: 'prepend', label: '前置' }, { value: 'append', label: '追加' }], values.position, (v) => { values.position = v; })));
        }
      }
      renderFields();
      const d = drawer(it ? '编辑规则' : '新建改写规则', (body) => {
        body.append(field('ID', id), field('类型', kind), field('名称', name), field('说明', note),
          field('渠道范围', providers), field('模型范围', models), extra);
        body.append(h('div', { class: 'inline', style: 'justify-content:flex-end' },
          btn('取消', { onClick: () => d.close() }),
          btn('保存', { primary: true, onClick: async () => {
            if (!id.value.trim()) { toast('ID 不能为空'); return; }
            const rule = { id: id.value.trim(), kind: kind.value, name: name.value.trim() || id.value.trim(), enabled: it ? it.enabled !== false : true };
            if (note.value.trim()) rule.note = note.value.trim();
            const pv = providers.value.split(',').map((x) => x.trim()).filter(Boolean);
            const mv = models.value.split(',').map((x) => x.trim()).filter(Boolean);
            if (pv.length) rule.providers = pv;
            if (mv.length) rule.models = mv;
            const num = (v) => { const n = Number(v); return v !== '' && Number.isFinite(n) ? n : undefined; };
            if (kind.value === 'param_override') {
              if (num(values.temperature) !== undefined) rule.temperature = num(values.temperature);
              if (num(values.top_p) !== undefined) rule.top_p = num(values.top_p);
              if (num(values.max_tokens) !== undefined) rule.max_tokens = Math.round(num(values.max_tokens));
            } else if (kind.value === 'model_map') {
              if (!values.from.trim() || !values.to.trim()) { toast('From 和 To 都要填'); return; }
              rule.from = values.from.trim(); rule.to = values.to.trim();
            } else if (kind.value === 'word_guard') {
              rule.words = values.words.split(',').map((x) => x.trim()).filter(Boolean);
              rule.patterns = values.patterns.split('\n').map((x) => x.trim()).filter(Boolean);
              if (!rule.words.length && !rule.patterns.length) { toast('至少一个关键词或正则'); return; }
              if (values.message.trim()) rule.message = values.message.trim();
            } else {
              if (!values.prompt.trim()) { toast('提示词不能为空'); return; }
              rule.prompt = values.prompt; rule.position = values.position;
            }
            try { await api.post('plugins', rule); d.close(); toast('已保存'); await load(); }
            catch (e) { toast('保存失败：' + (e.message || e)); }
          } })));
      });
    }
  },
};
