import { useState, type FormEvent } from "react";

import {
  BUILT_IN,
  checkDomain,
  toAccount,
  createDomain,
  deleteDomain,
  updateDomain,
  type Account,
  type DMARC,
  type Domain,
  type Relay,
  type SPF,
  type Settings,
  type Unknown,
} from "@app/api/admin";
import { Button } from "@app/components/Button";
import { CopyBlock } from "@app/components/CopyBlock";
import { DomainAccounts } from "@app/pages/admin/Accounts";
import { Dialog } from "@app/components/Dialog";
import { Field, Select } from "@app/components/Field";
import { TextField } from "@app/components/TextField";
import {
  DNSStatusMark,
  DNSSummary,
  DeleteButton,
  Facts,
  LimitField,
  Row,
  TrashField,
  daysText,
  daysTyped,
  toDays,
  trashKept,
  Rows,
  Section,
  Summary,
  accountOption,
  dmarcLabels,
  keepsMail,
  limitTyped,
  mbText,
  megabytes,
  relayName,
  spfLabels,
  toBytes,
  unknownLabels,
  unknownSummary,
  useDraft,
  useSave,
  when,
} from "@app/pages/admin/shared";

type Context = {
  settings: Settings;
  accounts: Account[];
  relays: Relay[];
  dnsAPI: boolean;
  /** Records are not checked in a staging run. */
  staging: boolean;
};

/**
 * The domains, each a card: its row saying what it does, a click opening its settings, and its
 * accounts under it.
 */
export function Domains({ domains, ...ctx }: Context & { domains: Domain[] }) {
  const [adding, setAdding] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const domain = domains.find((d) => d.id === open);

  return (
    <Section
      title="Domains"
      action={<Button onClick={() => setAdding(true)}>Add domain</Button>}
    >
      {domains.length ? (
        <div className="flex flex-col gap-3">
          {domains.map((d) => (
            <Rows key={d.id}>
              <Row
                title={d.name}
                summary={<DomainSummary domain={d} {...ctx} />}
                onOpen={() => setOpen(d.id)}
              />
              <DomainAccounts
                domain={d}
                accounts={ctx.accounts}
                domains={domains}
                settings={ctx.settings}
                relays={ctx.relays}
              />
            </Rows>
          ))}
        </div>
      ) : (
        <p className="text-sm text-faint">No domains yet.</p>
      )}
      <AddDomainDialog open={adding} onClose={() => setAdding(false)} />
      {domain ? (
        <DomainDialog
          open
          domain={domain}
          onClose={() => setOpen(null)}
          {...ctx}
        />
      ) : null}
    </Section>
  );
}

/** Each setting as it applies, marked where it is the default's. */
function DomainSummary({
  domain,
  settings,
  accounts,
  relays,
  staging,
}: Context & { domain: Domain }) {
  return (
    <Summary
      parts={[
        {
          label: "Catch-all",
          inherited: domain.unknown === null,
          value: domain.unknown
            ? unknownSummary(
                domain.unknown,
                domain.unknown_account_id,
                accounts,
              )
            : unknownLabels[settings.unknown],
        },
        {
          label: "Sending",
          inherited: domain.relay === null,
          value: relayName(domain.relay ?? settings.relay_id, relays),
        },
        {
          label: "Largest message",
          inherited: domain.max_message_bytes === null,
          value: megabytes(
            domain.max_message_bytes ?? settings.max_message_bytes,
          ),
        },
        {
          label: "Trash kept",
          inherited: domain.trash_days === null,
          value: trashKept(domain.trash_days ?? settings.trash_days),
        },
        {
          label: "DMARC",
          inherited: domain.dmarc === null,
          value: domain.dmarc ?? settings.dmarc,
        },
        {
          label: "SPF",
          inherited: domain.spf === null,
          value: domain.spf ?? settings.spf,
        },
        {
          label: "DNS",
          value: staging ? (
            "Not checked in staging"
          ) : (
            <DNSSummary domain={domain} />
          ),
        },
      ]}
    />
  );
}

function AddDomainDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const [name, setName] = useDraft(open, () => "");
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (await save(() => createDomain(name))) onClose();
  };
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Add a domain"
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            form="add-domain"
            variant="solid"
            disabled={busy || !name.trim()}
          >
            Add
          </Button>
        </>
      }
    >
      <form id="add-domain" className="flex flex-col gap-3" onSubmit={submit}>
        <Field label="Domain">
          <TextField
            placeholder="example.com"
            autoComplete="off"
            data-autofocus
            value={name}
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <p className="text-sm text-muted">
          It follows the defaults until its own settings say otherwise. Point
          its MX at the mail host.
        </p>
        {error ? <p className="text-sm text-accent">{error}</p> : null}
      </form>
    </Dialog>
  );
}

/** A domain's own settings as a dialog holds them until Save: "" is the default. */
type Draft = {
  unknown: Unknown | "";
  /** Who gets the catch-all's mail, for forward and junk. */
  to: string;
  dmarc: DMARC | "";
  spf: SPF | "";
  relay: string;
  /** The largest message in MB, and Trash days, as typed. */
  limit: string;
  trash: string;
};

