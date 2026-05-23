import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../../test/server";
import { renderWithClient } from "../../test/render";
import { API_BASE, clearTokens } from "../../lib/api/client";
import FirstRunPage from "./page";

const { pushMock } = vi.hoisted(() => ({ pushMock: vi.fn() }));
vi.mock("next/navigation", () => ({ useRouter: () => ({ push: pushMock }), usePathname: () => "/" }));

describe("first-run page", () => {
  beforeEach(() => {
    clearTokens();
    pushMock.mockReset();
  });

  it("creates the admin account and routes to TOTP enrollment", async () => {
    server.use(
      http.post(`${API_BASE}/auth/first-run`, () =>
        HttpResponse.json({ access_token: "a", refresh_token: "r" }, { status: 201 }),
      ),
    );

    renderWithClient(<FirstRunPage />);
    const user = userEvent.setup();

    await user.type(screen.getByLabelText(/email/i), "admin@acmecorp.test");
    await user.type(screen.getByLabelText(/password/i), "correct-horse-battery-staple");
    await user.click(screen.getByRole("button", { name: /create account/i }));

    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/totp-enroll"));
  });
});
