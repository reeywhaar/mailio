#!/usr/bin/env python3
"""SMTP-level assertions against a running mailio container.

Every check runs from outside mynetworks (the host, through published ports), so
what it proves is what an untrusted client on the internet can and cannot do.

  auth            <addr> <pw>                    AUTH on submission succeeds
  auth-refused    <addr> <pw>                    AUTH on submission is refused (535)
  submit          <addr> <pw> <from> <to> <mark> authenticated send is accepted
  submit-refused  <addr> <pw> <from> <to>        authenticated send is refused
  inbound         <from> <to> <mark>             unauthenticated port-25 mail is accepted
  inbound-refused <from> <to>                    unauthenticated port-25 mail is refused
  inbound-no-date <from> <to>                    a message missing a Date: header is refused

--expect <text> pins the refusal reason, so a check cannot pass by being refused
for some unrelated reason.
"""

import email.utils
import os
import smtplib
import ssl
import sys

HOST = os.environ.get("SMOKE_HOST", "127.0.0.1")
SMTP_PORT = int(os.environ.get("SMOKE_SMTP_PORT", "2525"))
SUBMISSION_PORT = int(os.environ.get("SMOKE_SUBMISSION_PORT", "2587"))
TIMEOUT = 30

# The container's cert is self-signed by design; this test is about SMTP
# behaviour, not about the cert chain.
CTX = ssl.create_default_context()
CTX.check_hostname = False
CTX.verify_mode = ssl.CERT_NONE


def fail(msg):
    print("FAIL: %s" % msg, file=sys.stderr)
    sys.exit(1)


def ok(msg):
    print("ok: %s" % msg)


def message(sender, rcpt, marker, date=True):
    # OpenDMARC runs with RequiredHeaders, so anything missing the RFC5322
    # mandatory headers is rejected at DATA — Date is not optional here.
    head = "From: %s\r\nTo: %s\r\nSubject: mailio smoke\r\n" % (sender, rcpt)
    if date:
        head += "Date: %s\r\nMessage-ID: %s\r\n" % (
            email.utils.formatdate(localtime=True),
            email.utils.make_msgid(domain="smoke-client.test"),
        )
    return head + "X-Smoke: %s\r\n\r\nsmoke %s\r\n" % (marker, marker)


def submission():
    s = smtplib.SMTP(HOST, SUBMISSION_PORT, timeout=TIMEOUT)
    s.ehlo()
    if "starttls" not in s.esmtp_features:
        fail("submission does not offer STARTTLS")
    s.starttls(context=CTX)
    s.ehlo()
    return s


def refusal(fn, expect, what):
    """Run fn, require it to be refused with a 5xx, and check the reason."""
    try:
        fn()
    except smtplib.SMTPResponseException as e:
        reason = e.smtp_error
        if isinstance(reason, bytes):
            reason = reason.decode("utf-8", "replace")
        if not 500 <= e.smtp_code < 600:
            fail("%s: refused with %d, wanted a 5xx: %s" % (what, e.smtp_code, reason))
        if expect and expect.lower() not in reason.lower():
            fail("%s: refused %d %s, wanted the reason to mention %r"
                 % (what, e.smtp_code, reason, expect))
        return ok("%s: refused %d %s" % (what, e.smtp_code, reason))
    except smtplib.SMTPRecipientsRefused as e:
        code, reason = list(e.recipients.values())[0]
        if isinstance(reason, bytes):
            reason = reason.decode("utf-8", "replace")
        if not 500 <= code < 600:
            fail("%s: refused with %d, wanted a 5xx: %s" % (what, code, reason))
        if expect and expect.lower() not in reason.lower():
            fail("%s: refused %d %s, wanted the reason to mention %r"
                 % (what, code, reason, expect))
        return ok("%s: refused %d %s" % (what, code, reason))
    fail("%s: accepted, and must not have been" % what)


def main(argv):
    expect = None
    if "--expect" in argv:
        i = argv.index("--expect")
        expect = argv[i + 1]
        argv = argv[:i] + argv[i + 2:]

    cmd, args = argv[0], argv[1:]

    if cmd == "auth":
        addr, pw = args
        s = submission()
        s.login(addr, pw)
        s.quit()
        ok("%s authenticated on submission" % addr)

    elif cmd == "auth-refused":
        addr, pw = args
        s = submission()
        refusal(lambda: s.login(addr, pw), expect, "AUTH as %s" % addr)
        s.close()

    elif cmd == "submit":
        addr, pw, sender, rcpt, marker = args
        s = submission()
        s.login(addr, pw)
        s.sendmail(sender, [rcpt], message(sender, rcpt, marker))
        s.quit()
        ok("%s submitted %s -> %s" % (addr, sender, rcpt))

    elif cmd == "submit-refused":
        addr, pw, sender, rcpt = args
        s = submission()
        s.login(addr, pw)
        refusal(
            lambda: s.sendmail(sender, [rcpt], message(sender, rcpt, "refused")),
            expect,
            "%s sending as %s" % (addr, sender),
        )
        s.close()

    elif cmd == "inbound":
        sender, rcpt, marker = args
        s = smtplib.SMTP(HOST, SMTP_PORT, timeout=TIMEOUT)
        s.sendmail(sender, [rcpt], message(sender, rcpt, marker))
        s.quit()
        ok("unauthenticated mail accepted for %s" % rcpt)

    elif cmd == "inbound-refused":
        sender, rcpt = args
        s = smtplib.SMTP(HOST, SMTP_PORT, timeout=TIMEOUT)
        refusal(
            lambda: s.sendmail(sender, [rcpt], message(sender, rcpt, "refused")),
            expect,
            "unauthenticated mail to %s" % rcpt,
        )
        s.close()

    elif cmd == "inbound-no-date":
        sender, rcpt = args
        s = smtplib.SMTP(HOST, SMTP_PORT, timeout=TIMEOUT)
        refusal(
            lambda: s.sendmail(
                sender, [rcpt], message(sender, rcpt, "nodate", date=False)
            ),
            expect,
            "mail with no Date: header",
        )
        s.close()

    else:
        fail("unknown check %r" % cmd)


if __name__ == "__main__":
    main(sys.argv[1:])
