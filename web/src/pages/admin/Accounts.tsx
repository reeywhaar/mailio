import { useState, type FormEvent } from "react";

import {
  BUILT_IN,
  NO_SEND,
  addCredential,
  clearMail,
  createAccount,
  createAlias,
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
import { Segmented } from "@app/components/Segmented";
import { TextField } from "@app/components/TextField";
import {
  DeleteButton,
  Item,
  Row,
  ShownCredential,
  Facts,
  LimitField,
  Summary,
  KeptField,
  daysText,
  daysTyped,
  toDays,
  daysKept,
  Toward,
  AccountOptions,
  addressOf,
  incomingLabels,
  keepsMail,
  limitTyped,
  mbText,
  megabytes,
  relayName,
  sizeText,
  toBytes,
  sendersTo,
  Senders,
  useDraft,
  useSave,
  useSettling,
  when,
} from "@app/pages/admin/shared";

/**
 * A domain's accounts, as rows under the domain's own, each saying what its mail does, with its
 * aliases, at any domain, under it; a click opens one's settings. The last row adds one at the
 * domain.
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
        .filter((a) => a.domain_id === domain.id && a.kind !== "alias")
        .map((a) => (
          <Row
            key={a.id}
            nested
            title={a.address}
            summary={
              <AccountSummary account={a} accounts={accounts} relays={relays} />
            }
            onOpen={() => setOpen(a.id)}
          >
            <AliasPills
              aliases={accounts.filter(
                (x) => x.kind === "alias" && x.forward_account_id === a.id,
              )}
              onOpen={setOpen}
            />
          </Row>
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
          accounts={accounts}
          onClose={() => setAdding(false)}
        />
        {account?.kind === "alias" ? (
          <AliasDialog
            open
            alias={account}
            accounts={accounts}
            onClose={() => setOpen(null)}
          />
        ) : account ? (
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

/** An account's aliases, under its summary, a pill each; a click opens one. */
function AliasPills({
  aliases,
  onOpen,
}: {
  aliases: Account[];
  onOpen: (id: string) => void;
}) {
  if (!aliases.length) return null;
  return (
    <div className="-mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-1 pr-3 pb-2 pl-8 text-sm text-muted">
      <span className="mr-0.5">Aliases:</span>
      {aliases.map((a) => (
        <button
          key={a.id}
          type="button"
          onClick={() => onOpen(a.id)}
          className="rounded-full border border-line bg-bg px-2 py-px text-fg hover:border-muted focus-visible:border-muted focus-visible:outline-none pointer-coarse:py-1"
        >
          {a.address}
        </button>
      ))}
    </div>
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
        { label: "Messages", value: account.messages.toLocaleString() },
        { label: "Size", value: sizeText(account.bytes) },
        ...(account.send_as_account_id
          ? [
              {
                label: "Sends as",
                value: addressOf(account.send_as_account_id, accounts),
              },
            ]
          : account.relay
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
        ...(account.trash_days !== null
          ? [{ label: "Trash kept", value: daysKept(account.trash_days) }]
          : []),
        ...(account.junk_days !== null
          ? [{ label: "Junk kept", value: daysKept(account.junk_days) }]
          : []),
        { label: "Credentials", value: account.credentials.length },
      ]}
    />
  );
}

type Kind = Account["kind"];

/**
 * A name at the domain, and an account or an alias of one. An account's default credential's
 * password is generated and shown here once; an alias has none, and the dialog closes.
 */
function AddAccountDialog({
  open,
  domain,
  accounts,
  onClose,
}: {
  open: boolean;
  domain: Domain;
  accounts: Account[];
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const keepers = accounts.filter(keepsMail);
  const [draft, setDraft] = useDraft(open, () => ({
    kind: "account" as Kind,
    name: "",
    // Somebody at the same domain when there is somebody, the likely owner.
    aliasOf:
      keepers.find((a) => a.domain_id === domain.id)?.id ??
      keepers[0]?.id ??
      "",
  }));
  const [made, setMade] = useDraft<{ login: string; password: string } | null>(
    open,
    () => null,
  );
  const settling = useSettling(made);
  const alias = draft.kind === "alias";

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const address = `${draft.name.trim()}@${domain.name}`;
    if (alias) {
      if (await save(() => createAlias(address, draft.aliasOf))) onClose();
      return;
    }
    await save(async () => {
      const a = await createAccount(address);
      setMade({ login: a.address, password: a.password });
    });
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={made ? "Account added" : alias ? "Add an alias" : "Add an account"}
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
              disabled={busy || !draft.name.trim() || (alias && !draft.aliasOf)}
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
          <Segmented
            label="Kind"
            value={draft.kind}
            onChange={(kind) => setDraft({ ...draft, kind })}
            options={[
              { value: "account", label: "Account" },
              { value: "alias", label: "Alias", disabled: !keepers.length },
            ]}
          />
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
                value={draft.name}
                onChange={(e) => setDraft({ ...draft, name: e.target.value })}
              />
              <span className="shrink-0 text-muted">@{domain.name}</span>
            </span>
          </Field>
          {alias ? (
            <AliasOf
              value={draft.aliasOf}
              onChange={(aliasOf) => setDraft({ ...draft, aliasOf })}
              accounts={accounts}
            />
          ) : (
            <p className="text-sm text-muted">
              Its password is generated, and shown once.
            </p>
          )}
          {error ? <p className="text-sm text-accent">{error}</p> : null}
        </form>
      )}
    </Dialog>
  );
}

