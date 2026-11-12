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
 * SessionRowLabel renders the sidebar's one-line session title. The latest
 * assistant reply stays available as a shared hover card instead of a
 * second visual line, so long conversations do not make every row bulky.
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
