/**
 * Leaving this page for another document.
 *
 * A module of its own so a test can stand in for it: jsdom does not navigate.
 */
export function leaveFor(path: string) {
  window.location.assign(path);
}
