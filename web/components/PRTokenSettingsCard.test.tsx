import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { PRTokenSettingsCard } from "./PRTokenSettingsCard";

beforeEach(() => {
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  return () => clearTokens();
});

describe("PRTokenSettingsCard", () => {
  it("shows status and PUTs the token on save", async () => {
    let body: Record<string, unknown> | null = null;
    server.use(
      http.get(`${API_BASE}/settings/pr-token`, () => HttpResponse.json({ is_set: false })),
      http.put(`${API_BASE}/settings/pr-token`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({ is_set: true });
      }),
    );
    renderWithClient(<PRTokenSettingsCard />);

    expect(await screen.findByText("Not set")).toBeInTheDocument();
    await userEvent.type(screen.getByLabelText(/plate recognizer token/i), "tok-1");
    await userEvent.click(screen.getByRole("button", { name: /save/i }));
    await waitFor(() => expect(body).toEqual({ token: "tok-1" }));
  });

  it("hides for a non-staff operator (403)", async () => {
    server.use(
      http.get(`${API_BASE}/settings/pr-token`, () =>
        HttpResponse.json({ message: "staff only" }, { status: 403 }),
      ),
    );
    renderWithClient(<PRTokenSettingsCard />);
    await waitFor(() =>
      expect(screen.queryByText(/plate recognizer token/i)).not.toBeInTheDocument(),
    );
  });
});
