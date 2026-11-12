import { describe, expect, it } from "vitest";
import { cn } from "./utils";

describe("cn", () => {
  it("keeps the custom text-ui size alongside a text color", () => {
    expect(cn("text-ui text-muted-foreground")).toContain("text-ui");
    expect(cn("text-ui text-muted-foreground")).toContain("text-muted-foreground");
    expect(cn("text-ui", "text-foreground")).toBe("text-ui text-foreground");
  });

  it("still resolves real utility conflicts", () => {
    expect(cn("px-2 p-3")).toBe("p-3");
    expect(cn("text-sm", "text-ui")).toBe("text-ui");
  });
});
