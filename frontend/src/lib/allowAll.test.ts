import { describe, expect, it } from "vitest";
import {
  ALLOW_ALL_RISK_KEY,
  allowAllRiskAlreadyAcknowledged,
  allowAllToggleHint,
  allowAllToggleLabel,
  rememberAllowAllRiskAcknowledged,
} from "./allowAll";

describe("allowAllRiskAlreadyAcknowledged", () => {
  it("only treats the explicit stored value as acknowledged", () => {
    expect(allowAllRiskAlreadyAcknowledged((k) => (k === ALLOW_ALL_RISK_KEY ? "1" : null))).toBe(true);
    expect(allowAllRiskAlreadyAcknowledged(() => null)).toBe(false);
    expect(allowAllRiskAlreadyAcknowledged((k) => (k === ALLOW_ALL_RISK_KEY ? "true" : null))).toBe(false);
  });

  it("fails safe when storage is unavailable", () => {
    // Private mode / disabled storage throws; the warning must still show
    // instead of silently enabling full access.
    expect(allowAllRiskAlreadyAcknowledged(() => { throw new Error("denied"); })).toBe(false);
  });
});

describe("rememberAllowAllRiskAcknowledged", () => {
  it("writes the acknowledgement under the shared key", () => {
    const writes: Array<[string, string]> = [];
    rememberAllowAllRiskAcknowledged((k, v) => writes.push([k, v]));
    expect(writes).toEqual([[ALLOW_ALL_RISK_KEY, "1"]]);
  });

  it("swallows storage failures so the grant itself still works", () => {
    expect(() => rememberAllowAllRiskAcknowledged(() => { throw new Error("denied"); })).not.toThrow();
  });
});

describe("allow-all wording", () => {
  it("keeps both switch locations on the same labels", () => {
    expect(allowAllToggleLabel(false)).toBe("允许完全访问");
    expect(allowAllToggleLabel(true)).toBe("完全访问已开启");
  });

  it("explains how to leave the granted state", () => {
    expect(allowAllToggleHint(false)).toContain("仅影响当前会话");
    expect(allowAllToggleHint(true)).toContain("点击关闭");
  });
});