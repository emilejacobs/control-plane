import { describe, it, expect } from "vitest";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";

// sourceFiles lists the non-test .ts/.tsx files under dir, recursively.
function sourceFiles(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir)) {
    const path = join(dir, entry);
    if (statSync(path).isDirectory()) {
      out.push(...sourceFiles(path));
    } else if (/\.tsx?$/.test(entry) && !/\.test\.tsx?$/.test(entry)) {
      out.push(path);
    }
  }
  return out;
}

// Structural rule (PRD § dashboard): live data flows only through the
// TanStack Query layer — no setInterval in components.
describe("structural rule: no setInterval", () => {
  it("no app/ or lib/ source file uses setInterval", () => {
    const root = join(import.meta.dirname, "..");
    const files = [join(root, "app"), join(root, "lib")].flatMap(sourceFiles);

    expect(files.length).toBeGreaterThan(0); // guards a broken file walk

    // Match the call `setInterval(` — not the bare word, which appears in
    // comments describing this very rule.
    const offenders = files.filter((f) => readFileSync(f, "utf8").includes("setInterval("));
    expect(offenders).toEqual([]);
  });
});
