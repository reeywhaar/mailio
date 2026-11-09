import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";

import { leaveFor } from "@app/leave";
import { AdminLink, SignIn } from "@app/pages/Login";
import { fakeServer } from "@app/test/server";

vi.mock("@app/leave", () => ({ leaveFor: vi.fn() }));

afterEach(() => {
  vi.unstubAllGlobals();
  vi.mocked(leaveFor).mockClear();
});

test("a dead admin link says so before anybody types into it", async () => {
  fakeServer({
    "POST /api/admin-link/check": () => ({
      status: 404,
      body: { code: "not_found", message: "Used." },
    }),
  });
  render(<AdminLink token="dead" />);
  expect(await screen.findByText(/has been used or has expired/)).toBeTruthy();
  expect(screen.queryByPlaceholderText(/^Name/)).toBeNull();
});

test("a live admin link is spent on a name and password, then the app opens", async () => {
  const { sent } = fakeServer({
    "POST /api/admin-link/check": () => ({ body: { expires_at: 1 } }),
    "POST /api/admin-link": () => ({ status: 204 }),
  });
  render(<AdminLink token="live" />);
  const button = await screen.findByRole("button", {
    name: "Save and sign in",
  });
  await waitFor(() =>
    expect((button as HTMLButtonElement).disabled).toBe(false),
  );

  fireEvent.change(screen.getByPlaceholderText(/^Name/), {
    target: { value: "misha" },
  });
  fireEvent.change(screen.getByPlaceholderText(/^Password/), {
    target: { value: "correct horse" },
  });
  fireEvent.click(button);

  await waitFor(() => expect(leaveFor).toHaveBeenCalledWith("/"));
  expect(sent.at(-1)).toEqual({
    method: "POST",
    path: "/api/admin-link",
    body: {
      token: "live",
      name: "misha",
      password: "correct horse",
    },
  });
});

test("signing in shows the server's refusal and stays on the page", async () => {
  fakeServer({
    "POST /api/session": () => ({
      status: 401,
      body: {
        code: "unauthenticated",
        message: "That name and password do not match.",
      },
    }),
  });
  render(<SignIn />);
  fireEvent.change(screen.getByPlaceholderText("Name"), {
    target: { value: "misha" },
  });
  fireEvent.change(screen.getByPlaceholderText("Password"), {
    target: { value: "whatever1" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
  expect(
    await screen.findByText("That name and password do not match."),
  ).toBeTruthy();
  expect(leaveFor).not.toHaveBeenCalled();
});
