import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";

import {
  addPassword,
  createAdmin,
  createDomain,
  createUser,
  deleteAdmin,
  deleteDomain,
  deletePassword,
  deleteRelay,
  deleteUser,
  getMail,
  getMe,
  getRelay,
  listAdmins,
  listDomains,
  listQueue,
  listUsers,
  retryQueue,
  setAdminPassword,
  setRelay,
  setUnknown,
  setIncoming,
  signOut,
  testRelay,
  type Admin as AdminT,
  type DNSRecord,
  type Domain,
  type Incoming,
  type Password,
  type MailStatus,
  type Queued,
  type Relay,
  type Security,
  type Unknown,
  type User,
} from "@app/api/admin";
import { ApiError, messageOf } from "@app/api/transport";
import { Button } from "@app/components/Button";
import { Select } from "@app/components/Field";
import { TextField } from "@app/components/TextField";
import { Wordmark } from "@app/components/Wordmark";
import { leaveFor } from "@app/leave";
import { generatePassword } from "@app/password";

/** Domains, the users at them, and who else may sign in here: everything an admin decides. */
export function Admin() {
  const me = useQuery({ queryKey: ["me"], queryFn: getMe });
  const domains = useQuery({ queryKey: ["domains"], queryFn: listDomains });
  const users = useQuery({ queryKey: ["users"], queryFn: listUsers });
  const admins = useQuery({ queryKey: ["admins"], queryFn: listAdmins });
  const mail = useQuery({ queryKey: ["mail"], queryFn: getMail });
  const relay = useQuery({ queryKey: ["relay"], queryFn: getRelay });
  const queue = useQuery({ queryKey: ["queue"], queryFn: listQueue });

  const failed =
    me.error ??
    domains.error ??
    users.error ??
    admins.error ??
    mail.error ??
    relay.error ??
    queue.error;
  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-10 px-4 py-6">
      <header className="flex items-center gap-4">
        <h1 className="text-xl font-semibold">
          <Wordmark />
        </h1>
        <span className="flex-1" />
        {me.data ? (
          <span className="truncate text-sm text-muted">{me.data.name}</span>
        ) : null}
        <Button
          variant="link"
          onClick={() => signOut().finally(() => leaveFor("/login"))}
        >
          Sign out
        </Button>
      </header>
      {failed && !(failed instanceof ApiError && failed.status === 401) ? (
        <p className="text-sm text-accent">{messageOf(failed)}</p>
      ) : null}
      {me.data &&
      domains.data &&
      users.data &&
      admins.data &&
      mail.data &&
      relay.data !== undefined &&
      queue.data ? (
        <>
          <MailServer mail={mail.data} />
          <Domains domains={domains.data} users={users.data} />
          <Users users={users.data} />
          <Sending relay={relay.data} queue={queue.data} />
          <Admins admins={admins.data} me={me.data} />
        </>
      ) : null}
    </div>
  );
}

/**
 * One press against the server: busy while it runs, the server's sentence when it refuses, and
 * every list read again when it succeeds, because one change can move another list.
 */
function useAct() {
  const client = useQueryClient();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const act = async (fn: () => Promise<unknown>): Promise<boolean> => {
    setBusy(true);
    setError("");
    try {
      await fn();
      await client.invalidateQueries();
      return true;
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) leaveFor("/login");
      else setError(messageOf(err));
      return false;
    } finally {
      setBusy(false);
    }
  };
  return { act, busy, error };
}

type Act = ReturnType<typeof useAct>["act"];

