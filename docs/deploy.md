# Deploy

One image, one directory.

```
ghcr.io/reeywhaar/mailio:latest
```

## Environment

| variable | default | meaning |
| --- | --- | --- |
| `MAILIO_PUBLIC_URL` | *required* | The address the admin interface is opened at |
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

The link asks for an address and a password. A new address becomes a user, and its domain is
added if mailio did not receive for it yet; that is how the first domain and user come to exist.
An existing address gets the password and is signed out everywhere. Either way it becomes an
admin and is signed in.

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

`:80` inside the container for the admin interface, reached by Caddy and published nowhere. The
mail ports are published straight from mailio, since Caddy speaks only HTTP; they arrive, with
their own certificate, alongside the code that listens on them.

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
MAILIO_PUBLIC_URL=http://localhost MAILIO_DATA_DIR=data go run . serve
```

`npm --prefix web run dev` serves the interface with reloading and sends `/api` to the Go server
on port 80.
