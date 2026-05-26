import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { server } from "../test/server";
import { API_BASE, setTokens, clearTokens } from "../lib/api/client";
import { LogsPanel } from "./LogsPanel";

afterEach(() => {
  cleanup();
  clearTokens();
});

describe("LogsPanel", () => {
  it("renders the log picker seeded to the first allow-list entry", () => {
    render(<LogsPanel deviceId="dev-1" />);
    const select = screen.getByLabelText("Log name") as HTMLSelectElement;
    expect(select.value).toBe("agent");
    const lines = screen.getByLabelText("Lines to fetch") as HTMLInputElement;
    expect(lines.value).toBe("200");
  });

  // Issue #7 / ADR-030 § 5: the docker-kind entry for the Plate
  // Recognizer container appears in the picker with its operator-
  // facing label, alongside the seven file entries. Selecting it
  // POSTs name='plate-recognizer' — wire format unchanged from the
  // file branch.
  it("picker includes the Plate Recognizer (Docker) entry", () => {
    render(<LogsPanel deviceId="dev-1" />);
    const select = screen.getByLabelText("Log name") as HTMLSelectElement;
    const options = Array.from(select.options).map((o) => ({
      value: o.value,
      label: o.label,
    }));
    expect(options).toContainEqual({
      value: "plate-recognizer",
      label: "Plate Recognizer (Docker)",
    });
  });

  it("selecting plate-recognizer fetches docker logs and renders them", async () => {
    setTokens({ accessToken: "t", refreshToken: "r" });
    const user = userEvent.setup();

    server.use(
      http.post(`${API_BASE}/devices/dev-1/logs/tail`, async ({ request }) => {
        const body = (await request.json()) as { log_name: string; lines: number };
        // Wire format pinned: the docker entry uses the same name+lines
        // payload as file entries — only the agent's resolver branches.
        expect(body.log_name).toBe("plate-recognizer");
        expect(body.lines).toBe(200);
        return HttpResponse.json(
          { correlation_id: "corr-pr-1" },
          { status: 202 },
        );
      }),
      http.get(`${API_BASE}/devices/dev-1/logs/tail/corr-pr-1`, () =>
        HttpResponse.json({
          correlation_id: "corr-pr-1",
          log_name: "plate-recognizer",
          lines_requested: 200,
          status: "done",
          content:
            "[plate-recognizer-stream] starting up\n[plate-recognizer-stream] webhook delivered\n",
          truncated: false,
          truncated_from: null,
          error_code: null,
          error_message: null,
          requested_at: "2026-05-26T10:00:00Z",
          returned_at: "2026-05-26T10:00:02Z",
        }),
      ),
    );

    render(<LogsPanel deviceId="dev-1" />);
    await user.selectOptions(
      screen.getByLabelText("Log name"),
      "plate-recognizer",
    );
    await user.click(screen.getByRole("button", { name: /fetch/i }));

    await waitFor(
      () => {
        expect(screen.getByText(/plate-recognizer-stream/)).toBeInTheDocument();
      },
      { timeout: 5000 },
    );
    expect(screen.getByText(/webhook delivered/)).toBeInTheDocument();
  });

  it("happy path: Fetch → POST 202 + poll done → content rendered", async () => {
    setTokens({ accessToken: "t", refreshToken: "r" });
    const user = userEvent.setup();

    server.use(
      http.post(`${API_BASE}/devices/dev-1/logs/tail`, () =>
        HttpResponse.json({ correlation_id: "corr-tail-1" }, { status: 202 }),
      ),
      http.get(`${API_BASE}/devices/dev-1/logs/tail/corr-tail-1`, () =>
        HttpResponse.json({
          correlation_id: "corr-tail-1",
          log_name: "agent",
          lines_requested: 200,
          status: "done",
          content: "first line\nsecond line\nthird line\n",
          truncated: false,
          truncated_from: null,
          error_code: null,
          error_message: null,
          requested_at: "2026-05-24T22:00:00Z",
          returned_at: "2026-05-24T22:00:02Z",
        }),
      ),
    );

    render(<LogsPanel deviceId="dev-1" />);
    await user.click(screen.getByRole("button", { name: /fetch/i }));

    // First tick fires at 2s — generous timeout in test env.
    await waitFor(
      () => {
        expect(screen.getByText(/first line/)).toBeInTheDocument();
      },
      { timeout: 5000 },
    );
    expect(screen.getByText(/second line/)).toBeInTheDocument();
    expect(screen.getByText(/third line/)).toBeInTheDocument();
  });

  it("error path: agent returns an error code → alert with code rendered", async () => {
    setTokens({ accessToken: "t", refreshToken: "r" });
    const user = userEvent.setup();

    server.use(
      http.post(`${API_BASE}/devices/dev-1/logs/tail`, () =>
        HttpResponse.json({ correlation_id: "corr-err" }, { status: 202 }),
      ),
      http.get(`${API_BASE}/devices/dev-1/logs/tail/corr-err`, () =>
        HttpResponse.json({
          correlation_id: "corr-err",
          log_name: "agent",
          lines_requested: 200,
          status: "error",
          content: null,
          truncated: false,
          truncated_from: null,
          error_code: "log_tail.binary_file",
          error_message: "looks binary",
          requested_at: "2026-05-24T22:00:00Z",
          returned_at: "2026-05-24T22:00:02Z",
        }),
      ),
    );

    render(<LogsPanel deviceId="dev-1" />);
    await user.click(screen.getByRole("button", { name: /fetch/i }));

    await waitFor(
      () => {
        expect(screen.getByRole("alert").textContent).toMatch(/binary_file/);
      },
      { timeout: 5000 },
    );
  });

  it("400 from POST surfaces as an alert without polling", async () => {
    setTokens({ accessToken: "t", refreshToken: "r" });
    const user = userEvent.setup();

    server.use(
      http.post(`${API_BASE}/devices/dev-1/logs/tail`, () =>
        HttpResponse.json(
          { code: "log_tail.bad_lines", message: "lines must be 1..500" },
          { status: 400 },
        ),
      ),
    );

    render(<LogsPanel deviceId="dev-1" />);
    await user.click(screen.getByRole("button", { name: /fetch/i }));

    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toMatch(/bad_lines|lines must/);
  });

  it("truncated response shows the truncation banner", async () => {
    setTokens({ accessToken: "t", refreshToken: "r" });
    const user = userEvent.setup();

    server.use(
      http.post(`${API_BASE}/devices/dev-1/logs/tail`, () =>
        HttpResponse.json({ correlation_id: "corr-trunc" }, { status: 202 }),
      ),
      http.get(`${API_BASE}/devices/dev-1/logs/tail/corr-trunc`, () =>
        HttpResponse.json({
          correlation_id: "corr-trunc",
          log_name: "install",
          lines_requested: 500,
          status: "done",
          content: "...last bytes of the install log...",
          truncated: true,
          truncated_from: 500,
          error_code: null,
          error_message: null,
          requested_at: "2026-05-24T22:00:00Z",
          returned_at: "2026-05-24T22:00:02Z",
        }),
      ),
    );

    render(<LogsPanel deviceId="dev-1" />);
    await user.click(screen.getByRole("button", { name: /fetch/i }));

    await waitFor(
      () => {
        expect(screen.getByText(/Truncated to fit/i)).toBeInTheDocument();
      },
      { timeout: 5000 },
    );
    expect(screen.getByText(/requested 500 lines/i)).toBeInTheDocument();
  });
});
