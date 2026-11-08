"""本地预览服务器：ThreadingHTTPServer，避免浏览器并发请求被单线程串行阻塞。"""
import functools
import http.server
import socketserver
import sys
from pathlib import Path


class Handler(http.server.SimpleHTTPRequestHandler):
    extensions_map = {
        **http.server.SimpleHTTPRequestHandler.extensions_map,
        ".woff2": "font/woff2",
        ".mjs": "text/javascript",
        ".js": "text/javascript",
    }

    def log_message(self, fmt, *args):
        pass  # 静音，避免刷屏


def main() -> int:
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 8123
    root = Path(__file__).resolve().parent.parent
    handler = functools.partial(Handler, directory=str(root))
    socketserver.ThreadingTCPServer.allow_reuse_address = True
    with socketserver.ThreadingTCPServer(("127.0.0.1", port), handler) as httpd:
        print(f"serving {root} at http://127.0.0.1:{port}/", flush=True)
        httpd.serve_forever()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
