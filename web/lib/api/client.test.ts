import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { apiRequest, setTokens, clearTokens, currentTokens } from "./client";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status });
}

describe("apiRequest", () => {
  beforeEach(() => clearTokens());
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
