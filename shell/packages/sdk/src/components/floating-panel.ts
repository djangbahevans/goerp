import type { RefObject } from "react";
import { useEffect, useLayoutEffect, useRef, useState } from "react";

export interface FloatingPanelPosition {
  top: number;
  left: number;
  width: number;
}

// Shared positioning mechanics behind every anchored combobox/menu/grid
// panel in this library (Combobox, IconPicker, ActionMenu, SelectMultiple)
// — floats a portaled panel below its trigger, flipping
// above when it wouldn't fit, clamping left so the panel can't overflow the
// viewport's right edge either.
export function useFloatingPanelPosition(
  isOpen: boolean,
  containerRef: RefObject<HTMLElement | null>,
  panelRef: RefObject<HTMLElement | null>,
  // Whether the caller pins the panel's own width to its trigger's width
  // (Combobox does, via this hook's own `width` return
  // value) — if so, this first layout pass measures the panel *before* that
  // width style has been applied (it renders hidden/unstyled for one frame
  // first), so its natural shrink-to-fit measurement can't be trusted as
  // the eventual rendered width for the left-clamp below; containerRect's
  // width is used instead, floored against the natural measurement in case
  // a CSS min-width on the panel would win out over a narrower trigger.
  matchTriggerWidth: boolean,
): FloatingPanelPosition | null {
  const [position, setPosition] = useState<FloatingPanelPosition | null>(null);

  function measure(): void {
    const containerEl = containerRef.current;
    if (!containerEl) return;
    const containerRect = containerEl.getBoundingClientRect();
    const panelRect = panelRef.current?.getBoundingClientRect();
    const panelHeight = panelRect?.height ?? 0;
    const naturalPanelWidth = panelRect?.width ?? containerRect.width;
    const panelWidth = matchTriggerWidth ? Math.max(containerRect.width, naturalPanelWidth) : naturalPanelWidth;
    const fitsBelow = containerRect.bottom + 4 + panelHeight <= window.innerHeight - 8;
    const top = fitsBelow ? containerRect.bottom + 4 : Math.max(8, containerRect.top - 4 - panelHeight);
    const maxLeft = window.innerWidth - panelWidth - 8;
    const left = Math.max(8, Math.min(containerRect.left, maxLeft));
    const width = containerRect.width;
    setPosition((prev) =>
      prev && prev.top === top && prev.left === left && prev.width === width ? prev : { top, left, width },
    );
  }

  // Runs before paint on open. Scroll and window resize aren't tracked,
  // since the panel closes on Escape/selection/outside-click well before
  // either would matter.
  // biome-ignore lint/correctness/useExhaustiveDependencies: containerRef/panelRef/matchTriggerWidth are stable across a given caller's lifetime, not reactive values — only isOpen should retrigger this.
  useLayoutEffect(() => {
    if (!isOpen) {
      setPosition(null);
      return;
    }
    measure();
  }, [isOpen]);

  // The trigger itself can change size while open — a multi-select's pill
  // row grows with each pick and pushes the input down — so the panel is
  // re-placed to stay below it.
  // biome-ignore lint/correctness/useExhaustiveDependencies: same stable refs as above.
  useEffect(() => {
    const containerEl = containerRef.current;
    if (!isOpen || !containerEl || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(() => measure());
    observer.observe(containerEl);
    return () => observer.disconnect();
  }, [isOpen]);

  return position;
}

const MODAL_SELECTOR = '[role="dialog"], [role="alertdialog"]';

// The z-index class for a panel portaled onto document.body. --z-dropdown
// sits below --z-modal, so a panel whose trigger lives inside a modal
// (AlertDialog, BulkActionPanel, SideSheet — every one a Radix dialog)
// would open behind that modal's own overlay. Such a panel takes --z-modal
// instead: the modal's own layer, painted above it because the panel's
// portal is appended to document.body after the modal's.
export function useFloatingPanelLayer(anchorRef: RefObject<HTMLElement | null>): string {
  const [inModal, setInModal] = useState(false);

  // A trigger never moves in or out of a modal over its lifetime, so this
  // is read once on mount, before the first paint. If a future trigger is
  // reparented into a dialog without remounting, it'll stay stuck on
  // --z-dropdown — give it a `key` change instead so it mounts fresh.
  // biome-ignore lint/correctness/useExhaustiveDependencies: anchorRef is a stable ref, not a reactive value.
  useLayoutEffect(() => {
    setInModal(anchorRef.current?.closest(MODAL_SELECTOR) != null);
  }, []);

  return inModal ? "z-(--z-modal)" : "z-(--z-dropdown)";
}

// Closes on a click outside every ref in `boundaryRefs` — mousedown (not
// click) so it commits first; a portaled panel isn't a DOM descendant of
// its trigger, so blur/relatedTarget can't be used instead.
export function useOutsideClickClose(
  isOpen: boolean,
  boundaryRefs: RefObject<HTMLElement | null>[],
  close: () => void,
): void {
  // The listener outlives renders, so it calls the latest close, which may
  // read state (a typed query) that changed after the panel opened.
  const closeRef = useRef(close);
  closeRef.current = close;
  // biome-ignore lint/correctness/useExhaustiveDependencies: boundaryRefs are stable refs; only isOpen should re-arm this listener.
  useEffect(() => {
    if (!isOpen) return;
    function handlePointerDown(event: MouseEvent): void {
      const target = event.target as Node;
      if (boundaryRefs.some((ref) => ref.current?.contains(target))) return;
      closeRef.current();
    }
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [isOpen]);
}

export function optionElementId(listboxId: string, index: number): string {
  return `${listboxId}-option-${index}`;
}

// Keeps a virtually-highlighted option (aria-activedescendant, not real DOM
// focus) scrolled into view within a panel capped by max-height/overflow-y.
// visibleCount is a deliberate extra dependency, not read in the body — a
// filter/search keystroke can change what's rendered at the same
// activeIndex without activeIndex's own value changing, which would
// otherwise leave the panel's scroll position stale.
export function useScrollHighlightedOptionIntoView(
  isOpen: boolean,
  listboxId: string,
  activeIndex: number,
  visibleCount: number,
): void {
  // biome-ignore lint/correctness/useExhaustiveDependencies: visibleCount is intentionally over-specified, see above.
  useEffect(() => {
    if (!isOpen) return;
    document.getElementById(optionElementId(listboxId, activeIndex))?.scrollIntoView({ block: "nearest" });
  }, [isOpen, activeIndex, listboxId, visibleCount]);
}
