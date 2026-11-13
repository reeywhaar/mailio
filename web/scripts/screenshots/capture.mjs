#!/usr/bin/env node
//
// Regenerate the README screenshots.
//
//   npm --prefix web run screenshots
//
// Builds the admin interface and a Linux binary, and starts mailio in Docker on an empty data
// directory, in staging, with nothing outside resolving. Makes seed.mjs's relay, domains, accounts
// and aliases through the admin API and imports its mail as a Maildir, then photographs the admin
// interface in headless Chromium over the DevTools protocol and overwrites the PNGs in
// docs/screenshots. Everything it starts is stopped on the way out.
//
//   SCALE=2      device pixels per CSS pixel
//   THEME=light  or dark
//   OUT=dir      where the PNGs land, so a look at dark need not overwrite the committed set
//   ONLY=a,b     just these shots
//   TIMEOUT=30   seconds a shot may take before the run gives up, cleaning up as it goes
//
// Needs go, patch, node, docker and Chromium or Chrome.
import { execFileSync, spawnSync } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import {
  cpSync,
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  utimesSync,
  writeFileSync,
} from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { setTimeout as sleep } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { crc32, inflateSync } from "node:zlib";

import { Browser, findChromium, waitFor } from "../cdp.mjs";
import {
  accounts,
  admin,
  aliases,
  domains,
  host,
  messages,
  now,
  relays,
  tags,
} from "./seed.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "..", "..", "..");
const work = mkdtempSync(join(tmpdir(), "mailio-shots-"));

const SCALE = Number(process.env.SCALE ?? 2);
const THEME = process.env.THEME ?? "light";
const OUT = process.env.OUT ?? join(root, "docs", "screenshots");
const ONLY = (process.env.ONLY ?? "").split(",").filter(Boolean);
const TIMEOUT = Number(process.env.TIMEOUT ?? 30);

const ALPINE = "alpine:3";
// Unique to the run, so two never trip over each other.
const name = `mailio-shots-${randomBytes(3).toString("hex")}`;

let started = false;
let browser;
function cleanup() {
  browser?.close();
  if (started) spawnSync("docker", ["rm", "-f", name], { stdio: "ignore" });
  // Chromium is still writing its profile as it exits.
  rmSync(work, {
    recursive: true,
    force: true,
    maxRetries: 5,
    retryDelay: 300,
  });
}
process.on("exit", cleanup);
process.on("SIGINT", () => process.exit(1));
process.on("SIGTERM", () => process.exit(1));

const step = (message) => console.log(`==> ${message}`);
function die(message, detail) {
  console.error(message);
  if (detail) console.error(detail);
  process.exit(1);
}

function run(command, args, options = {}) {
  try {
    return execFileSync(command, args, {
      stdio: "pipe",
      encoding: "utf8",
      ...options,
    });
  } catch (err) {
    die(
      `${command} ${args.join(" ")} failed`,
      [err.stdout, err.stderr].join("\n"),
    );
  }
}
const docker = (...args) => run("docker", args).trim();

// --- build ---------------------------------------------------------------------------------

const chromium = findChromium();
if (!chromium) die("no Chromium or Chrome found; set CHROMIUM");
docker("version", "--format", "{{.Server.Version}}");

step("building the admin interface");
if (!existsSync(join(root, "web", "node_modules")))
  run("npm", ["ci"], { cwd: join(root, "web") });
run("npm", ["run", "build"], { cwd: join(root, "web") });

// For Linux in Docker, whatever this machine is: mailio listens on 25, 80, 465, 587 and 993, which
// a container keeps to itself.
step("building the binary");
run("sh", ["patch-packages.sh"], { cwd: root });
const app = join(work, "app");
mkdirSync(join(app, "web"), { recursive: true });
run("go", ["build", "-trimpath", "-o", join(app, "mailio"), "."], {
  cwd: root,
  env: {
    ...process.env,
    GOOS: "linux",
    GOARCH: docker("version", "--format", "{{.Server.Arch}}"),
    CGO_ENABLED: "0",
  },
});
cpSync(join(root, "web", "dist"), join(app, "web", "dist"), {
  recursive: true,
});

