import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup, within } from "@testing-library/react";
import { HealthPanel } from "./HealthPanel";
import type { HealthProbe } from "../lib/api/devices";

const now = new Date("2026-05-28T12:00:00Z");

function probe(overrides: Partial<HealthProbe>): HealthProbe {
  return {
    name: "auto_login",
    status: "green",
    state: "configured",
    details: {},
    lastObservedAt: "2026-05-28T10:00:00Z", // 2h ago vs `now`
    ...overrides,
  };
}

afterEach(cleanup);

describe("HealthPanel — empty state", () => {
  it("shows the 'no report yet' placeholder when probes is empty", () => {
    render(<HealthPanel probes={[]} now={now} />);
    expect(screen.getByText(/no health-probe report yet/i)).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });
});

describe("HealthPanel — populated state", () => {
  it("renders one row per probe with the state label as pill text", () => {
    render(
      <HealthPanel
        probes={[
          probe({ name: "auto_login", status: "red", state: "missing" }),
          probe({ name: "gui_session", status: "red", state: "login_window", details: { console_user: "root" } }),
          probe({ name: "usb_audio", status: "green", state: "detected" }),
        ]}
        now={now}
      />,
    );

    const table = screen.getByRole("table");
    const rows = within(table).getAllByRole("row");
    expect(rows).toHaveLength(4); // 1 header + 3 data rows

    expect(within(table).getByText("auto_login")).toBeInTheDocument();
    // "missing" shows as the pill and (for probes with no richer detail)
    // again in the detail column, so there can be more than one.
    expect(within(table).getAllByText("missing").length).toBeGreaterThan(0);
    expect(within(table).getByText("login_window")).toBeInTheDocument();
    // gui_session detail surfaces the console user.
    expect(within(table).getByText(/console: root/)).toBeInTheDocument();
  });

  it("surfaces ephemeral-port % and CLOSE_WAIT for host_net_pressure", () => {
    render(
      <HealthPanel
        probes={[
          probe({
            name: "host_net_pressure",
            status: "red",
            state: "critical",
            details: { ephemeral_pct: 82.4, close_wait: 6 },
          }),
        ]}
        now={now}
      />,
    );
    expect(screen.getByText(/ephemeral ports: 82\.4% of pool/i)).toBeInTheDocument();
    expect(screen.getByText(/close_wait: 6/i)).toBeInTheDocument();
  });

  it("surfaces the whisper variant and size in the detail column", () => {
    render(
      <HealthPanel
        probes={[
          probe({
            name: "whisper_model",
            status: "green",
            state: "present",
            details: { variant: "medium.en", size_mb: 539 },
          }),
        ]}
        now={now}
      />,
    );
    expect(screen.getByText(/medium\.en \(539 MB\)/)).toBeInTheDocument();
  });
});
