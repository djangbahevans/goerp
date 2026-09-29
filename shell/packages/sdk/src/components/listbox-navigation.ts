import type { KeyboardEvent, MouseEvent } from "react";
import { useId, useState } from "react";
import { optionElementId, useScrollHighlightedOptionIntoView } from "./floating-panel.js";

// Roughly one max-h-80 panel of rows at the shared row height.
export const LISTBOX_PAGE_JUMP = 10;

export interface ListboxNavigationOptions {
  count: number;
  isOpen: boolean;
  wrap?: boolean | undefined;
  homeEnd?: boolean | undefined;
  onChoose: (index: number) => void;
}

export interface ListboxInputProps {
  role: "combobox";
  "aria-expanded": boolean;
  "aria-controls": string;
  "aria-autocomplete": "list";
  "aria-activedescendant": string | undefined;
}

export interface ListboxOptionProps {
  id: string;
  role: "option";
  "aria-selected": boolean;
  "aria-disabled": boolean | undefined;
  onMouseEnter: () => void;
  onMouseDown: (event: MouseEvent) => void;
  onClick: () => void;
}

export interface ListboxNavigation {
  listboxId: string;
  activeIndex: number;
  setActiveIndex: (index: number) => void;
  inputProps: ListboxInputProps;
  handleKeyDown: (event: KeyboardEvent) => boolean;
  getOptionProps: (
    index: number,
    options?: { disabled?: boolean | undefined; selected?: boolean | undefined },
  ) => ListboxOptionProps;
}

// docs/components/combobox.md "useListboxNavigation": the highlight, keys
// and ARIA wiring shared by every input-driven listbox.
export function useListboxNavigation({
  count,
  isOpen,
  wrap = true,
  homeEnd = true,
  onChoose,
}: ListboxNavigationOptions): ListboxNavigation {
  const listboxId = useId();
  const [highlighted, setHighlighted] = useState(0);
  const activeIndex = count > 0 ? Math.max(0, Math.min(highlighted, count - 1)) : 0;
  useScrollHighlightedOptionIntoView(isOpen, listboxId, activeIndex, count);

  function nextIndex(key: string): number | null {
    switch (key) {
      case "ArrowDown":
        return activeIndex + 1 < count ? activeIndex + 1 : wrap ? 0 : activeIndex;
      case "ArrowUp":
        return activeIndex > 0 ? activeIndex - 1 : wrap ? count - 1 : activeIndex;
      case "Home":
        return homeEnd ? 0 : null;
      case "End":
        return homeEnd ? count - 1 : null;
      case "PageDown":
        return Math.min(count - 1, activeIndex + LISTBOX_PAGE_JUMP);
      case "PageUp":
        return Math.max(0, activeIndex - LISTBOX_PAGE_JUMP);
      default:
        return null;
    }
  }

  function handleKeyDown(event: KeyboardEvent): boolean {
    if (!isOpen || count === 0 || event.nativeEvent.isComposing) return false;
    if (event.key === "Enter") {
      event.preventDefault();
      onChoose(activeIndex);
      return true;
    }
    const next = nextIndex(event.key);
    if (next === null) return false;
    event.preventDefault();
    setHighlighted(next);
    return true;
  }

  return {
    listboxId,
    activeIndex,
    setActiveIndex: setHighlighted,
    inputProps: {
      role: "combobox",
      "aria-expanded": isOpen,
      "aria-controls": listboxId,
      "aria-autocomplete": "list",
      "aria-activedescendant": isOpen && count > 0 ? optionElementId(listboxId, activeIndex) : undefined,
    },
    handleKeyDown,
    getOptionProps: (index, { disabled = false, selected } = {}) => ({
      id: optionElementId(listboxId, index),
      role: "option",
      // A multi-select listbox reports each option's checked state here instead of the highlight.
      "aria-selected": selected ?? index === activeIndex,
      "aria-disabled": disabled || undefined,
      onMouseEnter: () => {
        if (!disabled) setHighlighted(index);
      },
      // Keeps focus in the input, so typing continues after a pick.
      onMouseDown: (event) => event.preventDefault(),
      onClick: () => {
        if (!disabled) onChoose(index);
      },
    }),
  };
}
