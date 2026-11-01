/* API 客户端：同源、不缓存、错误带状态码与后端说明。 */

export class ApiError extends Error {
  constructor(status, message, path) {
    super(message || ('HTTP ' + status));
    this.name = 'ApiError';
    this.status = status;
    this.path = path;
  }
}

async function request(path, init) {
  const res = await fetch('/api/' + path, Object.assign({ cache: 'no-store' }, init || {}));
  const text = await res.text();
  let data = null;
  if (text) {
    try { data = JSON.parse(text); } catch { data = text; }
  }
  if (!res.ok) {
    const msg = (data && (data.error || data.message)) || (typeof data === 'string' ? data : '') || res.statusText;
    throw new ApiError(res.status, msg, path);
  }
  return data;
}

/* 同一路径的并发 GET 合并成一个请求；1 秒内复用结果。
   任何写操作前先把缓存清空，避免 POST 后立刻 load() 读到旧快照。 */
const inflight = new Map();
const cache = new Map();
const TTL = 1000;

export function get(path) {
  const hit = cache.get(path);
  if (hit && performance.now() - hit.at < TTL) return Promise.resolve(hit.data);
  if (inflight.has(path)) return inflight.get(path);
  const p = request(path).then((data) => {
    cache.set(path, { at: performance.now(), data });
    inflight.delete(path);
    return data;
  }, (err) => { inflight.delete(path); throw err; });
  inflight.set(path, p);
  return p;
}

function invalidate() { cache.clear(); }

export function post(path, body) {
  invalidate();
  return request(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body === undefined ? {} : body),
  }).finally(invalidate);
}

export function del(path) {
  invalidate();
  return request(path, { method: 'DELETE' }).finally(invalidate);
}

export async function version() {
  const res = await fetch('/version', { cache: 'no-store' });
  if (!res.ok) return {};
  return res.json();
}
