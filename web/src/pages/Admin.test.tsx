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

test("a user's password can be shown again", async () => {
  fakeServer({
    ...server,
    "GET /api/me": () => ({ body: misha }),
    "GET /api/domains": () => ({ body: [reey] }),
    "GET /api/users": () => ({ body: [inbox] }),
    "GET /api/admins": () => ({ body: [misha] }),
    "GET /api/users/u_1/password": () => ({
      body: { password: "correct horse" },
    }),
  });
  mount();
  fireEvent.click(await screen.findByRole("button", { name: "Show password" }));
  expect(await screen.findByText("correct horse")).toBeTruthy();
  expect(screen.queryByText(/not shown again/)).toBeNull();
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
