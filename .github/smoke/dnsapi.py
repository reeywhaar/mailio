#!/usr/bin/env python3
"""A stand-in for the REST DNS API mailio publishes records through.

It implements the same three calls internal/dns talks to — GET /dns to list,
POST /dns to create, DELETE /dns to remove — over in-memory state, so the smoke
test can assert what mailio published without touching a real zone.

It also keeps a log of every write at GET /_ops. That is what makes the second
boot meaningful: setup runs again on every start, and an upsert that recreated
an already-correct record would churn the zone (and, with a real provider, the
DKIM record mail is being signed against) on every restart.

Environment:
  DOMAINS  comma-separated zones to serve                 (default: smoke.test)
  SEED     JSON array of [domain, type, name, data] records to create up front.
           Used to hand mailio a stale record it has to clean up. JSON rather
           than a delimited string because record data contains ";" and "=".
  PORT     listen port                                    (default: 8433)
"""

import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

DOMAINS = [d for d in os.environ.get("DOMAINS", "smoke.test").split(",") if d]
records = {d: [] for d in DOMAINS}
ops = []
next_id = 1


def seed():
    for domain, rtype, name, data in json.loads(os.environ.get("SEED", "[]")):
        add(domain, rtype, name, data)
    ops.clear()  # seeding is not something mailio did


def add(domain, rtype, name, data):
    global next_id
    records.setdefault(domain, []).append(
        {"id": next_id, "type": rtype, "name": name, "data": data}
    )
    next_id += 1


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):  # one line per call, on stdout
        print("[dnsapi] " + fmt % args, flush=True)

    def body(self):
        length = int(self.headers.get("Content-Length") or 0)
        return json.loads(self.rfile.read(length) or b"{}")

    def reply(self, code, payload):
        data = json.dumps(payload).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path == "/_ops":
            return self.reply(200, ops)
        if self.path == "/dns":
            return self.reply(
                200, [{"name": d, "records": r} for d, r in records.items()]
            )
        self.reply(404, {"error": "not found"})

    def do_POST(self):
        if self.path != "/dns":
            return self.reply(404, {"error": "not found"})
        b = self.body()
        domain = b["domain"]
        if domain not in records:
            return self.reply(404, {"error": "no such domain %s" % domain})
        add(domain, b["type"], b["name"], b["data"])
        ops.append("create %s %s.%s" % (b["type"], b["name"], domain))
        self.reply(200, {"ok": True})

    def do_DELETE(self):
        if self.path != "/dns":
            return self.reply(404, {"error": "not found"})
        b = self.body()
        domain, rid = b["domain"], b["id"]
        before = len(records.get(domain, []))
        records[domain] = [r for r in records.get(domain, []) if r["id"] != rid]
        if len(records[domain]) == before:
            return self.reply(404, {"error": "no record %s in %s" % (rid, domain)})
        ops.append("delete %s %s" % (domain, rid))
        self.reply(200, {"ok": True})


if __name__ == "__main__":
    seed()
    port = int(os.environ.get("PORT", "8433"))
    print("[dnsapi] serving %s on :%d" % (", ".join(DOMAINS), port), flush=True)
    ThreadingHTTPServer(("0.0.0.0", port), Handler).serve_forever()
