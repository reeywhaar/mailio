import { useEffect, useRef, useState, type ReactNode } from "react";

/** Values to be pasted somewhere else, each with a button at its right that copies it. */
export function CopyBlock({
  title,
  aside,
  rows,
  children,
}: {
  title?: string;
  /** At the right of the title: what the values are found to be. */
  aside?: ReactNode;
  rows: { label: string; value: string }[];
  /** Below the values. */
  children?: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-1.5 rounded-md border border-line bg-surface py-2 pr-1.5 pl-3 text-sm">
      {title || aside ? (
        <div className="flex items-center gap-2 pr-1.5">
          <span className="caps text-faint">{title}</span>
          <span className="flex-1" />
          {aside}
        </div>
      ) : null}
      {rows.map((r) => (
        <CopyRow key={r.label} {...r} />
      ))}
      {children}
    </div>
  );
}

function CopyRow({ label, value }: { label: string; value: string }) {
  const [copied, setCopied] = useState(false);
  const button = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1500);
    return () => clearTimeout(t);
  }, [copied]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
    } catch {
      const host = button.current?.parentElement;
      if (host && copyByCommand(value, host)) setCopied(true);
    }
  };

  return (
    <div className="flex items-start gap-2">
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="text-muted">{label}</span>
        <code className="font-mono break-all">{value}</code>
      </div>
      <button
        ref={button}
        type="button"
        aria-label={`Copy ${label.toLowerCase()}`}
        title={copied ? "Copied" : "Copy"}
        onClick={() => void copy()}
        className="mt-0.5 shrink-0 rounded-md p-1.5 text-faint hover:bg-fill hover:text-fg"
      >
        <svg
          aria-hidden="true"
          viewBox="0 0 20 20"
          className="size-4"
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinecap="round"
          strokeLinejoin="round"
        >
          {copied ? (
            <path d="M4 10.5l4 4 8-9" />
          ) : (
            <>
              <rect x="7" y="7" width="10" height="10" rx="2" />
              <path d="M13 7V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v6a2 2 0 0 0 2 2h2" />
            </>
          )}
        </svg>
      </button>
    </div>
  );
}

/**
 * The clipboard API exists only on https and localhost, and the admin interface may be opened over
 * plain http on a private network. The old command still copies there, from a field nobody sees,
 * put beside the button: a modal dialog makes everything outside it unselectable.
 */
function copyByCommand(value: string, host: HTMLElement) {
  const area = document.createElement("textarea");
  area.value = value;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.opacity = "0";
  host.append(area);
  area.select();
  try {
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    area.remove();
  }
}
