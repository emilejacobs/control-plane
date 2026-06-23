import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { Gauge, gaugeTone } from "./Gauge";

describe("Gauge", () => {
  it("renders the rounded percentage, count, label and accessible label", () => {
    render(<Gauge value={60} max={63} label="Devices online" sub="3 offline" tone="success" />);
    expect(screen.getByText("95%")).toBeInTheDocument();
    expect(screen.getByText("60 / 63")).toBeInTheDocument();
    expect(screen.getByText("Devices online")).toBeInTheDocument();
    expect(screen.getByText("3 offline")).toBeInTheDocument();
    expect(screen.getByRole("img")).toHaveAttribute(
      "aria-label",
      "Devices online: 95% (60 of 63)",
    );
  });

  it("clamps the arc and shows 0% for a zero/empty max without dividing by zero", () => {
    render(<Gauge value={5} max={0} label="Empty" />);
    expect(screen.getByText("0%")).toBeInTheDocument();
  });
});

describe("gaugeTone", () => {
  it("is success at/above the threshold and warning below", () => {
    expect(gaugeTone(0.95)).toBe("success");
    expect(gaugeTone(1)).toBe("success");
    expect(gaugeTone(0.94)).toBe("warning");
    expect(gaugeTone(0.8, 0.9)).toBe("warning");
    expect(gaugeTone(0.92, 0.9)).toBe("success");
  });
});
