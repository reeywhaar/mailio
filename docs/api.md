# API

JSON over HTTP, for the admin interface. Every route but the first four needs the session cookie
of a user who is an admin; without one it answers `401`.

A body is JSON with `Content-Type: application/json`, or `415`. That requirement is half the
CSRF defense; the `SameSite=Lax` cookie is the other.

| route | does |
| --- | --- |
| `GET /healthz` | `204` when the database answers |
| `POST /api/admin-link/check` | `{token}` → `{expires_at}` for a live link, `404` otherwise |
| `POST /api/admin-link` | `{token, address, password}` → makes or resets an admin, signs in |
| `POST /api/session` | `{address, password}` → signs an admin in. `403` for a user who is not one |
| `DELETE /api/session` | signs out |
| `GET /api/me` | the signed-in user |
| `GET /api/domains` | every domain |
| `POST /api/domains` | `{name}` |
| `PATCH /api/domains/{id}` | `{unknown, unknown_user_id}` — see [addresses.md](addresses.md) |
| `DELETE /api/domains/{id}` | `409` while it has users |
| `GET /api/users` | every user |
| `POST /api/users` | `{address, password, admin}` |
| `PATCH /api/users/{id}` | `{password?, admin?}`. A new password signs the user out everywhere |
| `DELETE /api/users/{id}` | `409` for the last admin, or a user getting a domain's junk |

## A token travels in a body, never a URL

A proxy in front of mailio logs every URL it forwards. Checking an admin link is a `POST` for that
reason, though it changes nothing.

## A refusal is a code and a sentence

```json
{ "code": "conflict", "message": "reey.ru still has users. Delete them first." }
```

The code is the contract: `invalid`, `unauthenticated`, `forbidden`, `not_found`, `conflict`,
`unsupported_media_type`, `body_too_large`, `rate_limited`, `internal`. The sentence is written
for whoever reads it, and nothing may match on it.

## Times are Unix seconds

`created_at` and `expires_at` are integers, UTC. The browser shows them in the viewer's zone.
