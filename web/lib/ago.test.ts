import { describe, it, expect } from "vitest";
import { formatAgo } from "./ago";

// agoOf formats a point `seconds` before a fixed "now".
function agoOf(seconds: number): string {
  const now = new Date("2026-05-22T12:00:00Z");
  return formatAgo(new Date(now.getTime() - seconds * 1000), now);
}

describe("formatAgo", () => {
  it("renders sub-minute gaps in seconds", () => {
    expect(agoOf(12)).toBe("12 seconds ago");
  });

  it("renders sub-hour gaps in whole minutes", () => {
    expect(agoOf(125)).toBe("2 minutes ago");
  });

  it("renders sub-day gaps in whole hours", () => {
    expect(agoOf(2 * 3600)).toBe("2 hours ago");
  });

  it("renders longer gaps in whole days", () => {
    expect(agoOf(50 * 3600)).toBe("2 days ago");
  });

  it("uses the singular unit at exactly one", () => {
    expect(agoOf(1)).toBe("1 second ago");
    expect(agoOf(60)).toBe("1 minute ago");
    expect(agoOf(3600)).toBe("1 hour ago");
  });

  it("clamps a future instant to zero", () => {
    expect(agoOf(-30)).toBe("0 seconds ago");
  });
});
