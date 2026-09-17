import type { CSSProperties, ReactNode } from "react";

const CLEAR_BUTTON_STYLE: CSSProperties = {
  position: "absolute",
  insetInlineEnd: "var(--space-2)",
  top: "50%",
  transform: "translateY(-50%)",
};

export interface ComboboxClearButtonProps {
  label: string;
  disabled?: boolean | undefined;
  onClear: () => void;
}

// Shared clear ("×") affordance behind every closed combobox trigger with a
// value — CodeSelect (CountrySelect/LanguageSelect) and IconPicker.
export function ComboboxClearButton({ label, disabled = false, onClear }: ComboboxClearButtonProps): ReactNode {
  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onClear}
      aria-label={`Clear ${label}`}
      style={CLEAR_BUTTON_STYLE}
      className="rounded-control p-1 text-text-secondary hover:opacity-75 focus-visible:outline-none focus-visible:shadow-focus disabled:cursor-not-allowed disabled:opacity-50"
    >
      ×
    </button>
  );
}
