import { describe, it, expect } from "vitest";
import { screen } from "@testing-library/react";
import { http, HttpResponse } from "msw";
import { server } from "../../test/server";
import { renderWithClient } from "../../test/render";
import { API_BASE } from "../../lib/api/client";
import DevicesPage from "./page";

describe("devices page", () => {
  it("renders the Devices shell with an empty state", async () => {
    server.use(
      http.get(`${API_BASE}/devices`, () => HttpResponse.json({ devices: [] })),
    );

    renderWithClient(<DevicesPage />);

    expect(screen.getByRole("heading", { name: /devices/i })).toBeInTheDocument();
    expect(await screen.findByText(/no devices yet/i)).toBeInTheDocument();
  });
});
