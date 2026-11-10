import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";
import { MessageMarkdown } from "./MessageMarkdown";

function visibleText(html: string): string {
  return html
    .replace(/<!--.*?-->/g, "")
    .replace(/<[^>]*>/g, "")
    .replace(/&amp;/g, "&")
    .replace(/&lt;/g, "<")
    .replace(/&gt;/g, ">")
    .replace(/&quot;/g, '"')
    .replace(/&#x27;/g, "'");
}

function renderFence(lang: string, code: string): string {
  return visibleText(
    renderToStaticMarkup(<MessageMarkdown content={`\`\`\`${lang}\n${code}\n\`\`\``} />),
  );
}

describe("MessageMarkdown fenced code", () => {
  it("保留 bash 代码块中的非关键字标识符", () => {
    const code = "cd frontend && npx tsc --noEmit && npm test && npm run build";

    expect(renderFence("bash", code)).toContain(code);
  });

  it("同时保留普通标识符、关键字、字符串与数字", () => {
    const cases = [
      ["ts", 'const value = build("ready") + 42'],
      ["bash", 'echo "ready" && npm run build -- --port 9246'],
      ["yaml", "name: demo\nversion: 1"],
    ];

    for (const [lang, code] of cases) {
      expect(renderFence(lang, code)).toContain(code);
    }
  });
});
