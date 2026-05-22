// The dashboard's HTTP client for cp-api. It holds the operator's tokens in
// memory and attaches the bearer token to every request (Issue 16).

export interface Tokens {
  accessToken: string;
  refreshToken: string;
}

export const API_BASE =
  process.env.NEXT_PUBLIC_API_URL ?? "http://localhost:8080";

let tokens: Tokens | null = null;

export function setTokens(t: Tokens): void {
  tokens = t;
}

export function clearTokens(): void {
  tokens = null;
}

export function currentTokens(): Tokens | null {
  return tokens;
}

// apiRequest issues a fetch to cp-api, attaching the operator's bearer token
// when one is set and defaulting JSON bodies to application/json.
export async function apiRequest(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  if (tokens) {
    headers.set("Authorization", `Bearer ${tokens.accessToken}`);
  }
  if (init.body !== undefined && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  return fetch(`${API_BASE}${path}`, { ...init, headers });
}
