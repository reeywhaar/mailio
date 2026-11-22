import { useState, type FormEvent } from "react";
import { useQuery } from "@tanstack/react-query";

import {
  changePassword,
  createAdmin,
  deleteAdmin,
  listSessions,
  revokeOtherSessions,
  revokeSession,
  setAdminPassword,
  type Admin,
} from "@app/api/admin";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { Field } from "@app/components/Field";
import { TextField } from "@app/components/TextField";
import { leaveFor } from "@app/leave";
import {
  DeleteButton,
  Item,
  PasswordField,
  Section,
  useDraft,
  useSave,
  when,
} from "@app/pages/admin/shared";

/** What the server refuses anything shorter than. */
const PASSWORD_MIN = 8;

/**
 * Who may sign in here: a name and a password each, and never an account. Each row has its own
 * commands: another admin a new password or a delete, you a delete, your own password being the
 * Password section's.
 */
export function Admins({ admins, me }: { admins: Admin[]; me: Admin }) {
  const [adding, setAdding] = useState(false);
  const [resetting, setResetting] = useState<string | null>(null);
  const admin = admins.find((a) => a.id === resetting);
  return (
    <Section
      title="Admins"
      action={<Button onClick={() => setAdding(true)}>Add admin</Button>}
    >
      <ul
        aria-label="Admins"
        className="divide-y divide-line rounded-md border border-line bg-bg text-sm"
      >
        {admins.map((a) => {
          const you = a.id === me.id;
          return (
            <Item
              key={a.id}
              title={a.name}
              meta={you ? "you" : undefined}
              actions={
                <>
                  {you ? null : (
                    <Button size="bar" onClick={() => setResetting(a.id)}>
                      New password…
                    </Button>
                  )}
                  <DeleteButton
                    size="bar"
                    what="admin"
                    facts={[["Name", a.name]]}
                    note={
                      you
                        ? "That is you: you are signed out."
                        : "They are signed out everywhere."
                    }
                    onDelete={() => deleteAdmin(a.id)}
                    onDeleted={you ? () => leaveFor("/login") : undefined}
                  />
                </>
              }
            />
          );
        })}
      </ul>
      <AddAdminDialog open={adding} onClose={() => setAdding(false)} />
      {admin ? (
        <AdminDialog open admin={admin} onClose={() => setResetting(null)} />
      ) : null}
    </Section>
  );
}

function AddAdminDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const [draft, setDraft] = useDraft(open, () => ({ name: "", password: "" }));
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (await save(() => createAdmin(draft.name, draft.password))) onClose();
  };
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Add an admin"
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            form="add-admin"
            variant="solid"
            disabled={busy || !draft.name.trim() || !draft.password}
          >
            Add
          </Button>
        </>
      }
    >
      <form id="add-admin" className="flex flex-col gap-3" onSubmit={submit}>
        <Field label="Name">
          <TextField
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            data-autofocus
            value={draft.name}
            onChange={(e) => setDraft({ ...draft, name: e.target.value })}
          />
        </Field>
        <Field label="Password">
          <PasswordField
            value={draft.password}
            onChange={(password) => setDraft({ ...draft, password })}
          />
        </Field>
        <p className="text-sm text-muted">
          Hand the name and password to whoever it is for: only the hash is
          kept.
        </p>
        {error ? <p className="text-sm text-accent">{error}</p> : null}
      </form>
    </Dialog>
  );
}

/** Another admin: a new password for them, held until Save. */
function AdminDialog({
  open,
  admin,
  onClose,
}: {
  open: boolean;
  admin: Admin;
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const [password, setPassword] = useDraft(open, () => "");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (await save(() => setAdminPassword(admin.id, password))) onClose();
  };
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={admin.name}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            form="admin"
            variant="solid"
            disabled={busy || !password}
          >
            Save
          </Button>
        </>
      }
    >
      <form id="admin" className="flex flex-col gap-3" onSubmit={submit}>
        <Field label="New password">
          <PasswordField value={password} onChange={setPassword} />
        </Field>
        <p className="text-sm text-muted">They are signed out everywhere.</p>
        {error ? <p className="text-sm text-accent">{error}</p> : null}
      </form>
    </Dialog>
  );
}

