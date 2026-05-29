import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor, fireEvent, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";
import { OperatorsManager } from "./OperatorsManager";
import * as operatorsApi from "../lib/api/operators";
import * as taxonomy from "../lib/api/taxonomy";
import { ApiError } from "../lib/api/auth";

function renderWithClient(ui: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const op = (over: Partial<operatorsApi.Operator> = {}): operatorsApi.Operator => ({
  id: "op-1",
  email: "a@x.test",
  isStaff: true,
  totpEnrolled: true,
  deactivated: false,
  siteIds: [],
  ...over,
});

describe("OperatorsManager", () => {
  it("lists operators with role and state", async () => {
    vi.spyOn(operatorsApi, "getOperators").mockResolvedValue([
      op({ email: "admin@x.test", isStaff: true }),
      op({ id: "op-2", email: "field@x.test", isStaff: false, totpEnrolled: false, deactivated: true, siteIds: ["s1"] }),
    ]);
    vi.spyOn(taxonomy, "getSitesTree").mockResolvedValue([]);

    renderWithClient(<OperatorsManager />);

    expect(await screen.findByText("admin@x.test")).toBeInTheDocument();
    expect(screen.getByText("field@x.test")).toBeInTheDocument();
    expect(screen.getByText("Deactivated")).toBeInTheDocument();
    expect(screen.getByText("Scoped")).toBeInTheDocument();
  });

  it("shows a staff-only notice on 403", async () => {
    vi.spyOn(operatorsApi, "getOperators").mockRejectedValue(new ApiError(403, "forbidden"));
    vi.spyOn(taxonomy, "getSitesTree").mockResolvedValue([]);

    renderWithClient(<OperatorsManager />);

    expect(await screen.findByText(/need staff access/i)).toBeInTheDocument();
  });

  it("reveals the one-time temp password after creating an operator", async () => {
    vi.spyOn(operatorsApi, "getOperators").mockResolvedValue([op()]);
    vi.spyOn(taxonomy, "getSitesTree").mockResolvedValue([]);
    const create = vi.spyOn(operatorsApi, "createOperator").mockResolvedValue({
      operator: op({ id: "op-9", email: "new@x.test", isStaff: true }),
      tempPassword: "the-temp-pw",
    });

    renderWithClient(<OperatorsManager />);
    fireEvent.click(await screen.findByRole("button", { name: /add operator/i }));

    const dialog = await screen.findByRole("dialog", { name: /add operator/i });
    fireEvent.change(within(dialog).getByLabelText(/email/i), { target: { value: "new@x.test" } });
    fireEvent.click(within(dialog).getByRole("button", { name: /create operator/i }));

    await waitFor(() => expect(create).toHaveBeenCalledTimes(1));
    expect(await screen.findByText("the-temp-pw")).toBeInTheDocument();
  });
});
