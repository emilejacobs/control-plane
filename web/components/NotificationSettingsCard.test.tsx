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
      expect(body).toEqual({
        enabled: false,
        email_recipients: ["a@x.com", "b@y.com"],
        offline_grace_seconds: 180,
      }),
    );
  });

  it("seeds and PUTs the offline alert delay (debounce window)", async () => {
    let body: Record<string, unknown> | null = null;
    server.use(
      http.get(`${API_BASE}/settings/notifications`, () =>
        HttpResponse.json({
          enabled: true,
          email_recipients: [],
          offline_grace_seconds: 120,
          teams_webhook_set: false,
          teams_webhook_preview: "",
        }),
      ),
      http.put(`${API_BASE}/settings/notifications`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({});
      }),
    );
    renderWithClient(<NotificationSettingsCard />);

    const grace = await screen.findByLabelText(/offline alert delay/i);
    expect(grace).toHaveValue(120); // seeded from the loaded config
    await userEvent.clear(grace);
    await userEvent.type(grace, "300");
    await userEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(body?.offline_grace_seconds).toBe(300));
  });

  it("PUTs the Teams webhook to its write-only endpoint and never shows the value", async () => {
    let body: Record<string, unknown> | null = null;
    server.use(
      http.get(`${API_BASE}/settings/notifications`, () =>
        HttpResponse.json({
          enabled: true,
          email_recipients: [],
          teams_webhook_set: true,
          teams_webhook_preview: "default…powerplatform.com",
        }),
      ),
      http.put(`${API_BASE}/settings/notifications/teams-webhook`, async ({ request }) => {
        body = (await request.json()) as Record<string, unknown>;
        return HttpResponse.json({});
      }),
    );
    renderWithClient(<NotificationSettingsCard />);

    const webhookInput = await screen.findByLabelText(/teams webhook url/i);
    // Secret input — masked, and the stored value is not pre-filled.
    expect(webhookInput).toHaveAttribute("type", "password");
    expect(webhookInput).toHaveValue("");

    await userEvent.type(webhookInput, "https://new.example/hook?sig=abc");
    await userEvent.click(screen.getByRole("button", { name: /save webhook/i }));

    await waitFor(() =>
      expect(body).toEqual({ webhook_url: "https://new.example/hook?sig=abc" }),
    );
  });

  it("hides for a non-staff operator (403)", async () => {
    server.use(
      http.get(`${API_BASE}/settings/notifications`, () =>
        HttpResponse.json({ message: "staff only" }, { status: 403 }),
      ),
    );
    renderWithClient(<NotificationSettingsCard />);
    await waitFor(() =>
      expect(screen.queryByText(/^notifications$/i)).not.toBeInTheDocument(),
    );
  });
});