// --- the mail ------------------------------------------------------------------------------

// A Maildir like the Postfix-based mailio's, which the importer sorts to whoever each message
// was sent to, and Sent to whoever sent it. Each file is dated when its message arrived, which is
// the order the importer takes them in.
step("writing the mail");
const maildir = join(work, "maildir");
const moment = Date.parse(now);
const at = (ago) => new Date(moment - seconds(ago) * 1000);
const owners = new Set();
messages.forEach((m, i) => {
  const folder = m.folder ? join(maildir, `.${m.folder}`) : maildir;
  for (const sub of ["cur", "new", "tmp"])
    mkdirSync(join(folder, sub), { recursive: true });
  const file = m.unread
    ? join(folder, "new", `${i + 1}.screenshots`)
    : join(folder, "cur", `${i + 1}.screenshots:2,S`);
  writeFileSync(file, message(m, i));
  utimesSync(file, at(m.ago), at(m.ago));
  owners.add(addressIn(m.folder === "Sent" ? m.from : m.to));
});

function seconds(span) {
  const [, n, unit] =
    /^(\d+)([mhdw])$/.exec(span) ?? die(`not a span: ${span}`);
  return Number(n) * { m: 60, h: 3600, d: 86400, w: 604800 }[unit];
}

function addressIn(header) {
  return /<([^>]+)>/.exec(header)?.[1] ?? header;
}

/** The message as it was delivered: headers, then its text, and its attachment if it has one. */
function message(m, i) {
  const head = [
    `From: ${m.from}`,
    `To: ${m.to}`,
    `Subject: ${m.subject}`,
    `Date: ${at(m.ago).toUTCString()}`,
    `Message-ID: <${i + 1}@screenshots.example>`,
    "MIME-Version: 1.0",
  ];
  const text = [
    "Content-Type: text/plain; charset=utf-8",
    "Content-Transfer-Encoding: 8bit",
    "",
    m.body,
  ].join("\r\n");
  let body = text;
  if (m.attachment) {
    const a = m.attachment;
    const boundary = `=_screenshots_${i + 1}`;
    body = [
      `Content-Type: multipart/mixed; boundary="${boundary}"`,
      "",
      `--${boundary}`,
      text,
      `--${boundary}`,
      `Content-Type: ${a.type}; name="${a.name}"`,
      `Content-Disposition: attachment; filename="${a.name}"`,
      "Content-Transfer-Encoding: base64",
      "",
      Buffer.alloc(a.size, "mailio screenshots ")
        .toString("base64")
        .replace(/.{76}/g, "$&\r\n"),
      `--${boundary}--`,
      "",
    ].join("\r\n");
  }
  return [...head, body].join("\r\n").replace(/\r?\n/g, "\r\n");
}

// --- mailio --------------------------------------------------------------------------------

step("starting mailio");
const port = await free();
const BASE = `http://127.0.0.1:${port}`;
const data = join(work, "data");
mkdirSync(data);
started = true;
docker(
  "run",
  "-d",
  "--name",
  name,
  "--user",
  `${process.getuid()}:${process.getgid()}`,
  "--sysctl",
  "net.ipv4.ip_unprivileged_port_start=0",
  // Nothing outside resolves, so nothing is sent anywhere or looked up.
  "--dns",
  "127.0.0.1",
  "-p",
  `127.0.0.1:${port}:80`,
  "-v",
  `${app}:/app:ro`,
  "-v",
  `${data}:/data`,
  "-v",
  `${maildir}:/import:ro`,
  "-w",
  "/app",
  "-e",
  `MAILIO_PUBLIC_URL=${BASE}`,
  "-e",
  `MAILIO_MAIL_HOST=${host}`,
  "-e",
  "MAILIO_DATA_DIR=/data",
  "-e",
  `MAILIO_SECRET_KEY=${randomBytes(32).toString("base64")}`,
  "-e",
  "MAILIO_STAGING=true",
  ALPINE,
  "/app/mailio",
  "serve",
);

