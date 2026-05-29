import { describe, it, expect, afterEach, vi } from "vitest";
import { getFleetAlerts } from "./fleet";

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe("getFleetAlerts", () => {
  it("maps the {probes, services} wire envelope to camelCase", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({
          probes: [
            { probe_name: "plate_recognizer_container", red: ["dev-a"], yellow: ["dev-b"] },
          ],
          services: [{ service_name: "usb_audio", stopped: ["dev-a"] }],
        }),
      ),
    );

    const alerts = await getFleetAlerts();

    expect(alerts.probes).toHaveLength(1);
    expect(alerts.probes[0]).toEqual({
      probeName: "plate_recognizer_container",
      red: ["dev-a"],
      yellow: ["dev-b"],
    });
    expect(alerts.services).toEqual([{ serviceName: "usb_audio", stopped: ["dev-a"] }]);
  });

  it("tolerates missing arrays, defaulting to empty lists", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(jsonResponse({ probes: [{ probe_name: "boot_sanity" }] })),
    );

    const alerts = await getFleetAlerts();

    expect(alerts.probes[0]).toEqual({ probeName: "boot_sanity", red: [], yellow: [] });
    expect(alerts.services).toEqual([]);
  });
});