/** Your own password, changed in its dialog: a section of the page under Admins. */
export function Password() {
  const [open, setOpen] = useState(false);
  return (
    <Section
      title="Password"
      action={<Button onClick={() => setOpen(true)}>Change password…</Button>}
    >
      <p className="text-sm text-muted">
        Changing it signs out every other browser.
      </p>
      <PasswordDialog open={open} onClose={() => setOpen(false)} />
    </Section>
  );
}

/**
 * A new password for yourself, the current one first, held until Save. The current one is asked
 * for because being signed in is not knowing it: that is what keeps a borrowed session from
 * becoming a taken admin.
 */
function PasswordDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const [draft, setDraft] = useDraft(open, () => ({
    current: "",
    password: "",
    again: "",
    problem: "",
  }));
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    // Caught here because the server cannot: it gets one new password, not the two typed.
    const problem =
      draft.password.length < PASSWORD_MIN
        ? `A password needs at least ${PASSWORD_MIN} characters.`
        : draft.password !== draft.again
          ? "The two new passwords do not match."
          : "";
    setDraft({ ...draft, problem });
    if (problem) return;
    const changed = await save(() =>
      changePassword(draft.current, draft.password),
    );
    if (changed) onClose();
  };
  const password = (
    key: "current" | "password" | "again",
    label: string,
    autoComplete: string,
  ) => (
    <Field label={label}>
      <TextField
        type="password"
        autoComplete={autoComplete}
        className="w-full"
        data-autofocus={key === "current" ? true : undefined}
        value={draft[key]}
        onChange={(e) => setDraft({ ...draft, [key]: e.target.value })}
      />
    </Field>
  );
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Change password"
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            form="password"
            variant="solid"
            disabled={busy || !draft.current || !draft.password || !draft.again}
          >
            Save
          </Button>
        </>
      }
    >
      <form id="password" className="flex flex-col gap-3" onSubmit={submit}>
        {password("current", "Current password", "current-password")}
        {password("password", "New password", "new-password")}
        {password("again", "New password again", "new-password")}
        <p className="text-sm text-muted">
          At least {PASSWORD_MIN} characters. Every other browser is signed out;
          this one stays.
        </p>
        {draft.problem || error ? (
          <p className="text-sm text-accent">{draft.problem || error}</p>
        ) : null}
      </form>
    </Dialog>
  );
}

/**
 * Where you are signed in, each to sign out, and all but this browser at once: a section of the
 * page under Admins, read at a glance rather than opened.
 */
export function Sessions() {
  const sessions = useQuery({ queryKey: ["sessions"], queryFn: listSessions });
  const { save, busy, error } = useSave();
  const others = (sessions.data ?? []).filter((s) => !s.current).length;
  return (
    <Section
      title="Sessions"
      action={
        others > 0 ? (
          <Button
            disabled={busy}
            onClick={() => void save(revokeOtherSessions)}
          >
            Sign out everywhere else
          </Button>
        ) : undefined
      }
    >
      <ul
        aria-label="Sessions"
        className="divide-y divide-line rounded-md border border-line bg-bg text-sm"
      >
        {(sessions.data ?? []).map((s) => (
          <Item
            key={s.id}
            title={s.device || "A browser"}
            meta={
              s.current ? "This browser" : `Last used: ${when(s.last_seen_at)}`
            }
            actions={
              s.current ? (
                <DeleteButton
                  size="bar"
                  disabled={busy}
                  label="Sign out…"
                  what="session"
                  title="Sign this browser out?"
                  confirm="Sign out"
                  facts={[["Browser", s.device || "A browser"]]}
                  note="This is the browser you are using. Signing it out takes you to the sign-in page."
                  onDelete={() => revokeSession(s.id)}
                  onDeleted={() => leaveFor("/login")}
                />
              ) : (
                <Button
                  size="bar"
                  disabled={busy}
                  onClick={() => void save(() => revokeSession(s.id))}
                >
                  Sign out
                </Button>
              )
            }
          >
            {/* A guess, so what it was guessed from is under it, whole, scrolling rather than
                wrapping. */}
            <span className="scroll-thin block overflow-x-auto font-mono text-xs whitespace-nowrap text-muted">
              {s.user_agent}
            </span>
          </Item>
        ))}
      </ul>
      {error ? <p className="text-sm text-accent">{error}</p> : null}
    </Section>
  );
}
