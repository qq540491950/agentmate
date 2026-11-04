import { describe, expect, it } from "vitest";
import {
  PROVIDER_EDITOR_BODY_CLASS,
  PROVIDER_EDITOR_DIALOG_CLASS,
} from "./providerEditorLayout";

// #32 regression guard: with enough models the provider editor grew past the
// viewport and the 「保存」/「取消」 footer was unreachable. The dialog must cap
// its height and the body — not the dialog — must own the scroll.
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
    // Without `min-h-0` flexbox refuses to shrink the body below its content,
    // and the capped dialog clips the footer instead of scrolling the list.
    expect(PROVIDER_EDITOR_BODY_CLASS).toContain("min-h-0");
    expect(PROVIDER_EDITOR_BODY_CLASS).toContain("flex-1");
  });
});