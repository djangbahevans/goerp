import type { KeyboardEvent, ReactNode } from "react";
import { useId, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ComboboxClearButton } from "./combobox-clear-button.js";
import {
  optionElementId,
  useFloatingPanelPosition,
  useOutsideClickClose,
  useScrollHighlightedOptionIntoView,
} from "./floating-panel.js";
import { TextInput } from "./text-input.js";

// Shared combobox mechanics behind country-select.tsx, language-select.tsx,
// timezone-select.tsx, and currency-select.tsx — same searchable-list-over-
// a-dataset shape, differing only in dataset, name resolution, and row
// visual.

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

// Fixed row count, not a measured panel height — approximates max-h-80 at
// this file's shared row height without an extra DOM measurement pass.
export const PAGE_JUMP = 10;

// Shared by timezone-select.tsx/currency-select.tsx, whose datasets are
// enumerated at runtime instead of bundled like country/language.
export function supportedValuesOrEmpty(key: "currency" | "timeZone"): string[] {
  try {
    return Intl.supportedValuesOf(key);
  } catch {
    return [];
  }
}

export interface CodeSelectFallbackProps {
  id?: string | undefined;
  value?: string | undefined;
  onChange: (value: string) => void;
  placeholder?: string | undefined;
  disabled?: boolean | undefined;
}

// Rendered instead of CodeSelect when supportedValuesOrEmpty comes back
// empty (Intl.supportedValuesOf unavailable) — a plain free-text input,
// the terminal degrade CountrySelect/LanguageSelect never need since their
// bundled datasets are always populated.
export function CodeSelectFallbackInput({
  id,
  value,
  onChange,
  placeholder,
  disabled = false,
}: CodeSelectFallbackProps): ReactNode {
  return <TextInput id={id} value={value ?? ""} disabled={disabled} placeholder={placeholder} onChange={onChange} />;
}

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

  const position = useFloatingPanelPosition(isOpen, containerRef, panelRef, true);
  useOutsideClickClose(isOpen, [containerRef, panelRef], close);

  useScrollHighlightedOptionIntoView(isOpen, listboxId, activeIndex, matches.length);

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
      case "Home":
        // Only intercepted while browsing the unfiltered list — once a query
        // is typed, Home/End fall through to the input's own native
        // caret-to-start/caret-to-end behavior instead of hijacking it.
        if (query === "" && matches.length > 0) {
          event.preventDefault();
          setHighlightedIndex(0);
        }
        break;
      case "End":
        if (query === "" && matches.length > 0) {
          event.preventDefault();
          setHighlightedIndex(matches.length - 1);
        }
        break;
      case "PageUp":
        event.preventDefault();
        if (matches.length > 0) setHighlightedIndex(Math.max(0, activeIndex - PAGE_JUMP));
        break;
      case "PageDown":
        event.preventDefault();
        if (matches.length > 0) setHighlightedIndex(Math.min(matches.length - 1, activeIndex + PAGE_JUMP));
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
      <TextInput
        id={id}
        role="combobox"
        aria-expanded={isOpen}
        aria-controls={listboxId}
        aria-autocomplete="list"
        aria-activedescendant={isOpen && matches.length > 0 ? optionElementId(listboxId, activeIndex) : undefined}
        value={triggerValue}
        title={!isOpen && selected ? selected.name : undefined}
        disabled={disabled}
        placeholder={placeholder}
        onFocus={open}
        onChange={(next) => {
          setQuery(next);
          if (!isOpen) open();
          else setHighlightedIndex(0);
        }}
        onKeyDown={handleKeyDown}
        start={
          hasOverlayIcon && selected && leadingIcon ? (
            <span aria-hidden className="flex">
              {leadingIcon(selected.code)}
            </span>
          ) : undefined
        }
        end={
          !isOpen && selected ? (
            <ComboboxClearButton label={selected.name} disabled={disabled} onClear={() => onChange("")} />
          ) : undefined
        }
      />
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
            className="z-(--z-dropdown) max-h-80 min-w-60 overflow-y-auto rounded-structural border border-border bg-surface p-2 shadow-md"
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
                  id={optionElementId(listboxId, index)}
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
