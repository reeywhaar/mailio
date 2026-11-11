import { cancelHeld, retryQueue, type Queued } from "@app/api/admin";
import { Button } from "@app/components/Button";
import { DeleteButton, Section, useSave, when } from "@app/pages/admin/shared";

/** Whether a queued message is still held for the time its sender chose. */
const held = (q: Queued) => q.release_at !== null && q.attempts === 0;

/**
 * What is waiting to be sent, and why it has not gone yet: held until the time its sender chose,
 * which can be unscheduled here as in the sender's Scheduled, or tried and to be tried again.
 * Retry now leaves held mail to its time.
 */
export function Queue({ queue }: { queue: Queued[] }) {
  const { save, busy, error } = useSave();
  return (
    <Section
      title="Queue"
      action={
        queue.length ? (
          <Button disabled={busy} onClick={() => void save(retryQueue)}>
            Retry now
          </Button>
        ) : undefined
      }
    >
      {queue.length ? (
        <ul className="divide-y divide-line rounded-md border border-line bg-bg text-sm">
          {queue.map((q) => (
            <li key={q.id} className="flex items-center gap-3 px-3 py-2">
              <span className="flex min-w-0 flex-1 flex-col gap-0.5">
                <span className="break-words">
                  {q.sender} →{" "}
                  <span className="font-medium">{q.recipient}</span>
                </span>
                <span className="text-muted">
                  {held(q) ? (
                    <>Held until: {when(q.release_at!)}</>
                  ) : (
                    <>
                      Attempts: {q.attempts}
                      {q.last_error ? ` · ${q.last_error}` : ""}
                    </>
                  )}
                </span>
              </span>
              {held(q) ? (
                <DeleteButton
                  size="bar"
                  label="Unschedule…"
                  what="held mail"
                  title="Unschedule"
                  confirm="Unschedule"
                  facts={[
                    ["From", q.sender],
                    ["Held until", when(q.release_at!)],
                  ]}
                  note="It is not sent, to any of its recipients, and leaves its sender's Scheduled."
                  onDelete={() => cancelHeld(q.message_id)}
                />
              ) : null}
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-sm text-faint">Nothing waiting to be sent.</p>
      )}
      {error ? <p className="text-sm text-accent">{error}</p> : null}
    </Section>
  );
}
