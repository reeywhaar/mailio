/**
 * One of a few choices side by side across the width, the chosen one raised on a thumb that slides
 * to it: radios that look like a switch.
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
  const chosen = Math.max(
    0,
    options.findIndex((o) => o.value === value),
  );
  return (
    <div
      role="radiogroup"
      aria-label={label}
      className="relative flex w-full rounded-md border border-line bg-fill p-0.5"
    >
      {/* The options are equal widths, so one thumb's width moves it one option along. */}
      <span
        aria-hidden
        className="absolute inset-y-0.5 left-0.5 rounded bg-bg shadow-sm transition-transform duration-200 ease-out motion-reduce:transition-none"
        style={{
          width: `calc((100% - 0.25rem) / ${options.length})`,
          transform: `translateX(${chosen * 100}%)`,
        }}
      />
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={o.value === value}
          disabled={o.disabled}
          onClick={() => onChange(o.value)}
          className={`relative min-h-8 flex-1 rounded px-3 text-sm transition-colors duration-200 disabled:opacity-50 motion-reduce:transition-none pointer-coarse:min-h-10 ${
            o.value === value ? "text-fg" : "text-muted hover:text-fg"
          }`}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}
