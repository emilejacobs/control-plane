import { describe, it, expect, afterEach, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  within,
} from "@testing-library/react";
import { CamerasPanel } from "./CamerasPanel";
import type { Camera } from "../lib/api/devices";

function cam(overrides: Partial<Camera> = {}): Camera {
  return {
    cameraId: "cam1",
    label: "Drive-thru",
    rtspUrl: "rtsp://user:pass@10.0.0.42:554/stream",
    isLpr: false,
    status: "online",
    lastCheckedAt: "2026-05-26T11:59:30Z",
    statusChangedAt: null,
    ...overrides,
  };
}

afterEach(cleanup);

describe("CamerasPanel — empty state", () => {
  it("shows a placeholder when no cameras are configured", () => {
    render(<CamerasPanel cameras={[]} lastAppliedAt={null} />);
    expect(screen.getByText(/no cameras configured/i)).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });
});

describe("CamerasPanel — populated state", () => {
  it("renders one row per camera with label + LPR flag visible", () => {
    render(
      <CamerasPanel
        cameras={[
          cam({ cameraId: "cam1", label: "Drive-thru", isLpr: true }),
          cam({ cameraId: "cam2", label: "Entry", isLpr: false }),
        ]}
        lastAppliedAt="2026-05-26T12:00:00Z"
      />,
    );
    const table = screen.getByRole("table");
    const rows = within(table).getAllByRole("row");
    // 1 header + 2 data rows.
    expect(rows).toHaveLength(3);

    expect(within(table).getByText("Drive-thru")).toBeInTheDocument();
    expect(within(table).getByText("Entry")).toBeInTheDocument();
    // The LPR flag renders as a visible "LPR" pill on the lpr row.
    // The non-lpr row has no pill — scoping by row is the clean
    // assertion against the column-header "LPR" text.
    const lprRow = within(table).getByRole("row", { name: /Drive-thru/ });
    expect(within(lprRow).getByText("LPR")).toBeInTheDocument();
    const nonLprRow = within(table).getByRole("row", { name: /Entry/ });
    expect(within(nonLprRow).queryByText("LPR")).not.toBeInTheDocument();
  });

  it("renders the RTSP URL (operator copies these when troubleshooting)", () => {
    render(
      <CamerasPanel
        cameras={[cam({ rtspUrl: "rtsp://user:pass@10.0.0.42/stream" })]}
        lastAppliedAt="2026-05-26T12:00:00Z"
      />,
    );
    // The URL appears somewhere in the table (may be truncated for
    // long URLs, but the substring must be present).
    expect(
      screen.getByText(/rtsp:\/\/user:pass@10\.0\.0\.42\/stream/),
    ).toBeInTheDocument();
  });
});

