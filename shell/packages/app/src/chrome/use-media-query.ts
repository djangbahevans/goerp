import { useCallback, useSyncExternalStore } from "react";

function supportsMatchMedia(): boolean {
  return typeof window !== "undefined" && typeof window.matchMedia === "function";
}

// Without matchMedia (jsdom, non-browser renders) the query reads as
// `fallback`, so a caller picks which layout those environments get.
export function useMediaQuery(query: string, fallback: boolean): boolean {
  const subscribe = useCallback(
    (onChange: () => void) => {
      if (!supportsMatchMedia()) return () => {};
      const list = window.matchMedia(query);
      list.addEventListener("change", onChange);
      return () => list.removeEventListener("change", onChange);
    },
    [query],
  );
  const getSnapshot = () => (supportsMatchMedia() ? window.matchMedia(query).matches : fallback);
  return useSyncExternalStore(subscribe, getSnapshot, () => fallback);
}
