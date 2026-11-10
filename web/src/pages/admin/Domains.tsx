import { useState, type FormEvent } from "react";

import {
  BUILT_IN,
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
import { Dialog } from "@app/components/Dialog";
import { Field, Select } from "@app/components/Field";
import { TextField } from "@app/components/TextField";
import {
  DeleteButton,
  Row,
  Rows,
  Section,
  dmarcLabels,
  relayName,
  spfLabels,
  unknownLabels,
  unknownSummary,
  useDraft,
  useSave,
} from "@app/pages/admin/shared";

type Context = {
  settings: Settings;
  accounts: Account[];
  relays: Relay[];
  dnsAPI: boolean;
};

/** The domains, each a row saying what it does; a click opens its settings. */
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
        <Rows>
          {domains.map((d) => (
            <Row
              key={d.id}
              title={d.name}
              summary={<DomainSummary domain={d} {...ctx} />}
              onOpen={() => setOpen(d.id)}
            />
          ))}
        </Rows>
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
}: Context & { domain: Domain }) {
  const parts = [
    {
      label: "Unknown addresses",
      own: domain.unknown !== null,
      value: unknownSummary(
        domain.unknown ?? settings.unknown,
        domain.unknown
          ? domain.unknown_account_id
          : settings.unknown_account_id,
        accounts,
      ),
    },
    {
      label: "Sending",
      own: domain.relay !== null,
      value: relayName(domain.relay ?? settings.relay_id, relays),
    },
    {
      label: "DMARC",
      own: domain.dmarc !== null,
      value: domain.dmarc ?? settings.dmarc,
    },
    {
      label: "SPF",
      own: domain.spf !== null,
      value: domain.spf ?? settings.spf,
    },
  ];
  return (
    <span className="flex flex-wrap gap-x-3 gap-y-0.5">
      {parts.map((p) => (
        <span key={p.label}>
          {p.label}: <span className="text-fg">{p.value}</span>
          {p.own ? null : <span className="text-faint"> (default)</span>}
        </span>
      ))}
    </span>
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
  junkTo: string;
  dmarc: DMARC | "";
  spf: SPF | "";
  relay: string;
};

function DomainDialog({
  open,
  domain,
  onClose,
  settings,
  accounts,
  relays,
  dnsAPI,
}: Context & {
  open: boolean;
  domain: Domain;
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const [draft, setDraft] = useDraft<Draft>(open, () => ({
    unknown: domain.unknown ?? "",
    // Junk goes to somebody at the same domain when there is somebody, the likely reader.
    junkTo:
      domain.unknown_account_id ??
      accounts.find((a) => a.domain_id === domain.id)?.id ??
      accounts[0]?.id ??
      "",
    dmarc: domain.dmarc ?? "",
    spf: domain.spf ?? "",
    relay: domain.relay ?? "",
  }));

  const set = (change: Partial<Draft>) => setDraft({ ...draft, ...change });
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const ok = await save(() =>
      updateDomain(domain.id, {
        unknown: draft.unknown,
        unknown_account_id: draft.unknown === "junk" ? draft.junkTo : null,
        dmarc: draft.dmarc,
        spf: draft.spf,
        relay: draft.relay,
      }),
    );
    if (ok) onClose();
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={domain.name}
      footer={
        <>
          <span className="mr-auto">
            <DeleteButton
              disabled={busy}
              label="Delete domain…"
              onConfirm={() =>
                void save(() => deleteDomain(domain.id)).then(
                  (ok) => ok && onClose(),
                )
              }
            />
          </span>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            form="domain"
            variant="solid"
            disabled={busy || (draft.unknown === "junk" && !draft.junkTo)}
          >
            Save
          </Button>
        </>
      }
    >
      <form id="domain" className="flex flex-col gap-3" onSubmit={submit}>
        <Field label="Unknown addresses">
          <Select
            value={draft.unknown}
            onChange={(e) => set({ unknown: e.target.value as Unknown | "" })}
          >
            <option value="">
              Default:{" "}
              {unknownSummary(
                settings.unknown,
                settings.unknown_account_id,
                accounts,
              )}
            </option>
            {(Object.keys(unknownLabels) as Unknown[]).map((u) => (
              <option
                key={u}
                value={u}
                disabled={u === "junk" && !accounts.length}
              >
                {unknownLabels[u]}
              </option>
            ))}
          </Select>
        </Field>
        {draft.unknown === "junk" ? (
          <Field label="Who gets the junk">
            <Select
              value={draft.junkTo}
              onChange={(e) => set({ junkTo: e.target.value })}
            >
              {accounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.address}
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
            <option value={BUILT_IN}>
              Built in, straight to each recipient
            </option>
            {relays.map((r) => (
              <option key={r.id} value={r.id}>
                Through {r.label}
              </option>
            ))}
          </Select>
        </Field>
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
        <div className="flex flex-col gap-2 border-t border-line pt-3 text-sm">
          <p className="text-muted">
            {dnsAPI
              ? "mailio keeps these current through the DNS API, as last saved."
              : "Add these at the domain's DNS provider, as last saved."}
          </p>
          {domain.records.map((r) => (
            <div
              key={r.type + r.name + r.data}
              className="flex flex-col gap-0.5"
            >
              <span className="text-muted">
                {r.type} <span className="font-mono text-fg">{r.name}</span>
              </span>
              <code className="font-mono text-xs break-all select-all">
                {r.data}
              </code>
            </div>
          ))}
        </div>
      </form>
    </Dialog>
  );
}
