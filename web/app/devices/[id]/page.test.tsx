import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, act, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../../../test/server";
import { renderWithClient } from "../../../test/render";
import { API_BASE, setTokens, clearTokens } from "../../../lib/api/client";
import DevicePage from "./page";

// The page reads its device id from the route; the per-device tests all
// pin it to "dev-1". RequireAuth (the page's auth gate) also calls
// router.replace, which the same stub absorbs.
vi.mock("next/navigation", () => ({
  useParams: () => ({ id: "dev-1" }),
  usePathname: () => "/devices/dev-1",
  useRouter: () => ({ push: vi.fn(), replace: vi.fn() }),
}));

beforeEach(() => {
  // RequireAuth bounces to /login when no tokens are set; these tests
  // exercise the page as it appears to a signed-in operator.
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  // Default empty PR config so the Plate Recognizer panel loads cleanly (no
  // error alert) in page tests that don't exercise it; individual tests can
  // override. Keeps the panel from polluting unrelated alert assertions.
  server.use(
    http.get(`${API_BASE}/devices/:id/pr-config`, () =>
      HttpResponse.json({ camera_id: "", region: "", webhooks: [], lpr_camera_rtsp_url: "", last_applied_at: null }),
    ),
  );
  return () => clearTokens();
});

// device is a full GET /devices/{id} body; tests override the fields they
// exercise and leave the rest at these benign defaults.
function device(overrides: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    device_id: "dev-1",
    hostname: "mac-mini-acme-01",
    hardware_uuid: "22222222-2222-3333-4444-555555555555",
    hardware_kind: "mac",
    os_version: "macOS 15.0",
    agent_version: "0.1.0",
    iot_thing_arn: "arn:aws:iot:us-east-1:123:thing/dev-1",
    is_online: true,
    last_seen_ago_seconds: 12,
    mtls_cert_expires_at: "2027-01-15T12:00:00Z",
    mtls_cert_days_remaining: 365,
    enrolled_at: "2026-05-01T09:00:00Z",
    site_name: "Acme HQ",
    client_name: "Acme Corp",
    ...overrides,
  };
}

function deviceReturns(body: Record<string, unknown>) {
  server.use(
    http.get(`${API_BASE}/devices/dev-1`, () => HttpResponse.json(body)),
  );
}

// fieldValue reads the <dd> paired with the <dt> carrying the given label.
function fieldValue(label: string): string {
  const term = screen.getByText(label);
  return term.nextElementSibling?.textContent ?? "";
}

