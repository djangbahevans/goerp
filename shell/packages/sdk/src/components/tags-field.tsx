import type { ReactNode } from "react";
import { useState } from "react";

export interface TagValue {
  id: string;
  name: string;
  color?: string;
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
    <div>
      {label !== undefined && <span>{label}</span>}
      <span>
        {value.map((tag) => (
          <span key={tag.id}>
            {tag.name}{" "}
            <button type="button" disabled={disabled} onClick={() => remove(tag.id)} aria-label={`Remove ${tag.name}`}>
              ×
            </button>
          </span>
        ))}
      </span>
      <input
        type="text"
        value={query}
        disabled={disabled}
        placeholder={placeholder ?? `Add ${label ?? "a tag"}…`}
        aria-invalid={error !== undefined}
        onChange={(e) => setQuery(e.target.value)}
      />
      {query !== "" && (
        <span>
          {matches.map((option) => (
            <button key={option.id} type="button" disabled={disabled} onClick={() => add(option)}>
              {option.name}
            </button>
          ))}
          {creatable && !exactMatch && (
            <button type="button" disabled={disabled} onClick={create}>
              Create "{query.trim()}"
            </button>
          )}
        </span>
      )}
      {error !== undefined && <span role="alert">{error}</span>}
    </div>
  );
}
