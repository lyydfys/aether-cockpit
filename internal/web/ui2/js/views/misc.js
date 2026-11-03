/* 次级页面（自有实现）：Agent 接入 / 路由 / 脱敏 / 插件。只读优先，动作均走真实接口。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { card, chip, btn, iconBtn, select, field, input, textarea, toggle, drawer, empty, errorBox, skeleton, toast, confirmDialog, copyText } from '../components.js';

/* ---------- Agent 接入 ---------- */
// agentsShowAll 记住"是否展开未安装的客户端"：切页回来时保持用户的选择。
const agentsShowAll = { v: false };

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
        const all = st.agents || [];
        if (!all.length) { host.append(empty('没有检测到 agent', '安装 Claude Code / Codex CLI 等后刷新。')); return; }
        // 默认只显示"本机装了"或"已经接入"的：十几条全列会让真正在用的
        // 那几个淹在一堆"未检测到"里。想看全部时手动展开。
        const showAll = !!ctx?.query?.all || agentsShowAll.v;
        const list = showAll ? all : all.filter((a) => a.detected || a.wired);
        const hiddenCount = all.length - list.length;
        let drift = {};
        try { drift = await api.get('drift') || {}; } catch { /* 漂移检测失败不影响接入列表 */ }
        const driftIds = Object.keys(drift);
        if (driftIds.length) {
          host.append(h('div', { class: 'row', style: 'align-items:center;gap:10px;padding:10px 12px;border:1px solid var(--amber);border-radius:8px' },
            h('span', { class: 'name' }, '检测到 ' + driftIds.length + ' 个 Agent 的配置已被外部改动'),
            h('span', { class: 'meta' }, driftIds.slice(0, 6).join('、') + (driftIds.length > 6 ? '…' : '')),
            h('span', { class: 'grow' }),
            btn('重新写入', { primary: true, onClick: async () => {
              try {
                const out = await api.post('drift/apply', { ids: driftIds });
                const n = (out.applied || []).length;
                const failed = Object.keys(out.errors || {});
                toast(n ? '已重新写入 ' + n + ' 个 Agent' : (failed.length ? '写入失败：' + out.errors[failed[0]] : '没有需要写入的配置'));
                await load();
              } catch (e) { toast('写入失败：' + (e.message || e)); }
            } })));
        }
        const grid = h('div', { class: 'grid-2' });

        // 过滤提示：默认收起"没装也没接"的条目，但要让用户知道还有多少条、
        // 并且随时能展开（否则会以为列表不全）。
        if (hiddenCount > 0 || showAll) {
          host.append(h('div', { class: 'row', style: 'align-items:center;gap:10px;padding:4px 2px' },
            h('span', { class: 'meta' }, showAll
              ? '正在显示全部 ' + all.length + ' 个客户端'
              : '已隐藏 ' + hiddenCount + ' 个未安装、未接入的客户端'),
            h('span', { class: 'grow' }),
            btn(showAll ? '只看已安装' : '显示全部', {
              ghost: true,
              onClick: async () => { agentsShowAll.v = !showAll; await load(); },
            })));
        }

        // 方案（profile）：把"哪些 agent 用哪个模型"存成一份命名快照，一键切换。
        // 后端 /api/profiles 早就齐了，但前端一直没有入口——等于这个能力不存在。
        try {
          const pf = await api.get('profiles') || {};
          const list = pf.profiles || [];
          const pc = card('方案', { chips: [chip(list.length + ' 份', 'plain')] });
          pc.el.append(h('div', { class: 'meta' }, '把当前各 Agent 的模型存成一份方案，之后一键切换（只改模型，不动其它配置）。'));
          for (const p of list) {
            const active = p.id === pf.active;
            pc.el.append(h('div', { class: 'row' },
              h('span', { class: 'name' }, p.name),
              chip(active ? '使用中' : (p.clients || []).length + ' 个客户端', active ? 'green' : 'plain'),
              h('span', { class: 'grow' }),
              btn('应用', { primary: !active, ghost: active, onClick: async () => {
                try { await api.post('profiles/' + encodeURIComponent(p.id) + '/activate', {}); toast('已切到「' + p.name + '」'); await load(); }
                catch (e) { toast('切换失败：' + (e.message || e)); }
              } }),
              iconBtn('trash', '删除方案', async () => {
                if (!(await confirmDialog('删除方案「' + p.name + '」？', '只删这份快照，不改当前配置。', { danger: true, confirmText: '删除' }))) return;
                try { await api.del('profiles/' + encodeURIComponent(p.id)); toast('已删除'); await load(); }
                catch (e) { toast('删除失败：' + (e.message || e)); }
              }, { danger: true })));
          }
          pc.el.append(h('div', { class: 'row', style: 'gap:8px' },
            h('span', { class: 'grow' }),
            btn('保存当前为方案', { onClick: async () => {
              const name = prompt('方案名称（例如「省钱档」「长上下文档」）');
              if (!name) return;
              const clients = list.length >= 0
                ? st.agents.filter((x) => x.wired).map((x) => ({
                  client_id: x.id,
                  model: ((x.fields || []).find((f) => f.key === 'model') || {}).value || '',
                })).filter((c) => c.model)
                : [];
              if (!clients.length) { toast('没有已接入且设置了模型的 Agent'); return; }
              try { await api.post('profiles', { name, clients }); toast('已保存'); await load(); }
              catch (e) { toast('保存失败：' + (e.message || e)); }
            } })));
          host.append(pc.el);
        } catch { /* 方案不是主流程，读不到就不显示 */ }

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
          const extra = (a.fields || []).filter((f) => f.key !== 'model');
          const extraPicks = [];
          for (const f of extra) {
            const opts = (f.options || []).map((o) => ({ value: o.value, label: o.label || o.value }));
            if (!opts.some((o) => o.value === '')) opts.unshift({ value: '', label: '（跟随主模型）' });
            const sel = select(opts, f.value || '', (v) => { stageField(a.id, f.key, v); },
              { label: (a.name || a.id) + ' 的 ' + (f.label || f.key) });
            extraPicks.push({ key: f.key, sel });
          }
          const applyBtn = btn('应用模型', { primary: true, onClick: async () => {
            const target = pick.value;
            if (!target) { toast('先选一个模型'); return; }
            if (!(await confirmDialog('把「' + (a.name || a.id) + '」切到 ' + target + '？', '会改写该 agent 的配置文件；正在运行的进程需要重启后读取。', { confirmText: '写入' }))) return;
            try {
              // 先暂存附加字段（档位模型 / 推理档位），再走 connect —— connect 会把
              // 暂存值一起写进配置。此前这些值只写不读，选了也不生效。
              for (const p of extraPicks) await stageField(a.id, p.key, p.sel.value);
              await api.post('agents/connect/' + encodeURIComponent(a.id), {});
              toast('已写入配置'); await load();
            } catch (e) { toast('写入失败：' + (e.message || e)); }
          } });
          c.el.append(
            h('div', { class: 'row' }, h('span', { class: 'name' }, '配置文件'), h('span', { class: 'grow' }), h('span', { class: 'mono meta', style: 'max-width:60%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap' }, a.path || '—')),
            h('div', { class: 'row' }, h('span', { class: 'name' }, '当前模型'), h('span', { class: 'grow' }), chip(model?.value || '未设置', model?.value ? 'green' : 'plain')),
            h('div', { class: 'row' }, h('span', { class: 'name' }, '模型清单'), h('span', { class: 'grow' }), h('span', { class: 'meta' }, (a.models?.shown ?? visible.length) + ' 个可选（共 ' + (a.models?.listed ?? models.length) + '）')),
            ...extraPicks.map((p) => {
              const f = extra.find((x) => x.key === p.key) || {};
              return h('div', { class: 'row', style: 'gap:8px;flex-wrap:wrap' },
                h('span', { class: 'name' }, f.label || p.key), h('span', { class: 'grow' }), p.sel);
            }),
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

// stageField 暂存一个字段（不写配置），等"应用"时随 connect 一起落盘。
async function stageField(id, field, value) {
  try { await api.post('agents/stage/' + encodeURIComponent(id), { field, value }); }
  catch (e) { toast('暂存失败：' + (e.message || e)); }
}

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
            chip('策略 ' + (g.routingEffective || g.routing || 'priority'), 'plain'),
            (g.rules || []).length ? chip((g.rules || []).length + ' 条规则', 'green') : null,
            h('span', { class: 'grow', style: 'display:flex;flex-wrap:wrap;gap:4px' }, members.slice(0, 12)),
            h('span', { class: 'meta' }, names.length ? '预设: ' + names.slice(0, 3).join('、') + (names.length > 3 ? '…' : '') : '无预设'),
            btn('编辑', { ghost: true, onClick: () => editGroup(g, load) }),
            btn('预设', { ghost: true, onClick: () => routePresetDialog(g) }),
            h('span', { class: 'meta' }, members.length + ' 个成员')));
          for (const w of g.warnings || []) {
            list.append(h('div', { class: 'meta', style: 'padding:2px 0 6px 0;color:var(--amber)' }, '⚠ ' + w));
          }
        }
        host.append(list);
      } catch (e) { host.append(errorBox('读取失败：' + (e.message || e), load)); }
    }

    // editGroup 编辑一个路由组：成员（含嵌套 group/<id>）、策略、规则。
    //
    // 此前这一页只读，要改 routing 或加规则只能手写 JSON 打 /api/groups——
    // 后端早就支持，前端等于没这个能力。
    function editGroup(g, reload) {
      const name = input({ value: g.name || '', placeholder: g.id });
      const members = textarea({ value: (g.members || []).join('\n'), rows: 6 });
      const routing = select([
        { value: '', label: '默认（priority）' },
        { value: 'order', label: 'order 按顺序' },
        { value: 'rotate', label: 'rotate 轮转' },
        { value: 'usage', label: 'usage 按剩余额度' },
        { value: 'pace', label: 'pace 按消耗速度' },
        { value: 'smart', label: 'smart 意图路由' },
        { value: 'fast', label: 'fast 按实测时延' },
        { value: 'weight', label: 'weight（未实现：需权重表）' },
        { value: 'free', label: 'free（未实现：需成本元数据）' },
        { value: 'cheapest', label: 'cheapest（未实现）' },
        { value: 'fastest', label: 'fastest（未实现）' },
      ], g.routing || '', () => {}, { label: '路由策略' });
      const rules = textarea({
        value: (g.rules || []).length ? JSON.stringify(g.rules, null, 2) : '',
        rows: 8,
        placeholder: '[\n  {"use":["qoder-cn/auto"],"useEffort":"high","effort":"high"},\n  {"use":["workbuddy-cn/deepseek-v4.1-flash"],"time":{"from":"00:00","to":"08:00","zone":"Asia/Shanghai","days":[1,2,3,4,5]}},\n  {"use":["trae-cn/deepseek-v4.1-flash"],"intent":"chat"}\n]',
      });
      const d = drawer('编辑路由组 · ' + g.id, (body) => {
        body.append(
          field('名称', name),
          field('成员（每行一个；支持 group/<组 ID> 嵌套）', members),
          field('路由策略', routing),
          field('规则（JSON 数组，留空表示不启用）', rules),
          h('div', { class: 'meta', style: 'padding:4px 0' },
            '规则可用字段：use（成员前移）、useEffort（强制推理档）、effort（请求 ≥ 该档时命中）、intent（意图分类命中）、time（from/to/days/zone，支持跨午夜）。'),
          h('div', { class: 'inline', style: 'justify-content:flex-end;gap:8px' },
            btn('取消', { onClick: () => d.close() }),
            btn('保存', { primary: true, onClick: async () => {
              const list = members.value.split('\n').map((x) => x.trim()).filter(Boolean);
              if (!list.length) { toast('至少一个成员'); return; }
              let parsed = [];
              const raw = (rules.value || '').trim();
              if (raw) {
                try { parsed = JSON.parse(raw); } catch (e) { toast('规则不是合法 JSON：' + e.message); return; }
                if (!Array.isArray(parsed)) { toast('规则必须是数组'); return; }
              }
              try {
                await api.post('groups', {
                  id: g.id, name: name.value.trim() || g.id,
                  members: list, routing: routing.value, rules: parsed,
                });
                toast('已保存');
                d.close();
                await reload();
              } catch (e) { toast('保存失败：' + (e.message || e)); }
            } }),
          ));
      });
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
// 一页把内核支持的能力都接上：总开关、按类型处置、按类型开关、自定义规则、
// 白名单、试一试、记录（带过滤与详情）。
//
// 之前这里只有两个 KPI 加一段记录列表，且读的字段名和接口返回对不上
// （r.kind / r.sample / r.at vs 实际 {key,t,agent,provider,values}），
// 后端十来个端点全闲着。
const REDACT_KINDS = [
  ['API_KEY', '密钥'], ['TOKEN', '令牌'], ['SECRET', '口令/鉴权头'], ['PASSWORD', '密码'],
  ['PRIVATE_KEY', '私钥'], ['JWT', 'JWT'], ['EMAIL', '邮箱'], ['PHONE', '手机号'],
  ['ID', '身份证'], ['CARD', '银行卡'], ['SSN', 'SSN'], ['PASSPORT', '护照'],
  ['IBAN', 'IBAN'], ['BIRTHDAY', '生日'], ['MAC', 'MAC'], ['SERIAL', '序列号'],
  ['HOME', '主目录用户名'], ['BUCKET', '云存储桶'], ['USERHOST', '用户名@主机'], ['IP_ADDR', '公网 IP'],
];

export const masking = {
  title: '脱敏',
  subtitle: '请求离开电脑前的脱敏规则、处置方式与记录',
  mount(host) {
    host.append(skeleton(6));
    const state = { kind: '', by: '', agent: '', provider: '', blocked: false, days: 7 };
    load();

    async function load() {
      clear(host);
      try {
        const q = new URLSearchParams({ limit: '50' });
        if (state.kind) q.set('kind', state.kind);
        if (state.by) q.set('by', state.by);
        if (state.agent) q.set('agent', state.agent);
        if (state.provider) q.set('provider', state.provider);
        if (state.blocked) q.set('blocked', '1');
        const [s, sum, rec] = await Promise.all([
          api.get('settings').catch(() => ({})),
          api.get('masking/summary?days=' + state.days).catch(() => ({})),
          api.get('masking/records?' + q.toString()).catch(() => ({ records: [] })),
        ]);

        host.append(h('div', { class: 'kpis' },
          kpi('已脱敏请求', sum.requests ?? sum.total ?? 0),
          kpi('拦下的字段', sum.blocked ?? 0),
          kpi('不同值', sum.distinct ?? (sum.values || []).length),
          kpi('按规则 / 模型', (sum.by?.rule ?? 0) + ' / ' + (sum.by?.model ?? 0))));

        // ---- 总开关 ----
        const sw = card('开关', { chips: [chip(s.redact ? '已开启' : '未开启', s.redact ? 'green' : 'plain')] });
        sw.el.append(
          h('div', { class: 'row' }, h('span', { class: 'name' }, '请求脱敏'),
            h('span', { class: 'meta', style: 'flex:1' }, '离开本机前把密钥 / 邮箱 / 手机号换成占位符'),
            toggle(!!s.redact, (on) => saveSetting({ redact: on }, '请求脱敏'))),
          h('div', { class: 'row' }, h('span', { class: 'name' }, '个人信息'),
            h('span', { class: 'meta', style: 'flex:1' }, '包含邮箱、手机号、证件、银行卡等个人字段'),
            toggle(s.redactPersonal !== false, (on) => saveSetting({ redactPersonal: on }, '个人信息'))),
          h('div', { class: 'row' }, h('span', { class: 'name' }, '保留格式的假值'),
            h('span', { class: 'meta', style: 'flex:1' }, '密钥类改成"同形假值"（sk-… 仍是 sk-…），上游更难识别'),
            toggle(!!s.redactShape, (on) => saveSetting({ redactShape: on }, '同形假值'))),
        );
        host.append(sw.el);

        // ---- 按类型处置（mask / warn / block）----
        const acts = s.redactActions || {};
        const ac = card('处置方式', { chips: [chip('按类型生效', 'plain')] });
        ac.el.append(h('div', { class: 'meta' }, '脱敏 = 换成占位符；只记录 = 不改写；拦截 = 整个请求不发送（403）。'));
        for (const [kind, label] of REDACT_KINDS) {
          ac.el.append(h('div', { class: 'row' },
            h('span', { class: 'name' }, label),
            h('span', { class: 'meta' }, kind),
            h('span', { class: 'grow' }),
            select([
              { value: '', label: '默认（脱敏）' },
              { value: 'mask', label: '脱敏' },
              { value: 'warn', label: '只记录' },
              { value: 'block', label: '拦截' },
            ], acts[kind] || '', async (next) => {
              const merged = { ...(s.redactActions || {}) };
              if (next) merged[kind] = next; else delete merged[kind];
              try {
                await api.post('settings/redact-actions', { actions: merged });
                toast(next === 'block' ? label + '：命中将拒绝发送' : '已更新 ' + label);
                await load();
              } catch (e) { toast('保存失败：' + (e.message || e)); }
            }, { label: label + ' 的处置方式' })));
        }
        host.append(ac.el);

        // ---- 逐类型开关（个人信息细项）----
        const kindsOn = s.redactKinds || {};
        const kc = card('细项开关', { chips: [chip('默认关闭的项可单独打开', 'plain')] });
        for (const [kind, label] of [['HOME', '主目录用户名'], ['BUCKET', '云存储桶'], ['USERHOST', '用户名@主机'], ['IP_ADDR', '公网 IP']]) {
          const on = Object.prototype.hasOwnProperty.call(kindsOn, kind) ? !!kindsOn[kind] : false;
          kc.el.append(h('div', { class: 'row' },
            h('span', { class: 'name' }, label),
            h('span', { class: 'meta' }, '默认关闭：可能误伤正常文本'),
            h('span', { class: 'grow' }),
            toggle(on, async (next) => {
              const merged = { ...kindsOn, [kind]: next };
              try { await saveSetting({ redactKinds: merged }, label); } catch (e) { toast('保存失败：' + (e.message || e)); }
            }, { label: label + ' 开关' })));
        }
        host.append(kc.el);

        // ---- 自定义规则 ----
        const rules = Array.isArray(s.redactRules) ? s.redactRules : [];
        const rc = card('自定义规则', { chips: [chip(rules.length + ' 条', 'plain')] });
        rc.el.append(h('div', { class: 'meta' }, '前缀或正则；类型用来决定占位符前缀，例如 SECRET / TERM。'));
        for (let i = 0; i < rules.length; i++) {
          const r = rules[i];
          rc.el.append(h('div', { class: 'row' },
            h('span', { class: 'name' }, r.kind || 'TERM'),
            h('span', { class: 'mono meta', style: 'flex:1;overflow:hidden;text-overflow:ellipsis' }, r.prefix ? '前缀 ' + r.prefix : ('正则 ' + (r.regex || ''))),
            iconBtn('trash', '删除规则', async () => {
              const next = rules.filter((_, j) => j !== i);
              try { await api.post('settings/redact-rules', { rules: next }); toast('已删除'); await load(); }
              catch (e) { toast('删除失败：' + (e.message || e)); }
            }, { danger: true })));
        }
        rc.el.append(h('div', { class: 'inline', style: 'gap:8px;margin-top:6px;flex-wrap:wrap' },
          btn('添加规则', { onClick: () => addRule(rules, load) })));
        host.append(rc.el);

        // ---- 白名单 ----
        const allow = Array.isArray(s.redactAllow) ? s.redactAllow : [];
        const wc = card('白名单', { chips: [chip(allow.length + ' 项', 'plain')] });
        wc.el.append(h('div', { class: 'meta' }, '整值匹配，命中的值永不脱敏（例如公司域名）。'));
        if (allow.length) wc.el.append(h('div', { class: 'mono meta', style: 'padding:4px 0' }, allow.join('、')));
        wc.el.append(h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap' },
          btn('编辑白名单', { onClick: () => editAllow(allow, load) })));
        host.append(wc.el);

        // ---- 试一试 ----
        const tc = card('试一试', { chips: [chip('只预览，不发请求', 'plain')] });
        const ta = textarea({ placeholder: '粘贴一段文本，看看会被改成什么样', rows: 3 });
        const outBox = h('pre', { class: 'code-block', style: 'max-height:220px;overflow:auto;display:none' });
        tc.el.append(ta, h('div', { class: 'inline', style: 'gap:8px;margin-top:6px' },
          btn('预览', { primary: true, onClick: async () => {
            try {
              const r = await api.post('masking/preview', { text: ta.value || '' });
              outBox.style.display = '';
              outBox.textContent = r.text || '(无变化)';
              if (r.blocked) toast('这段文本命中「拦截」规则：真发请求会被拒绝');
            } catch (e) { toast('预览失败：' + (e.message || e)); }
          } })), outBox);
        host.append(tc.el);

        // ---- 记录 ----
        const list = rec.records || [];
        const cc = card('最近的脱敏记录', { chips: [chip(list.length + ' 条', 'plain')], actions: [
          btn('清空记录', { danger: true, onClick: async () => {
            if (!(await confirmDialog('清空脱敏记录？', '只删本机保留的脱敏历史，不改规则。', { danger: true, confirmText: '清空' }))) return;
            try { await api.post('masking/clear', {}); toast('已清空'); await load(); }
            catch (e) { toast('清空失败：' + (e.message || e)); }
          } }),
        ] });
        // 过滤条
        const kindOpts = [{ value: '', label: '全部类型' }].concat(REDACT_KINDS.map(([k, l]) => ({ value: k, label: l })));
        cc.el.append(h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap;padding-bottom:8px' },
          select(kindOpts, state.kind, async (v) => { state.kind = v; await load(); }, { label: '按类型过滤' }),
          select([{ value: '', label: '全部来源' }, { value: 'rule', label: '规则' }, { value: 'model', label: '模型' }],
            state.by, async (v) => { state.by = v; await load(); }, { label: '按来源过滤' }),
          toggle(state.blocked, async (on) => { state.blocked = on; await load(); }, { label: '只看被拦截' }),
          h('span', { class: 'grow' }),
          select([{ value: '7', label: '近 7 天' }, { value: '30', label: '近 30 天' }], String(state.days),
            async (v) => { state.days = Number(v); await load(); }, { label: '统计窗口' })));

        if (!list.length) {
          cc.el.append(empty('还没有脱敏记录', '开启「请求脱敏」后，这里会按请求出现。'));
        } else {
          for (const r of list) {
            const vals = r.values || [];
            const kinds = [...new Set(vals.map((v) => v.k))].slice(0, 4);
            const row = h('div', { class: 'row', style: 'cursor:pointer' },
              h('span', { class: 'mono meta' }, new Date(r.t).toLocaleString()),
              h('span', { class: 'name' }, r.provider || '—'),
              h('span', { class: 'meta' }, (r.agent || '') + (r.model ? ' · ' + r.model : '')),
              chip(r.blocked ? '已拦截' : (r.n || 0) + ' 处', r.blocked ? 'red' : 'plain'),
              h('span', { class: 'grow' }),
              h('span', { class: 'meta' }, kinds.join(' / ')),
              btn('详情', { ghost: true, onClick: () => showRecord(r, load) }),
              ...(r.back ? [h('span', { class: 'meta' }, '回填 ' + Object.keys(r.back).length)] : []));
            cc.el.append(row);
          }
        }
        host.append(cc.el);
      } catch (e) { host.append(errorBox('读取失败：' + (e.message || e), load)); }
    }

    function kpi(k, v) { return h('div', { class: 'kpi' }, h('div', { class: 'k' }, k), h('div', { class: 'v' }, String(v))); }

    async function saveSetting(body, label) {
      try { await api.post('settings', body); toast(label + ' 已保存'); await load(); }
      catch (e) { toast('保存失败：' + (e.message || e)); }
    }
  },
};

