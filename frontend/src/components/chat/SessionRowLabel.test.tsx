import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { SessionRowLabel, sessionRowTooltip } from "./SessionRowLabel";

describe("SessionRowLabel", () => {
  it("renders only the title on one visual line", () => {
    const html = renderToStaticMarkup(
      <SessionRowLabel
        title="会话统计命令"
        preview="`/stats` 是 AgentMate 前端的本地斜杠命令，不会走到模型。"
      />,
    );

    expect(html).toContain("会话统计命令");
    // The preview is tooltip-only metadata; the rendered text stays title-only.
    expect(html.replace(/<[^>]*>/g, "")).toBe("会话统计命令");
    expect(html).toContain("truncate");
    expect(sessionRowTooltip({
      title: "会话统计命令",
      preview: "`/stats` 是 AgentMate 前端的本地斜杠命令，不会走到模型。",
    })).toBe("`/stats` 是 AgentMate 前端的本地斜杠命令，不会走到模型。");
  });

  it("keeps the fork origin and reply preview in the hover tooltip", () => {
    const html = renderToStaticMarkup(
      <SessionRowLabel
        title="修复侧栏"
        preview="已改为单行标题。"
        forkedFromTitle="统计排查"
        typeLabel="Agent"
      />,
    );

    expect(html).toContain("修复侧栏");
    expect(html.replace(/<[^>]*>/g, "")).toBe("修复侧栏");
    expect(sessionRowTooltip({
      title: "修复侧栏",
      preview: "已改为单行标题。",
      forkedFromTitle: "统计排查",
      typeLabel: "Agent",
    })).toBe("Agent\n分叉自「统计排查」\n已改为单行标题。");
  });
});