// Camera observability (#115): each row carries a per-camera status
// badge (online/offline/unknown) plus a last-checked ago-string. The
// unknown state must render distinctly and never invent a timestamp.
describe("CamerasPanel — per-camera status badge (issue #115)", () => {
  // Fixed clock so the ago-string is deterministic. cam()'s default
  // lastCheckedAt is 30s before this instant.
  const now = new Date("2026-05-26T12:00:00Z");

  it("renders an Online badge + last-checked ago-string for an online camera", () => {
    render(
      <CamerasPanel
        cameras={[cam({ label: "Drive-thru", status: "online" })]}
        lastAppliedAt="2026-05-26T12:00:00Z"
        now={now}
      />,
    );
    const table = screen.getByRole("table");
    const row = within(table).getByRole("row", { name: /Drive-thru/ });
    expect(within(row).getByText("Online")).toBeInTheDocument();
    expect(within(row).getByText(/30 seconds ago/)).toBeInTheDocument();
  });

  it("renders an Offline badge for an offline camera", () => {
    render(
      <CamerasPanel
        cameras={[cam({ label: "Drive-thru", status: "offline" })]}
        lastAppliedAt="2026-05-26T12:00:00Z"
        now={now}
      />,
    );
    const table = screen.getByRole("table");
    const row = within(table).getByRole("row", { name: /Drive-thru/ });
    const badge = within(row).getByText("Offline");
    expect(badge).toBeInTheDocument();
    // red tone — Pill renders the tone as a class.
    expect(badge).toHaveClass("red");
  });

  it("renders an Unknown badge with no bogus timestamp when status is unknown / never probed", () => {
    render(
      <CamerasPanel
        cameras={[
          cam({ label: "Drive-thru", status: "unknown", lastCheckedAt: null }),
        ]}
        lastAppliedAt="2026-05-26T12:00:00Z"
        now={now}
      />,
    );
    const table = screen.getByRole("table");
    const row = within(table).getByRole("row", { name: /Drive-thru/ });
    const badge = within(row).getByText("Unknown");
    expect(badge).toBeInTheDocument();
    expect(badge).toHaveClass("neutral");
    // No invented time: not an ago-string, not an "Invalid Date".
    expect(within(row).queryByText(/ago/)).not.toBeInTheDocument();
    expect(within(row).queryByText(/Invalid Date/)).not.toBeInTheDocument();
    expect(within(row).getByText(/never checked/i)).toBeInTheDocument();
  });
});

describe("CamerasPanel — Scan network button (issue #3)", () => {
  it("renders the 'Scan network' button when onScanNetwork is provided", () => {
    render(
      <CamerasPanel
        cameras={[]}
        lastAppliedAt={null}
        onScanNetwork={vi.fn()}
      />,
    );
    expect(
      screen.getByRole("button", { name: /scan network/i }),
    ).toBeInTheDocument();
  });

  it("fires onScanNetwork when the button is clicked", () => {
    const onScanNetwork = vi.fn();
    render(
      <CamerasPanel
        cameras={[]}
        lastAppliedAt={null}
        onScanNetwork={onScanNetwork}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /scan network/i }));
    expect(onScanNetwork).toHaveBeenCalledTimes(1);
  });

  it("does not render the button when onScanNetwork is undefined", () => {
    render(<CamerasPanel cameras={[]} lastAppliedAt={null} />);
    expect(
      screen.queryByRole("button", { name: /scan network/i }),
    ).not.toBeInTheDocument();
  });
});

describe("CamerasPanel — Verify angle button (issue #4)", () => {
  it("renders a 'Verify angle' button for each camera row when onVerifyAngle is provided", () => {
    render(
      <CamerasPanel
        cameras={[
          cam({ cameraId: "cam1", label: "Drive-thru" }),
          cam({ cameraId: "cam2", label: "Entry" }),
        ]}
        lastAppliedAt="2026-05-26T12:00:00Z"
        onVerifyAngle={vi.fn()}
      />,
    );
    const buttons = screen.getAllByRole("button", { name: /verify angle/i });
    expect(buttons).toHaveLength(2);
  });

  it("fires onVerifyAngle with the row's camera when clicked", () => {
    const onVerifyAngle = vi.fn();
    const c1 = cam({ cameraId: "cam1", label: "Drive-thru" });
    render(
      <CamerasPanel
        cameras={[c1]}
        lastAppliedAt="2026-05-26T12:00:00Z"
        onVerifyAngle={onVerifyAngle}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /verify angle/i }));
    expect(onVerifyAngle).toHaveBeenCalledTimes(1);
    expect(onVerifyAngle).toHaveBeenCalledWith(c1);
  });

  it("renders the Edge UI URL as the button's title attribute when previewURL is provided", () => {
    const previewURL = (c: Camera) => `http://device-7.tail.example:5051/preview/${c.cameraId}`;
    render(
      <CamerasPanel
        cameras={[cam({ cameraId: "cam1" })]}
        lastAppliedAt="2026-05-26T12:00:00Z"
        onVerifyAngle={vi.fn()}
        previewURL={previewURL}
      />,
    );
    const btn = screen.getByRole("button", { name: /verify angle/i });
    expect(btn.getAttribute("title")).toBe(
      "http://device-7.tail.example:5051/preview/cam1",
    );
  });

  it("does not render the button when onVerifyAngle is undefined", () => {
    render(
      <CamerasPanel
        cameras={[cam()]}
        lastAppliedAt="2026-05-26T12:00:00Z"
      />,
    );
    expect(
      screen.queryByRole("button", { name: /verify angle/i }),
    ).not.toBeInTheDocument();
  });
});

