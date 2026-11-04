import { describe, expect, it } from "vitest";
import { resolveAgentModel } from "./agentModel";
import type { ModelProvider } from "@/lib/api";

const providers = [
  { id: "p1", name: "默认", isDefault: true, models: [{ id: "m1" }, { id: "m2" }] },
  { id: "p2", name: "备用", models: [{ id: "x1" }] },
] as ModelProvider[];

describe("resolveAgentModel", () => {
  it("inherits the default provider's first model when both are blank", () => {
    const got = resolveAgentModel({ providerId: "", model: "" }, providers);
    expect(got.provider?.id).toBe("p1");
    expect(got.model?.id).toBe("m1");
  });

  it("falls back to the first provider when none is marked default", () => {
    const got = resolveAgentModel({ providerId: "", model: "" }, providers.map((p) => ({ ...p, isDefault: false })));
    expect(got.provider?.id).toBe("p1");
    expect(got.model?.id).toBe("m1");
  });

  it("keeps an explicit provider and model", () => {
    const got = resolveAgentModel({ providerId: "p2", model: "x1" }, providers);
    expect(got.provider?.id).toBe("p2");
    expect(got.model?.id).toBe("x1");
  });

  it("uses the explicit provider's first model when only the model is blank", () => {
    const got = resolveAgentModel({ providerId: "p2", model: "" }, providers);
    expect(got.provider?.id).toBe("p2");
    expect(got.model?.id).toBe("x1");
  });

  it("does not silently rebind a dangling provider", () => {
    const got = resolveAgentModel({ providerId: "gone", model: "" }, providers);
    expect(got.provider).toBeNull();
    expect(got.model).toBeNull();
  });

  it("returns nothing when no provider is configured", () => {
    expect(resolveAgentModel({ providerId: "", model: "" }, [])).toEqual({ provider: null, model: null });
    expect(resolveAgentModel(null, providers)).toEqual({ provider: null, model: null });
  });
});