function Domains({ domains, users }: { domains: Domain[]; users: User[] }) {
  const { act, busy, error } = useAct();
  const [name, setName] = useState("");

  const add = async (e: FormEvent) => {
    e.preventDefault();
    if (await act(() => createDomain(name))) setName("");
  };

  return (
    <Section title="Domains">
      {domains.length ? (
        <ul className="divide-y divide-line rounded-md border border-line bg-bg">
          {domains.map((d) => (
            <DomainRow
              key={d.id}
              domain={d}
              users={users}
              act={act}
              busy={busy}
            />
          ))}
        </ul>
      ) : null}
      <form className="flex gap-2" onSubmit={add}>
        <TextField
          aria-label="Domain"
          placeholder="example.com"
          className="min-w-0 flex-1"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <Button type="submit" disabled={busy || !name.trim()}>
          Add domain
        </Button>
      </form>
      {error ? <p className="text-sm text-accent">{error}</p> : null}
    </Section>
  );
}

const unknownLabels: Record<Unknown, string> = {
  reject: "Reject",
  drop: "Drop",
  junk: "Junk to…",
};

function DomainRow({
  domain,
  users,
  act,
  busy,
}: {
  domain: Domain;
  users: User[];
  act: Act;
  busy: boolean;
}) {
  // Junk goes to somebody at the same domain when there is somebody, which is the likely reader.
  const junkDefault =
    users.find((u) => u.domain_id === domain.id)?.id ?? users[0]?.id;

  const [dns, setDNS] = useState(false);

  const choose = (unknown: Unknown) => {
    if (unknown !== "junk") {
      void act(() => setUnknown(domain.id, unknown, null));
    } else if (junkDefault) {
      void act(() => setUnknown(domain.id, "junk", junkDefault));
    }
  };

  return (
    <li className="flex flex-col gap-2 px-3 py-2">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <span className="font-medium">{domain.name}</span>
        <span className="flex flex-wrap items-center gap-2 text-sm text-muted">
          <label className="flex items-center gap-2">
            Unknown addresses
            <Select
              size="bar"
              value={domain.unknown}
              disabled={busy}
              onChange={(e) => choose(e.target.value as Unknown)}
            >
              {(Object.keys(unknownLabels) as Unknown[]).map((u) => (
                <option
                  key={u}
                  value={u}
                  disabled={u === "junk" && !junkDefault}
                >
                  {unknownLabels[u]}
                </option>
              ))}
            </Select>
          </label>
          {domain.unknown === "junk" ? (
            <Select
              size="bar"
              aria-label="Who gets the junk"
              value={domain.unknown_user_id ?? ""}
              disabled={busy}
              onChange={(e) =>
                void act(() => setUnknown(domain.id, "junk", e.target.value))
              }
            >
              {users.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.address}
                </option>
              ))}
            </Select>
          ) : null}
        </span>
        <span className="ml-auto flex items-center gap-x-4">
          <Button size="bar" onClick={() => setDNS(!dns)}>
            DNS…
          </Button>
          <DeleteButton
            disabled={busy}
            onConfirm={() => void act(() => deleteDomain(domain.id))}
          />
        </span>
      </div>
      {dns ? <Records records={domain.records} /> : null}
    </li>
  );
}

/** What a domain should publish, to copy into a DNS provider. */
function Records({ records }: { records: DNSRecord[] }) {
  return (
    <div className="flex flex-col gap-2 text-sm">
      {records.map((r) => (
        <div key={r.type + r.name + r.data} className="flex flex-col gap-0.5">
          <span className="text-muted">
            {r.type} <span className="font-mono text-fg">{r.name}</span>
            {r.kept ? null : (
              <span className="text-faint"> · if there is none</span>
            )}
          </span>
          <code className="font-mono text-xs break-all select-all">
            {r.data}
          </code>
        </div>
      ))}
    </div>
  );
}

const day = 24 * 60 * 60;

/** A moment in the viewer's own zone, to the minute: last use is recorded to the hour. */
function when(unix: number) {
  return new Date(unix * 1000).toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });
}

