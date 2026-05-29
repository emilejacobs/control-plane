import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import {
  apiRequest,
  setTokens,
  clearTokens,
  currentTokens,
  currentOperator,
  operatorInitials,
  TOKEN_STORAGE_KEY,
} from "./client";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status });
}

// fakeToken builds a JWT-shaped string with the given claims as its payload
// (base64url). Signature is irrelevant — currentOperator only decodes.
function fakeToken(claims: Record<string, unknown>): string {
  const b64 = (o: unknown) =>
    Buffer.from(JSON.stringify(o)).toString("base64url");
  return `${b64({ alg: "HS256" })}.${b64(claims)}.sig`;
}

describe("currentOperator + operatorInitials", () => {
  beforeEach(() => clearTokens());

  it("derives 2-char initials from the email local part", () => {
    expect(operatorInitials("ejacobs@uknomi.com")).toBe("EJ");
    expect(operatorInitials("sguddati@uknomi.com")).toBe("SG");
    expect(operatorInitials("x@y.com")).toBe("X");
  });

  it("returns null when logged out", () => {
    expect(currentOperator()).toBeNull();
  });

  it("decodes email + is_staff from the access token", () => {
    setTokens({
      accessToken: fakeToken({ email: "ejacobs@uknomi.com", is_staff: true }),
      refreshToken: "r",
    });
    expect(currentOperator()).toEqual({ email: "ejacobs@uknomi.com", isStaff: true });
  });

  it("returns null for a malformed token rather than throwing", () => {
    setTokens({ accessToken: "not-a-jwt", refreshToken: "r" });
    expect(currentOperator()).toBeNull();
  });
});

describe("token persistence (ADR-024)", () => {
  beforeEach(() => {
    clearTokens();
    localStorage.removeItem(TOKEN_STORAGE_KEY);
  });

  it("mirrors setTokens into localStorage so a reload keeps the operator signed in", () => {
    setTokens({ accessToken: "a-1", refreshToken: "r-1" });

    const raw = localStorage.getItem(TOKEN_STORAGE_KEY);
    expect(raw).not.toBeNull();
    expect(JSON.parse(raw!)).toEqual({ accessToken: "a-1", refreshToken: "r-1" });
  });

  it("clearTokens removes the localStorage entry", () => {
    setTokens({ accessToken: "a-1", refreshToken: "r-1" });
    clearTokens();

    expect(localStorage.getItem(TOKEN_STORAGE_KEY)).toBeNull();
    expect(currentTokens()).toBeNull();
  });

  it("rehydrates tokens from localStorage when the module is freshly imported", async () => {
    localStorage.setItem(
      TOKEN_STORAGE_KEY,
      JSON.stringify({ accessToken: "persisted-access", refreshToken: "persisted-refresh" }),
    );

    vi.resetModules();
    const fresh = await import("./client");

    expect(fresh.currentTokens()).toEqual({
      accessToken: "persisted-access",
      refreshToken: "persisted-refresh",
    });
  });

  it("treats malformed localStorage state as no session, without throwing", async () => {
    localStorage.setItem(TOKEN_STORAGE_KEY, "not json");

    vi.resetModules();
    const fresh = await import("./client");

    expect(fresh.currentTokens()).toBeNull();
  });
});

describe("apiRequest", () => {
  beforeEach(() => {
    clearTokens();
    localStorage.removeItem(TOKEN_STORAGE_KEY);
  });
  afterEach(() => vi.unstubAllGlobals());

  it("attaches the operator's bearer token when one is set", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    setTokens({ accessToken: "access-abc", refreshToken: "refresh-xyz" });
    await apiRequest("/devices");

    expect(fetchMock).toHaveBeenCalledOnce();
    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(new Headers(init.headers).get("Authorization")).toBe("Bearer access-abc");
  });

  it("omits the Authorization header when no token is set", async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response("{}", { status: 200 }));
    vi.stubGlobal("fetch", fetchMock);

    await apiRequest("/devices");

    const init = fetchMock.mock.calls[0][1] as RequestInit;
    expect(new Headers(init.headers).has("Authorization")).toBe(false);
  });

  it("refreshes the access token on 401 and retries the request", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response("unauthorized", { status: 401 }))
      .mockResolvedValueOnce(jsonResponse({ access_token: "access-2", refresh_token: "refresh-2" }))
      .mockResolvedValueOnce(jsonResponse({ devices: [] }));
    vi.stubGlobal("fetch", fetchMock);

    setTokens({ accessToken: "access-1", refreshToken: "refresh-1" });
    const res = await apiRequest("/devices");

    expect(res.status).toBe(200);
    expect(fetchMock).toHaveBeenCalledTimes(3);

    // The refresh call presented the refresh token.
    expect(String(fetchMock.mock.calls[1][0])).toContain("/auth/refresh");
    expect(JSON.parse(fetchMock.mock.calls[1][1].body as string)).toEqual({
      refresh_token: "refresh-1",
    });
    // /auth/refresh is a mutating POST behind the idempotency gate, which
    // 400s a request with no Idempotency-Key — so the refresh call must
    // carry one, just like every other mutating call.
    const refreshInit = fetchMock.mock.calls[1][1] as RequestInit;
    expect(new Headers(refreshInit.headers).get("Idempotency-Key")).toBeTruthy();

    // The retry carried the rotated-in access token.
    const retryInit = fetchMock.mock.calls[2][1] as RequestInit;
    expect(new Headers(retryInit.headers).get("Authorization")).toBe("Bearer access-2");
  });

  it("gives up and clears tokens when the refresh itself fails", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(new Response("unauthorized", { status: 401 }))
      .mockResolvedValueOnce(new Response("nope", { status: 401 }));
    vi.stubGlobal("fetch", fetchMock);

    setTokens({ accessToken: "access-1", refreshToken: "refresh-1" });
    const res = await apiRequest("/devices");

    expect(res.status).toBe(401);
    expect(currentTokens()).toBeNull();
    expect(fetchMock).toHaveBeenCalledTimes(2); // no retry after a failed refresh
  });
});
