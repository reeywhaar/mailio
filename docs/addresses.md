# Addresses

Where mail sent to an address goes. Decided once here; [internal/address](../internal/address)
and `store.Resolve` are the code.

## A user is an address and a password

A user is what mail is delivered to, and the same address and password sign in to read it and to
send as it. There is no list of extra addresses on a user: an address that should receive mail is
a user.

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

There is no forwarding to outside addresses. Forwarding means rewriting the envelope sender (SRS)
and, for senders that publish DMARC `p=reject`, signing what was forwarded (ARC); none of the
addresses here need it.
