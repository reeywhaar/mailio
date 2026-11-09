import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, test, vi } from "vitest";

import type { Domain, User } from "@app/api/admin";
import { Admin } from "@app/pages/Admin";
import { fakeServer } from "@app/test/server";

vi.mock("@app/leave", () => ({ leaveFor: vi.fn() }));

afterEach(() => vi.unstubAllGlobals());

const inbox: User = {
  id: "u_1",
  address: "inbox@reey.ru",
  domain_id: "dm_1",
  admin: true,
  created_at: 1,
};

const reey: Domain = {
  id: "dm_1",
  name: "reey.ru",
  unknown: "reject",
  unknown_user_id: null,
  created_at: 1,
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

test("the domains and users are listed", async () => {
  fakeServer({
    "GET /api/me": () => ({ body: inbox }),
    "GET /api/domains": () => ({ body: [reey] }),
    "GET /api/users": () => ({ body: [inbox] }),
  });
  mount();
  expect(await screen.findByText("reey.ru")).toBeTruthy();
  expect(screen.getAllByText("inbox@reey.ru").length).toBeGreaterThan(0);
  expect(screen.getByText("you")).toBeTruthy();
});

test("choosing junk sends unknown mail to somebody at the same domain", async () => {
  let domain = reey;
  const { sent } = fakeServer({
    "GET /api/me": () => ({ body: inbox }),
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
    "GET /api/me": () => ({ body: inbox }),
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
