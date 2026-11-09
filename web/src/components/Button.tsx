import type { ComponentProps } from "react";

/**
 * Four variants, and choosing between them is the whole decision.
 *
 * solid is the brand color, which is the one thing on a screen of greys that is this
 * application rather than the browser's furniture, and so there is one of it in a view.
 */
export type Variant = "solid" | "quiet" | "link" | "danger";

/**
 * How tall, which is a question about what the button is standing next to.
 *
 * field is beside an input and matches it. bar is a row of controls with no field in it — and
 * a finger is still a finger, so the height comes back below a pointer that is not one.
 */
export type Size = "field" | "bar";

const shapes: Record<Size, string> = {
  field: "min-h-10 px-3 py-1.5 text-sm",
  bar: "min-h-8 px-2.5 py-1 text-sm pointer-coarse:min-h-10",
};

const styles: Record<Variant, string> = {
  solid: "bg-brand text-brand-ink hover:opacity-90",
  quiet: "border border-line bg-bg text-fg hover:bg-fill",
  link: "text-muted underline-offset-2 hover:text-fg hover:underline",
  danger: "border border-line bg-bg text-accent hover:bg-fill",
};

/** A button's look, for a link that moves somewhere to wear as well. */
export function buttonLook(variant: Variant = "quiet", size: Size = "field") {
  const shape =
    variant === "link"
      ? "text-sm"
      : `${shapes[size]} justify-center rounded-md`;
  return `inline-flex items-center gap-1.5 disabled:opacity-50 ${shape} ${styles[variant]}`;
}

/** A label ends in "…" when the command stops to ask, and not when it acts on the press. */
export function Button({
  variant = "quiet",
  size = "field",
  className = "",
  ...props
}: ComponentProps<"button"> & {
  variant?: Variant;
  size?: Size;
}) {
  return (
    <button
      type="button"
      className={`${buttonLook(variant, size)} ${className}`}
      {...props}
    />
  );
}
