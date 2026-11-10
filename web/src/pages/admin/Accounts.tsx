import { useState, type FormEvent } from "react";

import {
  BUILT_IN,
  addCredential,
  createAccount,
  deleteAccount,
  deleteCredential,
  resetCredential,
  updateAccount,
  type Account,
  type Credential,
  type Domain,
  type Incoming,
  type Relay,
  type Settings,
} from "@app/api/admin";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { Field, Select } from "@app/components/Field";
import { TextField } from "@app/components/TextField";
import {
  DeleteButton,
  Item,
  Row,
  ShownCredential,
  Facts,
  LimitField,
  Summary,
  Toward,
  accountOption,
  incomingLabels,
  keepsMail,
  limitTyped,
  mbText,
  megabytes,
  relayName,
  toBytes,
  sendersTo,
  Senders,
  useDraft,
  useSave,
  useSettling,
  when,
} from "@app/pages/admin/shared";

/**
 * A domain's accounts, as rows under the domain's own, each saying what its mail does; a click
 * opens its settings. The last row adds one at the domain.
 */
export function DomainAccounts({
  domain,
  accounts,
  domains,
  settings,
  relays,
}: {
  domain: Domain;
  /** Every account: a forward can go to one at another domain. */
  accounts: Account[];
  domains: Domain[];
  settings: Settings;
  relays: Relay[];
}) {
  const [adding, setAdding] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const account = accounts.find((a) => a.id === open);

  return (
    <>
      {accounts
        .filter((a) => a.domain_id === domain.id)
        .map((a) => (
          <Row
            key={a.id}
            nested
            title={a.address}
            summary={
              <AccountSummary account={a} accounts={accounts} relays={relays} />
            }
            onOpen={() => setOpen(a.id)}
          />
        ))}
      <li>
        <button
          type="button"
          onClick={() => setAdding(true)}
          className="w-full py-2 pr-3 pl-8 text-left text-sm text-muted hover:bg-fill hover:text-fg focus-visible:bg-fill focus-visible:outline-none"
        >
          Add account
        </button>
        <AddAccountDialog
          open={adding}
          domain={domain}
          onClose={() => setAdding(false)}
        />
        {account ? (
          <AccountDialog
            open
            account={account}
            accounts={accounts}
            domain={domain}
            domains={domains}
            settings={settings}
            relays={relays}
            onClose={() => setOpen(null)}
          />
        ) : null}
      </li>
    </>
  );
}

/** What its mail does, and its own settings where it has them. */
function AccountSummary({
  account,
  accounts,
  relays,
}: {
  account: Account;
  accounts: Account[];
  relays: Relay[];
}) {
  const incoming =
    account.incoming === "forward" ? (
      <Toward
        label="Forward"
        to={account.forward_account_id}
        accounts={accounts}
      />
    ) : (
      incomingLabels[account.incoming]
    );
  return (
    <Summary
      parts={[
        { label: "Incoming", value: incoming },
        ...(account.relay
          ? [{ label: "Sending", value: relayName(account.relay, relays) }]
          : []),
        ...(account.max_message_bytes
          ? [
              {
                label: "Largest message",
                value: megabytes(account.max_message_bytes),
              },
            ]
          : []),
        { label: "Credentials", value: account.credentials.length },
      ]}
    />
  );
}

/**
 * A name at the domain, and nothing else asked: its default credential's password is generated.
 */
