import {
  ActionButton,
  FieldWrapper,
  PasswordField,
  Select,
  TextArea,
  TextInput,
  ToggleField,
} from "@goerp/sdk/components";
import { type ReactNode, useState } from "react";
import type { ConfigEntry } from "./admin-connectors-api.js";
import { type Draft, splitList, widgetOf } from "./config-widgets.js";

export interface ConfigFieldProps {
  entry: ConfigEntry;
  draft: Draft;
  error?: string | undefined;
  onChange: (draft: Draft) => void;
  onRotate: () => void;
  rotating: boolean;
}

function boundsHint(entry: ConfigEntry): string | undefined {
  if (entry.min !== null && entry.max !== null) return `Between ${entry.min} and ${entry.max}.`;
  if (entry.min !== null) return `At least ${entry.min}.`;
  if (entry.max !== null) return `At most ${entry.max}.`;
  return undefined;
}

function describe(entry: ConfigEntry, widget: ReturnType<typeof widgetOf>): string | undefined {
  const parts = [
    entry.description,
    widget === "list" ? "Separate values with commas." : undefined,
    widget === "number" ? boundsHint(entry) : undefined,
    entry.restartRequired ? "A change takes effect after the module reloads." : undefined,
  ].filter((part): part is string => !!part);
  return parts.length > 0 ? parts.join(" ") : undefined;
}

// One config_schema entry, rendered by its widget (shell-ux.md §5.4).
export function ConfigField({ entry, draft, error, onChange, onRotate, rotating }: ConfigFieldProps): ReactNode {
  const widget = widgetOf(entry);
  const description = describe(entry, widget);

  if (widget === "password") {
    return (
      <div className="flex flex-col gap-1">
        <PasswordField
          label={entry.label}
          value={draft as string}
          onChange={onChange}
          autoComplete="new-password"
          error={error}
        />
        {description && <p className="text-sm text-text-secondary">{description}</p>}
      </div>
    );
  }

  return (
    <FieldWrapper label={entry.label} description={description} error={error} required={entry.required}>
      {widget === "generated" && (
        <div className="flex items-center gap-3">
          <span className="min-w-0 flex-1 truncate rounded-control border border-border bg-bg-subtle px-3 py-2 font-mono text-sm text-text">
            {entry.encrypted
              ? entry.isSet
                ? "••••••••••••"
                : "Not generated yet"
              : (draft as string) || "Not generated yet"}
          </span>
          <ActionButton variant="secondary" onClick={onRotate} loading={rotating}>
            Rotate
          </ActionButton>
        </div>
      )}
      {widget === "toggle" && <ToggleField value={draft as boolean} onChange={onChange} />}
      {widget === "select" && (
        <Select
          options={entry.options.map((o) => ({ value: o.value, label: o.label }))}
          value={draft as string}
          onChange={(v) => onChange(v as string)}
          placeholder="Choose…"
        />
      )}
      {widget === "multiselect" && (
        <Select
          multiple
          options={entry.options.map((o) => ({ value: o.value, label: o.label }))}
          value={draft as string[]}
          onChange={(v) => onChange(v as string[])}
          placeholder="Choose…"
        />
      )}
      {widget === "list" && <ListInput value={draft as string[]} onChange={onChange} />}
      {widget === "number" && <TextInput type="number" value={draft as string} onChange={onChange} />}
      {(widget === "json" || widget === "textarea") && (
        <TextArea value={draft as string} onChange={onChange} rows={widget === "json" ? 6 : 3} />
      )}
      {widget === "text" && <TextInput value={draft as string} onChange={onChange} />}
    </FieldWrapper>
  );
}

// Keeps the typed text, which a split-and-rejoin round trip would reformat
// (and swallow a trailing comma from) while the user is still typing.
function ListInput({ value, onChange }: { value: string[]; onChange: (items: string[]) => void }): ReactNode {
  const [text, setText] = useState(value.join(", "));
  return (
    <TextInput
      value={text}
      onChange={(next) => {
        setText(next);
        onChange(splitList(next));
      }}
    />
  );
}
