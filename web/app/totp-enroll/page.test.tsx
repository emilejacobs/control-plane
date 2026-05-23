import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../../test/server";
import { renderWithClient } from "../../test/render";
import { API_BASE, setTokens } from "../../lib/api/client";
import TotpEnrollPage from "./page";

const { pushMock } = vi.hoisted(() => ({ pushMock: vi.fn() }));
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: pushMock }), usePathname: () => "/" }));

const recoveryCodes = Array.from({ length: 10 }, (_, i) => `code-${i + 1}`);

describe("TOTP enrollment page", () => {
  beforeEach(() => {
    setTokens({ accessToken: "a", refreshToken: "r" }); // arrived authenticated
    pushMock.mockReset();
  });

  it("shows the QR and recovery codes, and gates Continue on confirmation", async () => {
    server.use(
      http.post(`${API_BASE}/auth/totp/enroll`, () =>
        HttpResponse.json({
          provisioning_uri: "otpauth://totp/uKnomi:admin@acmecorp.test?secret=ABCDEF",
          recovery_codes: recoveryCodes,
        }),
      ),
    );
    renderWithClient(<TotpEnrollPage />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: /generate authenticator setup/i }));

    // The QR code and all ten recovery codes are shown.
    expect(await screen.findByLabelText(/authenticator qr code/i)).toBeInTheDocument();
    expect(screen.getByText("code-1")).toBeInTheDocument();
    expect(screen.getByText("code-10")).toBeInTheDocument();

    // Continue is disabled until the operator confirms they saved the codes.
    const cont = screen.getByRole("button", { name: /continue/i });
    expect(cont).toBeDisabled();

    await user.click(screen.getByRole("checkbox", { name: /saved my recovery codes/i }));
    expect(cont).toBeEnabled();

    await user.click(cont);
    expect(pushMock).toHaveBeenCalledWith("/login");
  });
});
