# API

JSON over HTTP, for the admin interface. Every route but the first four needs an admin's session
cookie; without one it answers `401`. Accounts never sign in here: an admin has a name, not an
address. See [addresses.md](addresses.md).

A body is JSON with `Content-Type: application/json`, or `415`. That requirement is half the
CSRF defense; the `SameSite=Lax` cookie is the other.

| route | does |
| --- | --- |
| `GET /healthz` | `204` when the database answers |
| `POST /api/admin-link/check` | `{token}` → `{expires_at}` for a live link, `404` otherwise |
| `POST /api/admin-link` | `{token, name, password}` → makes an admin or resets one, signs in |
| `POST /api/session` | `{name, password}` → signs an admin in |
| `DELETE /api/session` | signs out |
| `GET /api/me` | the signed-in admin |
| `GET /api/domains` | every domain, each record with `managed` (false for MX), `status` (`ok`, `missing`, `different`, `several`, `failed`, or null before the first check), `found`, and `error` for a failed one, and the domain's `dns_checked_at` |
| `POST /api/domains` | `{name}` |
| `PATCH /api/domains/{id}` | `{unknown, unknown_account_id, dmarc, spf, relay, max_message_bytes}`, each optional; `unknown` is the catch-all, `reject`, `drop`, `forward` or `junk`, the last two with `unknown_account_id`; `""` or `0` returns one to the default, and `relay` is `"builtin"` or a relay's id. See [addresses.md](addresses.md) |
| `POST /api/domains/{id}/check` | looks up its records now → the domain |
| `DELETE /api/domains/{id}` | `409` while it has accounts |
| `GET /api/accounts` | every account, with its credentials and never a password |
| `POST /api/accounts` | `{address}` → the account and `password`, its `default` credential's, generated and in this answer only |
| `PATCH /api/accounts/{id}` | `{incoming, forward_account_id, max_message_bytes, relay}`, each optional: `keep`, `reject`, or `forward` to an account; its own largest message, `0` returning it to its domain's; its own relay, `"builtin"` or a relay's id, `""` returning it to its domain's |
| `DELETE /api/accounts/{id}` | `409` for an account getting a domain's catch-all, or forwarded to |
| `POST /api/accounts/{id}/credentials` | `{label, password}` → `{id, label, login, password, …}`, the login newly tagged. An empty password is generated; either way it is in this answer only |
| `POST /api/accounts/{id}/credentials/{cid}/reset` | `{password}`, empty for a generated one → the same credential and login with the new `password`, in this answer only |
| `DELETE /api/accounts/{id}/credentials/{cid}` | the last may go too |
| `GET /api/mail` | `{host, dns_api, staging, certificate}` |
| `GET /api/settings` | the defaults: `{unknown, dmarc, spf, relay_id, max_message_bytes}`, `unknown` being `reject` or `drop`, a null relay being built-in |
| `PUT /api/settings` | the same, all of it; `max_message_bytes` left out stays as it is |
| `GET /api/relays` | every relay, by label, without its password: `{…, has_password}` |
| `POST /api/relays` | `{label, host, port, security, username, password}`. `409` for a label taken |
| `PUT /api/relays/{id}` | the same; an empty password keeps the stored one |
| `DELETE /api/relays/{id}` | `409` while it is the default, a domain's or an account's |
| `POST /api/relays/{id}/test` | sign in to the relay and send nothing. `502 relay_failed` with its answer |
| `GET /api/queue` | what is waiting to be sent |
| `POST /api/queue/retry` | send the queue now |
| `GET /api/admins` | every admin |
| `POST /api/admins` | `{name, password}` |
| `PATCH /api/admins/{id}` | `{password}`. Signs that admin out everywhere but the session sending it |
| `DELETE /api/admins/{id}` | `409` for the last admin |

## A token travels in a body, never a URL

A proxy in front of mailio logs every URL it forwards. Checking an admin link is a `POST` for that
reason, though it changes nothing.

## A refusal is a code and a sentence

```json
{ "code": "conflict", "message": "reey.ru still has accounts. Delete them first." }
```

The code is the contract: `invalid`, `unauthenticated`, `not_found`, `conflict`,
`unsupported_media_type`, `body_too_large`, `rate_limited`, `relay_failed`, `internal`. The sentence is written
for whoever reads it, and nothing may match on it.

## Times are Unix seconds

`created_at`, `last_used_at` and `expires_at` are integers, UTC. The browser shows them in the viewer's zone.
