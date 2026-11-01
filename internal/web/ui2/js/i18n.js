/* 国际化：中文原文作为 key（gettext 式），未翻译的字符串原样回退。
 *
 * 设计取舍：
 *   - 翻译发生在 DOM 构建时（dom.js 的 h/append 调用 translateText），所以首帧就是目标语言，没有闪烁；
 *   - 切换语言采用「持久化 + 重载」：语言是低频操作，重载能保证所有文案（含启动时构建的导航）一致，
 *     也避免了运行时替换 / 还原带来的状态复杂度；
 *   - 计数类文案用规则匹配，避免把每种数字组合写进字典。
 */
const LOCALES = [
  { id: 'zh-CN', label: '中文' },
  { id: 'en', label: 'English' },
];

const ATTRS = new Set(['title', 'aria-label', 'placeholder', 'alt']);
const SKIP_TAGS = new Set(['CODE', 'PRE', 'SCRIPT', 'STYLE', 'TEXTAREA']);

const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
/* 计数类文案用规则，避免把每一种数字组合都写进字典 */
/** @type {Array<[RegExp, (m: RegExpMatchArray) => string]>} */
const RULES = [
  [/^更新于 (.+)$/, (m) => `Updated ${m[1]}`],
  [/^快照 (.+)$/, (m) => `Snapshot ${m[1]}`],
  [/^刷新失败，显示的是 (.+) 的快照$/, (m) => `Refresh failed; showing the ${m[1]} snapshot`],
  [/^(\d{1,2})月$/, (m) => MONTHS[Number(m[1]) - 1] || m[0]],
  [/^(\d+) 把$/, (m) => `${m[1]} keys`],
  [/^剩余 (\$[\d.,]+)$/, (m) => `${m[1]} left`],
  [/^(\d+) 个渠道 → (\d+) 个端点$/, (m) => `${m[1]} channels → ${m[2]} endpoints`],
  [/^就绪 · (\d+) 账号$/, (m) => `Ready · ${m[1]} accounts`],
  [/^未就绪 · (\d+) 账号$/, (m) => `Not ready · ${m[1]} accounts`],
  [/^1 账号$/, () => '1 account'],
  [/^(\d+) 个账号$/, (m) => `${m[1]} accounts`],
  [/^(\d+) 账号$/, (m) => `${m[1]} accounts`],
  [/^(\d+) 个额度窗口$/, (m) => `${m[1]} quota windows`],
  [/^(\d+) 个条目$/, (m) => `${m[1]} entries`],
  [/^(\d+) 个任务$/, (m) => `${m[1]} tasks`],
  [/^(\d+) 条$/, (m) => `${m[1]} items`],
  [/^(\d+) 次$/, (m) => `${m[1]} calls`],
  [/^近 (\d+) 天$/, (m) => `Last ${m[1]} days`],
  [/^(\d+) 次请求$/, (m) => `${m[1]} requests`],
  [/^(\d+) 次 · (.+) Token$/, (m) => `${m[1]} calls · ${m[2]} tokens`],
  [/^(\d+) 类$/, (m) => `${m[1]} types`],
  [/^合计 (\d+) 次$/, (m) => `Total ${m[1]} calls`],
  [/^峰值 (\d+) 次$/, (m) => `Peak ${m[1]} calls`],
  [/^失败 (\d+)$/, (m) => `${m[1]} failed`],
  [/^失败 (\d+) 次$/, (m) => `${m[1]} failed calls`],
  [/^未计价 (\d+) 次$/, (m) => `${m[1]} unpriced`],
  [/^成本 (.+) · 未计价 (\d+) 次$/, (m) => `Cost ${m[1]} · ${m[2]} unpriced`],
  [/^成本 (\$.+)$/, (m) => `Cost ${m[1]}`],
  [/^(\d+) 天$/, (m) => `${m[1]} d`],
  [/^(\d+) 渠道$/, (m) => `${m[1]} channels`],
  [/^标签：(.+)$/, (m) => `Tag: ${m[1]}`],
  [/^全部套餐 \((\d+)\)$/, (m) => `All plans (${m[1]})`],
  [/^全部 (\d+)$/, (m) => `All ${m[1]}`],
  [/^(\d+) 个真实登录态 · (\d+) 个测试渠道$/, (m) => `${m[1]} live sessions · ${m[2]} test channels`],
  [/^失败 (\d+) · 成功率 (.+)$/, (m) => `${m[1]} failed · success ${m[2]}`],
  [/^缓存 (.+) · 命中 (.+)$/, (m) => `Cache ${m[1]} · hit ${m[2]}`],
  [/^缓存 (.+)%$/, (m) => `Cache ${m[1]}%`],
  [/^汇率 (.+)$/, (m) => `Exchange rate ${m[1]}`],
  [/^总计: (\d+) 次 · (.+)$/, (m) => `Total: ${m[1]} calls · ${m[2]}`],
  [/^缓存 (.+) · 成本 (.+)（未配价）$/, (m) => `Cache ${m[1]} · cost ${m[2]} (unpriced)`],
  [/^缓存 (.+) · 成本 (.+)$/, (m) => `Cache ${m[1]} · cost ${m[2]}`],
  [/^WSL (已关闭|开启) · 只读$/, (m) => `WSL ${m[1] === '开启' ? 'on' : 'off'} · read-only`],
  [/^最低剩余 (\d+)%$/, (m) => `Lowest left ${m[1]}%`],
  [/^剩余 (\d+)%$/, (m) => `${m[1]}% left`],
  [/^(.+) tokens · 活跃 (\d+) 天$/, (m) => `${m[1]} tokens · active ${m[2]} d`],
  [/^区间 (.+) Token · 悬停或聚焦后用 ← → 逐日查看$/, (m) => `${m[1]} tokens · hover or focus, then ← → for daily values`],
  [/^每 (\d+) 分钟$/, (m) => `every ${m[1]} min`],
  [/^(.+) Token$/, (m) => `${m[1]} tokens`],
];

