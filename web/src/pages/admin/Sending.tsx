import { useState, type FormEvent } from "react";

import {
  createRelay,
  deleteRelay,
  setSettings,
  testRelay,
  updateRelay,
  type Relay,
  type Security,
  type Settings,
} from "@app/api/admin";
import { Button } from "@app/components/Button";
import { Dialog } from "@app/components/Dialog";
import { Field, Select } from "@app/components/Field";
import { TextField } from "@app/components/TextField";
import {
  DeleteButton,
  Section,
  relayName,
  useDraft,
  useSave,
} from "@app/pages/admin/shared";

/** How mail goes out: a line saying so, and the relays behind a button. */
export function Sending({
  settings,
  relays,
}: {
  settings: Settings;
  relays: Relay[];
}) {
  const [open, setOpen] = useState(false);
  return (
    <Section
      title="Sending"
      action={<Button onClick={() => setOpen(true)}>Configure sending…</Button>}
    >
      <p className="text-sm text-muted">
        {settings.relay_id
          ? `Mail goes out through ${relayName(settings.relay_id, relays)}`
          : "Mail goes out built in, straight to each recipient's server, which many hosting providers block on port 25"}
        , unless a domain's own settings say otherwise.
      </p>
      <SendingDialog
        open={open}
        onClose={() => setOpen(false)}
        settings={settings}
        relays={relays}
      />
    </Section>
  );
}

/**
 * The relays, each added or changed in a dialog of its own, and the default among them, held
 * until Save.
 */
function SendingDialog({
  open,
  onClose,
  settings,
  relays,
}: {
  open: boolean;
  onClose: () => void;
  settings: Settings;
  relays: Relay[];
}) {
  const { save, busy, error } = useSave();
  const [relay, setRelay] = useDraft(open, () => settings.relay_id ?? "");
  // The relay being edited; null adds one. undefined is no dialog.
  const [editing, setEditing] = useState<Relay | null | undefined>(undefined);
  const [tested, setTested] = useState<{ id: string; text: string } | null>(
    null,
  );

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const ok = await save(() =>
      setSettings({ ...settings, relay_id: relay || null }),
    );
    if (ok) onClose();
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title="Sending"
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button type="submit" form="sending" variant="solid" disabled={busy}>
            Save
          </Button>
        </>
      }
    >
      <div className="flex flex-col gap-3 text-sm">
        <div className="flex items-center gap-2">
          <span className="caps text-muted">Relays</span>
          <span className="flex-1" />
          <Button size="bar" disabled={busy} onClick={() => setEditing(null)}>
            Add relay
          </Button>
        </div>
        {relays.length ? (
          <ul className="divide-y divide-line rounded-md border border-line bg-bg">
            {relays.map((r) => (
              <li key={r.id} className="flex flex-col gap-1 px-3 py-2">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
                  <span className="font-medium">{r.label}</span>
                  <span className="ml-auto flex items-center gap-2">
                    <Button
                      size="bar"
                      disabled={busy}
                      onClick={() => {
                        setTested(null);
                        void save(() => testRelay(r.id)).then(
                          (ok) =>
                            ok &&
                            setTested({
                              id: r.id,
                              text: "It accepted the sign-in.",
                            }),
                        );
                      }}
                    >
                      Test
                    </Button>
                    <Button
                      size="bar"
                      disabled={busy}
                      onClick={() => setEditing(r)}
                    >
                      Edit…
                    </Button>
                    <DeleteButton
                      size="bar"
                      disabled={busy}
                      onConfirm={() => void save(() => deleteRelay(r.id))}
                    />
                  </span>
                </div>
                <code className="font-mono text-xs text-muted">
                  {r.host}:{r.port}
                </code>
                {tested?.id === r.id ? (
                  <span className="text-muted">{tested.text}</span>
                ) : null}
              </li>
            ))}
          </ul>
        ) : (
          <p className="text-faint">No relays yet.</p>
        )}
        <form
          id="sending"
          onSubmit={submit}
          className="border-t border-line pt-3"
        >
          <Field label="Default for every domain">
            <Select value={relay} onChange={(e) => setRelay(e.target.value)}>
              <option value="">
                Built in: straight to each recipient's server
              </option>
              {relays.map((r) => (
                <option key={r.id} value={r.id}>
                  Through {r.label}
                </option>
              ))}
            </Select>
          </Field>
        </form>
        {error ? <p className="text-accent">{error}</p> : null}
      </div>
      <RelayDialog
        open={editing !== undefined}
        relay={editing ?? null}
        onClose={() => setEditing(undefined)}
      />
    </Dialog>
  );
}

/**
 * Adding a relay, or changing one. The password is never shown: left empty, the stored one is
 * kept.
 */
function RelayDialog({
  open,
  relay,
  onClose,
}: {
  open: boolean;
  relay: Relay | null;
  onClose: () => void;
}) {
  const { save, busy, error } = useSave();
  const [draft, setDraft] = useDraft(open, () => ({
    label: relay?.label ?? "",
    host: relay?.host ?? "",
    port: String(relay?.port ?? 587),
    security: relay?.security ?? ("starttls" as Security),
    username: relay?.username ?? "",
    password: "",
  }));
  const set = (change: Partial<typeof draft>) =>
    setDraft({ ...draft, ...change });

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const input = { ...draft, port: Number(draft.port) };
    const ok = await save(() =>
      relay ? updateRelay(relay.id, input) : createRelay(input),
    );
    if (ok) onClose();
  };

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={relay ? `Change ${relay.label}` : "Add a relay"}
      footer={
        <>
          <Button onClick={onClose}>Cancel</Button>
          <Button
            type="submit"
            form="relay"
            variant="solid"
            disabled={busy || !draft.label.trim() || !draft.host.trim()}
          >
            Save
          </Button>
        </>
      }
    >
      <form id="relay" className="flex flex-col gap-3" onSubmit={submit}>
        <Field label="Label">
          <TextField
            placeholder="e.g. Gmail"
            autoComplete="off"
            data-autofocus
            value={draft.label}
            onChange={(e) => set({ label: e.target.value })}
          />
        </Field>
        <div className="flex flex-wrap gap-2">
          <Field label="Host" className="min-w-40 flex-1">
            <TextField
              placeholder="smtp.relay.example"
              autoComplete="off"
              value={draft.host}
              onChange={(e) => set({ host: e.target.value })}
            />
          </Field>
          <Field label="Port" className="w-20">
            <TextField
              inputMode="numeric"
              value={draft.port}
              onChange={(e) => set({ port: e.target.value })}
            />
          </Field>
          <Field label="Encryption">
            <Select
              value={draft.security}
              onChange={(e) => set({ security: e.target.value as Security })}
            >
              <option value="starttls">STARTTLS</option>
              <option value="implicit">TLS</option>
            </Select>
          </Field>
        </div>
        <Field label="Username">
          <TextField
            autoComplete="off"
            value={draft.username}
            onChange={(e) => set({ username: e.target.value })}
          />
        </Field>
        <Field label="Password">
          <TextField
            type="password"
            autoComplete="new-password"
            placeholder={relay?.has_password ? "Unchanged" : ""}
            value={draft.password}
            onChange={(e) => set({ password: e.target.value })}
          />
        </Field>
        {error ? <p className="text-sm text-accent">{error}</p> : null}
      </form>
    </Dialog>
  );
}
