import { useVirtualizer } from "@tanstack/react-virtual";
import { dynamicIconImports } from "lucide-react/dynamic";
import type { KeyboardEvent, ReactNode } from "react";
import { useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ComboboxClearButton } from "./combobox-clear-button.js";
import { fieldInputClassName } from "./field-input-styles.js";
import { useFloatingPanelPosition, useOutsideClickClose } from "./floating-panel.js";
import { Icon } from "./icon.js";

export interface IconPickerProps {
  id?: string | undefined;
  // Lucide icon name (kebab-case), matching Icon.name.
  value?: string | undefined;
  onChange: (name: string) => void;
  placeholder?: string | undefined;
  disabled?: boolean | undefined;
}

// Fixed, not CSS auto-fill, since the virtualizer needs a JS-known column
// count — sizing math derived in docs/components/icon-picker.md.
const GRID_COLUMNS = 7;
const CELL_SIZE = 40;
const ROW_GAP = 4;

function chunk<T>(items: T[], size: number): T[][] {
  const rows: T[][] = [];
  for (let i = 0; i < items.length; i += size) rows.push(items.slice(i, i + size));
  return rows;
}

// dynamicIconImports is a plain name→loader map that never changes at
// runtime — module scope, not per-instance, so a form with several icon
// fields (or a remount navigating between records) doesn't re-sort the
// same ~2000-entry list each time.
const ALL_ICON_NAMES = Object.keys(dynamicIconImports).sort();

