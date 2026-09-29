import type { KeyboardEvent, ReactNode } from "react";
import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ComboboxClearButton } from "./combobox-clear-button.js";
import { EscapeLayer } from "./escape-layer.js";
import { useFloatingPanelLayer, useFloatingPanelPosition, useOutsideClickClose } from "./floating-panel.js";
import { useListboxNavigation } from "./listbox-navigation.js";
import { Skeleton } from "./skeleton.js";
import { TextInput } from "./text-input.js";

export type ComboboxStatus = "ready" | "loading" | "error";

export interface ComboboxProps<T> {
  id?: string | undefined;
  query: string;
  onQueryChange: (query: string) => void;
  options: readonly T[];
  getOptionKey: (option: T) => string;
  getOptionLabel: (option: T) => string;
  renderOption?: ((option: T) => ReactNode) | undefined;
  // A returned promise holds the query and panel until it settles, and a
  // rejection leaves both as they were.
  onSelect: (option: T) => void | Promise<void>;
  // Whether an option counts toward the announced result count; a "Create"
  // row doesn't.
  countsAsResult?: ((option: T) => boolean) | undefined;
  status?: ComboboxStatus | undefined;
  errorContent?: ReactNode;
  emptyContent?: ReactNode;
  selectedLabel?: string | undefined;
  onClear?: (() => void) | undefined;
  startAdornment?: ReactNode;
  above?: ReactNode;
  closeOnSelect?: boolean | undefined;
  keepQueryOnDismiss?: boolean | undefined;
  onOpenChange?: ((open: boolean) => void) | undefined;
  onKeyDown?: ((event: KeyboardEvent<HTMLInputElement>) => void) | undefined;
  disabled?: boolean | undefined;
  placeholder?: string | undefined;
}

const ROW_CLASSES = "flex items-center gap-2 rounded-control px-2 py-1 text-left text-sm";

function resultCount(n: number): string {
  if (n === 0) return "No results";
  return n === 1 ? "1 result" : `${n} results`;
}

