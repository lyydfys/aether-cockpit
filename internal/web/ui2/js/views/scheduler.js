/* 唤醒任务（自有实现）：签到与依赖队列的真实状态 + 手动入队。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { card, chip, btn, iconBtn, empty, errorBox, skeleton, toast, fmtDate, fmtReset, input, select, toggle, confirmDialog, trapFocus } from '../components.js';
import { icon } from '../icons.js';

export default {
  title: '唤醒任务',
  subtitle: '签到、额度刷新与依赖顺序',

  mount(host, ctx) {
    const state = {
      data: null, busy: false, wake: [], accounts: [], providers: [],
      status: '', tone: '', poll: null, pollTimer: null, idle: 0, pollOnDone: null, oneClickSince: 0,
    };
    host.append(skeleton(6));
    load();

    async function load() {
      try {
        const [sched, wake, accts, provs] = await Promise.all([
          api.get('scheduler').catch((e) => ({ __failed: e.message || String(e) })),
          api.get('wake').catch(() => ({ tasks: [] })),
          api.get('accounts').catch(() => ({ accounts: [] })),
          api.get('providers').catch(() => ({ providers: [] })),
        ]);
        state.data = sched;
        state.wake = wake.tasks || [];
        state.accounts = accts.accounts || [];
        state.providers = provs.providers || provs || [];
        render();
        if (!state.poll && (maxPending() > 0 || state.wake.some((t) => t.running))) {
          followTasks('检测到后台任务');
        }
      } catch (e) {
        clear(host);
        host.append(errorBox('任务读取失败：' + (e.message || e), load));
      }
    }

    function relative(unix) {
      if (!unix) return '—';
      const diff = unix * 1000 - Date.now();
      if (diff <= 0) return '即将触发';
      const min = Math.round(diff / 60000);
      if (min < 60) return min + ' 分钟后';
      const hr = Math.round(min / 60);
      if (hr < 48) return hr + ' 小时后';
      return Math.round(hr / 24) + ' 天后';
    }

    function maxPending() {
      const jobs = (state.data && state.data.jobs) || [];
      return jobs.reduce((n, j) => Math.max(n, Number(j.pending) || 0), 0);
    }

    function setStatus(text, tone = '') {
      state.status = text;
      state.tone = tone;
    }

    function statusBar() {
      return state.status ? h('div', { class: 'sched-status ' + (state.tone || '') }, state.status) : null;
    }

    async function runAll(e, oneClick = false) {
      if (state.busy) return;
      const startedAt = Math.floor(Date.now() / 1000);
      if (oneClick) state.oneClickSince = startedAt;
      state.busy = true;
      const el = e && e.currentTarget;
      if (el) { el.disabled = true; el.textContent = '入队中…'; }
      try {
        const out = await api.post('scheduler/run', {});
        state.busy = false;
        const n = Number(out.queued) || 0;
        const skipped = Number(out.skipped) || 0;
        if (n > 0) {
          setStatus('已入队 ' + n + ' 个任务，正在按渠道串行执行（间隔 2~6 秒）' +
            (skipped ? '；今日已完成 ' + skipped + ' 个，已跳过' : '') + '…', 'run');
          toast('已入队 ' + n + ' 个任务' + (skipped ? '，跳过 ' + skipped + ' 个今日已完成' : ''));
          await load();
          followTasks('签到任务', oneClick ? () => showCheckinSummary(startedAt) : null);
        } else if (skipped > 0) {
          setStatus(out.note || ('今天已经签到过：跳过 ' + skipped + ' 个账号，无需重复。'), 'ok');
          toast('今天已经签到过，无需重复');
          await load();
          if (oneClick) showCheckinSummary(startedAt);
        } else {
          setStatus('没有可执行任务：相关渠道当前没有可用账号。', 'warn');
          toast('没有可执行任务：相关渠道没有可用账号');
          await load();
          if (oneClick) showCheckinSummary(startedAt);
        }
      } catch (err) {
        setStatus('入队失败：' + (err.message || err), 'warn');
        toast('入队失败：' + (err.message || err));
        render();
      } finally {
        state.busy = false;
        if (el && el.isConnected) { el.disabled = false; el.textContent = '全部入队'; }
      }
    }

    async function runOne(providerId, e) {
      const use = providerUsable(providerId);
      if (!use.ok) { toast(providerId + '：' + use.why); return; }
      const el = e && e.currentTarget;
      if (el) { el.disabled = true; el.textContent = '入队中…'; }
      try {
        const out = await api.post('scheduler/run/' + encodeURIComponent(providerId), {});
        const n = Number(out.queued) || 0;
        const skipped = Number(out.skipped) || 0;
        if (n > 0) {
          setStatus('已为 ' + providerId + ' 入队 ' + n + ' 个任务，正在执行' +
            (skipped ? '；今日已完成 ' + skipped + ' 个，已跳过' : '') + '…', 'run');
          toast('已入队 ' + n + ' 个任务' + (skipped ? '，跳过 ' + skipped + ' 个今日已完成' : ''));
          await load();
          followTasks(providerId + ' 任务');
        } else if (skipped > 0) {
          setStatus(out.note || (providerId + ' 今天已经签到过，无需重复。'), 'ok');
          toast(providerId + ' 今天已经签到过，无需重复');
          await load();
        } else {
          setStatus(providerId + ' 未入队：任务可能已在执行，或账号暂不可用。', 'warn');
          toast('未入队：任务可能已在执行，或账号暂不可用');
          await load();
        }
      } catch (err) {
        setStatus('入队失败：' + (err.message || err), 'warn');
        toast('入队失败：' + (err.message || err));
        render();
      } finally {
        if (el && el.isConnected) { el.disabled = false; el.textContent = '立即运行'; }
      }
    }

    // 入队是异步的：轮询 scheduler/wake，把后台执行结果及时刷到页面。
    function followTasks(label, onDone = null) {
      stopFollow('', '');
      state.pollOnDone = typeof onDone === 'function' ? onDone : null;
      setStatus(label + '：正在执行，页面会自动刷新…', 'run');
      render();
      state.poll = setInterval(async () => {
        if (!host.isConnected) { stopFollow('', ''); return; }
        try {
          const [sched, wake] = await Promise.all([api.get('scheduler'), api.get('wake')]);
          state.data = sched;
          state.wake = wake.tasks || [];
          const pending = maxPending();
          const running = state.wake.some((t) => t.running);
          render();
          if (pending === 0 && !running) {
            state.idle++;
            if (state.idle >= 2) stopFollow('本轮任务已完成', 'ok');
          } else {
            state.idle = 0;
          }
        } catch { /* 网络抖动时继续轮询 */ }
      }, 1500);
      state.pollTimer = setTimeout(() => {
        if (state.poll) stopFollow('后台仍在执行，可稍后刷新查看', 'warn');
      }, 45000);
    }

    function stopFollow(text, tone) {
      if (state.poll) clearInterval(state.poll);
      state.poll = null;
      if (state.pollTimer) clearTimeout(state.pollTimer);
      state.pollTimer = null;
      state.idle = 0;
      const done = state.pollOnDone;
      state.pollOnDone = null;
      if (text) setStatus(text, tone);
      if (text && host.isConnected) render();
      if (done && text) setTimeout(done, 0);
    }

    function render() {
      clear(host);
      if (state.status) host.append(statusBar());
      const d = state.data || {};
      if (d.__failed) host.append(card('签到调度读取失败', {}).el).lastChild.append(empty('读取失败', d.__failed));
      if (!d.enabled) {
        host.append(card('签到调度未启动').el);
        host.lastChild.append(empty('签到调度不可用', d.reason || '桌面端会自动启动签到调度器；headless 模式按需开启。'));
        host.append(wakeCard());
        return;
      }
      const info = d.info || {};
      const jobs = d.jobs || [];
      const kpis = h('div', { class: 'kpis' },
        kpi('任务数', info.jobs ?? jobs.length, '按渠道分组'),
        kpi('今日完成', info.completed_today ?? 0, '签到 / 刷新'),
        kpi('下次唤醒', info.next_fire_at ? fmtReset(new Date(info.next_fire_at * 1000).toISOString()) : '—', relative(info.next_fire_at)),
        kpi('唤醒次数', info.wake_count ?? 0, '本进程'));
      host.append(kpis);

      const head = card('任务队列', { chips: [chip(jobs.length + ' 条', 'plain')] });
      head.head.append(h('span', { style: 'flex:1' }));
      head.head.append(iconBtn('refresh', '重新读取', load));
      head.head.append(btn('一键签到', { primary: true, icon: 'check', disabled: state.busy, onClick: (e) => runAll(e, true) }));
      if (!jobs.length) {
        head.el.append(empty('还没有任务', '签到任务由渠道能力决定；配好账号后会自动出现。'));
        host.append(head.el);
        host.append(wakeCard());
        return;
      }
      const list = h('div', { class: 'list' });
      const sorted = jobs.slice().sort((a, b) => (a.next_run || 0) - (b.next_run || 0));
      for (const j of sorted) {
        const ok = /成功|ok|✓/i.test(String(j.last_result || ''));
        const failed = j.last_result && !ok;
        const use = providerUsable(j.provider_id);
        const done = Number(j.today_done) || 0;
        const total = Number(j.today_total) || 0;
        const allDone = total > 0 && done >= total;
        const runText = j.last_run ? '上次 ' + fmtDate(j.last_run, true) : (allDone ? '今日已完成' : '还没跑过');
        list.append(h('div', { class: 'row', style: 'align-items:flex-start;gap:10px' },
          h('div', { style: 'min-width:0;flex:1' },
            h('div', { class: 'name' }, j.title || j.task_id),
            h('div', { class: 'meta', style: 'margin-top:2px' },
              [j.provider_id, 'cron ' + (j.cron || '—'), runText, j.pending ? '排队 ' + j.pending : ''].filter(Boolean).join(' · '))),
          j.optional ? chip('可选', 'plain') : null,
          j.depends_on && j.depends_on.length ? chip('依赖 ' + j.depends_on.length, 'plain') : null,
          allDone ? chip('今日已完成', 'green') : (done > 0 ? chip('今日 ' + done + '/' + total, 'amber') : null),
          j.last_result ? chip(j.last_result, failed ? 'red' : (ok ? 'green' : 'plain')) : null,
          use.ok ? chip(use.n + ' 账号', 'plain') : chip(use.why, 'plain'),
          h('span', { class: 'meta', style: 'min-width:90px;text-align:right' }, relative(j.next_run)),
          allDone
            ? btn('今日已完成', { class: 'sm', onClick: () => {
              setStatus((j.provider_id || '') + ' · ' + (j.title || j.task_id) + ' 今天已经签到过，无需重复；下次 ' + relative(j.next_run) + '。', 'ok');
              toast('今天已经签到过，无需重复');
              render();
            } })
            : (use.ok ? btn('立即运行', { class: 'sm', title: '为 ' + use.n + ' 个可用账号入队', onClick: (e) => runOne(j.provider_id, e) }) : null)));
      }
      head.el.append(list);
      if (info.state_path) head.el.append(h('div', { class: 'meta', style: 'margin-top:10px' }, '状态文件 ' + info.state_path));
      host.append(head.el);
      host.append(wakeCard());
    }

    // --- 一键签到结果 ---

    async function showCheckinSummary(since) {
      let s;
      try {
        s = await api.get('scheduler/checkin-summary?since=' + encodeURIComponent(since || 0));
      } catch (err) {
        setStatus('签到结果读取失败：' + (err.message || err), 'warn');
        toast('签到结果读取失败：' + (err.message || err));
        render();
        return;
      }
      const c = s.counts || {};
      const newSuccess = Number(c.new_success) || 0;
      const already = Number(c.already) || 0;
      const today = newSuccess + already;
      const failed = Number(c.failed) || 0;
      const noAccount = Number(c.no_account) || 0;
      const pending = (Number(c.pending) || 0) + (Number(c.not_run) || 0);
      setStatus('签到完成：本次成功 ' + newSuccess + ' 个，今日已签 ' + today +
        ' 个，无账号 ' + noAccount + ' 个，失败 ' + failed + ' 个' +
        (pending ? '；另有 ' + pending + ' 个未完成' : ''), failed ? 'warn' : 'ok');
      toast('签到完成：本次成功 ' + newSuccess + ' 个，今日已签 ' + today + ' 个' + (failed ? '，失败 ' + failed + ' 个' : ''));
      render();
      openCheckinModal(s, today);
    }

    function openCheckinModal(s, today) {
      const c = s.counts || {};
      const items = Array.isArray(s.items) ? s.items : [];
      const failures = [];
      for (const it of items) {
        for (const f of it.failures || []) {
          failures.push({ provider: it.provider_name || it.provider_id, title: it.title || it.task_id, ...f });
        }
      }
      const noAccounts = items.filter((it) => it.status === 'no_account');
      const unavailable = items.filter((it) => it.status === 'unavailable');
      const pending = items.filter((it) => it.status === 'pending' || it.status === 'not_run');
      const wrap = h('div', { class: 'modal-wrap' });
      const close = () => {
        release();
        wrap.classList.remove('on');
        document.removeEventListener('keydown', onKey, true);
        setTimeout(() => wrap.remove(), 160);
      };
      const kpi = (label, value, tone, hint) => h('div', { class: 'checkin-kpi ' + (tone || '') },
        h('b', null, String(value)), h('span', null, label), hint ? h('em', null, hint) : null);
      const box = h('div', { class: 'modal checkin-modal', role: 'dialog', 'aria-modal': 'true', 'aria-label': '一键签到结果' },
        h('div', { class: 'update-head' },
          h('div', { class: 'about-brand sm' }, icon('check', 26)),
          h('div', { class: 'grow' },
            h('h2', null, '一键签到结果'),
            h('p', null, '本次执行结果与今日累计')),
          iconBtn('x', '关闭', close)),
        h('div', { class: 'checkin-body' },
          h('div', { class: 'checkin-kpis' },
            kpi('本次成功', Number(c.new_success) || 0, 'ok', '账号'),
            kpi('今日已签', today, '', '账号'),
            kpi('无账号', Number(c.no_account) || 0, 'warn', '渠道'),
            kpi('失败', Number(c.failed) || 0, (Number(c.failed) || 0) ? 'failed' : '', '账号')),
          h('div', { class: 'checkin-today' }, '今日累计已签到 ' + today + ' 个账号' +
            ((Number(c.already) || 0) ? '（其中 ' + c.already + ' 个在本次之前已完成）' : '') + '。'),
          failures.length ? h('div', { class: 'checkin-block' },
            h('h3', null, '失败原因'),
            ...failures.map((f) => h('div', { class: 'checkin-failure' },
              h('div', { class: 'who' }, f.provider + ' · ' + f.title + (f.label ? ' · ' + f.label : '')),
              h('div', { class: 'why' }, f.message || '上游未返回失败原因')))) : null,
          noAccounts.length ? h('div', { class: 'checkin-block' },
            h('h3', null, '无可用账号'),
            h('div', { class: 'checkin-list' }, ...noAccounts.map((it) => chip(it.provider_name || it.provider_id, 'plain')))) : null,
          unavailable.length ? h('div', { class: 'checkin-block' },
            h('h3', null, '账号不可用'),
            h('div', { class: 'checkin-list' }, ...unavailable.map((it) => chip(it.provider_name || it.provider_id, 'plain')))) : null,
          pending.length ? h('div', { class: 'checkin-block' },
            h('h3', null, '仍在执行 / 未执行'),
            h('div', { class: 'checkin-list' }, ...pending.map((it) => chip(it.provider_name || it.provider_id, 'amber')))) : null,
          !failures.length && !noAccounts.length && !unavailable.length && !pending.length
            ? h('div', { class: 'checkin-today' }, '所有可用账号均已完成签到。') : null),
        h('div', { class: 'checkin-foot' }, btn('关闭', { onClick: close })));
      wrap.append(box);
      document.body.append(wrap);
      const onKey = (e) => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); close(); } };
      document.addEventListener('keydown', onKey, true);
      const release = trapFocus(box);
      requestAnimationFrame(() => wrap.classList.add('on'));
    }

    // --- 账号唤醒：定时发一个最小请求，提前触发上游额度重置周期 ---

    function providerOf(id) { return state.providers.find((p) => p.id === id); }

    function accountCount(providerId) {
      return state.accounts.filter((a) => a.provider_id === providerId && !a.disabled).length;
    }

    function providerUsable(providerId) {
      const p = providerOf(providerId);
      if (p && p.off) return { ok: false, why: '渠道已停用' };
      const n = accountCount(providerId);
      if (!n) return { ok: false, why: '无可用账号' };
      return { ok: true, n };
    }

    function wakeCard() {
      const list = state.wake || [];
      const chatAccts = state.accounts.filter((a) => !a.disabled && providerOf(a.provider_id)?.capabilities?.chat);
      const c = card('账号唤醒', { chips: [chip(list.length + ' 条', 'plain'), chip('提前触发额度重置', 'plain')] });
      const acctSel = select(chatAccts.map((a) => ({ value: a.account_id, label: a.label + ' · ' + a.provider_id })), chatAccts[0]?.account_id || '', () => {});
      acctSel.setAttribute('aria-label', '选择唤醒账号');
      const modelIn = input({ placeholder: '模型（留空用渠道默认）', label: '模型' });
      const everySel = select([
        { value: '60', label: '每 1 小时' },
        { value: '180', label: '每 3 小时' },
        { value: '300', label: '每 5 小时' },
        { value: '720', label: '每 12 小时' },
        { value: '1440', label: '每天' },
      ], '300', () => {}, { label: '唤醒间隔' });
      const createBtn = btn('创建任务', { primary: true, disabled: chatAccts.length === 0, onClick: async (e) => {
        const el = e.currentTarget; el.disabled = true; el.textContent = '创建中…';
        try {
          await api.post('wake', { account_id: acctSel.value, model: modelIn.value.trim(), every_minutes: Number(everySel.value) });
          setStatus('唤醒任务已创建；到点后会自动发送一个最小请求。', 'ok');
          toast('已创建唤醒任务');
          await load();
        } catch (err) {
          setStatus('创建失败：' + (err.message || err), 'warn');
          toast('创建失败：' + (err.message || err));
          render();
        }
      } });
      c.el.append(h('div', { class: 'inline', style: 'flex-wrap:wrap;padding-bottom:10px' }, acctSel, modelIn, everySel, createBtn,
        chatAccts.length ? null : h('span', { class: 'meta' }, '没有支持对话的账号')));
      if (!list.length) {
        c.el.append(empty('还没有唤醒任务', '定时对指定账号发一个最小请求，提前触发上游额度重置周期；账号卡上的闪电按钮可手动唤醒一次。'));
      } else {
        const box = h('div');
        for (const t of list) box.append(wakeRow(t));
        c.el.append(box);
      }
      return c.el;
    }

    function wakeRow(t) {
      const row = h('div', { class: 'row' });
      row.append(h('span', { class: 'name' }, t.label || t.account_id));
      row.append(chip(wakeEvery(t.every_minutes), 'plain'));
      if (t.running) row.append(chip('执行中', 'amber'));
      else if (t.last_ok) row.append(chip('上次成功', 'green'));
      else if (t.last_result) row.append(chip('上次失败', 'red'));
      else row.append(chip('未执行', 'plain'));
      const bits = [];
      if (t.enabled && t.next_run) bits.push('下次 ' + fmtDate(t.next_run));
      else bits.push('已停用');
      if (t.last_run) bits.push('上次 ' + fmtDate(t.last_run));
      if (t.last_model) bits.push(t.last_model);
      row.append(h('span', { class: 'meta', title: wakePreview(t) }, bits.join(' · ')));
      if (!t.last_ok && t.last_result) {
        row.append(h('span', { class: 'meta', style: 'color:var(--red);max-width:320px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap', title: t.last_result }, t.last_result));
      }
      row.append(h('span', { class: 'grow' }));
      row.append(toggle(t.enabled, async (on) => {
        try {
          await api.post('wake/' + encodeURIComponent(t.id), { enabled: on });
          setStatus(on ? '唤醒任务已启用' : '唤醒任务已停用', on ? 'ok' : 'warn');
          toast(on ? '已启用唤醒任务' : '已停用唤醒任务');
          await load();
        } catch (e) { toast('更新失败：' + (e.message || e)); }
      }, { label: '启用唤醒任务' }));
      row.append(btn('立即唤醒', { class: 'sm', onClick: async (e) => {
        const el = e.currentTarget; el.disabled = true; el.textContent = '触发中…';
        try {
          await api.post('wake/' + encodeURIComponent(t.id) + '/run', {});
          setStatus('已触发唤醒，正在请求上游…', 'run');
          toast('已触发唤醒');
          await load();
          followTasks('唤醒任务');
        } catch (err) {
          setStatus('唤醒失败：' + (err.message || err), 'warn');
          toast('唤醒失败：' + (err.message || err));
          render();
        }
      } }));
      row.append(iconBtn('trash', '删除任务', async () => {
        if (!(await confirmDialog('删除这条唤醒任务？', '只删除定时计划，不影响账号。', { danger: true, confirmText: '删除' }))) return;
        try {
          await api.del('wake/' + encodeURIComponent(t.id));
          setStatus('唤醒任务已删除', 'ok');
          toast('已删除唤醒任务');
          await load();
        } catch (e) { toast('删除失败：' + (e.message || e)); }
      }));
      return row;
    }

    function wakeEvery(m) {
      m = Number(m) || 0;
      if (m >= 1440 && m % 1440 === 0) return (m / 1440) + ' 天';
      if (m >= 60 && m % 60 === 0) return (m / 60) + ' 小时';
      return m + ' 分钟';
    }

    // 未来 3 次执行时间（悬停提示，对齐 local-vault 的 next-runs 预览）。
    function wakePreview(t) {
      if (!t.enabled || !t.next_run) return '任务已停用';
      const step = (Number(t.every_minutes) || 300) * 60;
      const out = [];
      let at = t.next_run;
      for (let i = 0; i < 3; i++) { out.push(fmtDate(at)); at += step; }
      return '未来执行：' + out.join(' → ');
    }

    function kpi(k, v, d) {
      return h('section', { class: 'kpi' }, h('div', { class: 'k' }, k), h('div', { class: 'v' }, String(v)), h('div', { class: 'd' }, d));
    }
  },
};
