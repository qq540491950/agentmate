import { readFileSync, readdirSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import * as ts from "typescript";
import { describe, expect, it } from "vitest";
import { tooltipCardClassName } from "@/components/ui/tooltip";

const SRC_ROOT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

// These components use `title` as a visible heading prop, not as a native
// browser tooltip. Everything else must go through TooltipHint/Tooltip.
const CONTENT_TITLE_COMPONENTS = new Set([
  "EmptyState",
  "PageHeader",
  "Section",
  "SessionRowLabel",
]);

function tsxFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) return tsxFiles(full);
    return entry.isFile() && entry.name.endsWith(".tsx") && !entry.name.endsWith(".test.tsx")
      ? [full]
      : [];
  });
}

describe("tooltip contract", () => {
  it("does not use native DOM title attributes for hover hints", () => {
    const violations: string[] = [];
    for (const file of tsxFiles(SRC_ROOT)) {
      const source = readFileSync(file, "utf8");
      const sourceFile = ts.createSourceFile(
        file,
        source,
        ts.ScriptTarget.Latest,
        true,
        ts.ScriptKind.TSX,
      );
      const visit = (node: ts.Node) => {
        if (
          ts.isJsxAttribute(node)
          && node.name.getText(sourceFile) === "title"
          && node.parent.parent
        ) {
          const element = node.parent.parent;
          if (ts.isJsxOpeningElement(element) || ts.isJsxSelfClosingElement(element)) {
            const tag = element.tagName.getText(sourceFile);
            if (!CONTENT_TITLE_COMPONENTS.has(tag)) {
              const line = sourceFile.getLineAndCharacterOfPosition(node.getStart(sourceFile)).line + 1;
              violations.push(`${path.relative(SRC_ROOT, file)}:${line} <${tag}>`);
            }
          }
        }
        ts.forEachChild(node, visit);
      };
      visit(sourceFile);
    }

    expect(violations).toEqual([]);
  });

  it("uses the shared overlay surface without a native-looking border", () => {
    expect(tooltipCardClassName).toContain("bg-overlay");
    expect(tooltipCardClassName).toContain("text-popover-foreground");
    expect(tooltipCardClassName).toContain("shadow-pop");
    expect(tooltipCardClassName).not.toMatch(/\bbg-foreground\b|\bborder\b|\bring-1\b/);
  });
});
