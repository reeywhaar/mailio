import { useEffect, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";

import {
  BUILT_IN,
  type Account,
  type DMARC,
  type Incoming,
  type Relay,
  type SPF,
  type Unknown,
} from "@app/api/admin";
import { ApiError, messageOf } from "@app/api/transport";
import { Button, type Size } from "@app/components/Button";
import { TextField } from "@app/components/TextField";
import { leaveFor } from "@app/leave";
import { generatePassword } from "@app/password";

/**
 * One press against the server, from a dialog: busy while it runs, the server's sentence when it
 * refuses, and every list read again when it succeeds, because one change can move another list.
 * A refusal leaves the dialog as it was, so nothing typed is lost.
 */
export function useSave() {
  const client = useQueryClient();
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const save = async (fn: () => Promise<unknown>): Promise<boolean> => {
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
  return { save, busy, error, clear: () => setError("") };
}

/**
 * Staged state for a dialog: a copy of what it edits, taken when it opens, so closing it is
 * forgetting the changes rather than undoing them.
 */
export function useDraft<T>(open: boolean, initial: () => T) {
  const [draft, setDraft] = useState<T>(initial);
  // Taken when it opens, not on every render: the lists are read again behind it after a save.
  useEffect(() => {
    if (open) setDraft(initial());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  return [draft, setDraft] as const;
}

export function Section({
  title,
  action,
  children,
}: {
  title: string;
  action?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="flex flex-col gap-3">
      <div className="flex items-center gap-3">
        <h2 className="caps text-muted">{title}</h2>
        <span className="flex-1" />
        {action}
      </div>
      {children}
    </section>
  );
}

/** A list of rows, each a button that opens what it summarizes. */
export function Rows({ children }: { children: ReactNode }) {
  return (
    <ul className="divide-y divide-line rounded-md border border-line bg-bg">
      {children}
    </ul>
  );
}

export function Row({
  title,
  aside,
  summary,
  onOpen,
}: {
  title: string;
  aside?: ReactNode;
  summary?: ReactNode;
  onOpen: () => void;
}) {
  return (
    <li>
      <button
        type="button"
        onClick={onOpen}
        className="flex w-full flex-col gap-0.5 px-3 py-2 text-left hover:bg-fill focus-visible:bg-fill focus-visible:outline-none"
      >
        <span className="font-medium">
          {title}
          {aside ? (
            <span className="ml-2 text-sm text-faint">{aside}</span>
          ) : null}
        </span>
        {summary ? <span className="text-sm text-muted">{summary}</span> : null}
      </button>
    </li>
  );
}

/** Two presses, the second within a few seconds, so a stray click deletes nothing. */
export function DeleteButton({
  disabled,
  onConfirm,
  size = "field",
  label = "Delete…",
}: {
  disabled: boolean;
  onConfirm: () => void;
  size?: Size;
  label?: string;
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
        size={size}
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
    <Button size={size} disabled={disabled} onClick={() => setArmed(true)}>
      {label}
    </Button>
  );
}

/**
 * Shown, not masked: it is typed or generated to be copied, to whoever it is for. Named by the
 * Field around it.
 */
export function PasswordField({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <span className="flex gap-2">
      <TextField
        placeholder="Password, at least 8 characters"
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

/** A login and its password, on screen once: only the hash is kept. */
export function ShownCredential({
  login,
  password,
}: {
  login: string;
  password: string;
}) {
  return (
    <div className="flex flex-col gap-2 text-sm">
      <div className="flex flex-col gap-1 rounded-md border border-line bg-surface px-3 py-2">
        <span className="text-muted">Login</span>
        <code className="font-mono break-all select-all">{login}</code>
        <span className="text-muted">Password</span>
        <code className="font-mono break-all select-all">{password}</code>
      </div>
      <p className="text-muted">
        Copy it now: it is not shown again. A lost one is replaced, never looked
        up.
      </p>
    </div>
  );
}

export const unknownLabels: Record<Unknown, string> = {
  reject: "Reject",
  drop: "Drop",
  junk: "Junk to…",
};

export const dmarcLabels: Record<DMARC, string> = {
  reject: "Refuse forgeries (reject)",
  quarantine: "Send forgeries to spam (quarantine)",
  none: "Deliver forgeries, report only (none)",
};

export const spfLabels: Record<SPF, string> = {
  "-all": "Other servers' mail fails (-all)",
  "~all": "Other servers' mail is suspect (~all)",
  "?all": "No word on other servers (?all)",
};

export const incomingLabels: Record<Incoming, string> = {
  keep: "Keep",
  reject: "Reject",
  forward: "Forward to…",
};

/** An account by id, as its address. */
export function addressOf(id: string | null, accounts: Account[]) {
  return (
    accounts.find((a) => a.id === id)?.address ?? "an account since deleted"
  );
}

/** How a relay choice reads: a relay's label, or built in. */
export function relayName(id: string | null, relays: Relay[]) {
  if (!id || id === BUILT_IN) return "built in";
  return relays.find((r) => r.id === id)?.label ?? "a relay since deleted";
}

/** What happens to unknown mail, as a summary says it. */
export function unknownSummary(
  unknown: Unknown,
  junkTo: string | null,
  accounts: Account[],
) {
  return unknown === "junk"
    ? `junk to ${addressOf(junkTo, accounts)}`
    : unknown;
}

/** A moment in the viewer's own zone, to the minute: last use is recorded to the hour. */
export function when(unix: number) {
  return new Date(unix * 1000).toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });
}
