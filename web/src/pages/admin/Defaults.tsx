import { useState, type FormEvent } from "react";

import {
  setSettings,
  type DMARC,
  type DefaultUnknown,
  type SPF,
  type Settings,
} from "@app/api/admin";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { Field, Select } from "@app/components/Field";
import {
  Facts,
  Section,
  dmarcLabels,
  spfLabels,
  unknownLabels,
  useDraft,
  useSave,
} from "@app/pages/admin/shared";

const defaultUnknowns: DefaultUnknown[] = ["reject", "drop"];

/** What every domain does and publishes unless its own settings say otherwise. */
export function Defaults({ settings }: { settings: Settings }) {
  const [open, setOpen] = useState(false);
  return (
    <Section
      title="Defaults"
      action={<Button onClick={() => setOpen(true)}>Change…</Button>}
    >
      <Facts
        items={[
          ["Unknown addresses", unknownLabels[settings.unknown]],
          ["DMARC", dmarcLabels[settings.dmarc]],
          ["SPF", spfLabels[settings.spf]],
        ]}
      />
      <DefaultsDialog
        open={open}
        onClose={() => setOpen(false)}
        settings={settings}
      />
    </Section>
  );
}

function DefaultsDialog({
  open,
  onClose,
  settings,
}: {
  open: boolean;
  onClose: () => void;
  settings: Settings;
}) {
  const { save, busy, error } = useSave();
  const [draft, setDraft] = useDraft(open, () => settings);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (await save(() => setSettings(draft))) onClose();
  };
  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Defaults"
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" form="defaults" variant="solid" disabled={busy}>
            Save
          </Button>
        </>
      }
    >
      <form id="defaults" className="flex flex-col gap-3" onSubmit={submit}>
        <Field
          label="Unknown addresses"
          hint="Junk to an account is chosen per domain."
        >
          <Select
            value={draft.unknown}
            onChange={(e) =>
              setDraft({ ...draft, unknown: e.target.value as DefaultUnknown })
            }
          >
            {defaultUnknowns.map((u) => (
              <option key={u} value={u}>
                {unknownLabels[u]}
              </option>
            ))}
          </Select>
        </Field>
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
