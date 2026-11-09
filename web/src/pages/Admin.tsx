import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";

import {
  createDomain,
  createUser,
  deleteDomain,
  deleteUser,
  getMe,
  listDomains,
  listUsers,
  setUnknown,
  signOut,
  updateUser,
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

/** Domains and the users at them: everything an admin decides. */
export function Admin() {
  const me = useQuery({ queryKey: ["me"], queryFn: getMe });
  const domains = useQuery({ queryKey: ["domains"], queryFn: listDomains });
  const users = useQuery({ queryKey: ["users"], queryFn: listUsers });

  const failed = me.error ?? domains.error ?? users.error;
  return (
    <div className="mx-auto flex w-full max-w-3xl flex-col gap-10 px-4 py-6">
      <header className="flex items-center gap-4">
        <h1 className="text-xl font-semibold">
          <Wordmark />
        </h1>
        <span className="flex-1" />
        {me.data ? (
          <span className="truncate text-sm text-muted">{me.data.address}</span>
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
      {me.data && domains.data && users.data ? (
        <>
          <Domains domains={domains.data} users={users.data} />
          <Users users={users.data} me={me.data} />
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
type Shown = { address: string; password: string };

function Users({ users, me }: { users: User[]; me: User }) {
  const { act, busy, error } = useAct();
  const [shown, setShown] = useState<Shown | null>(null);
  const [address, setAddress] = useState("");
  const [password, setPassword] = useState("");
  const [admin, setAdmin] = useState(false);

  const add = async (e: FormEvent) => {
    e.preventDefault();
    if (await act(() => createUser(address, password, admin))) {
      setShown({ address: address.trim().toLowerCase(), password });
      setAddress("");
      setPassword("");
      setAdmin(false);
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
              you={u.id === me.id}
              act={act}
              busy={busy}
              onPassword={setShown}
            />
          ))}
        </ul>
      ) : null}
      <form className="flex flex-col gap-2" onSubmit={add}>
        <div className="flex flex-wrap gap-2">
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
        </div>
        <div className="flex items-center gap-4">
          <label className="flex items-center gap-2 text-sm text-muted">
            <input
              type="checkbox"
              checked={admin}
              onChange={(e) => setAdmin(e.target.checked)}
            />
            Admin
          </label>
          <span className="flex-1" />
          <Button type="submit" disabled={busy || !address.trim() || !password}>
            Add user
          </Button>
        </div>
      </form>
      {error ? <p className="text-sm text-accent">{error}</p> : null}
    </Section>
  );
}

function UserRow({
  user,
  you,
  act,
  busy,
  onPassword,
}: {
  user: User;
  you: boolean;
  act: Act;
  busy: boolean;
  onPassword: (shown: Shown) => void;
}) {
  const [editing, setEditing] = useState(false);
  const [password, setPassword] = useState("");

  const save = async (e: FormEvent) => {
    e.preventDefault();
    if (await act(() => updateUser(user.id, { password }))) {
      setEditing(false);
      setPassword("");
      // Your own sessions end with your password, so the page is about to leave anyway.
      if (you) leaveFor("/login");
      else onPassword({ address: user.address, password });
    }
  };

  return (
    <li className="flex flex-col gap-2 px-3 py-2">
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <span className="font-medium">
          {user.address}
          {you ? <span className="ml-2 text-sm text-faint">you</span> : null}
        </span>
        {/* One group, so on a narrow screen the controls wrap together to the right. */}
        <span className="ml-auto flex items-center gap-x-4">
          <label className="flex items-center gap-2 text-sm text-muted">
            <input
              type="checkbox"
              checked={user.admin}
              disabled={busy}
              onChange={(e) => {
                const admin = e.target.checked;
                void act(() => updateUser(user.id, { admin })).then((ok) => {
                  if (ok && you && !admin) leaveFor("/login");
                });
              }}
            />
            Admin
          </label>
          <Button
            size="bar"
            disabled={busy}
            onClick={() => setEditing(!editing)}
          >
            Password…
          </Button>
          <DeleteButton
            disabled={busy}
            onConfirm={() => void act(() => deleteUser(user.id))}
          />
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
 * Shown, not masked: it is somebody else's password, typed or generated to be copied into their
 * mail client.
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
        Password for <span className="font-medium">{shown.address}</span>:
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
