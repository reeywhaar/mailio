import { useState, type FormEvent } from "react";

import {
  setSettings,
  type Account,
  type DMARC,
  type SPF,
  type Settings,
  type Unknown,
} from "@app/api/admin";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { Field, Select } from "@app/components/Field";
import {
  Section,
  dmarcLabels,
  spfLabels,
  unknownLabels,
  unknownSummary,
  useDraft,
  useSave,
} from "@app/pages/admin/shared";

/** What every domain does and publishes unless its own settings say otherwise. */
export function Defaults({
  settings,
  accounts,
}: {
  settings: Settings;
  accounts: Account[];
}) {
  const [open, setOpen] = useState(false);
  return (
    <Section
      title="Defaults"
      action={<Button onClick={() => setOpen(true)}>Change…</Button>}
    >
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
        <dt className="text-muted">Unknown addresses</dt>
        <dd>
          {unknownSummary(
            settings.unknown,
            settings.unknown_account_id,
            accounts,
          )}
        </dd>
        <dt className="text-muted">DMARC</dt>
        <dd>{dmarcLabels[settings.dmarc]}</dd>
        <dt className="text-muted">SPF</dt>
        <dd>{spfLabels[settings.spf]}</dd>
      </dl>
      <DefaultsDialog
        open={open}
        onClose={() => setOpen(false)}
        settings={settings}
        accounts={accounts}
      />
    </Section>
  );
}

function DefaultsDialog({
  open,
  onClose,
  settings,
  accounts,
}: {
  open: boolean;
  onClose: () => void;
  settings: Settings;
  accounts: Account[];
}) {
  const { save, busy, error } = useSave();
  const [draft, setDraft] = useDraft(open, () => ({
    ...settings,
    unknown_account_id: settings.unknown_account_id ?? accounts[0]?.id ?? null,
  }));
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const ok = await save(() =>
      setSettings({
        ...draft,
        unknown_account_id:
          draft.unknown === "junk" ? draft.unknown_account_id : null,
      }),
    );
    if (ok) onClose();
  };
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Defaults"
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            form="defaults"
            variant="solid"
            disabled={
              busy || (draft.unknown === "junk" && !draft.unknown_account_id)
            }
          >
            Save
          </Button>
        </>
      }
    >
      <form id="defaults" className="flex flex-col gap-3" onSubmit={submit}>
        <p className="text-sm text-muted">
          Every domain follows these unless its own settings say otherwise.
        </p>
        <Field label="Unknown addresses">
          <Select
            value={draft.unknown}
            onChange={(e) =>
              setDraft({ ...draft, unknown: e.target.value as Unknown })
            }
          >
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
              value={draft.unknown_account_id ?? ""}
              onChange={(e) =>
                setDraft({ ...draft, unknown_account_id: e.target.value })
              }
            >
              {accounts.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.address}
                </option>
              ))}
            </Select>
          </Field>
        ) : null}
        <Field label="DMARC">
          <Select
            value={draft.dmarc}
            onChange={(e) =>
              setDraft({ ...draft, dmarc: e.target.value as DMARC })
            }
          >
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
            onChange={(e) => setDraft({ ...draft, spf: e.target.value as SPF })}
          >
            {(Object.keys(spfLabels) as SPF[]).map((p) => (
              <option key={p} value={p}>
                {spfLabels[p]}
              </option>
            ))}
          </Select>
        </Field>
        {error ? <p className="text-sm text-accent">{error}</p> : null}
      </form>
    </Dialog>
  );
}
