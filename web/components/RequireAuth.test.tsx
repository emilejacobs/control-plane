import { describe, it, expect, beforeEach, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { RequireAuth } from "./RequireAuth";
import { setTokens, clearTokens } from "../lib/api/client";

const { replaceMock } = vi.hoisted(() => ({ replaceMock: vi.fn() }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: replaceMock }),
}));

describe("RequireAuth", () => {
  beforeEach(() => {
    clearTokens();
    replaceMock.mockReset();
  });

  it("renders children when an access token is present", () => {
    setTokens({ accessToken: "a", refreshToken: "r" });

    render(
      <RequireAuth>
        <div>fleet content</div>
      </RequireAuth>,
    );

    expect(screen.getByText("fleet content")).toBeInTheDocument();
    expect(replaceMock).not.toHaveBeenCalled();
  });

  it("redirects to /login and does not render children when no tokens are set", () => {
    render(
      <RequireAuth>
        <div>fleet content</div>
      </RequireAuth>,
    );

    expect(replaceMock).toHaveBeenCalledWith("/login");
    expect(screen.queryByText("fleet content")).not.toBeInTheDocument();
  });
});
