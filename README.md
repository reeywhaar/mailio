# mailio

Dockerized Postfix + OpenDKIM + OpenDMARC mail service. Configured via a single `config.yml`.

On startup the service:
1. Generates DKIM keypairs (persisted in a volume) for each configured domain
2. Creates / updates DKIM, SPF, and DMARC TXT records at your DNS provider via the DNS API sidecar (`doapi` by default)
3. Obtains or renews a TLS certificate from Let's Encrypt via DNS-01 challenge (if `letsencrypt:` is configured), otherwise generates a self-signed certificate
4. Writes all Postfix, OpenDKIM, and OpenDMARC config files
5. Creates Maildir directories (`cur/`, `new/`, `tmp/`) under `/var/mail/<local_user>/` for each account
6. Sets up SASL accounts so apps can authenticate and submit mail
7. Starts OpenDKIM (milter on port 8891), OpenDMARC (milter on port 8893), and Postfix (SMTP 25, submission 587)

### Inbound DMARC enforcement

Incoming mail is run through OpenDKIM (DKIM verification) and then OpenDMARC, which evaluates each message against **the sending domain's published DMARC policy** and rejects it at SMTP time when that domain publishes `p=reject` and the message fails (neither DKIM nor SPF aligns). OpenDMARC performs its own SPF check (via libspf2), so DMARC passes on either aligned SPF or aligned DKIM, matching the standard.

This means unsigned/unverified mail is **not** blanket-rejected — only mail whose own domain asked for rejection. Mail from domains publishing `p=none`/`p=quarantine` (or no DMARC record at all) is annotated with an `Authentication-Results` header and delivered. Our own authenticated outbound (submission, port 587) is exempt — it is signed by OpenDKIM but never DMARC-evaluated.

---

## Prerequisites

### DNS records that must exist before the first deploy

These are **not** auto-created — configure them once at your DNS provider:

| Type | Name | Value | Domain | Purpose |
|------|------|-------|--------|---------|
| A | `mail` | `<server IP>` | example-a.com | Resolves the mail hostname |
| MX | `@` | `mail.example-a.com` | example-a.com | Receives mail for example-a.com |
| MX | `@` | `mail.example-a.com` | example-b.com | Receives mail for example-b.com |
**Auto-managed by setup on every start:**
- `mail._domainkey.<domain>` TXT — DKIM public key
- `@.<domain>` TXT — SPF record (when `spf:` is set in config)
- `_dmarc.<domain>` TXT — DMARC policy (when `dmarc:` is set in config)

---

## Configuration

Edit `config.yml`:

```yaml
hostname: mail.example-a.com

# IP ranges allowed to relay without SASL auth
mynetworks:
  - 127.0.0.0/8
  - 172.20.0.0/16

# Optional: obtain a trusted TLS certificate from Let's Encrypt.
# The hostname's parent domain must be listed under `domains` below.
# Omit to use a self-signed certificate instead.
letsencrypt:
  email: admin@example-a.com   # ACME account contact address
  staging: false                # set true to use the LE staging server for testing

domains:
  - name: example-a.com
    selector: mail          # → mail._domainkey.example-a.com
    spf: "~all"             # auto-creates TXT @ → v=spf1 a:mail.example-a.com ~all
    dmarc:
      policy: none          # none / quarantine / reject
      rua: inbox@example-a.com   # optional aggregate report address
    catchall: user          # unknown addresses → user@example-a.com (or "discard")
    accounts:
      - local_part: user
        password: "secret"
        local_user: root    # mail delivered to /var/mail/root/ (Maildir)

  - name: example-b.com
    selector: mail
    accounts:
      - local_part: user
        password: "secret"
        local_user: root
```

