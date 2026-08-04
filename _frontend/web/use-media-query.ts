import { useCallback, useSyncExternalStore } from "react";

const subscribe = (query: string, onStoreChange: () => void) => {
  const media = window.matchMedia(query);
  media.addEventListener("change", onStoreChange);
  return () => media.removeEventListener("change", onStoreChange);
};

export const useMediaQuery = (query: string) => {
  const subscribeQuery = useCallback(
    (onStoreChange: () => void) => subscribe(query, onStoreChange),
    [query]
  );
  return useSyncExternalStore(
    subscribeQuery,
    () => window.matchMedia(query).matches,
    () => false
  );
};
