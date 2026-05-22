import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { apiRequest, setTokens, clearTokens } from "./client";

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
});
