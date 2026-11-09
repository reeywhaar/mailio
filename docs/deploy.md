# Deploy

One image, one directory.

```
ghcr.io/reeywhaar/mailio:latest
```

## Environment

| variable | default | meaning |
| --- | --- | --- |
| `MAILIO_PUBLIC_URL` | *required* | The address the admin interface is opened at |
| `MAILIO_SECRET_KEY` | *required* | 32 random bytes, base64. Seals users' passwords and the relay's |
| `MAILIO_MAIL_HOST` | the public URL's host | The mail server's name: MX target, greeting, certificate |
| `MAILIO_DNS_API_URL` | — | A DNS API (doapi). Publishes DKIM, SPF, DMARC, and gets the certificate |
| `MAILIO_DATA_DIR` | `/data` | Where `mailio.db` lives. Fixed in the image; a variable for local runs |
| `MAILIO_LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error` |
| `MAILIO_BACKUP_URL` | — | A backio agent's `POST /backup`. Unset, mailio takes no backups |
| `MAILIO_TRUSTED_PROXIES` | `private` | Whose `X-Forwarded-For` is believed: addresses, ranges, `private`, or `none` |

## There is no config file

Domains, users, and what each domain does with unknown mail are in the database and edited in the
admin interface. The environment is only for what must be true before the process starts.

## `MAILIO_PUBLIC_URL` is required and never inferred

`Host` and `X-Forwarded-Host` are both client-supplied, so an admin link built from one is a link
a stranger controls. It also decides whether the session cookie carries `Secure`. A trailing slash
is trimmed; a path is refused, because mailio serves from the root.

## `MAILIO_SECRET_KEY`

Required. It seals each user's password, so an admin can look it up again, and the relay's:

```sh
openssl rand -base64 32
```

**Keep it apart from the data directory and its backups**, which is the point of it: a copied
database is not a list of passwords. **And keep it**: with a new key, mail still flows and
everyone still signs in, but no stored password can be shown until it is set again.

## `MAILIO_MAIL_HOST` is the name the world reaches mail at

Every domain's MX points at it, other servers are greeted with it, and the mail ports'
certificate is for it; mail clients use it as their IMAP and SMTP server, whatever the domain.
Unset, it is the host of `MAILIO_PUBLIC_URL`, so one name serves both. Set it when the admin
interface lives elsewhere. The server's IP should have it as its reverse DNS, or the mail it
sends looks suspect.

## Caddy does the admin interface's TLS

mailio speaks plain HTTP on `:80` inside the container. [caddy-docker-proxy] gets the certificate
and terminates TLS, reaching mailio over its network, so nothing is published for the web side:

```yaml
services:
  mailio:
    image: ghcr.io/reeywhaar/mailio:latest
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
      caddy.log.output: stdout
      caddy.log.format: json

volumes:
  data:

networks:
  caddy:
    external: true
```

`MAILIO_PUBLIC_URL` and the `caddy` label name the same host. The `https` in the URL is what
makes the session cookie `Secure`, since mailio itself only ever sees Caddy's plain HTTP.

[caddy-docker-proxy]: https://github.com/lucaslorentz/caddy-docker-proxy

## The data directory is mounted, never created

```sh
docker run -d --name mailio \
  -e MAILIO_PUBLIC_URL=http://localhost:8080 \
  -e MAILIO_SECRET_KEY=$(openssl rand -base64 32) \
  -v mailio-data:/data \
  -p 8080:80 \
  ghcr.io/reeywhaar/mailio:latest
```

The image declares no `VOLUME`, and a missing directory refuses to start. A `VOLUME` makes docker
invent an anonymous volume whenever nobody mounts one, so a forgotten `-v` runs happily and loses
everything when the container is replaced.

## An admin exists because somebody with a shell opened a link

There is no sign-up page and no default password. When nobody is an admin — a new database, or
the last admin gone — `serve` logs a link at startup:

```sh
docker logs mailio
```

```
{"level":"INFO","msg":"no admin yet: open this link to make one","link":"https://mail.example.com/admin/password#token=…",…}
```

The link asks for a name and a password: what you sign in to the admin interface with, not a mail
address. A new name becomes an admin; one that already exists gets the password and is signed out
everywhere. Either way the link signs you in, and the domains and users are added from there.

Locked out, or a password forgotten:

```sh
docker exec mailio mailio admin reset
```

prints a new link. A link works once, for a day, and each new one ends the one before.

This is the one place a token is logged: the startup line is the only way into a fresh instance.

## The admin link's token is in the fragment

Everything after `#` stays in the browser; no request carries it. Caddy's access log records
every URL whole, query included, so a `?token=` would sit in it, live, until somebody used the
link. The page posts the token in a body instead.

## Every wrong password is one line, for fail2ban

mailio bans nobody itself. Each refused password is logged as

```
{"time":"…","level":"WARN","msg":"password refused","ip":"203.0.113.7","via":"admin"}
```

