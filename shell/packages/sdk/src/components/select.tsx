import * as SelectPrimitive from "@radix-ui/react-select";
import { Check, ChevronDown } from "lucide-react";
import type { CSSProperties, KeyboardEvent, ReactNode } from "react";
import { useId, useState } from "react";
import { Badge, type BadgeColor } from "./badge.js";
import { fieldInputClassName } from "./field-input-styles.js";
import { Icon } from "./icon.js";

// manifest-spec.md's FieldOption object (§10), redefined locally since
// packages/sdk must not depend on packages/app.
export interface SelectOption {
  value: string;
  label: string;
  icon?: string | undefined;
  color?: string | undefined;
  disabled?: boolean | undefined;
}

export interface SelectProps {
  id?: string | undefined;
  options: SelectOption[];
  value: string | string[];
  onChange: (value: string | string[]) => void;
  multiple?: boolean | undefined;
  placeholder?: string | undefined;
  disabled?: boolean | undefined;
}

const TRIGGER_CLASSES = `flex w-full items-center justify-between gap-2 text-left ${fieldInputClassName(false, "input", "sans")}`;
// docs/components/select.md's own states table never truncates panel
// labels, unlike the closed trigger.
const PANEL_CLASSES = "z-(--z-dropdown) w-max rounded-structural border border-border bg-surface p-2 shadow-md";
const ROW_CLASSES =
  "flex cursor-pointer items-center gap-2 whitespace-nowrap rounded-control px-2 py-1 text-sm text-text outline-none";
// Extra trailing room for the overlaid clear button (below) once a value
// is selected, past the chevron the trigger already reserves space for.
const TRIGGER_WITH_VALUE_STYLE: CSSProperties = { paddingInlineEnd: "var(--space-9)" };
// Overlays a clear control on the trigger, matching relation-picker.tsx's
// own single-select clear button.
const CLEAR_BUTTON_STYLE: CSSProperties = {
  position: "absolute",
  insetInlineEnd: "var(--space-6)",
  top: "50%",
  transform: "translateY(-50%)",
};

// FieldOption.color (manifest-spec.md §10) is a loosely-typed wire string,
// not a statically-checked BadgeColor — Badge's own gray-fallback handles
// an unrecognized value.
// `className` (only ever "truncate", from the closed trigger's single-line
// label — panel rows never pass one) applies to the label text alone, not
// this wrapper, since "truncate" on a flex container doesn't ellipsize.
function OptionLabel({ option, className }: { option: SelectOption; className?: string | undefined }): ReactNode {
  if (option.color) return <Badge label={option.label} color={option.color as BadgeColor} icon={option.icon} />;
  return (
    <span className="flex min-w-0 items-center gap-2">
      {option.icon && <Icon name={option.icon} size={14} className="shrink-0" aria-hidden="true" />}
      <span className={className}>{option.label}</span>
    </span>
  );
}

function SelectSingle({ id, options, value, onChange, placeholder, disabled = false }: Omit<SelectProps, "multiple">) {
  const selected = options.find((option) => option.value === value);
  return (
    <div className="relative">
      <SelectPrimitive.Root value={typeof value === "string" ? value : ""} onValueChange={onChange} disabled={disabled}>
        <SelectPrimitive.Trigger
          id={id}
          style={selected ? TRIGGER_WITH_VALUE_STYLE : undefined}
          className={TRIGGER_CLASSES}
        >
          <SelectPrimitive.Value placeholder={placeholder}>
            {selected ? <OptionLabel option={selected} className="truncate" /> : undefined}
          </SelectPrimitive.Value>
          <SelectPrimitive.Icon>
            <ChevronDown size={16} className="shrink-0 text-text-secondary" aria-hidden />
          </SelectPrimitive.Icon>
        </SelectPrimitive.Trigger>
        <SelectPrimitive.Portal>
          <SelectPrimitive.Content position="popper" sideOffset={4} className={PANEL_CLASSES}>
            <SelectPrimitive.Viewport>
              {options.map((option) => (
                <SelectPrimitive.Item
                  key={option.value}
                  value={option.value}
                  disabled={option.disabled ?? false}
                  textValue={option.label}
                  className={`${ROW_CLASSES} data-highlighted:bg-surface-hover data-disabled:cursor-not-allowed data-disabled:opacity-50`}
                >
                  <SelectPrimitive.ItemIndicator>
                    <Check size={16} className="shrink-0 text-primary" aria-hidden />
                  </SelectPrimitive.ItemIndicator>
                  <SelectPrimitive.ItemText>
                    <OptionLabel option={option} />
                  </SelectPrimitive.ItemText>
                </SelectPrimitive.Item>
              ))}
            </SelectPrimitive.Viewport>
          </SelectPrimitive.Content>
        </SelectPrimitive.Portal>
      </SelectPrimitive.Root>
      {selected && !disabled && (
        <button
          type="button"
          onClick={() => onChange("")}
          aria-label={`Clear ${selected.label}`}
          style={CLEAR_BUTTON_STYLE}
          className="rounded-control p-1 text-text-secondary hover:opacity-75 focus-visible:outline-none focus-visible:shadow-focus"
        >
          ×
        </button>
      )}
    </div>
  );
}

