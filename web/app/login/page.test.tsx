import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../../test/server";
import { renderWithClient } from "../../test/render";
import { API_BASE, clearTokens } from "../../lib/api/client";
import LoginPage from "./page";

const { pushMock } = vi.hoisted(() => ({ pushMock: vi.fn() }));
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: pushMock }), usePathname: () => "/" }));

// enrolledLogin mocks an enrolled operator: password alone (no code) is
// bounced with Reason: totp-required; a request carrying a TOTP or recovery
// code succeeds. Returns a ref to the last request for body assertions.
function enrolledLogin(): { last?: Request } {
  const ref: { last?: Request } = {};
  server.use(
    http.post(`${API_BASE}/auth/login`, async ({ request }) => {
      ref.last = request.clone();
      const body = (await request.json()) as { totp_code?: string; recovery_code?: string };
      if (!body.totp_code && !body.recovery_code) {
        return new HttpResponse(null, { status: 401, headers: { Reason: "totp-required" } });
      }
      return HttpResponse.json({ access_token: "a", refresh_token: "r", requires_totp_enrollment: false });
    }),
  );
  return ref;
}

beforeEach(() => {
  clearTokens();
  pushMock.mockReset();
});

describe("login page — two-step", () => {
  it("step 1 shows only email + password, no 2FA field", () => {
    renderWithClient(<LoginPage />);
    expect(screen.getByLabelText(/email/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/password/i)).toBeInTheDocument();
    expect(screen.queryByLabelText(/authenticator code/i)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /continue/i })).toBeInTheDocument();
  });

  it("a new operator (no 2FA) signs in on password alone and is routed to set-password", async () => {
    server.use(
      http.post(`${API_BASE}/auth/login`, () =>
        HttpResponse.json({
          access_token: "a",
          refresh_token: "r",
          requires_totp_enrollment: true,
          must_change_password: true,
        }),
      ),
    );
    const user = userEvent.setup();
    renderWithClient(<LoginPage />);
    await user.type(screen.getByLabelText(/email/i), "new@acmecorp.test");
    await user.type(screen.getByLabelText(/password/i), "temp-password-xyz");
    await user.click(screen.getByRole("button", { name: /continue/i }));

    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/set-password"));
    expect(screen.queryByLabelText(/authenticator code/i)).not.toBeInTheDocument();
  });

  it("advances an enrolled operator to the 2FA step, then to devices", async () => {
    enrolledLogin();
    const user = userEvent.setup();
    renderWithClient(<LoginPage />);
    await user.type(screen.getByLabelText(/email/i), "op@acmecorp.test");
    await user.type(screen.getByLabelText(/password/i), "correct-horse-battery-staple");
    await user.click(screen.getByRole("button", { name: /continue/i }));

    const code = await screen.findByLabelText(/authenticator code/i);
    expect(pushMock).not.toHaveBeenCalled();
    await user.type(code, "123456");
    await user.click(screen.getByRole("button", { name: /sign in/i }));
    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/overview"));
  });

  it("can use a recovery code on the 2FA step", async () => {
    const ref = enrolledLogin();
    const user = userEvent.setup();
    renderWithClient(<LoginPage />);
    await user.type(screen.getByLabelText(/email/i), "op@acmecorp.test");
    await user.type(screen.getByLabelText(/password/i), "correct-horse-battery-staple");
    await user.click(screen.getByRole("button", { name: /continue/i }));

    await screen.findByLabelText(/authenticator code/i);
    await user.click(screen.getByRole("button", { name: /use a recovery code/i }));
    await user.type(screen.getByLabelText(/recovery code/i), "abcd-efgh");
    await user.click(screen.getByRole("button", { name: /sign in/i }));

    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/overview"));
    expect(await ref.last!.json()).toMatchObject({ recovery_code: "abcd-efgh" });
  });

  it("shows an error and stays on step 1 for a bad password", async () => {
    server.use(http.post(`${API_BASE}/auth/login`, () => new HttpResponse(null, { status: 401 })));
    const user = userEvent.setup();
    renderWithClient(<LoginPage />);
    await user.type(screen.getByLabelText(/email/i), "op@acmecorp.test");
    await user.type(screen.getByLabelText(/password/i), "wrong");
    await user.click(screen.getByRole("button", { name: /continue/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/invalid email or password/i);
    expect(screen.queryByLabelText(/authenticator code/i)).not.toBeInTheDocument();
  });
});
