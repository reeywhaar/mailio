"""Drives a running mailio container through its admin API and its mail ports.

Standard library only, so CI needs nothing but python3. Ports and the admin token come from
smoke.sh through the environment.
"""

import http.cookiejar
import imaplib
import json
import os
import smtplib
import ssl
import sys
import time
import urllib.request

HOST = "127.0.0.1"
PORT = {k: int(os.environ[f"SMOKE_{k.upper()}"]) for k in ("http", "smtp", "submission", "submissions", "imap")}
# The certificate is self-signed: there is no DNS API here to get a real one through.
TLS = ssl.create_default_context()
TLS.check_hostname = False
TLS.verify_mode = ssl.CERT_NONE
PASSWORD = "correct horse battery"
STATE = "/tmp/mailio-smoke-login"


def fail(msg):
    print(f"FAIL: {msg}", file=sys.stderr)
    sys.exit(1)


jar = http.cookiejar.CookieJar()
web = urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))


def api(method, path, body=None):
    req = urllib.request.Request(
        f"http://{HOST}:{PORT['http']}{path}",
        method=method,
        data=None if body is None else json.dumps(body).encode(),
        headers={"Content-Type": "application/json"},
    )
    try:
        with web.open(req, timeout=30) as r:
            raw = r.read()
            return r.status, json.loads(raw) if raw else None
    except urllib.error.HTTPError as e:
        fail(f"{method} {path}: {e.code} {e.read().decode()[:200]}")


def inbox_count(login, password):
    m = imaplib.IMAP4_SSL(HOST, PORT["imap"], ssl_context=TLS)
    m.login(login, password)
    m.select("INBOX")
    n = len(m.search(None, "ALL")[1][0].split())
    m.logout()
    return n


def setup():
    api("POST", "/api/admin-link", {"token": os.environ["SMOKE_TOKEN"], "name": "smoke", "password": PASSWORD})
    api("POST", "/api/domains", {"name": "smoke.test"})
    _, user = api("POST", "/api/users", {"address": "inbox@smoke.test", "password": PASSWORD})
    _, phone = api("POST", f"/api/users/{user['id']}/passwords", {"label": "phone", "password": "phone password"})
    login = phone["login"]
    if not login.startswith("inbox+"):
        fail(f"the second password's login is {login}")
    print("admin, domain, user and a second login:", login)

    # Another server delivers on 25, upgrading with STARTTLS. The sender's domain publishes no
    # DMARC, so nothing here depends on it.
    s = smtplib.SMTP(HOST, PORT["smtp"], timeout=30)
    s.ehlo("sender.invalid")
    s.starttls(context=TLS)
    s.ehlo("sender.invalid")
    s.sendmail("a@sender.invalid", ["inbox+tag@smoke.test"],
               b"From: a@sender.invalid\r\nSubject: Smoke subject\r\n\r\nIn on 25.\r\n")
    try:
        s.sendmail("a@sender.invalid", ["nobody@smoke.test"], b"Subject: x\r\n\r\nx\r\n")
        fail("mail to an unknown address was accepted")
    except smtplib.SMTPRecipientsRefused:
        pass
    s.quit()
    print("delivered on 25 over STARTTLS; an unknown address refused")

    # The user sends to themself on 465 with the second login: it stays on the server.
    s = smtplib.SMTP_SSL(HOST, PORT["submissions"], context=TLS, timeout=30)
    s.login(login, "phone password")
    s.sendmail("inbox@smoke.test", ["inbox@smoke.test"],
               b"From: inbox@smoke.test\r\nSubject: Smoke subject\r\n\r\nOut on 465.\r\n")
    s.quit()
    s = smtplib.SMTP(HOST, PORT["submission"], timeout=30)
    s.ehlo("x")
    if s.has_extn("auth"):
        fail("587 offers AUTH before STARTTLS")
    s.starttls(context=TLS)
    s.ehlo("x")
    s.login("inbox@smoke.test", PASSWORD)
    s.quit()
    print("sent on 465 with the second login; 587 signs in only after STARTTLS")

    for _ in range(20):
        if inbox_count("inbox@smoke.test", PASSWORD) == 2:
            break
        time.sleep(0.5)
    else:
        fail("the INBOX does not hold both messages")
    print("both read over IMAP on 993")
    with open(STATE, "w") as f:
        f.write(login)


def after_restart():
    with open(STATE) as f:
        login = f.read()
    if inbox_count(login, "phone password") != 2:
        fail("the mail did not survive a restart")
    print("both messages still there, and the second login still signs in")


{"setup": setup, "after-restart": after_restart}[sys.argv[1]]()
