# mailio

A mail server in one static Go binary and one SQLite file. Domains, accounts and their settings
live in the database and are managed in its admin interface; mail is read with
[emguio](https://github.com/reeywhaar/emguio) or any mail client over IMAP.

- **Receives on 25.** SPF, DKIM and DMARC are checked; mail its sender's DMARC policy rejects is
  refused, and what it quarantines goes to Junk.
- **Serves IMAP on 993** to every client, with the standard mailboxes: Inbox, Sent, Drafts,
  Archive, Junk, Trash.
- **Sends on 465 and 587,** signed with each domain's DKIM key, directly or through a relay such
  as Gmail when the host may not send on 25 itself; relays are a list, one is the default, and a
  domain can choose another. Failures are retried for five days and reported to the sender.
- **Plus addresses.** `inbox+shop@example.com` arrives in `inbox@example.com`.
- **A credential per device.** Each has a label, its own login, `inbox+k3x9@example.com`, and a
  generated password shown once, and shows when it was last used. Delete the phone's without
  touching the laptop's.
- **Per account:** keep the mail, refuse it (a no-reply address that only sends), or forward it
  to another account. **Defaults, which a domain can override:** reject, drop or junk mail for
  addresses nobody has, the DMARC and SPF it publishes, and the relay it sends through.
- **Looks after itself** with a DNS API ([doapi](https://github.com/reeywhaar/doapi)): it
  publishes each domain's DKIM, SPF and DMARC, and gets and renews its certificate from Let's
  Encrypt.
- **Backups** of the whole server, as one file, to a
  [backio](https://github.com/reeywhaar/backio) agent.

## Run it

Behind [caddy-docker-proxy](https://github.com/lucaslorentz/caddy-docker-proxy), which serves the
admin interface over HTTPS; the mail ports are published from mailio itself:

```yaml
# compose.yml
services:
  mailio:
    image: ghcr.io/reeywhaar/mailio:v2
    container_name: mailio
    restart: unless-stopped
    environment:
      MAILIO_PUBLIC_URL: https://mail.example.com
      MAILIO_SECRET_KEY: … # openssl rand -base64 32
    ports: ["25:25", "465:465", "587:587", "993:993"]
    volumes:
      - data:/data
    networks:
      - caddy
    labels:
      caddy: mail.example.com
      caddy.reverse_proxy: "{{upstreams 80}}"

volumes:
  data:

networks:
  caddy:
    external: true
```

```sh
docker compose up -d
docker compose logs mailio
```

The log has a link. Open it and choose the first admin's name and password: a sign-in for the
admin interface, not a mailbox. Then, in the admin interface:

1. **Add a domain,** and point its MX at the mail host. A click on its row opens its settings,
   and the records mailio keeps for it: SPF, DMARC and its DKIM key.
2. **Add an account,** with its address. Its first credential is labelled `default`, its login
   is the address itself, and its password is generated and shown once.
3. **Add a relay** under Sending → Configure sending…, if the host's provider blocks outbound
   port 25, press Test, and make it the default.

Locked out later:

```sh
docker compose exec mailio mailio admin reset
```

| variable | |
| --- | --- |
| `MAILIO_PUBLIC_URL` | *required.* Where the admin interface is opened |
| `MAILIO_SECRET_KEY` | *required.* Seals the relays' passwords; keep it apart from backups |
| `MAILIO_MAIL_HOST` | the mail server's name, if not the public URL's host |
| `MAILIO_DNS_API_URL` | a DNS API, to publish records and get the certificate |
| `MAILIO_BACKUP_URL` | a backio agent's `POST /backup` |
| `MAILIO_TRUSTED_PROXIES` | whose `X-Forwarded-For` is believed; `private` by default |

Each is explained in [docs/deploy.md](docs/deploy.md), with fail2ban, the ports, and moving from
the Postfix-based mailio.

## Set up a mail client

| | |
| --- | --- |
| Incoming | IMAP, `mail.example.com`, port 993, SSL/TLS |
| Outgoing | SMTP, `mail.example.com`, port 465 SSL/TLS, or 587 STARTTLS |
| Username | the credential's login: `inbox@example.com` for the default, `inbox+k3x9@example.com` for another |
| Password | the credential's password |

A password is shown once, when it is made, and only its hash is kept. Give each device its own
credential: when one is lost, delete it and the others keep working, or give it a new password
and the same login.

## Command line

```sh
mailio serve                                   # the server; the image's default
mailio admin reset                             # a new admin link
mailio import maildir <dir> <address>...       # mail from a Maildir, sorted to its recipients
mailio version
```

`import maildir` takes the Postfix-based mailio's Maildirs, `.Sent` and `.Junk` included, and
sorts a Maildir several addresses shared to whoever each message was sent to. `--dry-run` counts
without writing.

## Images

| tag | |
| --- | --- |
| `:v2` | this mailio, fixes included: what a deployment pins to |
| `:latest` | whatever was published last; a pin to nothing |
| `:v1` | the Postfix-based mailio's last image |

## Develop

Go 1.27 and Node 26.

```sh
go test ./...                                   # the server, including SMTP in and IMAP out end to end
npm --prefix web ci && npm --prefix web test    # the admin interface

mkdir -p data
MAILIO_PUBLIC_URL=http://localhost MAILIO_SECRET_KEY=$(openssl rand -base64 32) \
  MAILIO_DATA_DIR=data go run . serve
```

`npm --prefix web run dev` serves the admin interface with reloading and sends `/api` to the
server on port 80. `.github/smoke/smoke.sh` runs a built image through mail in, mail out and a
restart, as CI does before publishing:

```sh
docker build -t mailio:smoke . && IMAGE=mailio:smoke .github/smoke/smoke.sh
```

```
main.go             the command line
internal/
  store/            SQLite: domains, accounts, credentials, mailboxes, messages, the queue
  smtpd/            25 in, with SPF, DKIM and DMARC; 465 and 587 out
  imapd/            993
  outbound/         the queue: the relay or each recipient's MX, retries, bounces
  certs/ dnssync/   the certificate and the DNS records, through the DNS API
  maildir/          the importer
  api/              the admin interface's JSON
  backup/           snapshots to backio
web/                the admin interface: React, Tailwind
docs/               decisions: deploy, addresses, storage, API, conventions
```

Where mail to an address goes is decided in [docs/addresses.md](docs/addresses.md), how it is
stored in [docs/storage.md](docs/storage.md), the admin API in [docs/api.md](docs/api.md), and how
the code is written in [docs/conventions.md](docs/conventions.md).