export function IconPicker({ id, value, onChange, placeholder, disabled = false }: IconPickerProps): ReactNode {
  const [query, setQuery] = useState("");
  const [isOpen, setIsOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(0);
  const panelId = useId();

  // Portaled to document.body, position: fixed — same reasoning as
  // CodeSelect's/ActionMenu's own panels: an ancestor with overflow:
  // hidden would otherwise clip the dropdown instead of letting it float
  // above the page.
  const containerRef = useRef<HTMLDivElement | null>(null);
  const panelRef = useRef<HTMLSpanElement | null>(null);
  const scrollElementRef = useRef<HTMLDivElement | null>(null);

  const normalizedQuery = query.trim().toLowerCase();
  const matches = useMemo(() => {
    if (normalizedQuery === "") return ALL_ICON_NAMES;
    return ALL_ICON_NAMES.filter((name) => name.includes(normalizedQuery));
  }, [normalizedQuery]);
  const rows = useMemo(() => chunk(matches, GRID_COLUMNS), [matches]);

  const rowVirtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollElementRef.current,
    estimateSize: () => CELL_SIZE + ROW_GAP,
    overscan: 4,
  });

  const clampedActiveIndex = Math.max(0, Math.min(activeIndex, matches.length - 1));

  function open(): void {
    if (disabled) return;
    setIsOpen(true);
    setActiveIndex(0);
  }

  function close(): void {
    setIsOpen(false);
    setQuery("");
  }

  const position = useFloatingPanelPosition(isOpen, containerRef, panelRef, false);

  // Keeps the keyboard-highlighted cell's row scrolled into view — layout
  // effect, not a passive one, so the row mounts (and aria-activedescendant
  // resolves to a real id) before paint, not after.
  // biome-ignore lint/correctness/useExhaustiveDependencies: rowVirtualizer.scrollToIndex is stable across renders, not a reactive dependency.
  useLayoutEffect(() => {
    if (!isOpen || matches.length === 0) return;
    rowVirtualizer.scrollToIndex(Math.floor(clampedActiveIndex / GRID_COLUMNS), { align: "auto" });
  }, [clampedActiveIndex, isOpen, matches.length, normalizedQuery]);

  useOutsideClickClose(isOpen, [containerRef, panelRef], close);

  function selectName(name: string): void {
    onChange(name);
    close();
  }

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>): void {
    if (event.nativeEvent.isComposing || disabled) return;

    if (!isOpen) {
      if (event.key === "ArrowDown") {
        event.preventDefault();
        open();
      }
      return;
    }

    const row = Math.floor(clampedActiveIndex / GRID_COLUMNS);
    const col = clampedActiveIndex - row * GRID_COLUMNS;
    const rowStart = row * GRID_COLUMNS;
    const rowEnd = Math.min(rowStart + GRID_COLUMNS, matches.length) - 1;
    const lastRow = rows.length - 1;

    switch (event.key) {
      case "ArrowRight":
        event.preventDefault();
        setActiveIndex((i) => Math.min(i + 1, matches.length - 1));
        break;
      case "ArrowLeft":
        event.preventDefault();
        setActiveIndex((i) => Math.max(i - 1, 0));
        break;
      case "ArrowDown":
        event.preventDefault();
        // The next row may be shorter (a partial last row) than the
        // current column offset — clamp into whatever it actually has.
        if (row < lastRow) setActiveIndex(Math.min(rowStart + GRID_COLUMNS + col, matches.length - 1));
        break;
      case "ArrowUp":
        event.preventDefault();
        // Every row above the last is always full, so this index always
        // lands on a real cell without needing to clamp.
        if (row > 0) setActiveIndex(rowStart - GRID_COLUMNS + col);
        break;
      case "Home":
        event.preventDefault();
        setActiveIndex(rowStart);
        break;
      case "End":
        event.preventDefault();
        setActiveIndex(rowEnd);
        break;
      case "Enter": {
        event.preventDefault();
        // An exact typed match wins over the highlighted cell (which
        // resets on every keystroke); a query with zero matches falls
        // back to the raw text, so a legacy/renamed name stays enterable.
        const exact = matches.find((name) => name === normalizedQuery);
        const fallback = matches.length === 0 && query.trim() !== "" ? query.trim() : matches[clampedActiveIndex];
        const name = exact ?? fallback;
        if (name) selectName(name);
        break;
      }
      case "Escape":
        event.preventDefault();
        close();
        break;
      default:
        break;
    }
  }

  const triggerValue = isOpen ? query : (value ?? query);
  const hasOverlayIcon = Boolean(value && !isOpen);

  return (
    <div ref={containerRef} className="relative flex flex-col">
      <input
        id={id}
        type="text"
        role="combobox"
        aria-expanded={isOpen}
        aria-controls={panelId}
        aria-autocomplete="list"
        aria-activedescendant={isOpen && matches.length > 0 ? `${panelId}-cell-${clampedActiveIndex}` : undefined}
        value={triggerValue}
        title={!isOpen && value ? value : undefined}
        disabled={disabled}
        placeholder={placeholder}
        onFocus={open}
        onChange={(event) => {
          setQuery(event.target.value);
          if (!isOpen) open();
          else setActiveIndex(0);
        }}
        onKeyDown={handleKeyDown}
        className={`w-full truncate ${fieldInputClassName(false, "input", "sans")} ${hasOverlayIcon ? "ps-8" : ""} ${
          !isOpen && value ? "pe-8" : ""
        }`}
      />
      {hasOverlayIcon && value && (
        <span aria-hidden className="pointer-events-none absolute inset-0 flex items-center pl-3">
          <Icon name={value} size={16} />
        </span>
      )}
      {!isOpen && value && <ComboboxClearButton label={value} disabled={disabled} onClear={() => onChange("")} />}
      {isOpen &&
        createPortal(
          <span
            ref={panelRef}
            style={
              position
                ? { position: "fixed", top: position.top, left: position.left }
                : { position: "fixed", top: 0, left: 0, visibility: "hidden" }
            }
            className="z-(--z-dropdown) w-80 rounded-structural border border-border bg-surface p-2 shadow-md"
          >
            {matches.length === 0 ? (
              <span id={panelId} aria-live="polite" className="block px-2 py-1 text-sm text-text-secondary">
                No results for &quot;{query}&quot;
              </span>
            ) : (
              // A composite ARIA widget (WAI-ARIA APG Grid pattern), not
              // tabular data — div/role is the correct choice here, not
              // <table>/<tr>/<td>, the same override CodeSelect's own
              // role="option" rows already need for its listbox pattern.
              // biome-ignore lint/a11y/useSemanticElements: see comment above.
              <div ref={scrollElementRef} id={panelId} role="grid" aria-label="Icons" className="h-80 overflow-auto">
                <div style={{ height: rowVirtualizer.getTotalSize(), position: "relative" }}>
                  {rowVirtualizer.getVirtualItems().map((virtualRow) => (
                    // biome-ignore lint/a11y/useSemanticElements: see the role="grid" comment above.
                    // biome-ignore lint/a11y/useFocusableInteractive: a row groups gridcells visually; only the cells themselves are ever virtually focused, via the input's aria-activedescendant.
                    <div
                      key={virtualRow.key}
                      role="row"
                      style={{
                        position: "absolute",
                        top: 0,
                        left: 0,
                        width: "100%",
                        transform: `translateY(${virtualRow.start}px)`,
                        gridTemplateColumns: `repeat(${GRID_COLUMNS}, 1fr)`,
                      }}
                      className="grid gap-1"
                    >
                      {(rows[virtualRow.index] ?? []).map((name, col) => {
                        const flatIndex = virtualRow.index * GRID_COLUMNS + col;
                        return (
                          // Never independently focusable, only virtually
                          // "focused" via the input's aria-activedescendant —
                          // same ARIA APG combobox pattern CodeSelect's own
                          // role="option" rows use.
                          // biome-ignore lint/a11y/useFocusableInteractive: see comment above.
                          // biome-ignore lint/a11y/useKeyWithClickEvents: keyboard selection is handled by the input's own onKeyDown.
                          // biome-ignore lint/a11y/useSemanticElements: see the role="grid" comment above.
                          <div
                            key={name}
                            id={`${panelId}-cell-${flatIndex}`}
                            role="gridcell"
                            aria-label={name}
                            aria-selected={name === value}
                            title={name}
                            onMouseEnter={() => setActiveIndex(flatIndex)}
                            onClick={() => selectName(name)}
                            className={`flex h-10 w-10 cursor-pointer items-center justify-center rounded-control ${
                              flatIndex === clampedActiveIndex ? "bg-surface-hover" : ""
                            } ${name === value ? "ring-2 ring-primary" : ""}`}
                          >
                            <Icon name={name} size={18} />
                          </div>
                        );
                      })}
                    </div>
                  ))}
                </div>
              </div>
            )}
          </span>,
          document.body,
        )}
    </div>
  );
}
