import { describe, it, expect, afterEach, vi } from "vitest";
import {
  getOperators,
  createOperator,
  updateOperator,
  deactivateOperator,
} from "./operators";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe("operators API client", () => {
  it("maps the list envelope to camelCase", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({
          operators: [
            { id: "op-1", email: "a@x.test", is_staff: true, totp_enrolled: true, deactivated: false, site_ids: [] },
          ],
        }),
      ),
    );
    const ops = await getOperators();
    expect(ops).toEqual([
      { id: "op-1", email: "a@x.test", isStaff: true, totpEnrolled: true, deactivated: false, siteIds: [] },
    ]);
  });

  it("create returns the operator and one-time temp password", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      jsonResponse(
        {
          operator: { id: "op-9", email: "new@x.test", is_staff: false, totp_enrolled: false, deactivated: false, site_ids: ["s1"] },
          temp_password: "generated-temp",
        },
        201,
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const res = await createOperator({ email: "new@x.test", isStaff: false, siteIds: ["s1"] });
    expect(res.tempPassword).toBe("generated-temp");
    expect(res.operator.id).toBe("op-9");
    // Body is sent snake_case.
    const sent = JSON.parse((fetchMock.mock.calls[0][1] as RequestInit).body as string);
    expect(sent).toEqual({ email: "new@x.test", is_staff: false, site_ids: ["s1"] });
  });

  it("update surfaces a temp password only when one is returned", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({
          operator: { id: "op-3", email: "e@x.test", is_staff: true, totp_enrolled: false, deactivated: false, site_ids: [] },
          temp_password: "fresh",
        }),
      ),
    );
    const res = await updateOperator("op-3", { isStaff: true, resetPassword: true });
    expect(res.tempPassword).toBe("fresh");
    expect(res.operator.isStaff).toBe(true);
  });

  it("deactivate rejects on a non-2xx response", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("nope", { status: 500 })));
    await expect(deactivateOperator("op-4")).rejects.toThrow();
  });
});
