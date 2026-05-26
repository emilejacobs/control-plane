import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup, within } from "@testing-library/react";
import { CamerasPanel } from "./CamerasPanel";
import type { Camera } from "../lib/api/devices";

function cam(overrides: Partial<Camera> = {}): Camera {
  return {
    cameraId: "cam1",
    label: "Drive-thru",
    rtspUrl: "rtsp://user:pass@10.0.0.42:554/stream",
    isLpr: false,
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
