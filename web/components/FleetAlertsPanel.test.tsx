import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { FleetAlertsPanel } from "./FleetAlertsPanel";
import type { FleetAlerts } from "../lib/api/fleet";
import type { DeviceSummary } from "../lib/api/devices";

function device(overrides: Partial<DeviceSummary>): DeviceSummary {
  return {
    deviceId: "dev-a",
    hostname: "mac-a",
    isOnline: true,
    siteName: "Store 54",
    clientName: "AcmeCorp",
    agentVersion: "1.0.0",
    certExpiresAt: null,
    certDaysRemaining: null,
    ...overrides,
  };
}

const devices: DeviceSummary[] = [
  device({ deviceId: "dev-a", hostname: "mac-a", siteName: "Store 54" }),
  device({ deviceId: "dev-b", hostname: "mac-b", siteName: "Store 12" }),
];

afterEach(cleanup);

describe("FleetAlertsPanel — alert-only", () => {
  it("renders nothing when there are no probe or service alerts", () => {
    const { container } = render(
      <FleetAlertsPanel alerts={{ probes: [], services: [] }} devices={devices} />,
    );
    expect(container).toBeEmptyDOMElement();
  });
});

describe("FleetAlertsPanel — populated", () => {
  const alerts: FleetAlerts = {
    probes: [
      { probeName: "plate_recognizer_container", red: ["dev-a"], yellow: ["dev-b"] },
    ],
    services: [{ serviceName: "usb_audio", stopped: ["dev-a"] }],
  };

  it("shows a row per probe and service with affected device counts", () => {
    render(<FleetAlertsPanel alerts={alerts} devices={devices} />);

    expect(screen.getByText("plate_recognizer_container")).toBeInTheDocument();
    expect(screen.getByText("usb_audio")).toBeInTheDocument();
    // 1 red + 1 yellow on the probe; 1 stopped on the service.
    expect(screen.getByText(/1 red/i)).toBeInTheDocument();
    expect(screen.getByText(/1 yellow/i)).toBeInTheDocument();
    expect(screen.getByText(/1 stopped/i)).toBeInTheDocument();
  });

  it("drills down to the affected devices when a probe row is expanded", async () => {
    const user = userEvent.setup();
    render(<FleetAlertsPanel alerts={alerts} devices={devices} />);

    // Affected devices are hidden until the row is expanded.
    expect(screen.queryByRole("link", { name: /mac-a/ })).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /plate_recognizer_container/ }));

    const linkA = screen.getByRole("link", { name: /mac-a/ });
    const linkB = screen.getByRole("link", { name: /mac-b/ });
    expect(linkA).toHaveAttribute("href", "/devices/dev-a");
    expect(linkB).toHaveAttribute("href", "/devices/dev-b");
    // Site name surfaces alongside the hostname for context.
    expect(screen.getByText(/Store 54/)).toBeInTheDocument();
  });

  it("falls back to the device id when it is not in the fleet list", async () => {
    const user = userEvent.setup();
    render(
      <FleetAlertsPanel
        alerts={{ probes: [{ probeName: "boot_sanity", red: ["ghost-id"], yellow: [] }], services: [] }}
        devices={devices}
      />,
    );
    await user.click(screen.getByRole("button", { name: /boot_sanity/ }));
    expect(screen.getByRole("link", { name: /ghost-id/ })).toHaveAttribute(
      "href",
      "/devices/ghost-id",
    );
  });
});