// @radix-ui/react-select has no multi-select support (string-only value,
// closes on pick) — a hand-rolled listbox instead, the same button-trigger
// + aria-activedescendant shape relation-picker.tsx uses.
function SelectMultiple({
  id,
  options,
  value,
  onChange,
  placeholder,
  disabled = false,
}: Omit<SelectProps, "multiple">) {
  const selectedValues = Array.isArray(value) ? value : [];
  const selectedSet = new Set(selectedValues);
  const selectedOptions = options.filter((option) => selectedSet.has(option.value));
  const [isOpen, setIsOpen] = useState(false);
  const [highlightedIndex, setHighlightedIndex] = useState(0);
  const listboxId = useId();
  const activeIndex = Math.max(0, Math.min(highlightedIndex, options.length - 1));

  function open(): void {
    if (disabled) return;
    setIsOpen(true);
    setHighlightedIndex(
      Math.max(
        0,
        options.findIndex((option) => !option.disabled),
      ),
    );
  }

  // No focus management needed: options are never independently
  // focusable, so focus never leaves the trigger button.
  function close(): void {
    setIsOpen(false);
  }

  function toggle(option: SelectOption): void {
    if (option.disabled) return;
    onChange(
      selectedSet.has(option.value)
        ? selectedValues.filter((v) => v !== option.value)
        : [...selectedValues, option.value],
    );
  }

  function handleKeyDown(event: KeyboardEvent<HTMLButtonElement>): void {
    if (disabled) return;
    if (!isOpen) {
      if (event.key === "ArrowDown" || event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        open();
      }
      return;
    }
    switch (event.key) {
      case "ArrowDown":
        event.preventDefault();
        if (options.length > 0) setHighlightedIndex((activeIndex + 1) % options.length);
        break;
      case "ArrowUp":
        event.preventDefault();
        if (options.length > 0) setHighlightedIndex((activeIndex - 1 + options.length) % options.length);
        break;
      case "Enter":
      case " ": {
        event.preventDefault();
        const option = options[activeIndex];
        if (option) toggle(option);
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

  const triggerContent =
    selectedOptions.length === 0 ? (
      <span className="truncate text-text-secondary">{placeholder}</span>
    ) : selectedOptions.length <= 2 ? (
      <span className="truncate" title={selectedOptions.map((o) => o.label).join(", ")}>
        {selectedOptions.map((o) => o.label).join(", ")}
      </span>
    ) : (
      <span className="truncate" title={selectedOptions.map((o) => o.label).join(", ")}>
        {`${selectedOptions
          .slice(0, 2)
          .map((o) => o.label)
          .join(", ")} +${selectedOptions.length - 2} more`}
      </span>
    );

  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: focus-out boundary only — closes the panel when focus leaves the trigger+listbox pair, not a user-facing interactive element itself.
    <div
      className="relative"
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setIsOpen(false);
      }}
    >
      <button
        id={id}
        type="button"
        disabled={disabled}
        role="combobox"
        aria-autocomplete="none"
        aria-haspopup="listbox"
        aria-expanded={isOpen}
        aria-controls={listboxId}
        aria-activedescendant={isOpen && options.length > 0 ? `${listboxId}-option-${activeIndex}` : undefined}
        onClick={() => (isOpen ? close() : open())}
        onKeyDown={handleKeyDown}
        className={TRIGGER_CLASSES}
      >
        {triggerContent}
        <ChevronDown size={16} className="shrink-0 text-text-secondary" aria-hidden />
      </button>
      {isOpen && (
        <span
          id={listboxId}
          role="listbox"
          aria-multiselectable="true"
          className={`absolute top-full mt-1 ${PANEL_CLASSES}`}
        >
          {options.map((option, index) => (
            // biome-ignore lint/a11y/useFocusableInteractive: ARIA APG listbox-button pattern — options are never independently focusable, only virtually "focused" via aria-activedescendant on the trigger button.
            // biome-ignore lint/a11y/useKeyWithClickEvents: keyboard selection is handled by the trigger button's own onKeyDown.
            <div
              key={option.value}
              id={`${listboxId}-option-${index}`}
              role="option"
              aria-selected={selectedSet.has(option.value)}
              aria-disabled={option.disabled}
              onMouseEnter={option.disabled ? undefined : () => setHighlightedIndex(index)}
              onClick={option.disabled ? undefined : () => toggle(option)}
              className={`${ROW_CLASSES} ${option.disabled ? "cursor-not-allowed opacity-50" : ""} ${
                index === activeIndex ? "bg-surface-hover" : ""
              }`}
            >
              {selectedSet.has(option.value) ? (
                <Check size={16} className="shrink-0 text-primary" aria-hidden />
              ) : (
                <span aria-hidden className="inline-block w-4" />
              )}
              <OptionLabel option={option} />
            </div>
          ))}
        </span>
      )}
    </div>
  );
}

export function Select({ multiple = false, ...props }: SelectProps): ReactNode {
  return multiple ? <SelectMultiple {...props} /> : <SelectSingle {...props} />;
}
