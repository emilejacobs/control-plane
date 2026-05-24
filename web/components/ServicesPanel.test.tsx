import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup, within } from "@testing-library/react";
import { ServicesPanel } from "./ServicesPanel";
import type { DeviceService } from "../lib/api/devices";

const now = new Date("2026-05-24T18:00:00Z");

function svc(overrides: Partial<DeviceService>): DeviceService {
  return {
    name: "com.uknomi.edge-ui",
    state: "running",
    stateSince: "2026-05-24T16:00:00Z", // 2h ago vs `now`
    lastReported: "2026-05-24T17:59:30Z",
    ...overrides,
  };
}

afterEach(cleanup);

describe("ServicesPanel — empty state", () => {
  it("shows the 'no report yet' placeholder when services is empty", () => {
    render(<ServicesPanel services={[]} now={now} />);
    expect(
      screen.getByText(/no service-status report yet/i),
    ).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });
});

describe("ServicesPanel — populated state", () => {
  it("renders one row per service with the right state pill", () => {
    render(
      <ServicesPanel
        services={[
          svc({ name: "com.uknomi.edge-ui", state: "running" }),
          svc({ name: "nginx", state: "stopped" }),
          svc({ name: "postfix", state: "unknown" }),
        ]}
        now={now}
      />,
    );

    const table = screen.getByRole("table");
    const rows = within(table).getAllByRole("row");
    // 1 header + 3 data rows.
    expect(rows).toHaveLength(4);

    expect(
      within(table).getByText("com.uknomi.edge-ui"),
    ).toBeInTheDocument();
    expect(within(table).getByText("nginx")).toBeInTheDocument();
    expect(within(table).getByText("postfix")).toBeInTheDocument();

    // The state labels appear as visible pill text. Lowercase per the
    // wire shape — matches the cp-api JSON contract.
    expect(within(table).getByText("running")).toBeInTheDocument();
    expect(within(table).getByText("stopped")).toBeInTheDocument();
    expect(within(table).getByText("unknown")).toBeInTheDocument();
  });

  it("renders the relative 'since' time from stateSince, not lastReported", () => {
    // stateSince is 2h ago; lastReported is 30s ago. The dashboard's
    // contract is "running since N hours" (how long the state has held),
    // not "last seen N seconds ago" (that's elsewhere on the page).
    render(
      <ServicesPanel
        services={[svc({ name: "nginx", state: "running" })]}
        now={now}
      />,
    );
    // formatAgo's output for 2h includes "hour" in the string. Loose
    // match to stay resilient to wording tweaks.
    const sinceCell = screen.getByRole("row", { name: /nginx/i });
    expect(within(sinceCell).getByText(/hour/i)).toBeInTheDocument();
  });
});
