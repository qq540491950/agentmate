import { GitFork } from "lucide-react";

/**
 * SessionRowLabel renders the sidebar's one-line session title. The latest
 * assistant reply stays available as a native hover tooltip instead of a
 * second visual line, so long conversations do not make every row bulky.
 */
export function SessionRowLabel({
  title,
  preview = "",
  forkedFromTitle = "",
}: {
  title: string;
  preview?: string;
  forkedFromTitle?: string;
}) {
  const tooltip = [
    forkedFromTitle ? `分叉自「${forkedFromTitle}」` : "",
    preview,
  ].filter(Boolean).join("\n") || title;
  return (
    <span className="block truncate" title={tooltip}>
      {forkedFromTitle && <GitFork className="mr-1 inline h-3 w-3 text-muted-foreground" />}
      {title}
    </span>
  );
}
