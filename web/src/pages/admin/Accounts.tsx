import { useState, type FormEvent } from "react";

import {
  addCredential,
  createAccount,
  deleteAccount,
  deleteCredential,
  resetCredential,
  setIncoming,
  type Account,
  type Credential,
  type Incoming,
} from "@app/api/admin";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { Field, Select } from "@app/components/Field";
import { TextField } from "@app/components/TextField";
import {
  DeleteButton,
  Row,
  Rows,
  Section,
  ShownCredential,
  addressOf,
  incomingLabels,
  useDraft,
  useSave,
  when,
} from "@app/pages/admin/shared";

/** The accounts, each a row saying what its mail does; a click opens its settings. */
export function Accounts({ accounts }: { accounts: Account[] }) {
  const [adding, setAdding] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const account = accounts.find((a) => a.id === open);

  return (
    <Section
      title="Accounts"
      action={<Button onClick={() => setAdding(true)}>Add account</Button>}
    >
      {accounts.length ? (
        <Rows>
          {accounts.map((a) => (
            <Row
              key={a.id}
              title={a.address}
              summary={<AccountSummary account={a} accounts={accounts} />}
              onOpen={() => setOpen(a.id)}
            />
          ))}
        </Rows>
      ) : (
        <p className="text-sm text-faint">No accounts yet.</p>
      )}
      <AddAccountDialog open={adding} onClose={() => setAdding(false)} />
      {account ? (
        <AccountDialog
          open
          account={account}
          accounts={accounts}
          onClose={() => setOpen(null)}
        />
      ) : null}
    </Section>
  );
}

function AccountSummary({
  account,
  accounts,
}: {
  account: Account;
  accounts: Account[];
}) {
  const mail =
    account.incoming === "keep"
      ? "keeps its mail"
      : account.incoming === "reject"
        ? "refuses mail"
        : `forwards to ${addressOf(account.forward_account_id, accounts)}`;
  const n = account.credentials.length;
  return (
    <>
      {mail} ·{" "}
      {n
        ? `${n} ${n === 1 ? "credential" : "credentials"}`
        : "no credentials: nobody signs in"}
    </>
  );
}

/** An address, and nothing else asked: its default credential's password is generated. */
function AddAccountDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const [address, setAddress] = useDraft(open, () => "");
  const [made, setMade] = useDraft<{ login: string; password: string } | null>(
    open,
    () => null,
  );

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    await save(async () => {
      const a = await createAccount(address);
      setMade({ login: a.address, password: a.password });
    });
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={made ? "Account added" : "Add an account"}
      footer={
        made ? (
          <Button variant="solid" onClick={onClose}>
            Done
          </Button>
        ) : (
          <>
            <Button onClick={onClose}>Cancel</Button>
            <Button
              type="submit"
              form="add-account"
              variant="solid"
              disabled={busy || !address.trim()}
            >
              Add
            </Button>
          </>
        )
      }
    >
      {made ? (
        <ShownCredential login={made.login} password={made.password} />
      ) : (
        <form
          id="add-account"
          className="flex flex-col gap-3"
          onSubmit={submit}
        >
          <Field label="Address">
            <TextField
              type="email"
              placeholder="name@example.com"
              autoComplete="off"
              data-autofocus
              value={address}
              onChange={(e) => setAddress(e.target.value)}
            />
          </Field>
          <p className="text-sm text-muted">
            Its password is generated, and shown once.
          </p>
          {error ? <p className="text-sm text-accent">{error}</p> : null}
        </form>
      )}
    </Dialog>
  );
}

type Draft = { incoming: Incoming; forwardTo: string };

