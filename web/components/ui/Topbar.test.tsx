import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../../test/server";
import { Topbar } from "./Topbar";
import {
  API_BASE,
  setTokens,
  currentTokens,
  clearTokens,
} from "../../lib/api/client";

const { pushMock } = vi.hoisted(() => ({ pushMock: vi.fn() }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: pushMock }),
  usePathname: () => "/overview",
}));

describe("Topbar sign-out", () => {
  beforeEach(() => {
    pushMock.mockReset();
    setTokens({ accessToken: "a", refreshToken: "r" });
  });

  it("calls POST /auth/logout, clears tokens, and routes to /login", async () => {
    let captured: Request | undefined;
    server.use(
      http.post(`${API_BASE}/auth/logout`, ({ request }) => {
        captured = request.clone();
        return new HttpResponse(null, { status: 204 });
      }),
    );
    render(<Topbar />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: /sign out/i }));

    await waitFor(() => expect(captured).toBeDefined());
    expect(await captured!.json()).toEqual({ refresh_token: "r" });
    expect(currentTokens()).toBeNull();
    expect(pushMock).toHaveBeenCalledWith("/login");
  });

  it("still routes to /login when the backend revoke fails", async () => {
    server.use(
      http.post(`${API_BASE}/auth/logout`, () => HttpResponse.error()),
    );
    render(<Topbar />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: /sign out/i }));

    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/login"));
    expect(currentTokens()).toBeNull();
  });

  it("is a no-op when there are no tokens (defensive)", async () => {
    clearTokens();
    render(<Topbar />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: /sign out/i }));

    // Still routes — useful for the case where the operator's session expired
    // and the topbar is briefly visible before the auth gate kicks in.
    await waitFor(() => expect(pushMock).toHaveBeenCalledWith("/login"));
  });
});