/** In the Sending choice, before the id of an account to send as, which is not a relay. */
const AS = "as:";

/**
 * An account's settings as its dialog holds them until Save: relay "" is its domain's, AS and an
 * id another account's, and limit in MB and trash and junk in days are as typed.
 */
type Draft = {
  incoming: Incoming;
  forwardTo: string;
  relay: string;
  limit: string;
  trash: string;
  junk: string;
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
    relay: account.send_as_account_id
      ? AS + account.send_as_account_id
      : (account.relay ?? ""),
    limit: mbText(account.max_message_bytes),
    trash: daysText(account.trash_days),
    junk: daysText(account.junk_days),
  }));
  const blocked = senders.length > 0 && draft.incoming !== "keep";
  // Whom it can send as: an account that sends as itself, while nobody sends as this one.
  const sentAs = accounts.some((a) => a.send_as_account_id === account.id);
  const sendAs = sentAs
    ? []
    : others.filter((a) => a.kind === "account" && !a.send_as_account_id);
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
        ...(draft.relay.startsWith(AS)
          ? { send_as_account_id: draft.relay.slice(AS.length) }
          : { relay: draft.relay, send_as_account_id: "" }),
        trash_days: toDays(draft.trash),
        junk_days: toDays(draft.junk),
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
              !limitTyped(draft.limit) ||
              !daysTyped(draft.trash) ||
              !daysTyped(draft.junk)
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
              <AccountOptions accounts={others} />
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
            <option value={NO_SEND}>Doesn't send</option>
            {sendAs.length ? (
              <optgroup label="As another account">
                {sendAs.map((a) => (
                  <option key={a.id} value={AS + a.id}>
                    {a.address}
                  </option>
                ))}
              </optgroup>
            ) : null}
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
        <KeptField
          box="Trash"
          value={draft.trash}
          onChange={(trash) => setDraft({ ...draft, trash })}
          inherited={{
            from: "the domain's",
            days: domain.trash_days ?? settings.trash_days,
          }}
        />
        <KeptField
          box="Junk"
          value={draft.junk}
          onChange={(junk) => setDraft({ ...draft, junk })}
          inherited={{
            from: "the domain's",
            days: domain.junk_days ?? settings.junk_days,
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

      <div className="flex flex-col gap-2 border-t border-line pt-3 text-sm">
        <span className="caps text-accent">Danger zone</span>
        <div className="flex items-center gap-3">
          <Facts
            items={[
              ["Messages", account.messages.toLocaleString()],
              ["Size", sizeText(account.bytes)],
            ]}
          />
          <span className="flex-1" />
          <DeleteButton
            size="bar"
            disabled={busy || account.messages === 0}
            label="Clear all mail…"
            what="mail"
            title="Clear all mail"
            confirm="Clear"
            facts={[
              ["Address", account.address],
              ["Messages", account.messages.toLocaleString()],
              ["Size", sizeText(account.bytes)],
            ]}
            note="Every message in every mailbox is deleted for good. The mailboxes stay."
            onDelete={() => clearMail(account.id)}
          />
        </div>
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

/** Which account an alias belongs to: one that keeps its mail. */
function AliasOf({
  value,
  onChange,
  accounts,
}: {
  value: string;
  onChange: (id: string) => void;
  accounts: Account[];
}) {
  return (
    <Field
      label="Alias of"
      hint="Its mail goes to that account, which can send as it too."
    >
      <Select value={value} onChange={(e) => onChange(e.target.value)}>
        <AccountOptions accounts={accounts} />
      </Select>
    </Field>
  );
}

/** An alias: the account it belongs to, held until Save. */
function AliasDialog({
  open,
  alias,
  accounts,
  onClose,
}: {
  open: boolean;
  alias: Account;
  accounts: Account[];
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const [to, setTo] = useDraft(open, () => alias.forward_account_id ?? "");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const ok = await save(() =>
      updateAccount(alias.id, { incoming: "forward", forward_account_id: to }),
    );
    if (ok) onClose();
  };
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={alias.address}
      footer={
        <>
          <span className="mr-auto">
            <DeleteButton
              disabled={busy}
              label="Delete alias…"
              what="alias"
              facts={[
                ["Address", alias.address],
                ["Alias of", addressOf(alias.forward_account_id, accounts)],
              ]}
              note="Mail to it is refused from then on. The mail it brought stays with the account."
              onDelete={() => deleteAccount(alias.id)}
              onDeleted={onClose}
            />
          </span>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            form="alias"
            variant="solid"
            disabled={busy || !to}
          >
            Save
          </Button>
        </>
      }
    >
      <form id="alias" className="flex flex-col gap-3" onSubmit={submit}>
        <AliasOf
          value={to}
          onChange={setTo}
          accounts={accounts.filter((a) => a.id !== alias.id)}
        />
        {error ? <p className="text-sm text-accent">{error}</p> : null}
      </form>
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
