import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Topbar } from "./Topbar";
import {
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

  it("clears tokens and routes to /login when sign-out is clicked", async () => {
    render(<Topbar />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: /sign out/i }));

    expect(currentTokens()).toBeNull();
    expect(pushMock).toHaveBeenCalledWith("/login");
  });

  it("is a no-op when there are no tokens (defensive)", async () => {
    clearTokens();
    render(<Topbar />);
    const user = userEvent.setup();

    await user.click(screen.getByRole("button", { name: /sign out/i }));

    // Still routes — useful for the case where the operator's session expired
    // and the topbar is briefly visible before the auth gate kicks in.
    expect(pushMock).toHaveBeenCalledWith("/login");
  });
});
