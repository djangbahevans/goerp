import type { ComponentPropsWithRef, HTMLInputTypeAttribute, MouseEvent, ReactNode } from "react";
import { useState } from "react";
import { cn } from "./cn.js";
import { fieldInputClassName } from "./field-input-styles.js";
import { joinIds, useFieldControl } from "./field-wrapper.js";

export type TextInputType = "text" | "email" | "search" | "tel" | "url" | "number";
export type TextInputSize = "sm" | "md";

type NativeInputProps = Omit<
  ComponentPropsWithRef<"input">,
  "value" | "onChange" | "type" | "size" | "className" | "style" | "children"
>;

export interface TextInputProps extends NativeInputProps {
  value: string;
  onChange: (value: string) => void;
  type?: TextInputType | undefined;
  size?: TextInputSize | undefined;
  // Defaults to the enclosing FieldWrapper's error state.
  invalid?: boolean | undefined;
  start?: ReactNode;
  end?: ReactNode;
}

// Fixed heights matching Button's, so an input and a button line up in a row.
const SIZE_CLASSES: Record<TextInputSize, string> = { md: "h-9 text-base", sm: "h-7 text-sm" };

// The <input> carries the horizontal padding, so a click anywhere in the box
// outside a slot lands on it. Beside a slot it keeps the --space-2 gap.
const INPUT_PADDING: Record<TextInputSize, { edge: string; start: string; end: string }> = {
  md: { edge: "ps-3", start: "ps-2", end: "pe-3" },
  sm: { edge: "ps-2", start: "ps-2", end: "pe-2" },
};

// docs/components/text-input.md.
export function TextInput(props: TextInputProps): ReactNode {
  return <InputBox {...props} />;
}

// TextInput without the `type` restriction, for the SDK's own fields that
// need a type TextInput doesn't offer (PasswordField's "password"), and with
// MoneyField's mono amount.
export function InputBox({
  font = "sans",
  value,
  onChange,
  type = "text",
  size = "md",
  invalid,
  start,
  end,
  id,
  required,
  "aria-describedby": ariaDescribedBy,
  className: _className,
  style: _style,
  ...rest
}: Omit<TextInputProps, "type"> & {
  type?: HTMLInputTypeAttribute | undefined;
  font?: "sans" | "mono" | undefined;
  className?: unknown;
  style?: unknown;
}): ReactNode {
  const field = useFieldControl();
  const isInvalid = invalid ?? field?.invalid ?? false;
  const [typed, setTyped] = useState(value);
  const inputProps = {
    ...rest,
    type,
    value: type === "number" && sameNumber(typed, value) ? typed : value,
    id: field?.id ?? id,
    ...requiredProps(rest.role, required ?? field?.required),
    "aria-describedby": joinIds(field?.describedBy, ariaDescribedBy),
    "aria-invalid": isInvalid || undefined,
    onChange: (event: { target: { value: string } }) => {
      setTyped(event.target.value);
      onChange(event.target.value);
    },
  };

  const padding = INPUT_PADDING[size];

  // One structure whether or not a slot is present, so a slot appearing (a
  // combobox's clear button) never remounts the focused <input>.
  return (
    // biome-ignore lint/a11y/noStaticElementInteractions: forwards a click on the box's padding or a decorative slot to the <input>, which stays the only focus target.
    <span
      onMouseDown={focusInputFromBox}
      className={cn(
        fieldInputClassName(isInvalid, "wrapper", font),
        "flex w-full items-center p-0",
        SIZE_CLASSES[size],
      )}
    >
      {start !== undefined && (
        <span className={cn("flex shrink-0 items-center font-sans text-text-secondary", padding.edge)}>{start}</span>
      )}
      <input
        {...inputProps}
        className={cn(
          "h-full min-w-0 flex-1 text-ellipsis rounded-control border-0 bg-transparent py-0 outline-none disabled:cursor-not-allowed",
          start === undefined ? padding.edge : padding.start,
          end === undefined ? padding.end : "pe-2",
        )}
      />
      {end !== undefined && <span className="flex shrink-0 items-center pe-1 text-text-secondary">{end}</span>}
    </span>
  );
}

// A number input's text can differ from its canonical value mid-edit ("1.0"
// while typing 1.05); rendering the canonical "1" back would erase it.
function sameNumber(typed: string, value: string): boolean {
  return typed !== value && typed !== "" && value !== "" && Number(typed) === Number(value);
}

// A combobox's text is a search query, empty once a value is picked, so native
// `required` would flag a filled field as missing.
export function requiredProps(
  role: string | undefined,
  required: boolean | undefined,
): { required?: boolean | undefined; "aria-required"?: true | undefined } {
  return role === "combobox" ? { required: undefined, "aria-required": required || undefined } : { required };
}

function focusInputFromBox(event: MouseEvent<HTMLSpanElement>): void {
  if ((event.target as Element).closest("input, button, a")) return;
  event.preventDefault();
  event.currentTarget.querySelector("input")?.focus();
}

type NativeTextAreaProps = Omit<
  ComponentPropsWithRef<"textarea">,
  "value" | "onChange" | "className" | "style" | "children"
>;

export interface TextAreaProps extends NativeTextAreaProps {
  value: string;
  onChange: (value: string) => void;
  invalid?: boolean | undefined;
  resize?: "vertical" | "none" | undefined;
}

export function TextArea({
  value,
  onChange,
  invalid,
  rows = 3,
  resize = "vertical",
  id,
  required,
  "aria-describedby": ariaDescribedBy,
  className: _className,
  style: _style,
  ...rest
}: TextAreaProps & { className?: unknown; style?: unknown }): ReactNode {
  const field = useFieldControl();
  const isInvalid = invalid ?? field?.invalid ?? false;
  return (
    <textarea
      {...rest}
      value={value}
      rows={rows}
      id={field?.id ?? id}
      required={required ?? field?.required}
      aria-describedby={joinIds(field?.describedBy, ariaDescribedBy)}
      aria-invalid={isInvalid || undefined}
      onChange={(event) => onChange(event.target.value)}
      className={cn(fieldInputClassName(isInvalid), "block w-full", resize === "vertical" ? "resize-y" : "resize-none")}
    />
  );
}
