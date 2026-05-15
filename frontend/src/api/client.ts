/* Shared fetch wrapper.
 *
 * Two responsibilities:
 *   1. Prepend the API base URL (set at build time by Vite).
 *   2. Decode the canonical error envelope into an `ApiError` that
 *      callers can surface via `user_action`. The UI layer reads
 *      `error.user_action` verbatim — see docs/architecture.md.
 */

const API_BASE: string = import.meta.env.VITE_API_BASE || "http://localhost:8080";

export class ApiError extends Error {
  code: string;
  status: number;
  userAction: string;
  details: Record<string, unknown>;

  constructor(opts: {
    code: string;
    message: string;
    status: number;
    userAction?: string;
    details?: Record<string, unknown>;
  }) {
    super(opts.message);
    this.code = opts.code;
    this.status = opts.status;
    this.userAction = opts.userAction || "";
    this.details = opts.details || {};
  }
}

export async function api<T = unknown>(
  path: string,
  init?: RequestInit
): Promise<T> {
  const res = await fetch(API_BASE + path, {
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers || {}),
    },
    ...init,
  });

  if (!res.ok) {
    let body: { error?: { code?: string; message?: string; user_action?: string; details?: Record<string, unknown> } } = {};
    try {
      body = await res.json();
    } catch {
      // Server returned non-JSON — fall through with a generic message.
    }
    throw new ApiError({
      code: body.error?.code || "http_" + res.status,
      message: body.error?.message || res.statusText || "Request failed",
      userAction: body.error?.user_action,
      details: body.error?.details,
      status: res.status,
    });
  }

  // 204 No Content
  if (res.status === 204) return undefined as T;

  return (await res.json()) as T;
}
