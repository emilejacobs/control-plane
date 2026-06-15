import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { CameraSnapshot } from "./CameraSnapshot";

beforeEach(() => {
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  return () => clearTokens();
});

function captureRow(over: { id: string; cameraId: string; createdAt: string }) {
  return {
    id: over.id,
    kind: "snapshot",
    content_type: "image/jpeg",
    size_bytes: 1024,
    metadata: { camera_id: over.cameraId },
    created_at: over.createdAt,
  };
}

describe("CameraSnapshot", () => {
  it("renders the latest snapshot thumbnail for the matching camera", async () => {
    server.use(
      // Newest-first, as cp-api returns. cam1's newest is snap-2.
      http.get(`${API_BASE}/devices/dev-1/captures`, () =>
        HttpResponse.json({
          captures: [
            captureRow({ id: "snap-2", cameraId: "cam1", createdAt: "2026-06-15T10:00:00Z" }),
            captureRow({ id: "snap-1", cameraId: "cam1", createdAt: "2026-06-15T09:00:00Z" }),
            captureRow({ id: "other", cameraId: "cam2", createdAt: "2026-06-15T11:00:00Z" }),
          ],
        }),
      ),
      http.get(`${API_BASE}/captures/snap-2/url`, () =>
        HttpResponse.json({ url: "https://s3.example/signed/snap-2.jpg" }),
      ),
    );
    renderWithClient(<CameraSnapshot deviceId="dev-1" cameraId="cam1" />);

    const img = (await screen.findByRole("img", { name: /latest snapshot for cam1/i })) as HTMLImageElement;
    expect(img.src).toBe("https://s3.example/signed/snap-2.jpg");
  });

  it("clicking the thumbnail opens a full-size lightbox; Escape closes it", async () => {
    server.use(
      http.get(`${API_BASE}/devices/dev-1/captures`, () =>
        HttpResponse.json({
          captures: [captureRow({ id: "snap-2", cameraId: "cam1", createdAt: "2026-06-15T10:00:00Z" })],
        }),
      ),
      http.get(`${API_BASE}/captures/snap-2/url`, () =>
        HttpResponse.json({ url: "https://s3.example/signed/snap-2.jpg" }),
      ),
    );
    renderWithClient(<CameraSnapshot deviceId="dev-1" cameraId="cam1" />);

    await screen.findByRole("img", { name: /latest snapshot for cam1/i });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /enlarge snapshot for cam1/i }));

    const dialog = await screen.findByRole("dialog", { name: /snapshot for cam1/i });
    const full = within(dialog).getByRole("img", { name: /full size/i }) as HTMLImageElement;
    expect(full.src).toBe("https://s3.example/signed/snap-2.jpg");

    await userEvent.keyboard("{Escape}");
    await waitFor(() => expect(screen.queryByRole("dialog")).not.toBeInTheDocument());
  });

  it("shows a placeholder when the camera has no snapshot", async () => {
    server.use(
      http.get(`${API_BASE}/devices/dev-1/captures`, () =>
        HttpResponse.json({
          captures: [captureRow({ id: "other", cameraId: "cam2", createdAt: "2026-06-15T11:00:00Z" })],
        }),
      ),
    );
    renderWithClient(<CameraSnapshot deviceId="dev-1" cameraId="cam1" />);

    expect(await screen.findByText(/no snapshot yet/i)).toBeInTheDocument();
    expect(screen.queryByRole("img")).not.toBeInTheDocument();
  });

  it("Refresh snapshot posts a snapshot request for the camera", async () => {
    let body: Record<string, unknown> | null = null;
    server.use(
      http.get(`${API_BASE}/devices/dev-1/captures`, () => HttpResponse.json({ captures: [] })),
      http.post(`${API_BASE}/devices/dev-1/snapshot`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ correlation_id: "corr-1", s3_key: "snapshots/dev-1/x.jpg" }, { status: 202 });
      }),
    );
    renderWithClient(<CameraSnapshot deviceId="dev-1" cameraId="cam1" />);

    await screen.findByText(/no snapshot yet/i);
    await userEvent.click(screen.getByRole("button", { name: /refresh snapshot for cam1/i }));

    await waitFor(() => expect(body).toEqual({ camera_id: "cam1" }));
  });

  it("surfaces a snapshot request failure inline", async () => {
    server.use(
      http.get(`${API_BASE}/devices/dev-1/captures`, () => HttpResponse.json({ captures: [] })),
      http.post(`${API_BASE}/devices/dev-1/snapshot`, () =>
        HttpResponse.json({ message: "device offline" }, { status: 502 }),
      ),
    );
    renderWithClient(<CameraSnapshot deviceId="dev-1" cameraId="cam1" />);

    await screen.findByText(/no snapshot yet/i);
    await userEvent.click(screen.getByRole("button", { name: /refresh snapshot for cam1/i }));

    expect(await screen.findByRole("alert")).toBeInTheDocument();
  });
});
