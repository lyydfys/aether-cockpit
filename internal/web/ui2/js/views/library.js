/* 库：指令 / 技能 / MCP / 子代理 / Hooks 的浏览、发现与写入。
   页面上半部分是库内条目，下半部分是本机各 agent 已有的资源（只读发现，
   导入只写库，不改动本机文件）。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { chip, btn, iconBtn, card, empty, errorBox, skeleton, drawer, toast, copyText, confirmDialog, input, select, field, textarea, fmtDate } from '../components.js';
import { openInBrowser } from '../links.js';

const TABS = [['prompts', '指令'], ['skills', '技能'], ['servers', 'MCP 服务器'], ['subagents', '子代理'], ['hooks', 'Hooks']];

const AGENT_NAMES = {
  claude_code: 'Claude Code', codex_cli: 'Codex CLI', gemini_cli: 'Gemini CLI',
  opencode: 'opencode', mimocode: 'MiMo Code', claude: 'Claude Code',
  qoder: 'Qoder', workbuddy: 'WorkBuddy', zcode: 'ZCode', trae: 'Trae', codebuddy: 'CodeBuddy',
  claude_desktop: 'Claude Desktop', grok: 'Grok Build', kimi: 'Kimi Code',
  dsh: 'DSH', openclaw: 'OpenClaw', pi: 'Pi', continue: 'Continue',
  vscode: 'VS Code', cline: 'Cline', roo: 'Roo Code', windsurf: 'Windsurf',
  hermes: 'Hermes', agy: 'Antigravity',
};
const agentName = (id) => AGENT_NAMES[id] || id || '';
const agentNames = (ids) => (ids || []).map(agentName).filter(Boolean).join(' · ');

// 共享指令只能写进有用户级指令文件约定的客户端；其余客户端 apply 会失败。
const PROMPT_TARGETS = { claude_code: 'claude', codex_cli: 'codex', gemini_cli: 'gemini' };

function fmtSize(n) {
  n = Number(n) || 0;
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
  return (n / 1024 / 1024).toFixed(1) + ' MB';
}

function kpi(k, v, d) {
  return h('section', { class: 'kpi' }, h('div', { class: 'k' }, k), h('div', { class: 'v' }, String(v)), h('div', { class: 'd' }, d));
}

export default {
  title: '库',
  subtitle: '指令 / 技能 / MCP / 子代理，一处维护、按需写入各个 agent',

  mount(host, ctx) {
    const state = { tab: 'prompts', data: null, q: '' };
    host.append(skeleton(8));
    load();

    async function load() {
      clear(host);
      try {
        state.data = await api.get('library');
        render();
      } catch (e) {
        clear(host);
        host.append(errorBox('库读取失败：' + (e.message || e), load));
      }
    }

    function libraryRows() {
      const d = state.data || {};
      return {
        prompts: d.prompts || [],
        skills: d.skills || [],
        servers: d.servers || [],
        subagents: d.subagents || [],
        hooks: d.hooks || [],
      };
    }

    function foundRows() {
      const d = state.data || {};
      return {
        prompts: ((d.instructions || {}).agents || []),
        skills: d.foundSkills || [],
        servers: (d.foundServers || []).map((x) => x.server || x),
        subagents: d.foundSubagents || [],
        files: ((d.instructions || {}).files || []),
        hooks: [],
      };
    }

    function visible(list) {
      const q = state.q.trim().toLowerCase();
      if (!q) return list;
      return list.filter((x) => {
        const hay = [x.name, x.title, x.description, x.path, x.command, x.url, x.repo, x.file, x.text, x.source, x.source_label, x.plugin, (x.sources || []).join(' '), agentNames(x.agents)]
          .filter(Boolean).join(' ').toLowerCase();
        return hay.includes(q);
      });
    }

    function render() {
      clear(host);
      const lib = libraryRows();
      const found = foundRows();
      host.append(statsBar(lib, found));
      host.append(tabBar(lib, found));
      host.append(toolbar(lib));
      if (state.tab === 'prompts') renderInstructions(lib.prompts, found.prompts);
      else if (state.tab === 'skills') renderSkills(lib.skills, found.skills);
      else if (state.tab === 'servers') renderServers(lib.servers, found.servers);
      else if (state.tab === 'subagents') renderSubagents(lib.subagents, found.subagents);
      else renderHooks(lib.hooks);
      host.append(h('div', { class: 'meta', style: 'padding:10px 2px 0' },
        '库里的条目可以一键写入本机各 agent；「本机已有」是只读发现，导入只写库，不改动原文件。本机发现覆盖 Claude / Codex / Qoder / Qoder CN / WorkBuddy / Gemini / Cursor / Claude Desktop / Grok / Kimi / DSH / MiMo / OpenClaw / Trae / ZCode / CodeBuddy / Pi 的用户目录、已启用插件、Claude 项目目录、工作区记忆与常见斜杠命令目录，不含应用商店缓存。'));
    }

    function availableCounts(lib, found) {
      const fileCount = found.prompts.filter((f) => f.exists).length + (found.files || []).length;
      return {
        prompts: lib.prompts.length + fileCount,
        skills: lib.skills.length + found.skills.length,
        servers: lib.servers.length + found.servers.length,
        subagents: lib.subagents.length + found.subagents.length,
        hooks: lib.hooks.length,
        fileCount,
      };
    }

    function statsBar(lib, found) {
      const n = availableCounts(lib, found);
      return h('div', { class: 'kpis', style: 'margin-bottom:12px;grid-template-columns:repeat(4,minmax(0,1fr))' },
        kpi('指令', n.prompts, '库中 ' + lib.prompts.length + ' · 本机 ' + n.fileCount),
        kpi('技能', n.skills, '库中 ' + lib.skills.length + ' · 本机 ' + found.skills.length),
        kpi('MCP 服务器', n.servers, '库中 ' + lib.servers.length + ' · 本机 ' + found.servers.length),
        kpi('子代理', n.subagents, '库中 ' + lib.subagents.length + ' · 本机 ' + found.subagents.length));
    }

    function tabBar(lib, found) {
      const total = availableCounts(lib, found);
      const tabs = h('div', { class: 'tabs', style: 'margin-bottom:10px' });
      for (const [id, label] of TABS) {
        const n = total[id] || 0;
        tabs.append(h('button', {
          class: 'tab' + (state.tab === id ? ' on' : ''), type: 'button',
          title: '库中 ' + ((lib[id] || []).length) + ' · 本机 ' + Math.max(0, n - (lib[id] || []).length),
          onclick: () => { state.tab = id; render(); },
        }, label, n ? h('span', { class: 'meta', style: 'margin-left:5px' }, String(n)) : null));
      }
      return tabs;
    }

    function toolbar(lib) {
      const bar = h('div', { class: 'toolbar', style: 'margin-bottom:12px' });
      bar.append(h('label', { class: 'search', style: 'max-width:320px' },
        h('input', {
          type: 'search', placeholder: '搜索名称、描述或路径…', value: state.q,
          oninput: (e) => {
            state.q = e.target.value;
            const pos = e.target.selectionStart;
            render();
            const next = host.querySelector('.search input');
            if (next) { next.focus(); next.setSelectionRange(pos, pos); }
          },
        })));
      bar.append(h('span', { class: 'grow' }));
      bar.append(iconBtn('refresh', '重新读取本机状态', load));
      return bar;
    }

    function section(title, opts, list, rowFn, emptyTitle, emptyHint) {
      const c = card(title, opts);
      if (!list.length) {
        c.el.append(h('div', { class: 'empty', style: 'padding:18px 0' }, h('b', null, emptyTitle), emptyHint ? h('div', { class: 'meta' }, emptyHint) : null));
      } else {
        const rows = h('div', { class: 'list' });
        for (const it of list) rows.append(rowFn(it));
        c.el.append(rows);
      }
      return c.el;
    }

    /* ---------- 指令 ---------- */

    function renderInstructions(prompts, files) {
      const lib = visible(prompts);
      host.append(section('共享指令集', {
        chips: [chip(String(prompts.length) + ' 条', 'plain')],
        actions: [btn('新建指令', { icon: 'plus', onClick: () => editPrompt(null) })],
      }, lib, promptRow, '还没有共享指令', '新建一条，或从下方的本机指令文件收录。'));

      const rows = visible(files);
      const existing = files.filter((f) => f.exists).length;
      host.append(h('div', { style: 'height:12px' }));
      host.append(section('本机指令文件', {
        chips: [chip(existing + ' / ' + files.length + ' 已存在', existing ? 'green' : 'plain')],
      }, rows, instructionRow, '没有可读取的 agent', '未检测到本机客户端。'));

      const docs = visible((state.data.instructions || {}).files || []);
      if (docs.length) {
        host.append(h('div', { style: 'height:12px' }));
        host.append(section('本机记忆 / 项目指令', {
          chips: [chip(docs.length + ' 个', 'plain')],
        }, docs, instructionRow, '没有本机记忆或项目指令', ''));
      }

      host.append(h('div', { class: 'meta', style: 'padding:8px 2px' },
        '每个 agent 从各自的用户级文件读取指令；「本机记忆 / 项目指令」来自 Qoder/Qoder CN 工作区记忆、WorkBuddy SOUL、自动识别的 Claude 项目与已添加项目。这里的「收录」只复制正文进共享指令集，不改写原文件。'));
    }

    function promptRow(it) {
      const targets = promptTargets().length;
      return h('div', { class: 'row2', role: 'button', tabindex: '0', onclick: () => detail(it) },
        h('span', { class: 't' }, it.name || it.id),
        h('span', { class: 'm' },
          h('span', { class: 'mono' }, (it.content || '').replace(/\s+/g, ' ').slice(0, 90) || '（空正文）'),
          targets ? chip('可写 ' + targets + ' 个 agent', 'plain') : null),
        h('span', { class: 'r' }, it.created_at ? h('span', null, fmtDate(it.created_at, false)) : null));
    }

    function promptTargets() {
      const ids = ((state.data.instructions || {}).agents || []).map((a) => PROMPT_TARGETS[a.agent]).filter(Boolean);
      return [...new Set(ids)];
    }

    function instructionRow(f) {
      const status = !f.exists ? chip('未创建', 'plain') : (!f.size ? chip('空文件', 'amber') : chip('有内容', 'green'));
      const title = f.file && !String(f.name || '').includes(f.file) ? f.name + ' · ' + f.file : (f.name || f.file || '');
      return h('div', { class: 'row2', role: 'button', tabindex: '0', onclick: () => instructionDetail(f) },
        h('span', { class: 't' }, title),
        h('span', { class: 'm' }, sourceChip(f), status, h('span', { class: 'mono' }, f.path || '未找到路径')),
        h('span', { class: 'r' },
          f.exists ? h('span', null, fmtSize(f.size)) : null,
          h('span', { class: 'meta' }, f.exists ? '查看正文' : '未写入')));
    }

    function instructionDetail(f) {
      const d = drawer((f.name || 'agent') + ' 指令', (body) => {
        body.append(h('div', { class: 'meta', style: 'word-break:break-all' }, f.path || ''));
        body.append(h('div', { class: 'inline', style: 'margin:8px 0' },
          !f.exists ? chip('文件未创建', 'plain') : (f.size ? chip('有内容 · ' + fmtSize(f.size), 'green') : chip('空文件', 'amber')),
          f.truncated ? chip('内容过长，仅显示前 256 KB', 'amber') : null));
        const pre = h('pre', { class: 'code-block', style: 'max-height:52vh;overflow:auto;white-space:pre-wrap' },
          f.exists && f.content ? f.content : (f.exists ? '（文件为空）' : '（该 agent 还没有写入指令文件）'));
        body.append(pre);
        let content = f.content || '';
        if (f.exists) {
          api.get('library/found/content?kind=instructions&path=' + encodeURIComponent(f.path || ''))
            .then((r) => {
              if (typeof r.text === 'string') {
                content = r.text;
                pre.textContent = content || '（文件为空）';
              }
            })
            .catch(() => { /* 保留列表预览 */ });
        }
        const acts = h('div', { class: 'inline', style: 'justify-content:flex-end' });
        acts.append(btn('复制', { onClick: () => copyText(content || '') }));
        if (f.exists && content.trim()) {
          acts.append(btn('收录为共享指令', { primary: true, onClick: () => {
            d.close();
            editPrompt(null, { name: f.name + ' 指令', description: '来自 ' + f.path, content });
          } }));
        }
        acts.append(btn('新建指令', { onClick: () => { d.close(); editPrompt(null, { name: f.name + ' 指令' }); } }));
        acts.append(btn('关闭', { ghost: true, onClick: () => d.close() }));
        body.append(acts);
      });
    }

    /* ---------- 技能 ---------- */

    function renderSkills(skills, found) {
      const lib = visible(skills);
      host.append(section('库中技能', {
        chips: [chip(String(skills.length) + ' 个', 'plain')],
        actions: [
          btn('从仓库安装', { icon: 'folder', onClick: installSkills }),
          btn('检查更新', { onClick: async () => {
            try { await api.post('library/skills/check', {}); toast('检查完成'); await load(); }
            catch (e) { toast('检查失败：' + (e.message || e)); }
          } }),
          btn('更新全部', { onClick: async () => {
            try { const r = await api.post('library/skills/update-all', {}); const n = (r.result?.updated || []).length; toast('已更新 ' + n + ' 个技能'); await load(); }
            catch (e) { toast('更新失败：' + (e.message || e)); }
          } }),
          btn('新建技能', { primary: true, icon: 'plus', onClick: () => editSkill(null) }),
        ],
      }, lib, skillRow, '库里还没有技能', '新建一个，或从下方「本机已有」导入。'));

      host.append(h('div', { style: 'height:12px' }));
      const rows = visible(found);
      host.append(section('本机已有（未入库）', {
        chips: [chip(String(found.length) + ' 个', found.length ? 'amber' : 'plain')],
        actions: rows.length ? [btn(state.q ? '导入当前 ' + rows.length + ' 条' : '全部导入', { primary: true, onClick: () => importAll('skills', rows) })] : [],
      }, rows, (it) => foundSkillRow(it), '没有发现未入库的技能', '扫描 Claude / Codex / Qoder / Qoder CN / WorkBuddy / Gemini / Cursor / Trae / ZCode / CodeBuddy / Claude Desktop 等用户与插件技能目录。'));
    }

    function skillRow(it) {
      const meta = [];
      if (it.kind) meta.push(it.kind);
      if (it.source || it.repo) meta.push(it.source || it.repo);
      return h('div', { class: 'row2', role: 'button', tabindex: '0', onclick: () => detail(it) },
        h('span', { class: 't' }, it.name),
        h('span', { class: 'm' },
          it.check ? checkChip(it.check) : null,
          it.edited ? chip('本地改过', 'amber') : null,
          it.agents?.length ? chip('已写入 ' + agentNames(it.agents), 'plain') : null,
          h('span', null, it.description || meta.join(' · ') || '—')),
        h('span', { class: 'r' }, it.path ? h('span', { class: 'mono', style: 'max-width:220px' }, it.path) : null));
    }

    function foundSkillRow(it) {
      return h('div', { class: 'row2', role: 'button', tabindex: '0', onclick: () => foundDetail('skills', it) },
        h('span', { class: 't' }, it.name),
        h('span', { class: 'm' },
          sourceChip(it),
          it.agents?.length ? chip(agentNames(it.agents), 'plain') : null,
          h('span', null, it.description || it.path || '—')),
        h('span', { class: 'r' }, btn('导入', { class: 'sm', onClick: (e) => { e.stopPropagation(); importOne('skills', it.name); } })));
    }

    /* ---------- MCP 服务器 ---------- */

    function renderServers(servers, found) {
      host.append(section('库中服务器', {
        chips: [chip(String(servers.length) + ' 个', 'plain')],
        actions: [btn('添加服务器', { icon: 'plus', onClick: editServer })],
      }, visible(servers), serverRow, '库里还没有 MCP 服务器', '添加一个，或从下方「本机已有」导入。'));

      host.append(h('div', { style: 'height:12px' }));
      const serverRows = visible(found);
      host.append(section('本机已有（未入库）', {
        chips: [chip(String(found.length) + ' 个', found.length ? 'amber' : 'plain')],
        actions: serverRows.length ? [btn(state.q ? '导入当前 ' + serverRows.length + ' 条' : '全部导入', { primary: true, onClick: () => importAll('servers', serverRows) })] : [],
      }, serverRows, foundServerRow, '没有发现未入库的服务器', '扫描各 Agent 用户配置、Qoder/Qoder CN、WorkBuddy、CodeBuddy、ZCode、Claude Desktop、Cursor、VS Code、Windsurf、Cline/Roo 与已启用插件里的 MCP 定义。'));
    }

    function serverLine(it) {
      if (it.transport === 'stdio' || it.command) {
        const args = (it.args || []).join(' ');
        return 'stdio · ' + [it.command, args].filter(Boolean).join(' ');
      }
      return (it.transport || 'http') + ' · ' + (it.url || '');
    }

    function serverRow(it) {
      const signIn = it.signIn || {};
      return h('div', { class: 'row2', role: 'button', tabindex: '0', onclick: () => detail(it) },
        h('span', { class: 't' }, it.name),
        h('span', { class: 'm' },
          h('span', { class: 'mono' }, serverLine(it)),
          it.agents?.length ? chip('已写入 ' + agentNames(it.agents), 'plain') : chip('未写入', 'plain'),
          it.transport !== 'stdio' ? chip(signIn.signedIn ? '已登录' : '未登录', signIn.signedIn ? 'green' : 'plain') : null),
        h('span', { class: 'r' }, it.disabledTools?.length ? h('span', null, '停用 ' + it.disabledTools.length + ' 个工具') : null));
    }

    function foundServerRow(it) {
      return h('div', { class: 'row2', role: 'button', tabindex: '0', onclick: () => foundDetail('servers', it) },
        h('span', { class: 't' }, it.name),
        h('span', { class: 'm' },
          sourceChip(it),
          h('span', { class: 'mono' }, serverLine(it)),
          it.agents?.length ? chip(agentNames(it.agents), 'plain') : null),
        h('span', { class: 'r' }, btn('导入', { class: 'sm', onClick: (e) => { e.stopPropagation(); importOne('servers', it.name); } })));
    }

    /* ---------- 子代理 ---------- */

    function renderSubagents(subagents, found) {
      host.append(section('库中子代理', {
        chips: [chip(String(subagents.length) + ' 个', 'plain')],
        actions: [btn('新建子代理', { icon: 'plus', onClick: editSubagent })],
      }, visible(subagents), subagentRow, '库里还没有子代理', '新建一个，或从下方「本机已有」导入。'));

      host.append(h('div', { style: 'height:12px' }));
      const agentRows = visible(found);
      host.append(section('本机已有（未入库）', {
        chips: [chip(String(found.length) + ' 个', found.length ? 'amber' : 'plain')],
        actions: agentRows.length ? [btn(state.q ? '导入当前 ' + agentRows.length + ' 条' : '全部导入', { primary: true, onClick: () => importAll('subagents', agentRows) })] : [],
      }, agentRows, foundSubagentRow, '没有发现未入库的子代理', '扫描 Claude / Codex / Qoder / Qoder CN / WorkBuddy / Trae / opencode 的用户目录、已启用插件与自动识别的项目 agents 目录。'));
    }

    function subagentRow(it) {
      return h('div', { class: 'row2', role: 'button', tabindex: '0', onclick: () => detail(it) },
        h('span', { class: 't' }, it.name),
        h('span', { class: 'm' },
          it.agents?.length ? chip('已写入 ' + agentNames(it.agents), 'plain') : chip('未写入', 'plain'),
          it.model ? chip(it.model, 'plain') : null,
          h('span', null, it.description || '—')),
        h('span', { class: 'r' }, it.skills?.length ? h('span', null, '技能 ' + it.skills.length) : null));
    }

    function foundSubagentRow(it) {
      return h('div', { class: 'row2', role: 'button', tabindex: '0', onclick: () => foundDetail('subagents', it) },
        h('span', { class: 't' }, it.name),
        h('span', { class: 'm' },
          sourceChip(it),
          it.agents?.length ? chip(agentNames(it.agents), 'plain') : null,
          it.model ? chip(it.model, 'plain') : null,
          h('span', null, it.description || it.path || '—')),
        h('span', { class: 'r' }, btn('导入', { class: 'sm', onClick: (e) => { e.stopPropagation(); importOne('subagents', it.name); } })));
    }

    /* ---------- Hooks ---------- */

    function renderHooks(hooks) {
      host.append(section('Hooks', {
        chips: [chip(String(hooks.length) + ' 个', 'plain')],
        actions: [btn('新建 Hook', { icon: 'plus', onClick: () => editHook(null) })],
      }, visible(hooks), hookRow, '还没有 Hook', '事件触发时执行命令；保存时会写入所选 agent 的配置。'));
    }

    function hookRow(it) {
      return h('div', { class: 'row2', role: 'button', tabindex: '0', onclick: () => detail(it) },
        h('span', { class: 't' }, it.name),
        h('span', { class: 'm' },
          chip(it.event || '—', 'plain'),
          it.matcher ? h('span', { class: 'mono' }, it.matcher) : null,
          h('span', { class: 'mono' }, it.command || ''),
          it.agents?.length ? chip('→ ' + agentNames(it.agents), 'plain') : null),
        h('span', { class: 'r' }, it.timeout ? h('span', null, it.timeout + 's') : null));
    }

    /* ---------- 导入 ---------- */

    async function importOne(kind, name) {
      try {
        await api.post('library/' + kind + '/import', { name });
        toast('已导入「' + name + '」');
        await load();
      } catch (e) {
        toast('导入失败：' + (e.message || e));
      }
    }

    async function importAll(kind, list) {
      const names = list.map((x) => x.name).filter(Boolean);
      if (!names.length) return;
      try {
        const r = await api.post('library/' + kind + '/import-all', { names });
        const ok = r.ok ?? names.length;
        const failed = (r.results || []).filter((x) => x.ok === false).length;
        toast(failed ? '已导入 ' + ok + ' 个，失败 ' + failed + ' 个' : '已导入 ' + ok + ' 个');
        await load();
      } catch (e) {
        toast('导入失败：' + (e.message || e));
      }
    }

    /* ---------- 本机条目的只读详情 ---------- */

    function foundDetail(kind, it) {
      const title = it.name || '本机条目';
      const d = drawer(title, (body) => {
        body.append(h('div', { class: 'inline', style: 'margin-bottom:8px' },
          chip('本机已有', 'plain'),
          it.agents?.length ? chip(agentNames(it.agents), 'plain') : null,
          it.model ? chip('模型 ' + it.model, 'plain') : null));
        if (it.path) body.append(h('div', { class: 'meta', style: 'word-break:break-all;margin-bottom:8px' }, it.path));
        if (kind === 'servers') {
          body.append(h('div', { class: 'meta', style: 'margin-bottom:8px' }, serverLine(it)));
        }
        let content = it.content || it.prompt || '';
        if (kind === 'servers') content = JSON.stringify({ transport: it.transport, command: it.command, args: it.args, url: it.url }, null, 2);
        const pre = h('pre', { class: 'code-block', style: 'max-height:52vh;overflow:auto;white-space:pre-wrap' }, content || '（没有可预览的内容）');
        body.append(pre);
        if (it.truncated) body.append(h('div', { class: 'meta', style: 'margin-top:6px' }, '列表里只带了预览；详情正在读取完整正文…'));
        if (kind === 'skills' || kind === 'subagents') {
          api.get('library/found/content?kind=' + kind + '&name=' + encodeURIComponent(it.name))
            .then((r) => {
              if (r.text) { content = r.text; pre.textContent = content; }
            })
            .catch((e) => { pre.textContent = (content || '') + '\n\n（读取完整正文失败：' + (e.message || e) + '）'; });
        }
        body.append(h('div', { class: 'inline', style: 'justify-content:flex-end' },
          btn('复制', { onClick: () => copyText(content || '') }),
          btn('导入到库', { primary: true, onClick: async () => {
            try {
              await api.post('library/' + kind + '/import', { name: it.name });
              d.close(); toast('已导入「' + it.name + '」'); await load();
            } catch (e) { toast('导入失败：' + (e.message || e)); }
          } }),
          btn('关闭', { ghost: true, onClick: () => d.close() })));
      });
    }

    /* ---------- 库内条目的详情 ---------- */

    function detail(it) {
      const d = drawer(it.name || it.title || '详情', (body) => {
        let content = it.content || it.prompt || it.config || it.description || '';
        if (state.tab === 'hooks') content = 'event: ' + it.event + (it.matcher ? '\nmatcher: ' + it.matcher : '') + '\ncommand: ' + it.command + (it.timeout ? '\ntimeout: ' + it.timeout + 's' : '') + '\nagents: ' + ((it.agents || []).join(', ') || '—');
        body.append(h('div', { class: 'meta' }, Object.entries(it).filter(([k]) => !['content', 'config', 'prompt', 'text'].includes(k)).map(([k, v]) => k + ': ' + (typeof v === 'string' ? v : JSON.stringify(v))).join('  ·  ')));
        const pre = h('pre', { class: 'code-block', style: 'max-height:52vh;overflow:auto;white-space:pre-wrap' }, typeof content === 'string' ? content : JSON.stringify(content, null, 2));
        body.append(pre);
        if (state.tab === 'skills') {
          api.get('library/skill?name=' + encodeURIComponent(it.name))
            .then((r) => { pre.textContent = r.text || ''; })
            .catch((e) => { pre.textContent = '（读取技能内容失败：' + (e.message || e) + '）'; });
        }
        if (state.tab === 'servers') {
          const disabled = new Set(it.disabledTools || []);
          const tools = h('div', { class: 'detail-models' });
          const status = h('span', { class: 'meta' }, disabled.size ? '已停用 ' + disabled.size + ' 个工具' : '未停用任何工具');
          const save = btn('保存工具开关', { primary: true, onClick: async () => {
            try {
              await api.post('library/servers/tools', { name: it.name, names: [...disabled] });
              toast('工具开关已保存'); d.close(); await load();
            } catch (e) { toast('保存失败：' + (e.message || e)); }
          } });
          save.style.display = 'none';
          const read = btn('读取工具清单', { onClick: async () => {
            read.disabled = true; read.textContent = '读取中…';
            clear(tools);
            try {
              const r = await api.post('library/mcp/check', { names: [it.name], fresh: true });
              const res = (r.servers && r.servers[it.name]) || r[it.name] || {};
              const list = res.toolList || [];
              if (!list.length) {
                tools.append(h('span', { class: 'meta' }, res.why ? '没有读到工具（' + res.why + (res.detail ? '：' + res.detail : '') + '）' : '服务器没有返回工具清单。'));
                return;
              }
              for (const name of list) {
                const box = h('input', { type: 'checkbox', checked: !disabled.has(name) });
                box.addEventListener('change', () => {
                  if (box.checked) disabled.delete(name); else disabled.add(name);
                  status.textContent = disabled.size ? '已停用 ' + disabled.size + ' 个工具' : '未停用任何工具';
                });
                tools.append(h('label', { class: 'chip plain', style: 'cursor:pointer;gap:6px' }, box, name));
              }
              save.style.display = '';
            } catch (e) {
              tools.append(h('span', { class: 'meta', style: 'color:var(--red)' }, '读取失败：' + (e.message || e)));
            } finally {
              read.disabled = false; read.textContent = '重新读取';
            }
          } });
          body.append(h('div', { class: 'detail-field' }, h('label', null, '工具'),
            h('div', { style: 'display:flex;flex-direction:column;gap:8px' }, h('div', { class: 'inline' }, status, read, save), tools)));
        }
        let pickedSkills = null;
        if (state.tab === 'skills') {
          const allowed = new Set(['claude_code', 'codex_cli', 'gemini_cli', 'opencode']);
          pickedSkills = new Set((state.data.agents || []).map((a) => a.id).filter((id) => allowed.has(id)));
          body.append(h('div', { class: 'detail-field' }, h('label', null, '写入 agent'), agentPicker(pickedSkills, (a) => allowed.has(a.id))));
        }
        const acts = h('div', { class: 'inline', style: 'justify-content:flex-end' },
          btn('复制', { onClick: () => copyText(pre.textContent || '') }));
        if (state.tab === 'prompts') {
          acts.append(btn('编辑', { onClick: () => { d.close(); editPrompt(it); } }));
          acts.append(btn('应用到 agent', { onClick: async () => {
            const targets = promptTargets();
            if (!targets.length) { toast('没有可写入的 agent'); return; }
            try {
              const r = await api.post('library/prompts/' + encodeURIComponent(it.id) + '/apply', { targets });
              const ok = r.ok ?? targets.length;
              toast(ok === targets.length ? '已写入 ' + ok + ' 个 agent' : '已写入 ' + ok + ' / ' + targets.length + ' 个 agent');
              d.close(); await load();
            } catch (e) { toast('应用失败：' + (e.message || e)); }
          } }));
          acts.append(btn('删除', { danger: true, onClick: async () => {
            if (!(await confirmDialog('删除「' + it.name + '」？', '', { danger: true, confirmText: '删除' }))) return;
            try { await api.del('library/prompts/' + encodeURIComponent(it.id)); d.close(); toast('已删除'); load(); } catch (e) { toast('删除失败：' + (e.message || e)); }
          } }));
        } else if (state.tab === 'skills') {
          acts.append(btn('编辑', { onClick: () => { const text = pre.textContent; d.close(); editSkill({ ...it, text }); } }));
          if (it.repo) {
            acts.append(btn('更新', { onClick: async () => {
              const replace = it.edited && await confirmDialog('本地改过这个技能的正文。', '继续会用仓库版本覆盖本地修改。', { danger: true, confirmText: '覆盖' });
              if (it.edited && !replace) return;
              try { await api.post('library/skills/update', { name: it.name, replace: !!replace }); toast('已检查并更新'); d.close(); await load(); }
              catch (e) { toast('更新失败：' + (e.message || e)); }
            } }));
          }
          acts.append(btn('应用', { onClick: async () => {
            const id = skillID(it.name);
            if (!id) { toast('找不到技能 ID，先点「重新读取」'); return; }
            const targets = [...new Set([...pickedSkills].map(skillTarget).filter(Boolean))];
            if (!targets.length) { toast('至少选一个 agent'); return; }
            try { const r = await api.post('library/skills/' + encodeURIComponent(id) + '/apply', { targets }); toast('已写入 ' + (r.ok ?? targets.length) + ' 个目标'); }
            catch (e) { toast('应用失败：' + (e.message || e)); }
          } }));
          acts.append(btn('删除', { danger: true, onClick: async () => {
            if (!(await confirmDialog('删除技能「' + it.name + '」？', '会从库和各种 agent 中移除。', { danger: true, confirmText: '删除' }))) return;
            const id = skillID(it.name);
            try { await api.del('library/skills/' + encodeURIComponent(id || it.name)); d.close(); toast('已删除'); load(); }
            catch (e) { toast('删除失败：' + (e.message || e)); }
          } }));
        } else if (state.tab === 'hooks') {
          acts.append(btn('编辑', { onClick: () => { d.close(); editHook(it); } }));
          acts.append(btn('删除', { danger: true, onClick: async () => {
            if (!(await confirmDialog('删除 Hook「' + it.name + '」？', '会从所有已写入的 agent 配置里移除。', { danger: true, confirmText: '删除' }))) return;
            try { await api.post('library/hooks/remove', { name: it.name }); d.close(); toast('已删除'); load(); }
            catch (e) { toast('删除失败：' + (e.message || e)); }
          } }));
        } else if (state.tab === 'servers') {
          const signIn = it.signIn || {};
          if (it.transport !== 'stdio') {
            acts.append(chip(signIn.signedIn ? '已登录' : (signIn.dead ? '登录已失效' : '未登录'), signIn.signedIn ? 'green' : 'plain'));
            if (signIn.signedIn) {
              acts.append(btn('退出登录', { onClick: async () => {
                if (!(await confirmDialog('退出「' + it.name + '」的登录？', '会撤销服务商侧授权，并把已写入的配置改回直连。', { danger: true, confirmText: '退出' }))) return;
                try { await api.post('library/mcp-signout', { name: it.name }); toast('已退出登录'); d.close(); await load(); }
                catch (e) { toast('退出失败：' + (e.message || e)); }
              } }));
            } else {
              acts.append(btn('登录', { onClick: () => mcpLogin(it) }));
            }
          }
          acts.append(btn('删除', { danger: true, onClick: async () => {
            if (!(await confirmDialog('移除「' + it.name + '」？', '会从各 agent 的 MCP 配置里移除。', { danger: true, confirmText: '移除' }))) return;
            try { await api.post('library/servers/remove', { name: it.name }); d.close(); toast('已移除'); load(); } catch (e) { toast('移除失败：' + (e.message || e)); }
          } }));
        } else if (state.tab === 'subagents') {
          acts.append(btn('删除', { danger: true, onClick: async () => {
            if (!(await confirmDialog('删除子代理「' + it.name + '」？', '', { danger: true, confirmText: '删除' }))) return;
            try { await api.post('library/subagents/remove', { name: it.name }); d.close(); toast('已删除'); load(); } catch (e) { toast('删除失败：' + (e.message || e)); }
          } }));
        }
        acts.append(btn('关闭', { ghost: true, onClick: () => d.close() }));
        body.append(acts);
      });
    }

    /* ---------- 新建 / 编辑 ---------- */

    function sourceChip(it) {
      const src = String(it.source || it.source_label || '').trim();
      if (!src) return chip('本机', 'plain');
      const more = String(it.sources || '').split(' · ').length > 2;
      const label = src.length > 28 ? src.slice(0, 27) + '…' : src;
      return h('span', { class: 'chip plain', title: src + (more ? '（多来源）' : '') }, label);
    }

    function checkChip(c) {
      if (!c || !c.status) return null;
      if (c.status === 'update') return chip('可更新', 'amber');
      if (c.status === 'gone') return chip('来源已删除', 'red');
      if (c.status === 'unknown') return chip('检查失败', 'red');
      return chip('最新', 'plain');
    }

    function installSkills() {
      const source = input({ placeholder: 'GitHub owner/repo、GitLab 地址或本机文件夹' });
      const found = h('div', { style: 'display:flex;flex-direction:column;gap:6px;max-height:34vh;overflow:auto' });
      const picked = new Set();
      const pickedAgents = new Set();
      let probed = null;
      const agentBox = h('div', { class: 'inline', style: 'flex-wrap:wrap' });
      for (const a of state.data.agents || []) {
        const b = h('button', { class: 'tab', type: 'button' }, a.name || a.id);
        b.addEventListener('click', () => {
          if (pickedAgents.has(a.id)) pickedAgents.delete(a.id); else pickedAgents.add(a.id);
          b.classList.toggle('on', pickedAgents.has(a.id));
        });
        agentBox.append(b);
      }
      const probeBtn = btn('探测', { onClick: async () => {
        clear(found);
        picked.clear();
        probed = null;
        if (!source.value.trim()) { toast('先填仓库或文件夹'); return; }
        probeBtn.disabled = true;
        found.append(h('div', { class: 'meta' }, '正在探测…'));
        try {
          const r = await api.post('library/skills/probe', { source: source.value.trim() });
          probed = r;
          clear(found);
          const list = r.candidates || [];
          if (!list.length) { found.append(empty('没有找到 SKILL.md', '确认仓库里有技能目录。')); return; }
          found.append(h('div', { class: 'meta' }, (r.source || '') + ' · ' + list.length + ' 个技能'));
          for (const c of list) {
            const box = h('input', { type: 'checkbox', checked: true });
            if (c.have) { box.checked = false; box.disabled = true; }
            picked.add(c.path);
            box.addEventListener('change', () => { if (box.checked) picked.add(c.path); else picked.delete(c.path); });
            found.append(h('label', { class: 'row', style: 'gap:8px;align-items:center' },
              box, h('span', { class: 'name' }, c.name), h('span', { class: 'meta', style: 'flex:1' }, c.description || c.path),
              c.have ? chip('已安装', 'plain') : null));
            if (c.have) picked.delete(c.path);
          }
        } catch (e) {
          clear(found);
          found.append(errorBox('探测失败：' + (e.message || e), () => {}));
        } finally { probeBtn.disabled = false; }
      } });
      const d = drawer('从仓库安装技能', (body) => {
        body.append(field('来源', h('div', { class: 'inline', style: 'gap:8px' }, source, probeBtn)));
        body.append(found);
        body.append(h('div', { class: 'detail-field' }, h('label', null, '安装后写入 agent（可留空）'), agentBox));
        body.append(h('div', { class: 'inline', style: 'justify-content:flex-end' },
          btn('取消', { onClick: () => d.close() }),
          btn('安装', { primary: true, onClick: async () => {
            if (!picked.size) { toast('先探测并选择技能'); return; }
            try {
              const r = await api.post('library/skills/install', { source: source.value.trim(), paths: [...picked], agents: [...pickedAgents] });
              const res = r.result || {};
              const n = (res.installed || res.paths || []).length || picked.size;
              const failed = (res.failed || []).length;
              toast(failed ? '已安装 ' + n + ' 个，失败 ' + failed + ' 个' : '已安装 ' + n + ' 个');
              d.close(); await load();
            } catch (e) { toast('安装失败：' + (e.message || e)); }
          } })));
      });
    }

    function skillID(name) {
      return (state.data.skills_raw || []).find((s) => s.name === name)?.id || '';
    }

    // 技能下发目标只有 claude（~/.claude/skills）与 agents（~/.agents/skills）两处。
    function skillTarget(agentID) {
      if (agentID === 'claude_code') return 'claude';
      if (['codex_cli', 'gemini_cli', 'opencode'].includes(agentID)) return 'agents';
      return '';
    }

    function agentPicker(picked, allow) {
      const wrap = h('div', { class: 'inline', style: 'flex-wrap:wrap' });
      for (const a of state.data.agents || []) {
        if (allow && !allow(a)) continue;
        const on = picked.has(a.id);
        const b = h('button', { class: 'tab' + (on ? ' on' : ''), type: 'button' }, a.name || a.id);
        b.addEventListener('click', () => {
          if (picked.has(a.id)) picked.delete(a.id); else picked.add(a.id);
          b.classList.toggle('on', picked.has(a.id));
        });
        wrap.append(b);
      }
      return wrap;
    }

    function editSkill(it) {
      const name = input({ value: it?.name || '', placeholder: '技能名，例如 review-helper' });
      const desc = input({ value: it?.description || '', placeholder: '一句话说明（可选）' });
      const content = textarea({ value: it?.text || it?.content || '', placeholder: 'SKILL.md 全文' });
      content.rows = 14;
      if (it && !it.text) {
        api.get('library/skill?name=' + encodeURIComponent(it.name))
          .then((r) => { content.value = r.text || ''; })
          .catch((e) => toast('读取技能内容失败：' + (e.message || e)));
      }
      const d = drawer(it ? '编辑技能' : '新建技能', (body) => {
        body.append(field('名称', name), field('说明', desc), field('SKILL.md', content));
        body.append(h('div', { class: 'inline', style: 'justify-content:flex-end' },
          btn('取消', { onClick: () => d.close() }),
          btn('保存', { primary: true, onClick: async () => {
            if (!name.value.trim() || !content.value.trim()) { toast('名称和正文不能为空'); return; }
            try { await api.post('library/skills', { name: name.value.trim(), description: desc.value.trim(), content: content.value }); d.close(); toast('已保存'); load(); }
            catch (e) { toast('保存失败：' + (e.message || e)); }
          } })));
      });
    }

    function editHook(it) {
      const name = input({ value: it?.name || '', placeholder: '名字，只能用字母、数字、- 和 _' });
      const desc = input({ value: it?.description || '', placeholder: '说明（可选）' });
      const event = select([{ value: 'PreToolUse', label: 'PreToolUse' }, { value: 'PostToolUse', label: 'PostToolUse' },
        { value: 'UserPromptSubmit', label: 'UserPromptSubmit' }, { value: 'Stop', label: 'Stop' },
        { value: 'SessionStart', label: 'SessionStart' }, { value: 'SessionEnd', label: 'SessionEnd' },
        { value: 'Notification', label: 'Notification' }], it?.event || 'PreToolUse', () => {});
      const matcher = input({ value: it?.matcher || '', placeholder: '匹配器（可选），例如 Bash' });
      const command = input({ value: it?.command || '', placeholder: '要执行的命令' });
      const timeout = input({ value: it?.timeout ? String(it.timeout) : '', placeholder: '超时秒数（可选）' });
      timeout.type = 'number';
      const picked = new Set(it?.agents || []);
      const agents = agentPicker(picked);
      const d = drawer(it ? '编辑 Hook' : '新建 Hook', (body) => {
        body.append(field('名称', name), field('说明', desc), field('事件', event), field('匹配器', matcher),
          field('命令', command), field('超时', timeout),
          h('div', { class: 'detail-field' }, h('label', null, '写入 agent'), agents));
        body.append(h('div', { class: 'inline', style: 'justify-content:flex-end' },
          btn('取消', { onClick: () => d.close() }),
          btn('保存', { primary: true, onClick: async () => {
            if (!name.value.trim() || !command.value.trim()) { toast('名称和命令不能为空'); return; }
            const hook = { name: name.value.trim(), description: desc.value.trim(), event: event.value, matcher: matcher.value.trim(), command: command.value.trim(), agents: [...picked] };
            if (timeout.value.trim()) hook.timeout = Number(timeout.value) || 0;
            try { await api.post('library/hooks/save', { old: it?.name || '', hook }); d.close(); toast('已保存'); load(); }
            catch (e) { toast('保存失败：' + (e.message || e)); }
          } })));
      });
    }

    function editPrompt(it, preset) {
      const name = input({ value: it?.name || preset?.name || '', placeholder: '名称，例如 工作规则' });
      const desc = input({ value: it?.description || preset?.description || '', placeholder: '一句话说明（可选）' });
      const content = textarea({ value: it?.content || preset?.content || '', placeholder: '指令正文（Markdown）' });
      content.rows = 12;
      const d = drawer(it ? '编辑指令' : '新建指令', (body) => {
        body.append(field('名称', name), field('说明', desc), field('正文', content));
        body.append(h('div', { class: 'inline', style: 'justify-content:flex-end' },
          btn('取消', { onClick: () => d.close() }),
          btn('保存', { primary: true, onClick: async () => {
            if (!name.value.trim() || !content.value.trim()) { toast('名称和正文不能为空'); return; }
            try { await api.post('library/prompts', { name: name.value.trim(), description: desc.value.trim(), content: content.value }); d.close(); toast('已保存'); load(); }
            catch (e) { toast('保存失败：' + (e.message || e)); }
          } })));
      });
    }

    function mcpLogin(it) {
      let flowId = '';
      let timer = 0;
      const status = h('div', { class: 'meta' }, '正在发起 OAuth…');
      const linkBox = h('div', { style: 'display:flex;flex-direction:column;gap:8px' });
      const d = drawer('登录 ' + it.name, (body) => {
        body.append(status, linkBox);
        body.append(h('div', { class: 'inline', style: 'justify-content:flex-end' },
          btn('取消登录', { ghost: true, onClick: async () => {
            if (flowId) { try { await api.post('library/mcp-signin/' + encodeURIComponent(flowId) + '/cancel', {}); } catch {} }
            d.close();
          } })));
      });
      const stop = () => { if (timer) clearInterval(timer); timer = 0; };
      (async () => {
        try {
          const out = await api.post('library/mcp-signin', { name: it.name });
          if (out.signedIn) { status.textContent = '已经登录'; toast('已经登录'); stop(); return; }
          flowId = out.id || '';
          status.textContent = '等待浏览器里完成授权…';
          if (out.url) {
            linkBox.append(btn('打开授权页面', { primary: true, icon: 'external', onClick: () => openInBrowser(out.url) }));
            openInBrowser(out.url);
          }
          const tick = async () => {
            if (!d.el.isConnected) { stop(); return; }
            try {
              const r = await api.get('library/mcp-signin/' + encodeURIComponent(flowId));
              if (r.state === 'done' || r.signedIn) {
                stop(); status.textContent = '登录成功';
                toast('登录成功'); d.close(); await load();
              } else if (r.state === 'failed' || r.state === 'canceled') {
                stop(); status.textContent = '登录失败：' + (r.error || r.state);
              }
            } catch (e) { stop(); status.textContent = '轮询失败：' + (e.message || e); }
          };
          timer = setInterval(tick, 2500);
          tick();
        } catch (e) {
          status.textContent = '无法开始登录：' + (e.message || e);
        }
      })();
    }

    function editServer() {
      const name = input({ placeholder: '名称，例如 fetch' });
      const transport = select([{ value: 'stdio', label: 'stdio' }, { value: 'sse', label: 'SSE' }, { value: 'http', label: 'HTTP' }], 'stdio', () => {});
      const command = input({ placeholder: 'stdio：命令，例如 uvx' });
      const args = input({ placeholder: 'stdio：参数，逗号分隔，例如 mcp-server-fetch' });
      const url = input({ placeholder: 'sse/http：地址' });
      const agents = h('div', { class: 'inline', style: 'flex-wrap:wrap' });
      const picked = new Set();
      for (const a of state.data.agents || []) {
        if (!a.mcp) continue;
        const b = h('button', { class: 'tab', type: 'button', onclick: () => { if (picked.has(a.id)) picked.delete(a.id); else picked.add(a.id); b.classList.toggle('on', picked.has(a.id)); } }, a.name);
        agents.append(b);
      }
      const d = drawer('添加 MCP 服务器', (body) => {
        body.append(field('名称', name), field('传输', transport), field('命令', command), field('参数', args), field('地址', url),
          h('div', { class: 'detail-field' }, h('label', null, '写入 agent'), agents),
          h('div', { class: 'inline', style: 'justify-content:flex-end' },
            btn('取消', { onClick: () => d.close() }),
            btn('保存', { primary: true, onClick: async () => {
              if (!name.value.trim()) { toast('名称不能为空'); return; }
              const server = { name: name.value.trim(), transport: transport.value };
              if (transport.value === 'stdio') { server.command = command.value.trim(); server.args = args.value.split(',').map((x) => x.trim()).filter(Boolean); }
              else server.url = url.value.trim();
              server.agents = [...picked];
              try { await api.post('library/servers/save', { server }); d.close(); toast('已保存'); load(); }
              catch (e) { toast('保存失败：' + (e.message || e)); }
            } })));
      });
    }

    function editSubagent() {
      const name = input({ placeholder: '名称，例如 reviewer' });
      const desc = input({ placeholder: '一句话说明' });
      const prompt = textarea({ value: '', placeholder: '系统提示词' });
      prompt.rows = 10;
      const model = input({ placeholder: '模型（可选），例如 provider/model' });
      // Claude Code 是目前唯一有子代理文件约定的客户端。
      const picked = new Set((state.data.agents || []).some((a) => a.id === 'claude_code') ? ['claude_code'] : []);
      const d = drawer('新建子代理', (body) => {
        body.append(field('名称', name), field('说明', desc), field('提示词', prompt), field('模型', model));
        body.append(h('div', { class: 'detail-field' }, h('label', null, '写入 agent'), agentPicker(picked, (a) => a.id === 'claude_code')));
        body.append(h('div', { class: 'inline', style: 'justify-content:flex-end' },
          btn('取消', { onClick: () => d.close() }),
          btn('保存', { primary: true, onClick: async () => {
            if (!name.value.trim() || !desc.value.trim() || !prompt.value.trim()) { toast('名称、说明和提示词不能为空'); return; }
            try {
              await api.post('library/subagents/save', {
                subagent: {
                  name: name.value.trim(), description: desc.value.trim(),
                  prompt: prompt.value, model: model.value.trim(), agents: [...picked],
                },
              });
              d.close(); toast('已保存'); load();
            }
            catch (e) { toast('保存失败：' + (e.message || e)); }
          } })));
      });
    }
  },
};
