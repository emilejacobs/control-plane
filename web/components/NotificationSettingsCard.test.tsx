import { describe, it, expect, beforeEach } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { renderWithClient } from "../test/render";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { NotificationSettingsCard } from "./NotificationSettingsCard";

beforeEach(() => {
  setTokens({ accessToken: "test-access", refreshToken: "test-refresh" });
  return () => clearTokens();
});

describe("NotificationSettingsCard", () => {
  it("renders current config: enabled, recipients, and webhook configured", async () => {
    server.use(
      http.get(`${API_BASE}/settings/notifications`, () =>
        HttpResponse.json({
          enabled: true,
          email_recipients: ["ops@example.com"],
          teams_webhook_set: true,
          teams_webhook_preview: "default…powerplatform.com",
        }),
      ),
    );
    renderWithClient(<NotificationSettingsCard />);

    // The recipient list is rendered into the editable field.
    const recipients = await screen.findByLabelText(/email recipients/i);
    expect(recipients).toHaveValue("ops@example.com");
    // The webhook is a secret — its value is never shown, only that it is set.
    expect(screen.getByText(/configured/i)).toBeInTheDocument();
    // The enable switch reflects the stored state.
    expect(screen.getByRole("checkbox", { name: /enable notifications/i })).toBeChecked();
  });

  it("PUTs enabled + recipient list on save", async () => {
    let body: Record<string, unknown> | null = null;
    server.use(
      http.get(`${API_BASE}/settings/notifications`, () =>
        HttpResponse.json({
          enabled: true,
          email_recipients: ["ops@example.com"],
          teams_webhook_set: true,
          teams_webhook_preview: "default…powerplatform.com",
        }),
      ),
      http.put(`${API_BASE}/settings/notifications`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({});
      }),
    );
    renderWithClient(<NotificationSettingsCard />);

    const recipients = await screen.findByLabelText(/email recipients/i);
    await userEvent.clear(recipients);
    await userEvent.type(recipients, "a@x.com\nb@y.com");
    await userEvent.click(screen.getByRole("checkbox", { name: /enable notifications/i }));
    await userEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() =>
      expect(body).toEqual({ enabled: false, email_recipients: ["a@x.com", "b@y.com"] }),
    );
  });
});