/** The server itself: its name, its certificate, and whether DNS looks after itself. */
function MailServer({ mail }: { mail: MailStatus }) {
  const cert = mail.certificate;
  const days = cert
    ? Math.floor((cert.not_after - Date.now() / 1000) / day)
    : 0;
  return (
    <Section title="Mail server">
      <div className="flex flex-col gap-1 text-sm">
        <span>
          <span className="font-mono">{mail.host}</span>
          <span className="text-muted"> · IMAP 993 · SMTP 465 and 587</span>
        </span>
        <span className="text-muted">
          {!cert
            ? "No certificate yet."
            : cert.issuer === "letsencrypt"
              ? `Certificate from Let's Encrypt, ${days} days left.`
              : "Self-signed certificate: mail clients will warn until Let's Encrypt issues one."}
        </span>
        <span className="text-muted">
          {mail.dns_api
            ? "DNS records are published through the DNS API."
            : "No DNS API: add each domain's records by hand (DNS…)."}
        </span>
      </div>
    </Section>
  );
}

/** Where outbound mail goes, and what is still waiting to. */
function Sending({ relay, queue }: { relay: Relay | null; queue: Queued[] }) {
  const { act, busy, error } = useAct();
  const [host, setHost] = useState(relay?.host ?? "");
  const [port, setPort] = useState(String(relay?.port ?? 587));
  const [security, setSecurity] = useState<Security>(
    relay?.security ?? "starttls",
  );
  const [username, setUsername] = useState(relay?.username ?? "");
  const [password, setPassword] = useState("");
  const [tested, setTested] = useState("");

  const save = async (e: FormEvent) => {
    e.preventDefault();
    setTested("");
    const ok = await act(() =>
      setRelay({ host, port: Number(port), security, username, password }),
    );
    if (ok) setPassword("");
  };

  return (
    <Section title="Sending">
      <p className="text-sm text-muted">
        {relay
          ? `Outbound mail goes through ${relay.host}.`
          : "No relay: mailio delivers to each recipient's server on port 25, which many hosting providers block."}
      </p>
      <form className="flex flex-col gap-2" onSubmit={save}>
        <div className="flex flex-wrap gap-2">
          <TextField
            aria-label="Relay host"
            placeholder="smtp.relay.example"
            autoComplete="off"
            className="min-w-48 flex-1"
            value={host}
            onChange={(e) => setHost(e.target.value)}
          />
          <TextField
            aria-label="Port"
            inputMode="numeric"
            className="w-20"
            value={port}
            onChange={(e) => setPort(e.target.value)}
          />
          <Select
            aria-label="Encryption"
            value={security}
            onChange={(e) => setSecurity(e.target.value as Security)}
          >
            <option value="starttls">STARTTLS</option>
            <option value="implicit">TLS</option>
          </Select>
        </div>
        <div className="flex flex-wrap gap-2">
          <TextField
            aria-label="Username"
            placeholder="Username"
            autoComplete="off"
            className="min-w-48 flex-1"
            value={username}
            onChange={(e) => setUsername(e.target.value)}
          />
          <TextField
            aria-label="Relay password"
            type="password"
            autoComplete="new-password"
            placeholder={
              relay?.has_password ? "Password: unchanged" : "Password"
            }
            className="min-w-48 flex-1"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Button type="submit" disabled={busy || !host.trim()}>
            Save relay
          </Button>
          {relay ? (
            <>
              <Button
                disabled={busy}
                onClick={() => {
                  setTested("");
                  void act(testRelay).then(
                    (ok) => ok && setTested("The relay accepted the sign-in."),
                  );
                }}
              >
                Test
              </Button>
              <DeleteButton
                disabled={busy}
                onConfirm={() => void act(deleteRelay)}
              />
            </>
          ) : null}
          {tested ? <span className="text-sm text-muted">{tested}</span> : null}
        </div>
      </form>
      {error ? <p className="text-sm text-accent">{error}</p> : null}
      {queue.length ? (
        <>
          <ul className="divide-y divide-line rounded-md border border-line bg-bg text-sm">
            {queue.map((q) => (
              <li key={q.id} className="flex flex-col gap-0.5 px-3 py-2">
                <span>
                  {q.sender} →{" "}
                  <span className="font-medium">{q.recipient}</span>
                </span>
                <span className="text-muted">
                  {q.attempts} {q.attempts === 1 ? "attempt" : "attempts"}
                  {q.last_error ? ` · ${q.last_error}` : ""}
                </span>
              </li>
            ))}
          </ul>
          <span>
            <Button disabled={busy} onClick={() => void act(retryQueue)}>
              Retry now
            </Button>
          </span>
        </>
      ) : (
        <p className="text-sm text-faint">Nothing waiting to be sent.</p>
      )}
    </Section>
  );
}

