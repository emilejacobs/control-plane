// The dashboard's HTTP client for cp-api. It holds the operator's tokens in
// memory and attaches the bearer token to every request (Issue 16). The
// pair is mirrored to localStorage so the operator stays signed in across
// reloads — see ADR-024.

export interface Tokens {
  accessToken: string;
  refreshToken: string;
}

export const API_BASE =
  process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

// TOKEN_STORAGE_KEY is exported only so tests can poke the same key. The
// dashboard never reads it directly outside of setTokens/clearTokens.
export const TOKEN_STORAGE_KEY = "uknomi.tokens";

function hasStorage(): boolean {
  return typeof window !== "undefined" && typeof window.localStorage !== "undefined";
}

function loadFromStorage(): Tokens | null {
  if (!hasStorage()) return null;
  const raw = window.localStorage.getItem(TOKEN_STORAGE_KEY);
  if (raw === null) return null;
  try {
    const parsed = JSON.parse(raw);
    if (
      parsed &&
      typeof parsed.accessToken === "string" &&
      typeof parsed.refreshToken === "string"
    ) {
      return { accessToken: parsed.accessToken, refreshToken: parsed.refreshToken };
    }
    return null;
  } catch {
    return null;
  }
}

let tokens: Tokens | null = loadFromStorage();

export function setTokens(t: Tokens): void {
  tokens = t;
  if (hasStorage()) {
    window.localStorage.setItem(TOKEN_STORAGE_KEY, JSON.stringify(t));
  }
}

export function clearTokens(): void {
  tokens = null;
  if (hasStorage()) {
    window.localStorage.removeItem(TOKEN_STORAGE_KEY);
  }
}

export function currentTokens(): Tokens | null {
  return tokens;
}

// rawRequest issues a single fetch to cp-api, attaching the operator's bearer
// token when one is set and defaulting JSON bodies to application/json.
async function rawRequest(path: string, init: RequestInit): Promise<Response> {
  const headers = new Headers(init.headers);
  if (tokens) {
    headers.set("Authorization", `Bearer ${tokens.accessToken}`);
  }
  if (init.body !== undefined && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  return fetch(`${API_BASE}${path}`, { ...init, headers });
}

// tryRefresh rotates the token pair via POST /auth/refresh. It returns false
// — and clears the now-useless tokens — when the refresh token is rejected.
async function tryRefresh(): Promise<boolean> {
  if (!tokens) return false;
  // /auth/refresh is a mutating POST behind cp-api's idempotency gate, which
  // 400s any mutating request with no Idempotency-Key. tryRefresh bypasses
  // apiRequest (to avoid recursing on its own 401 handling), so it must set
  // the key itself — otherwise every refresh 400s and the session can never
  // rotate past the access-token TTL, silently 401ing the whole dashboard.
  const res = await fetch(`${API_BASE}/auth/refresh`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      "Idempotency-Key": crypto.randomUUID(),
    },
    body: JSON.stringify({ refresh_token: tokens.refreshToken }),
  });
  if (!res.ok) {
    clearTokens();
    return false;
  }
  const body = (await res.json()) as { access_token: string; refresh_token: string };
  setTokens({ accessToken: body.access_token, refreshToken: body.refresh_token });
  return true;
}

// apiRequest issues a request to cp-api. A mutating request without an
// explicit Idempotency-Key gets a generated one (cp-api's idempotency
// middleware requires it). On a 401 it transparently refreshes the token
// pair once and retries — reusing the same key — and a failed refresh
// surfaces the 401.
export async function apiRequest(path: string, init: RequestInit = {}): Promise<Response> {
  const method = (init.method ?? "GET").toUpperCase();
  const headers = new Headers(init.headers);
  if (method !== "GET" && method !== "HEAD" && !headers.has("Idempotency-Key")) {
    headers.set("Idempotency-Key", crypto.randomUUID());
  }
  const normalized: RequestInit = { ...init, headers };

  const res = await rawRequest(path, normalized);
  if (res.status !== 401 || !tokens) {
    return res;
  }
  if (!(await tryRefresh())) {
    return res;
  }
  return rawRequest(path, normalized);
}
