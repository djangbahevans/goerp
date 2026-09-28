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

// The shell's one layout breakpoint: at 768px and wider the chrome has its
// sidebar rail and full header; below it, the bottom navigation bar
// (chrome-sidebar.md "Below 768px").
export const WIDE_VIEWPORT_QUERY = "(min-width: 768px)";

// Without matchMedia this reads as wide, the layout jsdom tests exercise by default.
export function useWideViewport(): boolean {
  return useMediaQuery(WIDE_VIEWPORT_QUERY, true);
}
