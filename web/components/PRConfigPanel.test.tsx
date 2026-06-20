import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { PRConfigPanel } from "./PRConfigPanel";

const DEV = "11111111-2222-3333-4444-555555555555";

beforeEach(() => {
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  return () => clearTokens();
});

describe("PRConfigPanel", () => {
  it("loads the config and PUTs edits on save", async () => {
    let body: Record<string, unknown> | null = null;
    server.use(
      http.get(`${API_BASE}/devices/${DEV}/pr-config`, () =>
        HttpResponse.json({
          camera_id: "66_3",
          region: "us-az",
          webhooks: [{ name: "prod", url: "https://api.uknomi.com/x", enabled: true, image: false, caching: false }],
          lpr_camera_rtsp_url: "rtsp://cam/lpr",
          last_applied_at: "2026-06-20T00:00:00Z",
        }),
      ),
      http.put(`${API_BASE}/devices/${DEV}/pr-config`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({
          camera_id: "66_3",
          region: "us-ca",
          webhooks: body!.webhooks,
          lpr_camera_rtsp_url: "rtsp://cam/lpr",
          last_applied_at: null,
        });
      }),
    );

    renderWithClient(<PRConfigPanel deviceId={DEV} />);

    // Form seeds from the fetched config (read-only LPR url shown).
    expect(await screen.findByText("rtsp://cam/lpr")).toBeInTheDocument();
    const region = screen.getByLabelText("Region") as HTMLSelectElement;
    await waitFor(() => expect(region.value).toBe("us-az"));

    // Edit region (dropdown) + a webhook flag, then save.
    await userEvent.selectOptions(region, "us-ca");
    await userEvent.click(screen.getByLabelText("webhook 0 image"));
    await userEvent.click(screen.getByRole("button", { name: /save/i }));

    await waitFor(() => expect(body).not.toBeNull());
    expect(body!.camera_id).toBe("66_3");
    expect(body!.region).toBe("us-ca");
    expect(body!.webhooks).toEqual([
      { name: "prod", url: "https://api.uknomi.com/x", enabled: true, image: true, caching: false },
    ]);
  });

  it("shows an error when the config fails to load", async () => {
    server.use(
      http.get(`${API_BASE}/devices/${DEV}/pr-config`, () =>
        HttpResponse.json({ message: "boom" }, { status: 500 }),
      ),
    );
    renderWithClient(<PRConfigPanel deviceId={DEV} />);
    expect(await screen.findByRole("alert")).toHaveTextContent(/failed to load/i);
  });
});
