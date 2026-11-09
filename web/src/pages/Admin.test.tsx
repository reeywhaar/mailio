import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, test, vi } from "vitest";

import type { Admin as AdminT, Domain, User } from "@app/api/admin";
import { Admin } from "@app/pages/Admin";
import { fakeServer } from "@app/test/server";

vi.mock("@app/leave", () => ({ leaveFor: vi.fn() }));

afterEach(() => vi.unstubAllGlobals());

const inbox: User = {
  id: "u_1",
  address: "inbox@reey.ru",
  domain_id: "dm_1",
  incoming: "keep",
  forward_user_id: null,
  passwords: [
    {
      id: "pw_1",
      label: "default",
      login: "inbox@reey.ru",
      created_at: 1,
      last_used_at: null,
    },
  ],
  created_at: 1,
};

const misha: AdminT = { id: "ad_1", name: "misha", created_at: 1 };

const reey: Domain = {
  id: "dm_1",
  name: "reey.ru",
  unknown: "reject",
  unknown_user_id: null,
  records: [
    {
      type: "TXT",
      name: "_dmarc.reey.ru",
      data: "v=DMARC1; p=none",
      kept: false,
    },
  ],
  created_at: 1,
};

/** The server's own routes, which every page load reads and these tests are not about. */
const server = {
  "GET /api/mail": () => ({
    body: { host: "mail.reey.ru", dns_api: true, certificate: null },
  }),
  "GET /api/relay": () => ({ body: null }),
  "GET /api/queue": () => ({ body: [] }),
};

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

test("the domains, users and admins are listed", async () => {
  fakeServer({
    ...server,
    "GET /api/me": () => ({ body: misha }),
    "GET /api/admins": () => ({ body: [misha] }),
    "GET /api/domains": () => ({ body: [reey] }),
    "GET /api/users": () => ({ body: [inbox] }),
  });
  mount();
  expect(await screen.findByText("reey.ru")).toBeTruthy();
  expect(screen.getAllByText("inbox@reey.ru").length).toBeGreaterThan(0);
  // The signed-in admin is marked in the admins, and is not a user.
  expect(screen.getAllByText("misha").length).toBeGreaterThan(0);
  expect(screen.getByText("you")).toBeTruthy();
});

test("choosing junk sends unknown mail to somebody at the same domain", async () => {
  let domain = reey;
  const { sent } = fakeServer({
    ...server,
    "GET /api/me": () => ({ body: misha }),
    "GET /api/admins": () => ({ body: [misha] }),
    "GET /api/domains": () => ({ body: [domain] }),
    "GET /api/users": () => ({ body: [inbox] }),
    "PATCH /api/domains/dm_1": (body) => {
      const b = body as Pick<Domain, "unknown" | "unknown_user_id">;
      domain = { ...domain, ...b };
      return { body: domain };
    },
  });
  mount();
  const select = await screen.findByLabelText("Unknown addresses");
  fireEvent.change(select, { target: { value: "junk" } });

  await waitFor(() =>
    expect(sent.find((r) => r.method === "PATCH")?.body).toEqual({
      unknown: "junk",
      unknown_user_id: "u_1",
    }),
  );
  expect(await screen.findByLabelText("Who gets the junk")).toBeTruthy();
});

