import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { CommissionButton } from "./CommissionButton";

beforeEach(() => {
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  return () => clearTokens();
});

describe("CommissionButton", () => {
  it("is disabled until a site is assigned", () => {
    renderWithClient(<CommissionButton deviceId="dev-1" assigned={false} />);
    expect(screen.getByRole("button", { name: /commission/i })).toBeDisabled();
    expect(screen.getByText(/assign a site first/i)).toBeInTheDocument();
  });

  it("POSTs commission when assigned", async () => {
    let hit = false;
    server.use(
      http.post(`${API_BASE}/devices/dev-1/commission`, () => {
        hit = true;
        return HttpResponse.json({ correlation_id: "c1" }, { status: 202 });
      }),
    );
    renderWithClient(<CommissionButton deviceId="dev-1" assigned={true} />);
    await userEvent.click(screen.getByRole("button", { name: /commission/i }));
    await waitFor(() => expect(hit).toBe(true));
    expect(await screen.findByRole("status")).toBeInTheDocument();
  });

  it("surfaces a failure", async () => {
    server.use(
      http.post(`${API_BASE}/devices/dev-1/commission`, () =>
        HttpResponse.json({ message: "not assigned" }, { status: 409 }),
      ),
    );
    renderWithClient(<CommissionButton deviceId="dev-1" assigned={true} />);
    await userEvent.click(screen.getByRole("button", { name: /commission/i }));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
  });
});
