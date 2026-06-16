import { describe, it, expect, beforeEach, vi } from "vitest";
import { screen, within, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { SnapshotHistoryModal } from "./SnapshotHistoryModal";

beforeEach(() => {
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  return () => clearTokens();
});

function row(id: string, cameraId: string, createdAt: string) {
  return {
    id,
    kind: "snapshot",
    content_type: "image/jpeg",
    size_bytes: 1024,
    metadata: { camera_id: cameraId },
    created_at: createdAt,
  };
}

function stubCaptures(captures: ReturnType<typeof row>[]) {
  server.use(
    http.get(`${API_BASE}/devices/dev-1/captures`, () => HttpResponse.json({ captures })),
    http.get(`${API_BASE}/captures/:id/url`, ({ params }) =>
      HttpResponse.json({ url: `https://s3.example/signed/${params.id}.jpg` }),
    ),
  );
}

describe("SnapshotHistoryModal", () => {
  it("lists this camera's snapshots newest-first and excludes other cameras", async () => {
    stubCaptures([
      row("a", "cam1", "2026-06-15T10:00:00Z"),
      row("b", "cam1", "2026-06-14T10:00:00Z"),
      row("other", "cam2", "2026-06-15T11:00:00Z"),
    ]);
    renderWithClient(<SnapshotHistoryModal deviceId="dev-1" cameraId="cam1" onClose={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: /snapshot history for cam1/i });
    // Two cam1 thumbnails, not cam2's.
    await waitFor(() => expect(within(dialog).getAllByRole("img").length).toBe(2));
  });

  it("paginates when there are more than a page of snapshots", async () => {
    const many = Array.from({ length: 10 }, (_, i) =>
      row(`s${i}`, "cam1", `2026-06-${String(10 + i).padStart(2, "0")}T10:00:00Z`),
    );
    stubCaptures(many);
    renderWithClient(<SnapshotHistoryModal deviceId="dev-1" cameraId="cam1" onClose={vi.fn()} />);

    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByText(/page 1 of 2/i)).toBeInTheDocument();
    await userEvent.click(within(dialog).getByRole("button", { name: /next/i }));
    expect(within(dialog).getByText(/page 2 of 2/i)).toBeInTheDocument();
  });

  it("clicking a row opens the full-size lightbox", async () => {
    stubCaptures([row("a", "cam1", "2026-06-15T10:00:00Z")]);
    renderWithClient(<SnapshotHistoryModal deviceId="dev-1" cameraId="cam1" onClose={vi.fn()} />);

    const dialog = await screen.findByRole("dialog", { name: /snapshot history for cam1/i });
    await userEvent.click(await within(dialog).findByRole("button", { name: /view full-size snapshot/i }));

    const lightbox = await screen.findByRole("dialog", { name: /full size/i });
    const full = within(lightbox).getByRole("img", { name: /full size/i }) as HTMLImageElement;
    expect(full.src).toBe("https://s3.example/signed/a.jpg");
  });

  it("shows an empty state when the camera has no snapshots", async () => {
    stubCaptures([row("other", "cam2", "2026-06-15T11:00:00Z")]);
    renderWithClient(<SnapshotHistoryModal deviceId="dev-1" cameraId="cam1" onClose={vi.fn()} />);
    expect(await screen.findByText(/no snapshots yet for this camera/i)).toBeInTheDocument();
  });

  it("Close button fires onClose", async () => {
    const onClose = vi.fn();
    stubCaptures([]);
    renderWithClient(<SnapshotHistoryModal deviceId="dev-1" cameraId="cam1" onClose={onClose} />);
    await userEvent.click(await screen.findByRole("button", { name: /^close$/i }));
    expect(onClose).toHaveBeenCalled();
  });
});
