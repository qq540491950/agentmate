import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import {
  BudgetPauseCard,
  RecoveryBanner,
  VerificationCard,
  planDraftToSteps,
} from "./WorkflowCards";

describe("WorkflowCards", () => {
  it("renders budget pause with continue and stop actions", () => {
    const html = renderToStaticMarkup(
      <BudgetPauseCard
        budget={{ reason: "tool_calls", limit: 120, used: 120, toolCalls: 120, modelCalls: 20, turnTokens: 5000 }}
        onContinue={vi.fn()}
        onStop={vi.fn()}
      />,
    );
    expect(html).toContain("本轮已按预算暂停");
    expect(html).toContain("继续");
    expect(html).toContain("停止");
    expect(html).toContain("bg-warning-soft");
    expect(html.slice(0, html.indexOf(">"))).not.toMatch(/\bborder-/);
  });

  it("renders recovery actions and blocked verification with artifact", () => {
    const recovery = renderToStaticMarkup(
      <RecoveryBanner status="interrupted" onResume={vi.fn()} onViewChanges={vi.fn()} onAbandon={vi.fn()} />,
    );
    expect(recovery).toContain("查看变更");
    expect(recovery).toContain("放弃");
    const verification = renderToStaticMarkup(
      <VerificationCard
        verification={{ ok: false, status: "blocked", artifact: "C:/tmp/verify.log", reason: "测试失败" }}
        onRetry={vi.fn()}
        onExempt={vi.fn()}
      />,
    );
    expect(verification).toContain("验证未通过");
    expect(verification).toContain("verify.log");
    expect(verification).toContain("显式豁免");
  });

  it("converts plan editor text into dependency, acceptance and evidence arrays", () => {
    const steps = planDraftToSteps([{
      id: "one",
      content: "实现",
      status: "completed",
      dependsOnText: "zero, prep",
      acceptanceText: "测试通过\n构建通过",
      evidenceText: "verify:ok\nartifact:out.log",
    }]);
    expect(steps[0].dependsOn).toEqual(["zero", "prep"]);
    expect(steps[0].acceptance).toEqual(["测试通过", "构建通过"]);
    expect(steps[0].evidence).toEqual(["verify:ok", "artifact:out.log"]);
  });
});