// An empty database has no admin, so serve prints the one way in.
const logs = () =>
  spawnSync("docker", ["logs", name], { encoding: "utf8" }).output.join("");
const token = await waitFor(
  () => /\/admin\/password#token=([^"\s]+)/.exec(logs())?.[1],
).catch(() => die("mailio printed no admin link", logs()));

/** What a page load answers with: the session's cookie, once signed in. */
let session;

/** One call as the signed-in admin. Any failure ends the run. */
async function api(method, path, body) {
  const res = await fetch(BASE + path, {
    method,
    headers: {
      ...(body ? { "Content-Type": "application/json" } : {}),
      ...(session ? { Cookie: `mailio_session=${session}` } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  for (const c of res.headers.getSetCookie()) {
    const [, value] = /^mailio_session=([^;]*)/.exec(c) ?? [];
    if (value) session = value;
  }
  const text = await res.text();
  if (!res.ok) die(`${method} ${path} answered ${res.status}`, text);
  return text ? JSON.parse(text) : null;
}

step("filling it in");
await api("POST", "/api/admin-link", {
  token: decodeURIComponent(token),
  ...admin,
});

const relayID = {};
for (const r of relays)
  relayID[r.label] = (await api("POST", "/api/relays", r)).id;
const defaults = await api("GET", "/api/settings");
await api("PUT", "/api/settings", {
  ...defaults,
  relay_id: relayID[relays[0].label],
});

const domainID = {};
for (const d of domains)
  domainID[d.name] = (await api("POST", "/api/domains", { name: d.name })).id;

const accountID = {};
for (const a of accounts) {
  const made = await api("POST", "/api/accounts", { address: a.address });
  accountID[a.address] = made.id;
  for (const label of a.credentials ?? [])
    await api("POST", `/api/accounts/${made.id}/credentials`, {
      label,
      password: "",
    });
}
// Once every account is there, since one forwards to another.
for (const { address, forward, credentials, relay, ...own } of accounts) {
  const change = {
    ...own,
    ...(forward ? { forward_account_id: accountID[forward] } : {}),
    ...(relay ? { relay: relayID[relay] ?? relay } : {}),
  };
  if (Object.keys(change).length)
    await api("PATCH", `/api/accounts/${accountID[address]}`, change);
}
for (const a of aliases)
  await api("POST", "/api/accounts", {
    address: a.address,
    alias_of: accountID[a.of],
  });
for (const { name: domain, unknown_account, ...own } of domains) {
  const change = {
    ...own,
    ...(unknown_account
      ? { unknown_account_id: accountID[unknown_account] }
      : {}),
  };
  if (Object.keys(change).length)
    await api("PATCH", `/api/domains/${domainID[domain]}`, change);
}

run("docker", [
  "exec",
  name,
  "/app/mailio",
  "import",
  "maildir",
  "/import",
  ...owners,
]);

// What each run makes up for itself, as each install does: the logins of credentials past the
// first, and every domain's DKIM key. Drawn as the same made-up values every run, so a run with
// nothing changed changes nothing.
const pinned = {};
let tag = 0;
for (const a of await api("GET", "/api/accounts"))
  for (const c of a.credentials)
    if (c.login.includes("+"))
      pinned[c.login] = c.login.replace(/\+[^@]+@/, `+${tags[tag++]}@`);
for (const d of await api("GET", "/api/domains"))
  for (const r of d.records) {
    // DKIM's p= alone: DMARC's says reject, which is everywhere else on the page too.
    if (!r.name.includes("._domainkey.")) continue;
    const key = /\bp=([A-Za-z0-9+/=]+)/.exec(r.data)?.[1];
    if (key) pinned[key] = madeUpKey(d.name, key.length);
  }

/** Base64 that reads as an RSA public key, the same for a domain every time. */
function madeUpKey(seed, length) {
  let out = "MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEA";
  for (let i = 0; out.length < length; i++)
    out += createHash("sha256").update(`${seed} ${i}`).digest("base64");
  return out.replace(/=/g, "").slice(0, length - 6) + "IDAQAB";
}

/** A port nothing on this machine is listening on, a moment ago. */
function free() {
  return new Promise((done) => {
    const probe = createServer().listen(0, "127.0.0.1", () => {
      const { port } = probe.address();
      probe.close(() => done(port));
    });
  });
}

// --- the browser -----------------------------------------------------------------------------

browser = await Browser.launch({
  chromium,
  // Not a fixed one: another browser already on it would be driven instead, and its own work
  // would push this page to the back.
  port: await free(),
  profile: join(work, "chrome"),
  base: BASE,
});
await browser.scheme(THEME);
await browser.send("Emulation.setTimezoneOverride", { timezoneId: "UTC" });
await browser.send("Emulation.setLocaleOverride", { locale: "en-US" });
await browser.cookie("mailio_session", session);

// --- the shots -----------------------------------------------------------------------------

/** What the shot under way is waiting for, which is what it says when it runs out of time. */
let awaiting = "";

/** Polls f until it holds, saying what for. The shot's own time limit is what ends it. */
function until(what, f) {
  awaiting = what;
  return waitFor(f, TIMEOUT * 4);
}

/** Everything drawn: fonts in, and a dialog's entrance over. */
async function settled() {
  await until("the fonts", () =>
    browser.eval("document.fonts.ready.then(() => true)"),
  );
  await sleep(400);
}

/** The admin interface, with every list filled in. */
async function home() {
  await browser.open("/");
  await until("the accounts", () =>
    browser.eval(
      `[...document.querySelectorAll("button")].some((b) => b.textContent.startsWith(${JSON.stringify(accounts[0].address)}))`,
    ),
  );
}

/** Clicks the button that says text, or the first that starts with it, as a row does. */
const click = (text) =>
  browser.eval(`(() => {
    const all = [...document.querySelectorAll("button")];
    const el = all.find((b) => b.textContent.trim() === ${JSON.stringify(text)}) ??
      all.find((b) => b.textContent.trim().startsWith(${JSON.stringify(text)}));
    if (!el) throw new Error("nothing to click: " + ${JSON.stringify(text)});
    el.click();
    return true;
  })()`);

/** Opens the dialog behind a button or a row. */
async function dialog(text) {
  await home();
  await click(text);
  await until(`the dialog of ${text}`, () =>
    browser.eval(`!!document.querySelector("dialog[open]")`),
  );
}

/** The region an element takes, and a margin, inside the window. */
const around = (selector, margin) =>
  browser.eval(`(() => {
    const r = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect();
    const x = Math.max(0, r.left - ${margin});
    const y = Math.max(0, r.top - ${margin});
    const right = Math.min(innerWidth, r.right + ${margin});
    const bottom = Math.min(innerHeight, r.bottom + ${margin});
    return { x, y, width: right - x, height: bottom - y };
  })()`);

/**
 * Everything at rest and pinned: the caret hidden, every transition and animation finished, and
 * the made-up values each run draws in their place.
 */
const still = () =>
  browser.eval(`(() => {
    const style = document.createElement("style");
    style.textContent =
      "*, ::before, ::after { caret-color: transparent !important; transition: none !important; }";
    document.head.append(style);
    for (const animation of document.getAnimations()) animation.finish();
    const pinned = Object.entries(${JSON.stringify(pinned)});
    const walk = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
    for (let node; (node = walk.nextNode()); )
      for (const [from, to] of pinned)
        if (node.textContent.includes(from)) node.textContent = node.textContent.replaceAll(from, to);
    return new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(() => done(true))));
  })()`);

// Each shot brings the page to a view and photographs it: the element it names, with a margin.
const shots = [
  {
    name: "admin",
    window: [960, 2400],
    element: "#root > div",
    margin: 0,
    go: home,
  },
  {
    name: "domain",
    window: [960, 1500],
    element: "dialog[open]",
    margin: 16,
    go: () => dialog(domains[0].name),
  },
  {
    name: "account",
    window: [960, 1500],
    element: "dialog[open]",
    margin: 16,
    go: () => dialog(accounts[0].address),
  },
  {
    name: "sending",
    window: [960, 1100],
    element: "dialog[open]",
    margin: 16,
    go: () => dialog("Configure sending…"),
  },
];

step(`photographing at ${SCALE}x, ${THEME}`);
mkdirSync(OUT, { recursive: true });
for (const shot of shots) {
  if (ONLY.length && !ONLY.includes(shot.name)) continue;
  // A step waiting on something that never comes would otherwise wait forever.
  awaiting = "";
  const limit = setTimeout(
    () =>
      die(
        `${shot.name} took more than ${TIMEOUT} seconds${awaiting ? `, waiting for ${awaiting}` : ""}`,
      ),
    TIMEOUT * 1000,
  );
  await browser.viewport(...shot.window, SCALE);
  await shot.go();
  await settled();
  awaiting = "the page to come to rest";
  await still();
  const clip = await around(shot.element, shot.margin);
  const file = join(OUT, `${shot.name}.png`);
  const png = stamp(await browser.png(clip), SCALE);
  const before = existsSync(file) ? readFileSync(file) : null;
  if (before && alike(before, png)) {
    // The same picture, kept; its metadata brought up to date if the stamp has changed.
    const restamped = stamp(before, SCALE);
    if (!restamped.equals(before)) writeFileSync(file, restamped);
    console.log(`  ${shot.name}.png, unchanged`);
  } else {
    writeFileSync(file, png);
    console.log(`  ${shot.name}.png`);
  }
  clearTimeout(limit);
}

step("done");
process.exit(0);

/**
 * The density a PNG was drawn at, said the two ways a macOS screenshot says it: a pHYs chunk,
 * and an EXIF block with the same resolution in inches, which is what some of the system reads
 * instead. A 2x capture says 144 dpi, and a viewer that reads it shows it at the size it was on
 * screen rather than twice that.
 */
function stamp(png, scale) {
  const chunk = (type, data) => {
    const head = Buffer.alloc(4);
    head.writeUInt32BE(data.length);
    const body = Buffer.concat([Buffer.from(type), data]);
    const tail = Buffer.alloc(4);
    tail.writeUInt32BE(crc32(body));
    return Buffer.concat([head, body, tail]);
  };
  const dpi = 72 * scale;
  const phys = Buffer.alloc(9);
  phys.writeUInt32BE(Math.round(dpi / 0.0254), 0);
  phys.writeUInt32BE(Math.round(dpi / 0.0254), 4);
  phys[8] = 1; // the unit is the metre

  // A big-endian TIFF header and one directory of three entries: XResolution and YResolution,
  // each a rational stored after the directory, and ResolutionUnit, 2 being the inch.
  const exif = Buffer.alloc(66);
  exif.write("MM", 0, "latin1");
  exif.writeUInt16BE(42, 2);
  exif.writeUInt32BE(8, 4);
  exif.writeUInt16BE(3, 8);
  const entry = (at, tag, type, value) => {
    exif.writeUInt16BE(tag, at);
    exif.writeUInt16BE(type, at + 2);
    exif.writeUInt32BE(1, at + 4);
    if (type === 3) exif.writeUInt16BE(value, at + 8);
    else exif.writeUInt32BE(value, at + 8);
  };
  entry(10, 0x011a, 5, 50);
  entry(22, 0x011b, 5, 58);
  entry(34, 0x0128, 3, 2);
  exif.writeUInt32BE(0, 46); // no next directory
  for (const at of [50, 58]) {
    exif.writeUInt32BE(dpi, at);
    exif.writeUInt32BE(1, at + 4);
  }

  // After IHDR, which is always first, and in place of any already there.
  const parts = [png.subarray(0, 8)];
  for (let at = 8; at < png.length;) {
    const length = png.readUInt32BE(at);
    const type = png.toString("latin1", at + 4, at + 8);
    const end = at + 12 + length;
    if (type !== "pHYs" && type !== "eXIf") parts.push(png.subarray(at, end));
    if (type === "IHDR") parts.push(chunk("pHYs", phys), chunk("eXIf", exif));
    at = end;
  }
  return Buffer.concat(parts);
}

/**
 * Whether two captures are one picture, give or take the rasteriser.
 *
 * With the content pinned, a run can still move a few dozen anti-aliased pixels at an edge by a
 * few levels, and git would see a new file. Kept when under a thousandth of the pixels differ and
 * none by more than 24 of 255: a moved icon or a changed word is far more than 24 levels, and a
 * changed colour is far more than a thousandth of the picture.
 */
function alike(before, after) {
  const a = decode(before);
  const b = decode(after);
  if (
    !a ||
    !b ||
    a.width !== b.width ||
    a.height !== b.height ||
    a.channels !== b.channels
  ) {
    return false;
  }
  let differing = 0;
  for (let at = 0; at < a.pixels.length; at += a.channels) {
    let worst = 0;
    for (let c = 0; c < a.channels; c++) {
      worst = Math.max(worst, Math.abs(a.pixels[at + c] - b.pixels[at + c]));
    }
    if (worst > 24) return false;
    if (worst > 0) differing++;
  }
  return differing <= (a.pixels.length / a.channels) * 0.001;
}

/** The pixels of an 8-bit, non-interlaced RGB or RGBA PNG, which is what Chromium writes. */
function decode(png) {
  let width, height, channels;
  const data = [];
  for (let at = 8; at < png.length;) {
    const length = png.readUInt32BE(at);
    const type = png.toString("latin1", at + 4, at + 8);
    const body = png.subarray(at + 8, at + 8 + length);
    if (type === "IHDR") {
      width = body.readUInt32BE(0);
      height = body.readUInt32BE(4);
      channels = { 2: 3, 6: 4 }[body[9]];
      if (body[8] !== 8 || !channels || body[12] !== 0) return null;
    }
    if (type === "IDAT") data.push(body);
    at += 12 + length;
  }
  const raw = inflateSync(Buffer.concat(data));
  const stride = width * channels;
  const pixels = Buffer.alloc(height * stride);
  for (let y = 0; y < height; y++) {
    const filter = raw[y * (stride + 1)];
    const line = y * (stride + 1) + 1;
    for (let x = 0; x < stride; x++) {
      const a = x >= channels ? pixels[y * stride + x - channels] : 0;
      const b = y > 0 ? pixels[(y - 1) * stride + x] : 0;
      const c =
        x >= channels && y > 0 ? pixels[(y - 1) * stride + x - channels] : 0;
      let value = raw[line + x];
      if (filter === 1) value += a;
      else if (filter === 2) value += b;
      else if (filter === 3) value += (a + b) >> 1;
      else if (filter === 4) {
        const p = a + b - c;
        const pa = Math.abs(p - a),
          pb = Math.abs(p - b),
          pc = Math.abs(p - c);
        value += pa <= pb && pa <= pc ? a : pb <= pc ? b : c;
      }
      pixels[y * stride + x] = value & 255;
    }
  }
  return { width, height, channels, pixels };
}