// docs/components/combobox.md "Combobox".
export function Combobox<T>({
  id,
  query,
  onQueryChange,
  options,
  getOptionKey,
  getOptionLabel,
  renderOption,
  onSelect,
  countsAsResult,
  status = "ready",
  errorContent,
  emptyContent,
  selectedLabel,
  onClear,
  startAdornment,
  above,
  closeOnSelect = true,
  keepQueryOnDismiss = false,
  onOpenChange,
  onKeyDown,
  disabled = false,
  placeholder,
}: ComboboxProps<T>): ReactNode {
  const [isOpen, setIsOpen] = useState(false);
  const [announcement, setAnnouncement] = useState("");
  // Portaled to document.body with position: fixed, so an ancestor with
  // overflow: hidden (a collapsing SectionCard) can't clip the panel.
  const containerRef = useRef<HTMLDivElement | null>(null);
  const panelRef = useRef<HTMLDivElement | null>(null);
  const messageRef = useRef<HTMLDivElement | null>(null);

  // An error replaces the list, even one kept from an earlier search.
  const shown = status === "error" ? [] : options;
  const resultTotal = countsAsResult ? shown.filter(countsAsResult).length : shown.length;

  const nav = useListboxNavigation({
    count: shown.length,
    isOpen,
    homeEnd: query === "",
    onChoose: (index) => {
      const option = shown[index];
      if (option !== undefined) choose(option);
    },
  });

  function setOpen(next: boolean): void {
    setIsOpen(next);
    onOpenChange?.(next);
  }

  function open(): void {
    if (disabled || isOpen) return;
    setOpen(true);
    nav.setActiveIndex(0);
  }

  function dismiss(): void {
    if (!isOpen) return;
    setOpen(false);
    if (!keepQueryOnDismiss && query !== "") onQueryChange("");
  }

  function finishChoosing(): void {
    if (query !== "") onQueryChange("");
    if (closeOnSelect) setOpen(false);
    else nav.setActiveIndex(0);
  }

  function choose(option: T): void {
    const result = onSelect(option);
    // A synchronous pick finishes at once; an async one waits, and a rejection leaves the query and panel.
    if (result instanceof Promise) result.then(finishChoosing, () => {});
    else finishChoosing();
  }

  function handleKeyDown(event: KeyboardEvent<HTMLInputElement>): void {
    onKeyDown?.(event);
    if (event.defaultPrevented || disabled || event.nativeEvent.isComposing) return;
    if (!isOpen) {
      if (event.key === "ArrowDown") {
        event.preventDefault();
        open();
      }
      return;
    }
    if (event.key === "Tab") {
      dismiss();
      return;
    }
    // With nothing to choose, Enter in an open panel still mustn't submit a surrounding form.
    if (!nav.handleKeyDown(event) && event.key === "Enter") event.preventDefault();
  }

  const position = useFloatingPanelPosition(isOpen, containerRef, panelRef, true);
  const layerClassName = useFloatingPanelLayer(containerRef);
  useOutsideClickClose(isOpen, [containerRef, panelRef], dismiss);

  // The status region stays mounted and only its text changes: a region
  // inserted together with its text isn't reliably announced.
  // biome-ignore lint/correctness/useExhaustiveDependencies: query re-announces an unchanged count for a new search.
  useEffect(() => {
    if (!isOpen) {
      setAnnouncement("");
      return;
    }
    if (status === "loading") return;
    if (shown.length > 0) {
      setAnnouncement(resultCount(resultTotal));
      return;
    }
    setAnnouncement(messageRef.current?.textContent ?? "");
  }, [isOpen, status, shown.length, resultTotal, query]);

  const hasSelection = !isOpen && selectedLabel !== undefined;

  function renderPanelContent(): ReactNode {
    if (shown.length === 0) {
      if (status === "loading") return <Skeleton lines={3} />;
      return (
        <div ref={messageRef}>
          {status === "error"
            ? (errorContent ?? <p className="px-2 py-1 text-danger text-sm">Couldn't load results.</p>)
            : (emptyContent ?? (
                <p className="px-2 py-1 text-sm text-text-secondary">No results for &quot;{query}&quot;</p>
              ))}
        </div>
      );
    }
    return shown.map((option, index) => {
      const label = getOptionLabel(option);
      return (
        // biome-ignore lint/a11y/useAriaPropsSupportedByRole: role="option" comes from getOptionProps.
        <div
          key={getOptionKey(option)}
          {...nav.getOptionProps(index)}
          aria-label={label}
          title={label}
          className={`${ROW_CLASSES} cursor-pointer text-text ${index === nav.activeIndex ? "bg-surface-hover" : ""}`}
        >
          {renderOption ? renderOption(option) : <span className="min-w-0 truncate">{label}</span>}
        </div>
      );
    });
  }

  return (
    <div ref={containerRef} className="relative flex flex-col gap-1">
      {above}
      <TextInput
        id={id}
        {...nav.inputProps}
        value={isOpen ? query : (selectedLabel ?? query)}
        title={hasSelection ? selectedLabel : undefined}
        disabled={disabled}
        placeholder={placeholder}
        onFocus={open}
        onClick={open}
        onChange={(next) => {
          onQueryChange(next);
          if (!isOpen) open();
          else nav.setActiveIndex(0);
        }}
        onKeyDown={handleKeyDown}
        start={
          hasSelection && startAdornment !== undefined ? (
            <span aria-hidden className="flex">
              {startAdornment}
            </span>
          ) : undefined
        }
        end={
          hasSelection && onClear ? (
            <ComboboxClearButton label={selectedLabel} disabled={disabled} onClear={onClear} />
          ) : undefined
        }
      />
      <span role="status" className="sr-only">
        {announcement}
      </span>
      {isOpen &&
        createPortal(
          <EscapeLayer onEscape={dismiss}>
            <div
              ref={panelRef}
              id={nav.listboxId}
              role="listbox"
              aria-busy={status === "loading" || undefined}
              style={
                position
                  ? { position: "fixed", top: position.top, left: position.left, width: position.width }
                  : { position: "fixed", top: 0, left: 0, visibility: "hidden" }
              }
              className={`${layerClassName} max-h-80 min-w-60 overflow-y-auto rounded-structural border border-border bg-surface p-2 shadow-md`}
            >
              {renderPanelContent()}
            </div>
          </EscapeLayer>,
          document.body,
        )}
    </div>
  );
}
