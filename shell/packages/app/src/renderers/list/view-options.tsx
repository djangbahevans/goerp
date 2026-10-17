import type { SelectOption } from "@goerp/sdk/components";
import { Button, Checkbox, Select } from "@goerp/sdk/components";
import type { ReactNode } from "react";
import { SideSheet } from "../../chrome/side-sheet.js";
import { FilterFieldLabel, LEGEND_CLASSES } from "./list-filters.js";
import type { ListColumn } from "./list-view-types.js";

export function ViewOptionSelect({
  id,
  label,
  options,
  value,
  emptyValue,
  onChange,
}: {
  id: string;
  label: string;
  options: SelectOption[];
  value: string;
  emptyValue: string;
  onChange: (value: string | undefined) => void;
}): ReactNode {
  return (
    <FilterFieldLabel id={id} label={label}>
      <span className="block min-w-40">
        <Select
          id={id}
          options={options}
          value={value}
          emptyValue={emptyValue}
          onChange={(next) => {
            const selected = Array.isArray(next) ? next[0] : next;
            onChange(selected !== undefined && selected !== emptyValue ? selected : undefined);
          }}
        />
      </span>
    </FilterFieldLabel>
  );
}

export function ColumnCheckboxes({
  columns,
  revealed,
  onToggle,
}: {
  columns: ListColumn[];
  revealed: ReadonlySet<string>;
  onToggle: (field: string) => void;
}): ReactNode {
  return (
    <fieldset className="m-0 flex flex-col gap-2 border-0 p-0">
      <legend className={`mb-2 ${LEGEND_CLASSES}`}>Columns</legend>
      {columns.map((column) => (
        <Checkbox
          key={column.field}
          label={column.label ?? column.field}
          checked={revealed.has(column.field)}
          onChange={() => onToggle(column.field)}
        />
      ))}
    </fieldset>
  );
}

// list-renderer.md "View options below 42rem".
export function ViewOptionsSheet({
  changed,
  open,
  onOpenChange,
  children,
}: {
  changed: number;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  children: ReactNode;
}): ReactNode {
  return (
    <>
      <Button
        variant="secondary"
        icon="sliders-horizontal"
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={changed > 0 ? `View options, ${changed} changed` : undefined}
        onClick={() => onOpenChange(!open)}
      >
        View options{" "}
        {changed > 0 && (
          <span
            aria-hidden="true"
            className="rounded-full bg-primary px-1.5 py-0.5 font-medium text-[10px] text-text-inverse"
          >
            {changed}
          </span>
        )}
      </Button>
      <SideSheet open={open} onClose={() => onOpenChange(false)} title="View options">
        <div className="flex flex-col gap-4 p-4">{children}</div>
      </SideSheet>
    </>
  );
}
