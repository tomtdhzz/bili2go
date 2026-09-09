"""bili2go summarizer 服务：digest.json -> summary.md 的 HTTP 边界。

契约（冻结）：
- GET  /health            -> 200 {"status":"ok","llm":<bool>}
- POST /summarize[?top=N] -> body 为 digest.json；
                             200 {"markdown","model","fallback"[,"reason"]}
                             400 {"error"} 当 digest 非法/字段缺失
纯标准库；docker 内监听 0.0.0.0:8091。
"""
from __future__ import annotations

import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from urllib.parse import parse_qs, urlparse

import summarize as S

DEFAULT_TOP = int(os.environ.get("SUMMARY_TOP", "25"))
MAX_BODY = 64 * 1024 * 1024  # 64 MiB 上限，长视频 digest.json 可达数 MB


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _send(self, code: int, obj: dict) -> None:
        body = json.dumps(obj, ensure_ascii=False).encode("utf-8")
        self.send_response(code)
        self.send_header("Content-Type", "application/json; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):  # noqa: D401 - 静默默认 stderr 噪声
        pass

    def do_GET(self):
        if urlparse(self.path).path == "/health":
            llm = bool(os.environ.get("OPENAI_BASE_URL") and os.environ.get("OPENAI_API_KEY"))
            self._send(200, {"status": "ok", "llm": llm})
        else:
            self._send(404, {"error": "not found"})

    def do_POST(self):
        parsed = urlparse(self.path)
        if parsed.path != "/summarize":
            self._send(404, {"error": "not found"})
            return
        top = DEFAULT_TOP
        qs = parse_qs(parsed.query)
        if "top" in qs:
            try:
                top = int(qs["top"][0])
            except ValueError:
                self._send(400, {"error": "top 必须为整数"})
                return
        length = int(self.headers.get("Content-Length", "0") or "0")
        if length <= 0 or length > MAX_BODY:
            self._send(400, {"error": f"Content-Length 非法或超限: {length}"})
            return
        raw = self.rfile.read(length)
        try:
            digest = json.loads(raw.decode("utf-8"))
        except (json.JSONDecodeError, UnicodeDecodeError) as e:
            self._send(400, {"error": f"digest 非合法 JSON: {e}"})
            return
        try:
            result = S.summarize(digest, top)
        except ValueError as e:
            self._send(400, {"error": str(e)})
            return
        self._send(200, result)


def main():
    port = int(os.environ.get("PORT", "8091"))
    srv = ThreadingHTTPServer(("0.0.0.0", port), Handler)
    print(f"summarizer listening on :{port} (llm={bool(os.environ.get('OPENAI_API_KEY'))})", flush=True)
    srv.serve_forever()


if __name__ == "__main__":
    main()
