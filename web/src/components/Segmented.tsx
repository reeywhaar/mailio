/**
 * One of a few choices side by side across the width, the chosen one raised: radios that look
 * like a switch.
 */
export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: { value: T; label: string; disabled?: boolean }[];
  onChange: (value: T) => void;
}) {
  return (
    <div
      role="radiogroup"
      aria-label={label}
      className="flex w-full rounded-md border border-line bg-fill p-0.5"
    >
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={o.value === value}
          disabled={o.disabled}
          onClick={() => onChange(o.value)}
          className={`min-h-8 flex-1 rounded px-3 text-sm disabled:opacity-50 pointer-coarse:min-h-10 ${
            o.value === value
              ? "bg-bg text-fg shadow-sm"
              : "text-muted hover:text-fg"
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}
