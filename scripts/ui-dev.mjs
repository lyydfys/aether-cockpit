#!/usr/bin/env node
/* 前端开发服务：ui2 是 //go:embed 进二进制的，改 CSS/JS 后必须重编译才生效。
   这个脚本从磁盘直接服务 ui2，并把 /api、/healthz、/readyz、/version 代理到
   一个已在运行的网关，省掉「改一行样式 → go build 40 秒」的循环。

   用法：
     1) 先起网关（另一个终端）：bin/cockpit.exe -addr 127.0.0.1:7999
     2) node scripts/ui-dev.mjs
     3) 打开 http://127.0.0.1:7998

   环境变量：UI_PORT（默认 7998）、API_ORIGIN（默认 http://127.0.0.1:7999）。
*/
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const HERE = path.dirname(fileURLToPath(import.meta.url));
const ROOT = path.resolve(HERE, '..', 'internal', 'web', 'ui2');
const API = process.env.API_ORIGIN || 'http://127.0.0.1:7999';
const PORT = Number(process.env.UI_PORT || 7998);

const MIME = {
  '.html': 'text/html; charset=utf-8',
  '.js': 'text/javascript; charset=utf-8',
  '.css': 'text/css; charset=utf-8',
  '.svg': 'image/svg+xml',
  '.png': 'image/png',
  '.woff2': 'font/woff2',
  '.json': 'application/json',
  '.ico': 'image/x-icon',
  '.txt': 'text/plain; charset=utf-8',
};

const isApi = (p) => p.startsWith('/api') || p === '/healthz' || p === '/readyz' || p === '/version';

http.createServer((req, res) => {
  const url = new URL(req.url, 'http://local');
  if (isApi(url.pathname)) {
    const up = http.request(API + req.url, {
      method: req.method,
      headers: { ...req.headers, host: new URL(API).host },
    }, (r) => {
      res.writeHead(r.statusCode || 502, r.headers);
      r.pipe(res);
    });
    up.on('error', (e) => { res.writeHead(502); res.end('proxy error: ' + e.message); });
    req.pipe(up);
    return;
  }
  if (url.pathname === '/css/app.css') {
    const names = ['tokens.css', 'base.css', 'components.css', 'views.css'];
    const buf = names.map((n) => fs.readFileSync(path.join(ROOT, 'css', n))).join('\n');
    res.writeHead(200, { 'content-type': 'text/css; charset=utf-8', 'cache-control': 'no-store' });
    res.end(buf);
    return;
  }
  const rel = decodeURIComponent(url.pathname === '/' ? '/index.html' : url.pathname);
  const file = path.resolve(path.join(ROOT, rel));
  if (!file.startsWith(ROOT)) { res.writeHead(403); res.end('forbidden'); return; }
  fs.readFile(file, (err, buf) => {
    if (err) { res.writeHead(404, { 'content-type': 'text/plain; charset=utf-8' }); res.end('not found: ' + rel); return; }
    res.writeHead(200, { 'content-type': MIME[path.extname(file)] || 'application/octet-stream', 'cache-control': 'no-store' });
    res.end(buf);
  });
}).listen(PORT, '127.0.0.1', () => {
  console.log('ui2 dev  →  http://127.0.0.1:' + PORT + '   (api → ' + API + ')');
});
