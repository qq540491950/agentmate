import { describe, expect, it } from "vitest";
import {
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_MIN_WIDTH,
  clampSidebarWidth,
  parseStoredSidebarWidth,
  sidebarMaxWidth,
} from "./sidebarWidth";

describe("sidebarMaxWidth", () => {
  it("caps the sidebar at half the window", () => {
    expect(sidebarMaxWidth(1200)).toBe(600);
  });

  it("never drops below the readable minimum on tiny windows", () => {
    expect(sidebarMaxWidth(320)).toBe(SIDEBAR_MIN_WIDTH);
  });
});

describe("clampSidebarWidth", () => {
  it("keeps an in-range width unchanged", () => {
    expect(clampSidebarWidth(320, 1200)).toBe(320);
  });

  it("clamps below the minimum and above half the window", () => {
    expect(clampSidebarWidth(120, 1200)).toBe(SIDEBAR_MIN_WIDTH);
    expect(clampSidebarWidth(900, 1200)).toBe(600);
  });

  it("rounds fractional pointer positions", () => {
    expect(clampSidebarWidth(317.6, 1200)).toBe(318);
  });

  it("falls back to the default for non-finite input", () => {
    expect(clampSidebarWidth(Number.NaN, 1200)).toBe(SIDEBAR_DEFAULT_WIDTH);
  });
});

describe("parseStoredSidebarWidth", () => {
  it("uses the default when nothing is stored", () => {
    expect(parseStoredSidebarWidth(null, 1200)).toBe(SIDEBAR_DEFAULT_WIDTH);
  });

  it("restores a persisted width", () => {
    expect(parseStoredSidebarWidth("340", 1200)).toBe(340);
  });

  it("falls back on malformed values", () => {
    expect(parseStoredSidebarWidth("wide", 1200)).toBe(SIDEBAR_DEFAULT_WIDTH);
  });

  it("re-clamps a stale width against the current window", () => {
    expect(parseStoredSidebarWidth("900", 1200)).toBe(600);
  });
});