/** An account: what its mail does, held until Save, and its credentials. */
function AccountDialog({
  open,
  account,
  accounts,
  onClose,
}: {
  open: boolean;
  account: Account;
  accounts: Account[];
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const others = accounts.filter((a) => a.id !== account.id);
  const [draft, setDraft] = useDraft<Draft>(open, () => ({
    incoming: account.incoming,
    forwardTo: account.forward_account_id ?? others[0]?.id ?? "",
  }));
  // The credential being added (null) or given a new password; undefined is no dialog.
  const [credential, setCredential] = useState<Credential | null | undefined>(
    undefined,
  );

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const ok = await save(() =>
      setIncoming(
        account.id,
        draft.incoming,
        draft.incoming === "forward" ? draft.forwardTo : null,
      ),
    );
    if (ok) onClose();
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={account.address}
      footer={
        <>
          <span className="mr-auto">
            <DeleteButton
              disabled={busy}
              label="Delete account…"
              onConfirm={() =>
                void save(() => deleteAccount(account.id)).then(
                  (ok) => ok && onClose(),
                )
              }
            />
          </span>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            form="account"
            variant="solid"
            disabled={
              busy || (draft.incoming === "forward" && !draft.forwardTo)
            }
          >
            Save
          </Button>
        </>
      }
    >
      <form id="account" className="flex flex-col gap-3" onSubmit={submit}>
        <Field label="Incoming mail">
          <Select
            value={draft.incoming}
            onChange={(e) =>
              setDraft({ ...draft, incoming: e.target.value as Incoming })
            }
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
        </Field>
        {draft.incoming === "forward" ? (
          <Field label="Who gets the mail">
            <Select
              value={draft.forwardTo}
              onChange={(e) =>
                setDraft({ ...draft, forwardTo: e.target.value })
              }
            >
              {others.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.address}
                </option>
              ))}
            </Select>
          </Field>
        ) : null}
        {error ? <p className="text-sm text-accent">{error}</p> : null}
      </form>

      <div className="flex flex-col gap-2 border-t border-line pt-3 text-sm">
        <div className="flex items-center gap-2">
          <span className="caps text-muted">Credentials</span>
          <span className="flex-1" />
          <Button size="bar" onClick={() => setCredential(null)}>
            Add credential
          </Button>
        </div>
        {account.credentials.length ? (
          <ul className="divide-y divide-line rounded-md border border-line">
            {account.credentials.map((c) => (
              <li key={c.id} className="flex flex-col gap-1 px-3 py-2">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                  <span className="font-medium">{c.label}</span>
                  <span className="text-faint">
                    {c.last_used_at
                      ? `used ${when(c.last_used_at)}`
                      : "never used"}
                  </span>
                  <span className="ml-auto flex items-center gap-2">
                    <Button
                      size="bar"
                      disabled={busy}
                      onClick={() => setCredential(c)}
                    >
                      New password…
                    </Button>
                    <DeleteButton
                      size="bar"
                      disabled={busy}
                      onConfirm={() =>
                        void save(() => deleteCredential(account.id, c.id))
                      }
                    />
                  </span>
                </div>
                <code className="font-mono text-xs text-muted">{c.login}</code>
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-faint">
            No credentials: the account receives mail and nobody signs in as it.
          </p>
        )}
      </div>

      <CredentialDialog
        open={credential !== undefined}
        account={account}
        credential={credential ?? null}
        onClose={() => setCredential(undefined)}
      />
    </Dialog>
  );
}

/**
 * Adding a credential, or replacing one's password. Either way the password is generated and shown
 * here, once; a replaced one keeps its login, and the device using it needs the new password.
 */
function CredentialDialog({
  open,
  account,
  credential,
  onClose,
}: {
  open: boolean;
  account: Account;
  credential: Credential | null;
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const [label, setLabel] = useDraft(open, () => "");
  const [made, setMade] = useDraft<{ login: string; password: string } | null>(
    open,
    () => null,
  );

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    await save(async () => {
      const c = credential
        ? await resetCredential(account.id, credential.id)
        : await addCredential(account.id, label);
      setMade({ login: c.login, password: c.password });
    });
  };

  const title = made
    ? credential
      ? `New password for ${credential.label}`
      : "Credential added"
    : credential
      ? `New password for ${credential.label}`
      : "Add a credential";

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={title}
      footer={
        made ? (
          <Button variant="solid" onClick={onClose}>
            Done
          </Button>
        ) : (
          <>
            <Button onClick={onClose}>Cancel</Button>
            <Button
              type="submit"
              form="credential"
              variant="solid"
              disabled={busy || (!credential && !label.trim())}
            >
              {credential ? "Replace" : "Add"}
            </Button>
          </>
        )
      }
    >
      {made ? (
        <ShownCredential login={made.login} password={made.password} />
      ) : (
        <form id="credential" className="flex flex-col gap-3" onSubmit={submit}>
          {credential ? (
            <p className="text-sm text-muted">
              {credential.login} gets a new password. Whatever signs in with the
              old one stops until it is given the new one.
            </p>
          ) : (
            <>
              <Field label="Label">
                <TextField
                  placeholder="What it is for, e.g. iPhone"
                  autoComplete="off"
                  data-autofocus
                  value={label}
                  onChange={(e) => setLabel(e.target.value)}
                />
              </Field>
              <p className="text-sm text-muted">
                It gets a login of its own and a generated password, shown once.
              </p>
            </>
          )}
          {error ? <p className="text-sm text-accent">{error}</p> : null}
        </form>
      )}
    </Dialog>
  );
}
