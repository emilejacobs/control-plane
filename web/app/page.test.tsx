import { describe, it, expect, vi } from "vitest";
import { screen } from "@testing-library/react";
import Home from "./page";
import { renderWithClient } from "../test/render";

// The Home page now renders the Overview shell, which uses
// usePathname for the Topbar active state.
vi.mock("next/navigation", () => ({ usePathname: () => "/" }));

describe("dashboard scaffold", () => {
  it("renders the fleet overview heading on /", () => {
    renderWithClient(<Home />);
    expect(
      screen.getByRole("heading", { level: 1, name: /fleet overview/i }),
    ).toBeInTheDocument();
  });
});
