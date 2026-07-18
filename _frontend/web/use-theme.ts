import { useCallback, useEffect, useSyncExternalStore } from "react";

export type Theme = "light" | "dark" | "system";

const storageKey = "platformd-theme";
const listeners = new Set<() => void>();

const storedTheme = (): Theme => {
  try {
    const stored = localStorage.getItem(storageKey);
    return stored === "light" || stored === "dark" ? stored : "system";
  } catch {
    return "system";
  }
};

const applyTheme = (theme: Theme) => {
  const dark =
    theme === "dark" ||
    (theme === "system" &&
      window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
  document
    .querySelector('meta[name="color-scheme"]')
    ?.setAttribute("content", dark ? "dark" : "light");
  document
    .querySelector('meta[name="theme-color"]')
    ?.setAttribute(
      "content",
      getComputedStyle(document.documentElement)
        .getPropertyValue("--background")
        .trim()
    );
};

const emitChange = () => {
  for (const listener of listeners) {
    listener();
  }
};

const subscribe = (listener: () => void) => {
  listeners.add(listener);
  const handleStorage = (event: StorageEvent) => {
    if (event.key === storageKey) {
      applyTheme(storedTheme());
      listener();
    }
  };
  window.addEventListener("storage", handleStorage);
  return () => {
    listeners.delete(listener);
    window.removeEventListener("storage", handleStorage);
  };
};

export const initializeTheme = () => applyTheme(storedTheme());

export const useTheme = () => {
  const theme = useSyncExternalStore(
    subscribe,
    storedTheme,
    () => "system" as Theme
  );

  const setTheme = useCallback((next: Theme) => {
    try {
      if (next === "system") {
        localStorage.removeItem(storageKey);
      } else {
        localStorage.setItem(storageKey, next);
      }
    } catch {
      // The active document can still switch themes when storage is blocked.
    }
    applyTheme(next);
    emitChange();
  }, []);

  useEffect(() => {
    applyTheme(theme);
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const handleSystemThemeChange = () => {
      if (storedTheme() === "system") {
        applyTheme("system");
      }
    };
    media.addEventListener("change", handleSystemThemeChange);
    return () => media.removeEventListener("change", handleSystemThemeChange);
  }, [theme]);

  return { setTheme, theme } as const;
};
