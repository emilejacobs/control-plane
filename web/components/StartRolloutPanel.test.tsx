import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { StartRolloutPanel } from "./StartRolloutPanel";

beforeEach(() => {
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  return () => clearTokens();
});

function stubCatalog() {
  server.use(
    http.get(`${API_BASE}/fleet/agent-versions`, () =>
      HttpResponse.json({ versions: ["1.4.1", "1.4.0"] }),
    ),
    http.get(`${API_BASE}/sites`, () =>
      HttpResponse.json({
        clients: [
          {
            id: "c1",
            external_id: "",
            name: "Acme",
            sites: [
              {
                id: "s1",
                external_id: "",
                name: "HQ",
                brand_name: "",
                brand_external_id: "",
                active: true,
              },
            ],
          },
        ],
      }),
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

  it("targets a site: switching to Specific site POSTs {version, site_id}", async () => {
    stubCatalog();
    let body: Record<string, unknown> | null = null;
    server.use(
      http.post(`${API_BASE}/agent-rollouts`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ correlation_id: "c", targeted: 2, pushed: 2 }, { status: 202 });
      }),
    );
    renderWithClient(<StartRolloutPanel />);

    await screen.findByLabelText(/target version/i);
    await userEvent.click(screen.getByRole("radio", { name: /specific site/i }));
    await userEvent.selectOptions(await screen.findByLabelText(/^site$/i), "s1");
    await userEvent.click(screen.getByRole("button", { name: /start rollout/i }));

    await waitFor(() => expect(body).not.toBeNull());
    expect(body).toEqual({ version: "1.4.1", site_id: "s1" });
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
