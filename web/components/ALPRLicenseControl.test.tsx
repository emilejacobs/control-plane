import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { ALPRLicenseControl } from "./ALPRLicenseControl";

beforeEach(() => {
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  return () => clearTokens();
});

describe("ALPRLicenseControl", () => {
  it("shows the set/not-set state", () => {
    renderWithClient(<ALPRLicenseControl deviceId="dev-1" licenseSet={true} />);
    expect(screen.getByText("Set")).toBeInTheDocument();
  });

  it("PUTs the entered license on save", async () => {
    let body: Record<string, unknown> | null = null;
    server.use(
      http.put(`${API_BASE}/devices/dev-1/alpr-license`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return new HttpResponse(null, { status: 200 });
      }),
    );
    renderWithClient(<ALPRLicenseControl deviceId="dev-1" licenseSet={false} />);
    expect(screen.getByText("Not set")).toBeInTheDocument();

    await userEvent.type(screen.getByLabelText(/alpr license/i), "LIC-123");
    await userEvent.click(screen.getByRole("button", { name: /save/i }));
    await waitFor(() => expect(body).toEqual({ license: "LIC-123" }));
  });

  it("surfaces a save failure", async () => {
    server.use(
      http.put(`${API_BASE}/devices/dev-1/alpr-license`, () =>
        HttpResponse.json({ message: "nope" }, { status: 500 }),
      ),
    );
    renderWithClient(<ALPRLicenseControl deviceId="dev-1" licenseSet={false} />);
    await userEvent.type(screen.getByLabelText(/alpr license/i), "X");
    await userEvent.click(screen.getByRole("button", { name: /save/i }));
    expect(await screen.findByRole("alert")).toBeInTheDocument();
  });
});
