import { describe, it, expect } from "vitest";
import { edgePreviewURL } from "./devices";

// Issue #14: edgePreviewURL prefers tailscaleName over hostname.
// The bench-Mac drift case (2026-05-26): device.hostname was the
// macOS LocalHostName the operator typed during install, while the
// Tailscale admin console renamed the node — `os.Hostname()`-derived
// hostname stopped resolving. tailscaleName from telemetry is the
// authoritative tailnet-resolvable name.
describe("edgePreviewURL", () => {
  it("uses tailscaleName when set, ignoring hostname", () => {
    const url = edgePreviewURL(
      {
        hostname: "07-eegees-mesa-macmini",
        tailscaleName: "07-eegees-store54-macmini.tailnet.ts.net",
      },
      "cam1",
    );
    expect(url).toBe(
      "http://07-eegees-store54-macmini.tailnet.ts.net:5051/preview/cam1",
    );
  });

  it("falls back to hostname when tailscaleName is null", () => {
    const url = edgePreviewURL(
      { hostname: "device-7", tailscaleName: null },
      "cam2",
    );
    expect(url).toBe("http://device-7:5051/preview/cam2");
  });

  it("falls back to hostname when tailscaleName is undefined (older API responses)", () => {
    const url = edgePreviewURL({ hostname: "device-7" }, "cam2");
    expect(url).toBe("http://device-7:5051/preview/cam2");
  });

  it("falls back to hostname when tailscaleName is the empty string", () => {
    const url = edgePreviewURL(
      { hostname: "device-7", tailscaleName: "" },
      "cam2",
    );
    expect(url).toBe("http://device-7:5051/preview/cam2");
  });
});
