// What the screenshots show: an admin, two domains, their accounts and aliases, a relay, and some
// mail in the accounts.
//
// Invented, all of it, for people out of Dan Brown's novels. Adding an account, an alias or a
// message is an entry here and nothing else: capture.mjs makes them through the admin API, and
// imports the mail as a Maildir, the way mail is brought over from the Postfix-based mailio.
//
// An account is { address, incoming?, forward?, credentials?, max_message_bytes?, relay?,
// send_as?, trash_days?, junk_days? }: credentials are labels, after the `default` every account
// starts with; forward is an account's address, for incoming "forward"; relay is a relay's label or
// "builtin"; send_as is the address of the account what it sends leaves as.
//
// A message is { to, folder?, from, subject, ago, body, unread?, attachment? }:
//   to          the account it is in; for Sent, the one that sent it
//   folder      INBOX unless it says Sent or Junk
//   ago         how long before `now` it arrived: 25m, 3h, 2d, 1w
//   attachment  { name, type, size }, filled with bytes of that size

// The moment the mail is dated from. Fixed, so a run on another day stores the same mail.
export const now = "2026-11-10T10:30:00Z";

// The mail server's name, which the admin interface shows, and every account's IMAP and SMTP host.
export const host = "mail.example.org";

// Who signs in to the admin interface, through the first-run link.
export const admin = {
  name: "robert",
  password: "a password nobody will ever type",
};

// Relays to send through, the first the default for every domain.
export const relays = [
  {
    label: "Relay",
    host: "smtp.example.net",
    port: 587,
    security: "starttls",
    username: "langdon@example.net",
    password: "a relay password nobody types",
  },
];

// Each with what it says that differs from the defaults.
export const domains = [
  {
    name: "example.org",
    unknown: "junk",
    unknown_account: "langdon@example.org",
    junk_days: 14,
  },
  { name: "example.net", dmarc: "quarantine", max_message_bytes: 25 << 20 },
];

export const accounts = [
  {
    address: "langdon@example.org",
    credentials: ["iPhone", "MacBook", "Harvard office"],
  },
  { address: "sophie@example.org", credentials: ["Phone"] },
  { address: "info@example.org", send_as: "langdon@example.org" },
  { address: "noreply@example.org", incoming: "reject", relay: "builtin" },
  { address: "katherine@example.net", trash_days: 7 },
  {
    address: "peter@example.net",
    incoming: "forward",
    forward: "katherine@example.net",
  },
];

// Another address of an account, at any domain.
export const aliases = [
  { address: "robert@example.org", of: "langdon@example.org" },
  { address: "symbols@example.net", of: "langdon@example.org" },
  { address: "neveu@example.org", of: "sophie@example.org" },
];

// The login tags the extra credentials are shown with, in place of the random ones each run makes,
// in the order the accounts and their credentials are listed.
export const tags = ["k3x9", "m7qa", "t2vd", "w8rn"];

const langdon = "Robert Langdon <langdon@example.org>";
const sophie = "Sophie Neveu <sophie@example.org>";
const katherine = "Katherine Solomon <katherine@example.net>";
const faukman = "Jonas Faukman <jonas@faukman.example>";
const teabing = "Leigh Teabing <leigh@teabing.example>";

export const messages = [
  {
    to: langdon,
    from: faukman,
    subject: "The new manuscript",
    ago: "2d",
    body: `Robert,

The draft is attached. Chapter nine still needs the footnotes we talked about, and the
publisher would like the whole thing by the end of the month.

Jonas`,
    attachment: {
      name: "manuscript.pdf",
      type: "application/pdf",
      size: 1_400_000,
    },
  },
  {
    to: langdon,
    folder: "Sent",
    from: langdon,
    subject: "Re: The new manuscript",
    ago: "1d",
    body: `Jonas,

Footnotes by Friday. The rest by the thirtieth, as promised.

Robert`,
  },
  {
    to: langdon,
    from: katherine,
    subject: "Your lecture at the institute",
    ago: "5h",
    unread: true,
    body: `Robert,

The hall is booked for Thursday at seven. Bring the slides on the seal; the students asked
for them twice.

Katherine`,
  },
  {
    to: langdon,
    from: sophie,
    subject: "Lunch on Saturday?",
    ago: "40m",
    unread: true,
    body: `Robert,

I am in Paris until Friday and back on Saturday. The usual place, at one?

Sophie`,
  },
  {
    to: langdon,
    folder: "Junk",
    from: "Prize Office <winner@lottery.example>",
    subject: "You have won a cryptex",
    ago: "3h",
    body: "Claim your prize today: send us your bank details.",
  },
  {
    to: sophie,
    from: teabing,
    subject: "The papers from the château",
    ago: "3d",
    body: `My dear Sophie,

The papers are catalogued at last. Come and see them when you are next in England.

Leigh`,
  },
  {
    to: sophie,
    from: langdon,
    subject: "Re: Lunch on Saturday?",
    ago: "20m",
    unread: true,
    body: "One o'clock. I will be the one with the tweed jacket.",
  },
  {
    to: katherine,
    from: "Peter Solomon <peter@example.net>",
    subject: "The lab schedule",
    ago: "1w",
    body: `Katherine,

The lab is yours for the next two weeks. I have told the guards.

Peter`,
  },
];
