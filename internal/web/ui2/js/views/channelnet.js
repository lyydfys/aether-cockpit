/* 渠道网络优化：连接预热 / 在途上限 / 边缘优选。
 *
 * 放在渠道详情抽屉的「网络」标签里——这些开关是"这个上游地址"的属性，
 * 不属于全局设置；全局设置只保留一份总览（见 settings.js）。
 * 所有操作只作用于网关内部，不修改系统 hosts。 */
import { h, clear } from '../dom.js';
import * as api from '../api.js';
import { btn, chip, input, toast, errorBox } from '../components.js';

const NUM = { type: 'number', min: '0', style: 'width:88px' };

// hostFromBase 从 base URL 里取主机名。
function hostFromBase(base) {
  try {
    return new URL(String(base || '')).hostname;
  } catch {
    return '';
  }
}

// channelNetFields 返回一个可插入抽屉的节点。
export function channelNetFields(providerId, baseUrl) {
  const box = h('div', { class: 'detail-fields' });

  // ---- 一、按渠道的连接策略 ----
  const pre = input({ ...NUM, placeholder: '45' });
  const inflight = input({ ...NUM, placeholder: '4' });
  const liveCount = chip('在途 —', 'plain');
  const saveRow = h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap' },
    h('span', { class: 'meta' }, '预热(秒)'), pre,
    h('span', { class: 'meta' }, '在途上限'), inflight,
    btn('保存', { onClick: () => saveProfile() }),
    btn('恢复默认', { ghost: true, onClick: () => saveProfile(true) }),
    liveCount);
  const profileNote = h('div', { class: 'meta', style: 'line-height:1.6' },
    '预热 = 每隔 N 秒打一次 /models（不消耗 token）把 TCP+TLS 保持热态；'
    + '在途上限 = 同时最多几条流，防止并发互相拖慢。都填 0 即恢复默认。');
  box.append(h('div', { class: 'detail-field', style: 'align-items:flex-start' },
    h('label', null, '连接策略'), h('div', { style: 'display:grid;gap:8px' }, saveRow, profileNote)));

  // ---- 二、边缘优选 ----
  const hostInput = input({ value: hostFromBase(baseUrl), placeholder: 'amr-link.example.com' });
  const result = h('div', { style: 'display:grid;gap:6px' });
  const pinRow = h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap' });
  const edgeBlock = h('div', { style: 'display:grid;gap:10px' },
    h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap' },
      hostInput,
      btn('测一下', { ghost: true, onClick: () => diagnose() }),
      btn('扫描并钉住', { ghost: true, onClick: () => scan() }),
      btn('全量扫描', { ghost: true, onClick: () => scan(true) })),
    result,
    pinRow,
    h('div', { class: 'meta', style: 'line-height:1.6' },
      '同一个域名在不同线路上会被 anycast 分到不同机房。扫描会逐个候选做真实 SNI 握手 + 证书校验，'
      + '读 /cdn-cgi/trace 确认机房，再按延迟排序钉住前两个。只作用于网关内部，不修改系统 hosts、'
      + '不需要管理员权限，随时可清除；钉住的 IP 失效会自动重新竞速。'));
  box.append(h('div', { class: 'detail-field', style: 'align-items:flex-start' },
    h('label', null, '边缘优选'), edgeBlock));
  // ---- 三、自动切换线路 ----
  // 对齐社区两个项目的常驻模式：定期测当前线路、扫候选、按阈值切换。
  // 区别是只作用于本渠道（不改系统 hosts、不需要管理员）。
  const autoPath = input({ value: '' , placeholder: '/v1/models（留空 = /cdn-cgi/trace）' });
  const autoInterval = input({ ...NUM, placeholder: '300' });
  const autoThreshold = input({ ...NUM, placeholder: '150' });
  const autoHealth = input({ ...NUM, placeholder: '30' });
  const autoStatus = h('div', { class: 'meta', style: 'line-height:1.6' });
  const autoBtn = btn('开启自动切换', { ghost: true, onClick: () => setAuto(true) });
  const autoOff = btn('关闭', { ghost: true, onClick: () => setAuto(false) });
  const autoBlock = h('div', { style: 'display:grid;gap:8px' },
    h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap' }, autoPath,
      h('span', { class: 'meta' }, '重扫(秒)'), autoInterval,
      h('span', { class: 'meta' }, '阈值(ms)'), autoThreshold,
      h('span', { class: 'meta' }, '采样(秒)'), autoHealth),
    h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap' }, autoBtn, autoOff),
    autoStatus,
    h('div', { class: 'meta', style: 'line-height:1.6' },
      '每轮先测当前线路（3 次取中位数），再扫候选（含 SJC 段）；' +
      '只有新线路快出阈值才切，当前线路全挂则立即切。切换结果写进固定表，跨重启保留。' +
      '回源在远端的站点请把路径填成真实 API 路径（如 /v1/models），否则只测到边缘、会选错机房。'));
  box.append(h('div', { class: 'detail-field', style: 'align-items:flex-start' },
    h('label', null, '自动切换'), autoBlock));

  loadProfile();
  loadPins();
  loadAuto();
  // 页面打开期间每 15 秒刷新一次运行态（劣化告警），离开后自动停。
  const poll = setInterval(() => {
    if (!box.isConnected) { clearInterval(poll); return; }
    loadAuto();
  }, 15000);
  return box;

  async function loadProfile() {
    try {
      const r = await api.get('channels/net');
      const row = (r.channels || []).find((x) => x.provider_id === providerId);
      if (row) {
        pre.value = row.prewarm_sec ? String(row.prewarm_sec) : '';
        inflight.value = row.max_inflight ? String(row.max_inflight) : '';
        setChip(liveCount, '在途 ' + (row.inflight || 0), (row.inflight || 0) > 0 ? 'green' : 'plain');
      } else {
        setChip(liveCount, '未开启优化', 'plain');
      }
    } catch {
      setChip(liveCount, '读取失败', 'amber');
    }
  }

  async function saveProfile(reset) {
    try {
      await api.post('channels/net', {
        provider_id: providerId,
        prewarm_sec: reset ? 0 : Number(pre.value) || 0,
        max_inflight: reset ? 0 : Number(inflight.value) || 0,
      });
      toast(reset ? '已恢复默认' : '已保存');
      await loadProfile();
    } catch (e) {
      toast('保存失败：' + (e.message || e));
    }
  }

  async function loadPins() {
    clear(pinRow);
    try {
      const r = await api.get('network/edge-pins');
      const host = hostInput.value.trim();
      const ips = (r.pins || {})[host] || [];
      if (!ips.length) return;
      pinRow.append(
        h('span', { class: 'meta' }, '已钉住'),
        h('span', { class: 'mono' }, ips.join(', ')),
        btn('清除', { ghost: true, onClick: async () => {
          try {
            await api.post('network/edge-pin', { host, ips: [] });
            toast('已清除');
            await loadPins();
          } catch (e) { toast('清除失败：' + (e.message || e)); }
        } }));
    } catch { /* 固定表读不到不影响其它字段 */ }
  }

  async function loadAuto() {
    const host = hostInput.value.trim();
    clear(autoStatus);
    if (!host) return;
    try {
      const r = await api.get('network/edge-auto');
      const row = (r.hosts || []).find((x) => x.state && x.state.host === host);
      if (!row) {
        autoStatus.append(h('span', null, '尚未开启自动切换'));
        return;
      }
      const cfg = row.config || {};
      const st = row.state || {};
      autoBtn.textContent = cfg.enabled ? '已开启' : '开启自动切换';
      autoBtn.disabled = !!cfg.enabled;
      autoOff.disabled = !cfg.enabled;
      if (cfg.path) autoPath.value = cfg.path;
      if (cfg.interval_sec) autoInterval.value = String(cfg.interval_sec);
      if (cfg.threshold_ms) autoThreshold.value = String(cfg.threshold_ms);
      if (cfg.health_sec) autoHealth.value = String(cfg.health_sec);
      const pins = (row.pins || []).join(', ');
      const bits = [];
      if (pins) bits.push('当前线路 ' + pins);
      if (st.current_ms) bits.push('当前 ' + st.current_ms + 'ms / 成功 ' + (st.current_ok || 0) + ' 次');
      if (st.best_ip) bits.push('最优 ' + st.best_ip + '（' + (st.best_ms || 0) + 'ms）');
      if (st.usable) bits.push('可用 ' + st.usable + '/' + (st.scanned || 0));
      if (st.switches) bits.push('已切换 ' + st.switches + ' 次');
      if (st.live_ms) bits.push('真实首 token ' + st.live_ms + 'ms' + (st.live_baseline_ms ? '（基线 ' + st.live_baseline_ms + 'ms）' : ''));
      if (st.reason) bits.push(st.reason);
      if (st.error) bits.push('错误：' + st.error);
      autoStatus.append(h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap' },
        st.degraded ? chip('线路劣化', 'red') : chip(cfg.enabled ? '监控中' : '未监控', cfg.enabled ? 'green' : 'plain'),
        h('span', null, bits.join(' · ') || '等待首轮检测')));
      for (const a of (st.alerts || []).slice(0, 3)) {
        const t2 = new Date((a.at || 0) * 1000);
        const hhmm = String(t2.getHours()).padStart(2, '0') + ':' + String(t2.getMinutes()).padStart(2, '0');
        autoStatus.append(h('div', { class: 'meta', style: 'color:' + (a.level === 'warn' ? 'var(--red, #dc2626)' : 'inherit') },
          hhmm + ' · ' + (a.message || '')));
      }
    } catch (e) {
      autoStatus.append(h('span', null, '读取失败：' + (e.message || e)));
    }
  }

  async function setAuto(enabled) {
    const host = hostInput.value.trim();
    if (!host) { toast('先填上游域名'); return; }
    try {
      await api.post('network/edge-auto', {
        host,
        enabled,
        path: autoPath.value.trim(),
        interval_sec: Number(autoInterval.value) || 0,
        threshold_ms: Number(autoThreshold.value) || 0,
        health_sec: Number(autoHealth.value) || 0,
      });
      toast(enabled ? '已开启，首轮检测在后台进行' : '已关闭自动切换');
      await loadAuto();
      if (enabled) setTimeout(loadAuto, 3000);
    } catch (e) {
      toast('保存失败：' + (e.message || e));
    }
  }

  async function diagnose() {
    const host = hostInput.value.trim();
    if (!host) { toast('先填上游域名'); return; }
    clear(result);
    result.append(h('div', { class: 'meta' }, '正在探测…'));
    try {
      const r = await api.get('network/diag?host=' + encodeURIComponent(host));
      clear(result);
      result.append(h('div', { class: 'meta', style: 'line-height:1.6' }, r.summary || ''));
      for (const ip of r.ips || []) {
        result.append(ipLine(ip));
      }
    } catch (e) {
      clear(result);
      result.append(errorBox('探测失败：' + (e.message || e), diagnose));
    }
  }

  async function scan(full) {
    const host = hostInput.value.trim();
    if (!host) { toast('先填上游域名'); return; }
    clear(result);
    result.append(h('div', { class: 'meta' },
      full ? '正在全量扫描 288 个候选（约 20–60 秒）…' : '正在扫描候选边缘（约 5–20 秒）…'));
    try {
      const r = await api.get('network/edge-scan?host=' + encodeURIComponent(host) + (full ? '&full=1' : '') + '&apply=1');
      clear(result);
      const applied = (r.applied || []).join(', ');
      result.append(h('div', { class: 'meta', style: 'line-height:1.6' },
        '可用 ' + (r.usable || 0) + ' / 扫描 ' + (r.scanned || 0) + ' 个，用时 ' + (r.elapsed_ms || 0) + ' ms'
        + (r.full ? '（全量池）' : '') + (applied ? '；已钉住 ' + applied : '')));
      for (const ip of r.best || []) {
        result.append(ipLine({ ...ip, ok: true }));
      }
      await loadPins();
    } catch (e) {
      clear(result);
      result.append(errorBox('扫描失败：' + (e.message || e), scan));
    }
  }

  function ipLine(ip) {
    const parts = [];
    if (ip.tcp_ms) parts.push('TCP ' + ip.tcp_ms + 'ms');
    if (ip.tls_ms) parts.push('TLS ' + ip.tls_ms + 'ms');
    if (ip.ttfb_ms) parts.push('首字节 ' + ip.ttfb_ms + 'ms');
    return h('div', { class: 'inline', style: 'gap:8px;flex-wrap:wrap' },
      h('span', { class: 'mono' }, ip.ip),
      ip.colo ? chip(ip.colo, 'plain') : null,
      h('span', { class: 'meta' }, parts.join(' · ') || '—'),
      h('span', { class: 'grow' }),
      chip(ip.ok ? '可用' : (ip.tcp_error || ip.tls_error || ip.http_error || '不可用'), ip.ok ? 'green' : 'red'));
  }
}

// setChip 就地换掉 chip 的文字与配色（chip 是一次性组件，不能直接改 className）。
function setChip(el, text, tone) {
  el.textContent = text;
  el.className = 'chip' + (tone && tone !== 'plain' ? ' ' + tone : ' plain');
}
