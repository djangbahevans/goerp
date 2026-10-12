import type { MetaSchema } from "@goerp/sdk/schema";

export const OVERRIDE_PERMISSION = "engine.manage_view_overrides";
export type Property = "label" | "hidden" | "required" | "order" | "help_text" | "placeholder";
export type Values = Record<Property, string | boolean | number>;
export type Draft = Partial<Values>;

export interface EditableView {
  name: string;
  module: string;
  type: "list" | "form";
  declaration: Record<string, unknown>;
}

export interface ElementRow {
  key: string;
  element: string;
  values: Values;
  requiredControl: boolean;
}

export interface ElementGroup {
  key: string;
  label: string;
  rows: ElementRow[];
}

function object(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;
}

export function editableViews(schema: MetaSchema): EditableView[] {
  return Object.entries(schema.modules)
    .flatMap(([module, definition]) =>
      definition.views.flatMap((raw) => {
        const view = object(raw);
        if (!view || typeof view.name !== "string" || (view.type !== "list" && view.type !== "form")) return [];
        return [{ name: view.name, module, type: view.type, declaration: view } as EditableView];
      }),
    )
    .sort((a, b) => a.name.localeCompare(b.name));
}

export function elementGroups(view: EditableView, schema: MetaSchema): ElementGroup[] {
  const groups: ElementGroup[] = [];
  const modelField = (resource: string, field: string) =>
    Object.values(schema.modules)
      .flatMap((module) => module.models[resource]?.fields ?? [])
      .find((definition) => definition.name === field);

  function visit(parent: Record<string, unknown>, path: string, resource: string, label: string) {
    for (const collection of ["fields", "columns", "sections", "tabs"]) {
      const items = parent[collection];
      if (!Array.isArray(items)) continue;
      const key = `${path}/${collection}`;
      const rows: ElementRow[] = [];
      const occurrences = new Map<string, number>();

      for (const [index, raw] of items.entries()) {
        const item = object(raw);
        if (!item) continue;
        const element = [item.field, item.name, item.label].find((value) => typeof value === "string" && value !== "");
        if (typeof element !== "string") continue;
        const field = modelField(resource, element);
        const occurrence = occurrences.get(element) ?? 0;
        occurrences.set(element, occurrence + 1);
        const rowKey = `${key}/${element}/${occurrence}`;
        if (collection === "fields" || collection === "columns") {
          rows.push({
            key: rowKey,
            element,
            requiredControl: view.type === "form" && collection === "fields",
            values: {
              label: typeof item.label === "string" ? item.label : element,
              hidden: item.hidden === true,
              required: typeof item.required === "boolean" ? item.required : field?.required === true,
              order: typeof item.order === "number" ? item.order : index,
              help_text: typeof item.help_text === "string" ? item.help_text : "",
              placeholder: typeof item.placeholder === "string" ? item.placeholder : "",
            },
          });
        }
        visit(
          item,
          rowKey,
          item.type === "sub_list" ? (field?.related_model ?? resource) : resource,
          typeof item.label === "string" ? item.label : element,
        );
      }
      if (rows.length) groups.push({ key, label, rows });
    }
  }

  visit(
    view.declaration,
    view.name,
    String(view.declaration.resource ?? ""),
    view.type === "list" ? "Columns" : "Fields",
  );
  return groups;
}
