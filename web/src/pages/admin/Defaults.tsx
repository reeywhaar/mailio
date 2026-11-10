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
  LimitField,
  Section,
  TrashField,
  daysText,
  daysTyped,
  toDays,
  trashKept,
  dmarcLabels,
  limitTyped,
  mbText,
  megabytes,
  spfLabels,
  toBytes,
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
          ["Catch-all", unknownLabels[settings.unknown]],
          ["DMARC", dmarcLabels[settings.dmarc]],
          ["SPF", spfLabels[settings.spf]],
          ["Largest message", megabytes(settings.max_message_bytes)],
          ["Trash kept", trashKept(settings.trash_days)],
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
  const [draft, setDraft] = useDraft(open, () => ({
    ...settings,
    limit: mbText(settings.max_message_bytes),
    trash: daysText(settings.trash_days),
  }));
  const trashDays = toDays(draft.trash);
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const { limit, trash, ...set } = draft;
    const ok = await save(() =>
      setSettings({
        ...set,
        max_message_bytes: toBytes(limit),
        trash_days: toDays(trash) ?? settings.trash_days,
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
              busy ||
              !limitTyped(draft.limit) ||
              trashDays === null ||
              !daysTyped(draft.trash)
            }
          >
            Save
          </Button>
        </>
      }
    >
      <form id="defaults" className="flex flex-col gap-3" onSubmit={submit}>
        <Field
          label="Catch-all"
          hint="Mail for addresses nobody has. Forward and junk go to an account, chosen per domain."
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
        <LimitField
          value={draft.limit}
          onChange={(limit) => setDraft({ ...draft, limit })}
          hint="For mail received, sent and stored. A domain or an account can have its own."
        />
        <TrashField
          value={draft.trash}
          onChange={(trash) => setDraft({ ...draft, trash })}
          hint="Then deleted for good. 0: forever. A domain or an account can have its own."
        />
        {error ? <p className="text-sm text-accent">{error}</p> : null}
      </form>
    </Dialog>
  );
}
