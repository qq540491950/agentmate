import { describe, expect, it } from "vitest";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const sourceRoot = fileURLToPath(new URL("..", import.meta.url));
const css = readFileSync(fileURLToPath(new URL("../index.css", import.meta.url)), "utf8");

function tsxFiles(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) return tsxFiles(path);
    return path.endsWith(".tsx") ? [path] : [];
  });
}

describe("visual system guardrails", () => {
  it("keeps the Chinese reading font ahead of the Windows fallback", () => {
    const stack = css.match(/--font-sans:\s*([^;]+);/)?.[1] ?? "";
    expect(stack.indexOf("Noto Sans SC")).toBeGreaterThan(-1);
    expect(stack.indexOf("Noto Sans SC")).toBeLessThan(stack.indexOf("Microsoft YaHei UI"));
  });

  it("removes sub-12px Chinese text and text opacity variants", () => {
    const forbidden = [
      { name: "10/11px text utility", pattern: /\btext-3xs\b|\btext-\[(?:10|11)px\]/ },
      {
        name: "text opacity",
        pattern:
          /\btext-(?:foreground|muted-foreground|accent-text|danger-text|warning-text|success-text|popover-foreground)\/[0-9]+/,
      },
    ];
    const offenders = tsxFiles(sourceRoot).flatMap((path) => {
      const source = readFileSync(path, "utf8");
      return forbidden
        .filter(({ pattern }) => pattern.test(source))
        .map(({ name }) => `${path.replace(sourceRoot, "")}: ${name}`);
    });
    expect(offenders).toEqual([]);
  });

  it("keeps layout surfaces solid rather than ad-hoc alpha fills", () => {
    const pattern =
      /\bbg-(?:panel|recess|control|selected|bubble|chrome|overlay|canvas|muted|accent)\/[0-9]+/;
    const offenders = tsxFiles(sourceRoot)
      .filter((path) => pattern.test(readFileSync(path, "utf8")))
      .map((path) => path.replace(sourceRoot, ""));
    expect(offenders).toEqual([]);
  });

  it("keeps materials and control radii on the shared scale", () => {
    expect(css).toContain("--text-2xs: 0.75rem");
    expect(css).toContain("--radius-lg: 8px");
    expect(css).toContain("--radius-xl: 12px");
    expect(css).toContain("--radius-2xl: 16px");
    expect(css).toContain("@utility surface-dialog");
    expect(css).not.toContain("--shadow-card");
    expect(css).not.toContain("--shadow-panel");
  });
});
