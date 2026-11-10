import { configure } from "@testing-library/react";

// The first render in a file is cold, and on a busy machine it can pass testing-library's one
// second to wait for something that was on its way.
configure({ asyncUtilTimeout: 5000 });

/**
 * jsdom has no modal dialog: showModal and close are missing, and Dialog calls both. Only what the
 * component needs — open flipped, and close firing the event it listens for; focus trapping and
 * the top layer are a browser's to answer.
 */
function showModal(this: HTMLDialogElement) {
  this.open = true;
}

function close(this: HTMLDialogElement) {
  this.open = false;
  this.dispatchEvent(new Event("close"));
}

if (typeof HTMLDialogElement !== "undefined") {
  HTMLDialogElement.prototype.showModal ??= showModal;
  HTMLDialogElement.prototype.close ??= close;
}
