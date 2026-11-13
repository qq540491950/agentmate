import { describe, expect, it } from "vitest";
import {
  PROVIDER_EDITOR_BODY_CLASS,
  PROVIDER_EDITOR_DIALOG_CLASS,
} from "./providerEditorLayout";

// #32 回归防护：模型较多时，供应商编辑器曾超出视口，导致「保存」/「取消」
// 页脚无法点击。对话框必须限制高度，并由主体——而不是对话框——负责滚动。
describe("provider editor dialog layout", () => {
  it("caps the dialog height instead of letting the model list grow it", () => {
    expect(PROVIDER_EDITOR_DIALOG_CLASS).toContain("max-h-[85vh]");
    expect(PROVIDER_EDITOR_DIALOG_CLASS).toContain("flex");
    expect(PROVIDER_EDITOR_DIALOG_CLASS).toContain("flex-col");
  });

  it("scrolls only the body so the footer stays visible", () => {
    expect(PROVIDER_EDITOR_BODY_CLASS).toContain("overflow-y-auto");
    expect(PROVIDER_EDITOR_DIALOG_CLASS).not.toContain("overflow-y-auto");
  });

  it("lets the body shrink below its content height inside the flex column", () => {
    // 没有 `min-h-0` 时，flexbox
    // 不会把主体收缩到低于其内容高度，而设置了高度上限的对话框会裁剪页脚，而不
    // 是滚动列表。
    expect(PROVIDER_EDITOR_BODY_CLASS).toContain("min-h-0");
    expect(PROVIDER_EDITOR_BODY_CLASS).toContain("flex-1");
  });
});
