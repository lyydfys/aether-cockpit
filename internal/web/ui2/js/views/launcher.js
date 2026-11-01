/* 应用启动（自有实现）：正常用户目录一键接入并启动 + 桌面端多开隔离实例。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { card, chip, btn, iconBtn, select, field, empty, errorBox, skeleton, toast, confirmDialog, fmtDate } from '../components.js';

const LABELS = {
  claude_code: 'Claude Code', codex_cli: 'Codex CLI', gemini_cli: 'Gemini CLI', opencode: 'OpenCode',
  claude_desktop: 'Claude 桌面端', codex_desktop: 'Codex 桌面端',
};
const labelOf = (id) => LABELS[id] || id;

export default {
  title: '应用启动',
  subtitle: '一键接入本网关并打开客户端；需要并行会话时再开隔离实例',

  mount(host) {
    const state = { data: null, desktopApp: '', busy: false };
    host.append(skeleton(6));
    load();

    async function load() {
      try {
        state.data = await api.get('launcher');
        const apps = multiApps(state.data);
        if (!state.desktopApp || !apps.some((a) => a.id === state.desktopApp)) {
          state.desktopApp = apps[0]?.id || '';
        }
        render();
      } catch (e) {
        clear(host);
        host.append(errorBox('启动信息读取失败：' + (e.message || e), load));
      }
    }

    function multiApps(d) {
      return ((d || {}).desktop || []).filter((a) => a.available && a.executable);
    }

    async function launchCli() {
      if (state.busy) return;
      state.busy = true;
      try {
        const out = await api.post('launcher/launch', { client_id: 'claude_code', model: '', isolate: false, connect: true });
        const inst = out.instance || out;
        toast('已接入网关并启动 Claude Code（PID ' + (inst.pid || '?') + '）');
        await load();
      } catch (e) { toast('启动失败：' + (e.message || e)); }
      finally { state.busy = false; }
    }

    async function launchDesktop(app) {
      if (state.busy) return;
      state.busy = true;
      try {
        const out = await api.post('launcher/desktop/launch', { app: app.id });
        toast(out.restart_required
          ? '已写入网关配置；' + app.name + ' 正在运行，重启后生效'
          : '已接入网关并打开 ' + app.name);
      } catch (e) { toast('启动失败：' + (e.message || e)); }
      finally { state.busy = false; }
    }

    async function launchDesktopInstance() {
      if (!state.desktopApp) { toast('没有可多开的桌面端'); return; }
      if (state.busy) return;
      state.busy = true;
      try {
        const out = await api.post('launcher/desktop/instance', { app: state.desktopApp });
        const inst = out.instance || out;
        toast('已启动 ' + labelOf(state.desktopApp) + ' 隔离实例（PID ' + (inst.pid || '?') + '）');
        await load();
      } catch (e) { toast('启动失败：' + (e.message || e)); }
      finally { state.busy = false; }
    }

    async function stop(inst) {
      if (!(await confirmDialog('停止「' + labelOf(inst.client_id) + '」？', '会结束该实例的整棵进程树；隔离目录会保留。', { danger: true, confirmText: '停止' }))) return;
      try { await api.post('launcher/' + inst.id + '/stop', {}); toast('已停止'); await load(); }
      catch (e) { toast('停止失败：' + (e.message || e)); }
    }

    async function cleanup(inst) {
      if (!(await confirmDialog('清理实例目录？', '会删除隔离实例的配置与会话（' + (inst.dir || '无隔离目录') + '），不可恢复。', { danger: true, confirmText: '删除目录' }))) return;
      try { const out = await api.post('launcher/' + inst.id + '/cleanup', { wipe: true }); toast(out.dir ? '已删除 ' + out.dir : '实例没有隔离目录'); await load(); }
      catch (e) { toast('清理失败：' + (e.message || e)); }
    }

    function render() {
      clear(host);
      const d = state.data || {};
      const commands = d.commands || {};
      const running = d.running || [];
      host.append(normalLaunchCard(d, commands));
      host.append(isolateCard(d));
      host.append(runningCard(running));
    }

    /* ---------- 正常目录一键接入并启动 ---------- */

    function normalLaunchCard(d, commands) {
      const c = card('一键启动', { chips: [chip('使用当前用户目录', 'plain'), chip('自动接入本网关', 'green')] });
      const grid = h('div', { class: 'grid-3' });
      const cliAvailable = !!commands.claude_code;
      grid.append(appTile({
        name: 'Claude Code',
        kind: 'CLI · 终端',
        note: cliAvailable ? (commands.claude_code + ' · 写入 ~/.claude 配置') : '未配置启动命令',
        available: cliAvailable,
        onLaunch: launchCli,
      }));
      const desktop = d.desktop || [];
      for (const app of desktop) {
        grid.append(appTile({
          name: app.name,
          kind: '桌面端',
          note: app.available ? (app.note || '使用当前用户目录与登录态') : (app.reason || '未检测到安装'),
          available: !!app.available,
          onLaunch: () => launchDesktop(app),
        }));
      }
      if (!desktop.length) {
        for (const name of ['Claude 桌面端', 'Codex 桌面端']) {
          grid.append(appTile({ name, kind: '桌面端', note: '当前平台暂不支持一键打开', available: false, onLaunch: () => {} }));
        }
      }
      c.el.append(grid);
      c.el.append(h('div', { class: 'meta', style: 'margin-top:8px' },
        '启动前会把本网关写进客户端的正常用户配置（自动备份）；Claude Code 在终端里启动，桌面端若已在运行需重启一次才会重新读取配置。需要独立目录或多个并行会话时，用下方的「多开隔离实例」。'));
      return c.el;
    }

    function appTile({ name, kind, note, available, onLaunch }) {
      return h('div', { class: 'inset', style: 'display:flex;flex-direction:column;gap:8px;min-height:132px' },
        h('div', { class: 'inline', style: 'gap:8px' }, h('b', null, name), chip(kind, 'plain')),
        h('div', { class: 'meta', style: 'flex:1' }, note),
        h('div', { class: 'inline' },
          btn('接入并启动', { primary: true, icon: 'play', disabled: !available, onClick: onLaunch }),
          available ? chip('已检测', 'green') : chip('不可用', 'red')));
    }

    /* ---------- 桌面端多开隔离实例 ---------- */

    function isolateCard(d) {
      const apps = multiApps(d);
      const c = card('多开隔离实例', { chips: [chip('桌面端', 'plain'), chip(apps.length + ' 个可多开', apps.length ? 'green' : 'plain')] });
      if (!apps.length) {
        c.el.append(empty('没有可多开的桌面端', '需要安装 Claude 桌面端或 Codex 桌面端；CLI 客户端请使用上方的 Claude Code 一键启动。'));
        return c.el;
      }
      const sel = select(apps.map((a) => ({ value: a.id, label: a.name })), state.desktopApp, (v) => { state.desktopApp = v; }, { label: '客户端' });
      sel.style.minWidth = '180px';
      c.el.append(h('div', { class: 'inline', style: 'gap:10px;flex-wrap:wrap;align-items:center' },
        h('span', { class: 'meta' }, '客户端'),
        sel,
        btn('启动隔离实例', { primary: true, icon: 'play', onClick: launchDesktopInstance }),
        h('span', { class: 'meta', style: 'margin-left:auto' }, '每个实例一个 --user-data-dir')));
      c.el.append(h('div', { class: 'meta', style: 'margin-top:8px' },
        '实例目录在 data/instances 下，启动前会把本网关写入正常配置。Codex 桌面端支持独立 profile；Claude 桌面端若限制单实例，可能只会聚焦已打开的窗口，此时会直接提示启动失败。'));
      return c.el;
    }

    /* ---------- 运行中的实例 ---------- */

    function runningCard(running) {
      const listCard = card('运行中的实例', { chips: [chip(running.length + ' 个', 'plain')] });
      listCard.head.append(h('span', { style: 'flex:1' }), iconBtn('refresh', '刷新进程列表', load));
      if (!running.length) {
        listCard.el.append(empty('没有运行中的实例', '一键启动的 CLI 会在这里追踪；桌面端一键打开不纳入停止列表，隔离实例会出现在这里。'));
        return listCard.el;
      }
      const list = h('div', { class: 'list' });
      for (const inst of running) {
        list.append(h('div', { class: 'row', style: 'align-items:flex-start;gap:10px' },
          h('div', { style: 'min-width:0;flex:1' },
            h('div', { class: 'name' }, labelOf(inst.client_id) + ' · PID ' + inst.pid),
            h('div', { class: 'meta', style: 'margin-top:2px' },
              [inst.command, inst.model || (inst.dir ? '隔离实例' : '默认模型'), '启动于 ' + fmtDate(inst.started_at, true), inst.dir || '非隔离'].join(' · ')),
            inst.seed_error ? h('div', { class: 'meta', style: 'color:var(--red)' }, '种子写入失败：' + inst.seed_error) : null),
          chip(inst.dir ? '隔离' : '共享配置', inst.dir ? 'green' : 'plain'),
          btn('停止', { danger: true, onClick: () => stop(inst) }),
          btn('清理目录', { ghost: true, onClick: () => cleanup(inst) })));
      }
      listCard.el.append(list);
      return listCard.el;
    }
  },
};
