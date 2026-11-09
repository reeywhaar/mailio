import { vi } from "vitest";

type Reply = { status?: number; body?: unknown };
type Route = (body: unknown, url: URL) => Reply;

/**
 * A stand-in for the Go server: "METHOD /path" to a reply, and every request it was sent, so a
 * test can say what the page asked for as well as what it showed.
 */
export function fakeServer(routes: Record<string, Route>) {
  const sent: { method: string; path: string; body: unknown }[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: string, init?: RequestInit) => {
      const url = new URL(input, "http://mail.example.com");
      const method = init?.method ?? "GET";
      const body = init?.body ? JSON.parse(String(init.body)) : undefined;
      sent.push({ method, path: url.pathname, body });
      const route = routes[`${method} ${url.pathname}`];
      const reply = route
        ? route(body, url)
        : { status: 404, body: { code: "not_found", message: "No route." } };
      const status = reply.status ?? 200;
      return new Response(
        status === 204 ? null : JSON.stringify(reply.body ?? {}),
        { status },
      );
    }),
  );
  return { sent };
}
