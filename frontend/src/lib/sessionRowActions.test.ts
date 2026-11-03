import { describe, expect, it } from "vitest";
import { SESSION_ROW_ACTIONS_CLASS } from "./sessionRowActions";

describe("SESSION_ROW_ACTIONS_CLASS", () => {
  it("keeps the dropdown trigger laid out while its menu is open", () => {
    // Regression for #28: once Radix's modal layer kills :hover and moves
    // focus into the portal, a display:none cluster leaves the popper with a
    // 0×0 anchor rect, so the menu lands in the window's top-left corner.
    expect(SESSION_ROW_ACTIONS_CLASS).toContain("group-has-data-[state=open]:flex");
  });

  it("still reveals the actions on hover and on keyboard focus", () => {
    // The cluster is collapsed at rest; hover and focus-within are what make
    // it reachable (#24 / the keyboard-accessibility fix that introduced
    // group-focus-within).
    expect(SESSION_ROW_ACTIONS_CLASS.split(" ")).toContain("hidden");
    expect(SESSION_ROW_ACTIONS_CLASS).toContain("group-hover:flex");
    expect(SESSION_ROW_ACTIONS_CLASS).toContain("group-focus-within:flex");
  });
});