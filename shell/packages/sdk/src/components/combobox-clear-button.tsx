import type { ReactNode } from "react";
import { IconButton } from "./icon-button.js";

export interface ComboboxClearButtonProps {
  label: string;
  disabled?: boolean | undefined;
  onClear: () => void;
}

// Shared clear ("×") affordance in the `end` slot of every closed combobox
// input with a value — CodeSelect (CountrySelect/LanguageSelect), IconPicker
// and RelationPicker.
export function ComboboxClearButton({ label, disabled = false, onClear }: ComboboxClearButtonProps): ReactNode {
  return <IconButton icon="x" label={`Clear ${label}`} size="sm" disabled={disabled} onClick={onClear} />;
}
