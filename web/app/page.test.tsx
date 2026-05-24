import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE } from "../lib/api/client";
import Home from "./page";

// The root page now probes GET /auth/first-run on mount: an empty system
// must auto-redirect to /first-run instead of forcing the operator to
// type the URL by hand.
const { replaceMock } = vi.hoisted(() => ({ replaceMock: vi.fn() }));
vi.mock("next/navigation", () => ({
  useRouter: () => ({ replace: replaceMock }),
  usePathname: () => "/",
}));

describe("dashboard root", () => {
  beforeEach(() => {
    replaceMock.mockReset();
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

  it("renders the fleet overview when the system is initialized", async () => {
    server.use(
      http.get(`${API_BASE}/auth/first-run`, () =>
        HttpResponse.json({ initialized: true }),
      ),
    );

    renderWithClient(<Home />);

    expect(
      await screen.findByRole("heading", { level: 1, name: /fleet overview/i }),
    ).toBeInTheDocument();
    expect(replaceMock).not.toHaveBeenCalled();
  });
});
