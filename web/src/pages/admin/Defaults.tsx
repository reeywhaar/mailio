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
  KeptField,
  daysText,
  daysTyped,
  toDays,
  daysKept,
  dmarcHint,
  dmarcLabels,
  limitTyped,
  mbText,
  megabytes,
  spfHint,
  spfLabels,
  toBytes,
  unknownLabels,
  useDraft,
  useSave,
} from "@app/pages/admin/shared";

// A default's days are required: there is nothing above it to follow.
const daysSet = (text: string) => daysTyped(text) && toDays(text) !== null;

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
          ["Trash kept", daysKept(settings.trash_days)],
          ["Junk kept", daysKept(settings.junk_days)],
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
    junk: daysText(settings.junk_days),
  }));
  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const { limit, trash, junk, ...set } = draft;
    const ok = await save(() =>
      setSettings({
        ...set,
        max_message_bytes: toBytes(limit),
        trash_days: toDays(trash) ?? settings.trash_days,
        junk_days: toDays(junk) ?? settings.junk_days,
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
              !daysSet(draft.trash) ||
              !daysSet(draft.junk)
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
        <Field label="DMARC" hint={dmarcHint}>
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
        <Field label="SPF" hint={spfHint}>
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
        <KeptField
          box="Trash"
          value={draft.trash}
          onChange={(trash) => setDraft({ ...draft, trash })}
          hint="Then deleted for good. 0: forever."
        />
        <KeptField
          box="Junk"
          value={draft.junk}
          onChange={(junk) => setDraft({ ...draft, junk })}
          hint="Then deleted for good. 0: forever."
        />
        {error ? <p className="text-sm text-accent">{error}</p> : null}
      </form>
    </Dialog>
  );
}
