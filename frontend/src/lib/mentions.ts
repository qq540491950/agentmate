/** Composer "@" reference parsing and candidate selection.
 *
 * The token detector is caret-aware: "@" works anywhere in the message, not
 * only at the start. Leading tokens additionally offer agents/teams/remotes
 * (the backend routes those), while every token offers workspace files and
 * folders. Paths containing spaces are written in the @"quoted path" form the
 * backend parses.
 */

export type MentionToken = {
  /** Index of the "@" in the input. */
  start: number;
  /** Caret position; the query ends here. */
  caret: number;
  /** End of the existing token text (exclusive), so a mid-token pick replaces it. */
  end: number;
  /** Lowercased text between "@" and the caret. */
  query: string;
  /** True when the token is the first thing in the message (routing allowed). */
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

/** isAssistantCandidate narrows a popup row to the routing entries. */
export function isAssistantCandidate(c: MentionCandidate): c is AssistantCandidate {
  return c.kind === "agent" || c.kind === "team" || c.kind === "remote";
}

/** Sentence punctuation and whitespace end an unquoted path token. */
const TERMINATORS = new Set([
  " ", "\t", "\n", "\r", "\f", "\v",
  "@", '"', "'", ",", ";", ":", "!", "?", "(", ")", "[", "]", "{", "}", "<", ">",
  "，", "。", "；", "：", "！", "？", "、", "）", "】", "》", "」", "』",
]);

function isTerminator(ch: string): boolean {
  return TERMINATORS.has(ch) || /\s/.test(ch);
}

/** ASCII word characters before "@" keep e-mail/URL text from becoming claims. */
function isWordChar(ch: string): boolean {
  return /[A-Za-z0-9_.+-]/.test(ch);
}

function isLeading(text: string, at: number): boolean {
  return text.slice(0, at).trim() === "";
}

/**
 * mentionAtCaret returns the @ token the caret is inside, or null. Quoted
 * paths ("@\"my notes.md\"") stay open while the caret is inside the quotes.
 */
export function mentionAtCaret(text: string, caret: number): MentionToken | null {
  const pos = Math.max(0, Math.min(caret, text.length));
  if (pos === 0) return null;
  const at = text.lastIndexOf("@", pos - 1);
  if (at < 0) return null;
  if (at > 0 && isWordChar(text[at - 1])) return null;

  if (text[at + 1] === '"') {
    const close = text.indexOf('"', at + 2);
    if (close >= 0 && close + 1 <= pos) return null; // caret is past the token
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

  // Unquoted: a separator between "@" and the caret means the token is over.
  for (let i = at + 1; i < pos; i++) {
    if (isTerminator(text[i])) return null;
  }
  // "@ " with the caret right after the @ is not a token yet.
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
 * applyMentionToken replaces the token under the caret with the picked value.
 * Folders keep the caret right after the inserted slash so the menu continues
 * into the folder; files/assistants get one trailing space.
 */
export function applyMentionToken(
  text: string,
  token: MentionToken,
  value: string,
  opts: { trailingSpace?: boolean } = {},
): { text: string; caret: number } {
  const trailingSpace = opts.trailingSpace ?? true;
  // Whitespace and quote characters need the @"..." form; the backend parser
  // otherwise ends the token at them.
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
 * buildMentionCandidates ranks the unified popup list: assistants first while
 * the token is leading (the backend only routes a leading mention), then
 * workspace folders/files with basename matches before path matches. The
 * result is capped so a huge workspace never floods the composer.
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
