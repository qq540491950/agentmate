import { describe, expect, it } from "vitest";
import {
  APPROVAL_OPTIONS,
  SANDBOX_PROFILE_OPTIONS,
  approvalPolicyLabel,
  approvalPolicyOption,
  approvalPolicyShortLabel,
  executionPolicyHint,
  sandboxNetworkCopy,
  sandboxNeedsRiskConfirmation,
  sandboxProfileLabel,
  sandboxProfileOption,
  sandboxProfileShortLabel,
  sandboxStatusSummary,
  SANDBOX_UNAVAILABLE_MESSAGE,
} from "./sandboxMode";

describe("execution policy metadata", () => {
  it("exposes the three approval and three sandbox choices shown by both pickers", () => {
    expect(APPROVAL_OPTIONS.map((option) => option.value)).toEqual([
      "request-approval",
      "auto-approve",
      "never",
    ]);
    expect(APPROVAL_OPTIONS.map((option) => option.label)).toEqual([
      "请求批准",
      "仅风险确认",
      "从不询问",
    ]);
    expect(SANDBOX_PROFILE_OPTIONS.map((option) => option.value)).toEqual([
      "workspace-write",
      "read-only",
      "danger-full-access",
    ]);
    expect(SANDBOX_PROFILE_OPTIONS.map((option) => option.label)).toEqual([
      "工作区可写",
      "只读",
      "完全访问",
    ]);
  });

  it("normalizes legacy and unknown values to safe independent options", () => {
    expect(approvalPolicyOption("workspace-write").value).toBe("request-approval");
    expect(approvalPolicyOption("auto").value).toBe("auto-approve");
    expect(approvalPolicyOption("danger-full-access").value).toBe("never");
    expect(approvalPolicyOption("unknown").value).toBe("request-approval");
    expect(approvalPolicyLabel("auto-approve")).toBe("仅风险确认");
    expect(sandboxProfileOption("request-approval").value).toBe("workspace-write");
    expect(sandboxProfileOption("unknown").value).toBe("workspace-write");
    expect(sandboxProfileOption("read-only")).toEqual(expect.objectContaining({
      value: "read-only",
      label: "只读",
      description: "终端只能读，不能写工作区；文件写入会被拒绝。",
    }));
    expect(sandboxProfileLabel("danger-full-access")).toBe("完全访问");
  });

  it("uses compact composer labels without changing the settings title", () => {
    expect(approvalPolicyShortLabel("auto-approve")).toBe("仅风险");
    expect(approvalPolicyShortLabel("never")).toBe("从不询问");
    expect(sandboxProfileShortLabel("workspace-write")).toBe("工作区");
    expect(sandboxProfileShortLabel("read-only")).toBe("只读");
    expect(sandboxProfileShortLabel("danger-full-access")).toBe("完全访问");
  });

  it("requires an explicit risk acknowledgement before full access", () => {
    expect(SANDBOX_PROFILE_OPTIONS.filter((option) => option.danger).map((option) => option.value)).toEqual([
      "danger-full-access",
    ]);
    expect(sandboxNeedsRiskConfirmation("danger-full-access")).toBe(true);
    expect(sandboxNeedsRiskConfirmation("workspace-write")).toBe(false);
    expect(sandboxNeedsRiskConfirmation("read-only")).toBe(false);
    expect(sandboxNeedsRiskConfirmation("unknown")).toBe(false);
  });

  it("explains the two cross-axis combinations that are easy to misread", () => {
    expect(executionPolicyHint({
      approvalPolicy: "never",
      sandboxProfile: "workspace-write",
    })).toContain("仍受工作区沙箱约束");
    expect(executionPolicyHint({
      approvalPolicy: "request-approval",
      sandboxProfile: "danger-full-access",
    })).toContain("每一步仍会请求批准");
    expect(executionPolicyHint({
      approvalPolicy: "never",
      sandboxProfile: "read-only",
    })).toBe("只读沙箱：终端只能读，不能写工作区；审批策略仍独立，文件写入会被拒绝。");
    expect(executionPolicyHint({
      approvalPolicy: "auto-approve",
      sandboxProfile: "workspace-write",
    })).toBeNull();
  });

  it("reports the backend, or the exact fail-closed warning, from SandboxStatus", () => {
    expect(sandboxStatusSummary({
      available: true,
      backend: "windows-appcontainer",
    })).toEqual(expect.objectContaining({
      title: "本地工具沙箱 · Windows AppContainer",
      warning: false,
    }));
    expect(sandboxStatusSummary({
      available: false,
      backend: "unsupported",
    })).toEqual(expect.objectContaining({
      title: "本地工具沙箱不可用",
      detail: SANDBOX_UNAVAILABLE_MESSAGE,
      warning: true,
    }));
  });

  it("keeps network copy honest for danger + never without rewriting the preference", () => {
    const copy = sandboxNetworkCopy({
      approvalPolicy: "never",
      sandboxProfile: "danger-full-access",
    }, "off");
    expect(copy.label).toBe("允许联网（不限制目标）");
    expect(copy.checked).toBe(true);
    expect(copy.disabled).toBe(true);
    expect(copy.note).toContain("domain allowlist");
    expect(copy.note).toContain("私网");
    expect(copy.notice).toContain("联网由完全访问隐含开启");
    expect(copy.notice).toContain("从不询问 + 完全访问");
  });
});
