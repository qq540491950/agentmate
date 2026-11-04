import type { AgentConfig, ModelInfo, ModelProvider } from "@/lib/api";

export type ResolvedAgentModel = {
  provider: ModelProvider | null;
  model: ModelInfo | null;
};

/**
 * resolveAgentModel mirrors the backend's config.ResolveAgentModel: an agent
 * with a blank provider follows the global default provider, and a blank
 * model that provider's first model. Explicit IDs that no longer exist stay
 * unresolved so the UI can show an unknown provider instead of silently
 * rebinding the agent (#39).
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