import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { TurnStatus, turnStatusElapsed } from "./TurnStatus";

describe("TurnStatus", () => {
  it("静态渲染微光文字与三点呼吸，不暴露为读屏重复播报", () => {
    const html = renderToStaticMarkup(<TurnStatus />);

    expect(html).toContain('data-slot="turn-status"');
    expect(html).toContain("正在思考");
    expect(html).toContain("wb-thinking-shimmer");
    expect(html.match(/wb-dot-pulse/g)).toHaveLength(3);
    expect(html).toContain('aria-hidden="true"');
  });

  it("前两秒不显示计时，之后显示秒数", () => {
    expect(turnStatusElapsed(0)).toBe("");
    expect(turnStatusElapsed(1)).toBe("");
    expect(turnStatusElapsed(2)).toBe("2s");
    expect(turnStatusElapsed(12)).toBe("12s");
  });
});
