import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, waitFor, cleanup } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "./server";
import { renderWithClient } from "./render";
import { API_BASE, clearTokens } from "../lib/api/client";
import FirstRunPage from "../app/first-run/page";
import TotpEnrollPage from "../app/totp-enroll/page";
import LoginPage from "../app/login/page";
import DevicesPage from "../app/devices/page";

const { pushMock } = vi.hoisted(() => ({ pushMock: vi.fn() }));
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: pushMock }) }));

// The Issue-16 "minimal E2E equivalent": one test walking the whole auth
// path across all four pages, against an MSW-mocked cp-api, with the token
// store threading the session through each step.
describe("auth flow: first-run → TOTP enroll → login → devices", () => {
  beforeEach(() => {
    clearTokens();
    pushMock.mockReset();
  });

  it("walks an operator from a fresh deployment to the Devices page", async () => {
    server.use(
      http.post(`${API_BASE}/auth/first-run`, () =>
        HttpResponse.json({ access_token: "a1", refresh_token: "r1" }, { status: 201 }),
      ),
      http.post(`${API_BASE}/auth/totp/enroll`, () =>
        HttpResponse.json({
          provisioning_uri: "otpauth://totp/uKnomi:admin@acmecorp.test?secret=ABCDEF",
          recovery_codes: Array.from({ length: 10 }, (_, i) => `rc-${i + 1}`),
        }),
      ),
      http.post(`${API_BASE}/auth/login`, () =>
        HttpResponse.json({
          access_token: "a2",
          refresh_token: "r2",
          requires_totp_enrollment: false,
        }),
      ),
      http.get(`${API_BASE}/devices`, () => HttpResponse.json({ devices: [] })),
    );
    const user = userEvent.setup();

    // 1. Claim the first-run admin account.
    renderWithClient(<FirstRunPage />);
    await user.type(screen.getByLabelText(/email/i), "admin@acmecorp.test");
    await user.type(screen.getByLabelText(/password/i), "correct-horse-battery-staple");
    await user.click(screen.getByRole("button", { name: /create account/i }));
    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/totp-enroll"));
    cleanup();

    // 2. Complete mandatory TOTP enrollment.
    renderWithClient(<TotpEnrollPage />);
    await user.click(screen.getByRole("button", { name: /generate authenticator setup/i }));
    expect(await screen.findByLabelText(/authenticator qr code/i)).toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: /saved my recovery codes/i }));
    await user.click(screen.getByRole("button", { name: /continue/i }));
    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/login"));
    cleanup();

    // 3. Log in with the freshly enrolled second factor.
    renderWithClient(<LoginPage />);
    await user.type(screen.getByLabelText(/email/i), "admin@acmecorp.test");
    await user.type(screen.getByLabelText(/password/i), "correct-horse-battery-staple");
    await user.type(screen.getByLabelText(/authenticator code/i), "123456");
    await user.click(screen.getByRole("button", { name: /sign in/i }));
    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/devices"));
    cleanup();

    // 4. Land on the Devices shell.
    renderWithClient(<DevicesPage />);
    expect(screen.getByRole("heading", { name: /devices/i })).toBeInTheDocument();
    expect(await screen.findByText(/no devices yet/i)).toBeInTheDocument();
  });
});
