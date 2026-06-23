import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { EditServicesModal } from "./EditServicesModal";
import type { Device } from "../lib/api/devices";

function deviceFixture(overrides: Partial<Device> = {}): Device {
  return {
    deviceId: "dev-1",
    hostname: "mac-mini-acme-01",
    hardwareUuid: "u",
    hardwareKind: "mac",
    osVersion: "macOS 15",
    agentVersion: "0.1.0",
    iotThingArn: "arn",
    isOnline: true,
    lastSeenAt: new Date(),
    certExpiresAt: null,
    certDaysRemaining: 365,
    enrolledAt: "2026-05-01T09:00:00Z",
    siteId: null,
    siteName: null,
    clientName: null,
    assetNumber: null,
    lanIp: null,
    tailscaleIp: null,
    tailscaleName: null,
    snapshotCadence: "weekly",
    alprLicenseSet: false,
    services: [
      { name: "com.uknomi.webui", state: "running", stateSince: "2026-05-24T17:00:00Z", lastReported: "2026-05-24T17:59:30Z" },
      { name: "com.tailscale.tailscaled", state: "running", stateSince: "2026-05-24T17:00:00Z", lastReported: "2026-05-24T17:59:30Z" },
    ],
    serviceConfig: {
      allowListOverride: null,
      intervalOverride: null,
      lastAppliedAt: null,
      lastAppliedCorrelationId: null,
    },
    lastBootTime: null,
    lastShutdownCause: null,
    lastShutdownCauseCode: null,
    recentReboots: [],
    ...overrides,
  };
}

afterEach(() => {
  cleanup();
  clearTokens();
});

describe("EditServicesModal", () => {
  it("seeds the textarea from the observed services when no override is set", () => {
    render(
      <EditServicesModal
        device={deviceFixture()}
        onClose={() => {}}
        onApplied={() => {}}
      />,
    );
    const textarea = screen.getByLabelText("Service allow-list") as HTMLTextAreaElement;
    expect(textarea.value).toContain("com.uknomi.webui");
    expect(textarea.value).toContain("com.tailscale.tailscaled");
  });

  it("seeds from allowListOverride when one is set", () => {
    render(
      <EditServicesModal
        device={deviceFixture({
          serviceConfig: {
            allowListOverride: ["anydesk"],
            intervalOverride: "2m",
            lastAppliedAt: null,
            lastAppliedCorrelationId: null,
          },
        })}
        onClose={() => {}}
        onApplied={() => {}}
      />,
    );
    const textarea = screen.getByLabelText("Service allow-list") as HTMLTextAreaElement;
    expect(textarea.value).toBe("anydesk");
    const interval = screen.getByLabelText("Reporting interval") as HTMLInputElement;
    expect(interval.value).toBe("2m");
  });

  it("happy path: PUT 202 + matching poll response transitions to applied", async () => {
    setTokens({ accessToken: "t", refreshToken: "r" });
    const user = userEvent.setup();
    const onApplied = vi.fn();
    const onClose = vi.fn();

    let putBody: unknown = null;
    server.use(
      http.put(`${API_BASE}/devices/dev-1/service-config`, async ({ request }) => {
        putBody = await request.json();
        return HttpResponse.json({ correlation_id: "corr-applied-99" }, { status: 202 });
      }),
      http.get(`${API_BASE}/devices/dev-1`, () =>
        HttpResponse.json({
          device_id: "dev-1",
          hostname: "mac-mini-acme-01",
          hardware_uuid: "u",
          hardware_kind: "mac",
          os_version: "macOS 15",
          agent_version: "0.1.0",
          iot_thing_arn: "arn",
          is_online: true,
          last_seen_ago_seconds: 5,
          mtls_cert_expires_at: null,
          mtls_cert_days_remaining: 365,
          enrolled_at: "2026-05-01T09:00:00Z",
          site_name: null,
          client_name: null,
          services: [],
          service_config: {
            allow_list_override: ["new"],
            interval_override: "1m",
            last_applied_at: "2026-05-24T19:00:00Z",
            last_applied_correlation_id: "corr-applied-99",
          },
        }),
      ),
    );

    render(<EditServicesModal device={deviceFixture()} onClose={onClose} onApplied={onApplied} />);
    const textarea = screen.getByLabelText("Service allow-list") as HTMLTextAreaElement;
    await user.clear(textarea);
    await user.type(textarea, "new");
    const interval = screen.getByLabelText("Reporting interval") as HTMLInputElement;
    await user.clear(interval);
    await user.type(interval, "1m");
    await user.click(screen.getByRole("button", { name: /save/i }));

    // Wait for the saving → waiting → applied transition (poll fires
    // every 2s; the test's polling default is generous enough).
    await waitFor(
      () => {
        expect(screen.getByText(/^Applied\.$/)).toBeInTheDocument();
      },
      { timeout: 5000 },
    );
    await waitFor(() => expect(onApplied).toHaveBeenCalled());
    expect(putBody).toEqual({
      service_allow_list: ["new"],
      service_status_interval: "1m",
    });
  });

  it("surfaces a 400 validation error from cp-api", async () => {
    setTokens({ accessToken: "t", refreshToken: "r" });
    const user = userEvent.setup();

    server.use(
      http.put(`${API_BASE}/devices/dev-1/service-config`, () =>
        HttpResponse.json(
          { code: "config_update.bad_interval", message: "interval 1s outside 30s..1h0m0s" },
          { status: 400 },
        ),
      ),
    );

    render(<EditServicesModal device={deviceFixture()} onClose={() => {}} onApplied={() => {}} />);
    const interval = screen.getByLabelText("Reporting interval") as HTMLInputElement;
    await user.clear(interval);
    await user.type(interval, "1s");
    await user.click(screen.getByRole("button", { name: /save/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/bad_interval|interval/i);
  });

  it("Cancel calls onClose when not busy", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();
    render(<EditServicesModal device={deviceFixture()} onClose={onClose} onApplied={() => {}} />);
    await user.click(screen.getByRole("button", { name: /cancel/i }));
    expect(onClose).toHaveBeenCalled();
  });
});
