# Addresses

Where mail sent to an address goes. Decided once here; [internal/address](../internal/address)
and `store.Resolve` are the code.

## An account is an address and its credentials

An account is what mail is delivered to. There is no list of extra addresses on an account: an
address that should receive mail is an account. Adding one asks for the address only.

## Each credential has its own login

An account has credentials, each a label, a login and a password, one per phone or laptop. The
login is the address with a tag, `inbox+k3x9@reey.ru`, and it signs in with that credential's
password and no other. The credential an account is created with is labelled `default`, and its
login is the plain address. Any of them can be deleted, `default` too; with none left, the
account still receives mail and nobody signs in as it.

**A login names one credential**, so a sign-in checks one hash however many the account has, a
wrong guess costs the same, and the log can say which credential signed in. A login cannot clash
with another account: no account's address has a `+`, and the name before it is always the
login's own account. Mail sent to a login address arrives like any tagged mail.

**A password is generated, 24 characters, unless the admin types one,** for a mail client that is
particular about what a password holds; at least 8 characters either way. It is shown once: the
answer that makes it carries it, and from then on only its hash is kept. A lost one is replaced:
the credential keeps its login and gets a new password, and whatever signed in with the old one
stops until it is given the new one.

## An account keeps, refuses or forwards its mail

| incoming | mail sent to the account |
| --- | --- |
| `keep` | is delivered to its own mailboxes |
| `reject` | is refused at `RCPT TO`, `550 5.2.1`: an address that only sends, such as `noreply@` |
| `forward` | is delivered to another account's INBOX instead |

**Mail is sent on once, to an account that keeps it.** A forward, or a domain's junk, goes only
to an account whose incoming is `keep`: one that is no-reply would refuse it, and one that
forwards would make a chain. And an account mail is sent on to keeps its mail: while somebody
forwards to it or it gets a domain's junk, it stays `keep` and is not deleted. Both sides are
checked in the transaction that saves, so there are no chains and no circles to follow.

Delivery still follows at most 5 forwards before refusing the mail, for anything saved before
the rule.

## The largest message is a default, a domain's own, or an account's own

The default is 50 MB, set under Defaults. A domain's own wins over it, and an account's own over
its domain's. Any of them is from 1 MB to 100 MB: a message is held in memory whole while it is
received, signed and stored. A MB here is 1,048,576 bytes.

It is the largest the account receives, sends and stores:

| | refused |
| --- | --- |
| mail sent to it | `552 5.2.3` at `RCPT TO` when the sender gives the size up front, as most servers do, and after `DATA` otherwise |
| mail it sends | at `MAIL FROM` when the client gives the size, and after `DATA` otherwise, so the mail app says so at once |
| `APPEND` over IMAP | `TOOBIG`; once signed in, `APPENDLIMIT` is the account's |

**Mail sent on has to fit every account it reaches.** Mail forwarded, or a domain's junk, takes
the smaller of the address it was sent to and the account that keeps it, and mail sent from one
account here to another fits both.

After `DATA`, one answer speaks for every recipient, so a message too big for one address here is
refused for all of them. A sender that gives the size up front hears it per address instead.

Mail sent through a relay meets the relay's limit as well, which mailio does not know: Gmail
takes about 34 MB. A larger one comes back as a bounce in the sender's INBOX.

## An admin is not an account

An admin signs in to the admin interface with a name, not an address, and has no mailbox. So the
first admin exists before any domain does, and adds the domains and then the accounts. Giving
somebody both means giving them two: an account to read mail, an admin to manage it.

## Mail to name+anything goes to name

Everything after the first `+` in the local part is a tag: `inbox+shop@reey.ru` is delivered to
`inbox@reey.ru`, and the tag is kept with the message for rules to use later. So an account's
own address can never contain a `+`.

## Addresses are lowercase

The whole address is lowercased on the way in, the local part as well as the domain. RFC 5321
allows a server to tell `Inbox@` from `inbox@`; no sender expects one to, and mail that arrives
spelled one way and bounces spelled another is worse than two addresses that cannot coexist.

## Mail for an address nobody has follows a default, which a domain can override

The default is set under Defaults in the admin interface, and a domain's own setting, in its
settings dialog, wins over it:

| setting | what happens | |
| --- | --- | --- |
| `reject` | refused while the sender is still connected | the default until changed |
| `drop` | accepted and thrown away | default or a domain's |
| `junk` | delivered to one chosen account, as junk | a domain's only |

**Junk is never the default.** It goes to one account, which is somebody at one domain, and the
junk of every domain landing in one person's mailbox is a choice to make per domain. The account
may be at any domain, and cannot be deleted while a domain sends it junk.

## An unknown address is refused while the sender is still connected

The recipient is resolved at `RCPT TO`, so a `reject` domain answers `550` there and then. Mail
accepted and bounced afterwards goes to whatever the envelope sender claimed to be, which for spam
is somebody innocent.

## Mail is only delivered to accounts here

Forwarding goes to another account, never outside. Forwarding means rewriting the envelope sender (SRS)
and, for senders that publish DMARC `p=reject`, signing what was forwarded (ARC); none of the
addresses here need it.