function DomainDialog({
  open,
  domain,
  onClose,
  settings,
  accounts,
  relays,
  dnsAPI,
  staging,
}: Context & {
  open: boolean;
  domain: Domain;
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const check = useSave();
  const [draft, setDraft] = useDraft<Draft>(open, () => ({
    unknown: domain.unknown ?? "",
    // Somebody at the same domain when there is somebody, the likely reader.
    to:
      domain.unknown_account_id ??
      accounts.find((a) => a.domain_id === domain.id && keepsMail(a))?.id ??
      accounts.find(keepsMail)?.id ??
      "",
    dmarc: domain.dmarc ?? "",
    spf: domain.spf ?? "",
    relay: domain.relay ?? "",
    limit: mbText(domain.max_message_bytes),
    trash: daysText(domain.trash_days),
  }));

  const set = (change: Partial<Draft>) => setDraft({ ...draft, ...change });
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const ok = await save(() =>
      updateDomain(domain.id, {
        unknown: draft.unknown,
        unknown_account_id: toAccount(draft.unknown) ? draft.to : null,
        dmarc: draft.dmarc,
        spf: draft.spf,
        relay: draft.relay,
        max_message_bytes: toBytes(draft.limit),
        trash_days: toDays(draft.trash),
      }),
    );
    if (ok) onClose();
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={domain.name}
      wide
      footer={
        <>
          <span className="mr-auto">
            <DeleteButton
              disabled={busy}
              label="Delete domain…"
              what="domain"
              facts={[["Domain", domain.name]]}
              note="Its DKIM key goes with it. A domain with accounts is kept until they are deleted."
              onDelete={() => deleteDomain(domain.id)}
              onDeleted={onClose}
            />
          </span>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            form="domain"
            variant="solid"
            disabled={
              busy ||
              (toAccount(draft.unknown) && !draft.to) ||
              !limitTyped(draft.limit) ||
              !daysTyped(draft.trash)
            }
          >
            Save
          </Button>
        </>
      }
    >
      <form id="domain" className="flex flex-col gap-3" onSubmit={submit}>
        <Field label="Catch-all" hint="Mail for addresses nobody has.">
          <Select
            value={draft.unknown}
            onChange={(e) => set({ unknown: e.target.value as Unknown | "" })}
          >
            <option value="">Default: {unknownLabels[settings.unknown]}</option>
            {(Object.keys(unknownLabels) as Unknown[]).map((u) => (
              <option
                key={u}
                value={u}
                disabled={toAccount(u) && !accounts.some(keepsMail)}
              >
                {unknownLabels[u]}
              </option>
            ))}
          </Select>
        </Field>
        {toAccount(draft.unknown) ? (
          <Field
            label={
              draft.unknown === "junk"
                ? "Who gets the junk"
                : "Who gets the mail"
            }
          >
            <Select
              value={draft.to}
              onChange={(e) => set({ to: e.target.value })}
            >
              {accounts.map((a) => (
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
            onChange={(e) => set({ relay: e.target.value })}
          >
            <option value="">
              Default: {relayName(settings.relay_id, relays)}
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
          onChange={(limit) => set({ limit })}
          inherited={{
            from: "the default",
            bytes: settings.max_message_bytes,
          }}
        />
        <TrashField
          value={draft.trash}
          onChange={(trash) => set({ trash })}
          inherited={{ from: "the default", days: settings.trash_days }}
        />
        <Field label="DMARC">
          <Select
            value={draft.dmarc}
            onChange={(e) => set({ dmarc: e.target.value as DMARC | "" })}
          >
            <option value="">Default: {dmarcLabels[settings.dmarc]}</option>
            {(Object.keys(dmarcLabels) as DMARC[]).map((d) => (
              <option key={d} value={d}>
                {dmarcLabels[d]}
              </option>
            ))}
          </Select>
        </Field>
        <Field label="SPF">
          <Select
            value={draft.spf}
            onChange={(e) => set({ spf: e.target.value as SPF | "" })}
          >
            <option value="">Default: {spfLabels[settings.spf]}</option>
            {(Object.keys(spfLabels) as SPF[]).map((p) => (
              <option key={p} value={p}>
                {spfLabels[p]}
              </option>
            ))}
          </Select>
        </Field>
        {error ? <p className="text-sm text-accent">{error}</p> : null}
        <div className="flex flex-col gap-2 border-t border-line pt-3">
          <div className="flex items-start gap-3">
            <Facts
              items={[
                ["DNS records", dnsAPI ? "DNS API" : "By hand"],
                [
                  "Checked",
                  staging
                    ? "Not in staging"
                    : domain.dns_checked_at
                      ? when(domain.dns_checked_at)
                      : "Not yet",
                ],
              ]}
            />
            <span className="flex-1" />
            {staging ? null : (
              <Button
                size="bar"
                disabled={check.busy}
                onClick={() => void check.save(() => checkDomain(domain.id))}
              >
                {check.busy ? "Checking…" : "Check now"}
              </Button>
            )}
          </div>
          {check.error ? (
            <p className="text-sm text-accent">{check.error}</p>
          ) : null}
          {domain.records.map((r) => (
            <CopyBlock
              key={r.type + r.name}
              title={r.type}
              aside={
                <span className="flex gap-2">
                  {r.managed ? null : (
                    <span className="text-faint">Set outside mailio</span>
                  )}
                  <DNSStatusMark record={r} />
                </span>
              }
              rows={[
                { label: "Name", value: r.name },
                { label: "Value", value: r.data },
              ]}
            >
              {r.found.length ? (
                <div className="flex flex-col gap-0.5 pr-1.5">
                  <span className="text-muted">Found</span>
                  {r.found.map((f) => (
                    <code key={f} className="font-mono break-all text-accent">
                      {f}
                    </code>
                  ))}
                </div>
              ) : null}
            </CopyBlock>
          ))}
        </div>
      </form>
    </Dialog>
  );
}
