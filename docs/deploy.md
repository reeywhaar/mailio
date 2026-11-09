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

## Ports

`:80` inside the container for the admin interface, reached by Caddy and published nowhere. The
mail ports are published straight from mailio, since Caddy speaks only HTTP; they arrive, with
their own certificate, alongside the code that listens on them.

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
