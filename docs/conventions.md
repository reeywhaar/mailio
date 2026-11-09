# Conventions

The same as emguio's, so the two read as one codebase. Settled once here so it is not re-argued
in review.

## Words

One name per thing, in identifiers, the API, the docs and the interface.

| word | means | in code |
| --- | --- | --- |
| domain | a domain mailio receives mail for | `Domain`, `domains`, `domain_id` |
| user | an address mail is delivered to, and the password that signs in as it | `User`, `users`, `user_id` |
| admin | a name and a password that sign in to the admin interface; never a user | `Admin`, `admins` |
| admin link | the one-time link that makes an admin | `AdminLink`, `admin_links` |
| address | `local@domain`, lowercased | `address.Address` |
| tag | what follows the `+` in an address | `Tag` |

**"Account" is not a word here.** It is what Postfix's mailio called a user, and what a mail
client calls the connection to one. A user is a user.

## American spelling

`color`, `favorite`. It is the dialect everything this is written against already uses.

## Comments

**Why, never what.** A comment restating the line under it is noise. A comment recording the
reason a line is written the way it is stops somebody "simplifying" it back into a bug.

**No prose.** A sentence or two. An argument that needs three paragraphs belongs in `docs/`.

**No history.** Never "this used to be X". Git holds that.

**No negations.** A comment explains the code that is there, not what is absent. Why something
does not exist is a decision, and it goes in `docs/`.

**Say it once.** The same reason in a function, its test and its caller is three copies to keep
true.

## Docs

`docs/` holds the decisions, one file per subject. A heading states the decision and the text
under it gives the reason: `## Commits are synchronous=FULL`, not `## Durability`. A decision is
written down in the change that makes it.

## Commits

**One or two lines.** A declarative sentence saying what the change accomplishes. Capitalized, no
trailing period, no prefix, no ticket number.

**No trailers. No `Co-Authored-By`. No generated-with footer.** Ever, whoever or whatever wrote
the change.

## Go

- `gofmt` clean.
- Tests beside sources as `*_test.go`, named as sentences:
  `TestMailToATaggedAddressGoesToItsUser`.
- Errors wrap with `%w` and name what was being done.
- One error vocabulary in `internal/store` — `ErrNotFound`, `ErrConflict`, `ErrInvalid` — built
  through `store.NotFound`, `store.Conflict` and `store.Invalid` with a sentence for whoever reads
  it. `internal/store` is the only package that writes SQL.
- `context.Context` first on anything that can block.
- Injectable clocks, so expiry is driven rather than slept through.

## TypeScript

- Prettier, default settings. oxlint.
- Components are `PascalCase.tsx`; everything else is `camelCase.ts`.
- Imports use the `@app/*` alias, never `../../`.
- `strict`, `noUncheckedIndexedAccess`, `noUnusedLocals`, `noUnusedParameters`,
  `verbatimModuleSyntax`.

## Identifiers

A software-only identifier is a ULID: a prefix, then 26 Crockford characters over 16 bytes, so
`ORDER BY id` is a time order.

| prefix | names |
| --- | --- |
| `dm_` | domain |
| `u_` | user |
| `ad_` | admin |

A new prefix is added to this table in the change that introduces it. Ids are opaque and never
parsed back. A malformed one is refused before it reaches a query, so a typo is a `400` rather
than an empty result that looks like a `404`.

## Migrations

Named `<timestamp>_<snake_case_name>.go`, the timestamp being the UTC moment the file was written:

```sh
date -u +%Y%m%d%H%M%S
```

**Never edit a released migration.** Every deployment past it has recorded it as applied.

## Time

- `main.go` pins `time.Local = time.UTC`.
- Stored as Unix seconds in an `INTEGER` column.
- The server never formats a date for a person.

## Logging

`log/slog`, one JSON object a line on stderr. Log what an operator needs: a sign-in refused, a
user created or deleted and by whom, a domain changed, a sweep that deleted something.

A line names users and domains by id. **Never** a password, a session cookie, or a request's
query; the admin link at startup is the one token logged, and [deploy.md](deploy.md) says why.
When mail arrives, **never** anything from a message: subject, body, attachment names.
