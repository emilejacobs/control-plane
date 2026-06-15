import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { RolloutActions } from "./RolloutActions";
import type { RolloutDevice } from "../lib/api/rollouts";

beforeEach(() => {
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  return () => clearTokens();
});

function dev(p: Partial<RolloutDevice> & { id: string }): RolloutDevice {
  return {
    id: p.id,
    hostname: p.hostname ?? p.id,
    siteName: p.siteName ?? null,
    clientName: p.clientName ?? null,
    reportedVersion: p.reportedVersion ?? "",
    desiredVersion: p.desiredVersion ?? null,
    isOnline: p.isOnline ?? true,
    state: p.state ?? "untargeted",
  };
}

const active: RolloutDevice[] = [
  dev({ id: "d1", reportedVersion: "1.4.0", desiredVersion: "1.4.1", state: "in_flight" }),
  dev({ id: "d2", reportedVersion: "1.4.1", desiredVersion: "1.4.1", state: "done" }),
  dev({ id: "d3", reportedVersion: "", desiredVersion: null, state: "untargeted" }),
];

describe("RolloutActions", () => {
  it("renders nothing when no device is targeted", () => {
    const { container } = renderWithClient(
      <RolloutActions devices={[dev({ id: "x", state: "untargeted" })]} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("promotes the active version to the entire fleet", async () => {
    let body: Record<string, unknown> | null = null;
    server.use(
      http.post(`${API_BASE}/agent-rollouts`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ correlation_id: "c", targeted: 9, pushed: 5 }, { status: 202 });
      }),
    );
    renderWithClient(<RolloutActions devices={active} />);

    await userEvent.click(screen.getByRole("button", { name: /promote 1\.4\.1 to entire fleet/i }));

    await waitFor(() => expect(body).not.toBeNull());
    expect(body).toEqual({ version: "1.4.1", all: true });
  });

  it("aborts: reverts each un-converged device to its reported version", async () => {
    let body: Record<string, unknown> | null = null;
    server.use(
      http.post(`${API_BASE}/agent-rollouts`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ correlation_id: "c", targeted: 1, pushed: 1 }, { status: 202 });
      }),
    );
    renderWithClient(<RolloutActions devices={active} />);

    await userEvent.click(screen.getByRole("button", { name: /revert .*un-converged/i }));

    await waitFor(() => expect(body).not.toBeNull());
    // d1 is the only in-flight device; reset its desired to its reported 1.4.0.
    expect(body).toEqual({ version: "1.4.0", device_ids: ["d1"] });
  });

  it("disables Promote when more than one version is in flight", () => {
    const mixed: RolloutDevice[] = [
      dev({ id: "a", reportedVersion: "1.3.0", desiredVersion: "1.4.0", state: "in_flight" }),
      dev({ id: "b", reportedVersion: "1.3.0", desiredVersion: "1.4.1", state: "in_flight" }),
    ];
    renderWithClient(<RolloutActions devices={mixed} />);
    expect(screen.getByRole("button", { name: /promote/i })).toBeDisabled();
  });
});
