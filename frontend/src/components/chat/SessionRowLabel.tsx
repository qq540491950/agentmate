/**
 * SessionRowLabel renders the sidebar's one-line session title. The latest
 * assistant reply stays available as a native hover tooltip instead of a
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
  const tooltip = [
    typeLabel,
    forkedFromTitle ? `分叉自「${forkedFromTitle}」` : "",
    preview,
  ].filter(Boolean).join("\n") || title;
  return (
    <span className="block truncate" title={tooltip}>
      {title}
    </span>
  );
}
