import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, test, vi } from "vitest";

import type {
  Account,
  Admin as AdminT,
  Domain,
  Relay,
  Settings,
} from "@app/api/admin";
import { Admin } from "@app/pages/Admin";
import { fakeServer } from "@app/test/server";

vi.mock("@app/leave", () => ({ leaveFor: vi.fn() }));

afterEach(() => vi.unstubAllGlobals());

const inbox: Account = {
  id: "ac_1",
  kind: "account",
  address: "inbox@example.org",
  domain_id: "dm_1",
  incoming: "keep",
  forward_account_id: null,
  max_message_bytes: null,
  relay: null,
  send_as_account_id: null,
  trash_days: null,
  junk_days: null,
  credentials: [
    {
      id: "cr_1",
      label: "default",
      login: "inbox@example.org",
      created_at: 1,
      last_used_at: null,
    },
  ],
  messages: 3,
  bytes: 4096,
  created_at: 1,
};

const misha: Account = {
  ...inbox,
  id: "ac_2",
  address: "misha@example.org",
  credentials: [],
};

const me: AdminT = { id: "ad_1", name: "misha", created_at: 1 };

const exampleOrg: Domain = {
  id: "dm_1",
  name: "example.org",
  unknown: null,
  unknown_account_id: null,
  dmarc: null,
  spf: null,
  relay: null,
  max_message_bytes: null,
  trash_days: null,
  junk_days: null,
  records: [
    {
      type: "MX",
      name: "example.org",
      data: "mail.example.org",
      managed: false,
      status: "ok",
      found: [],
      error: null,
    },
    {
      type: "TXT",
      name: "_dmarc.example.org",
      data: "v=DMARC1; p=reject",
      managed: true,
      status: "ok",
      found: [],
      error: null,
    },
  ],
  dns_checked_at: 1,
  created_at: 1,
};

const defaults: Settings = {
  unknown: "reject",
  dmarc: "reject",
  spf: "~all",
  relay_id: null,
  max_message_bytes: 50 * 1024 * 1024,
  trash_days: 30,
  junk_days: 30,
};

/** Every route a page load reads, as these tests start from; each test replaces what it is about. */
function routes(over: Parameters<typeof fakeServer>[0] = {}) {
  return fakeServer({
    "GET /api/me": () => ({ body: me }),
    "GET /api/admins": () => ({ body: [me] }),
    "GET /api/domains": () => ({ body: [exampleOrg] }),
    "GET /api/accounts": () => ({ body: [inbox] }),
    "GET /api/mail": () => ({
      body: {
        host: "mail.example.org",
        dns_api: true,
        staging: false,
        certificate: null,
      },
    }),
    "GET /api/relays": () => ({ body: [] }),
    "GET /api/queue": () => ({ body: [] }),
    "GET /api/settings": () => ({ body: defaults }),
    ...over,
  });
}

function mount() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={client}>
      <Admin />
    </QueryClientProvider>,
  );
}

/** The open dialog with this title. */
const dialog = (title: string) => screen.getByRole("dialog", { name: title });

const open = async (row: RegExp) =>
  fireEvent.click(await screen.findByRole("button", { name: row }));

const writes = <T extends { method: string }>(sent: T[]) =>
  sent.filter((r) => r.method !== "GET");

/** Past the moment a button that just appeared ignores presses, as a stuttered click would not be. */
const settle = () => new Promise((r) => setTimeout(r, 550));

test("domains, accounts and admins are rows that summarize them", async () => {
  routes();
  mount();
  expect(
    await screen.findByRole("button", { name: /^example\.org/ }),
  ).toBeTruthy();
  expect(
    screen.getByRole("button", { name: /^inbox@example\.org/ }),
  ).toBeTruthy();
  // The signed-in admin is marked, and is not an account.
  expect(screen.getByRole("button", { name: /^misha/ })).toBeTruthy();
  expect(screen.getByText("you")).toBeTruthy();
  // Each setting a domain follows the default for, dimmed and said so to a screen reader.
  expect(screen.getAllByText("(default)").length).toBe(7);
});