let locale = 'zh-CN';
let dict = null;

export function getLocale() { return locale; }
export function locales() { return LOCALES.slice(); }

function lookup(key) {
  if (!dict || !key) return null;
  if (Object.prototype.hasOwnProperty.call(dict, key)) return dict[key];
  for (const [re, fn] of RULES) {
    const m = key.match(re);
    if (m) return (/** @type {(m: RegExpMatchArray) => string} */ (fn))(m);
  }
  return null;
}

/* 给 dom.js 用：构建文本节点时同步翻译 */
export function translateText(text) {
  if (!dict) return text;
  const s = String(text);
  if (s.length > 80) return s; // 长段落不查表，避免无谓开销
  const hit = lookup(s.trim());
  if (!hit) return s;
  return s.replace(s.trim(), hit);
}

/* 给 dom.js 用：只翻白名单属性，代码块内的 title 等不翻 */
export function translateAttr(name, value, tagName) {
  if (!dict || !ATTRS.has(name) || typeof value !== 'string') return value;
  if (SKIP_TAGS.has(String(tagName || '').toUpperCase())) return value;
  return lookup(value.trim()) || value;
}

/* 给视图里非 DOM 的场合（toast、confirm）用 */
export function t(source, vars) {
  const out = lookup(source) || source;
  if (!vars) return out;
  return out.replace(/\{(\w+)\}/g, (_, k) => (vars[k] == null ? '' : String(vars[k])));
}

export async function setLocale(id) {
  const next = LOCALES.some((l) => l.id === id) ? id : 'zh-CN';
  try { localStorage.setItem('aether.locale', next); } catch {}
  // 语言是低频操作：重载一次，保证启动时构建的导航、品牌等也一起切换
  location.reload();
}

export async function initLocale() {
  let saved = 'zh-CN';
  try { saved = localStorage.getItem('aether.locale') || 'zh-CN'; } catch {}
  locale = LOCALES.some((l) => l.id === saved) ? saved : 'zh-CN';
  if (locale !== 'zh-CN') {
    const mod = await import('./locales/en.js').catch(() => null);
    dict = mod ? mod.default : null;
  }
  document.documentElement.lang = locale;
}
