import { useState, type FormEvent } from "react";

import {
  createAdmin,
  deleteAdmin,
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
  PasswordField,
  Row,
  Rows,
  Section,
  useDraft,
  useSave,
} from "@app/pages/admin/shared";

/** Who may sign in here: a name and a password each, and never an account. */
export function Admins({ admins, me }: { admins: Admin[]; me: Admin }) {
  const [adding, setAdding] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const admin = admins.find((a) => a.id === open);
  return (
    <Section
      title="Admins"
      action={<Button onClick={() => setAdding(true)}>Add admin</Button>}
    >
      <Rows>
        {admins.map((a) => (
          <Row
            key={a.id}
            title={a.name}
            aside={a.id === me.id ? "you" : undefined}
            onOpen={() => setOpen(a.id)}
          />
        ))}
      </Rows>
      <AddAdminDialog open={adding} onClose={() => setAdding(false)} />
      {admin ? (
        <AdminDialog
          open
          admin={admin}
          you={admin.id === me.id}
          onClose={() => setOpen(null)}
        />
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

/** An admin: a new password, held until Save, or a delete. */
function AdminDialog({
  open,
  admin,
  you,
  onClose,
}: {
  open: boolean;
  admin: Admin;
  you: boolean;
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
      aside={you ? <span className="text-sm text-faint">you</span> : undefined}
      footer={
        <>
          <span className="mr-auto">
            <DeleteButton
              disabled={busy}
              label="Delete admin…"
              what="admin"
              facts={[["Name", admin.name]]}
              note={
                you
                  ? "That is you: you are signed out."
                  : "They are signed out everywhere."
              }
              onDelete={() => deleteAdmin(admin.id)}
              onDeleted={() => (you ? leaveFor("/login") : onClose())}
            />
          </span>
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
        <p className="text-sm text-muted">
          {you
            ? "You stay signed in here; everywhere else you are signed out."
            : "They are signed out everywhere."}
        </p>
        {error ? <p className="text-sm text-accent">{error}</p> : null}
      </form>
    </Dialog>
  );
}
