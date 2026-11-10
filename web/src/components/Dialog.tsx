import {
  createContext,
  useContext,
  useEffect,
  useId,
  useRef,
  type ReactNode,
  type RefObject,
} from "react";

import { lockScroll } from "@app/components/scrollLock";

/**
 * The dialog a piece of the tree is inside, if it is inside one.
 *
 * A ref rather than the element, because the provider is the dialog itself and its element does
 * not exist until after the first render; by the time a child's effect reads .current, it does.
 */
const DialogContext = createContext<RefObject<HTMLDialogElement | null> | null>(
  null,
);

/**
 * A modal, on the native dialog element, at every size.
 *
 * Native rather than a div with role="dialog": showModal() brings focus trapping, Escape,
 * inertness of the rest of the page, and top-layer stacking that no z-index can lose an
 * argument with. Every one of those is tedious to reimplement and easy to reimplement slightly
 * wrong.
 *
 * Controlled, because the element's own open state is DOM state and two sources of truth for
 * one boolean is how a dialog ends up shut in React and open on screen.
 */
export function Dialog({
  open,
  onClose,
  title,
  lead,
  aside,
  actions,
  children,
  footer,
  wide = false,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  /** Before the title, on its line: where what the dialog holds lives, like a task's project. */
  lead?: ReactNode;
  /** What the title is about — an id, a state — on the title's own line rather than on a row of
   *  its own underneath it. */
  aside?: ReactNode;
  /** Buttons at the right of the title bar, beside the close: a switch between two faces of
   *  the same dialog, rather than one more thing to do to what is in it. */
  actions?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  /** A description is markdown with images in it, and needs room to be worth writing in. */
  wide?: boolean;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  const titleID = useId();
  const parent = useContext(DialogContext);

  // Where the press started. A click on the backdrop targets the dialog itself — but so does one
  // that began on text inside and finished outside, which is what selecting a description and
  // dragging past the edge looks like.
  const startedOnBackdrop = useRef(false);

  useEffect(() => {
    const dialog = ref.current;
    if (!dialog) return;
    // Guarded both ways: showModal on an open dialog throws, and close on a shut one fires a
    // second close event that would call onClose again.
    if (open && !dialog.open) {
      dialog.showModal();

      // showModal focuses the first control it finds whether or not that control wanted it,
      // and a ring on Delete reads as armed. A field says so with data-autofocus; where none
      // does, the dialog holds focus, and escape and the tab order still work from there with
      // nothing lit.
      //
      // data-autofocus rather than React's autoFocus, which is a call and not an attribute:
      // it runs while this is still display:none, where focusing anything is a no-op.
      const wants = dialog.querySelector("[data-autofocus]");
      if (wants instanceof HTMLElement) wants.focus();
      else dialog.focus();
    } else if (!open && dialog.open) dialog.close();
  }, [open]);

  useEffect(() => {
    if (!open) return;
    // Both are needed and neither covers the other: holding the page still leaves a parent
    // dialog free to scroll away underneath, and the reverse.
    const releasePage = lockScroll(document.body);
    const above = parent?.current;
    const releaseParent = above ? lockScroll(above) : undefined;
    return () => {
      releaseParent?.();
      releasePage();
    };
  }, [open, parent]);

  return (
    <dialog
      ref={ref}
      tabIndex={-1}
      aria-labelledby={titleID}
      // Mine, not one of my children's. close and cancel do not bubble in the DOM, and React
      // delivers them as though they did — so a picker opening from inside the editor and being
      // saved took the editor down with it.
      onClose={(e) => e.target === ref.current && onClose()}
      // Escape fires cancel before close. Routing through one path means there is one way out.
      onCancel={(e) => {
        if (e.target !== ref.current) return;
        e.preventDefault();
        onClose();
      }}
      onPointerDown={(e) => {
        startedOnBackdrop.current = e.target === e.currentTarget;
      }}
      onClick={(e) => {
        if (startedOnBackdrop.current && e.target === e.currentTarget) {
          onClose();
        }
        startedOnBackdrop.current = false;
      }}
      // hidden until it is open, because `flex` would otherwise win the argument against the
      // browser's own `dialog:not([open]) { display: none }` — a shut dialog then lays itself
      // out as a 3px sliver of border across whatever is behind it.
      //
      // The whole screen on a phone. A centred card there spends its margins on the page
      // behind it, which nobody is reading, and leaves the editor a slot to type into.
      //
      // m-auto is load-bearing above that: a modal dialog is centred with inset:0; margin:auto,
      // and Tailwind's preflight resets margin to 0, which leaves only the inset and drops it
      // in the corner.
      //
      // And h-fit rather than h-auto, for the other half of the same rule: a modal dialog is
      // laid out with both block insets at 0, and an absolutely positioned box with auto height
      // between two insets is stretched to fill them. Every card here was 85dvh tall whatever
      // was inside it — fit-content is a height, so the box is its contents and the margins go
      // back to centring it.
      className={`hidden h-dvh max-h-dvh w-dvw max-w-none flex-col overflow-hidden border-0 bg-bg p-0 text-fg backdrop:bg-black/50 focus:outline-none open:flex sm:m-auto sm:border sm:border-line sm:shadow-xl sm:h-fit sm:max-h-[85dvh] sm:rounded-xl ${
        wide
          ? "sm:w-[min(42rem,calc(100vw-2rem))]"
          : "sm:w-[min(28rem,calc(100vw-2rem))]"
      }`}
    >
      {/* Unmounted while closed, so a form inside starts empty rather than holding whatever was
          typed and abandoned last time. */}
      {open ? (
        <DialogContext.Provider value={ref}>
          {/*
            The body scrolls and the footer does not, so what a dialog asks for is never below
            the fold with nothing to press.

            overscroll-contain stops a touch scroll that reached the end carrying on into the
            page behind, where overflow:hidden on the body is not reliably enough on its own.
          */}
          {/* Outside the scroller, and ruled off from it.
              
              The title used to scroll away with the body, which on a long description meant a
              page of prose with nothing at the top of it saying what it belonged to. A rule
              under it says where the dialog's own furniture ends and its contents begin — the
              same line the footer draws, from the other end.

              A title bar rather than a panel: 49px around a 28px control, the same at every
              width. It was 57 on a phone and 65 above it, which on one short word and a close
              is a band of nothing with a rule under it.

              A way out that is always drawn, because the ones that are not always there leave
              the phone with none: below the breakpoint this fills the screen, so there is no
              backdrop to press beside it, and there is no Escape on a phone. Not every dialog
              has a Cancel in its footer, and the one that is hardest to leave is the editor,
              which has three buttons and none of them is "not this". */}
          <div className="flex shrink-0 items-center justify-between gap-3 border-b border-line px-4 py-2.5 sm:px-5">
            <div className="flex min-w-0 items-center gap-2">
              {lead}
              <h2 id={titleID} className="truncate text-base font-semibold">
                {title}
              </h2>
              {aside}
            </div>
            <div className="flex shrink-0 items-center gap-1.5">
              {actions}
              <button
                type="button"
                aria-label="Close"
                title="Close"
                onClick={onClose}
                // Still 28px, with more of it cross: at 16px the mark was 8px of a phone's screen.
                className="-mr-1 shrink-0 rounded-md p-1 text-faint hover:bg-fill hover:text-fg"
              >
                <svg
                  aria-hidden="true"
                  viewBox="0 0 20 20"
                  className="size-5"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.75"
                  strokeLinecap="round"
                >
                  <path d="M5 5l10 10M15 5L5 15" />
                </svg>
              </button>
            </div>
          </div>

          {/* flex-auto, not flex-1, here and in what fills it: a card's height is fitted to its
              contents, and Safari fits a flex-1 child at its basis of 0 — the body came out as its
              padding and nothing else. Growing from what it holds still takes the slack on a phone. */}
          <div className="flex min-h-0 flex-auto flex-col gap-4 overflow-y-auto overscroll-contain px-4 pt-4 sm:px-5">
            {children}
            {/* A box with a height rather than padding on the box that scrolls.

                Measured: this container's padding-bottom read 16px and contributed nothing at
                the end of a scroll — a flex scroll container drops it — so a long description
                stopped dead against the bottom edge. A child is in the flow and cannot be
                dropped, and the gap above it is part of the cushion. */}
            <div aria-hidden="true" className="h-2 shrink-0" />
          </div>
          {footer ? (
            <div className="flex shrink-0 flex-wrap items-center justify-end gap-2 border-t border-line px-4 pt-4 pb-5 sm:px-5">
              {footer}
            </div>
          ) : null}
        </DialogContext.Provider>
      ) : null}
    </dialog>
  );
}
