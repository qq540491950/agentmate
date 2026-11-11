import { describe, expect, it } from "vitest";
import { isPartialNumericText, numericDraft, validateNumericDraft, type NumericRule } from "./numericRules";

const integer: NumericRule = { min: 0, max: 65535, integer: true };

describe("numeric rules", () => {
  it("accepts legal integers at the boundaries", () => {
    expect(validateNumericDraft("0", integer)).toEqual({ valid: true, value: 0, empty: false });
    expect(validateNumericDraft("65535", integer)).toEqual({ valid: true, value: 65535, empty: false });
  });

  it("rejects negatives, decimals and scientific notation", () => {
    expect(validateNumericDraft("-1", integer).valid).toBe(false);
    expect(validateNumericDraft("1.5", integer).valid).toBe(false);
    expect(validateNumericDraft("1e3", integer).valid).toBe(false);
  });

  it("turns an empty optional field into its empty value", () => {
    expect(validateNumericDraft("", { ...integer, emptyValue: 0 })).toEqual({
      valid: true,
      value: 0,
      empty: true,
    });
  });

  it("honours decimal precision and range", () => {
    const temperature: NumericRule = { min: 0, max: 2, integer: false, decimals: 2 };
    expect(validateNumericDraft("1.25", temperature)).toEqual({ valid: true, value: 1.25, empty: false });
    expect(validateNumericDraft("1.256", temperature).valid).toBe(false);
    expect(validateNumericDraft("2.1", temperature).valid).toBe(false);
  });

  it("recognises partial text so typing a decimal does not flash an error", () => {
    expect(isPartialNumericText("1.", { integer: false })).toBe(true);
    expect(isPartialNumericText("1.2", { integer: false })).toBe(true);
    expect(isPartialNumericText("e", { integer: false })).toBe(false);
    expect(isPartialNumericText("1.2", { integer: true })).toBe(false);
  });

  it("formats null and undefined as an empty draft", () => {
    expect(numericDraft(undefined)).toBe("");
    expect(numericDraft(null)).toBe("");
    expect(numericDraft(0)).toBe("0");
  });
});
