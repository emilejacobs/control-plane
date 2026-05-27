import { describe, it, expect, afterEach, vi } from "vitest";
import {
  render,
  screen,
  cleanup,
  fireEvent,
  waitFor,
  within,
} from "@testing-library/react";
import { EditDeploymentModal } from "./EditDeploymentModal";
import type { ClientWithSites } from "../lib/api/taxonomy";

afterEach(cleanup);

const sampleTree: ClientWithSites[] = [
  {
    id: "client-a-uuid",
    externalId: "14",
    name: "Client #14",
    sites: [
      {
        id: "site-bk-uuid",
        externalId: "60",
        name: "BK Mesa",
        brandName: "Burger King",
        brandExternalId: "12",
        active: true,
      },
      {
        id: "site-dd-uuid",
        externalId: "50",
        name: "DD09",
        brandName: "Dunkin Donuts",
        brandExternalId: "13",
        active: true,
      },
    ],
  },
  {
    id: "client-b-uuid",
    externalId: "2",
    name: "Client #2",
    sites: [
      {
        id: "site-eg-uuid",
        externalId: "54",
        name: "Eegee's Store 54",
        brandName: "Eegee's",
        brandExternalId: "2",
        active: true,
      },
    ],
  },
];

describe("EditDeploymentModal — pre-population", () => {
  it("pre-selects the device's current site_id and asset_number", () => {
    render(
      <EditDeploymentModal
        tree={sampleTree}
        currentSiteId="site-dd-uuid"
        currentAssetNumber="UK-MAC-007"
        saving={false}
        onSubmit={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    const select = screen.getByLabelText(/Site/i) as HTMLSelectElement;
    expect(select.value).toBe("site-dd-uuid");
    const asset = screen.getByLabelText(/Asset number/i) as HTMLInputElement;
    expect(asset.value).toBe("UK-MAC-007");
  });

  it("renders an 'Unassigned' option that maps to null", () => {
    render(
      <EditDeploymentModal
        tree={sampleTree}
        currentSiteId={null}
        currentAssetNumber={null}
        saving={false}
        onSubmit={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    const select = screen.getByLabelText(/Site/i) as HTMLSelectElement;
    expect(select.value).toBe(""); // empty string represents Unassigned
    expect(screen.getByRole("option", { name: /Unassigned/i })).toBeInTheDocument();
  });

  it("groups sites under their client (optgroup) for the picker", () => {
    render(
      <EditDeploymentModal
        tree={sampleTree}
        currentSiteId={null}
        currentAssetNumber={null}
        saving={false}
        onSubmit={vi.fn()}
        onClose={vi.fn()}
      />,
    );
    const select = screen.getByLabelText(/Site/i) as HTMLSelectElement;
    const optgroups = select.querySelectorAll("optgroup");
    expect(optgroups).toHaveLength(2);
    expect(optgroups[0].label).toMatch(/Client #14/);
    expect(optgroups[1].label).toMatch(/Client #2/);
    // Each site option shows name + brand + external_id.
    expect(within(select).getByRole("option", { name: /DD09.*Dunkin Donuts.*50/i })).toBeInTheDocument();
  });
});

describe("EditDeploymentModal — save", () => {
  it("invokes onSubmit with the picked site and asset number", async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(
      <EditDeploymentModal
        tree={sampleTree}
        currentSiteId={null}
        currentAssetNumber={null}
        saving={false}
        onSubmit={onSubmit}
        onClose={vi.fn()}
      />,
    );
    const select = screen.getByLabelText(/Site/i) as HTMLSelectElement;
    fireEvent.change(select, { target: { value: "site-bk-uuid" } });
    const asset = screen.getByLabelText(/Asset number/i) as HTMLInputElement;
    fireEvent.change(asset, { target: { value: "UK-MAC-042" } });
    fireEvent.click(screen.getByRole("button", { name: /Save/i }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith({
        siteId: "site-bk-uuid",
        assetNumber: "UK-MAC-042",
      }),
    );
  });

  it("passes null when site is Unassigned and asset is blank", async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(
      <EditDeploymentModal
        tree={sampleTree}
        currentSiteId="site-dd-uuid"
        currentAssetNumber="UK-OLD"
        saving={false}
        onSubmit={onSubmit}
        onClose={vi.fn()}
      />,
    );
    const select = screen.getByLabelText(/Site/i) as HTMLSelectElement;
    fireEvent.change(select, { target: { value: "" } });
    const asset = screen.getByLabelText(/Asset number/i) as HTMLInputElement;
    fireEvent.change(asset, { target: { value: "" } });
    fireEvent.click(screen.getByRole("button", { name: /Save/i }));
    await waitFor(() =>
      expect(onSubmit).toHaveBeenCalledWith({
        siteId: null,
        assetNumber: null,
      }),
    );
  });
});

describe("EditDeploymentModal — cancel + error", () => {
  it("Cancel invokes onClose without firing onSubmit", () => {
    const onSubmit = vi.fn();
    const onClose = vi.fn();
    render(
      <EditDeploymentModal
        tree={sampleTree}
        currentSiteId={null}
        currentAssetNumber={null}
        saving={false}
        onSubmit={onSubmit}
        onClose={onClose}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /Cancel/i }));
    expect(onClose).toHaveBeenCalled();
    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("surfaces mutation errors inline rather than throwing", async () => {
    const onSubmit = vi.fn().mockRejectedValue(new Error("staff only"));
    render(
      <EditDeploymentModal
        tree={sampleTree}
        currentSiteId={null}
        currentAssetNumber={null}
        saving={false}
        onSubmit={onSubmit}
        onClose={vi.fn()}
      />,
    );
    fireEvent.click(screen.getByRole("button", { name: /Save/i }));
    await waitFor(() =>
      expect(screen.getByText(/staff only/i)).toBeInTheDocument(),
    );
  });
});
