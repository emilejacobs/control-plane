import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { StartRolloutPanel } from "./StartRolloutPanel";
import type { RolloutDevice } from "../lib/api/rollouts";

function device(over: Partial<RolloutDevice> & { id: string }): RolloutDevice {
  return {
    hostname: over.id,
    siteId: null,
    siteName: null,
    clientName: null,
    reportedVersion: "1.4.0",
    desiredVersion: null,
    isOnline: true,
    state: "untargeted",
    ...over,
  };
}

beforeEach(() => {
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  return () => clearTokens();
});

function stubCatalog() {
  // The panel builds its site dropdown from the `devices` prop (#64), so it no
  // longer fetches the /sites taxonomy tree — only the version catalog.
  server.use(
    http.get(`${API_BASE}/fleet/agent-versions`, () =>
      HttpResponse.json({ versions: ["1.4.1", "1.4.0"] }),
    ),
  );
}

describe("StartRolloutPanel", () => {
  it("defaults the version picker to the newest published version", async () => {
    stubCatalog();
    renderWithClient(<StartRolloutPanel />);

    // Wait for the catalog to load and populate the options.
    await screen.findByRole("option", { name: "1.4.1" });
    const select = screen.getByLabelText(/target version/i) as HTMLSelectElement;
    expect(select.value).toBe("1.4.1");
    // both catalog versions are offered
    expect(screen.getByRole("option", { name: "1.4.0" })).toBeInTheDocument();
  });

  it("starts a fleet-wide rollout: POSTs {version, all:true} and reports the result", async () => {
    stubCatalog();
    let body: Record<string, unknown> | null = null;
    server.use(
      http.post(`${API_BASE}/agent-rollouts`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json(
          { correlation_id: "corr-1", targeted: 5, pushed: 3 },
          { status: 202 },
        );
      }),
    );
    renderWithClient(<StartRolloutPanel />);

    await screen.findByLabelText(/target version/i);
    await userEvent.click(screen.getByRole("button", { name: /start rollout/i }));

    await waitFor(() => expect(body).not.toBeNull());
    expect(body).toEqual({ version: "1.4.1", all: true });
    expect(await screen.findByRole("status")).toHaveTextContent(/5/);
  });

  it("targets a site: the dropdown is built from device assignments and POSTs the device's site_id (#64)", async () => {
    stubCatalog();
    let body: Record<string, unknown> | null = null;
    server.use(
      http.post(`${API_BASE}/agent-rollouts`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ correlation_id: "c", targeted: 2, pushed: 2 }, { status: 202 });
      }),
    );
    const devices = [
      device({ id: "d1", siteId: "site-54", siteName: "Store 54", clientName: "Eegee's" }),
      device({ id: "d2", siteId: null }), // unassigned → contributes no option
    ];
    renderWithClient(<StartRolloutPanel devices={devices} />);

    await screen.findByLabelText(/target version/i);
    await userEvent.click(screen.getByRole("radio", { name: /specific site/i }));
    const siteSelect = await screen.findByLabelText(/^site$/i);
    // Only the assigned site is offered (placeholder + Store 54); the
    // unassigned device adds nothing.
    expect(within(siteSelect).getByRole("option", { name: /store 54/i })).toBeInTheDocument();
    expect(within(siteSelect).getAllByRole("option")).toHaveLength(2);

    await userEvent.selectOptions(siteSelect, "site-54");
    await userEvent.click(screen.getByRole("button", { name: /start rollout/i }));

    await waitFor(() => expect(body).not.toBeNull());
    // The id sent is the device's stored site_id, so it always matches ≥1 device.
    expect(body).toEqual({ version: "1.4.1", site_id: "site-54" });
  });

  it("dedups sites and lists each assigned site once across multiple devices (#64)", async () => {
    stubCatalog();
    const devices = [
      device({ id: "d1", siteId: "site-54", siteName: "Store 54", clientName: "Eegee's" }),
      device({ id: "d2", siteId: "site-54", siteName: "Store 54", clientName: "Eegee's" }),
      device({ id: "d3", siteId: "site-12", siteName: "Store 12", clientName: "Eegee's" }),
    ];
    renderWithClient(<StartRolloutPanel devices={devices} />);

    await screen.findByLabelText(/target version/i);
    await userEvent.click(screen.getByRole("radio", { name: /specific site/i }));
    const siteSelect = await screen.findByLabelText(/^site$/i);
    // placeholder + the two distinct sites
    expect(within(siteSelect).getAllByRole("option")).toHaveLength(3);
    expect(within(siteSelect).getByRole("option", { name: /store 54/i })).toBeInTheDocument();
    expect(within(siteSelect).getByRole("option", { name: /store 12/i })).toBeInTheDocument();
  });

  it("canary: targets the selected device subset and POSTs {version, device_ids}", async () => {
    stubCatalog();
    let body: Record<string, unknown> | null = null;
    const onStarted = vi.fn();
    server.use(
      http.post(`${API_BASE}/agent-rollouts`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ correlation_id: "c", targeted: 2, pushed: 2 }, { status: 202 });
      }),
    );
    renderWithClient(
      <StartRolloutPanel selectedDeviceIds={["d1", "d2"]} onStarted={onStarted} />,
    );

    await screen.findByLabelText(/target version/i);
    await userEvent.click(screen.getByRole("radio", { name: /selected devices \(2\)/i }));
    await userEvent.click(screen.getByRole("button", { name: /start rollout/i }));

    await waitFor(() => expect(body).not.toBeNull());
    expect(body).toEqual({ version: "1.4.1", device_ids: ["d1", "d2"] });
    await waitFor(() => expect(onStarted).toHaveBeenCalled());
  });

  it("canary: the Selected devices option is disabled when nothing is selected", async () => {
    stubCatalog();
    renderWithClient(<StartRolloutPanel selectedDeviceIds={[]} />);

    await screen.findByLabelText(/target version/i);
    expect(screen.getByRole("radio", { name: /selected devices \(0\)/i })).toBeDisabled();
  });

  it("surfaces the server's error message when the rollout is rejected", async () => {
    stubCatalog();
    server.use(
      http.post(`${API_BASE}/agent-rollouts`, () =>
        HttpResponse.json(
          { code: "agent_rollout.unknown_version", message: "no release manifest for version 1.4.1" },
          { status: 400 },
        ),
      ),
    );
    renderWithClient(<StartRolloutPanel />);

    await screen.findByLabelText(/target version/i);
    await userEvent.click(screen.getByRole("button", { name: /start rollout/i }));

    expect(await screen.findByRole("alert")).toHaveTextContent(/no release manifest/i);
  });
});
