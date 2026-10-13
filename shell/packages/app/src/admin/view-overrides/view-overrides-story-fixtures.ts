import { apiClient } from "@goerp/sdk";
import { AppError } from "@goerp/sdk/error";
import type { MetaSchema } from "@goerp/sdk/schema";
import type { Property } from "./view-elements.js";
import type { ViewOverride } from "./view-overrides-api.js";

type StoryElement = { field: string } & Partial<Record<Property, string | boolean | number>>;

const FORM_FIELDS: StoryElement[] = [
  { field: "name", label: "Full name", required: true, help_text: "Given and family name" },
  { field: "email", label: "Email", placeholder: "name@example.com" },
  { field: "phone", label: "Phone" },
];
const LIST_COLUMNS: StoryElement[] = [
  { field: "name", label: "Name" },
  { field: "email", label: "Email" },
  { field: "phone", label: "Phone" },
];

function schema(rows: ViewOverride[]): MetaSchema {
  function effective(elements: StoryElement[], viewName: string) {
    return elements
      .map((element, index) => {
        const patched = { ...element };
        for (const row of rows.filter((row) => row.view_name === viewName && row.element === element.field))
          patched[row.property] = row.value;
        return { patched, index };
      })
      .sort((a, b) => Number(a.patched.order ?? a.index) - Number(b.patched.order ?? b.index))
      .map(({ patched }) => patched);
  }

  return {
    engine_version: "dev",
    schema_hash: JSON.stringify(rows),
    modules: {
      contacts: {
        name: "contacts",
        version: "0.0.0",
        display_name: "Contacts",
        routes: [],
        navigation: [],
        views: [
          {
            name: "contacts_form",
            type: "form",
            label: "Contact",
            resource: "contacts.contact",
            sections: [
              { name: "contact", label: "Contact information", fields: effective(FORM_FIELDS, "contacts_form") },
            ],
          },
          {
            name: "contacts_list",
            type: "list",
            label: "Contacts",
            resource: "contacts.contact",
            columns: effective(LIST_COLUMNS, "contacts_list"),
          },
        ],
        models: {
          "contacts.contact": {
            name: "contacts.contact",
            label: "Contact",
            label_plural: "Contacts",
            fields: [
              { name: "name", type: "text", required: true },
              { name: "email", type: "email" },
              { name: "phone", type: "phone" },
            ],
            enabled_ops: ["get", "search", "create", "update", "delete"],
            shareable: false,
          },
        },
        permissions: [],
        frontend: null,
        public_config: {},
        error_codes: [],
        notification_types: [],
        view_extensions: [],
        view_extension_definitions: [],
        load_order: 0,
      },
    },
  };
}

type Handler = (path: string, ...args: unknown[]) => Promise<unknown>;

export function fakeViewOverridesBackend({ failLoad = false, empty = false } = {}) {
  return () => {
    const client = apiClient as unknown as Record<"get" | "post" | "delete", Handler>;
    const original = { get: client.get, post: client.post, delete: client.delete };
    let rows: ViewOverride[] = [
      { id: "email-label", view_name: "contacts_form", element: "email", property: "label", value: "Work email" },
    ];

    client.get = async (path, options) => {
      if (path !== "/_meta/schema" && path !== "/_meta/view-overrides") return original.get(path, options);
      if (failLoad) throw new AppError({ code: "internal_error", message: "Couldn't load views.", httpStatus: 500 });
      if (path === "/_meta/schema") return empty ? { ...schema([]), modules: {} } : schema(rows);
      const viewName = (options as { params: { view_name: string } }).params.view_name;
      return { data: rows.filter((row) => row.view_name === viewName) };
    };
    client.post = async (path, body, options) => {
      if (path !== "/_meta/view-overrides") return original.post(path, body, options);
      const input = body as Omit<ViewOverride, "id"> & { required?: false };
      const fields = input.view_name === "contacts_form" ? FORM_FIELDS : LIST_COLUMNS;
      const field = fields.find((field) => field.field === input.element);
      const modelRequired = FORM_FIELDS.find((field) => field.field === input.element)?.required;
      const current = rows.filter((row) => row.view_name === input.view_name && row.element === input.element);
      const values = Object.fromEntries(current.map((row) => [row.property, row.value]));
      values[input.property] = input.value;
      if (input.required === false) values.required = false;
      if ((values.hidden ?? field?.hidden) && (values.required ?? field?.required ?? modelRequired))
        throw new AppError({
          code: "invalid_request",
          message: `field "${input.element}" cannot be hidden while required`,
          httpStatus: 400,
        });
      function upsert(property: Property, value: ViewOverride["value"]) {
        const existing = rows.find(
          (row) => row.view_name === input.view_name && row.element === input.element && row.property === property,
        );
        const id = existing?.id ?? `${input.view_name}-${input.element}-${property}`;
        rows = rows.filter(
          (row) => row.view_name !== input.view_name || row.element !== input.element || row.property !== property,
        );
        const row = { id, view_name: input.view_name, element: input.element, property, value };
        rows.push(row);
        return row;
      }
      const row = upsert(input.property, input.value);
      if (input.required === false) upsert("required", false);
      return row;
    };
    client.delete = async (path, options) => {
      if (!path.startsWith("/_meta/view-overrides/")) return original.delete(path, options);
      const id = decodeURIComponent(path.slice("/_meta/view-overrides/".length));
      rows = rows.filter((row) => row.id !== id);
    };
    return () => Object.assign(client, original);
  };
}
