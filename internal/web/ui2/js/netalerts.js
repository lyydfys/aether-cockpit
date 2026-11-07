/* 线路劣化的全局即时告警。
 *
 * 网关侧在两次全量扫描之间每 30 秒采样一次当前线路，判定劣化/恢复时发事件：
 *   network.edge_degraded —— 当前线路连续失败或明显慢于基线（已自动触发切换尝试）
 *   network.edge_recovered —— 线路恢复正常
 *
 * 这里连一条 SSE（/api/events）把事件变成 toast，用户在任何页面都能看到；
 * 渠道详情里的「自动切换」区块还会显示最近几条告警与当前状态。
 */
import { toast } from './components.js';

// startNetAlerts 启动事件订阅；断线后自动重连。
export function startNetAlerts() {
  if (typeof EventSource === 'undefined') return;
  let stopped = false;
  let es = null;
  let retry = null;

  const connect = () => {
    if (stopped) return;
    try {
      es = new EventSource('/api/events');
    } catch {
      return;
    }
    es.addEventListener('network.edge_degraded', (ev) => {
      const d = safeParse(ev.data) || {};
      const host = d.host || '上游线路';
      const why = d.reason ? '（' + d.reason + '）'
        : (d.now_ms && d.baseline_ms ? '（' + d.now_ms + 'ms，基线 ' + d.baseline_ms + 'ms）' : '');
      toast('线路劣化：' + host + why + '，正在尝试切换');
    });
    es.addEventListener('network.edge_recovered', (ev) => {
      const d = safeParse(ev.data) || {};
      toast('线路已恢复：' + (d.host || '上游线路'));
    });
    es.onerror = () => {
      if (stopped) return;
      try { es.close(); } catch {}
      if (retry) clearTimeout(retry);
      retry = setTimeout(connect, 5000);
    };
  };
  connect();
}

function safeParse(raw) {
  try { return JSON.parse(raw); } catch { return null; }
}
