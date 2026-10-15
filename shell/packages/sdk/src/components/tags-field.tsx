import type { KeyboardEvent, ReactNode } from "react";
import { useRef, useState } from "react";
import { EscapeLayer } from "./escape-layer.js";
import { fieldInputClassName } from "./field-input-styles.js";
import { FieldError, FieldLabel, useSelfLabelledFieldControl } from "./field-wrapper.js";
import { useOutsideClickClose } from "./floating-panel.js";
import { useListboxNavigation } from "./listbox-navigation.js";
import { TagPill } from "./tag-pill.js";
import { requiredProps } from "./text-input.js";

export interface TagValue {
  id: string;
  name: string;
  color?: string;
}

export interface TagsFieldProps {
  label?: string | undefined;
  // Applied to the combobox <input>, not the "Remove tag" pill buttons that
  // render ahead of it — lets an external <label htmlFor> target it directly
  // (goerp#698).
  id?: string | undefined;
  value: TagValue[];
  onChange: (value: TagValue[]) => void;
  options: TagValue[];
  creatable?: boolean | undefined;
  // Returns the created tag, which is added to the selection. On a rejection the typed name stays
  // in the input and nothing is added; reporting the error is the caller's job.
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
  id,
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
  const [closed, setClosed] = useState(false);
  const { id: fieldId, errorId, controlProps } = useSelfLabelledFieldControl(id, error);
  const containerRef = useRef<HTMLDivElement | null>(null);
  const selectedIds = new Set(value.map((t) => t.id));
  const normalizedQuery = query.trim().toLowerCase();
  const matches = options.filter((o) => !selectedIds.has(o.id) && o.name.toLowerCase().includes(normalizedQuery));
  const exactMatch = options.some((o) => o.name.toLowerCase() === normalizedQuery);
  const showCreate = creatable && normalizedQuery !== "" && !exactMatch;
  const optionCount = matches.length + (showCreate ? 1 : 0);
  const isOpen = query !== "" && !closed && optionCount > 0;
  const nav = useListboxNavigation({
    count: optionCount,
    isOpen,
    // The list only shows while a query is typed, so Home/End always move the caret.
    homeEnd: false,
    onChoose: (index) => {
      const match = matches[index];
      if (match) add(match);
      else if (showCreate) void create();
    },
  });

  // The list sits in the page flow, so no portal is needed, but a press
  // outside still dismisses it the way Escape does, keeping the query.
  useOutsideClickClose(isOpen, [containerRef], () => setClosed(true));

  const add = (tag: TagValue) => {
    onChange([...value, tag]);
    setQuery("");
  };

  const remove = (id: string) => {
    onChange(value.filter((t) => t.id !== id));
  };

  const create = async () => {
    if (!onCreate || normalizedQuery === "") return;
    let tag: TagValue;
    try {
      tag = await onCreate(query.trim());
    } catch {
      return;
    }
    add(tag);
  };

  const handleInputChange = (nextQuery: string) => {
    setQuery(nextQuery);
    setClosed(false);
    nav.setActiveIndex(0);
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
        nav.setActiveIndex(0);
      }
      return;
    }
    if (event.key === "Tab") {
      setClosed(true);
      return;
    }
    nav.handleKeyDown(event);
  };

  const renderOption = (index: number, key: string, content: ReactNode) => (
    <div
      key={key}
      {...nav.getOptionProps(index, { disabled })}
      className={`rounded-control px-2 py-1 text-left text-sm text-text max-md:min-h-11 ${
        disabled ? "cursor-not-allowed opacity-50" : "cursor-pointer"
      } ${index === nav.activeIndex ? "bg-surface-hover" : ""}`}
    >
      {content}
    </div>
  );

  return (
    <div ref={containerRef} className="flex flex-col gap-1">
      {label !== undefined && <FieldLabel htmlFor={fieldId}>{label}</FieldLabel>}
      {value.length > 0 && (
        <span className="flex flex-wrap gap-1">
          {value.map((tag) => (
            <TagPill key={tag.id} name={tag.name} color={tag.color} size="md">
              <button
                type="button"
                disabled={disabled}
                onClick={() => remove(tag.id)}
                aria-label={`Remove tag: ${tag.name}`}
                className="inline-flex items-center justify-center rounded-control p-1 transition-colors duration-(--duration-fast) ease-out hover:opacity-75 focus-visible:shadow-focus focus-visible:outline-none disabled:cursor-not-allowed disabled:opacity-50 max-md:-my-2 max-md:min-h-11 max-md:min-w-11"
              >
                ×
              </button>
            </TagPill>
          ))}
        </span>
      )}
      <input
        {...controlProps}
        {...requiredProps("combobox", controlProps.required)}
        type="text"
        {...nav.inputProps}
        value={query}
        disabled={disabled}
        placeholder={placeholder ?? `Add ${label ?? "a tag"}…`}
        onChange={(e) => handleInputChange(e.target.value)}
        onKeyDown={handleKeyDown}
        className={fieldInputClassName(controlProps["aria-invalid"] === true)}
      />
      {isOpen && (
        <EscapeLayer onEscape={() => setClosed(true)}>
          <span
            id={nav.listboxId}
            role="listbox"
            className="flex flex-col gap-1 rounded-structural border border-border bg-surface p-2 shadow-md"
          >
            {matches.map((option, index) => renderOption(index, option.id, option.name))}
            {showCreate && renderOption(matches.length, "create", `Create "${query.trim()}"`)}
          </span>
        </EscapeLayer>
      )}
      {error !== undefined && <FieldError id={errorId}>{error}</FieldError>}
    </div>
  );
}
