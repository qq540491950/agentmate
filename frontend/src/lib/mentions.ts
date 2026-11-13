/**
 * 输入框的 "@" 引用解析与候选项选择。
 *
 * 标记检测器会感知光标位置："@"
 * 在消息中的任意位置均有效，而不仅限于开头。位于开头的标记还会额外提供
 * Agent/团队/远程项（后端会路由这些项），而每个标记都会提供工作区文件和文件夹。
 * 包含空格的路径采用后端会解析的 @"quoted path" 形式书写。
 */

export type MentionToken = {
  /** 输入中 "@" 的索引。 */
  start: number;
  /** 光标位置；查询在此处结束。 */
  caret: number;
  /** 现有标记文本的结束位置（不含），因此从标记中间选择时会替换该标记。 */
  end: number;
  /** 位于 "@" 与光标之间且已转为小写的文本。 */
  query: string;
  /** 当标记是消息中的第一个内容时为 true（允许路由）。 */
  leading: boolean;
};

export type AssistantCandidate = {
  kind: "agent" | "team" | "remote";
  id: string;
  name: string;
};

export type ContextEntry = { path: string; dir: boolean };

export type MentionCandidate =
  | AssistantCandidate
  | { kind: "file"; path: string }
  | { kind: "directory"; path: string };

/** isAssistantCandidate 将弹窗行收窄为路由条目。 */
export function isAssistantCandidate(c: MentionCandidate): c is AssistantCandidate {
  return c.kind === "agent" || c.kind === "team" || c.kind === "remote";
}

/** 句子标点和空白字符会终止未加引号的路径标记。 */
const TERMINATORS = new Set([
  " ", "\t", "\n", "\r", "\f", "\v",
  "@", '"', "'", ",", ";", ":", "!", "?", "(", ")", "[", "]", "{", "}", "<", ">",
  "，", "。", "；", "：", "！", "？", "、", "）", "】", "》", "」", "』",
]);

function isTerminator(ch: string): boolean {
  return TERMINATORS.has(ch) || /\s/.test(ch);
}

/** "@" 前的 ASCII 单词字符可避免电子邮件/URL 文本被当作提及。 */
function isWordChar(ch: string): boolean {
  return /[A-Za-z0-9_.+-]/.test(ch);
}

function isLeading(text: string, at: number): boolean {
  return text.slice(0, at).trim() === "";
}

/**
 * mentionAtCaret 返回光标所处的 @ 标记；若光标不在任何标记内，则返回
 * null。带引号的路径（"@\"my notes.md\""）在光标位于引号内时保持打开状态。
 */
export function mentionAtCaret(text: string, caret: number): MentionToken | null {
  const pos = Math.max(0, Math.min(caret, text.length));
  if (pos === 0) return null;
  const at = text.lastIndexOf("@", pos - 1);
  if (at < 0) return null;
  if (at > 0 && isWordChar(text[at - 1])) return null;

  if (text[at + 1] === '"') {
    const close = text.indexOf('"', at + 2);
    if (close >= 0 && close + 1 <= pos) return null; // 光标已越过该标记
    if (pos < at + 1) return null;
    const queryEnd = close >= 0 ? Math.min(pos, close) : pos;
    return {
      start: at,
      caret: pos,
      end: close >= 0 ? close + 1 : pos,
      query: text.slice(at + 2, queryEnd).toLowerCase(),
      leading: isLeading(text, at),
    };
  }

  // 未加引号时："@" 与光标之间出现分隔符即表示该标记已结束。
  for (let i = at + 1; i < pos; i++) {
    if (isTerminator(text[i])) return null;
  }
  // "@ " 且光标紧跟 @ 之后时，尚不构成标记。
  if (pos === at + 1 && text[at + 1] !== undefined && isTerminator(text[at + 1])) {
    return null;
  }
  let end = pos;
  while (end < text.length && !isTerminator(text[end])) end++;
  return {
    start: at,
    caret: pos,
    end,
    query: text.slice(at + 1, pos).toLowerCase(),
    leading: isLeading(text, at),
  };
}

/**
 * applyMentionToken
 * 用选中的值替换光标处的标记。文件夹会将光标保留在插入的斜杠之后，以便菜单继续
 * 进入该文件夹；文件/助手则会在末尾添加一个空格。
 */
export function applyMentionToken(
  text: string,
  token: MentionToken,
  value: string,
  opts: { trailingSpace?: boolean } = {},
): { text: string; caret: number } {
  const trailingSpace = opts.trailingSpace ?? true;
  // 空白字符和引号字符需要使用 @"..."
  // 形式；否则后端解析器会在这些字符处结束该标记。
  const quoted = /[\s"']/.test(value);
  const escaped = value.replace(/"/g, "");
  const tokenText = quoted ? `@"${escaped}"` : `@${value}`;
  const after = text.slice(token.end);
  const suffix = trailingSpace && !/^\s/.test(after) ? " " : "";
  const inserted = tokenText + suffix;
  return {
    text: text.slice(0, token.start) + inserted + after,
    caret: token.start + inserted.length,
  };
}

function scoreName(name: string, query: string): number {
  if (query === "") return 1;
  const n = name.toLowerCase();
  if (n.startsWith(query)) return 0;
  if (n.includes(query)) return 2;
  return -1;
}

function scorePath(path: string, query: string): number {
  if (query === "") return 0;
  const p = path.toLowerCase();
  const base = p.slice(p.lastIndexOf("/") + 1);
  if (base.startsWith(query)) return 0;
  if (p.startsWith(query)) return 1;
  if (p.includes("/" + query)) return 2;
  if (p.includes(query)) return 3;
  return -1;
}

/**
 * buildMentionCandidates
 * 对统一弹窗列表排序：当标记位于开头时优先显示助手（后端仅路由位于开头的提及）
 * ，然后是工作区文件夹/文件，其中基名匹配排在路径匹配之前。结果数量设有上限，因
 * 此庞大的工作区绝不会淹没输入框。
 */
export function buildMentionCandidates(
  token: MentionToken,
  assistants: readonly AssistantCandidate[],
  entries: readonly ContextEntry[],
  limit = 10,
): MentionCandidate[] {
  const out: MentionCandidate[] = [];
  const query = token.query.trim();
  if (token.leading) {
    const ranked = assistants
      .map((a) => ({ a, score: scoreName(a.name, query) }))
      .filter((x) => x.score >= 0)
      .sort((x, y) => x.score - y.score || x.a.name.length - y.a.name.length || x.a.name.localeCompare(y.a.name));
    for (const { a } of ranked) {
      if (out.length >= Math.min(5, limit)) break;
      out.push({ kind: a.kind, id: a.id, name: a.name });
    }
  }
  const files = entries
    .map((e) => ({ e, score: scorePath(e.path, query) }))
    .filter((x) => x.score >= 0)
    .sort((x, y) => x.score - y.score || x.e.path.length - y.e.path.length || x.e.path.localeCompare(y.e.path));
  for (const { e } of files) {
    if (out.length >= limit) break;
    out.push({ kind: e.dir ? "directory" : "file", path: e.path });
  }
  return out;
}
