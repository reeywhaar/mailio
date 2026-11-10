# Storage

## One SQLite file holds everything

`mailio.db` in the data directory: domains, accounts, sessions, and — as it arrives — the mail
itself. A backup is one file — see [deploy.md](deploy.md#backups-go-to-a-backio-agent) — and a
stopped instance leaves exactly one, because closing checkpoints the write-ahead log into it.

## Commits are synchronous=FULL

Once mailio answers `250` to a message, the sending server deletes its copy. A commit that a power
cut can undo is then mail lost with nobody holding it, so every commit is flushed before it is
acknowledged. `NORMAL`, which is enough for a cache of somebody else's mail, is not enough here.

## Pragmas are in the connection string

The driver applies DSN pragmas to every connection it opens, and a pool opens connections whenever
it likes. A pragma run once on a pool reaches one connection, and the rest go without it. Each one
is read back at open, because a pragma is a request rather than a guarantee.

## The writer takes its lock at BEGIN

The writer pool opens transactions `IMMEDIATE`. The command line writes to the same file as the
running server, and a transaction that reads and then upgrades to writing fails at once, without
waiting, if the other process holds the lock at that moment.

## Mail is rows, and bodies are rows apart

`mailboxes` per account, `messages` per mailbox with IMAP's UID, flags, arrival time and when it
was put in that mailbox, and the message itself in `bodies`, so listing a mailbox never reads a
body. A message is stored as it
arrived, with mailio's Received and Authentication-Results headers in front.

Mailboxes and messages are keyed by rowid, not by a prefixed ULID: nothing outside the database
names them. What a client sees is the mailbox's name and the message's UID.

## Trash and Junk empty themselves

Each hour mailio deletes for good what has been in an account's Trash or Junk longer than it keeps
it there: 30 days by default for each, a domain's own, or an account's own, set in their dialogs,
and 0 keeps it forever. The time counts from when the message was put there, delivered as junk or
moved, which the message records and its arrival time does not; messages there before mailio
recorded it count from the upgrade. A client with the mailbox open hears of each one going, as it
would of an expunge by another client.

## An account's mail can be cleared at once

The account's dialog shows how many messages it holds and how many bytes they take, as received;
each domain's row and the Mail server section add them up. Clear all mail, in its danger zone,
deletes every message in every one of its mailboxes for good after a confirm, and keeps the
mailboxes; open clients hear of each message going.

## A UID is given inside the insert's transaction

`uid_next` is bumped and read in one statement, in the transaction that inserts the message, so
two deliveries never share a UID and a rolled-back one leaves a gap rather than a reuse. Every
write that adds a message also holds one lock through to telling IMAP about it, so open sessions
hear of new UIDs in order.

## Mail arriving counts as a change

Every delivered or moved message moves the backup counter, so an instance receiving mail sends a
whole copy of the database every half hour. That is fine while the database is small; when it is
not, the interval is the thing to revisit.

