import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, within } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { server } from "../../test/server";
import { renderWithClient } from "../../test/render";
import { API_BASE, setTokens, clearTokens } from "../../lib/api/client";
import RolloutsPage from "./page";

// Topbar + RequireAuth both reach for next/navigation; stub it so the full
// page renders in isolation (same pattern as the fleet-view test).
vi.mock("next/navigation", () => ({
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
  usePathname: () => "/rollouts",
}));

beforeEach(() => {
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  return () => clearTokens();
});

// fakeToken builds a JWT-shaped access token carrying the given claims, so the
// page's currentOperator()/is_staff gate can be exercised (sig is irrelevant —
// only the payload is decoded).
function fakeToken(claims: Record<string, unknown>): string {
  const b64 = (o: unknown) => Buffer.from(JSON.stringify(o)).toString("base64url");
  return `${b64({ alg: "HS256" })}.${b64(claims)}.sig`;
}

function rolloutReturns(body: Record<string, unknown>) {
  server.use(
    http.get(`${API_BASE}/fleet/agent-rollout`, () => HttpResponse.json(body)),
  );
}

const sample = {
  counts: { done: 2, in_flight: 1, untargeted: 3 },
  devices: [
    {
      id: "d1",
      hostname: "mac-inflight",
      site_name: "HQ",
      client_name: "Acme",
      reported_version: "1.4.0",
      desired_version: "1.4.1",
      is_online: true,
      state: "in_flight",
    },
    {
      id: "d2",
      hostname: "mac-converged",
      site_name: "HQ",
      client_name: "Acme",
      reported_version: "1.4.1",
      desired_version: "1.4.1",
      is_online: true,
      state: "done",
    },
    {
      id: "d3",
      hostname: "mac-untargeted",
      site_name: null,
      client_name: null,
      reported_version: "1.4.0",
      desired_version: null,
      is_online: false,
      state: "untargeted",
    },
  ],
};

describe("rollout dashboard — read view", () => {
  it("renders the roll-up counts from GET /fleet/agent-rollout", async () => {
    rolloutReturns(sample);
    renderWithClient(<RolloutsPage />);

    // Each count is a labelled stat; assert the value sits with its label.
    const converged = await screen.findByTestId("rollup-done");
    expect(converged).toHaveTextContent("2");
    expect(screen.getByTestId("rollup-in_flight")).toHaveTextContent("1");
    expect(screen.getByTestId("rollup-untargeted")).toHaveTextContent("3");
  });

  it("renders a per-device desired-vs-reported row with its state", async () => {
    rolloutReturns(sample);
    renderWithClient(<RolloutsPage />);

    const row = await screen.findByRole("row", { name: /mac-inflight/ });
    // reported -> desired versions both visible on the row
    expect(row).toHaveTextContent("1.4.0");
    expect(row).toHaveTextContent("1.4.1");
    // and the derived state surfaces as a readable label
    expect(within(row).getByText(/in flight/i)).toBeInTheDocument();
  });

  it("links each device row to its detail page", async () => {
    rolloutReturns(sample);
    renderWithClient(<RolloutsPage />);

    const row = await screen.findByRole("row", { name: /mac-converged/ });
    expect(within(row).getByRole("link", { name: /mac-converged/ })).toHaveAttribute(
      "href",
      "/devices/d2",
    );
  });

  it("shows an em dash for an untargeted device's (null) desired version", async () => {
    rolloutReturns(sample);
    renderWithClient(<RolloutsPage />);

    const row = await screen.findByRole("row", { name: /mac-untargeted/ });
    // Exact match: the hostname "mac-untargeted" also contains "untargeted",
    // so match the state pill's exact label rather than a substring.
    expect(within(row).getByText("Untargeted")).toBeInTheDocument();
    expect(row).toHaveTextContent("—");
  });

  it("hides the start-rollout control from a non-staff operator", async () => {
    rolloutReturns(sample);
    renderWithClient(<RolloutsPage />);

    // Read view still renders for a scoped operator.
    await screen.findByTestId("rollup-done");
    expect(screen.queryByText(/start a rollout/i)).not.toBeInTheDocument();
  });

  it("shows the start-rollout control to a staff operator", async () => {
    setTokens({
      accessToken: fakeToken({ email: "staff@uknomi.com", is_staff: true }),
      refreshToken: "r",
    });
    rolloutReturns(sample);
    server.use(
      http.get(`${API_BASE}/fleet/agent-versions`, () =>
        HttpResponse.json({ versions: ["1.4.1"] }),
      ),
      http.get(`${API_BASE}/sites`, () => HttpResponse.json({ clients: [] })),
    );
    renderWithClient(<RolloutsPage />);

    expect(await screen.findByText(/start a rollout/i)).toBeInTheDocument();
    expect(await screen.findByRole("button", { name: /start rollout/i })).toBeInTheDocument();
  });

  it("polls GET /fleet/agent-rollout every 10 seconds", async () => {
    vi.useFakeTimers();
    try {
      let calls = 0;
      server.use(
        http.get(`${API_BASE}/fleet/agent-rollout`, () => {
          calls += 1;
          return HttpResponse.json(sample);
        }),
      );
      renderWithClient(<RolloutsPage />);

      await vi.advanceTimersByTimeAsync(0);
      expect(calls).toBe(1);
      await vi.advanceTimersByTimeAsync(10_000);
      expect(calls).toBe(2);
    } finally {
      vi.useRealTimers();
    }
  });
});
