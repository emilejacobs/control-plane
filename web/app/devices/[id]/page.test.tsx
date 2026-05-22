import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { server } from "../../../test/server";
import { renderWithClient } from "../../../test/render";
import { API_BASE } from "../../../lib/api/client";
import DevicePage from "./page";

// The page reads its device id from the route; the per-device tests all
// pin it to "dev-1".
vi.mock("next/navigation", () => ({ useParams: () => ({ id: "dev-1" }) }));

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

  it("shows Unassigned for a device with no site or client", async () => {
    deviceReturns(device({ site_name: null, client_name: null }));
    renderWithClient(<DevicePage />);
    await screen.findByRole("heading", { name: "mac-mini-acme-01" });

    expect(fieldValue("Client")).toBe("Unassigned");
    expect(fieldValue("Site")).toBe("Unassigned");
  });

  it("shows a presence chip reflecting the device's online state", async () => {
    deviceReturns(device({ is_online: true }));
    renderWithClient(<DevicePage />);
    await screen.findByRole("heading", { name: "mac-mini-acme-01" });

    expect(screen.getByText("Online")).toBeInTheDocument();
  });
});