/** A password just made, on screen until dismissed: only its hash is kept, so nothing shows it again. */
type Shown = { who: string; login?: string; password: string; once?: boolean };

function Users({ users }: { users: User[] }) {
  const { act, busy, error } = useAct();
  const [shown, setShown] = useState<Shown | null>(null);
  const [address, setAddress] = useState("");
  const [password, setPassword] = useState("");

  const add = async (e: FormEvent) => {
    e.preventDefault();
    if (await act(() => createUser(address, password))) {
      const who = address.trim().toLowerCase();
      setShown({ who: `${who} (default)`, login: who, password, once: true });
      setAddress("");
      setPassword("");
    }
  };

  return (
    <Section title="Users">
      {shown ? (
        <ShownPassword shown={shown} onDone={() => setShown(null)} />
      ) : null}
      {users.length ? (
        <ul className="divide-y divide-line rounded-md border border-line bg-bg">
          {users.map((u) => (
            <UserRow
              key={u.id}
              user={u}
              users={users}
              act={act}
              busy={busy}
              onShown={setShown}
            />
          ))}
        </ul>
      ) : null}
      <form className="flex flex-wrap gap-2" onSubmit={add}>
        <TextField
          aria-label="Address"
          type="email"
          placeholder="name@example.com"
          autoComplete="off"
          className="min-w-48 flex-1"
          value={address}
          onChange={(e) => setAddress(e.target.value)}
        />
        <PasswordField value={password} onChange={setPassword} />
        <Button type="submit" disabled={busy || !address.trim() || !password}>
          Add user
        </Button>
      </form>
      {error ? <p className="text-sm text-accent">{error}</p> : null}
    </Section>
  );
}

const incomingLabels: Record<Incoming, string> = {
  keep: "Keep",
  reject: "Reject",
  forward: "Forward to…",
};

/** One user: what its mail does, its passwords, and a delete. */
function UserRow({
  user,
  users,
  act,
  busy,
  onShown,
}: {
  user: User;
  users: User[];
  act: Act;
  busy: boolean;
  onShown: (shown: Shown) => void;
}) {
  const [open, setOpen] = useState(false);
  const others = users.filter((u) => u.id !== user.id);

  const choose = (incoming: Incoming) => {
    if (incoming !== "forward") {
      void act(() => setIncoming(user.id, incoming, null));
    } else if (others[0]) {
      void act(() => setIncoming(user.id, "forward", others[0]!.id));
    }
  };

  return (
    <li className="flex flex-col gap-2 px-3 py-2">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <span className="font-medium">{user.address}</span>
        <span className="flex flex-wrap items-center gap-2 text-sm text-muted">
          <label className="flex items-center gap-2">
            Incoming mail
            <Select
              size="bar"
              value={user.incoming}
              disabled={busy}
              onChange={(e) => choose(e.target.value as Incoming)}
            >
              {(Object.keys(incomingLabels) as Incoming[]).map((i) => (
                <option
                  key={i}
                  value={i}
                  disabled={i === "forward" && !others.length}
                >
                  {incomingLabels[i]}
                </option>
              ))}
            </Select>
          </label>
          {user.incoming === "forward" ? (
            <Select
              size="bar"
              aria-label="Who gets the mail"
              value={user.forward_user_id ?? ""}
              disabled={busy}
              onChange={(e) =>
                void act(() => setIncoming(user.id, "forward", e.target.value))
              }
            >
              {others.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.address}
                </option>
              ))}
            </Select>
          ) : null}
        </span>
        {/* One group, so on a narrow screen the controls wrap together to the right. */}
        <span className="ml-auto flex items-center gap-x-4">
          <Button size="bar" onClick={() => setOpen(!open)}>
            Passwords ({user.passwords.length})…
          </Button>
          <DeleteButton
            disabled={busy}
            onConfirm={() => void act(() => deleteUser(user.id))}
          />
        </span>
      </div>
      {open ? (
        <Passwords user={user} act={act} busy={busy} onShown={onShown} />
      ) : null}
    </li>
  );
}

