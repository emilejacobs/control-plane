import "@testing-library/jest-dom/vitest";
import { afterAll, afterEach, beforeAll } from "vitest";
import { cleanup } from "@testing-library/react";
import { server } from "./test/server";

// MSW intercepts cp-api requests for the dashboard's flow tests. Tests that
// stub fetch directly are unaffected — unhandled requests are bypassed.
beforeAll(() => server.listen({ onUnhandledRequest: "bypass" }));
afterEach(() => {
  server.resetHandlers();
  cleanup(); // unmount React trees between tests
});
afterAll(() => server.close());
