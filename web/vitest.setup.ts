import "@testing-library/jest-dom/vitest";
import { afterAll, afterEach, beforeAll } from "vitest";
import { cleanup } from "@testing-library/react";
import { server } from "./test/server";

// vitest 4 + jsdom 29 ship an empty {} as localStorage rather than a real
// Storage object, so tests that touch localStorage need a working stand-in.
// A Map-backed Storage is enough for the dashboard — we only read/write/
// remove the single tokens key (see ADR-024).
class MemoryStorage implements Storage {
  private store = new Map<string, string>();
  get length(): number {
    return this.store.size;
  }
  clear(): void {
    this.store.clear();
  }
  getItem(key: string): string | null {
    return this.store.has(key) ? (this.store.get(key) as string) : null;
  }
  key(index: number): string | null {
    return Array.from(this.store.keys())[index] ?? null;
  }
  removeItem(key: string): void {
    this.store.delete(key);
  }
  setItem(key: string, value: string): void {
    this.store.set(key, String(value));
  }
}
const memoryLocalStorage = new MemoryStorage();
Object.defineProperty(globalThis, "localStorage", {
  configurable: true,
  value: memoryLocalStorage,
});
if (typeof window !== "undefined") {
  Object.defineProperty(window, "localStorage", {
    configurable: true,
    value: memoryLocalStorage,
  });
}

// MSW intercepts cp-api requests for the dashboard's flow tests. Tests that
// stub fetch directly are unaffected — unhandled requests are bypassed.
beforeAll(() => server.listen({ onUnhandledRequest: "bypass" }));
afterEach(() => {
  server.resetHandlers();
  cleanup(); // unmount React trees between tests
});
afterAll(() => server.close());
