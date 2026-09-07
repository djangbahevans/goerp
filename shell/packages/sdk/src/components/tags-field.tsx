import type { ReactNode } from "react";
import { useState } from "react";
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
  const selectedIds = new Set(value.map((t) => t.id));
  const normalizedQuery = query.trim().toLowerCase();
  const matches = options.filter((o) => !selectedIds.has(o.id) && o.name.toLowerCase().includes(normalizedQuery));
  const exactMatch = options.some((o) => o.name.toLowerCase() === normalizedQuery);

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
        value={query}
        disabled={disabled}
        placeholder={placeholder ?? `Add ${label ?? "a tag"}…`}
        aria-invalid={error !== undefined}
        onChange={(e) => setQuery(e.target.value)}
        className={fieldInputClassName(error !== undefined)}
      />
      {query !== "" && (
        <span className="flex flex-col gap-1 rounded-structural border border-border bg-surface p-2 shadow-md">
          {matches.map((option) => (
            <button
              key={option.id}
              type="button"
              disabled={disabled}
              onClick={() => add(option)}
              className="rounded-control px-2 py-1 text-left text-sm text-text hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-50"
            >
              {option.name}
            </button>
          ))}
          {creatable && !exactMatch && (
            <button
              type="button"
              disabled={disabled}
              onClick={create}
              className="rounded-control px-2 py-1 text-left text-sm text-text hover:bg-surface-hover disabled:cursor-not-allowed disabled:opacity-50"
            >
              Create "{query.trim()}"
            </button>
          )}
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
