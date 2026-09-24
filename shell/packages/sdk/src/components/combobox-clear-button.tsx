import type { CSSProperties, ReactNode } from "react";
import { IconButton } from "./icon-button.js";

const CLEAR_BUTTON_STYLE: CSSProperties = {
  position: "absolute",
  insetInlineEnd: "var(--space-1)",
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
    <span style={CLEAR_BUTTON_STYLE} className="flex">
      <IconButton icon="x" label={`Clear ${label}`} size="sm" disabled={disabled} onClick={onClear} />
    </span>
  );
}