and fail2ban, reading the container's log as a file, does the banning. `via` says where the
password was tried; the mail ports log the same line when they arrive, so one filter and one jail
cover all of them.

```ini
# filter.d/mailio.local
[Definition]
failregex = ^\{.*"msg":"password refused".*"ip":"<ADDR>"
ignoreregex =
```

```ini
# jail.d/mailio.local
[mailio]
enabled   = true
filter    = mailio
logpath   = /remotelogs/docker/mailio.log
banaction = iptables-allports[chain=DOCKER-USER]
findtime  = 600
bantime   = 1d
maxretry  = 5
```

**All ports, in `DOCKER-USER`.** Traffic to a published container port never passes `INPUT`, and
a guesser refused on IMAP should not get to carry on at submission or the admin interface.

**The line names neither the address tried nor the password.** A password typed into the address
field is common enough that logging the address would log passwords.

## The caller is the first `X-Forwarded-For` hop that is not a trusted proxy

The admin interface sits behind a proxy, so the connection mailio sees is the proxy's. Each proxy
appends who it accepted to `X-Forwarded-For`, so mailio reads the header right to left, stepping
over hops that are trusted proxies; the first one that is not is the caller. Everything left of
it is whatever the caller sent.

The header is read at all only when the connection comes from a trusted proxy. From anywhere else
it is the caller's own claim, and believing it would let a stranger choose the address fail2ban
bans — yours, say. `X-Real-IP` is never read: a proxy that does not set it passes the caller's
along.

`MAILIO_TRUSTED_PROXIES` names the trusted proxies: addresses, ranges, `private`, or `none`,
separated by commas or spaces.

| setup | value |
| --- | --- |
| Proxy on the same host or Docker network | `private`, the default |
| Proxy on another machine | its address, e.g. `198.51.100.3` |
| A CDN in front of that proxy | `private` plus the CDN's published ranges |
| No proxy, or the port is reachable without it | `none` |

Caddy and Traefik set the header by default; nginx needs
`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for`. In a chain, every proxy has to
trust the one before it too, or it replaces the header rather than appending to it: Caddy behind
Cloudflare needs Cloudflare's ranges in its own `trusted_proxies`.

`private` is 10/8, 172.16/12, 192.168/16, fc00::/7 and loopback. It assumes every caller with
such an address is yours — on a shared Docker network, every other container; where that is not
so, name the proxy itself instead.

Rootless Docker and Podman can deliver every connection from one private address, so a caller
reaching the port directly looks like the proxy. Keep that port unpublished, or set `none`.

### Behind a CDN, an `iptables` ban does nothing

The banned address never connects to this host; the CDN does. A jail there needs the CDN's own
ban, such as fail2ban's `cloudflare` action, in place of `iptables-allports`.

### The mail ports take the connection's own address

SMTP and IMAP are published straight from mailio, so the peer is the client and no header is
read. A TCP proxy in front of them would put every client behind one address, and one ban would
lock everybody out.

## Ports

| port | what | published |
| --- | --- | --- |
| 80 | the admin interface, plain HTTP | no: Caddy reaches it |
| 25 | other servers deliver; STARTTLS offered, no AUTH | yes |
| 587 | users send; AUTH only after STARTTLS | yes |
| 465 | users send; TLS from the first byte | yes |
| 993 | users read; TLS from the first byte | yes |

There is no plain IMAP on 143 and no POP3: a password never crosses the wire unencrypted.

## The mail ports have their own certificate

For `MAILIO_MAIL_HOST`, kept in the database and swapped in without a restart. With
`MAILIO_DNS_API_URL`, it comes from Let's Encrypt, proved through a DNS record the API writes, and
is renewed once a third of its life is left. DNS rather than HTTP because port 80 is Caddy's and
the mail ports cannot answer a challenge. Without a DNS API it stays self-signed: other servers
deliver over it all the same, and mail clients warn.

## DNS records

Each domain's are in the admin interface under DNS…: MX to `MAILIO_MAIL_HOST`, SPF, DMARC, and
the DKIM key. With `MAILIO_DNS_API_URL`, mailio publishes them every ten minutes.

**The DKIM record is kept current; SPF and DMARC are only created when absent.** An existing SPF
is somebody's decision, such as one that includes the relay, and a sync rewriting it would break
mail. **MX is never touched**: where a domain's mail goes is set by hand, once.

The key is published as `mailio._domainkey`, not the `mail._domainkey` the Postfix-based mailio
used, so the old key keeps verifying mail it signed while the new one takes over.

## Sending goes through the relay when there is one

Many hosting providers block outbound port 25. Set a relay in the admin interface under Sending,
with Test to check it signs in, and every message is handed to it. Without one, mailio delivers
to each recipient's MX itself.

A message waits in the queue until delivered. A temporary failure is retried with growing waits
for five days; a refusal, or the five days running out, puts a report in the sender's INBOX. A
relay that cannot be reached or refuses the sign-in is always temporary, so a wrong relay
password is fixed in the admin interface and nothing is lost meanwhile. Retry now sends the
queue at once.

