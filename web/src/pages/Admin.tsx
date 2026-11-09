import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";

import {
  createAdmin,
  createDomain,
  createUser,
  deleteAdmin,
  deleteDomain,
  deleteUser,
  getMe,
  listAdmins,
  listDomains,
  listUsers,
  setAdminPassword,
  setUnknown,
  setUserPassword,
  signOut,
  type Admin as AdminT,
  type Domain,
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

  const failed = me.error ?? domains.error ?? users.error ?? admins.error;
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
      {me.data && domains.data && users.data && admins.data ? (
        <>
          <Domains domains={domains.data} users={users.data} />
          <Users users={users.data} />
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

  const choose = (unknown: Unknown) => {
    if (unknown !== "junk") {
      void act(() => setUnknown(domain.id, unknown, null));
    } else if (junkDefault) {
      void act(() => setUnknown(domain.id, "junk", junkDefault));
    }
  };

  return (
    <li className="flex flex-wrap items-center gap-x-4 gap-y-2 px-3 py-2">
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
              <option key={u} value={u} disabled={u === "junk" && !junkDefault}>
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
      <span className="ml-auto">
        <DeleteButton
          disabled={busy}
          onConfirm={() => void act(() => deleteDomain(domain.id))}
        />
      </span>
    </li>
  );
}

/** A password just set, shown until dismissed because nothing can show it again. */
type Shown = { who: string; password: string };

function Users({ users }: { users: User[] }) {
  const { act, busy, error } = useAct();
  const [shown, setShown] = useState<Shown | null>(null);
  const [address, setAddress] = useState("");
  const [password, setPassword] = useState("");

  const add = async (e: FormEvent) => {
    e.preventDefault();
    if (await act(() => createUser(address, password))) {
      setShown({ who: address.trim().toLowerCase(), password });
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
            <Row
              key={u.id}
              label={u.address}
              busy={busy}
              onPassword={async (pw) => {
                if (!(await act(() => setUserPassword(u.id, pw)))) return false;
                setShown({ who: u.address, password: pw });
                return true;
              }}
              onDelete={() => void act(() => deleteUser(u.id))}
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

/** Who may sign in here. A name and a password each, and no mailbox. */
function Admins({ admins, me }: { admins: AdminT[]; me: AdminT }) {
  const { act, busy, error } = useAct();
  const [shown, setShown] = useState<Shown | null>(null);
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");

  const add = async (e: FormEvent) => {
    e.preventDefault();
    if (await act(() => createAdmin(name, password))) {
      setShown({ who: name.trim().toLowerCase(), password });
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
              if (a.id !== me.id) setShown({ who: a.name, password: pw });
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

/** A user or an admin: its name, a new password, and a delete. */
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
      <code className="font-mono select-all">{shown.password}</code>
      <span className="text-muted">Copy it now: it is not shown again.</span>
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
