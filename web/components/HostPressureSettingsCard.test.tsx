import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { HostPressureSettingsCard } from "./HostPressureSettingsCard";

beforeEach(() => {
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  return () => clearTokens();
});

describe("HostPressureSettingsCard", () => {
  it("seeds the inputs from the loaded thresholds", async () => {
    server.use(
      http.get(`${API_BASE}/settings/host-pressure`, () =>
        HttpResponse.json({
          ephemeral_warn_pct: 40,
          ephemeral_crit_pct: 60,
          close_wait_warn: 100,
          close_wait_crit: 400,
        }),
      ),
    );
    renderWithClient(<HostPressureSettingsCard />);
    expect(await screen.findByLabelText(/ephemeral pool critical percent/i)).toHaveValue(60);
    expect(screen.getByLabelText(/CLOSE_WAIT critical/i)).toHaveValue(400);
  });

  it("PUTs edited thresholds on save", async () => {
    let body: Record<string, unknown> | null = null;
    server.use(
      http.get(`${API_BASE}/settings/host-pressure`, () =>
        HttpResponse.json({
          ephemeral_warn_pct: 40,
          ephemeral_crit_pct: 60,
          close_wait_warn: 100,
          close_wait_crit: 400,
        }),
      ),
      http.put(`${API_BASE}/settings/host-pressure`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({});
      }),
    );
    renderWithClient(<HostPressureSettingsCard />);

    const crit = await screen.findByLabelText(/ephemeral pool critical percent/i);
    await userEvent.clear(crit);
    await userEvent.type(crit, "50");
    await userEvent.click(screen.getByRole("button", { name: /save thresholds/i }));

    await waitFor(() => expect(body).not.toBeNull());
    expect(body).toMatchObject({ ephemeral_crit_pct: 50, ephemeral_warn_pct: 40 });
  });
});
