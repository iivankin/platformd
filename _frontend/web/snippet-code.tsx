import { useEffect, useState, useSyncExternalStore } from "react";

import { cn } from "@/lib/utils";

export interface SyntaxToken {
  color?: string;
  content: string;
  fontStyle?: number;
}

export type SnippetLanguage =
  | "bash"
  | "c"
  | "cpp"
  | "csharp"
  | "css"
  | "go"
  | "html"
  | "java"
  | "javascript"
  | "json"
  | "php"
  | "python"
  | "ruby"
  | "rust"
  | "text"
  | "toml"
  | "tsx"
  | "typescript"
  | "vue"
  | "yaml";

const plainLines = (value: string): SyntaxToken[][] =>
  value.split("\n").map((line) => [{ content: line || " " }]);

export const syntaxTokenStyle = (fontStyle?: number): React.CSSProperties => ({
  fontStyle:
    fontStyle && [1, 3, 5, 7].includes(fontStyle) ? "italic" : undefined,
  fontWeight: fontStyle && [2, 3, 6, 7].includes(fontStyle) ? 700 : undefined,
  textDecoration:
    fontStyle && [4, 5, 6, 7].includes(fontStyle) ? "underline" : undefined,
});

const darkModeListeners = new Set<() => void>();
let darkModeObserver: MutationObserver | undefined;

const documentIsDark = () =>
  typeof document !== "undefined" &&
  document.documentElement.classList.contains("dark");

const subscribeToDarkMode = (listener: () => void) => {
  darkModeListeners.add(listener);
  if (!darkModeObserver && typeof MutationObserver !== "undefined") {
    darkModeObserver = new MutationObserver(() => {
      for (const notify of darkModeListeners) {
        notify();
      }
    });
    darkModeObserver.observe(document.documentElement, {
      attributeFilter: ["class"],
      attributes: true,
    });
  }
  return () => {
    darkModeListeners.delete(listener);
    if (darkModeListeners.size === 0) {
      darkModeObserver?.disconnect();
      darkModeObserver = undefined;
    }
  };
};

const useDocumentDark = () =>
  useSyncExternalStore(subscribeToDarkMode, documentIsDark, () => false);

export const useHighlightedCode = (
  value: string,
  language: SnippetLanguage
) => {
  const dark = useDocumentDark();
  const key = `${dark ? "dark" : "light"}\0${language}\0${value}`;
  const [highlighted, setHighlighted] = useState<{
    key: string;
    tokens: SyntaxToken[][];
  }>();

  useEffect(() => {
    let active = true;
    const highlight = async () => {
      try {
        const { codeToTokens } = await import("shiki/bundle/full");
        const result = await codeToTokens(value, {
          lang: language,
          theme: dark ? "github-dark-default" : "github-light",
        });
        if (active) {
          setHighlighted({ key, tokens: result.tokens });
        }
      } catch {
        if (active) {
          setHighlighted({ key, tokens: plainLines(value) });
        }
      }
    };
    void highlight();
    return () => {
      active = false;
    };
  }, [dark, key, language, value]);

  return highlighted?.key === key ? highlighted.tokens : plainLines(value);
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
  const tokens = useHighlightedCode(value, language);

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
                style={{
                  color: token.color,
                  ...syntaxTokenStyle(token.fontStyle),
                }}
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
