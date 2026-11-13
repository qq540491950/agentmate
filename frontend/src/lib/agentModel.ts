import type { AgentConfig, ModelInfo, ModelProvider } from "@/lib/api";

export type ResolvedAgentModel = {
  provider: ModelProvider | null;
  model: ModelInfo | null;
};

/**
 * resolveAgentModel 镜像后端的 config.ResolveAgentModel：供应商为空的 Agent
 * 会跟随全局默认供应商，而模型为空时会使用该供应商的第一个模型。已不存在的显式
 * ID 会保持未解析状态，以便 UI 能显示未知供应商，而不是静默地重新绑定
 * Agent（#39）。
 */
export function resolveAgentModel(
  agent: Pick<AgentConfig, "providerId" | "model"> | null | undefined,
  providers: ModelProvider[],
): ResolvedAgentModel {
  if (!agent) return { provider: null, model: null };
  const provider = agent.providerId
    ? providers.find((p) => p.id === agent.providerId) ?? null
    : providers.find((p) => p.isDefault) ?? providers[0] ?? null;
  if (!provider) return { provider: null, model: null };
  const wanted = agent.model?.trim();
  if (wanted) {
    return { provider, model: provider.models?.find((m) => m.id === wanted) ?? null };
  }
  return { provider, model: provider.models?.[0] ?? null };
}