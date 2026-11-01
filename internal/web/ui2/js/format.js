/* 纯格式化函数：不依赖 DOM，便于单测。 */

export const fmtTokens = (n) => {
  const v = Number(n) || 0;
  if (v >= 1e9) return (v / 1e9).toFixed(1) + 'B';
  if (v >= 1e6) return (v / 1e6).toFixed(1) + 'M';
  if (v >= 1e3) return (v / 1e3).toFixed(1) + 'K';
  return String(v);
};

export const fmtMoney = (n) => '$' + (Math.round((Number(n) || 0) * 100) / 100).toFixed(2);

/* 费用展示：内部按美元存储，展示层按币种与汇率换算。
   小额费用保留更多小数，避免日志里大量请求显示成 $0.00。 */
export function fmtCost(n, currency = 'USD', rate = 7.2) {
  const usd = Number(n) || 0;
  const v = currency === 'CNY' ? usd * rate : usd;
  const symbol = currency === 'CNY' ? '¥' : '$';
  if (!v) return symbol + '0';
  if (v < 0.01) return symbol + v.toFixed(6);
  if (v < 1) return symbol + v.toFixed(4);
  return symbol + v.toFixed(2);
}

export function fmtDate(unix, withTime = true, withSeconds = false) {
  if (!unix) return '—';
  const d = new Date(unix * 1000), p2 = (x) => String(x).padStart(2, '0');
  return d.getFullYear() + '/' + p2(d.getMonth() + 1) + '/' + p2(d.getDate()) +
    (withTime ? ' ' + p2(d.getHours()) + ':' + p2(d.getMinutes()) + (withSeconds ? ':' + p2(d.getSeconds()) : '') : '');
}

export function fmtReset(s) {
  if (!s) return '';
  const d = new Date(s), p2 = (x) => String(x).padStart(2, '0');
  return p2(d.getMonth() + 1) + '/' + p2(d.getDate()) + ' ' + p2(d.getHours()) + ':' + p2(d.getMinutes());
}

/* 额度百分比：后端三种口径并存——0–1 小数、0–100 百分数、amount/limit */
export function usedPct(w) {
  if (!w) return 0;
  if (w.amount != null && w.limit) {
    return Math.max(0, Math.min(100, (Number(w.amount) / Number(w.limit)) * 100));
  }
  const raw = Number(w.used) || 0;
  return Math.max(0, Math.min(100, raw <= 1 ? raw * 100 : raw));
}
