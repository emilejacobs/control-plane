import { describe, it, expect, afterEach, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

// scan builds a NetworkScan row; tests override the fields they exercise.
function scan(overrides: Partial<NetworkScan> = {}): NetworkScan {
  return {
    correlationId: "corr-1",
    cidr: null,
    status: "pending",
    hosts: null,
    errorCode: null,
    errorMessage: null,
    requestedAt: "2026-05-26T16:00:00Z",
    returnedAt: null,
    ...overrides,
  };
}

// renderModal mounts the modal with stubbed callbacks unless overridden, so
// each test only states the props it cares about.
function renderModal(
  props: Partial<React.ComponentProps<typeof NetworkScanModal>> = {},
) {
  const onClose = vi.fn();
  const onAddCamera = vi.fn();
  const onRescan = vi.fn();
  render(
    <NetworkScanModal
      scan={props.scan ?? scan()}
      onClose={props.onClose ?? onClose}
      onAddCamera={props.onAddCamera ?? onAddCamera}
      onRescan={props.onRescan ?? onRescan}
    />,
  );
  return { onClose, onAddCamera, onRescan, ...props };
}

describe("NetworkScanModal — initial pending state", () => {
  it("shows a 'Scanning…' indicator while the result is pending", () => {
    renderModal({ scan: scan({ status: "pending" }) });
    expect(screen.getByText(/scanning/i)).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });

  it("treats a null scan (kicked off, no poll yet) as pending", () => {
    renderModal({ scan: null });
    expect(screen.getByText(/scanning/i)).toBeInTheDocument();
    expect(screen.queryByRole("table")).not.toBeInTheDocument();
  });
});

describe("NetworkScanModal — done state", () => {
  it("renders one row per discovered host with ip / vendor / ports", () => {
    renderModal({
      scan: scan({
        status: "done",
        cidr: "192.168.1.0/24",
        hosts: [
          host({ ip: "192.168.1.10", vendor: "Hikvision", openPorts: [80, 554] }),
          host({ ip: "192.168.1.42", vendor: "Dahua", openPorts: [443] }),
        ],
        returnedAt: "2026-05-26T16:00:04Z",
      }),
    });
    const table = screen.getByRole("table");
    const rows = within(table).getAllByRole("row");
    expect(rows).toHaveLength(3); // header + 2 rows
    expect(within(table).getByText("192.168.1.10")).toBeInTheDocument();
    expect(within(table).getByText("192.168.1.42")).toBeInTheDocument();
    expect(within(table).getByText("Hikvision")).toBeInTheDocument();
    expect(within(table).getByText("Dahua")).toBeInTheDocument();
  });

  it("shows an empty-result placeholder when no hosts came back", () => {
    renderModal({ scan: scan({ status: "done", hosts: [], returnedAt: "2026-05-26T16:00:04Z" }) });
    expect(screen.getByText(/no candidate cameras/i)).toBeInTheDocument();
  });

  it("fires onAddCamera with the row's IP when the row's 'Add as camera' button is clicked", () => {
    const onAddCamera = vi.fn();
    renderModal({
      onAddCamera,
      scan: scan({ status: "done", hosts: [host({ ip: "192.168.1.99", vendor: "Hikvision" })], returnedAt: "2026-05-26T16:00:04Z" }),
    });
    fireEvent.click(screen.getByRole("button", { name: /add as camera/i }));
    expect(onAddCamera).toHaveBeenCalledWith("192.168.1.99");
  });
});

describe("NetworkScanModal — error state", () => {
  it("shows the agent's error message", () => {
    renderModal({
      scan: scan({
        status: "error",
        errorCode: "network_scan.scan_failed",
        errorMessage: "nmap: command not found",
        returnedAt: "2026-05-26T16:00:04Z",
      }),
    });
    expect(screen.getByText(/nmap: command not found/)).toBeInTheDocument();
    expect(screen.getByText(/network_scan\.scan_failed/)).toBeInTheDocument();
  });
});

describe("NetworkScanModal — CIDR override (#12)", () => {
  it("re-scans in auto-detect mode (no cidr) when the CIDR field is left empty", async () => {
    const onRescan = vi.fn();
    renderModal({ onRescan, scan: scan({ status: "done", hosts: [], returnedAt: "2026-05-26T16:00:04Z" }) });

    await userEvent.click(screen.getByRole("button", { name: /re-scan/i }));
    expect(onRescan).toHaveBeenCalledWith(undefined);
  });

  it("passes a valid CIDR through to onRescan", async () => {
    const onRescan = vi.fn();
    renderModal({ onRescan, scan: scan({ status: "done", hosts: [], returnedAt: "2026-05-26T16:00:04Z" }) });

    await userEvent.type(screen.getByLabelText(/subnet|cidr/i), "10.0.5.0/24");
    await userEvent.click(screen.getByRole("button", { name: /re-scan/i }));
    expect(onRescan).toHaveBeenCalledWith("10.0.5.0/24");
  });

  it("rejects an invalid CIDR: shows a hint and does not re-scan", async () => {
    const onRescan = vi.fn();
    renderModal({ onRescan, scan: scan({ status: "done", hosts: [], returnedAt: "2026-05-26T16:00:04Z" }) });

    await userEvent.type(screen.getByLabelText(/subnet|cidr/i), "999.1/77");
    expect(screen.getByText(/valid cidr/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /re-scan/i })).toBeDisabled();
    await userEvent.click(screen.getByRole("button", { name: /re-scan/i }));
    expect(onRescan).not.toHaveBeenCalled();
  });

  it("disables re-scan while a scan is still pending", () => {
    renderModal({ scan: scan({ status: "pending" }) });
    expect(screen.getByRole("button", { name: /re-scan/i })).toBeDisabled();
  });
});

describe("NetworkScanModal — close", () => {
  it("calls onClose when the close button is clicked", async () => {
    const onClose = vi.fn();
    renderModal({ onClose, scan: scan({ status: "done", hosts: [], returnedAt: "2026-05-26T16:00:04Z" }) });
    fireEvent.click(screen.getByRole("button", { name: /close/i }));
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });
});