- SASL username is the **full address** `local_part@domain` (e.g. `user@example-a.com`), authenticated against the domain as the realm. This lets the same `local_part` exist in several domains with independent passwords.
- An authenticated account may only send mail **as its own address** — Postfix rejects a `From` belonging to another configured account (`reject_sender_login_mismatch`). See [the note below](#sender-identity-binding).
- `local_user` maps inbound mail to `/var/mail/<local_user>/` in Maildir format. Omit to disable inbound delivery for that account.
- `spf`, `dmarc`, `catchall`, `letsencrypt`, and `relay` are all optional.

### Outbound relay (smarthost)

By default Postfix delivers mail directly to recipient mail servers over port 25. Many cloud and residential networks block outbound port 25, so mail never leaves the host. Configure a `relay:` to forward all outgoing mail through an authenticated SMTP server instead:

```yaml
relay:
  host: smtp.gmail.com
  port: 587                 # 587 = STARTTLS (default), 465 = implicit TLS
  username: you@gmail.com
  password: "your-app-password"
```

When set, the service writes `relayhost = [<host>]:<port>` plus SMTP-client SASL settings to `main.cf`, and stores the credentials in `/etc/postfix/sasl_passwd` (owned `root:postfix`, mode `0640`). The relay connection always uses TLS (`smtp_tls_security_level = encrypt`) so the credentials are never sent over a plaintext or downgraded link.

**Per-domain and per-account relays.** A `relay:` can also be set under a `domain` or under an individual `account`, overriding the top-level relay for mail from that domain or account. Resolution is by **envelope sender**, most specific first: account (`user@domain`) → domain (`@domain`) → global. This uses Postfix sender-dependent routing:

```yaml
relay:                          # global default
  host: smtp.gmail.com
  username: shared@gmail.com
  password: "..."

domains:
  - name: example-a.com
    relay:                      # all of example-a.com, unless an account overrides
      host: smtp.gmail.com
      username: team@gmail.com
      password: "..."
    accounts:
      - local_part: alice
        relay:                  # only alice@example-a.com
          host: smtp.gmail.com
          username: alice@gmail.com
          password: "..."
      - local_part: bob         # -> uses the example-a.com domain relay
```

When any per-domain/per-account relay is present, the service writes `/etc/postfix/sender_relay` (sender → smarthost) alongside `sasl_passwd` (keyed by sender, with the global relay kept as the nexthop-keyed fallback), and enables `smtp_sender_dependent_authentication`. Because implicit TLS (port 465) is a single global toggle that can't vary per sender, **all relays must use STARTTLS (587)** in this mode; 465 is only allowed when the global relay is the only relay.

**Using Gmail:**

1. Enable 2-Step Verification on the Google account.
2. Create an [App Password](https://myaccount.google.com/apppasswords) and use it as `password` — your normal account password will not work.
3. Gmail sends mail as the authenticated address. To preserve your own From addresses (e.g. `user@example-a.com`), add each as a verified alias under Gmail Settings → Accounts → "Send mail as"; otherwise Gmail rewrites the From header to the account address.

### Let's Encrypt certificate

When `letsencrypt:` is configured, the service uses the ACME v2 DNS-01 challenge to obtain a certificate from Let's Encrypt. The flow on startup:

1. Loads (or generates) a persistent ECDSA account key at `/etc/letsencrypt/account.key`
2. Skips renewal if the current certificate has more than 30 days remaining
3. Creates a `_acme-challenge` TXT record at your DNS provider via the DNS API
4. Waits for Let's Encrypt to validate the challenge
5. Downloads the signed certificate chain and writes it to `/etc/postfix/tls/`
6. Deletes the challenge TXT record

The `hostname`'s parent domain must be present in `domains` so the DNS challenge record can be created. For example, if `hostname: mail.example-a.com`, then `example-a.com` must be listed as a domain.

**Automatic renewal:** the container runs a background loop that checks the certificate once a day and renews it when it's within 30 days of expiry, reloading Postfix automatically when a new certificate is issued. No cron job or external scheduler is needed — as long as the container is running, the certificate stays valid. Use `mailio cert renew --force` only if you need to force an out-of-band renewal.

---

## Usage

```bash
# 1. Create your config from the example
cp config.example.yml config.yml
# edit config.yml with real values

# 2. First run
docker compose up -d --build

# View logs
docker logs -f mailio

# Restart to re-apply config changes (setup re-runs on every start)
docker compose restart mailio
```

---

## CLI commands

The `mailio` binary supports subcommands in addition to the default setup run:

### `mailio config`

Prints the SMTP credentials for every account, one block per account:

```bash
docker exec mailio mailio config
```

```
email: user@example-a.com
smtp_url: smtp://user%40example-a.com@mail.example-a.com:587/
password: changeme
```

The username embedded in `smtp_url` is the full address (`@` shown as `%40`).

### `mailio add-account` / `mailio delete-account`

Add or remove an account in `config.yml`. The file is edited in place through a YAML round-trip, so **comments are preserved** (only whitespace/indentation may be normalized).

```bash
# Add an account; a strong password is generated unless --password is given.
docker exec mailio mailio add-account example-b.com user2 [--password <pw>] [--local_user <user>]

# Remove an account (revokes the login; the mailbox is kept).
docker exec mailio mailio delete-account example-b.com user2
```

`add-account` prints the new credentials (same format as `mailio config`) on success. The target domain must already exist in `config.yml`.

Both commands edit `config.yml` in place (it's bind-mounted read-write) and, **when run inside the running container, apply live** — `add-account` provisions the SASL login, routing maps, and Maildir then reloads Postfix; `delete-account` revokes the login and refreshes the maps. No restart needed. Run outside the container (where Postfix/SASL aren't reachable), they only edit the config and tell you to `docker compose restart mailio` to apply — which works too, because:

**Config is the source of truth.** On each start, setup **rebuilds** the SASL database and regenerates the routing maps (`vmailbox`, `sender_login`) entirely from `config.yml`. So an account removed from config — whether by `delete-account` or by hand — has its **login and routing dropped on the next restart**; there's nothing to prune manually.

**Mail data is kept.** Pruning never touches mailboxes — an account's stored mail stays under `/var/mail/<local_user>` (the `mail_data` volume) even after the account is removed. Delete the directory by hand if you also want the stored mail gone.

### `mailio config mutt`

Prints a mutt/neomutt-compatible SMTP config to stdout based on the accounts in `config.yml`.

```bash
docker exec mailio mailio config mutt
# or locally (requires /etc/mailio/config.yml):
./mailio config mutt
```

Example output:

```muttrc
# Generated by mailio config mutt
# Server: mail.example-a.com

set from      = "user@example-a.com"
set smtp_url  = "smtp://user%40example-a.com@mail.example-a.com:587/"
set smtp_pass = "secret"

# Additional accounts:

# --- user@example-b.com ---
# set from      = "user@example-b.com"
# set smtp_url  = "smtp://user%40example-b.com@mail.example-a.com:587/"
# set smtp_pass = "secret"
```

The first account across all domains is set as the active default. Additional accounts are included as commented-out blocks.

### `mailio config monit`

Prints a [monit](https://mmonit.com/monit/)-compatible mail config to stdout based on the accounts in `config.yml`. Useful for letting monit send alerts through this server.

```bash
docker exec mailio mailio config monit
```

Example output:

```
# Generated by mailio config monit
# Add to /etc/monitrc (or /etc/monit/monitrc) and run: monit reload
# monit must run on the mail host (submits over localhost).

set mailserver localhost port 587
    username "user@example-a.com" password "secret"

set mail-format { from: Monit <user@example-a.com> }

# Set your alert recipient(s), e.g.:
# set alert you@example.com
```

monit points at `localhost:587` without TLS (the password never leaves the host over loopback). The `from:` address equals the SMTP-AUTH username because of the [sender/login binding](#sender-identity-binding) — using a different address would be rejected. As with `config mutt`, the first account is the active default and the rest are commented alternatives (monit uses a single mailserver/from pair).

### `mailio cert renew [--force]`

Manually triggers Let's Encrypt certificate renewal. Requires `letsencrypt:` to be configured.

```bash
docker exec mailio mailio cert renew
# Force renewal even if the certificate is still valid:
docker exec mailio mailio cert renew --force
```

Without `--force`, renewal is skipped if the certificate has more than 30 days remaining. Use this command in a cron job to keep the certificate up to date (Let's Encrypt certificates are valid for 90 days).

---

## Sending mail from an app

Connect to port `587` with STARTTLS and PLAIN/LOGIN auth:

| Setting | Value |
|---------|-------|
| Host | `<server IP>` or `mailio` (from same Docker network) |
| Port | `587` |
| Auth | PLAIN or LOGIN |
| Username | full address, e.g. `user@example-a.com` |
| Password | value from `config.yml` |
| From address | must equal the username (the account's own address) |

<a name="sender-identity-binding"></a>
**Sender identity binding.** The SMTP-AUTH username is the full address (`user@example-a.com`), not the bare `local_part`, so accounts in different domains stay distinct. Once authenticated, an account may send mail **only as its own address** — using a `From`/MAIL FROM belonging to a *different* configured account is rejected (`reject_sender_login_mismatch`). Note this constrains addresses that are owned by some account; it does not by itself forbid an authenticated client from using an unrelated, unconfigured `From`.

---

## Testing

```bash
# From the host (requires swaks: brew install swaks)
# Username is the full address; From must match it.
swaks --to test@example.com --from user@example-a.com \
  --server localhost:587 --auth PLAIN \
  --auth-user user@example-a.com --auth-password secret

# Verify DKIM signature
# Send a mail to check-auth@verifier.port25.com and inspect the reply
```

---

## Dependencies

- A **DNS API** exposing a REST `/dns` endpoint (GET to list, POST to create, DELETE to remove) must be reachable before `mailio` starts — it syncs DNS records during setup. [`doapi`](https://github.com/reeywhaar/doapi) is the reference implementation; any service speaking the same contract works.
- Point `DNS_API_URL` at it (required — DNS operations fail if unset). The compose file defaults to `http://doapi:8433`.
- The Docker network shared with the DNS API must already exist (e.g. `docker network create doapi-net`).

### `/dns` endpoint specification

`mailio` talks to the DNS API over three methods on a single `/dns` path, relative to `DNS_API_URL`. Request bodies are JSON (`Content-Type: application/json`); any response with an HTTP status `>= 400` is treated as a failure and its body is surfaced verbatim as the error message.

Two object shapes are used throughout:

```jsonc
// Record
{
  "id":   123,             // integer, provider-assigned; used to delete
  "type": "TXT",           // record type, e.g. "TXT", "A", "MX"
  "name": "_dmarc",        // name relative to the domain (see conventions below)
  "data": "v=DMARC1; ..."  // record value
}

// Domain
{
  "name": "example-a.com", // apex domain name
  "records": [ /* Record, ... */ ]
}
```

#### `GET /dns` — list all records

Returns a JSON array of `Domain` objects covering every domain the provider manages. No request body.

```jsonc
// 200 OK
[
  {
    "name": "example-a.com",
    "records": [
      { "id": 1, "type": "TXT", "name": "@",                "data": "v=spf1 a:mail.example-a.com ~all" },
      { "id": 2, "type": "TXT", "name": "mail._domainkey",  "data": "v=DKIM1; k=rsa; p=MIGf..." },
      { "id": 3, "type": "TXT", "name": "_dmarc",           "data": "v=DMARC1; p=quarantine" }
    ]
  }
]
```

#### `POST /dns` — create a record

Request body:

```jsonc
{
  "domain": "example-a.com",   // required, apex domain the record belongs to
  "type":   "TXT",             // required, record type
  "name":   "_dmarc",          // required, name relative to the domain
  "data":   "v=DMARC1; p=...", // required, record value
  "ttl":    3600               // optional; omitted entirely to use the provider default
}
```

The response body is ignored — only the status code matters (`< 400` = success).

#### `DELETE /dns` — remove a record

Request body:

```jsonc
{
  "domain": "example-a.com",   // required, apex domain the record belongs to
  "id":     3                  // required, the record's provider-assigned id (from GET)
}
```

The response body is ignored — only the status code matters (`< 400` = success).

#### Name conventions

Record `name` is always **relative to the apex domain**, never a fully-qualified name. `mailio` reads and writes these names:

| Name | Type | Purpose |
|------|------|---------|
| `@` | TXT | SPF (`v=spf1 …`) at the apex |
| `<selector>._domainkey` | TXT | DKIM public key |
| `_dmarc` | TXT | DMARC policy |
| `_acme-challenge[.<sub>]` | TXT | Let's Encrypt DNS-01 challenge (created then cleaned up during cert issuance) |

`mailio` upserts by listing first (`GET`), deleting any stale/duplicate records by `id` (`DELETE`), then creating the desired record (`POST`); a record whose `data` already matches is left untouched.

#### Reference implementation: `doapi`

[`doapi`](https://github.com/reeywhaar/doapi) implements this contract on top of the DigitalOcean DNS API. It exposes the `/dns` HTTP server (and a matching CLI) for listing, creating, and deleting records across DigitalOcean-managed domains, and is the default target in the compose file (`DNS_API_URL=http://doapi:8433`). If your DNS is hosted elsewhere, run any service that speaks the contract above in its place.
