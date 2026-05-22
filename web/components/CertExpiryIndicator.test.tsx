import { describe, it, expect } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { CertExpiryIndicator } from "./CertExpiryIndicator";

// statusFor renders the indicator for a given days-remaining and reads back
// the color band it landed in.
function statusFor(daysRemaining: number): string | null {
  cleanup();
  render(
    <CertExpiryIndicator
      expiresAt="2027-01-15T12:00:00Z"
      daysRemaining={daysRemaining}
    />,
  );
  return screen
    .getByText(/certificate expires/i)
    .getAttribute("data-cert-status");
}

// Thresholds per Issue 09: green > 180, yellow 30–180, red < 30.
describe("CertExpiryIndicator color coding", () => {
  it("flags a cert with under 30 days remaining as red", () => {
    expect(statusFor(10)).toBe("red");
  });

  it("flags a cert with 30 to 180 days remaining as yellow", () => {
    expect(statusFor(90)).toBe("yellow");
  });

  it("flags a cert with over 180 days remaining as green", () => {
    expect(statusFor(365)).toBe("green");
  });

  it("keeps the threshold days in the safer band", () => {
    expect(statusFor(30)).toBe("yellow");
    expect(statusFor(180)).toBe("yellow");
  });

  it("flags an already-expired cert as red", () => {
    expect(statusFor(-5)).toBe("red");
  });
});
