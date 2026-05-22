import { describe, it, expect, beforeEach } from "vitest";
import { http, HttpResponse } from "msw";
import { server } from "../../test/server";
import { API_BASE, clearTokens, currentTokens } from "./client";
import { firstRun } from "./auth";

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
