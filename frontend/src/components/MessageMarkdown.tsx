import { memo, useMemo, useState } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Check, Copy } from "lucide-react";
import { cn } from "@/lib/utils";

/* ---------------------------------------------------------------------------
 * Zero-dependency syntax highlighting for fenced code blocks. A combined
 * regex tokenizes comments / strings / numbers / identifiers; identifiers are
 * checked against a shared keyword set. Display-only — the merged text is
 * always the original code, so a tokenizer miss never corrupts output.
 * ------------------------------------------------------------------------- */

const KEYWORDS = new Set([
  // js / ts
  "const", "let", "var", "function", "return", "if", "else", "for", "while", "switch", "case",
  "break", "continue", "new", "class", "extends", "import", "from", "export", "default", "await",
  "async", "try", "catch", "finally", "throw", "typeof", "instanceof", "in", "of", "this",
  "super", "null", "true", "false", "undefined", "void", "delete", "interface", "type", "enum",
  "implements", "public", "private", "protected", "static", "readonly", "as", "satisfies",
  // go
  "func", "go", "defer", "chan", "select", "map", "struct", "range", "package", "fallthrough",
  // python
  "def", "self", "None", "True", "False", "elif", "lambda", "pass", "with", "as", "yield",
  "raise", "not", "and", "or", "is", "elif",
  // rust
  "fn", "impl", "pub", "use", "mut", "match", "where", "unsafe", "trait", "dyn",
  // shell
  "echo", "local", "set", "then", "fi", "esac", "do", "done", "end",
]);

const SUPPORTED_LANGS = new Set([
  "js", "jsx", "ts", "tsx", "javascript", "typescript", "go", "python", "py", "sh", "bash",
  "zsh", "shell", "json", "yaml", "yml", "toml", "sql", "rust", "rs", "java", "c", "cpp",
]);

type Token = { text: string; kind?: "kw" | "str" | "num" | "com" };

const TOKEN_RE = /(\/\*[\s\S]*?\*\/|\/\/[^\n]*|#[^\n]*)|("(?:\\.|[^"\\\n])*"|'(?:\\.|[^'\\\n])*'|`(?:\\.|[^`\\])*`)|(\b\d[\w.]*\b)|([A-Za-z_$][\w$]*)/g;

function tokenize(code: string): Token[] {
  const out: Token[] = [];
  let last = 0;
  for (const m of code.matchAll(TOKEN_RE)) {
    const [, com, str, num, ident] = m;
    if (m.index > last) out.push({ text: code.slice(last, m.index) });
    if (com) out.push({ text: com, kind: "com" });
    else if (str) out.push({ text: str, kind: "str" });
    else if (num) out.push({ text: num, kind: "num" });
    else if (ident && KEYWORDS.has(ident)) out.push({ text: ident, kind: "kw" });
    last = m.index + m[0].length;
    if (out.length > 4000) break; // safety valve for pathological input
  }
  if (last < code.length) out.push({ text: code.slice(last) });
  return out;
}

function HighlightedCode({ code, lang }: { code: string; lang?: string }) {
  const enabled = SUPPORTED_LANGS.has((lang ?? "").toLowerCase());
  const tokens = useMemo(() => (enabled ? tokenize(code) : null), [code, enabled]);
  if (!tokens) return <>{code}</>;
  return (
    <>
      {tokens.map((t, i) =>
        t.kind ? <span key={i} className={`hl-${t.kind}`}>{t.text}</span> : t.text,
      )}
    </>
  );
}

function CopyButton({ text, className, iconOnly = false, title }: {
  text: string;
  className?: string;
  iconOnly?: boolean;
  title?: string;
}) {
  const [copied, setCopied] = useState(false);
  const label = copied ? "已复制" : "复制";
  return (
    <button
      className={cn(
        "inline-flex items-center gap-1 rounded-md bg-panel/80 px-1.5 py-0.5 text-2xs text-muted-foreground transition-colors hover:bg-accent hover:text-foreground",
        className,
      )}
      title={title ?? label}
      aria-label={title ?? label}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(text);
        } catch {
          // Clipboard API may be unavailable; fall back silently.
          const ta = document.createElement("textarea");
          ta.value = text;
          document.body.appendChild(ta);
          ta.select();
          document.execCommand("copy");
          ta.remove();
        }
        setCopied(true);
        setTimeout(() => setCopied(false), 1500);
      }}
    >
      {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
      {!iconOnly && label}
    </button>
  );
}

// `node` is react-markdown's hast element — spreading it onto the DOM trips
// React's unknown-prop warning on every code fragment, so it is dropped.
function CodeBlock({ children, className, node, ...rest }: any) {
  const raw = typeof children === "string" ? children : "";
  const match = /language-(\w+)/.exec(className || "");
  const isBlock = String(className || "").includes("language-") || String(raw).includes("\n");
  if (!isBlock) {
    return (
      <code className="rounded bg-muted px-1 py-0.5 font-mono text-[0.85em]" {...rest}>
        {children}
      </code>
    );
  }
  return (
    <div className="group/code relative my-3 overflow-hidden rounded-lg bg-panel/60">
      <div className="flex items-center justify-between px-3 py-1.5">
        <span className="font-mono text-2xs text-muted-foreground">{match?.[1] ?? "text"}</span>
        <CopyButton text={raw} className="border-0 bg-transparent opacity-0 transition-opacity group-hover/code:opacity-100" />
      </div>
      <pre className="overflow-x-auto px-3 pb-3 pt-0.5 text-xs leading-[1.7]">
        <code className="font-mono" {...rest}>
          {raw ? <HighlightedCode code={raw} lang={match?.[1]} /> : children}
        </code>
      </pre>
    </div>
  );
}

/** MessageMarkdown renders assistant text as GitHub-flavored Markdown. */
export const MessageMarkdown = memo(function MessageMarkdown({ content, className }: { content: string; className?: string }) {
  return (
    <div className={cn("message-md space-y-2 break-words text-md leading-[1.7]", className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          code: CodeBlock,
          pre: ({ children }: any) => <>{children}</>,
          a: ({ href, children }: any) => (
            <a href={href} target="_blank" rel="noreferrer" className="text-accent-text underline underline-offset-2">
              {children}
            </a>
          ),
          ul: ({ children }: any) => <ul className="ml-4 list-disc space-y-1">{children}</ul>,
          ol: ({ children }: any) => <ol className="ml-4 list-decimal space-y-1">{children}</ol>,
          h1: ({ children }: any) => <h1 className="mt-4 text-lg font-semibold">{children}</h1>,
          h2: ({ children }: any) => <h2 className="mt-4 text-base font-semibold">{children}</h2>,
          h3: ({ children }: any) => <h3 className="mt-3 text-md font-semibold">{children}</h3>,
          blockquote: ({ children }: any) => (
            <blockquote className="border-l-2 border-border/60 pl-3 text-muted-foreground">{children}</blockquote>
          ),
          table: ({ children }: any) => (
            <div className="my-3 overflow-x-auto rounded-lg border border-border">
              <table className="w-full text-xs">{children}</table>
            </div>
          ),
          th: ({ children }: any) => <th className="border-b border-border bg-muted/40 px-2.5 py-1.5 text-left font-medium">{children}</th>,
          td: ({ children }: any) => <td className="border-b border-border px-2.5 py-1.5">{children}</td>,
        }}
      >
        {content}
      </ReactMarkdown>
    </div>
  );
});

export { CopyButton };
