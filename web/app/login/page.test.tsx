import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../../test/server";
import { renderWithClient } from "../../test/render";
import { API_BASE, clearTokens } from "../../lib/api/client";
import LoginPage from "./page";

const { pushMock } = vi.hoisted(() => ({ pushMock: vi.fn() }));
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: pushMock }) }));

function loginReturns(body: Record<string, unknown>) {
  server.use(
    http.post(`${API_BASE}/auth/login`, () =>
      HttpResponse.json({ access_token: "a", refresh_token: "r", ...body }),
    ),
  );
}

async function submitLogin() {
  const user = userEvent.setup();
  await user.type(screen.getByLabelText(/email/i), "op@acmecorp.test");
  await user.type(screen.getByLabelText(/password/i), "correct-horse-battery-staple");
  await user.type(screen.getByLabelText(/code/i), "123456");
  await user.click(screen.getByRole("button", { name: /sign in/i }));
}

describe("login page", () => {
  beforeEach(() => {
    clearTokens();
    pushMock.mockReset();
  });

  it("routes an enrolled operator to the devices page", async () => {
    loginReturns({ requires_totp_enrollment: false });
    renderWithClient(<LoginPage />);

    await submitLogin();

    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/devices"));
  });

  it("routes a not-yet-enrolled operator to TOTP enrollment", async () => {
    loginReturns({ requires_totp_enrollment: true });
    renderWithClient(<LoginPage />);

    await submitLogin();

    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/totp-enroll"));
  });
});