// addRule 弹一个最小的输入流程（不引第三方 dialog，用 prompt 保持依赖面最小）。
function addRule(rules, reload) {
  const kind = (prompt('类型（例如 SECRET / TERM / API_KEY）', 'SECRET') || '').trim().toUpperCase();
  if (!kind) return;
  const mode = (prompt('匹配方式：prefix（前缀）或 regex（正则）', 'prefix') || '').trim().toLowerCase();
  if (mode !== 'prefix' && mode !== 'regex') { toast('匹配方式只能是 prefix 或 regex'); return; }
  const value = (prompt(mode === 'prefix' ? '前缀（例如 acme-sk-）' : '正则（例如 \\bacme_[0-9a-f]{32}\\b）', '') || '').trim();
  if (!value) return;
  const next = rules.concat([Object.assign({ kind }, mode === 'prefix' ? { prefix: value } : { regex: value })]);
  api.post('settings/redact-rules', { rules: next })
    .then(() => { toast('已添加'); reload(); })
    .catch((e) => toast('添加失败：' + (e.message || e)));
}

// editAllow 用一次 prompt 编辑整份白名单（逗号或换行分隔）。
function editAllow(allow, reload) {
  const text = prompt('白名单（逗号或换行分隔，整值匹配、忽略大小写）', (allow || []).join(', '));
  if (text === null) return;
  const next = text.split(/[,，\n]/).map((x) => x.trim()).filter(Boolean);
  api.post('settings/redact-allow', { allow: next })
    .then(() => { toast('已保存'); reload(); })
    .catch((e) => toast('保存失败：' + (e.message || e)));
}

