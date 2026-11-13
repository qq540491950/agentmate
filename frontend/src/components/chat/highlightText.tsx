import type { ReactNode } from "react";

// highlightText 会渲染文本，并标记出 query
// 中不区分大小写的所有出现位置。仅用于纯文本渲染路径（用户气泡、错误、工具
// JSON）；助手 markdown 有意不处理。query
// 为空时会原样返回文本，因此各调用点保持无条件调用。
export function highlightText(text: string, query: string, markClass: string): ReactNode {
  if (!query || !text) return text;
  const lower = text.toLowerCase();
  const q = query.toLowerCase();
  const out: ReactNode[] = [];
  let i = 0;
  let key = 0;
  while (i < text.length) {
    const hit = lower.indexOf(q, i);
    if (hit < 0) {
      out.push(text.slice(i));
      break;
    }
    if (hit > i) out.push(text.slice(i, hit));
    out.push(<mark key={key++} className={markClass}>{text.slice(hit, hit + q.length)}</mark>);
    i = hit + q.length;
  }
  return out;
}
