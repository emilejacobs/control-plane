import { setupServer } from "msw/node";

// server intercepts cp-api calls in tests. Each test installs its own
// handlers via server.use(...); vitest.setup.ts resets them between tests.
export const server = setupServer();
