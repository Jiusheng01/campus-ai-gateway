#!/usr/bin/env python3
"""A minimal OpenAI-compatible upstream for end-to-end checks.

The gateway's data plane can only be verified against something that answers
/v1/chat/completions. Pointing a test at a real provider spends money, needs a
key, and makes the result depend on a network the check is not about.

It refuses a request without an Authorization header so that "the credential
was forwarded" is an assertion the caller can make, and it reports fixed usage
numbers so a quota check has a predictable arithmetic.

Usage:
    python fake-upstream.py [port]
"""

import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# Fixed usage: one request costs 15 tokens, which makes a 20-token cap fail on
# the second call without depending on how the gateway counts.
PROMPT_TOKENS = 10
COMPLETION_TOKENS = 5

MODELS = ["fake-model-a", "fake-model-b"]


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, fmt, *args):
        sys.stderr.write("upstream %s\n" % (fmt % args))
        sys.stderr.flush()

    def _json(self, status, payload):
        body = json.dumps(payload).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _authorized(self):
        auth = self.headers.get("Authorization", "")
        if not auth.startswith("Bearer ") or not auth[7:].strip():
            self._json(401, {"error": {"message": "missing credential"}})
            return False
        return True

    def do_GET(self):
        if self.path.rstrip("/") == "/v1/models":
            if not self._authorized():
                return
            self._json(200, {"object": "list", "data": [{"id": m, "object": "model"} for m in MODELS]})
            return
        self._json(404, {"error": {"message": "not found"}})

    def do_POST(self):
        if self.path.rstrip("/") != "/v1/chat/completions":
            self._json(404, {"error": {"message": "not found"}})
            return
        if not self._authorized():
            return
        length = int(self.headers.get("Content-Length") or 0)
        raw = self.rfile.read(length) if length else b"{}"
        try:
            req = json.loads(raw or b"{}")
        except ValueError:
            self._json(400, {"error": {"message": "invalid json"}})
            return
        model = req.get("model", "")
        self._json(200, {
            "id": "chatcmpl-fake",
            "object": "chat.completion",
            "model": model,
            "choices": [{
                "index": 0,
                "message": {"role": "assistant", "content": "pong"},
                "finish_reason": "stop",
            }],
            "usage": {
                "prompt_tokens": PROMPT_TOKENS,
                "completion_tokens": COMPLETION_TOKENS,
                "total_tokens": PROMPT_TOKENS + COMPLETION_TOKENS,
            },
        })


def main():
    port = int(sys.argv[1]) if len(sys.argv) > 1 else 18080
    server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    sys.stderr.write("fake upstream on http://127.0.0.1:%d\n" % port)
    sys.stderr.flush()
    server.serve_forever()


if __name__ == "__main__":
    main()