describe("per-device view", () => {
  it("renders the device hostname", async () => {
    deviceReturns(device({ hostname: "mac-mini-acme-07" }));
    renderWithClient(<DevicePage />);

    expect(
      await screen.findByRole("heading", { name: "mac-mini-acme-07" }),
    ).toBeInTheDocument();
  });

  it("shows the static fields in a key/value grid", async () => {
    deviceReturns(device());
    renderWithClient(<DevicePage />);
    await screen.findByRole("heading", { name: "mac-mini-acme-01" });

    expect(fieldValue("Client")).toBe("Acme Corp");
    expect(fieldValue("Site")).toBe("Acme HQ");
    expect(fieldValue("Hardware kind")).toBe("mac");
    expect(fieldValue("OS version")).toBe("macOS 15.0");
    expect(fieldValue("Agent version")).toBe("0.1.0");
    expect(fieldValue("Hardware UUID")).toBe(
      "22222222-2222-3333-4444-555555555555",
    );
    expect(fieldValue("IoT Thing ARN")).toBe(
      "arn:aws:iot:us-east-1:123:thing/dev-1",
    );
    expect(fieldValue("Enrolled")).toBe("2026-05-01");
  });

  // Issue #14 shipped lan_ip + tailscale_ip + tailscale_name into the
  // device record; issue #15 renders them so operators can read them
  // off the device page without inspecting the API.
  it("renders LAN IP, Tailscale IP, and Tailscale name on the System card", async () => {
    deviceReturns(
      device({
        lan_ip: "192.168.54.215",
        tailscale_ip: "100.122.190.107",
        tailscale_name: "07-eegees-store54-macmini.tail46201.ts.net",
      }),
    );
    renderWithClient(<DevicePage />);
    await screen.findByRole("heading", { name: "mac-mini-acme-01" });

    expect(fieldValue("LAN IP")).toBe("192.168.54.215");
    expect(fieldValue("Tailscale IP")).toBe("100.122.190.107");
    expect(fieldValue("Tailscale name")).toBe(
      "07-eegees-store54-macmini.tail46201.ts.net",
    );
  });

  it("shows 'Unknown' for null LAN IP / Tailscale IP / Tailscale name (pre-rollout devices)", async () => {
    deviceReturns(
      device({ lan_ip: null, tailscale_ip: null, tailscale_name: null }),
    );
    renderWithClient(<DevicePage />);
    await screen.findByRole("heading", { name: "mac-mini-acme-01" });

    expect(fieldValue("LAN IP")).toBe("Unknown");
    expect(fieldValue("Tailscale IP")).toBe("Unknown");
    expect(fieldValue("Tailscale name")).toBe("Unknown");
  });

  it("shows Unassigned for a device with no site or client", async () => {
    deviceReturns(device({ site_name: null, client_name: null }));
    renderWithClient(<DevicePage />);
    await screen.findByRole("heading", { name: "mac-mini-acme-01" });

    expect(fieldValue("Client")).toBe("Unassigned");
    expect(fieldValue("Site")).toBe("Unassigned");
  });

  it("shows the asset number on the Deployment card", async () => {
    deviceReturns(device({ asset_number: "AC-00237" }));
    renderWithClient(<DevicePage />);
    await screen.findByRole("heading", { name: "mac-mini-acme-01" });

    expect(fieldValue("Asset number")).toBe("AC-00237");
  });

  it("shows Unassigned when the asset number is null", async () => {
    deviceReturns(device({ asset_number: null }));
    renderWithClient(<DevicePage />);
    await screen.findByRole("heading", { name: "mac-mini-acme-01" });

    expect(fieldValue("Asset number")).toBe("Unassigned");
  });

  it("shows a presence chip reflecting the device's online state", async () => {
    deviceReturns(device({ is_online: true }));
    renderWithClient(<DevicePage />);
    await screen.findByRole("heading", { name: "mac-mini-acme-01" });

    // Scope to <main> — the Topbar carries its own "Online" status pill,
    // which is incidental to this assertion.
    const main = screen.getByRole("main");
    expect(within(main).getByText("Online")).toBeInTheDocument();
  });

  it("shows last_seen as an ago-string", async () => {
    deviceReturns(device({ last_seen_ago_seconds: 125 }));
    renderWithClient(<DevicePage />);
    await screen.findByRole("heading", { name: "mac-mini-acme-01" });

    expect(screen.getByText("2 minutes ago")).toBeInTheDocument();
  });

  it("shows Never when the device has never reported a heartbeat", async () => {
    deviceReturns(device({ last_seen_ago_seconds: null }));
    renderWithClient(<DevicePage />);
    await screen.findByRole("heading", { name: "mac-mini-acme-01" });

    expect(screen.getByText(/last seen never/i)).toBeInTheDocument();
  });

  it("shows a loading state while the first query is in flight", () => {
    deviceReturns(device());
    renderWithClient(<DevicePage />);

    expect(screen.getByRole("status")).toHaveTextContent(/loading/i);
  });

  it("shows an error state with a refresh button that retries", async () => {
    let attempt = 0;
    server.use(
      http.get(`${API_BASE}/devices/dev-1`, () => {
        attempt += 1;
        if (attempt === 1) {
          return new HttpResponse(null, { status: 500 });
        }
        return HttpResponse.json(device());
      }),
    );
    renderWithClient(<DevicePage />);

    // The first load fails — error message + a refresh button.
    expect(await screen.findByRole("alert")).toHaveTextContent(
      /could not load/i,
    );
    const refresh = screen.getByRole("button", { name: /refresh/i });

    // Refresh refetches; the retry succeeds and the device page renders.
    await userEvent.click(refresh);
    expect(
      await screen.findByRole("heading", { name: "mac-mini-acme-01" }),
    ).toBeInTheDocument();
  });

  it("shows the mTLS cert expiry with days remaining", async () => {
    deviceReturns(
      device({
        mtls_cert_expires_at: "2027-01-15T12:00:00Z",
        mtls_cert_days_remaining: 365,
      }),
    );
    renderWithClient(<DevicePage />);
    await screen.findByRole("heading", { name: "mac-mini-acme-01" });

    const cert = screen.getByText(/certificate expires/i);
    expect(cert).toHaveTextContent("2027-01-15");
    expect(cert).toHaveTextContent("365 days");
  });

  it("re-renders the ago-string every second between polls", async () => {
    vi.useFakeTimers();
    try {
      deviceReturns(device({ last_seen_ago_seconds: 12 }));
      renderWithClient(<DevicePage />);

      await act(async () => {
        await vi.advanceTimersByTimeAsync(0); // flush the initial queries
      });
      expect(screen.getByText("12 seconds ago")).toBeInTheDocument();

      // Advance the client clock 5s — inside the 10s poll window, so the
      // server is never re-queried. The ago-string still climbs ~5s on its
      // own: it is recomputed from useNow every second, not frozen at the
      // value the last poll returned.
      await act(async () => {
        await vi.advanceTimersByTimeAsync(5_000);
      });
      expect(screen.queryByText("12 seconds ago")).not.toBeInTheDocument();
      expect(screen.getByText(/^1[5-7] seconds ago$/)).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});

// Network scan UX feedback (#12): clicking "Scan network" must give immediate
// in-flight feedback, open the modal in a pending state, and surface POST
// failures inline instead of failing silently.
describe("network scan flow", () => {
  it("loading arc: click flips the button to a disabled in-flight state and opens the modal pending", async () => {
    deviceReturns(device());
    server.use(
      http.post(`${API_BASE}/devices/dev-1/network-scan`, () =>
        HttpResponse.json({ correlation_id: "corr-1" }, { status: 202 }),
      ),
      http.get(`${API_BASE}/devices/dev-1/network-scan/corr-1`, () =>
        HttpResponse.json({
          correlation_id: "corr-1",
          cidr: null,
          status: "pending",
          result: null,
          error_code: null,
          error_message: null,
          requested_at: "2026-06-15T00:00:00Z",
          returned_at: null,
        }),
      ),
    );
    renderWithClient(<DevicePage />);

    await userEvent.click(await screen.findByRole("button", { name: /scan network/i }));

    // Button enters a loading state immediately — no more silent 20s wait.
    expect(await screen.findByRole("button", { name: /scanning/i })).toBeDisabled();
    // Modal opens on correlation_id, before results land, showing pending.
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText(/scanning the device's lan/i)).toBeInTheDocument();
  });

  it("surfaces a POST failure inline instead of failing silently", async () => {
    deviceReturns(device());
    server.use(
      http.post(`${API_BASE}/devices/dev-1/network-scan`, () =>
        HttpResponse.json({ message: "agent offline" }, { status: 500 }),
      ),
    );
    renderWithClient(<DevicePage />);

    await userEvent.click(await screen.findByRole("button", { name: /scan network/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/failed to start network scan/i);
    // No modal, and the button is interactive again (not stuck scanning).
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: /scan network/i })).toBeEnabled();
  });
});
