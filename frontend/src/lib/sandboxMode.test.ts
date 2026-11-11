import { describe, expect, it } from "vitest";
import { SANDBOX_MODE_OPTIONS, sandboxModeLabel, sandboxModeOption, sandboxModeShortLabel } from "./sandboxMode";

describe("sandbox mode metadata", () => {
  it("keeps the three modes in the order shown by both pickers", () => {
    expect(SANDBOX_MODE_OPTIONS.map((option) => option.value)).toEqual([
      "request-approval",
      "auto-approve",
      "danger-full-access",
    ]);
  });

  it("normalizes legacy and unknown values to the safe option", () => {
    expect(sandboxModeOption("workspace-write").value).toBe("request-approval");
    expect(sandboxModeOption("unknown").value).toBe("request-approval");
    expect(sandboxModeLabel("auto")).toBe("帮我批准");
  });

  it("marks only full access as dangerous", () => {
    expect(SANDBOX_MODE_OPTIONS.filter((option) => option.danger).map((option) => option.value)).toEqual([
      "danger-full-access",
    ]);
  });

  it("uses a compact composer label without weakening the settings title", () => {
    expect(sandboxModeLabel("danger-full-access")).toBe("完全访问权限");
    expect(sandboxModeShortLabel("danger-full-access")).toBe("完全访问");
    expect(sandboxModeShortLabel("auto-approve")).toBe("帮我批准");
  });

  it("keeps the default option safe and the full option explicit", () => {
    expect(sandboxModeOption(undefined).value).toBe("request-approval");
    expect(sandboxModeOption("danger-full-access").description).toContain("关闭 OS 沙箱");
  });
});
