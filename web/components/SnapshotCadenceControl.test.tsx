import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { SnapshotCadenceControl } from "./SnapshotCadenceControl";

beforeEach(() => {
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  return () => clearTokens();
});

describe("SnapshotCadenceControl", () => {
  it("reflects the current cadence", () => {
    renderWithClient(<SnapshotCadenceControl deviceId="dev-1" cadence="daily" />);
    const select = screen.getByLabelText(/scheduled snapshot cadence/i) as HTMLSelectElement;
    expect(select.value).toBe("daily");
  });

  it("PUTs the chosen cadence on change", async () => {
    let body: Record<string, unknown> | null = null;
    server.use(
      http.put(`${API_BASE}/devices/dev-1/snapshot-config`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ cadence: "off" });
      }),
    );
    renderWithClient(<SnapshotCadenceControl deviceId="dev-1" cadence="weekly" />);

    await userEvent.selectOptions(screen.getByLabelText(/scheduled snapshot cadence/i), "off");
    await waitFor(() => expect(body).toEqual({ cadence: "off" }));
  });

  it("surfaces a save failure", async () => {
    server.use(
      http.put(`${API_BASE}/devices/dev-1/snapshot-config`, () =>
        HttpResponse.json({ message: "nope" }, { status: 500 }),
      ),
    );
    renderWithClient(<SnapshotCadenceControl deviceId="dev-1" cadence="weekly" />);

    await userEvent.selectOptions(screen.getByLabelText(/scheduled snapshot cadence/i), "daily");
    expect(await screen.findByRole("alert")).toBeInTheDocument();
  });
});