// showRecord 展示一条记录的详情：命中值、实际发出去的文本（仅内存）。
function showRecord(r, reload) {
  const body = h('div');
  const vals = r.values || [];
  body.append(h('div', { class: 'meta' }, '这条请求共 ' + (r.n || 0) + ' 处命中' + (r.blocked ? '（含拦截规则，已拒绝发送）' : '')));
  const table = h('div', { class: 'list', style: 'margin-top:6px' });
  for (const v of vals) {
    table.append(h('div', { class: 'row' },
      h('span', { class: 'name' }, v.k),
      h('span', { class: 'mono meta' }, v.p || ('(' + (v.a || 'mask') + ')')),
      h('span', { class: 'meta' }, '首字符 ' + (v.h || '') + ' · 长度 ' + (v.l || 0)),
      h('span', { class: 'grow' }),
      chip(v.a || 'mask', v.a === 'block' ? 'red' : 'plain')));
  }
  body.append(table);
  const sentBox = h('pre', { class: 'code-block', style: 'max-height:240px;overflow:auto;display:none' });
  body.append(h('div', { class: 'inline', style: 'gap:8px;margin-top:8px' },
    btn('查看实际发出的文本', { onClick: async () => {
      try {
        const out = await api.get('masking/sent?key=' + encodeURIComponent(r.key));
        const msgs = out.messages || [];
        sentBox.style.display = '';
        sentBox.textContent = msgs.length
          ? msgs.map((m) => '[' + (m.role || 'user') + ']\n' + m.content).join('\n\n')
          : '(已过期：只保留最近 300 条请求的内存快照)';
      } catch (e) { toast('读取失败：' + (e.message || e)); }
    } })), sentBox);
  drawer('脱敏详情', (bodyEl) => { bodyEl.append(body); });
}

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
