import { describe, expect, it } from "vitest";
import {
  applyMentionToken,
  buildMentionCandidates,
  mentionAtCaret,
  type AssistantCandidate,
  type ContextEntry,
} from "./mentions";

const ASSISTANTS: AssistantCandidate[] = [
  { kind: "agent", id: "a1", name: "写手" },
  { kind: "team", id: "t1", name: "写作团队" },
  { kind: "remote", id: "r1", name: "远端助手" },
];

const ENTRIES: ContextEntry[] = [
  { path: "src", dir: true },
  { path: "src/main.go", dir: false },
  { path: "docs", dir: true },
  { path: "docs/notes.md", dir: false },
  { path: "my notes.txt", dir: false },
  { path: "README.md", dir: false },
];

describe("mentionAtCaret", () => {
  it("opens on a leading @ and keeps the query lowercased", () => {
    expect(mentionAtCaret("@Wri", 4)).toEqual({
      start: 0, caret: 4, end: 4, query: "wri", leading: true,
    });
  });

  it("opens mid-message, including directly after CJK text", () => {
    const text = "请参考@notes.md";
    const token = mentionAtCaret(text, text.length);
    expect(token?.query).toBe("notes.md");
    expect(token?.leading).toBe(false);
    expect(token?.start).toBe(3);
    expect(token?.end).toBe(text.length);
  });

  it("does not open for e-mail text", () => {
    expect(mentionAtCaret("mail a@b.com", 12)).toBeNull();
  });

  it("closes after the token ends", () => {
    const text = "@notes.md 继续";
    expect(mentionAtCaret(text, text.length)).toBeNull();
  });

  it("supports quoted paths with spaces", () => {
    const text = '看 @"my notes';
    const token = mentionAtCaret(text, text.length);
    expect(token?.query).toBe("my notes");
    expect(token?.start).toBe(2);
    expect(token?.end).toBe(text.length);

    const closed = '看 @"my notes.txt" 继续';
    expect(mentionAtCaret(closed, closed.indexOf("继续"))).toBeNull();
    expect(mentionAtCaret(closed, closed.indexOf("notes") + "notes".length)?.query).toBe("my notes");
  });
});

describe("applyMentionToken", () => {
  it("replaces the whole token and leaves the caret after the inserted value", () => {
    const text = "请参考@not 谢谢";
    const token = mentionAtCaret(text, text.indexOf("@") + 4)!;
    const out = applyMentionToken(text, token, "notes.md");
    expect(out.text).toBe("请参考@notes.md 谢谢");
    expect(out.caret).toBe("请参考@notes.md".length);
  });

  it("quotes paths with spaces", () => {
    const text = "@my";
    const out = applyMentionToken(text, mentionAtCaret(text, 3)!, "my notes.txt");
    expect(out.text).toBe('@"my notes.txt" ');
    expect(out.caret).toBe('@"my notes.txt" '.length);
  });

  it("quotes apostrophes so the backend token does not end early", () => {
    const text = "@it";
    const out = applyMentionToken(text, mentionAtCaret(text, 3)!, "it's.md");
    expect(out.text).toBe(`@"it's.md" `);
  });

  it("keeps folders open-ended for continued navigation", () => {
    const text = "@sr";
    const out = applyMentionToken(text, mentionAtCaret(text, 3)!, "src/", { trailingSpace: false });
    expect(out.text).toBe("@src/");
    expect(out.caret).toBe(5);
  });
});

describe("buildMentionCandidates", () => {
  it("offers assistants only for a leading token", () => {
    const leading = mentionAtCaret("@写", 2)!;
    const kinds = buildMentionCandidates(leading, ASSISTANTS, ENTRIES).map((c) => c.kind);
    expect(kinds.slice(0, 1)).toEqual(["agent"]);

    const text = "看看 @写";
    const mid = mentionAtCaret(text, text.length)!;
    expect(buildMentionCandidates(mid, ASSISTANTS, ENTRIES).some((c) => c.kind === "agent")).toBe(false);
  });

  it("ranks basename matches before path matches and includes folders", () => {
    const token = mentionAtCaret("@notes", 6)!;
    const paths = buildMentionCandidates(token, [], ENTRIES)
      .map((c) => (c.kind === "file" || c.kind === "directory" ? c.path : ""));
    expect(paths[0]).toBe("docs/notes.md");
    expect(paths).not.toContain("README.md");
  });

  it("lists short paths first when the query is empty", () => {
    const token = mentionAtCaret("@", 1)!;
    const paths = buildMentionCandidates(token, ASSISTANTS, ENTRIES, 4)
      .map((c) => (c.kind === "file" || c.kind === "directory" ? c.path : c.name));
    // Three assistants + one shortest path: the leading group wins first.
    expect(paths.slice(0, 3)).toEqual(["写手", "写作团队", "远端助手"]);
    expect(paths[3]).toBe("src");
  });

  it("caps the list", () => {
    const token = mentionAtCaret("@", 1)!;
    expect(buildMentionCandidates(token, ASSISTANTS, ENTRIES, 2)).toHaveLength(2);
  });
});
