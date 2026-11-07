import { ArrowRight, Sparkles } from "lucide-react";
import type { SettingsTab } from "@/pages/SettingsPage";

export function Onboarding({ onOpenSettings }: { onOpenSettings: (tab?: SettingsTab) => void }) {
  const steps = [
    { n: 1, title: "配置模型供应商", desc: "添加 OpenAI 兼容 / Anthropic / Gemini 接口" },
    { n: 2, title: "直接开聊", desc: "输入消息立即对话；@ 可指定助手、团队或远程 Agent" },
  ];
  return (
    <div className="mx-auto mt-12 max-w-md rounded-2xl border border-border bg-card p-6 shadow-card">
      <div className="flex items-center gap-3">
        <span className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl bg-brand/10 text-brand">
          <Sparkles className="h-5 w-5" />
        </span>
        <div className="min-w-0">
          <div className="text-xl font-semibold leading-tight">欢迎使用 AgentMate</div>
          <p className="mt-0.5 text-ui text-muted-foreground">基于 Google ADK 的本地 Agent 工作台</p>
        </div>
      </div>
      <div className="mt-5 grid gap-2">
        {steps.map((s) => (
          <button
            key={s.n}
            onClick={s.n === 1 ? () => onOpenSettings("models") : undefined}
            className="flex w-full items-center gap-3 rounded-lg border border-border p-3 text-left transition-colors hover:border-brand/40 hover:bg-brand/5"
          >
            <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-brand/10 text-xs font-semibold text-brand">
              {s.n}
            </span>
            <span className="min-w-0 flex-1">
              <span className="block text-ui font-medium">{s.title}</span>
              <span className="block text-xs text-muted-foreground">{s.desc}</span>
            </span>
            <ArrowRight className="h-4 w-4 shrink-0 text-muted-foreground" />
          </button>
        ))}
      </div>
    </div>
  );
}
