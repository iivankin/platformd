import { cn } from "@/lib/utils";
import { syntaxTokenStyle, useHighlightedCode } from "@/snippet-code";
import type { SnippetLanguage } from "@/snippet-code";

interface SourceLine {
  active: boolean;
  number?: number;
  text: string;
}

export const sourceLanguage = (filename?: string): SnippetLanguage => {
  const extension = filename
    ?.split(/[?#]/u)[0]
    ?.split(".")
    .at(-1)
    ?.toLowerCase();
  const languages: Record<string, SnippetLanguage> = {
    bash: "bash",
    c: "c",
    cc: "cpp",
    cpp: "cpp",
    cs: "csharp",
    css: "css",
    go: "go",
    h: "c",
    hpp: "cpp",
    java: "java",
    js: "javascript",
    json: "json",
    jsx: "javascript",
    mjs: "javascript",
    php: "php",
    py: "python",
    rb: "ruby",
    rs: "rust",
    sh: "bash",
    toml: "toml",
    ts: "typescript",
    tsx: "tsx",
    vue: "vue",
    yaml: "yaml",
    yml: "yaml",
  };
  return languages[extension ?? ""] ?? "text";
};

export const SyntaxSource = ({
  filename,
  lines,
}: {
  filename?: string;
  lines: SourceLine[];
}) => {
  const tokens = useHighlightedCode(
    lines.map((line) => line.text).join("\n"),
    sourceLanguage(filename)
  );

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
