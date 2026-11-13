# Screenshots

The pictures in the project README, captured from a real mailio holding invented domains,
accounts and mail.

```sh
npm --prefix web run screenshots
```

That builds the admin interface and a Linux binary, and starts mailio in Docker in staging, on an
empty data directory, with nothing outside resolving. It makes
[`seed.mjs`](../../web/scripts/screenshots/seed.mjs)'s relay, domains, accounts and aliases
through the admin API, as an admin would, and imports its mail as a Maildir, as mail from the
Postfix-based mailio is. Then it photographs the admin interface in headless Chromium over the
DevTools protocol and overwrites the PNGs here. Everything it starts is stopped on the way out.
Each PNG says its density, 72 dpi times `SCALE`, as a macOS screenshot does.

mailio runs in a container even on a Mac, because it listens on 25, 80, 465, 587 and 993, which
a container keeps to itself.

Needs `go`, `patch`, `node`, `docker` and Chromium or Chrome.

**A run with nothing changed changes nothing.** The mail is dated from a fixed moment, the page is
in UTC and `en-US`, the caret is hidden and animations finished. Two things each install makes up
for itself, the login tags of credentials past the first and every domain's DKIM key, are drawn
as the same made-up values on every run. A new shot that differs from the file already there in
under a thousandth of its pixels, by at most 24 levels, leaves the file alone.

| knob | |
| --- | --- |
| `SCALE=2` | device pixels per CSS pixel |
| `THEME=dark` | the other theme; `OUT=/tmp/shots` keeps it off the committed set |
| `ONLY=account` | just those shots |
| `TIMEOUT=30` | seconds a shot may take; one that runs over says what it was waiting for |

**The data** is `seed.mjs`: the admin, the relay, the domains and the accounts with what each
sets apart from the defaults, the aliases, and the messages, each with the account it is in, how
long ago it came, and its attachment if it has one.

**The shots** are the list at the bottom of
[`capture.mjs`](../../web/scripts/screenshots/capture.mjs): what to open, and the element to crop
to.
