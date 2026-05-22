import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import Home from "./page";

describe("dashboard scaffold", () => {
  it("renders the app shell", () => {
    render(<Home />);
    expect(screen.getByText(/uKnomi Control Plane/i)).toBeInTheDocument();
  });
});
