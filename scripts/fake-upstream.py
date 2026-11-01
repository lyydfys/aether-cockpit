#!/usr/bin/env python3
"""临时假上游：模拟一个标准 OpenAI 兼容的 SSE 端点，用于端到端验证。

不依赖任何第三方库，仅用于本地联调。
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, HTTPServer


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        sys.stderr.write("[upstream] " + fmt % args + "\n")

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length)
        try:
            body = json.loads(raw)
        except Exception:
            body = {}

        # 校验鉴权头，确保网关真的把凭证带过来了
        auth = self.headers.get("Authorization", "")
        if auth != "Bearer fake-key-123":
            self.send_response(401)
            self.send_header("Content-Type", "application/json")
            self.end_headers()
            self.wfile.write(b'{"error":{"message":"bad key"}}')
            return

        model = body.get("model", "?")
        tools = body.get("tools") or []
        msgs = body.get("messages") or []
        last = ""
        for m in reversed(msgs):
            if m.get("role") == "user":
                last = m.get("content") or ""
                break
        if isinstance(last, list):
            last = "".join(p.get("text", "") for p in last if isinstance(p, dict))

        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Cache-Control", "no-cache")
        self.end_headers()

        def emit(obj):
            self.wfile.write(b"data: " + json.dumps(obj, ensure_ascii=False).encode() + b"\n\n")
            self.wfile.flush()

        # 如果客户端带了 tools，就模拟一次工具调用，验证 index 配对
        if tools:
            emit({"choices": [{"delta": {"reasoning_content": "需要调用工具"}}]})
            emit({"choices": [{"delta": {"tool_calls": [{
                "index": 0, "id": "call_real_1", "type": "function",
                "function": {"name": tools[0]["function"]["name"], "arguments": ""},
            }]}}]})
            emit({"choices": [{"delta": {"tool_calls": [{
                "index": 0, "function": {"arguments": '{"path":"cfg.json"}'},
            }]}}]})
            emit({"choices": [{"delta": {}, "finish_reason": "tool_calls"}]})
        else:
            emit({"choices": [{"delta": {"content": f"[{model}] 真实HTTP上游收到：{last}"}}]})
            emit({"choices": [{"delta": {}, "finish_reason": "stop"}]})

        emit({"choices": [], "usage": {"prompt_tokens": 12, "completion_tokens": 8, "total_tokens": 20}})
        self.wfile.write(b"data: [DONE]\n\n")
        self.wfile.flush()


if __name__ == "__main__":
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 7900
    print(f"[upstream] listening on 127.0.0.1:{port}", flush=True)
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()
