# mailio

A mail server that keeps its mail in one SQLite file: one static Go binary, no Postfix, no config
file. Domains and users are set up in its admin interface; mail is read with
[emguio](https://github.com/reeywhaar/emguio), or any mail client, over IMAP.

**Being rebuilt.** The admin interface works: domains, users, and what each domain does with mail
for addresses nobody has. Receiving (SMTP on 25), reading (IMAP) and sending (submission, DKIM,
the outbound queue) come next. The Postfix-based mailio is on `main` until then.

## Run it

Behind [caddy-docker-proxy](https://github.com/lucaslorentz/caddy-docker-proxy), which does the
admin interface's TLS:

```yaml
# compose.yml
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

The log has a link. Open it, and give the address and password of the first admin; its domain is
added with it. Locked out later:

```sh
docker compose exec mailio mailio admin reset
```

Backups go to a [backio agent](https://github.com/reeywhaar/backio/tree/main/agent): set
`MAILIO_BACKUP_URL` to its `POST /backup`.

More in [docs/deploy.md](docs/deploy.md). Where mail to an address goes is in
[docs/addresses.md](docs/addresses.md), the API in [docs/api.md](docs/api.md), and how the code is
written in [docs/conventions.md](docs/conventions.md).
