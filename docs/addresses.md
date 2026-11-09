# Addresses

Where mail sent to an address goes. Decided once here; [internal/address](../internal/address)
and `store.Resolve` are the code.

## A user is an address and its passwords

A user is what mail is delivered to. There is no list of extra addresses on a user: an address
that should receive mail is a user.

## Each password has its own login

A user has passwords, each with a label, one per phone or laptop. Each signs in with its own
login, the address with a tag: `inbox+k3x9@reey.ru` and that password, and no other password. The
one the user is created with is labelled `default`, and its login is the plain address. Any of
them can be deleted, `default` too; with none left, the user still receives mail and nobody signs
in as it.

**A login names one password**, so a sign-in checks one hash however many the user has, a wrong
guess costs the same, and the log can say which password signed in. A login cannot clash with
another user: no user's address has a `+`, and the name before it is always the login's own user.
Mail sent to a login address arrives like any tagged mail.

**A password is shown once, when it is made, and only its hash is kept.** A device that needs one
gets its own, and a lost one is deleted and replaced, never looked up. Nor is one changed in
place: a device still using the old one stops, rather than silently holding a password that is no
longer its.

## A user keeps, refuses or forwards its mail

| incoming | mail sent to the user |
| --- | --- |
| `keep` | is delivered to its own mailboxes |
| `reject` | is refused at `RCPT TO`, `550 5.2.1`: an address that only sends, such as `noreply@` |
| `forward` | is delivered to another user's INBOX instead |

A forward follows the target's own setting, so forwarding into an address that rejects rejects.
Saving one that would come back round is refused, and a user others forward to cannot be deleted
until they stop.

## An admin is not a user

An admin signs in to the admin interface with a name, not an address, and has no mailbox. So the
first admin exists before any domain does, and adds the domains and then the users. Giving
somebody both means giving them two: a user to read mail, an admin to manage it.

## Mail to name+anything goes to name

Everything after the first `+` in the local part is a tag: `inbox+shop@reey.ru` is delivered to
`inbox@reey.ru`, and the tag is kept with the message for rules to use later. So a user's own
address can never contain a `+`.

## Addresses are lowercase

The whole address is lowercased on the way in, the local part as well as the domain. RFC 5321
allows a server to tell `Inbox@` from `inbox@`; no sender expects one to, and mail that arrives
spelled one way and bounces spelled another is worse than two addresses that cannot coexist.

## Each domain decides what happens to mail for an address nobody has

One of three, set per domain in the admin interface:

| setting | what happens |
| --- | --- |
| `reject` | refused while the sender is still connected. The default |
| `drop` | accepted and thrown away |
| `junk` | delivered to one chosen user, as junk |

The `junk` user may be at any domain. A user who gets a domain's junk cannot be deleted until
the domain is set to something else.

## An unknown address is refused while the sender is still connected

The recipient is resolved at `RCPT TO`, so a `reject` domain answers `550` there and then. Mail
accepted and bounced afterwards goes to whatever the envelope sender claimed to be, which for spam
is somebody innocent.

## Mail is only delivered to users here

Forwarding goes to another user, never outside. Forwarding means rewriting the envelope sender (SRS)
and, for senders that publish DMARC `p=reject`, signing what was forwarded (ARC); none of the
addresses here need it.