test("a domain's dialog holds its changes and sends them in one request on Save", async () => {
  let domain = exampleOrg;
  const { sent } = routes({
    "GET /api/domains": () => ({ body: [domain] }),
    "PATCH /api/domains/dm_1": (body) => {
      domain = { ...domain, unknown: "junk", unknown_account_id: "ac_1" };
      return { body: { ...domain, ...(body as object) } };
    },
  });
  mount();
  await open(/^example\.org/);
  const d = within(dialog("example.org"));
  // Forward and junk both go to an account, forward to its INBOX.
  fireEvent.change(d.getByLabelText("Catch-all"), {
    target: { value: "forward" },
  });
  expect(d.getByLabelText("Who gets the mail")).toBeTruthy();
  fireEvent.change(d.getByLabelText("Catch-all"), {
    target: { value: "junk" },
  });
  // Junk goes to somebody at the same domain until somebody else is chosen.
  expect(
    (d.getByLabelText("Who gets the junk") as HTMLSelectElement).value,
  ).toBe("ac_1");
  fireEvent.change(d.getByLabelText("DMARC"), {
    target: { value: "quarantine" },
  });
  expect(writes(sent)).toEqual([]);

  fireEvent.click(d.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog", { name: "example.org" })).toBeNull(),
  );
  expect(writes(sent)).toEqual([
    {
      method: "PATCH",
      path: "/api/domains/dm_1",
      body: {
        unknown: "junk",
        unknown_account_id: "ac_1",
        dmarc: "quarantine",
        spf: "",
        relay: "",
        max_message_bytes: 0,
        trash_days: null,
        junk_days: null,
      },
    },
  ]);
});

test("a refused save keeps the dialog open with the server's sentence and the changes", async () => {
  routes({
    "PATCH /api/domains/dm_1": () => ({
      status: 409,
      body: { code: "conflict", message: "Nothing is listening there." },
    }),
  });
  mount();
  await open(/^example\.org/);
  const d = within(dialog("example.org"));
  fireEvent.change(d.getByLabelText("DMARC"), { target: { value: "none" } });
  fireEvent.click(d.getByRole("button", { name: "Save" }));

  expect(await d.findByText("Nothing is listening there.")).toBeTruthy();
  expect((d.getByLabelText("DMARC") as HTMLSelectElement).value).toBe("none");
});

