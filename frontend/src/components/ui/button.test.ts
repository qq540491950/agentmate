import { describe, expect, it } from "vitest";
import { buttonVariants } from "./button";

describe("button surface variants", () => {
  it("uses the control surface for outline buttons", () => {
    const className = buttonVariants({ variant: "outline" });
    expect(className).toContain("bg-control");
    expect(className).not.toContain("bg-panel");
  });
});
