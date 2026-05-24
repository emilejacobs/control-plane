import { describe, it, expect, beforeEach } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "../../test/server";
import { API_BASE, clearTokens, currentTokens, setTokens } from "./client";
import { firstRun, login, logout } from "./auth";

describe("firstRun", () => {
  beforeEach(() => clearTokens());

  it("posts credentials, stores the returned tokens, and sends an Idempotency-Key", async () => {
    let captured: Request | undefined;
    server.use(
      http.post(`${API_BASE}/auth/first-run`, ({ request }) => {
        captured = request.clone();
        return HttpResponse.json(
          { access_token: "access-1", refresh_token: "refresh-1" },
          { status: 201 },
        );
      }),
    );

    await firstRun("admin@acmecorp.test", "correct-horse-battery-staple");

    expect(captured).toBeDefined();
    expect(await captured!.json()).toEqual({
      email: "admin@acmecorp.test",
      password: "correct-horse-battery-staple",
    });
    // POST /auth/first-run is behind cp-api's idempotency middleware.
    expect(captured!.headers.get("Idempotency-Key")).toBeTruthy();
    expect(currentTokens()).toEqual({ accessToken: "access-1", refreshToken: "refresh-1" });
  });
});

describe("login", () => {
  beforeEach(() => clearTokens());

  it("posts credentials with a TOTP code, stores tokens, and reports the enrollment flag", async () => {
    let captured: Request | undefined;
    server.use(
      http.post(`${API_BASE}/auth/login`, ({ request }) => {
        captured = request.clone();
        return HttpResponse.json({
          access_token: "access-9",
          refresh_token: "refresh-9",
          requires_totp_enrollment: false,
        });
      }),
    );

    const result = await login({
      email: "op@acmecorp.test",
      password: "correct-horse-battery-staple",
      totpCode: "123456",
    });

    expect(await captured!.json()).toMatchObject({
      email: "op@acmecorp.test",
      password: "correct-horse-battery-staple",
      totp_code: "123456",
    });
    expect(result.requiresTotpEnrollment).toBe(false);
    expect(currentTokens()).toEqual({ accessToken: "access-9", refreshToken: "refresh-9" });
  });

  it("can present a recovery code in place of a TOTP code", async () => {
    let captured: Request | undefined;
    server.use(
      http.post(`${API_BASE}/auth/login`, ({ request }) => {
        captured = request.clone();
        return HttpResponse.json({
          access_token: "a",
          refresh_token: "r",
          requires_totp_enrollment: false,
        });
      }),
    );

    await login({
      email: "op@acmecorp.test",
      password: "correct-horse-battery-staple",
      recoveryCode: "abcd-efgh",
    });

    expect(await captured!.json()).toMatchObject({ recovery_code: "abcd-efgh" });
  });
});

describe("logout", () => {
  beforeEach(() => clearTokens());

  it("posts the current refresh token to /auth/logout", async () => {
    let captured: Request | undefined;
    server.use(
      http.post(`${API_BASE}/auth/logout`, ({ request }) => {
        captured = request.clone();
        return new HttpResponse(null, { status: 204 });
      }),
    );

    setTokens({ accessToken: "a-9", refreshToken: "r-9" });
    await logout();

    expect(captured).toBeDefined();
    expect(await captured!.json()).toEqual({ refresh_token: "r-9" });
  });

  it("is a no-op when no tokens are set (defensive)", async () => {
    let called = false;
    server.use(
      http.post(`${API_BASE}/auth/logout`, () => {
        called = true;
        return new HttpResponse(null, { status: 204 });
      }),
    );

    await logout();

    expect(called).toBe(false);
  });

  it("swallows network errors so Sign out always completes locally", async () => {
    server.use(
      http.post(`${API_BASE}/auth/logout`, () => HttpResponse.error()),
    );

    setTokens({ accessToken: "a", refreshToken: "r" });
    await expect(logout()).resolves.toBeUndefined();
    // currentTokens is untouched — logout() doesn't clear, callers do.
    expect(currentTokens()).not.toBeNull();
  });
});
