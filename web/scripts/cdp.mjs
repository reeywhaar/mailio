/**
 * A headless Chromium over the DevTools protocol, and nothing else.
 *
 * For the screenshot capture: Chromium has a headless mode and Node has a WebSocket client, so
 * there is nothing here for Playwright to do.
 */
import { spawn } from "node:child_process";
import { accessSync, constants } from "node:fs";

/** The first browser that is there, or CHROMIUM when it is set. */
export function findChromium() {
  if (process.env.CHROMIUM) return process.env.CHROMIUM;
  return [
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
    "/usr/bin/google-chrome",
  ].find((candidate) => {
    try {
      accessSync(candidate, constants.X_OK);
      return true;
    } catch {
      return false;
    }
  });
}

/** One CDP connection, with commands addressed to a page session. */
export class Browser {
  #ws;
  #next = 1;
  #pending = new Map();
  #session;

  /** @param {{ chromium: string, port: number, profile: string, base: string }} options */
  static async launch({ chromium, port, profile, base }) {
    const proc = spawn(
      chromium,
      [
        "--headless=new",
        `--remote-debugging-port=${port}`,
        `--user-data-dir=${profile}`,
        "--no-first-run",
        "--no-default-browser-check",
        "--hide-scrollbars",
        "--force-color-profile=srgb",
        "--disable-gpu",
        // The instance under test serves http on a loopback address.
        "--ignore-certificate-errors",
        // A window the system counts as covered, as every one is once the screen sleeps, holds
        // hidden pages that draw no frames, and a run waiting on one waits forever.
        "--disable-backgrounding-occluded-windows",
        "--disable-renderer-backgrounding",
        "--disable-background-timer-throttling",
        // Nothing here reads chromium's output, and a pipe nobody drains fills and blocks the
        // process writing into it — which looks like a browser that never finished starting.
      ],
      { stdio: "ignore" },
    );
    proc.on("error", (err) => {
      console.error(`could not start ${chromium}: ${err.message}`);
      process.exit(2);
    });

    const endpoint = await waitFor(async () => {
      const res = await fetch(`http://127.0.0.1:${port}/json/version`);
      return (await res.json()).webSocketDebuggerUrl;
    });

    const browser = new Browser();
    browser.base = base;
    await browser.#connect(endpoint);
    await browser.#attach();
    browser.proc = proc;
    return browser;
  }

  async #connect(endpoint) {
    this.#ws = new WebSocket(endpoint);
    await new Promise((resolve, reject) => {
      this.#ws.onopen = resolve;
      this.#ws.onerror = reject;
    });
    this.#ws.onmessage = (event) => {
      const msg = JSON.parse(event.data);
      const waiting = this.#pending.get(msg.id);
      if (!waiting) return;
      this.#pending.delete(msg.id);
      if (msg.error) waiting.reject(new Error(msg.error.message));
      else waiting.resolve(msg.result);
    };
  }

  async #attach() {
    const { targetId } = await this.send("Target.createTarget", {
      url: "about:blank",
    });
    const { sessionId } = await this.send("Target.attachToTarget", {
      targetId,
      flatten: true,
    });
    this.#session = sessionId;
    await this.send("Page.enable");
    await this.send("Runtime.enable");
  }

  send(method, params = {}) {
    const id = this.#next++;
    const message = { id, method, params };
    if (this.#session && !method.startsWith("Target."))
      message.sessionId = this.#session;
    this.#ws.send(JSON.stringify(message));
    return new Promise((resolve, reject) =>
      this.#pending.set(id, { resolve, reject }),
    );
  }

  /** Navigates and waits for the app to have rendered something. */
  async open(path) {
    await this.send("Page.navigate", { url: this.base + path });
    await waitFor(async () => {
      const value = await this.eval(
        "document.querySelector('#root')?.children.length ?? 0",
      );
      return value > 0 ? value : undefined;
    });
  }

  /** Sets a cookie for the app's own origin, as if it had answered with it. */
  cookie(name, value) {
    return this.send("Network.setCookie", { name, value, url: this.base });
  }

  async eval(expression) {
    const { result, exceptionDetails } = await this.send("Runtime.evaluate", {
      expression: `(() => { return (${expression}); })()`,
      awaitPromise: true,
      returnByValue: true,
    });
    if (exceptionDetails)
      throw new Error(
        exceptionDetails.exception?.description ?? exceptionDetails.text,
      );
    return result.value;
  }

  scheme(value) {
    return this.send("Emulation.setEmulatedMedia", {
      features: [{ name: "prefers-color-scheme", value }],
    });
  }

  viewport(width, height, scale = 1) {
    return this.send("Emulation.setDeviceMetricsOverride", {
      width,
      height,
      deviceScaleFactor: scale,
      mobile: false,
    });
  }

  /** A PNG of the viewport, or of the region given in CSS pixels. */
  async png(clip) {
    const { data } = await this.send("Page.captureScreenshot", {
      format: "png",
      ...(clip ? { clip: { ...clip, scale: 1 } } : {}),
    });
    return Buffer.from(data, "base64");
  }

  close() {
    this.#ws.close();
    this.proc.kill();
  }
}

/** Polls until f returns something, for about fifteen seconds. */
export async function waitFor(f, attempts = 60) {
  for (let i = 0; i < attempts; i++) {
    try {
      const value = await f();
      if (value !== undefined && value !== null && value !== false)
        return value;
    } catch {
      // Not up yet.
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error("gave up waiting");
}
