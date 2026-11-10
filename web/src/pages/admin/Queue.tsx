import { retryQueue, type Queued } from "@app/api/admin";
import { Button } from "@app/components/Button";
import { Section, useSave, when } from "@app/pages/admin/shared";

/**
 * What is waiting to be sent, and why it has not gone yet: held until the time its sender chose,
 * or tried and to be tried again. Retry now leaves held mail to its time.
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
            <li key={q.id} className="flex flex-col gap-0.5 px-3 py-2">
              <span>
                {q.sender} → <span className="font-medium">{q.recipient}</span>
              </span>
              <span className="text-muted">
                {q.release_at && q.attempts === 0 ? (
                  <>Held until: {when(q.release_at)}</>
                ) : (
                  <>
                    Attempts: {q.attempts}
                    {q.last_error ? ` · ${q.last_error}` : ""}
                  </>
                )}
              </span>
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
