import { useEffect, useState } from "react";

import { cn } from "@/lib/utils";

interface SyntaxToken {
  color?: string;
  content: string;
  fontStyle?: number;
}

export type SnippetLanguage = "html" | "javascript" | "tsx" | "typescript";

const plainLines = (value: string): SyntaxToken[][] =>
  value.split("\n").map((line) => [{ content: line || " " }]);

const tokenStyle = (fontStyle?: number): React.CSSProperties => ({
  fontStyle:
    fontStyle && [1, 3, 5, 7].includes(fontStyle) ? "italic" : undefined,
  fontWeight: fontStyle && [2, 3, 6, 7].includes(fontStyle) ? 700 : undefined,
  textDecoration:
    fontStyle && [4, 5, 6, 7].includes(fontStyle) ? "underline" : undefined,
});

const useDocumentDark = () => {
  const [dark, setDark] = useState(() =>
    document.documentElement.classList.contains("dark")
  );
  useEffect(() => {
    const root = document.documentElement;
    const sync = () => setDark(root.classList.contains("dark"));
    const observer = new MutationObserver(sync);
    observer.observe(root, { attributeFilter: ["class"], attributes: true });
    sync();
    return () => observer.disconnect();
  }, []);
  return dark;
};

export const HighlightedSnippet = ({
  className,
  language,
  value,
}: {
  className?: string;
  language: SnippetLanguage;
  value: string;
}) => {
  const dark = useDocumentDark();
  const [tokens, setTokens] = useState<SyntaxToken[][]>(() =>
    plainLines(value)
  );

  useEffect(() => {
    let active = true;
    const highlight = async () => {
      try {
        const { codeToTokens } = await import("shiki/bundle/web");
        const result = await codeToTokens(value, {
          lang: language,
          theme: dark ? "github-dark-default" : "github-light",
        });
        if (active) {
          setTokens(result.tokens);
        }
      } catch {
        if (active) {
          setTokens(plainLines(value));
        }
      }
    };
    void highlight();
    return () => {
      active = false;
    };
  }, [dark, language, value]);

  return (
    <pre
      className={cn(
        "overflow-auto border border-border bg-muted/20 p-4 pr-24 font-mono text-[10px] leading-5",
        className
      )}
    >
      {tokens.map((line, lineIndex) => (
        <div
          key={`${lineIndex}:${line.map((token) => token.content).join("")}`}
        >
          {line.length === 0 ? (
            <span>{"\n"}</span>
          ) : (
            line.map((token, tokenIndex) => (
              <span
                key={`${tokenIndex}:${token.content}`}
                style={{ color: token.color, ...tokenStyle(token.fontStyle) }}
              >
                {token.content}
              </span>
            ))
          )}
        </div>
      ))}
    </pre>
  );
};
