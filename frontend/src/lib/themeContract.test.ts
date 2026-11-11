import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

const css = readFileSync(fileURLToPath(new URL("../index.css", import.meta.url)), "utf8");

type ThemeName = "nord" | "graphite" | "paper" | "contrast";
type Mode = "light" | "dark";

function declarationsFor(theme: ThemeName, mode: Mode): Record<string, string> {
  const selector = mode === "dark"
    ? `[data-theme="${theme}"].dark`
    : `[data-theme="${theme}"]`;
  const blocks = [...css.matchAll(/([^{}]+)\{([^{}]*)\}/g)];
  const block = blocks.find(([, rawSelector]) => rawSelector.includes(selector));
  if (!block) throw new Error(`missing ${theme}/${mode} token block`);
  const tokens: Record<string, string> = {};
  for (const match of block[2].matchAll(/--([a-z0-9-]+):\s*([^;]+);/gi)) {
    tokens[match[1]] = match[2].trim();
  }
  return tokens;
}

function luminance(hex: string): number {
  const value = hex.replace("#", "");
  const channels = [0, 2, 4].map((offset) => parseInt(value.slice(offset, offset + 2), 16) / 255);
  const linear = channels.map((channel) =>
    channel <= 0.04045 ? channel / 12.92 : Math.pow((channel + 0.055) / 1.055, 2.4),
  );
  return 0.2126 * linear[0] + 0.7152 * linear[1] + 0.0722 * linear[2];
}

function contrast(a: string, b: string): number {
  if (!a.startsWith("#") || !b.startsWith("#")) {
    throw new Error(`non-hex token cannot be tested: ${a} / ${b}`);
  }
  const [high, low] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (high + 0.05) / (low + 0.05);
}

const themes: ThemeName[] = ["nord", "graphite", "paper", "contrast"];
const modes: Mode[] = ["light", "dark"];

describe("theme token contract", () => {
  for (const theme of themes) {
    for (const mode of modes) {
      it(`${theme}/${mode} keeps text, statuses and focus readable`, () => {
        const tokens = declarationsFor(theme, mode);
        for (const background of ["canvas", "chrome", "panel", "overlay"]) {
          expect(contrast(tokens.foreground, tokens[background]), `foreground/${background}`).toBeGreaterThanOrEqual(4.5);
          expect(contrast(tokens["muted-foreground"], tokens[background]), `muted/${background}`).toBeGreaterThanOrEqual(4.5);
        }
        for (const status of ["danger-text", "warning-text", "success-text", "accent-text"]) {
          expect(contrast(tokens[status], tokens.panel), `${status}/panel`).toBeGreaterThanOrEqual(4.5);
        }
        expect(contrast(tokens.primary, tokens["primary-foreground"]), "primary pair").toBeGreaterThanOrEqual(4.5);
        for (const background of ["canvas", "chrome", "panel"]) {
          expect(contrast(tokens.ring, tokens[background]), `ring/${background}`).toBeGreaterThanOrEqual(3);
        }
      });

      it(`${theme}/${mode} keeps controls distinct from their surface`, () => {
        const tokens = declarationsFor(theme, mode);
        expect(tokens.control).toBeDefined();
        const ratio = contrast(tokens.control, tokens.panel);
        expect(ratio, "control/panel").toBeGreaterThanOrEqual(1.05);
        expect(ratio, "control/panel").toBeLessThanOrEqual(1.35);
        expect(contrast(tokens.control, tokens.accent), "control/accent").toBeGreaterThanOrEqual(1.05);
        expect(contrast(tokens.foreground, tokens.control), "foreground/control").toBeGreaterThanOrEqual(4.5);
      });

      it(`${theme}/${mode} keeps selected choices visible and readable`, () => {
        const tokens = declarationsFor(theme, mode);
        expect(tokens["choice-selected"]).toBeDefined();
        expect(tokens["choice-selected-danger"]).toBeDefined();
        expect(contrast(tokens["choice-selected"], tokens.control), "choice-selected/control").toBeGreaterThanOrEqual(1.2);
        expect(contrast(tokens["choice-selected"], tokens.panel), "choice-selected/panel").toBeGreaterThanOrEqual(1.2);
        expect(contrast(tokens["choice-selected-danger"], tokens.control), "choice-danger/control").toBeGreaterThanOrEqual(1.15);
        for (const background of ["choice-selected", "choice-selected-danger"] as const) {
          expect(contrast(tokens.foreground, tokens[background]), `foreground/${background}`).toBeGreaterThanOrEqual(4.5);
        }
        expect(contrast(tokens["danger-text"], tokens["choice-selected-danger"]), "danger-text/choice-danger").toBeGreaterThanOrEqual(3);
      });

      it(`${theme}/${mode} keeps layout surfaces perceptible but calm`, () => {
        const tokens = declarationsFor(theme, mode);
        for (const [a, b] of [["canvas", "chrome"], ["chrome", "panel"]] as const) {
          const ratio = contrast(tokens[a], tokens[b]);
          expect(ratio, `${a}/${b}`).toBeGreaterThanOrEqual(1.05);
          expect(ratio, `${a}/${b}`).toBeLessThanOrEqual(1.3);
        }
      });
    }
  }
});
