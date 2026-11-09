/**
 * The only file that mentions fetch.
 *
 * credentials is same-origin and there is no other origin: a request needing CORS would be a
 * bug rather than a feature.
 */

/** A refusal, carrying the server's own code and sentence. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

type Options = {
  method?: string;
  body?: unknown;
  signal?: AbortSignal;
};

export async function request<T>(
  path: string,
  options: Options = {},
): Promise<T> {
  const { method = "GET", body, signal } = options;

  const init: RequestInit = { method, credentials: "same-origin", signal };
  if (body !== undefined) {
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(body);
  }

  const response = await fetch(path, init);
  if (response.status === 204) return undefined as T;

  const text = await response.text();
  const parsed: unknown = text ? JSON.parse(text) : undefined;

  if (!response.ok) {
    const refusal = parsed as { code?: string; message?: string } | undefined;
    // The server writes the sentence for whoever reads it; re-wording it here would mean
    // maintaining two vocabularies for one failure.
    throw new ApiError(
      response.status,
      refusal?.code ?? "invalid",
      refusal?.message ?? "Something went wrong here.",
    );
  }
  return parsed as T;
}

/** The sentence to show for anything a request threw. */
export function messageOf(err: unknown): string {
  return err instanceof ApiError ? err.message : "Something went wrong here.";
}

/** Query strings, with empty values left out rather than sent as blanks. */
export function query(
  params: Record<string, string | number | undefined>,
): string {
  const search = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value !== undefined && value !== "") search.set(key, String(value));
  }
  const rendered = search.toString();
  return rendered ? `?${rendered}` : "";
}
