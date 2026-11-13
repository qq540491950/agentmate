import type { LucideIcon } from "lucide-react";
import { cn } from "@/lib/utils";

// EmptyState 是列表视图共享的空占位符：统一采用一种视觉语言（居中的图标 + 标题
// + 提示）， 而不是让每个页面各自临时编写一段自己的 muted 段落。
export function EmptyState({ icon: Icon, title, desc, className }: {
  icon: LucideIcon;
  title: string;
  desc?: string;
  className?: string;
}) {
  return (
    <div className={cn("flex flex-col items-center justify-center gap-2 px-6 py-12 text-center", className)}>
      <span className="flex h-10 w-10 items-center justify-center rounded-full bg-accent-soft text-accent-text">
        <Icon className="h-5 w-5" aria-hidden />
      </span>
      <div className="text-sm font-medium">{title}</div>
      {desc && <div className="max-w-md text-xs text-muted-foreground">{desc}</div>}
    </div>
  );
}
