# API

JSON over HTTP, for the admin interface. Every route but the first four needs an admin's session
cookie; without one it answers `401`. Users never sign in here: an admin has a name, not an
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
| `GET /api/domains` | every domain |
| `POST /api/domains` | `{name}` |
| `PATCH /api/domains/{id}` | `{unknown, unknown_user_id}` — see [addresses.md](addresses.md) |
| `DELETE /api/domains/{id}` | `409` while it has users |
| `GET /api/users` | every user |
| `POST /api/users` | `{address, password}` |
| `PATCH /api/users/{id}` | `{password}` |
| `DELETE /api/users/{id}` | `409` for a user getting a domain's junk |
| `GET /api/users/{id}/password` | `{password}`, sealed copy opened. `404` for one set before passwords were kept |
| `GET /api/mail` | `{host, dns_api, certificate}` |
| `GET /api/relay` | the relay without its password, `{…, has_password}`, or `null` |
| `PUT /api/relay` | `{host, port, security, username, password}`. An empty password keeps the stored one |
| `DELETE /api/relay` | deliver directly again |
| `POST /api/relay/test` | sign in to the relay and send nothing. `502 relay_failed` with its answer |
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
{ "code": "conflict", "message": "reey.ru still has users. Delete them first." }
```

The code is the contract: `invalid`, `unauthenticated`, `not_found`, `conflict`,
`unsupported_media_type`, `body_too_large`, `rate_limited`, `relay_failed`, `internal`. The sentence is written
for whoever reads it, and nothing may match on it.

## Times are Unix seconds

`created_at` and `expires_at` are integers, UTC. The browser shows them in the viewer's zone.
