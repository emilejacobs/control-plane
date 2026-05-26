import { describe, it, expect, afterEach } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import PreviewClient from "./preview-client";

afterEach(cleanup);

describe("/preview/[cameraId] — Camera live preview", () => {
  it("renders an <img> whose src points at /preview/<id>/stream", () => {
    // PreviewClient is the inner client component; the outer server
    // component (page.tsx) resolves params + delegates rendering to
    // this one. Testing the client part keeps the test fast and
    // doesn't need to await a Promise param.
    render(<PreviewClient cameraId="cam1" />);
    const img = screen.getByRole("img");
    expect(img.getAttribute("src")).toBe("/preview/cam1/stream");
  });
});