function AddAccountDialog({
  open,
  domain,
  onClose,
}: {
  open: boolean;
  domain: Domain;
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const [name, setName] = useDraft(open, () => "");
  const [made, setMade] = useDraft<{ login: string; password: string } | null>(
    open,
    () => null,
  );
  const settling = useSettling(made);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    await save(async () => {
      const a = await createAccount(`${name.trim()}@${domain.name}`);
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
          <Button variant="solid" onClick={() => !settling() && onClose()}>
            Done
          </Button>
        ) : (
          <>
            <Button onClick={onClose}>Cancel</Button>
            <Button
              type="submit"
              form="add-account"
              variant="solid"
              disabled={busy || !name.trim()}
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
            <span className="flex items-center gap-2">
              <TextField
                placeholder="name"
                aria-label={`Name at ${domain.name}`}
                autoComplete="off"
                autoCapitalize="none"
                autoCorrect="off"
                spellCheck={false}
                data-autofocus
                className="min-w-0 flex-1"
                value={name}
                onChange={(e) => setName(e.target.value)}
              />
              <span className="shrink-0 text-muted">@{domain.name}</span>
            </span>
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

/**
 * An account's settings as its dialog holds them until Save: relay "" is its domain's, and limit
 * is in MB, as typed.
 */
type Draft = {
  incoming: Incoming;
  forwardTo: string;
  relay: string;
  limit: string;
};

/** An account: what its mail does and its largest message, held until Save, and its credentials. */
function AccountDialog({
  open,
  account,
  accounts,
  domain,
  domains,
  settings,
  relays,
  onClose,
}: {
  open: boolean;
  account: Account;
  accounts: Account[];
  domain: Domain;
  domains: Domain[];
  settings: Settings;
  relays: Relay[];
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const others = accounts.filter((a) => a.id !== account.id);
  // Mail sent on here has to stay here: it was sent on once already.
  const senders = sendersTo(account.id, accounts, domains);
  const [draft, setDraft] = useDraft<Draft>(open, () => ({
    incoming: account.incoming,
    forwardTo: account.forward_account_id ?? others.find(keepsMail)?.id ?? "",
    relay: account.relay ?? "",
    limit: mbText(account.max_message_bytes),
  }));
  const blocked = senders.length > 0 && draft.incoming !== "keep";
  // The credential being added (null) or given a new password; undefined is no dialog.
  const [credential, setCredential] = useState<Credential | null | undefined>(
    undefined,
  );

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const ok = await save(() =>
      updateAccount(account.id, {
        incoming: draft.incoming,
        forward_account_id:
          draft.incoming === "forward" ? draft.forwardTo : null,
        max_message_bytes: toBytes(draft.limit),
        relay: draft.relay,
      }),
    );
    if (ok) onClose();
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={account.address}
      wide
      footer={
        <>
          <span className="mr-auto">
            <DeleteButton
              disabled={busy}
              label="Delete account…"
              what="account"
              facts={[["Address", account.address]]}
              note="Its credentials, mailboxes and every message in them go with it."
              onDelete={() => deleteAccount(account.id)}
              onDeleted={onClose}
            />
          </span>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            form="account"
            variant="solid"
            disabled={
              busy ||
              blocked ||
              (draft.incoming === "forward" && !draft.forwardTo) ||
              !limitTyped(draft.limit)
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
                disabled={
                  i !== "keep" &&
                  (senders.length > 0 ||
                    (i === "forward" && !others.some(keepsMail)))
                }
              >
                {i === "reject"
                  ? `${incomingLabels[i]} · no-reply account`
                  : incomingLabels[i]}
              </option>
            ))}
          </Select>
        </Field>
        {senders.length ? (
          <Senders senders={senders} blocked={blocked} />
        ) : null}
        {draft.incoming === "forward" ? (
          <Field label="Who gets the mail">
            <Select
              value={draft.forwardTo}
              onChange={(e) =>
                setDraft({ ...draft, forwardTo: e.target.value })
              }
            >
              {others.map((a) => (
                <option key={a.id} value={a.id} disabled={!keepsMail(a)}>
                  {accountOption(a)}
                </option>
              ))}
            </Select>
          </Field>
        ) : null}
        <Field label="Sending">
          <Select
            value={draft.relay}
            onChange={(e) => setDraft({ ...draft, relay: e.target.value })}
          >
            <option value="">
              Domain's: {relayName(domain.relay ?? settings.relay_id, relays)}
            </option>
            <option value={BUILT_IN}>Built-in</option>
            {relays.map((r) => (
              <option key={r.id} value={r.id}>
                {r.label}
              </option>
            ))}
          </Select>
        </Field>
        <LimitField
          value={draft.limit}
          onChange={(limit) => setDraft({ ...draft, limit })}
          inherited={{
            from: "the domain's",
            bytes: domain.max_message_bytes ?? settings.max_message_bytes,
          }}
        />
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
              <Item
                key={c.id}
                title={c.label}
                meta={`Last used: ${c.last_used_at ? when(c.last_used_at) : "never"}`}
                detail={c.login}
                actions={
                  <>
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
                      what="credential"
                      facts={[
                        ["Label", c.label],
                        ["Login", c.login],
                      ]}
                      note="Whatever signs in with it stops."
                      onDelete={() => deleteCredential(account.id, c.id)}
                    />
                  </>
                }
              />
            ))}
          </ul>
        ) : (
          <p className="text-faint">No credentials.</p>
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
  const [draft, setDraft] = useDraft(open, () => ({ label: "", password: "" }));
  const [made, setMade] = useDraft<{ login: string; password: string } | null>(
    open,
    () => null,
  );
  const settling = useSettling(made);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    await save(async () => {
      const c = credential
        ? await resetCredential(account.id, credential.id, draft.password)
        : await addCredential(account.id, draft.label, draft.password);
      setMade({ login: c.login, password: c.password });
    });
  };

  const title = credential
    ? `New password for ${credential.label}`
    : made
      ? "Credential added"
      : "Add a credential";

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={title}
      footer={
        made ? (
          <Button variant="solid" onClick={() => !settling() && onClose()}>
            Done
          </Button>
        ) : (
          <>
            <Button onClick={onClose}>Cancel</Button>
            <Button
              type="submit"
              form="credential"
              variant="solid"
              disabled={busy || (!credential && !draft.label.trim())}
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
            <Facts items={[["Login", credential.login]]} />
          ) : (
            <Field label="Label">
              <TextField
                placeholder="iPhone"
                autoComplete="off"
                data-autofocus
                value={draft.label}
                onChange={(e) => setDraft({ ...draft, label: e.target.value })}
              />
            </Field>
          )}
          <Field
            label="Password"
            hint="Empty: generated, 24 characters. Shown once either way."
          >
            <TextField
              placeholder="Generated"
              autoComplete="off"
              autoCapitalize="none"
              autoCorrect="off"
              spellCheck={false}
              className="font-mono"
              data-autofocus={credential ? true : undefined}
              value={draft.password}
              onChange={(e) => setDraft({ ...draft, password: e.target.value })}
            />
          </Field>
          {error ? <p className="text-sm text-accent">{error}</p> : null}
        </form>
      )}
    </Dialog>
  );
}
