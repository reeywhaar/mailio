/**
 * Holding one element still while something is open on top of it.
 *
 * showModal() puts the dialog in the top layer and makes the rest of the document inert, which
 * sounds like it covers this and does not: inertness is about focus and pointer targeting, not
 * the wheel. A wheel over the backdrop scrolls the page underneath by the full delta.
 *
 * Counted rather than set and unset, because two dialogs can be open at once and the inner one
 * closing must not hand the page back its scroll while the outer is still up.
 */
type Held = { count: number; overflow: string; paddingRight: string };

const held = new Map<HTMLElement, Held>();

/**
 * Stops el scrolling until the returned function is called. Calling it twice does nothing the
 * second time: React runs cleanup on unmount and again on every re-run, and in development
 * deliberately mounts, unmounts and remounts to catch exactly this.
 */
export function lockScroll(el: HTMLElement): () => void {
  const already = held.get(el);
  if (already) {
    already.count += 1;
  } else {
    // Read before anything changes: the gap is the width the scrollbar was taking, and it is
    // zero the moment overflow goes hidden.
    const gap = scrollbarWidth(el);
    held.set(el, {
      count: 1,
      overflow: el.style.overflow,
      paddingRight: el.style.paddingRight,
    });
    el.style.overflow = "hidden";
    if (gap > 0) {
      const padding = Number.parseFloat(getComputedStyle(el).paddingRight) || 0;
      el.style.paddingRight = `${padding + gap}px`;
    }
  }

  let released = false;
  return () => {
    if (released) return;
    released = true;

    const lock = held.get(el);
    if (!lock) return;
    lock.count -= 1;
    if (lock.count > 0) return;

    held.delete(el);
    // Restored rather than cleared, so this composes with an element that had its own overflow.
    el.style.overflow = lock.overflow;
    el.style.paddingRight = lock.paddingRight;
  };
}

/**
 * The body is the exception and has to be: the page's scrollbar belongs to the viewport rather
 * than to the body box, so measuring the body against itself reports nothing.
 */
function scrollbarWidth(el: HTMLElement): number {
  if (el === document.body || el === document.documentElement) {
    return window.innerWidth - document.documentElement.clientWidth;
  }
  return el.offsetWidth - el.clientWidth;
}