test("closing a dialog forgets what was changed in it", async () => {
  const { sent } = routes();
  mount();
  await open(/^example\.org/);
  fireEvent.change(within(dialog("example.org")).getByLabelText("DMARC"), {
    target: { value: "none" },
  });
  fireEvent.click(
    within(dialog("example.org")).getByRole("button", { name: "Close" }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog", { name: "example.org" })).toBeNull(),
  );

  await open(/^example\.org/);
  expect(
    (within(dialog("example.org")).getByLabelText("DMARC") as HTMLSelectElement)
      .value,
  ).toBe("");
  expect(writes(sent)).toEqual([]);
});

test("a new account is added under its domain by its name alone, and shows its generated password once, to copy", async () => {
  const writeText = vi.fn(async () => {});
  Object.defineProperty(navigator, "clipboard", {
    value: { writeText },
    configurable: true,
  });
  const { sent } = routes({
    "POST /api/accounts": () => ({
      body: { ...misha, password: "Generated24CharacterPass" },
    }),
  });
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Add account" }));
  const d = within(dialog("Add an account"));
  fireEvent.change(d.getByLabelText("Name at example.org"), {
    target: { value: "misha" },
  });
  fireEvent.click(d.getByRole("button", { name: "Add" }));

  const shown = within(
    await screen.findByRole("dialog", { name: "Account added" }),
  );
  expect(shown.getByText("misha@example.org")).toBeTruthy();
  expect(shown.getByText("Generated24CharacterPass")).toBeTruthy();
  fireEvent.click(shown.getByRole("button", { name: "Copy password" }));
  await waitFor(() =>
    expect(writeText).toHaveBeenCalledWith("Generated24CharacterPass"),
  );
  expect(writes(sent)).toEqual([
    {
      method: "POST",
      path: "/api/accounts",
      body: { address: "misha@example.org" },
    },
  ]);

  // A stuttered second click on Add lands on Done, and must not close a password shown once.
  fireEvent.click(shown.getByRole("button", { name: "Done" }));
  expect(shown.getByText("Generated24CharacterPass")).toBeTruthy();
  await settle();
  fireEvent.click(shown.getByRole("button", { name: "Done" }));
  await waitFor(() =>
    expect(screen.queryByText("Generated24CharacterPass")).toBeNull(),
  );
});

test("an alias is added of an account that keeps its mail, shows no password, and is a pill under the account, which opens it", async () => {
  const shop: Account = {
    ...inbox,
    id: "ac_3",
    kind: "alias",
    address: "shop@example.org",
    incoming: "forward",
    forward_account_id: "ac_1",
    credentials: [],
  };
  let accounts = [inbox];
  const { sent } = routes({
    "GET /api/accounts": () => ({ body: accounts }),
    "POST /api/accounts": () => {
      accounts = [inbox, shop];
      return { body: shop };
    },
  });
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Add account" }));
  const d = within(dialog("Add an account"));
  fireEvent.click(d.getByRole("radio", { name: "Alias" }));
  const a = within(dialog("Add an alias"));
  fireEvent.change(a.getByLabelText("Name at example.org"), {
    target: { value: "shop" },
  });
  expect((a.getByLabelText("Alias of") as HTMLSelectElement).value).toBe(
    "ac_1",
  );
  fireEvent.click(a.getByRole("button", { name: "Add" }));

  await waitFor(() =>
    expect(screen.queryByRole("dialog", { name: "Add an alias" })).toBeNull(),
  );
  expect(writes(sent)).toEqual([
    {
      method: "POST",
      path: "/api/accounts",
      body: { address: "shop@example.org", alias_of: "ac_1" },
    },
  ]);
  const alias = await screen.findByRole("button", { name: "shop@example.org" });
  const row = screen
    .getByRole("button", { name: /^inbox@example\.org.*Incoming/ })
    .closest("li")!;
  expect(row.contains(alias)).toBe(true);
  expect(within(row).getByText("Aliases:")).toBeTruthy();

  fireEvent.click(alias);
  expect(
    (
      within(dialog("shop@example.org")).getByLabelText(
        "Alias of",
      ) as HTMLSelectElement
    ).value,
  ).toBe("ac_1");
});

test("an account's mail is cleared from its danger zone, after a confirm saying how much", async () => {
  const { sent } = routes({
    "DELETE /api/accounts/ac_1/mail": () => ({
      body: { ...inbox, messages: 0 },
    }),
  });
  mount();
  expect(
    await screen.findByRole("button", {
      name: /^inbox@example\.org.*Messages: 3.*Size: 4 KB/,
    }),
  ).toBeTruthy();
  await open(/^inbox@example\.org/);
  fireEvent.click(
    within(dialog("inbox@example.org")).getByRole("button", {
      name: "Clear all mail…",
    }),
  );
  const d = within(dialog("Clear all mail"));
  expect(d.getByText("3")).toBeTruthy();
  await settle();
  fireEvent.click(d.getByRole("button", { name: "Clear" }));
  await waitFor(() =>
    expect(writes(sent)).toEqual([
      { method: "DELETE", path: "/api/accounts/ac_1/mail", body: undefined },
    ]),
  );
});

test("held mail in the queue says when it goes, tried mail how often it was tried", async () => {
  routes({
    "GET /api/queue": () => ({
      body: [
        {
          id: 1,
          message_id: 7,
          sender: "inbox@example.org",
          recipient: "someone@example.com",
          attempts: 0,
          last_error: "",
          next_at: 1795000000,
          created_at: 1,
          release_at: 1795000000,
        },
        {
          id: 2,
          message_id: 8,
          sender: "inbox@example.org",
          recipient: "nobody@example.com",
          attempts: 2,
          last_error: "451 try later",
          next_at: 2,
          created_at: 1,
          release_at: null,
        },
      ],
    }),
  });
  mount();
  expect(await screen.findByText(/^Held until: /)).toBeTruthy();
  expect(screen.getByText("Attempts: 2 · 451 try later")).toBeTruthy();
  // Only held mail can be unscheduled.
  expect(screen.getAllByRole("button", { name: "Unschedule…" }).length).toBe(1);
});

test("held mail is unscheduled from the queue after a confirm", async () => {
  const { sent } = routes({
    "GET /api/queue": () => ({
      body: [
        {
          id: 1,
          message_id: 7,
          sender: "inbox@example.org",
          recipient: "someone@example.com",
          attempts: 0,
          last_error: "",
          next_at: 1795000000,
          created_at: 1,
          release_at: 1795000000,
        },
      ],
    }),
    "DELETE /api/queue/7": () => ({ status: 204 }),
  });
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Unschedule…" }));
  const d = within(dialog("Unschedule"));
  await settle();
  fireEvent.click(d.getByRole("button", { name: "Unschedule" }));
  await waitFor(() =>
    expect(writes(sent)).toEqual([
      { method: "DELETE", path: "/api/queue/7", body: undefined },
    ]),
  );
});

test("a credential is added by its label, and its login and password are shown once", async () => {
  const { sent } = routes({
    "POST /api/accounts/ac_1/credentials": () => ({
      body: {
        id: "cr_2",
        label: "iPhone",
        login: "inbox+k3x9@example.org",
        created_at: 2,
        last_used_at: null,
        password: "AnotherGeneratedPassword",
      },
    }),
  });
  mount();
  await open(/^inbox@example\.org/);
  fireEvent.click(
    within(dialog("inbox@example.org")).getByRole("button", {
      name: "Add credential",
    }),
  );
  const d = within(dialog("Add a credential"));
  fireEvent.change(d.getByLabelText("Label"), { target: { value: "iPhone" } });
  fireEvent.click(d.getByRole("button", { name: "Add" }));

  const shown = within(
    await screen.findByRole("dialog", { name: "Credential added" }),
  );
  expect(shown.getByText("inbox+k3x9@example.org")).toBeTruthy();
  expect(shown.getByText("AnotherGeneratedPassword")).toBeTruthy();
  expect(writes(sent)).toEqual([
    {
      method: "POST",
      path: "/api/accounts/ac_1/credentials",
      body: { label: "iPhone", password: "" },
    },
  ]);
});

test("a credential's new password is generated by the server and shown once", async () => {
  const { sent } = routes({
    "POST /api/accounts/ac_1/credentials/cr_1/reset": () => ({
      body: { ...inbox.credentials[0], password: "ReplacedGeneratedPasswd" },
    }),
  });
  mount();
  await open(/^inbox@example\.org/);
  fireEvent.click(
    within(dialog("inbox@example.org")).getByRole("button", {
      name: "New password…",
    }),
  );
  const d = within(dialog("New password for default"));
  fireEvent.click(d.getByRole("button", { name: "Replace" }));

  expect(await d.findByText("ReplacedGeneratedPasswd")).toBeTruthy();
  expect(writes(sent)).toEqual([
    {
      method: "POST",
      path: "/api/accounts/ac_1/credentials/cr_1/reset",
      body: { password: "" },
    },
  ]);
});

test("a credential's new password can be one typed, for a client particular about passwords", async () => {
  const { sent } = routes({
    "POST /api/accounts/ac_1/credentials/cr_1/reset": (body) => ({
      body: { ...inbox.credentials[0], ...(body as object) },
    }),
  });
  mount();
  await open(/^inbox@example\.org/);
  fireEvent.click(
    within(dialog("inbox@example.org")).getByRole("button", {
      name: "New password…",
    }),
  );
  const d = within(dialog("New password for default"));
  fireEvent.change(d.getByLabelText("Password"), {
    target: { value: "lettersonly" },
  });
  fireEvent.click(d.getByRole("button", { name: "Replace" }));

  expect(await d.findByText("lettersonly")).toBeTruthy();
  expect(writes(sent)).toEqual([
    {
      method: "POST",
      path: "/api/accounts/ac_1/credentials/cr_1/reset",
      body: { password: "lettersonly" },
    },
  ]);
});

test("forwarding an account's mail is held until Save", async () => {
  const { sent } = routes({
    "GET /api/accounts": () => ({ body: [inbox, misha] }),
    "PATCH /api/accounts/ac_1": () => ({ body: inbox }),
  });
  mount();
  await open(/^inbox@example\.org/);
  const d = within(dialog("inbox@example.org"));
  fireEvent.change(d.getByLabelText("Incoming mail"), {
    target: { value: "forward" },
  });
  expect(
    (d.getByLabelText("Who gets the mail") as HTMLSelectElement).value,
  ).toBe("ac_2");
  // Grouped by domain.
  expect(
    within(d.getByRole("group", { name: "example.org" }))
      .getAllByRole("option")
      .map((o) => o.textContent),
  ).toEqual(["misha@example.org"]);
  expect(writes(sent)).toEqual([]);

  fireEvent.click(d.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(writes(sent)).toEqual([
      {
        method: "PATCH",
        path: "/api/accounts/ac_1",
        body: {
          incoming: "forward",
          forward_account_id: "ac_2",
          max_message_bytes: 0,
          relay: "",
          send_as_account_id: "",
          trash_days: null,
          junk_days: null,
        },
      },
    ]),
  );
});

test("an account sends as another, chosen with the relays, and its row says so", async () => {
  let accounts = [inbox, misha];
  const { sent } = routes({
    "GET /api/accounts": () => ({ body: accounts }),
    "PATCH /api/accounts/ac_1": () => {
      accounts = [{ ...inbox, send_as_account_id: "ac_2" }, misha];
      return { body: accounts[0] };
    },
  });
  mount();
  await open(/^inbox@example\.org/);
  const d = within(dialog("inbox@example.org"));
  const sending = d.getByLabelText("Sending") as HTMLSelectElement;
  expect(
    within(within(sending).getByRole("group", { name: "As another account" }))
      .getAllByRole("option")
      .map((o) => o.textContent),
  ).toEqual(["misha@example.org"]);
  fireEvent.change(sending, { target: { value: "as:ac_2" } });
  fireEvent.click(d.getByRole("button", { name: "Save" }));

  await waitFor(() =>
    expect(writes(sent)).toEqual([
      {
        method: "PATCH",
        path: "/api/accounts/ac_1",
        body: {
          incoming: "keep",
          forward_account_id: null,
          max_message_bytes: 0,
          send_as_account_id: "ac_2",
          trash_days: null,
          junk_days: null,
        },
      },
    ]),
  );
  expect(
    await screen.findByRole("button", {
      name: /^inbox@example\.org.*Sends as: misha@example\.org/,
    }),
  ).toBeTruthy();
});

test("an account's largest message is typed in MB, and empty is its domain's", async () => {
  const { sent } = routes({
    "GET /api/domains": () => ({
      body: [{ ...exampleOrg, max_message_bytes: 25 * 1024 * 1024 }],
    }),
    "PATCH /api/accounts/ac_1": () => ({ body: inbox }),
  });
  mount();
  await open(/^inbox@example\.org/);
  const d = within(dialog("inbox@example.org"));
  expect(d.getByText("Empty: the domain's, 25 MB.")).toBeTruthy();
  const field = d.getByLabelText("Largest message");
  fireEvent.change(field, { target: { value: "many" } });
  expect(d.getByText("A number of MB.")).toBeTruthy();
  expect(
    (d.getByRole("button", { name: "Save" }) as HTMLButtonElement).disabled,
  ).toBe(true);

  fireEvent.change(field, { target: { value: "10" } });
  fireEvent.click(d.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(writes(sent)).toEqual([
      {
        method: "PATCH",
        path: "/api/accounts/ac_1",
        body: {
          incoming: "keep",
          forward_account_id: null,
          max_message_bytes: 10 * 1024 * 1024,
          relay: "",
          send_as_account_id: "",
          trash_days: null,
          junk_days: null,
        },
      },
    ]),
  );
});

test("a domain's Trash and Junk days are typed, 0 keeping them forever, and empty is the default", async () => {
  const { sent } = routes({
    "PATCH /api/domains/dm_1": () => ({
      body: { ...exampleOrg, trash_days: 0 },
    }),
  });
  mount();
  await open(/^example\.org/);
  const d = within(dialog("example.org"));
  expect(
    d.getAllByText("Empty: the default, 30 days. 0: forever.").length,
  ).toBe(2);
  const field = d.getByLabelText("Trash kept");
  fireEvent.change(field, { target: { value: "a week" } });
  expect(d.getByText("A whole number of days.")).toBeTruthy();
  fireEvent.change(field, { target: { value: "0" } });
  fireEvent.change(d.getByLabelText("Junk kept"), { target: { value: "3" } });
  fireEvent.click(d.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(writes(sent)).toEqual([
      {
        method: "PATCH",
        path: "/api/domains/dm_1",
        body: {
          unknown: "",
          unknown_account_id: null,
          dmarc: "",
          spf: "",
          relay: "",
          max_message_bytes: 0,
          trash_days: 0,
          junk_days: 3,
        },
      },
    ]),
  );
});

test("a relay is added in its own dialog, then made the default on Save", async () => {
  let relays: Relay[] = [];
  const { sent } = routes({
    "GET /api/relays": () => ({ body: relays }),
    "POST /api/relays": (body) => {
      const { password, ...rest } = body as Relay & { password: string };
      relays = [{ ...rest, id: "rl_1", has_password: password !== "" }];
      return { body: relays[0] };
    },
    "PUT /api/settings": (body) => ({ body }),
  });
  mount();
  fireEvent.click(
    await screen.findByRole("button", { name: "Configure sending…" }),
  );
  expect(within(dialog("Sending")).getByText("No relays yet.")).toBeTruthy();
  fireEvent.click(
    within(dialog("Sending")).getByRole("button", { name: "Add relay" }),
  );

  const r = within(dialog("Add a relay"));
  fireEvent.change(r.getByLabelText("Label"), { target: { value: "Gmail" } });
  fireEvent.change(r.getByLabelText("Host"), {
    target: { value: "smtp.gmail.com" },
  });
  fireEvent.change(r.getByLabelText("Username"), {
    target: { value: "me@gmail.com" },
  });
  fireEvent.change(r.getByLabelText("Password"), {
    target: { value: "app-password" },
  });
  fireEvent.click(r.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog", { name: "Add a relay" })).toBeNull(),
  );

  const s = within(dialog("Sending"));
  expect(await s.findByRole("option", { name: "Gmail" })).toBeTruthy();
  fireEvent.change(s.getByLabelText("Default"), {
    target: { value: "rl_1" },
  });
  fireEvent.click(s.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(screen.queryByRole("dialog", { name: "Sending" })).toBeNull(),
  );
  expect(writes(sent)).toEqual([
    {
      method: "POST",
      path: "/api/relays",
      body: {
        label: "Gmail",
        host: "smtp.gmail.com",
        port: 587,
        security: "starttls",
        username: "me@gmail.com",
        password: "app-password",
      },
    },
    {
      method: "PUT",
      path: "/api/settings",
      body: { ...defaults, relay_id: "rl_1" },
    },
  ]);
});

test("the defaults are changed in their dialog, keeping the relay", async () => {
  const { sent } = routes({
    "GET /api/settings": () => ({ body: { ...defaults, relay_id: "rl_1" } }),
    "PUT /api/settings": (body) => ({ body }),
  });
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Change…" }));
  const d = within(dialog("Defaults"));
  // Forward and junk go to one account, which only a domain chooses.
  expect(
    within(d.getByLabelText("Catch-all"))
      .getAllByRole("option")
      .map((o) => o.textContent),
  ).toEqual(["Reject", "Drop"]);
  fireEvent.change(d.getByLabelText("SPF"), { target: { value: "-all" } });
  fireEvent.click(d.getByRole("button", { name: "Save" }));

  await waitFor(() =>
    expect(writes(sent)).toEqual([
      {
        method: "PUT",
        path: "/api/settings",
        body: { ...defaults, spf: "-all", relay_id: "rl_1" },
      },
    ]),
  );
});

test("an admin is added in a dialog with a name and a password", async () => {
  const { sent } = routes({
    "POST /api/admins": () => ({
      body: { id: "ad_2", name: "anna", created_at: 2 },
    }),
  });
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Add admin" }));
  const d = within(dialog("Add an admin"));
  fireEvent.change(d.getByLabelText("Name"), { target: { value: "anna" } });
  fireEvent.change(d.getByLabelText("Password"), {
    target: { value: "long enough" },
  });
  fireEvent.click(d.getByRole("button", { name: "Add" }));

  await waitFor(() =>
    expect(screen.queryByRole("dialog", { name: "Add an admin" })).toBeNull(),
  );
  expect(writes(sent)).toEqual([
    {
      method: "POST",
      path: "/api/admins",
      body: { name: "anna", password: "long enough" },
    },
  ]);
});

test("a record DNS serves otherwise is marked on the row and in the dialog, with what was found", async () => {
  const wrong: Domain = {
    ...exampleOrg,
    records: [
      exampleOrg.records[0]!,
      {
        ...exampleOrg.records[1]!,
        status: "different",
        found: ["v=DMARC1; p=none"],
      },
    ],
  };
  let domain = wrong;
  const { sent } = routes({
    "GET /api/domains": () => ({ body: [domain] }),
    "POST /api/domains/dm_1/check": () => {
      domain = exampleOrg;
      return { body: domain };
    },
  });
  mount();
  expect(
    await screen.findByRole("button", {
      name: /^example\.org.*DNS: 1 problem/,
    }),
  ).toBeTruthy();

  await open(/^example\.org/);
  const d = within(dialog("example.org"));
  expect(d.getByText("Set outside mailio")).toBeTruthy();
  expect(d.getByText("Different")).toBeTruthy();
  expect(d.getByText("v=DMARC1; p=none")).toBeTruthy();

  fireEvent.click(d.getByRole("button", { name: "Check now" }));
  await waitFor(() => expect(d.queryByText("Different")).toBeNull());
  expect(d.queryByText("v=DMARC1; p=none")).toBeNull();
  expect(writes(sent)).toEqual([
    { method: "POST", path: "/api/domains/dm_1/check", body: undefined },
  ]);
});

test("a delete asks in a dialog of its own, ignores a stuttered click, and keeps a refusal there", async () => {
  let refuse = true;
  const { sent } = routes({
    "DELETE /api/accounts/ac_1/credentials/cr_1": () =>
      refuse
        ? {
            status: 409,
            body: { code: "conflict", message: "Not now." },
          }
        : { status: 204 },
  });
  mount();
  await open(/^inbox@example\.org/);
  fireEvent.click(
    within(dialog("inbox@example.org")).getByRole("button", {
      name: "Delete…",
    }),
  );
  const d = within(dialog("Delete credential"));
  expect(d.getByText("default")).toBeTruthy();
  expect(d.getByText("inbox@example.org")).toBeTruthy();

  fireEvent.click(d.getByRole("button", { name: "Delete" }));
  expect(writes(sent)).toEqual([]);

  await settle();
  fireEvent.click(d.getByRole("button", { name: "Delete" }));
  expect(await d.findByText("Not now.")).toBeTruthy();

  refuse = false;
  fireEvent.click(d.getByRole("button", { name: "Delete" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("dialog", { name: "Delete credential" }),
    ).toBeNull(),
  );
  expect(writes(sent).map((r) => r.method + " " + r.path)).toEqual([
    "DELETE /api/accounts/ac_1/credentials/cr_1",
    "DELETE /api/accounts/ac_1/credentials/cr_1",
  ]);
});

test("a record that could not be checked opens why", async () => {
  const failed: Domain = {
    ...exampleOrg,
    records: [
      exampleOrg.records[0]!,
      {
        ...exampleOrg.records[1]!,
        status: "failed",
        error: "None of its nameservers answered.",
      },
    ],
  };
  routes({ "GET /api/domains": () => ({ body: [failed] }) });
  mount();
  expect(
    await screen.findByRole("button", {
      name: /^example\.org.*DNS: Couldn't check/,
    }),
  ).toBeTruthy();
  await open(/^example\.org/);
  fireEvent.click(
    within(dialog("example.org")).getByRole("button", {
      name: "Couldn't check",
    }),
  );
  const why = within(dialog("Couldn't check"));
  expect(why.getByText("_dmarc.example.org")).toBeTruthy();
  expect(why.getByText("None of its nameservers answered.")).toBeTruthy();
});

test("mail is sent on only to an account that keeps it, and that account keeps it", async () => {
  const noreply: Account = { ...misha, incoming: "reject" };
  const billing: Account = {
    ...misha,
    id: "ac_3",
    address: "billing@example.org",
    incoming: "forward",
    forward_account_id: "ac_1",
  };
  routes({
    "GET /api/accounts": () => ({ body: [inbox, noreply, billing] }),
  });
  mount();
  const row = await screen.findByRole("button", {
    name: /^billing@example\.org/,
  });
  expect(row.textContent).toContain("Forward→inbox@example.org");

  // billing forwards to inbox, so inbox keeps its mail, and says so.
  await open(/^inbox@example\.org/);
  const d = within(dialog("inbox@example.org"));
  expect(
    d.getByText("Keeps its mail while mail is sent on here from:"),
  ).toBeTruthy();
  expect(d.getByText("billing@example.org", { selector: "li" })).toBeTruthy();
  const incoming = (name: string) =>
    (d.getByRole("option", { name }) as HTMLOptionElement).disabled;
  expect(incoming("Keep")).toBe(false);
  expect(incoming("Reject · no-reply account")).toBe(true);
  expect(incoming("Forward")).toBe(true);
  fireEvent.click(d.getByRole("button", { name: "Close" }));
  await waitFor(() =>
    expect(
      screen.queryByRole("dialog", { name: "inbox@example.org" }),
    ).toBeNull(),
  );

  // misha's forward is offered only accounts that keep their mail.
  await open(/^misha@example\.org/);
  const m = within(dialog("misha@example.org"));
  fireEvent.change(m.getByLabelText("Incoming mail"), {
    target: { value: "forward" },
  });
  const target = (name: string) =>
    (
      within(m.getByLabelText("Who gets the mail")).getByRole("option", {
        name,
      }) as HTMLOptionElement
    ).disabled;
  expect(target("inbox@example.org")).toBe(false);
  expect(target("billing@example.org · forwards")).toBe(true);
  expect(
    (m.getByLabelText("Who gets the mail") as HTMLSelectElement).value,
  ).toBe("ac_1");
});
