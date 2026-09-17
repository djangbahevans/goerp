import * as v from "valibot";
import { describe, expect, it } from "vitest";
import {
  FormFieldSchema,
  FormSectionSchema,
  FormSidebarSchema,
  FormTabSchema,
  FormViewDeclarationSchema,
} from "./form-view-types.js";

describe("FormFieldSchema", () => {
  it("only requires field", () => {
    expect(v.safeParse(FormFieldSchema, { field: "email" }).success).toBe(true);
  });

  it("rejects a type outside manifest-spec.md §10's Field Types table", () => {
    expect(v.safeParse(FormFieldSchema, { field: "email", type: "email" }).success).toBe(true);
    expect(v.safeParse(FormFieldSchema, { field: "email", type: "money" }).success).toBe(false);
  });

  it("accepts manifest-spec.md §9.2's own canonical FormField example verbatim, explicit nulls included", () => {
    const result = v.safeParse(FormFieldSchema, {
      field: "email",
      label: "Email",
      type: "email",
      required: false,
      readonly: false,
      readonly_condition: null,
      hidden: false,
      condition: null,
      placeholder: null,
      help_text: null,
      span: 1,
      autofocus: false,
      computed: false,
      options: null,
      resource: null,
      resource_filter: null,
      resource_label_field: null,
      multiple: false,
      creatable: false,
      min: null,
      max: null,
      step: null,
      rows: null,
      accept: null,
      currency_field: null,
      align: null,
      component: null,
      component_props: null,
    });
    expect(result.success).toBe(true);
    expect(result.success && result.output.options).toBeUndefined();
  });

  it("reuses FilterOption's shape for options rather than a redefined FieldOption schema", () => {
    const result = v.safeParse(FormFieldSchema, {
      field: "status",
      options: [{ value: "draft", label: "Draft", icon: null, color: null, disabled: false }],
    });
    expect(result.success).toBe(true);
    // A malformed option (missing the required label) must still be caught —
    // proof this validates against FilterOptionSchema's real shape, not v.unknown().
    expect(v.safeParse(FormFieldSchema, { field: "status", options: [{ value: "draft" }] }).success).toBe(false);
  });
});

describe("FormSectionSchema", () => {
  it("accepts manifest-spec.md §9.2's own canonical FormSection example", () => {
    const result = v.safeParse(FormSectionSchema, {
      name: "contact_info",
      label: "Contact Information",
      type: "fields",
      columns: 2,
      collapsible: false,
      collapsed_by_default: false,
      condition: null,
      fields: [],
    });
    expect(result.success).toBe(true);
  });

  it("accepts columns as a layout count (1-4) or as a ListColumn[] for sub_list sections", () => {
    expect(v.safeParse(FormSectionSchema, { columns: 3 }).success).toBe(true);
    expect(v.safeParse(FormSectionSchema, { columns: 5 }).success).toBe(false);
    expect(v.safeParse(FormSectionSchema, { columns: [{ field: "name" }] }).success).toBe(true);
  });

  it("rejects a type outside fields/header/sub_list/custom", () => {
    expect(v.safeParse(FormSectionSchema, { type: "grid" }).success).toBe(false);
  });
});

describe("FormTabSchema", () => {
  it("requires label and type", () => {
    expect(v.safeParse(FormTabSchema, { label: "Addresses", type: "sub_list" }).success).toBe(true);
    expect(v.safeParse(FormTabSchema, { type: "sub_list" }).success).toBe(false);
    expect(v.safeParse(FormTabSchema, { label: "Addresses" }).success).toBe(false);
  });

  it("rejects a type outside sub_list/view/fields/component", () => {
    expect(v.safeParse(FormTabSchema, { label: "X", type: "form" }).success).toBe(false);
  });

  it("validates a nested 'fields'-type tab's own sections/fields", () => {
    const result = v.safeParse(FormTabSchema, {
      label: "Credit Settings",
      type: "fields",
      sections: [{ label: "Credit", fields: [{ field: "credit_limit", type: "currency" }] }],
    });
    expect(result.success).toBe(true);
  });
});

describe("FormSidebarSchema", () => {
  it("accepts manifest-spec.md §9.2's own canonical FormSidebar example", () => {
    const result = v.safeParse(FormSidebarSchema, {
      width: 280,
      sections: [{ label: "Quick Info", fields: ["status", "assigned_to", "created_at"] }],
    });
    expect(result.success).toBe(true);
  });

  it("requires sections", () => {
    expect(v.safeParse(FormSidebarSchema, { width: 280 }).success).toBe(false);
  });
});

describe("FormViewDeclarationSchema", () => {
  const base = { name: "contacts_form", type: "form", resource: "contacts.contact", label: "Contact", sections: [] };

  it("accepts manifest-spec.md §9.2's own canonical Form View example verbatim, explicit nulls included", () => {
    const result = v.safeParse(FormViewDeclarationSchema, {
      name: "contacts_form",
      type: "form",
      resource: "contacts.contact",
      label: "Contact",
      create_route: "contacts.create_contact",
      update_route: "contacts.update_contact",
      delete_route: "contacts.delete_contact",
      fetch_route: "contacts.get_contact",
      sections: [],
      tabs: [],
      header_actions: [],
      sidebar: null,
      chatter: true,
      autosave: false,
      readonly_condition: null,
    });
    expect(result.success).toBe(true);
  });

  it("requires name/type/resource/label/sections", () => {
    expect(v.safeParse(FormViewDeclarationSchema, base).success).toBe(true);
    for (const key of ["name", "type", "resource", "label", "sections"] as const) {
      const { [key]: _omitted, ...rest } = base;
      expect(v.safeParse(FormViewDeclarationSchema, rest).success).toBe(false);
    }
  });

  it("rejects a type mismatched against the literal 'form' discriminant", () => {
    expect(v.safeParse(FormViewDeclarationSchema, { ...base, type: "list" }).success).toBe(false);
  });

  it("validates nested sections/tabs/sidebar", () => {
    const result = v.safeParse(FormViewDeclarationSchema, {
      ...base,
      sections: [{ label: "Contact Info", fields: [{ field: "email", type: "email" }] }],
      tabs: [{ label: "Addresses", type: "sub_list", field: "address_ids" }],
      sidebar: { sections: [{ fields: ["status"] }] },
    });
    expect(result.success).toBe(true);
  });

  it("rejects a malformed nested section instead of silently accepting it", () => {
    const result = v.safeParse(FormViewDeclarationSchema, { ...base, sections: [{ fields: [{ label: "no field" }] }] });
    expect(result.success).toBe(false);
  });

  it("keeps an unknown field intact rather than stripping it", () => {
    const result = v.safeParse(FormViewDeclarationSchema, { ...base, future_field: 1 });
    expect(result.success).toBe(true);
    expect(result.success && result.output).toMatchObject({ future_field: 1 });
  });
});
