import { describe, it, expect, beforeEach } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "../../test/server";
import { API_BASE, clearTokens, currentTokens } from "./client";
import { firstRun, login } from "./auth";

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