/**
 * A user's passwords. Each signs in with its own login, the address tagged, so one device's can
 * be deleted without the others; the default's login is the address itself.
 */
function Passwords({
  user,
  act,
  busy,
  onShown,
}: {
  user: User;
  act: Act;
  busy: boolean;
  onShown: (shown: Shown) => void;
}) {
  const [label, setLabel] = useState("");
  const [password, setPassword] = useState("");

  const add = async (e: FormEvent) => {
    e.preventDefault();
    let made: Password | undefined;
    const ok = await act(async () => {
      made = await addPassword(user.id, label, password);
    });
    if (ok && made) {
      onShown({
        who: `${user.address} (${made.label})`,
        login: made.login,
        password,
        once: true,
      });
      setLabel("");
      setPassword("");
    }
  };

  return (
    <div className="flex flex-col gap-2 border-t border-line pt-2 text-sm">
      {user.passwords.length ? (
        user.passwords.map((p) => (
          <div
            key={p.id}
            className="flex flex-wrap items-center gap-x-3 gap-y-1"
          >
            <span className="font-medium">{p.label}</span>
            <code className="font-mono text-xs text-muted">{p.login}</code>
            <span className="text-faint">
              {p.last_used_at ? `used ${when(p.last_used_at)}` : "never used"}
            </span>
            <span className="ml-auto flex items-center gap-x-3">
              <DeleteButton
                disabled={busy}
                onConfirm={() => void act(() => deletePassword(user.id, p.id))}
              />
            </span>
          </div>
        ))
      ) : (
        <p className="text-faint">
          No passwords: nobody can sign in as this user.
        </p>
      )}
      <form className="flex flex-wrap gap-2" onSubmit={add}>
        <TextField
          aria-label="Label"
          placeholder="Label, e.g. iPhone"
          autoComplete="off"
          className="w-40"
          value={label}
          onChange={(e) => setLabel(e.target.value)}
        />
        <PasswordField value={password} onChange={setPassword} />
        <Button type="submit" disabled={busy || !label.trim() || !password}>
          Add password
        </Button>
      </form>
    </div>
  );
}

/** Who may sign in here. A name and a password each, and no mailbox. */
function Admins({ admins, me }: { admins: AdminT[]; me: AdminT }) {
  const { act, busy, error } = useAct();
  const [shown, setShown] = useState<Shown | null>(null);
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");

  const add = async (e: FormEvent) => {
    e.preventDefault();
    if (await act(() => createAdmin(name, password))) {
      setShown({ who: name.trim().toLowerCase(), password, once: true });
      setName("");
      setPassword("");
    }
  };

  return (
    <Section title="Admins">
      {shown ? (
        <ShownPassword shown={shown} onDone={() => setShown(null)} />
      ) : null}
      <ul className="divide-y divide-line rounded-md border border-line bg-bg">
        {admins.map((a) => (
          <Row
            key={a.id}
            label={a.name}
            you={a.id === me.id}
            busy={busy}
            onPassword={async (pw) => {
              if (!(await act(() => setAdminPassword(a.id, pw)))) return false;
              // Your own you just typed; somebody else's is theirs to be handed.
              if (a.id !== me.id) {
                setShown({ who: a.name, password: pw, once: true });
              }
              return true;
            }}
            onDelete={() => void act(() => deleteAdmin(a.id))}
          />
        ))}
      </ul>
      <form className="flex flex-wrap gap-2" onSubmit={add}>
        <TextField
          aria-label="Name"
          placeholder="Name"
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          className="min-w-48 flex-1"
          value={name}
          onChange={(e) => setName(e.target.value)}
        />
        <PasswordField value={password} onChange={setPassword} />
        <Button type="submit" disabled={busy || !name.trim() || !password}>
          Add admin
        </Button>
      </form>
      {error ? <p className="text-sm text-accent">{error}</p> : null}
    </Section>
  );
}

