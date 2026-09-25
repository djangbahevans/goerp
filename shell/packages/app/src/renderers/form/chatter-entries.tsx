import { formatFieldValue } from "@goerp/sdk/components";
import type { ActivityEntry, ActivityFieldChange, RelationBatchSpec } from "@goerp/sdk/react";
import type { FieldDef } from "@goerp/sdk/schema";
import type { ReactNode } from "react";
import { titleCaseWords } from "../../chrome/title-case-words.js";
import type { FormField, FormViewDeclaration } from "./form-view-types.js";

const EMPTY_VALUE = "—";

// scheduled-activities.md §3's fixed activity types, per form-chatter.md.
const ACTIVITY_TYPES: Record<string, { icon: string; label: string }> = {
  call: { icon: "phone", label: "call" },
  meeting: { icon: "users", label: "meeting" },
  email: { icon: "mail", label: "email" },
  todo: { icon: "square-check", label: "to-do" },
};

export function activityTypeDisplay(type: string): { icon: string; label: string } {
  return ACTIVITY_TYPES[type] ?? { icon: "circle-check", label: type };
}

// Every field the form declares, across its sections and its "fields" tabs.
export function collectFormFields(view: FormViewDeclaration): Map<string, FormField> {
  const fields = new Map<string, FormField>();
  const sections = [...view.sections, ...(view.tabs ?? []).flatMap((tab) => tab.sections ?? [])];
  for (const section of sections) {
    for (const field of section.fields ?? []) {
      if (!fields.has(field.field)) fields.set(field.field, field);
    }
  }
  return fields;
}

export function fieldLabel(name: string, declared: FormField | undefined): string {
  return declared?.label ?? titleCaseWords(name.replace(/_id$/, ""), /_/);
}

export interface ChangeLabelContext {
  formFields: Map<string, FormField>;
  modelFields: Map<string, FieldDef>;
  // Keyed by relationSpecKey().
  relationLabels: Map<string, Record<string, string>>;
}

function relationSpecKey(relatedModel: string, labelField: string | undefined): string {
  return `${relatedModel}|${labelField ?? ""}`;
}

// One spec per related model (and label field) across every loaded change.
export function changeRelationSpecs(
  entries: ActivityEntry[],
  formFields: Map<string, FormField>,
  modelFields: Map<string, FieldDef>,
): RelationBatchSpec[] {
  const specs = new Map<string, RelationBatchSpec>();
  for (const entry of entries) {
    if (entry.kind !== "change") continue;
    for (const change of entry.changes) {
      const def = modelFields.get(change.field);
      if (def?.type !== "many2one" || !def.related_model) continue;
      const labelField = formFields.get(change.field)?.resource_label_field;
      const key = relationSpecKey(def.related_model, labelField);
      const spec = specs.get(key) ?? {
        key,
        resource: def.related_model,
        ...(labelField !== undefined ? { labelField } : {}),
        ids: [],
      };
      for (const value of [change.old, change.new]) {
        if (typeof value === "string" && value !== "") spec.ids.push(value);
      }
      specs.set(key, spec);
    }
  }
  return [...specs.values()];
}

export function formatChangeValue(change: ActivityFieldChange, value: unknown, context: ChangeLabelContext): string {
  if (value === null || value === undefined || value === "") return EMPTY_VALUE;
  const def = context.modelFields.get(change.field);
  const declared = context.formFields.get(change.field);
  switch (def?.type) {
    case "selection":
    case "enum":
      return declared?.options?.find((option) => option.value === String(value))?.label ?? String(value);
    case "many2one": {
      if (!def.related_model) return String(value);
      const labels = context.relationLabels.get(relationSpecKey(def.related_model, declared?.resource_label_field));
      return labels?.[String(value)] || String(value);
    }
    case "date":
      return formatFieldValue(value, "date", undefined, EMPTY_VALUE);
    case "timestamptz":
      return formatFieldValue(value, "datetime", undefined, EMPTY_VALUE);
    case "time":
      return formatFieldValue(value, "time", undefined, EMPTY_VALUE);
    case "boolean":
      return formatFieldValue(value, "boolean", undefined, EMPTY_VALUE);
    case "integer":
    case "bigint":
    case "float":
    case "decimal":
      return formatFieldValue(value, "number", undefined, EMPTY_VALUE);
    default:
      return typeof value === "object" ? formatFieldValue(value, "json", undefined, EMPTY_VALUE) : String(value);
  }
}

export interface EntryDisplay {
  icon: string;
  title: string;
  body?: ReactNode;
}

function ChangeLines({ changes, context }: { changes: ActivityFieldChange[]; context: ChangeLabelContext }) {
  const single = changes.length === 1;
  return (
    <ul className="space-y-1 text-sm text-text-secondary [overflow-wrap:anywhere]">
      {changes.map((change) => (
        <li key={change.field}>
          {!single && `${fieldLabel(change.field, context.formFields.get(change.field))}: `}
          {formatChangeValue(change, change.old, context)} →{" "}
          <span className="text-text">{formatChangeValue(change, change.new, context)}</span>
        </li>
      ))}
    </ul>
  );
}

export function entryDisplay(entry: ActivityEntry, context: ChangeLabelContext): EntryDisplay {
  const bySystem = entry.author === null ? " by the system" : "";
  switch (entry.kind) {
    case "created":
      return { icon: "circle-plus", title: `Created this record${bySystem}` };
    case "change": {
      const [first] = entry.changes;
      const title =
        entry.changes.length === 1 && first
          ? `Changed ${fieldLabel(first.field, context.formFields.get(first.field))}`
          : `Changed ${entry.changes.length} fields`;
      return {
        icon: "pencil",
        title: `${title}${bySystem}`,
        body: <ChangeLines changes={entry.changes} context={context} />,
      };
    }
    case "comment":
      if (entry.deleted) return { icon: "message-square-off", title: "Comment deleted" };
      return {
        icon: "message-square",
        title: "Commented",
        body: (
          <p className="max-w-[80ch] whitespace-pre-wrap text-sm text-text [overflow-wrap:anywhere]">{entry.body}</p>
        ),
      };
    case "activity_done": {
      const { icon, label } = activityTypeDisplay(entry.activity.type);
      return {
        icon,
        title: `Completed ${label}: ${entry.activity.summary}${bySystem}`,
        body: (
          <div className="text-sm text-text-secondary">
            <p>Due {formatFieldValue(entry.activity.dueDate, "date", undefined, EMPTY_VALUE)}</p>
            {entry.activity.feedback && (
              <p className="max-w-[80ch] whitespace-pre-wrap text-text [overflow-wrap:anywhere]">
                {entry.activity.feedback}
              </p>
            )}
          </div>
        ),
      };
    }
  }
}
