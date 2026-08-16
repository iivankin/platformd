import { useEffect, useState } from "react";

import { cn } from "@/lib/utils";

interface SourceLine {
  active: boolean;
  number?: number;
  text: string;
}

interface SyntaxToken {
  color?: string;
  content: string;
  fontStyle?: number;
}

type SourceLanguage =
  | "bash"
  | "c"
  | "cpp"
  | "css"
  | "java"
  | "javascript"
  | "json"
  | "python"
  | "text"
  | "tsx"
  | "typescript"
  | "vue";

export const sourceLanguage = (filename?: string): SourceLanguage => {
  const extension = filename
    ?.split(/[?#]/u)[0]
    ?.split(".")
    .at(-1)
    ?.toLowerCase();
  const languages: Record<string, SourceLanguage> = {
    bash: "bash",
    c: "c",
    cc: "cpp",
    cpp: "cpp",
    css: "css",
    h: "c",
    hpp: "cpp",
    java: "java",
    js: "javascript",
    json: "json",
    jsx: "javascript",
    mjs: "javascript",
    py: "python",
    sh: "bash",
    ts: "typescript",
    tsx: "tsx",
    vue: "vue",
  };
  return languages[extension ?? ""] ?? "text";
};

const plainTokens = (lines: SourceLine[]): SyntaxToken[][] =>
  lines.map((line) => [{ content: line.text || " " }]);

const syntaxTokenStyle = (fontStyle?: number): React.CSSProperties => ({
  fontStyle:
    fontStyle && [1, 3, 5, 7].includes(fontStyle) ? "italic" : undefined,
  fontWeight: fontStyle && [2, 3, 6, 7].includes(fontStyle) ? 700 : undefined,
  textDecoration:
    fontStyle && [4, 5, 6, 7].includes(fontStyle) ? "underline" : undefined,
});

export const SyntaxSource = ({
  filename,
  lines,
}: {
  filename?: string;
  lines: SourceLine[];
}) => {
  const [tokens, setTokens] = useState<SyntaxToken[][]>(() =>
    plainTokens(lines)
  );

  useEffect(() => {
    let active = true;
    const highlight = async () => {
      try {
        const { codeToTokens } = await import("shiki/bundle/web");
        const result = await codeToTokens(
          lines.map((line) => line.text).join("\n"),
          {
            lang: sourceLanguage(filename),
            theme: "github-dark-default",
          }
        );
        if (active) {
          setTokens(result.tokens);
        }
      } catch {
        if (active) {
          setTokens(plainTokens(lines));
        }
      }
    };
    void highlight();
    return () => {
      active = false;
    };
  }, [filename, lines]);

  return (
    <div className="overflow-x-auto border-y border-border bg-background">
      <pre className="w-max min-w-full py-1 text-[9px] leading-5">
        {lines.map((line, index) => (
          <span
            className={cn(
              "grid grid-cols-[4.5rem_minmax(max-content,1fr)] border-l-2 pr-4",
              line.active
                ? "border-l-destructive bg-destructive/10 text-foreground"
                : "border-l-transparent text-muted-foreground"
            )}
            key={`${line.number ?? "unknown"}:${index}`}
          >
            <span className="border-r border-border px-3 text-right text-foreground/40 select-none">
              {line.number ?? "—"}
            </span>
            <code className="pl-4">
              {(tokens[index] ?? [{ content: line.text || " " }]).map(
                (token, tokenIndex) => (
                  <span
                    key={`${tokenIndex}:${token.content}`}
                    style={{
                      color: token.color,
                      ...syntaxTokenStyle(token.fontStyle),
                    }}
                  >
                    {token.content}
                  </span>
                )
              )}
            </code>
          </span>
        ))}
      </pre>
    </div>
  );
};