/** An admin: a name, a new password, and a delete. */
function Row({
  label,
  you = false,
  busy,
  onPassword,
  onDelete,
}: {
  label: string;
  you?: boolean;
  busy: boolean;
  onPassword: (password: string) => Promise<boolean>;
  onDelete: () => void;
}) {
  const [editing, setEditing] = useState(false);
  const [password, setPassword] = useState("");

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (await onPassword(password)) {
      setEditing(false);
      setPassword("");
    }
  };

  return (
    <li className="flex flex-col gap-2 px-3 py-2">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <span className="font-medium">
          {label}
          {you ? <span className="ml-2 text-sm text-faint">you</span> : null}
        </span>
        {/* One group, so on a narrow screen the controls wrap together to the right. */}
        <span className="ml-auto flex items-center gap-x-4">
          <Button
            size="bar"
            disabled={busy}
            onClick={() => setEditing(!editing)}
          >
            Password…
          </Button>
          <DeleteButton disabled={busy} onConfirm={onDelete} />
        </span>
      </div>
      {editing ? (
        <form className="flex flex-wrap gap-2" onSubmit={save}>
          <PasswordField value={password} onChange={setPassword} />
          <Button type="submit" disabled={busy || !password}>
            Save
          </Button>
          <Button variant="link" onClick={() => setEditing(false)}>
            Cancel
          </Button>
        </form>
      ) : null}
    </li>
  );
}

/**
 * Shown, not masked: it is typed or generated to be copied, into a mail client or to whoever it
 * is for.
 */
function PasswordField({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <span className="flex min-w-48 flex-1 gap-2">
      <TextField
        aria-label="Password"
        placeholder="Password"
        autoComplete="off"
        spellCheck={false}
        className="min-w-0 flex-1 font-mono"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      />
      <Button onClick={() => onChange(generatePassword())}>Generate</Button>
    </span>
  );
}

function ShownPassword({
  shown,
  onDone,
}: {
  shown: Shown;
  onDone: () => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-md border border-line bg-surface px-3 py-2 text-sm">
      <span>
        Password for <span className="font-medium">{shown.who}</span>:
      </span>
      {shown.login ? (
        <span>
          <span className="text-muted">login </span>
          <code className="font-mono select-all">{shown.login}</code>
        </span>
      ) : null}
      <span>
        {shown.login ? <span className="text-muted">password </span> : null}
        <code className="font-mono select-all">{shown.password}</code>
      </span>
      {shown.once ? (
        <span className="text-muted">Copy it now: it is not shown again.</span>
      ) : null}
      <span className="flex-1" />
      <Button variant="link" onClick={onDone}>
        Done
      </Button>
    </div>
  );
}

/** Two presses, the second within a few seconds, so a stray click deletes nothing. */
function DeleteButton({
  disabled,
  onConfirm,
}: {
  disabled: boolean;
  onConfirm: () => void;
}) {
  const [armed, setArmed] = useState(false);
  useEffect(() => {
    if (!armed) return;
    const t = setTimeout(() => setArmed(false), 4000);
    return () => clearTimeout(t);
  }, [armed]);

  if (armed) {
    return (
      <Button
        variant="danger"
        size="bar"
        disabled={disabled}
        onClick={() => {
          setArmed(false);
          onConfirm();
        }}
      >
        Really delete?
      </Button>
    );
  }
  return (
    <Button size="bar" disabled={disabled} onClick={() => setArmed(true)}>
      Delete…
    </Button>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <h2 className="caps text-muted">{title}</h2>
      {children}
    </section>
  );
}
