import { describe, it, expect, afterEach, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor,
  within,
} from "@testing-library/react";
import { NetworkScanModal } from "./NetworkScanModal";
import type { NetworkScan } from "../lib/api/devices";

afterEach(cleanup);

function host(overrides: Partial<NetworkScan["hosts"] extends (infer T)[] | null ? T : never> = {}) {
  return {
    ip: "192.168.1.42",
    mac: "44:19:b6:aa:bb:cc",
    vendor: "Hikvision",
    openPorts: [80, 554],
    ...overrides,
  };
}

describe("NetworkScanModal — initial pending state", () => {
  it("shows a 'Scanning…' indicator while the result is pending", () => {
    render(
      <NetworkScanModal
        scan={{
          correlationId: "corr-1",
          cidr: null,
          status: "pending",
          hosts: null,
          errorCode: null,
          errorMessage: null,
          requestedAt: "2026-05-26T16:00:00Z",
          returnedAt: null,
        }}
        onClose={vi.fn()}
        onAddCamera={vi.fn()}
      />,
    );
    expect(screen.getByText(/scanning/i)).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });
});

describe("NetworkScanModal — done state", () => {
  it("renders one row per discovered host with ip / vendor / ports", () => {
    render(
      <NetworkScanModal
        scan={{
          correlationId: "corr-1",
          cidr: "192.168.1.0/24",
          status: "done",
          hosts: [
            host({ ip: "192.168.1.10", vendor: "Hikvision", openPorts: [80, 554] }),
            host({ ip: "192.168.1.42", vendor: "Dahua", openPorts: [443] }),
          ],
          errorCode: null,
          errorMessage: null,
          requestedAt: "2026-05-26T16:00:00Z",
          returnedAt: "2026-05-26T16:00:04Z",
        }}
        onClose={vi.fn()}
        onAddCamera={vi.fn()}
      />,
    );
    const table = screen.getByRole("table");
    const rows = within(table).getAllByRole("row");
    expect(rows).toHaveLength(3); // header + 2 rows
    expect(within(table).getByText("192.168.1.10")).toBeInTheDocument();
    expect(within(table).getByText("192.168.1.42")).toBeInTheDocument();
    expect(within(table).getByText("Hikvision")).toBeInTheDocument();
    expect(within(table).getByText("Dahua")).toBeInTheDocument();
  });

  it("shows an empty-result placeholder when no hosts came back", () => {
    render(
      <NetworkScanModal
        scan={{
          correlationId: "corr-1",
          cidr: null,
          status: "done",
          hosts: [],
          errorCode: null,
          errorMessage: null,
          requestedAt: "2026-05-26T16:00:00Z",
          returnedAt: "2026-05-26T16:00:04Z",
        }}
        onClose={vi.fn()}
        onAddCamera={vi.fn()}
      />,
    );
    expect(screen.getByText(/no candidate cameras/i)).toBeInTheDocument();
  });

  it("fires onAddCamera with the row's IP when the row's 'Add as camera' button is clicked", () => {
    const onAddCamera = vi.fn();
    render(
      <NetworkScanModal
        scan={{
          correlationId: "corr-1",
          cidr: null,
          status: "done",
          hosts: [host({ ip: "192.168.1.99", vendor: "Hikvision" })],
          errorCode: null,
          errorMessage: null,
          requestedAt: "2026-05-26T16:00:00Z",
          returnedAt: "2026-05-26T16:00:04Z",
        }}
        onClose={vi.fn()}
        onAddCamera={onAddCamera}
      />,
    );
    const btn = screen.getByRole("button", { name: /add as camera/i });
    fireEvent.click(btn);
    expect(onAddCamera).toHaveBeenCalledWith("192.168.1.99");
  });
});

describe("NetworkScanModal — error state", () => {
  it("shows the agent's error message", () => {
    render(
      <NetworkScanModal
        scan={{
          correlationId: "corr-1",
          cidr: null,
          status: "error",
          hosts: null,
          errorCode: "network_scan.scan_failed",
          errorMessage: "nmap: command not found",
          requestedAt: "2026-05-26T16:00:00Z",
          returnedAt: "2026-05-26T16:00:04Z",
        }}
        onClose={vi.fn()}
        onAddCamera={vi.fn()}
      />,
    );
    expect(screen.getByText(/nmap: command not found/)).toBeInTheDocument();
    expect(screen.getByText(/network_scan\.scan_failed/)).toBeInTheDocument();
  });
});

describe("NetworkScanModal — close", () => {
  it("calls onClose when the close button is clicked", async () => {
    const onClose = vi.fn();
    render(
      <NetworkScanModal
        scan={{
          correlationId: "corr-1",
          cidr: null,
          status: "done",
          hosts: [],
          errorCode: null,
          errorMessage: null,
          requestedAt: "2026-05-26T16:00:00Z",
          returnedAt: "2026-05-26T16:00:04Z",
        }}
        onClose={onClose}
        onAddCamera={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /close/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });
});
