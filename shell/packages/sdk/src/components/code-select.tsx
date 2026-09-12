import type { CSSProperties, KeyboardEvent, ReactNode } from "react";
import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { fieldInputClassName } from "./field-input-styles.js";

// Shared combobox mechanics behind country-select.tsx and
// language-select.tsx — same searchable-list-over-a-static-dataset shape,
// differing only in dataset, name resolution, and row visual.

export interface CodeSelectProps {
  id?: string | undefined;
  codes: string[];
  nameOf: (code: string) => string;
  renderRow: (code: string, name: string) => ReactNode;
  // Decorative icon overlaid on the closed trigger, left of its plain-text
  // value — an <input>'s own value can't hold rich child content the way
  // an open row can, so the closed state can't literally nest renderRow.
  leadingIcon?: ((code: string) => ReactNode) | undefined;
  value?: string | undefined;
  onChange: (code: string) => void;
  placeholder?: string | undefined;
  disabled?: boolean | undefined;
}

interface Entry {
  code: string;
  name: string;
}

const CLEAR_BUTTON_STYLE: CSSProperties = {
  position: "absolute",
  insetInlineEnd: "var(--space-2)",
  top: "50%",
  transform: "translateY(-50%)",
};

export function CodeSelect({
  id,
  codes,
  nameOf,
  renderRow,
  leadingIcon,
  value,
  onChange,
  placeholder,
  disabled = false,
}: CodeSelectProps): ReactNode {
  const [query, setQuery] = useState("");
  const [isOpen, setIsOpen] = useState(false);
  const [highlightedIndex, setHighlightedIndex] = useState(0);
  const listboxId = useId();

  // Portaled to document.body, position: fixed — same reasoning as
  // ActionMenu's/RelationPicker's own panels: an ancestor with overflow:
  // hidden (SectionCard's own collapse-transition wrapper, e.g.) would
  // otherwise clip the dropdown instead of letting it float above the page.
  const containerRef = useRef<HTMLDivElement | null>(null);
  const panelRef = useRef<HTMLSpanElement | null>(null);
  const [position, setPosition] = useState<{ top: number; left: number; width: number } | null>(null);

  // The resolved locale doesn't change while mounted, so names+sort order
  // are computed once per dataset.
  const entries = useMemo<Entry[]>(() => {
    const withNames = codes.map((code): Entry => ({ code, name: nameOf(code) }));
    withNames.sort((a, b) => a.name.localeCompare(b.name));
    return withNames;
  }, [codes, nameOf]);

  const normalizedQuery = query.trim().toLowerCase();
  const matches = useMemo(() => {
    if (normalizedQuery === "") return entries;
    return entries.filter(
      (entry) =>
        entry.name.toLowerCase().includes(normalizedQuery) || entry.code.toLowerCase().includes(normalizedQuery),
    );
  }, [entries, normalizedQuery]);

  const activeIndex = Math.max(0, Math.min(highlightedIndex, matches.length - 1));
  // A value outside the bundled dataset (legacy data, a code the standard
  // gained after this list was written) still needs to display and stay
  // clearable, not silently vanish — falls back to nameOf's own
  // raw-code degrade, same as an unresolvable Intl.DisplayNames lookup.
  const selected = value
    ? (entries.find((entry) => entry.code === value) ?? { code: value, name: nameOf(value) })
    : undefined;

  function open(): void {
    if (disabled) return;
    setIsOpen(true);
    setHighlightedIndex(0);
  }

  function close(): void {
    setIsOpen(false);
    setQuery("");
  }

  // Runs before paint, positioned once on open — not re-tracked on
  // scroll/resize, since the panel closes on Escape/selection/outside-click
  // well before either would matter (same simplification ActionMenu's own
  // panel makes).
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

  // Closes on a click outside both the input and the portaled panel —
  // mousedown, not click, so it commits before any outside element's own
  // click handler fires. Doesn't rely on blur/relatedTarget the way this
  // component's pre-portal version did: the panel is no longer a DOM
  // descendant of the input's container once portaled, so a plain
  // event.currentTarget.contains(event.relatedTarget) check would
  // incorrectly treat every click inside the panel as "outside" too.
  // biome-ignore lint/correctness/useExhaustiveDependencies: close is a plain function recreated every render, not a reactive dependency — only isOpen should re-arm this listener.
  useEffect(() => {
    if (!isOpen) return;
    function handlePointerDown(event: MouseEvent): void {
      const target = event.target as Node;
      if (containerRef.current?.contains(target) || panelRef.current?.contains(target)) return;
      close();
    }
    document.addEventListener("mousedown", handlePointerDown);
    return () => document.removeEventListener("mousedown", handlePointerDown);
  }, [isOpen]);

  function selectEntry(entry: Entry): void {
    onChange(entry.code);
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

    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        if (matches.length > 0) setHighlightedIndex((activeIndex + 1) % matches.length);
        break;
      case "ArrowUp":
        event.preventDefault();
        if (matches.length > 0) setHighlightedIndex((activeIndex - 1 + matches.length) % matches.length);
        break;
      case "Enter": {
        event.preventDefault();
        const entry = matches[activeIndex];
        if (entry) selectEntry(entry);
        break;
      }
      case "Escape":
        event.preventDefault();
        setIsOpen(false);
        setQuery("");
        break;
      default:
        break;
    }
  }

  const triggerValue = isOpen ? query : (selected?.name ?? query);
  const hasOverlayIcon = Boolean(leadingIcon && selected && !isOpen);

  return (
    <div ref={containerRef} className="relative flex flex-col">
      <input
        id={id}
        type="text"
        role="combobox"
        aria-expanded={isOpen}
        aria-controls={listboxId}
        aria-autocomplete="list"
        aria-activedescendant={isOpen && matches.length > 0 ? `${listboxId}-option-${activeIndex}` : undefined}
        value={triggerValue}
        title={!isOpen && selected ? selected.name : undefined}
        disabled={disabled}
        placeholder={placeholder}
        onFocus={open}
        onChange={(event) => {
          setQuery(event.target.value);
          if (!isOpen) open();
          else setHighlightedIndex(0);
        }}
        onKeyDown={handleKeyDown}
        className={`w-full truncate ${fieldInputClassName(false, "input", "sans")} ${hasOverlayIcon ? "ps-8" : ""} ${
          !isOpen && selected ? "pe-8" : ""
        }`}
      />
      {hasOverlayIcon && selected && leadingIcon && (
        <span aria-hidden className="pointer-events-none absolute inset-0 flex items-center pl-3">
          {leadingIcon(selected.code)}
        </span>
      )}
      {!isOpen && selected && (
        <button
          type="button"
          disabled={disabled}
          onClick={() => onChange("")}
          aria-label={`Clear ${selected.name}`}
          style={CLEAR_BUTTON_STYLE}
          className="rounded-control p-1 text-text-secondary hover:opacity-75 focus-visible:outline-none focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-50"
        >
          ×
        </button>
      )}
      {isOpen &&
        createPortal(
          <span
            ref={panelRef}
            id={listboxId}
            role="listbox"
            style={
              position
                ? { position: "fixed", top: position.top, left: position.left, width: position.width }
                : { position: "fixed", top: 0, left: 0, visibility: "hidden" }
            }
            className="z-(--z-dropdown) min-w-60 rounded-structural border border-border bg-surface p-2 shadow-md"
          >
            {matches.length === 0 ? (
              <span aria-live="polite" className="block px-2 py-1 text-sm text-text-secondary">
                No results for &quot;{query}&quot;
              </span>
            ) : (
              matches.map((entry, index) => (
                // aria-label overrides name-from-content, since CountryFlag's own icon already carries one.
                // biome-ignore lint/a11y/useFocusableInteractive: ARIA APG combobox-with-listbox — options are never independently focusable, only virtually "focused" via aria-activedescendant.
                // biome-ignore lint/a11y/useKeyWithClickEvents: keyboard selection is handled by the input's own onKeyDown.
                <div
                  key={entry.code}
                  id={`${listboxId}-option-${index}`}
                  role="option"
                  aria-label={entry.name}
                  aria-selected={index === activeIndex}
                  onMouseEnter={() => setHighlightedIndex(index)}
                  onClick={() => selectEntry(entry)}
                  className={`flex cursor-pointer items-center gap-2 rounded-control px-2 py-1 text-left text-sm text-text ${
                    index === activeIndex ? "bg-surface-hover" : ""
                  }`}
                >
                  {renderRow(entry.code, entry.name)}
                </div>
              ))
            )}
          </span>,
          document.body,
        )}
    </div>
  );
}
