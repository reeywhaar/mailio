# Storage

## One SQLite file holds everything

`mailio.db` in the data directory: domains, users, sessions, and — as it arrives — the mail
itself. A backup is one file, and a stopped instance leaves exactly one, because closing
checkpoints the write-ahead log into it.

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