// Issue #14: when device.lan_ip is set, the panel exposes a
// secondary "Copy LAN URL" affordance per camera so the operator
// can fall back to the device's LAN IP when the tailnet is
// unreachable. The affordance is hidden when lan_ip is null —
// pre-rollout devices keep the current single-button shape.
describe("CamerasPanel — Copy LAN URL affordance (issue #14)", () => {
  it("renders a 'Copy LAN URL' button per camera when lanURL is provided", () => {
    const lanURL = (c: Camera) => `http://192.168.54.215:5051/preview/${c.cameraId}`;
    render(
      <CamerasPanel
        cameras={[
          cam({ cameraId: "cam1", label: "Drive-thru" }),
          cam({ cameraId: "cam2", label: "Entry" }),
        ]}
        lastAppliedAt="2026-05-26T12:00:00Z"
        onVerifyAngle={vi.fn()}
        lanURL={lanURL}
      />,
    );
    const buttons = screen.getAllByRole("button", { name: /copy lan url/i });
    expect(buttons).toHaveLength(2);
  });

  it("does NOT render the affordance when lanURL is undefined", () => {
    render(
      <CamerasPanel
        cameras={[cam()]}
        lastAppliedAt="2026-05-26T12:00:00Z"
        onVerifyAngle={vi.fn()}
      />,
    );
    expect(
      screen.queryByRole("button", { name: /copy lan url/i }),
    ).not.toBeInTheDocument();
  });

  it("renders the LAN URL as the button's title attribute so the operator can hover-inspect it", () => {
    const lanURL = (c: Camera) =>
      `http://192.168.54.215:5051/preview/${c.cameraId}`;
    render(
      <CamerasPanel
        cameras={[cam({ cameraId: "cam1" })]}
        lastAppliedAt="2026-05-26T12:00:00Z"
        onVerifyAngle={vi.fn()}
        lanURL={lanURL}
      />,
    );
    const btn = screen.getByRole("button", { name: /copy lan url/i });
    expect(btn.getAttribute("title")).toBe(
      "http://192.168.54.215:5051/preview/cam1",
    );
  });

  it("still renders Verify angle alongside the Copy LAN URL button", () => {
    const lanURL = (c: Camera) =>
      `http://192.168.54.215:5051/preview/${c.cameraId}`;
    render(
      <CamerasPanel
        cameras={[cam({ cameraId: "cam1", label: "Drive-thru" })]}
        lastAppliedAt="2026-05-26T12:00:00Z"
        onVerifyAngle={vi.fn()}
        previewURL={(c) => `http://device.ts.net:5051/preview/${c.cameraId}`}
        lanURL={lanURL}
      />,
    );
    expect(
      screen.getByRole("button", { name: /verify angle/i }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /copy lan url/i }),
    ).toBeInTheDocument();
  });
});

describe("CamerasPanel — pending badge", () => {
  it("shows a 'pending' badge when lastAppliedAt is null", () => {
    render(
      <CamerasPanel cameras={[cam()]} lastAppliedAt={null} />,
    );
    expect(screen.getByText(/pending/i)).toBeInTheDocument();
  });

  it("does NOT show 'pending' when lastAppliedAt is set", () => {
    render(
      <CamerasPanel
        cameras={[cam()]}
        lastAppliedAt="2026-05-26T12:00:00Z"
      />,
    );
    expect(screen.queryByText(/pending/i)).not.toBeInTheDocument();
  });
});
