import { AlertDialog, Button, SectionCard } from "@goerp/sdk/components";
import { AppError } from "@goerp/sdk/error";
import { toast } from "@goerp/sdk/notifications";
import type { UseMutationResult } from "@tanstack/react-query";
import { type ReactNode, useState } from "react";
import type { ConfigEntry, SaveConfigResult } from "./config-api.js";
import { ConfigField } from "./config-field.js";
import { type Draft, draftOf, groupByCategory, parseDraft, sameDraft, widgetOf } from "./config-widgets.js";

type Drafts = Record<string, Draft>;

function initialDrafts(entries: ConfigEntry[]): Drafts {
  return Object.fromEntries(entries.map((entry) => [entry.key, draftOf(entry)]));
}

// The server rejects a save with a 422 whose details map each key to its
// message.
function fieldErrorsOf(err: unknown, module: string): Record<string, string> | null {
  if (!(err instanceof AppError) || err.httpStatus !== 422 || !err.details) return null;
  const errors: Record<string, string> = {};
  for (const [qualified, message] of Object.entries(err.details)) {
    if (typeof message === "string")
      errors[qualified.startsWith(`${module}.`) ? qualified.slice(module.length + 1) : qualified] = message;
  }
  return Object.keys(errors).length > 0 ? errors : null;
}

export interface ConfigFormProps {
  moduleName: string;
  entries: ConfigEntry[];
  save: UseMutationResult<SaveConfigResult, Error, Record<string, unknown>>;
  // Absent for a module with no endpoint to rotate a generated value.
  rotate?: UseMutationResult<unknown, Error, string> | undefined;
  onSaved: () => void;
  // Shown beside the Save button.
  actions?: ReactNode;
  // Shown under the button row.
  footer?: ReactNode;
}

// A module's config_schema entries as an editable form (shell-ux.md §5.4),
// grouped by category, saving only the keys that changed.
export function ConfigForm({
  moduleName,
  entries,
  save,
  rotate,
  onSaved,
  actions,
  footer,
}: ConfigFormProps): ReactNode {
  const initial = initialDrafts(entries);
  const [drafts, setDrafts] = useState<Drafts>(initial);
  const [errors, setErrors] = useState<Record<string, string>>({});
  const [rotating, setRotating] = useState<ConfigEntry | null>(null);

  const editable = entries.filter((entry) => widgetOf(entry) !== "generated");
  const changed = editable.filter((entry) => !sameDraft(drafts[entry.key] as Draft, initial[entry.key] as Draft));

  const change = (key: string, draft: Draft) => {
    setDrafts((current) => ({ ...current, [key]: draft }));
    setErrors(({ [key]: _, ...rest }) => rest);
  };

  const submit = () => {
    const nextErrors: Record<string, string> = {};
    const changes: Record<string, unknown> = {};
    for (const entry of changed) {
      const parsed = parseDraft(entry, drafts[entry.key] as Draft);
      if (parsed.ok) changes[entry.key] = parsed.value;
      else nextErrors[entry.key] = parsed.message;
    }
    setErrors(nextErrors);
    if (Object.keys(nextErrors).length > 0) return;

    save.mutate(changes, {
      onSuccess: (result) => {
        onSaved();
        toast.success(
          result.restartRequired.length > 0
            ? "Configuration saved. Some changes take effect after the module reloads."
            : "Configuration saved.",
        );
      },
      onError: (err) => {
        const fieldErrors = fieldErrorsOf(err, moduleName);
        if (fieldErrors) setErrors(fieldErrors);
        else toast.error(err instanceof AppError && err.message ? err.message : "Couldn't save. Try again.");
      },
    });
  };

  const confirmRotate = () => {
    const entry = rotating;
    setRotating(null);
    if (!entry || !rotate) return;
    rotate.mutate(entry.key, {
      onSuccess: () => toast.success(`${entry.label} rotated.`),
      onError: () => toast.error(`Couldn't rotate ${entry.label}.`),
    });
  };

  return (
    <form
      className="flex flex-col gap-6"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
    >
      {groupByCategory(entries).map(({ category, entries: group }) => (
        <SectionCard key={category} title={category}>
          <div className="mt-4 flex max-w-xl flex-col gap-5">
            {group.map((entry) => (
              <ConfigField
                key={entry.key}
                entry={entry}
                draft={drafts[entry.key] as Draft}
                error={errors[entry.key]}
                onChange={(draft) => change(entry.key, draft)}
                onRotate={rotate ? () => setRotating(entry) : undefined}
                rotating={rotate ? rotate.isPending && rotate.variables === entry.key : undefined}
              />
            ))}
          </div>
        </SectionCard>
      ))}
      <div className="flex flex-wrap items-center gap-2">
        <Button type="submit" variant="primary" disabled={changed.length === 0 || save.isPending}>
          {save.isPending ? "Saving…" : "Save configuration"}
        </Button>
        {actions}
      </div>
      {footer}
      <AlertDialog
        open={rotating !== null}
        title={`Rotate ${rotating?.label ?? "value"}?`}
        description="A new value is generated and the current one stops working."
        tone="warning"
        confirmLabel="Rotate"
        onCancel={() => setRotating(null)}
        onConfirm={confirmRotate}
      />
    </form>
  );
}
