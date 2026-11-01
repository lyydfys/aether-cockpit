/* 自有路由：hash 路由（迁移期可挂在任意前缀下），视图表由 app.js 注入。
   路由可注册「视图对象」或「加载函数」：后者用于二级页面按需加载，
   首屏不再为 10 个低频页付出解析成本。 */
let routes = new Map();
let current = null;
/** @type {(r: any, view: any) => void} */
let onRender = () => {};
let seq = 0;

export function register(id, view) { routes.set(id, view); }
export function currentRoute() { return current; }

/* 纯函数：输入 hash 字符串，输出路由与参数，便于单测。 */
export function parseHash(hash) {
  const raw = String(hash || '').replace(/^#\/?/, '');
  const [id, qs] = raw.split('?');
  const params = new URLSearchParams(qs || '');
  return { id: id || 'overview', params };
}

export function parse() {
  return parseHash(location.hash);
}

export function navigate(id, params) {
  const q = params ? '?' + new URLSearchParams(params).toString() : '';
  const next = '#/' + id + q;
  if (location.hash === next) render();
  else location.hash = next;
}

export function rerender() { render(); }

function render() {
  const r = parse();
  current = r;
  const entry = routes.get(r.id) || routes.get('overview');
  const mine = ++seq;
  const done = (v) => {
    if (mine !== seq) return; // 快速连点时只渲染最后一次
    onRender(r, v && v.default ? v.default : v);
  };
  if (typeof entry === 'function') {
    entry().then(done).catch((e) => {
      console.error('[router] 页面加载失败', e);
      const fallback = routes.get('overview');
      if (typeof fallback !== 'function') done(fallback);
    });
  } else {
    done(entry);
  }
}

export function start(renderer) {
  onRender = renderer;
  addEventListener('hashchange', render);
  render();
}
