import { describe, it, expect, afterEach, vi, beforeEach } from "vitest";
import {
  render,
  screen,
  cleanup,
  waitFor,
  fireEvent,
} from "@testing-library/react";
import { TaxonomySettingsCard } from "./TaxonomySettingsCard";
import * as taxonomy from "../lib/api/taxonomy";
import { ApiError } from "../lib/api/auth";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe("TaxonomySettingsCard — staff path", () => {
  beforeEach(() => {
    vi.spyOn(taxonomy, "getTaxonomyStatus").mockResolvedValue({
      clientsTotal: 3,
      clientsActive: 3,
      sitesTotal: 87,
      sitesActive: 85,
      lastSyncedAt: "2026-05-26T08:00:00Z",
    });
  });

  it("renders the card with counts and a Force sync button", async () => {
    render(<TaxonomySettingsCard />);
    await waitFor(() =>
      expect(screen.getByText(/Clients & Sites/i)).toBeInTheDocument(),
    );
    // Counts are visible (3 clients, 87 sites, 85 active).
    expect(screen.getByText(/3 clients/i)).toBeInTheDocument();
    expect(screen.getByText(/87 sites \(85 active\)/i)).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: /Force sync now/i }),
    ).toBeInTheDocument();
  });

  it("clicking Force sync now triggers the sync and surfaces the started message", async () => {
    const trigger = vi
      .spyOn(taxonomy, "triggerTaxonomySync")
      .mockResolvedValue({ taskArn: "arn:aws:ecs:us-east-1:0:task/abc" });

    render(<TaxonomySettingsCard />);
    const btn = await screen.findByRole("button", { name: /Force sync now/i });
    fireEvent.click(btn);

    await waitFor(() => expect(trigger).toHaveBeenCalledTimes(1));
    expect(
      await screen.findByText(/Sync started — refresh in ~1 minute/i),
    ).toBeInTheDocument();
  });
});

describe("TaxonomySettingsCard — empty state", () => {
  it("renders 'Never' when last_synced_at is null", async () => {
    vi.spyOn(taxonomy, "getTaxonomyStatus").mockResolvedValue({
      clientsTotal: 0,
      clientsActive: 0,
      sitesTotal: 0,
      sitesActive: 0,
      lastSyncedAt: null,
    });
    render(<TaxonomySettingsCard />);
    await waitFor(() =>
      expect(screen.getByText(/Last successful sync/i)).toBeInTheDocument(),
    );
    expect(screen.getByText(/Never/i)).toBeInTheDocument();
  });
});

describe("TaxonomySettingsCard — non-staff path", () => {
  it("renders nothing when cp-api returns 403", async () => {
    vi.spyOn(taxonomy, "getTaxonomyStatus").mockRejectedValue(
      new ApiError(403, "staff only"),
    );
    const { container } = render(<TaxonomySettingsCard />);
    // Wait a tick for the failed fetch to resolve; the card hides itself.
    await waitFor(() => expect(container.firstChild).toBeNull());
  });
});
