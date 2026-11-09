# mailio

A mail server that keeps its mail in one SQLite file: one static Go binary, no Postfix, no config
file. Domains and users are set up in its admin interface; mail is read with
[emguio](https://github.com/reeywhaar/emguio), or any mail client, over IMAP.

It receives on 25, with SPF, DKIM and DMARC checked; serves IMAP on 993; takes users' mail on 465
and 587, signs it with each domain's DKIM key, and sends it directly or through a relay. The
certificate and the DNS records look after themselves when there is a DNS API. Moving from the
Postfix-based mailio, on `main`: see [docs/deploy.md](docs/deploy.md#switching-from-the-postfix-based-mailio).

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

The log has a link. Open it, and choose the first admin's name and password — a sign-in for the
admin interface, not a mailbox. Domains and users are added from there. Locked out later:

```sh
docker compose exec mailio mailio admin reset
```

Backups go to a [backio agent](https://github.com/reeywhaar/backio/tree/main/agent): set
`MAILIO_BACKUP_URL` to its `POST /backup`.

More in [docs/deploy.md](docs/deploy.md). Where mail to an address goes is in
[docs/addresses.md](docs/addresses.md), the API in [docs/api.md](docs/api.md), and how the code is
written in [docs/conventions.md](docs/conventions.md).
