import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { PlanDock } from "./TodoCards";

describe("PlanDock", () => {
  it("renders the active plan above the composer without layout borders", () => {
    const html = renderToStaticMarkup(
      <PlanDock
        running
        waiting
        todos={[
          { id: "1", content: "收集素材", status: "completed" },
          { id: "2", content: "起草周报", status: "in_progress" },
          { id: "3", content: "审阅", status: "pending", dependsOn: ["2"] },
        ]}
      />,
    );
    expect(html).toContain("任务计划");
    expect(html).toContain("1/3 完成");
    expect(html).toContain("等待确认");
    expect(html).toContain("起草周报");
    expect(html).toContain("收起任务计划");
    expect(html).toContain("bg-panel");
    expect(html.slice(0, html.indexOf(">"))).not.toMatch(/\bborder-/);
  });

  it("surfaces blocked steps in the summary", () => {
    const html = renderToStaticMarkup(
      <PlanDock
        running={false}
        todos={[
          { id: "1", content: "测试", status: "blocked" },
          { id: "2", content: "构建", status: "pending" },
        ]}
      />,
    );
    expect(html).toContain("1 项阻塞");
    expect(html).toContain("text-warning-text");
  });
});