## DMARC is obeyed and nothing else filters

An arriving message gets SPF, DKIM and DMARC checked and an Authentication-Results header. When
its sender's DMARC policy is reject and it fails, it is refused; quarantine puts it in Junk.
There is no content filter: what is junk beyond that is the reader's to move.

## Switching from the Postfix-based mailio

1. Deploy mailio beside the old container, under another name, with no mail ports. Add the
   domains and users in the admin interface. With `MAILIO_DNS_API_URL` the new DKIM key and the
   certificate arrive by themselves; without, publish the records under DNS….
2. Set the relay, if the host needs one.
3. Stop the old container, and give the new one its name and the mail ports:

   ```yaml
   ports: ["25:25", "465:465", "587:587", "993:993"]
   ```

4. Import the old mail, mounting the old Maildir read-only:

   ```sh
   docker exec mailio mailio import maildir --dry-run /import/root inbox@reey.ru inbox@vyrtsev.com
   docker exec mailio mailio import maildir /import/root inbox@reey.ru inbox@vyrtsev.com
   ```

   Several addresses delivered into one Maildir are sorted to whoever `X-Original-To` names, the
   address before any alias; sent mail by its From. Subfolders such as `.Sent` and `.Junk` go to
   the matching mailbox. Importing twice imports twice.

5. Point mail clients at `MAILIO_MAIL_HOST`, IMAP 993 and SMTP 465, with the user's address and
   password.

## Backups go to a backio agent

Set `MAILIO_BACKUP_URL` to a [backio-agent] and mailio posts it a gzipped `VACUUM INTO` snapshot
of the database. Leave it unset and mailio takes no backups at all. The snapshot is written to a
`backup-*` folder in the data directory and removed once sent; one left by a process killed
mid-pass is removed on the next start.

```
mailio ──POST archive──▶ agent ──▶ wherever the agent was told
       (no credential)    (holds the token, names the file, prunes old ones)
```

```yaml
services:
  mailio:
    image: ghcr.io/reeywhaar/mailio:latest
    environment:
      MAILIO_PUBLIC_URL: https://mail.example.com
      MAILIO_SECRET_KEY: …
      MAILIO_BACKUP_URL: http://backup:8080/backup
    volumes:
      - data:/data

  backup:
    image: ghcr.io/reeywhaar/backio-agent:latest
    restart: unless-stopped
    environment:
      # …destination and credential, which are the agent's business
```

Behind Caddy, mailio is on both networks, `networks: [default, caddy]`: Caddy reaches it on one
and it reaches the agent on the other.

**mailio holds no credential, no provider, no bucket and no retention policy.** It decides when
to back up and what goes in the archive; everything after that belongs to the agent. A
compromised mailio container cannot read, overwrite or delete a single existing backup. It can
only hand over one more archive.

[backio-agent]: https://github.com/reeywhaar/backio/tree/main/agent

### A copy goes out when content changed, and at no other time

The loop looks every half hour and sends only when something was written since the last archive
the agent accepted: a domain, a user, a password, an admin, what a domain does with unknown mail.
Saving what is already there is not a change. Sessions and admin links are the process noticing
itself and do not count either.

A restart sends one archive, because a process that has just started may be on a volume nobody
has a copy of yet. There is no heartbeat: an instance nobody touched sends nothing, and whether
it is running is `/healthz`'s question.

### The snapshot is read, never written

`VACUUM INTO` runs on a reader connection. A WAL reader sees one moment and blocks nobody, so mail
keeps committing while a large copy is written; on the one writer connection, every delivery would
wait for the copy to finish.

### Waiting as long as the agent does

The agent answers once the archive is on the remote. mailio waits up to 30 minutes, the agent's
own `UPLOAD_TIMEOUT` default, because giving up sooner records a failure for an archive that is
stored anyway.

### Restoring

Stop the container, extract the archive into the data directory, start it again. Admins stay
signed in, because sessions are in the same file.

The archive carries every password hash, so it is worth the same care as the directory itself.

## The image

- **Node stage** builds the admin interface; **Go stage** cross-compiles with `CGO_ENABLED=0`, the
  version stamped through `-ldflags`. Both run on the build machine, never under emulation.
- `modernc.org/sqlite` is pure Go, which is what keeps the binary static.
- **`HEALTHCHECK` runs `mailio healthcheck`**, a second process asking the first, so the image
  needs no HTTP client and a wedged server fails it.
- The bundle is read from `/srv/web` at startup rather than embedded, so `go test ./...` and
  `go run .` work with no frontend build.

## Running from a checkout

```sh
npm --prefix web ci && npm --prefix web run build
mkdir -p data
MAILIO_PUBLIC_URL=http://localhost MAILIO_SECRET_KEY=$(openssl rand -base64 32) MAILIO_DATA_DIR=data go run . serve
```

`npm --prefix web run dev` serves the interface with reloading and sends `/api` to the Go server
on port 80.
