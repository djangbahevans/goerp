import type { CSSProperties, KeyboardEvent, ReactNode } from "react";
import { useId, useMemo, useState } from "react";
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
    // flex flex-col (not just relative) matches relation-picker.tsx's own
    // wrapper — a plain block container gives an absolutely positioned
    // child with no explicit left/right an unpredictable static position;
    // flex's own alignment-based static-position algorithm doesn't.
    // biome-ignore lint/a11y/noStaticElementInteractions: focus-out boundary only — closes the panel when focus leaves the trigger+listbox pair, not a user-facing interactive element itself.
    <div
      className="relative flex flex-col"
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) close();
      }}
    >
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
        style={{
          ...(hasOverlayIcon ? { paddingInlineStart: "var(--space-8)" } : undefined),
          ...(!isOpen && selected ? { paddingInlineEnd: "var(--space-8)" } : undefined),
        }}
        className={`w-full truncate ${fieldInputClassName(false, "input", "sans")}`}
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
      {isOpen && (
        <span
          id={listboxId}
          role="listbox"
          className="absolute top-full z-(--z-dropdown) mt-1 w-full min-w-60 rounded-structural border border-border bg-surface p-2 shadow-md"
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
        </span>
      )}
    </div>
  );
}
