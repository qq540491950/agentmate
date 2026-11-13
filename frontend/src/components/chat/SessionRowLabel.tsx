import { TooltipHint } from "@/components/ui/tooltip";

export function sessionRowTooltip({
  title,
  preview = "",
  forkedFromTitle = "",
  typeLabel = "",
}: {
  title: string;
  preview?: string;
  forkedFromTitle?: string;
  typeLabel?: string;
}) {
  return [
    typeLabel,
    forkedFromTitle ? `分叉自「${forkedFromTitle}」` : "",
    preview,
  ].filter(Boolean).join("\n") || title;
}

/**
 * SessionRowLabel
 * 渲染侧边栏的单行会话标题。最新助手回复会以共享悬停卡片的形式保持可用，而不是
 * 占用第二条视觉行，因此长对话不会让每一行显得臃肿。
 */
export function SessionRowLabel({
  title,
  preview = "",
  forkedFromTitle = "",
  typeLabel = "",
}: {
  title: string;
  preview?: string;
  forkedFromTitle?: string;
  typeLabel?: string;
}) {
  const tooltip = sessionRowTooltip({ title, preview, forkedFromTitle, typeLabel });
  return (
    <TooltipHint content={tooltip}>
      <span className="block truncate">
        {title}
      </span>
    </TooltipHint>
  );
}
