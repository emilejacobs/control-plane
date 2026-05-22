import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { server } from "../../test/server";
import { renderWithClient } from "../../test/render";
import { API_BASE } from "../../lib/api/client";
import DevicesPage from "./page";

function devicesReturn(devices: Array<Record<string, unknown>>) {
  server.use(
    http.get(`${API_BASE}/devices`, () => HttpResponse.json({ devices })),
  );
}

describe("fleet view", () => {
  it("groups devices by client and site, hostname-sorted within a site", async () => {
    devicesReturn([
      { device_id: "d1", hostname: "mac-z", is_online: true, site_name: "HQ", client_name: "Acme" },
      { device_id: "d2", hostname: "mac-a", is_online: false, site_name: "HQ", client_name: "Acme" },
      { device_id: "d3", hostname: "pi-1", is_online: true, site_name: "Depot", client_name: "Beta" },
      { device_id: "d4", hostname: "mac-unsited", is_online: true, site_name: null, client_name: null },
    ]);
    renderWithClient(<DevicesPage />);

    // Client headings (level 2) and site headings (level 3) render,
    // including the Unassigned client bucket.
    expect(await screen.findByRole("heading", { level: 2, name: "Acme" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 3, name: "HQ" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Beta" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { level: 2, name: "Unassigned" })).toBeInTheDocument();

    // Within Acme HQ, mac-a sorts before mac-z.
    const rows = screen.getAllByRole("listitem").map((li) => li.textContent ?? "");
    const a = rows.findIndex((t) => t.includes("mac-a"));
    const z = rows.findIndex((t) => t.includes("mac-z"));
    expect(a).toBeGreaterThanOrEqual(0);
    expect(a).toBeLessThan(z);
  });
});
