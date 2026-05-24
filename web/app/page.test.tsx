import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE, clearTokens, setTokens } from "../lib/api/client";
import Home from "./page";

// The root page makes two gating decisions on mount:
//   1. /auth/first-run says initialized=false  → redirect to /first-run
//   2. initialized=true but no token in memory → redirect to /login
// Otherwise it renders the Overview.
const { replaceMock } = vi.hoisted(() => ({ replaceMock: vi.fn() }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: replaceMock }),
  usePathname: () => "/",
}));

describe("dashboard root", () => {
  beforeEach(() => {
    replaceMock.mockReset();
    clearTokens();
  });

  it("redirects to /first-run when the system has no operators", async () => {
    server.use(
      http.get(`${API_BASE}/auth/first-run`, () =>
        HttpResponse.json({ initialized: false }),
      ),
    );

    renderWithClient(<Home />);

    await waitFor(() =>
      expect(replaceMock).toHaveBeenCalledWith("/first-run"),
    );
  });

  it("redirects to /login when initialized but no tokens are present", async () => {
    server.use(
      http.get(`${API_BASE}/auth/first-run`, () =>
        HttpResponse.json({ initialized: true }),
      ),
    );

    renderWithClient(<Home />); // tokens cleared in beforeEach

    await waitFor(() =>
      expect(replaceMock).toHaveBeenCalledWith("/login"),
    );
  });

  it("renders the fleet overview when initialized and authenticated", async () => {
    server.use(
      http.get(`${API_BASE}/auth/first-run`, () =>
        HttpResponse.json({ initialized: true }),
      ),
    );
    setTokens({ accessToken: "a", refreshToken: "r" });

    renderWithClient(<Home />);

    expect(
      await screen.findByRole("heading", { level: 1, name: /fleet overview/i }),
    ).toBeInTheDocument();
    expect(replaceMock).not.toHaveBeenCalled();
  });
});
