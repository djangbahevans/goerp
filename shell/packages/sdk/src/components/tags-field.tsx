import type { KeyboardEvent, ReactNode } from "react";
import { useId, useState } from "react";
import { fieldInputClassName } from "./field-input-styles.js";

export interface TagValue {
  id: string;
  name: string;
  color?: string;
}

// A tag's color is an arbitrary, tenant-chosen value (not one of our
// pre-calibrated token pairs like Badge's), so contrast against it has to
// be computed rather than looked up — WCAG relative luminance, picking
// whichever of black/white text reads against it. Falls back to the
// default text color for anything that isn't a plain 6-digit hex.
function pillTextClassFor(color: string | undefined): string {
  const match = color ? /^#?([0-9a-f]{6})$/i.exec(color) : null;
  if (!match) return "text-text";
  const hex = match[1] as string;
  const channel = (start: number) => {
    const c = Number.parseInt(hex.slice(start, start + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  const luminance = 0.2126 * channel(0) + 0.7152 * channel(2) + 0.0722 * channel(4);
  return luminance > 0.5 ? "text-black" : "text-white";
}

export interface TagsFieldProps {
  label?: string | undefined;
  value: TagValue[];
  onChange: (value: TagValue[]) => void;
  options: TagValue[];
  creatable?: boolean | undefined;
  onCreate?: ((name: string) => TagValue | Promise<TagValue>) | undefined;
  disabled?: boolean | undefined;
  error?: string | undefined;
  // Overrides the default "Add {label}…" text — for a caller that already
  // renders its own visible label (so it doesn't pass `label` here, to
  // avoid a duplicate) but still wants the add-input's placeholder to name
  // the field.
  placeholder?: string | undefined;
}

export function TagsField({
  label,
  value,
  onChange,
  options,
  creatable = false,
  onCreate,
  disabled = false,
  error,
  placeholder,
}: TagsFieldProps): ReactNode {
  const [query, setQuery] = useState("");
  const [highlightedIndex, setHighlightedIndex] = useState(0);
  const [closed, setClosed] = useState(false);
  const listboxId = useId();
  const selectedIds = new Set(value.map((t) => t.id));
  const normalizedQuery = query.trim().toLowerCase();
  const matches = options.filter((o) => !selectedIds.has(o.id) && o.name.toLowerCase().includes(normalizedQuery));
  const exactMatch = options.some((o) => o.name.toLowerCase() === normalizedQuery);
  const showCreate = creatable && normalizedQuery !== "" && !exactMatch;
  const optionCount = matches.length + (showCreate ? 1 : 0);
  const isOpen = query !== "" && !closed && optionCount > 0;
  const activeIndex = Math.max(0, Math.min(highlightedIndex, optionCount - 1));

  const add = (tag: TagValue) => {
    onChange([...value, tag]);
    setQuery("");
  };

  const remove = (id: string) => {
    onChange(value.filter((t) => t.id !== id));
  };

  const create = async () => {
    if (!onCreate || normalizedQuery === "") return;
    const tag = await onCreate(query.trim());
    add(tag);
  };

  const handleInputChange = (nextQuery: string) => {
    setQuery(nextQuery);
    setClosed(false);
    setHighlightedIndex(0);
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    // Ignore every special key while an IME composition is in progress —
    // Enter/Backspace here commonly confirm or edit the composition itself,
    // not this field's own selection/removal.
    if (event.nativeEvent.isComposing || disabled) return;

    if (event.key === "Backspace" && query === "" && value.length > 0) {
      event.preventDefault();
      const last = value[value.length - 1];
      if (last) remove(last.id);
      return;
    }
    if (!isOpen) {
      // ARIA APG's editable-combobox pattern: Down Arrow redisplays a
      // dismissed (Escape-closed) listbox for the current query.
      if (event.key === "ArrowDown" && query !== "" && optionCount > 0) {
        event.preventDefault();
        setClosed(false);
        setHighlightedIndex(0);
      }
      return;
    }
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        setHighlightedIndex((activeIndex + 1) % optionCount);
        break;
      case "ArrowUp":
        event.preventDefault();
        setHighlightedIndex((activeIndex - 1 + optionCount) % optionCount);
        break;
      case "Enter":
        event.preventDefault();
        if (activeIndex < matches.length) {
          const match = matches[activeIndex];
          if (match) add(match);
        } else if (showCreate) {
          create();
        }
        break;
      case "Escape":
        event.preventDefault();
        setClosed(true);
        break;
      default:
        break;
    }
  };

  const renderOption = (index: number, key: string, content: ReactNode, onSelect: () => void) => (
    // biome-ignore lint/a11y/useFocusableInteractive: the ARIA APG combobox-with-listbox pattern keeps focus on the input throughout — options are never independently focusable, only virtually "focused" via aria-activedescendant.
    // biome-ignore lint/a11y/useKeyWithClickEvents: keyboard selection is handled by the input's own onKeyDown (Enter/ArrowUp/ArrowDown), not a key handler on the option itself.
    <div
      key={key}
      id={`${listboxId}-option-${index}`}
      role="option"
      aria-selected={index === activeIndex}
      aria-disabled={disabled}
      onMouseEnter={disabled ? undefined : () => setHighlightedIndex(index)}
      onClick={disabled ? undefined : onSelect}
      className={`rounded-control px-2 py-1 text-left text-sm text-text ${
        disabled ? "cursor-not-allowed opacity-50" : "cursor-pointer"
      } ${index === activeIndex ? "bg-surface-hover" : ""}`}
    >
      {content}
    </div>
  );

  return (
    <div className="flex flex-col gap-1">
      {label !== undefined && <span className="text-sm text-text">{label}</span>}
      {value.length > 0 && (
        <span className="flex flex-wrap gap-1">
          {value.map((tag) => (
            <span
              key={tag.id}
              style={tag.color ? { backgroundColor: tag.color } : undefined}
              className={`inline-flex items-center gap-1 rounded-full p-2 text-sm ${
                tag.color ? pillTextClassFor(tag.color) : "bg-bg-subtle text-text-secondary"
              }`}
            >
              {tag.name}
              <button
                type="button"
                disabled={disabled}
                onClick={() => remove(tag.id)}
                aria-label={`Remove tag: ${tag.name}`}
                className="rounded-control p-1 transition-colors duration-(--duration-fast) ease-out hover:opacity-75 focus-visible:outline-none focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-50"
              >
                ×
              </button>
            </span>
          ))}
        </span>
      )}
      <input
        type="text"
        role="combobox"
        aria-expanded={isOpen}
        aria-controls={listboxId}
        aria-autocomplete="list"
        aria-activedescendant={isOpen ? `${listboxId}-option-${activeIndex}` : undefined}
        value={query}
        disabled={disabled}
        placeholder={placeholder ?? `Add ${label ?? "a tag"}…`}
        aria-invalid={error !== undefined}
        onChange={(e) => handleInputChange(e.target.value)}
        onKeyDown={handleKeyDown}
        className={fieldInputClassName(error !== undefined)}
      />
      {isOpen && (
        <span
          id={listboxId}
          role="listbox"
          className="flex flex-col gap-1 rounded-structural border border-border bg-surface p-2 shadow-md"
        >
          {matches.map((option, index) => renderOption(index, option.id, option.name, () => add(option)))}
          {showCreate && renderOption(matches.length, "create", `Create "${query.trim()}"`, create)}
        </span>
      )}
      {error !== undefined && (
        <span role="alert" className="text-sm text-danger">
          {error}
        </span>
      )}
    </div>
  );
}
