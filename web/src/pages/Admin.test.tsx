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
  address: "inbox@reey.ru",
  domain_id: "dm_1",
  incoming: "keep",
  forward_account_id: null,
  credentials: [
    {
      id: "cr_1",
      label: "default",
      login: "inbox@reey.ru",
      created_at: 1,
      last_used_at: null,
    },
  ],
  created_at: 1,
};

const misha: Account = {
  ...inbox,
  id: "ac_2",
  address: "misha@reey.ru",
  credentials: [],
};

const me: AdminT = { id: "ad_1", name: "misha", created_at: 1 };

const reey: Domain = {
  id: "dm_1",
  name: "reey.ru",
  unknown: null,
  unknown_account_id: null,
  dmarc: null,
  spf: null,
  relay: null,
  records: [
    { type: "TXT", name: "_dmarc.reey.ru", data: "v=DMARC1; p=reject" },
  ],
  created_at: 1,
};

const defaults: Settings = {
  unknown: "reject",
  unknown_account_id: null,
  dmarc: "reject",
  spf: "~all",
  relay_id: null,
};

/** Every route a page load reads, as these tests start from; each test replaces what it is about. */
function routes(over: Parameters<typeof fakeServer>[0] = {}) {
  return fakeServer({
    "GET /api/me": () => ({ body: me }),
    "GET /api/admins": () => ({ body: [me] }),
    "GET /api/domains": () => ({ body: [reey] }),
    "GET /api/accounts": () => ({ body: [inbox] }),
    "GET /api/mail": () => ({
      body: { host: "mail.reey.ru", dns_api: true, certificate: null },
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

const writes = (sent: { method: string }[]) =>
  sent.filter((r) => r.method !== "GET");

test("domains, accounts and admins are rows that summarize them", async () => {
  routes();
  mount();
  expect(await screen.findByRole("button", { name: /^reey\.ru/ })).toBeTruthy();
  expect(screen.getByRole("button", { name: /^inbox@reey\.ru/ })).toBeTruthy();
  // The signed-in admin is marked, and is not an account.
  expect(screen.getByRole("button", { name: /^misha/ })).toBeTruthy();
  expect(screen.getByText("you")).toBeTruthy();
  expect(screen.getAllByText("(default)").length).toBe(4);
});

test("a domain's dialog holds its changes and sends them in one request on Save", async () => {
  let domain = reey;
  const { sent } = routes({
    "GET /api/domains": () => ({ body: [domain] }),
    "PATCH /api/domains/dm_1": (body) => {
      domain = { ...domain, unknown: "junk", unknown_account_id: "ac_1" };
      return { body: { ...domain, ...(body as object) } };
    },
  });
  mount();
  await open(/^reey\.ru/);
  const d = within(dialog("reey.ru"));
  fireEvent.change(d.getByLabelText("Unknown addresses"), {
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
    expect(screen.queryByRole("dialog", { name: "reey.ru" })).toBeNull(),
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
  await open(/^reey\.ru/);
  const d = within(dialog("reey.ru"));
  fireEvent.change(d.getByLabelText("DMARC"), { target: { value: "none" } });
  fireEvent.click(d.getByRole("button", { name: "Save" }));

  expect(await d.findByText("Nothing is listening there.")).toBeTruthy();
  expect((d.getByLabelText("DMARC") as HTMLSelectElement).value).toBe("none");
});

test("closing a dialog forgets what was changed in it", async () => {
  const { sent } = routes();
  mount();
  await open(/^reey\.ru/);
  fireEvent.change(within(dialog("reey.ru")).getByLabelText("DMARC"), {
    target: { value: "none" },
  });
  fireEvent.click(
    within(dialog("reey.ru")).getByRole("button", { name: "Close" }),
  );
  await waitFor(() =>
    expect(screen.queryByRole("dialog", { name: "reey.ru" })).toBeNull(),
  );

  await open(/^reey\.ru/);
  expect(
    (within(dialog("reey.ru")).getByLabelText("DMARC") as HTMLSelectElement)
      .value,
  ).toBe("");
  expect(writes(sent)).toEqual([]);
});

test("a new account asks only its address, and shows its generated password once", async () => {
  const { sent } = routes({
    "POST /api/accounts": () => ({
      body: { ...misha, password: "Generated24CharacterPass" },
    }),
  });
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Add account" }));
  const d = within(dialog("Add an account"));
  fireEvent.change(d.getByLabelText("Address"), {
    target: { value: "misha@reey.ru" },
  });
  fireEvent.click(d.getByRole("button", { name: "Add" }));

  const shown = within(
    await screen.findByRole("dialog", { name: "Account added" }),
  );
  expect(shown.getByText("misha@reey.ru")).toBeTruthy();
  expect(shown.getByText("Generated24CharacterPass")).toBeTruthy();
  expect(writes(sent)).toEqual([
    {
      method: "POST",
      path: "/api/accounts",
      body: { address: "misha@reey.ru" },
    },
  ]);

  fireEvent.click(shown.getByRole("button", { name: "Done" }));
  await waitFor(() =>
    expect(screen.queryByText("Generated24CharacterPass")).toBeNull(),
  );
});

test("a credential is added by its label, and its login and password are shown once", async () => {
  const { sent } = routes({
    "POST /api/accounts/ac_1/credentials": () => ({
      body: {
        id: "cr_2",
        label: "iPhone",
        login: "inbox+k3x9@reey.ru",
        created_at: 2,
        last_used_at: null,
        password: "AnotherGeneratedPassword",
      },
    }),
  });
  mount();
  await open(/^inbox@reey\.ru/);
  fireEvent.click(
    within(dialog("inbox@reey.ru")).getByRole("button", {
      name: "Add credential",
    }),
  );
  const d = within(dialog("Add a credential"));
  fireEvent.change(d.getByLabelText("Label"), { target: { value: "iPhone" } });
  fireEvent.click(d.getByRole("button", { name: "Add" }));

  const shown = within(
    await screen.findByRole("dialog", { name: "Credential added" }),
  );
  expect(shown.getByText("inbox+k3x9@reey.ru")).toBeTruthy();
  expect(shown.getByText("AnotherGeneratedPassword")).toBeTruthy();
  expect(writes(sent)).toEqual([
    {
      method: "POST",
      path: "/api/accounts/ac_1/credentials",
      body: { label: "iPhone" },
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
  await open(/^inbox@reey\.ru/);
  fireEvent.click(
    within(dialog("inbox@reey.ru")).getByRole("button", {
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
      body: undefined,
    },
  ]);
});

test("forwarding an account's mail is held until Save", async () => {
  const { sent } = routes({
    "GET /api/accounts": () => ({ body: [inbox, misha] }),
    "PATCH /api/accounts/ac_1": () => ({ body: inbox }),
  });
  mount();
  await open(/^inbox@reey\.ru/);
  const d = within(dialog("inbox@reey.ru"));
  fireEvent.change(d.getByLabelText("Incoming mail"), {
    target: { value: "forward" },
  });
  expect(
    (d.getByLabelText("Who gets the mail") as HTMLSelectElement).value,
  ).toBe("ac_2");
  expect(writes(sent)).toEqual([]);

  fireEvent.click(d.getByRole("button", { name: "Save" }));
  await waitFor(() =>
    expect(writes(sent)).toEqual([
      {
        method: "PATCH",
        path: "/api/accounts/ac_1",
        body: { incoming: "forward", forward_account_id: "ac_2" },
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
  expect(await s.findByText("Gmail")).toBeTruthy();
  fireEvent.change(s.getByLabelText("Default for every domain"), {
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
