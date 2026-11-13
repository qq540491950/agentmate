import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { ScrollArea } from "./scroll-area";

// 根元素必须保留 overflow-hidden：聊天列将此组件作为固定高度布局的 flex-1
// 子元素放置，且仅非 visible 的 overflow 才能让 flexbox
// 将其收缩到内容高度以下。若没有它，长聊天会把输入框挤出窗口，且该区域无法滚动
// 。
describe("ScrollArea", () => {
  it("renders the root with overflow-hidden so flex parents can shrink it", () => {
    const html = renderToStaticMarkup(
      <ScrollArea className="flex-1">
        <div>content</div>
      </ScrollArea>,
    );
    const rootTag = html.slice(0, html.indexOf(">") + 1);
    expect(rootTag).toContain('data-slot="scroll-area"');
    expect(rootTag).toContain("overflow-hidden");
  });
});
