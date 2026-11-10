# Conventions

The same as emguio's, so the two read as one codebase. Settled once here so it is not re-argued
in review.

## Words

One name per thing, in identifiers, the API, the docs and the interface.

| word | means | in code |
| --- | --- | --- |
| domain | a domain mailio receives mail for | `Domain`, `domains`, `domain_id` |
| account | an address mail is delivered to, and the credentials that sign in as it | `Account`, `accounts`, `account_id` |
| credential | one of an account's sign-ins: a label, a login and a generated password | `Credential`, `credentials` |
| login | what signs in with one credential: the address, tagged unless it is the default | `Login` |
| mailbox | an IMAP folder inside an account: INBOX, Sent, Junk | `mailboxes` |
| relay | a server mail is sent through instead of straight to the recipient's, with a unique label | `Relay`, `relays` |
| defaults | what every domain follows unless it has its own: unknown addresses, DMARC, SPF, relay | `Settings`, `settings` |
| admin | a name and a password that sign in to the admin interface; never an account | `Admin`, `admins` |
| admin link | the one-time link that makes an admin | `AdminLink`, `admin_links` |
| address | `local@domain`, lowercased | `address.Address` |
| tag | what follows the `+` in an address | `Tag` |

**"User" is not a word here.** It would mean an account to one reader and an admin to another.
Somebody signing in to mail has an account; somebody signing in here is an admin.

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
  `TestMailToATaggedAddressGoesToItsAccount`.
- Errors wrap with `%w` and name what was being done.
- One error vocabulary in `internal/store` — `ErrNotFound`, `ErrConflict`, `ErrInvalid` — built
  through `store.NotFound`, `store.Conflict` and `store.Invalid` with a sentence for whoever reads
  it. `internal/store` is the only package that writes SQL.
- `context.Context` first on anything that can block.
- Injectable clocks, so expiry is driven rather than slept through.

## Interface

**Every add or change happens in a dialog that holds it until Save.** A button opens it ("Add
account", "Configure sending…"), or a row does: a list is rows, each a summary of what it is, and
a click opens its settings. Save sends the whole change at once; a refusal leaves the dialog open
with the server's sentence and everything chosen in it; closing it forgets the change. So the page
only ever shows what the server has, and a failed change never leaves it half edited.

A delete is in the dialog of what it deletes and takes two presses. A command whose label ends in
"…" opens a dialog; one without acts on the press.

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
| `ac_` | account |
| `ad_` | admin |
| `cr_` | an account's credential |
| `rl_` | a relay |

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
account created or deleted and by whom, a domain changed, a sweep that deleted something.

A line names accounts, credentials and domains by id. **Never** a password, a session cookie, or a request's
query; the admin link at startup is the one token logged, and [deploy.md](deploy.md) says why.
When mail arrives, **never** anything from a message: subject, body, attachment names.
