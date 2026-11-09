import type { ReactNode, SelectHTMLAttributes } from "react";

import { fieldLook, fieldSizes } from "@app/components/TextField";

/** A control and the label that names it, which is also what a click on the words focuses. */
export function Field({
  label,
  hint,
  className = "",
  children,
}: {
  label: string;
  hint?: string;
  className?: string;
  children: ReactNode;
}) {
  // The hint outside the label, or a screen reader names the field by its hint as well.
  return (
    <div className={`flex flex-col gap-1 ${className}`}>
      <label className="flex flex-col gap-1">
        <span className="caps text-sm text-muted">{label}</span>
        {children}
      </label>
      {hint ? <span className="text-xs text-faint">{hint}</span> : null}
    </div>
  );
}

// Mobile Safari replaces a select's min-height with its own unless it has a height.
const selectHeights = {
  field: "h-10",
  bar: "h-8 pointer-coarse:h-10",
} as const;

/** The platform's own select, dressed as a field. */
export function Select({
  size = "field",
  className = "",
  ...props
}: Omit<SelectHTMLAttributes<HTMLSelectElement>, "size"> & {
  size?: keyof typeof fieldSizes;
}) {
  return (
    <select
      className={`${fieldLook} ${fieldSizes[size]} ${selectHeights[size]} ${className}`}
      {...props}
    />
  );
}