test("a refused change shows the server's sentence", async () => {
  fakeServer({
    ...server,
    "GET /api/me": () => ({ body: misha }),
    "GET /api/admins": () => ({ body: [misha] }),
    "GET /api/domains": () => ({ body: [reey] }),
    "GET /api/users": () => ({ body: [inbox] }),
    "POST /api/domains": () => ({
      status: 409,
      body: {
        code: "conflict",
        message: "mailio already receives for reey.ru.",
      },
    }),
  });
  mount();
  fireEvent.change(await screen.findByLabelText("Domain"), {
    target: { value: "reey.ru" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add domain" }));
  expect(
    await screen.findByText("mailio already receives for reey.ru."),
  ).toBeTruthy();
});

test("a new admin's password is shown once, to be handed over", async () => {
  let admins = [misha];
  const { sent } = fakeServer({
    ...server,
    "GET /api/me": () => ({ body: misha }),
    "GET /api/domains": () => ({ body: [] }),
    "GET /api/users": () => ({ body: [] }),
    "GET /api/admins": () => ({ body: admins }),
    "POST /api/admins": () => {
      const anna: AdminT = { id: "ad_2", name: "anna", created_at: 2 };
      admins = [anna, misha];
      return { status: 201, body: anna };
    },
  });
  mount();
  fireEvent.change(await screen.findByLabelText("Name"), {
    target: { value: "Anna" },
  });
  fireEvent.change(screen.getAllByLabelText("Password").at(-1)!, {
    target: { value: "correct horse" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add admin" }));

  await waitFor(() =>
    expect(sent.find((r) => r.method === "POST")?.body).toEqual({
      name: "Anna",
      password: "correct horse",
    }),
  );
  expect(await screen.findByText("correct horse")).toBeTruthy();
  expect(screen.getByText(/not shown again/)).toBeTruthy();
});

test("a new password gets its own login, shown once with it", async () => {
  const { sent } = fakeServer({
    ...server,
    "GET /api/me": () => ({ body: misha }),
    "GET /api/domains": () => ({ body: [reey] }),
    "GET /api/users": () => ({ body: [inbox] }),
    "GET /api/admins": () => ({ body: [misha] }),
    "POST /api/users/u_1/passwords": () => ({
      status: 201,
      body: {
        id: "pw_2",
        label: "iPhone",
        login: "inbox+k3x9@reey.ru",
        created_at: 2,
        last_used_at: null,
      },
    }),
  });
  mount();
  fireEvent.click(
    await screen.findByRole("button", { name: "Passwords (1)…" }),
  );
  // Listed, and nothing to reveal: only the hash is kept.
  expect(await screen.findByText("default")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "Show" })).toBeNull();

  fireEvent.change(screen.getByLabelText("Label"), {
    target: { value: "iPhone" },
  });
  fireEvent.change(screen.getAllByLabelText("Password").at(0)!, {
    target: { value: "phone password" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Add password" }));
  await waitFor(() =>
    expect(
      sent.find((r) => r.method === "POST" && r.path.endsWith("/passwords"))
        ?.body,
    ).toEqual({ label: "iPhone", password: "phone password" }),
  );
  expect(await screen.findByText("inbox+k3x9@reey.ru")).toBeTruthy();
  expect(screen.getByText("phone password")).toBeTruthy();
  expect(screen.getByText(/not shown again/)).toBeTruthy();
});

test("forwarding a user's mail sends it to somebody else", async () => {
  const billing: User = { ...inbox, id: "u_2", address: "billing@reey.ru" };
  const { sent } = fakeServer({
    ...server,
    "GET /api/me": () => ({ body: misha }),
    "GET /api/domains": () => ({ body: [reey] }),
    "GET /api/users": () => ({ body: [billing, inbox] }),
    "GET /api/admins": () => ({ body: [misha] }),
    "PATCH /api/users/u_2": () => ({
      body: { ...billing, incoming: "forward" },
    }),
  });
  mount();
  const select = (await screen.findAllByLabelText("Incoming mail"))[0]!;
  fireEvent.change(select, { target: { value: "forward" } });
  await waitFor(() =>
    expect(sent.find((r) => r.method === "PATCH")?.body).toEqual({
      incoming: "forward",
      forward_user_id: "u_1",
    }),
  );
});

test("a domain shows the records it needs", async () => {
  fakeServer({
    ...server,
    "GET /api/me": () => ({ body: misha }),
    "GET /api/domains": () => ({ body: [reey] }),
    "GET /api/users": () => ({ body: [inbox] }),
    "GET /api/admins": () => ({ body: [misha] }),
  });
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "DNS…" }));
  expect(await screen.findByText("v=DMARC1; p=none")).toBeTruthy();
});

test("the relay is saved with its password, which is never shown again", async () => {
  let relay: unknown = null;
  const { sent } = fakeServer({
    ...server,
    "GET /api/relay": () => ({ body: relay }),
    "PUT /api/relay": () => {
      relay = {
        host: "smtp.relay.example",
        port: 587,
        security: "starttls",
        username: "me",
        has_password: true,
      };
      return { body: relay };
    },
    "GET /api/me": () => ({ body: misha }),
    "GET /api/domains": () => ({ body: [] }),
    "GET /api/users": () => ({ body: [] }),
    "GET /api/admins": () => ({ body: [misha] }),
  });
  mount();
  fireEvent.change(await screen.findByLabelText("Relay host"), {
    target: { value: "smtp.relay.example" },
  });
  fireEvent.change(screen.getByLabelText("Username"), {
    target: { value: "me" },
  });
  fireEvent.change(screen.getByLabelText("Relay password"), {
    target: { value: "relay secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Save relay" }));
  await waitFor(() =>
    expect(sent.find((r) => r.method === "PUT")?.body).toEqual({
      host: "smtp.relay.example",
      port: 587,
      security: "starttls",
      username: "me",
      password: "relay secret",
    }),
  );
  expect(
    await screen.findByText("Outbound mail goes through smtp.relay.example."),
  ).toBeTruthy();
  expect(
    (screen.getByLabelText("Relay password") as HTMLInputElement).value,
  ).toBe("");
});
