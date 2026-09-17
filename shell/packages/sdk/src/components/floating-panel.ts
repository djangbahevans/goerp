import type { RefObject } from "react";
import { useEffect, useLayoutEffect, useState } from "react";

export interface FloatingPanelPosition {
  top: number;
  left: number;
  width: number;
}

// Shared positioning mechanics behind every anchored combobox/grid panel in
// this library (CodeSelect, IconPicker) — floats a portaled panel below its
// trigger, flipping above when it wouldn't fit, same reasoning ActionMenu's
// own panel already establishes.
export function useFloatingPanelPosition(
  isOpen: boolean,
  containerRef: RefObject<HTMLElement | null>,
  panelRef: RefObject<HTMLElement | null>,
): FloatingPanelPosition | null {
  const [position, setPosition] = useState<FloatingPanelPosition | null>(null);

  // Runs before paint, positioned once on open — not re-tracked on
  // scroll/resize, since the panel closes on Escape/selection/outside-click
  // well before either would matter.
  // biome-ignore lint/correctness/useExhaustiveDependencies: containerRef/panelRef are stable ref objects, not reactive values — only isOpen should retrigger this.
  useLayoutEffect(() => {
    if (!isOpen) {
      setPosition(null);
      return;
    }
    const containerEl = containerRef.current;
    if (!containerEl) return;
    const containerRect = containerEl.getBoundingClientRect();
    const panelHeight = panelRef.current?.getBoundingClientRect().height ?? 0;
    const fitsBelow = containerRect.bottom + 4 + panelHeight <= window.innerHeight - 8;
    const top = fitsBelow ? containerRect.bottom + 4 : Math.max(8, containerRect.top - 4 - panelHeight);
    setPosition({ top, left: containerRect.left, width: containerRect.width });
  }, [isOpen]);

  return position;
}

// Closes on a click outside every ref in `boundaryRefs` — mousedown (not
// click) so it commits first; a portaled panel isn't a DOM descendant of
// its trigger, so blur/relatedTarget can't be used instead.
export function useOutsideClickClose(
  isOpen: boolean,
  boundaryRefs: RefObject<HTMLElement | null>[],
  close: () => void,
): void {
  // biome-ignore lint/correctness/useExhaustiveDependencies: close is a plain function recreated every render, not a reactive dependency — only isOpen should re-arm this listener.
  useEffect(() => {
    if (!isOpen) return;
    function handlePointerDown(event: MouseEvent): void {
      const target = event.target as Node;
      if (boundaryRefs.some((ref) => ref.current?.contains(target))) return;
      close();
    }
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [isOpen]);
}
