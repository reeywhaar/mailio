import { Fragment, useEffect, useRef, useState, type ReactNode } from "react";
import { useQueryClient } from "@tanstack/react-query";

import {
  BUILT_IN,
  toAccount,
  type Account,
  type DMARC,
  type DNSRecord,
  type DNSStatus,
  type Domain,
  type Incoming,
  type Relay,
  type SPF,
  type Unknown,
} from "@app/api/admin";
import { ApiError, messageOf } from "@app/api/transport";
import { Button, type Size } from "@app/components/Button";
import { CopyBlock } from "@app/components/CopyBlock";
import { Dialog } from "@app/components/Dialog";
import { Field } from "@app/components/Field";
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
  nested = false,
  onOpen,
}: {
  title: string;
  aside?: ReactNode;
  summary?: ReactNode;
  /** Under the row before it, as an account is under its domain. */
  nested?: boolean;
  onOpen: () => void;
}) {
  return (
    <li>
      <button
        type="button"
        onClick={onOpen}
        className={`flex w-full flex-col gap-0.5 py-2 pr-3 text-left hover:bg-fill focus-visible:bg-fill focus-visible:outline-none ${nested ? "pl-8" : "pl-3"}`}
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

/**
 * One entry of a list inside a dialog: what it is on the left, its commands at its right. On a
 * phone the commands go on a line of their own, rather than wrapping wherever they run out.
 */
export function Item({
  title,
  meta,
  detail,
  actions,
  children,
}: {
  title: string;
  /** Beside the title, quieter. */
  meta?: ReactNode;
  /** Under the title, in mono: a login, a host. */
  detail?: string;
  actions: ReactNode;
  children?: ReactNode;
}) {
  return (
    <li className="flex flex-col gap-2 px-3 py-2 sm:flex-row sm:items-center sm:gap-3">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="flex flex-wrap items-baseline gap-x-2">
          <span className="font-medium">{title}</span>
          {meta ? <span className="text-faint">{meta}</span> : null}
        </span>
        {detail ? (
          <code className="font-mono text-xs break-all text-muted">
            {detail}
          </code>
        ) : null}
        {children}
      </div>
      <span className="flex shrink-0 items-center gap-2">{actions}</span>
    </li>
  );
}

/**
 * Whether a button that has just appeared should still ignore a press: a stuttered double click
 * lands its second click on whatever replaced the first button, and a Done there throws away a
 * password shown once.
 */
export function useSettling(shown: unknown, ms = 500) {
  const since = useRef(0);
  useEffect(() => {
    since.current = performance.now();
  }, [shown]);
  return () => performance.now() - since.current < ms;
}

/**
 * "Delete…", which asks first in a dialog of its own: what goes, and what goes with it. A
 * refusal stays there with the server's sentence.
 */
export function DeleteButton({
  what,
  facts,
  note,
  onDelete,
  onDeleted,
  disabled,
  size = "field",
  label = "Delete…",
}: {
  /** What is deleted, for the title: "credential". */
  what: string;
  facts: [string, ReactNode][];
  note?: string;
  onDelete: () => Promise<unknown>;
  onDeleted?: () => void;
  disabled?: boolean;
  size?: Size;
  label?: string;
}) {
  const [open, setOpen] = useState(false);
  const { save, busy, error, clear } = useSave();
  const settling = useSettling(open);
  const close = () => {
    clear();
    setOpen(false);
  };
  const confirm = async () => {
    if (settling()) return;
    if (await save(onDelete)) {
      setOpen(false);
      onDeleted?.();
    }
  };
  return (
    <>
      <Button size={size} disabled={disabled} onClick={() => setOpen(true)}>
        {label}
      </Button>
      <Dialog
        open={open}
        onClose={close}
        title={`Delete ${what}`}
        footer={
          <>
            <Button onClick={close}>Cancel</Button>
            <Button
              variant="danger"
              disabled={busy}
              onClick={() => void confirm()}
            >
              Delete
            </Button>
          </>
        }
      >
        <div className="flex flex-col gap-3">
          <Facts items={facts} />
          {note ? <p className="text-sm text-muted">{note}</p> : null}
          {error ? <p className="text-sm text-accent">{error}</p> : null}
        </div>
      </Dialog>
    </>
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

const MB = 1 << 20;

/** A size as the interface writes it: 50 MB. */
export const megabytes = (bytes: number) =>
  `${Math.round((bytes / MB) * 10) / 10} MB`;

/** A largest message as its field holds it: MB, and "" for none of its own. */
export const mbText = (bytes: number | null) =>
  bytes ? String(Math.round((bytes / MB) * 10) / 10) : "";

/**
 * A largest-message field's text as bytes: 0 for empty, which follows the one above, and NaN for
 * anything but a number. The server says the range.
 */
export function toBytes(text: string) {
  const t = text.trim().replace(",", ".");
  if (!t) return 0;
  const n = Number(t);
  return Number.isFinite(n) && n > 0 ? Math.round(n * MB) : NaN;
}

/** Whether a largest-message field holds something to send. */
export const limitTyped = (text: string) => !Number.isNaN(toBytes(text));

/**
 * The largest message, in MB. With `inherited`, empty follows it: the default for a domain, the
 * domain's for an account.
 */
export function LimitField({
  value,
  onChange,
  inherited,
  hint,
}: {
  value: string;
  onChange: (text: string) => void;
  inherited?: { from: string; bytes: number };
  hint?: string;
}) {
  return (
    <Field
      label="Largest message"
      hint={
        inherited
          ? `Empty: ${inherited.from}, ${megabytes(inherited.bytes)}.`
          : hint
      }
      error={limitTyped(value) ? undefined : "A number of MB."}
    >
      <span className="flex items-center gap-2">
        <TextField
          inputMode="decimal"
          autoComplete="off"
          aria-label="Largest message"
          placeholder={inherited ? mbText(inherited.bytes) : undefined}
          className="w-28"
          value={value}
          onChange={(e) => onChange(e.target.value)}
        />
        <span className="text-muted">MB</span>
      </span>
    </Field>
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
    <div className="flex flex-col gap-2">
      <CopyBlock
        rows={[
          { label: "Login", value: login },
          { label: "Password", value: password },
        ]}
      />
      <p className="text-sm text-muted">
        Shown once: only its hash is kept. A lost one is replaced.
      </p>
    </div>
  );
}

/** Labels and their values, as a section shows what it holds. */
export function Facts({ items }: { items: [string, ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
      {items.map(([label, value]) => (
        <Fragment key={label}>
          <dt className="text-muted">{label}</dt>
          <dd className="min-w-0 break-words">{value}</dd>
        </Fragment>
      ))}
    </dl>
  );
}

/** A row's summary: each setting a label and its value, marked where it is the default's. */
export function Summary({
  parts,
}: {
  parts: { label: string; value: ReactNode; inherited?: boolean }[];
}) {
  return (
    <span className="flex flex-wrap gap-x-3 gap-y-0.5">
      {parts.map((p) => (
        <span key={p.label}>
          {p.label}: <span className="text-fg">{p.value}</span>
          {p.inherited ? <Tag>default</Tag> : null}
        </span>
      ))}
    </span>
  );
}

export const unknownLabels: Record<Unknown, string> = {
  reject: "Reject",
  drop: "Drop",
  forward: "Forward",
  junk: "Junk",
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
  forward: "Forward",
};

export const dnsStatusLabels: Record<DNSStatus, string> = {
  ok: "OK",
  missing: "Missing",
  different: "Different",
  several: "Several records",
  failed: "Couldn't check",
};

/** A record status as a word, colored by whether it needs fixing. */
export function DNSStatusMark({ record }: { record: DNSRecord }) {
  const { status } = record;
  if (!status) return <span className="text-faint">Not checked</span>;
  if (status === "failed") return <FailedCheck record={record} />;
  return (
    <span className={status === "ok" ? "text-ok" : "text-accent"}>
      {dnsStatusLabels[status]}
    </span>
  );
}

/** "Couldn't check", which opens why. */
function FailedCheck({ record }: { record: DNSRecord }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        className="text-accent underline decoration-dotted underline-offset-2 hover:decoration-solid"
      >
        {dnsStatusLabels.failed}
      </button>
      <Dialog
        open={open}
        onClose={() => setOpen(false)}
        title={dnsStatusLabels.failed}
        footer={<Button onClick={() => setOpen(false)}>Close</Button>}
      >
        <div className="flex flex-col gap-3 text-sm">
          <Facts
            items={[
              ["Type", record.type],
              [
                "Name",
                <span className="font-mono break-all">{record.name}</span>,
              ],
            ]}
          />
          <p className="break-words">
            {record.error ?? "No reason was given."}
          </p>
        </div>
      </Dialog>
    </>
  );
}

/** A domain's records at once: how many need fixing, or why that is not known. */
export function DNSSummary({ domain }: { domain: Domain }) {
  const statuses = domain.records.map((r) => r.status);
  const problems = statuses.filter(
    (s) => s === "missing" || s === "different" || s === "several",
  ).length;
  if (problems) {
    return (
      <span className="text-accent">
        {problems} {problems === 1 ? "problem" : "problems"}
      </span>
    );
  }
  if (statuses.includes("failed")) {
    return <span className="text-accent">{dnsStatusLabels.failed}</span>;
  }
  if (statuses.includes(null)) return <>Not checked</>;
  return <span className="text-ok">OK</span>;
}

/** Whether mail can be sent on to an account: only one that keeps its own takes it. */
export const keepsMail = (a: Account) => a.incoming === "keep";

/** An account as a picker offers it, marked where it would not keep what is sent on. */
export function accountOption(a: Account) {
  if (a.kind === "alias") return `${a.address} · alias`;
  if (a.incoming === "reject") return `${a.address} · no-reply`;
  if (a.incoming === "forward") return `${a.address} · forwards`;
  return a.address;
}

/** A small word beside a value: quiet for "default", red for what loses mail. */
export function Tag({
  children,
  bad = false,
}: {
  children: ReactNode;
  bad?: boolean;
}) {
  return (
    <span
      className={`ml-1.5 rounded bg-fill px-1 py-px text-xs ${bad ? "text-accent" : "text-faint"}`}
    >
      {children}
    </span>
  );
}

/**
 * A setting and the account its mail goes to: "Forward → b@example.org". Marked when that account
 * would not keep it, which only data saved before the rule can show.
 */
export function Toward({
  label,
  to,
  accounts,
}: {
  label: string;
  to: string | null;
  accounts: Account[];
}) {
  const target = accounts.find((a) => a.id === to);
  return (
    <>
      {label}
      <span className="mx-1.5 text-faint">→</span>
      {target?.address ?? "Deleted account"}
      {target?.incoming === "reject" ? <Tag bad>no-reply</Tag> : null}
      {target?.incoming === "forward" ? <Tag bad>forwards</Tag> : null}
    </>
  );
}

/**
 * Who sends mail on to an account: its aliases, those forwarding to it, and domains whose
 * catch-all it gets.
 */
export function sendersTo(id: string, accounts: Account[], domains: Domain[]) {
  return [
    ...accounts
      .filter((a) => a.incoming === "forward" && a.forward_account_id === id)
      .map((a) => ({
        name: a.address,
        sends: a.kind === "alias" ? "alias" : "forward",
      })),
    ...domains
      .filter((d) => d.unknown_account_id === id)
      .map((d) => ({
        name: d.name,
        sends: d.unknown === "junk" ? "catch-all, junk" : "catch-all",
      })),
  ];
}

/**
 * Who sends mail on to an account, listed under its incoming mail, whose other choices are off
 * while they do. Red for an account saved before the rule, set to something else already.
 */
export function Senders({
  senders,
  blocked,
}: {
  senders: { name: string; sends: string }[];
  blocked: boolean;
}) {
  const tone = blocked
    ? "border-accent/40 bg-accent/10 text-accent"
    : "border-warn/40 bg-warn/10 text-warn";
  return (
    <div
      className={`flex flex-col gap-1 rounded-md border px-3 py-2 text-sm ${tone}`}
    >
      <span>
        {blocked
          ? "Mail is sent on here, so it keeps its mail. Change these first:"
          : "Keeps its mail while mail is sent on here from:"}
      </span>
      <ul className="flex flex-wrap gap-x-3 gap-y-1 text-fg">
        {senders.map((s) => (
          <li key={s.sends + s.name}>
            {s.name}
            <Tag>{s.sends}</Tag>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** An account by id, as its address. */
export function addressOf(id: string | null, accounts: Account[]) {
  return accounts.find((a) => a.id === id)?.address ?? "Deleted account";
}

/** A relay choice by name: a relay's label, or built-in. */
export function relayName(id: string | null, relays: Relay[]) {
  if (!id || id === BUILT_IN) return "Built-in";
  return relays.find((r) => r.id === id)?.label ?? "Deleted relay";
}

/** A catch-all, with who gets the mail when it goes to an account. */
export function unknownSummary(
  unknown: Unknown,
  to: string | null,
  accounts: Account[],
) {
  return toAccount(unknown) ? (
    <Toward label={unknownLabels[unknown]} to={to} accounts={accounts} />
  ) : (
    unknownLabels[unknown]
  );
}

/** A moment in the viewer's own zone, to the minute: last use is recorded to the hour. */
export function when(unix: number) {
  return new Date(unix * 1000).toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });
}
