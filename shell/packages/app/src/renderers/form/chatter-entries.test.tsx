import type { ActivityEntry, ActivityFieldChange } from "@goerp/sdk/react";
import type { FieldDef } from "@goerp/sdk/schema";
import { describe, expect, it } from "vitest";
import {
  activityTypeDisplay,
  type ChangeLabelContext,
  changeRelationSpecs,
  collectFormFields,
  fieldLabel,
  formatChangeValue,
} from "./chatter-entries.js";
import type { FormField, FormViewDeclaration } from "./form-view-types.js";

function context(overrides: Partial<ChangeLabelContext> = {}): ChangeLabelContext {
  return { formFields: new Map(), modelFields: new Map(), relationLabels: new Map(), ...overrides };
}

function modelFields(...fields: FieldDef[]): Map<string, FieldDef> {
  return new Map(fields.map((field) => [field.name, field]));
}

const change = (field: string): ActivityFieldChange => ({ field, old: null, new: null });

describe("collectFormFields", () => {
  it("collects fields from sections and from fields tabs", () => {
    const view: FormViewDeclaration = {
      name: "v",
      type: "form",
      resource: "sales.order",
      label: "Order",
      sections: [{ fields: [{ field: "state", label: "Status" }] }],
      tabs: [{ label: "More", type: "fields", sections: [{ fields: [{ field: "notes" }] }] }],
    };
    expect([...collectFormFields(view).keys()]).toEqual(["state", "notes"]);
  });
});

describe("fieldLabel", () => {
  it("prefers the form's label", () => {
    expect(fieldLabel("state", { field: "state", label: "Status" })).toBe("Status");
  });

  it("title-cases the field name, dropping a trailing _id", () => {
    expect(fieldLabel("customer_id", undefined)).toBe("Customer");
    expect(fieldLabel("delivery_on", undefined)).toBe("Delivery On");
  });
});

describe("formatChangeValue", () => {
  it("renders null and empty values as a dash", () => {
    expect(formatChangeValue(change("x"), null, context())).toBe("—");
    expect(formatChangeValue(change("x"), "", context())).toBe("—");
  });

  it("labels a selection value from the form's options, falling back to the raw value", () => {
    const declared: FormField = { field: "state", options: [{ value: "draft", label: "Draft" }] };
    const ctx = context({
      formFields: new Map([["state", declared]]),
      modelFields: modelFields({ name: "state", type: "selection" }),
    });
    expect(formatChangeValue(change("state"), "draft", ctx)).toBe("Draft");
    expect(formatChangeValue(change("state"), "archived", ctx)).toBe("archived");
  });

  it("labels a many2one id from the resolved relation labels, falling back to the raw id", () => {
    const ctx = context({
      modelFields: modelFields({ name: "customer_id", type: "many2one", related_model: "contacts.contact" }),
      relationLabels: new Map([["contacts.contact|", { c1: "Acme Corp" }]]),
    });
    expect(formatChangeValue(change("customer_id"), "c1", ctx)).toBe("Acme Corp");
    expect(formatChangeValue(change("customer_id"), "c9", ctx)).toBe("c9");
  });

  it("formats a date as the calendar day it names", () => {
    const ctx = context({ modelFields: modelFields({ name: "due_on", type: "date" }) });
    expect(formatChangeValue(change("due_on"), "2026-09-24", ctx)).toBe(
      new Intl.DateTimeFormat(undefined, { dateStyle: "medium" }).format(new Date(2026, 8, 24)),
    );
  });

  it("formats booleans and numbers", () => {
    const ctx = context({
      modelFields: modelFields({ name: "paid", type: "boolean" }, { name: "qty", type: "integer" }),
    });
    expect(formatChangeValue(change("paid"), true, ctx)).toBe("Yes");
    expect(formatChangeValue(change("qty"), 1200, ctx)).toBe(new Intl.NumberFormat().format(1200));
  });

  it("stringifies values of unknown fields", () => {
    expect(formatChangeValue(change("x"), 42, context())).toBe("42");
    expect(formatChangeValue(change("x"), { a: 1 }, context())).toBe('{"a":1}');
  });
});

describe("changeRelationSpecs", () => {
  it("batches every loaded change's many2one ids into one spec per related model", () => {
    const entries: ActivityEntry[] = [
      {
        id: "e1",
        kind: "change",
        changes: [
          { field: "customer_id", old: "c1", new: "c2" },
          { field: "state", old: "draft", new: "confirmed" },
        ],
        author: null,
        createdAt: "2026-09-24T10:00:00Z",
      },
      {
        id: "e2",
        kind: "change",
        changes: [{ field: "customer_id", old: null, new: "c3" }],
        author: null,
        createdAt: "2026-09-24T09:00:00Z",
      },
    ];
    const specs = changeRelationSpecs(
      entries,
      new Map([["customer_id", { field: "customer_id", resource_label_field: "legal_name" }]]),
      modelFields(
        { name: "customer_id", type: "many2one", related_model: "contacts.contact" },
        { name: "state", type: "selection" },
      ),
    );
    expect(specs).toEqual([
      {
        key: "contacts.contact|legal_name",
        resource: "contacts.contact",
        labelField: "legal_name",
        ids: ["c1", "c2", "c3"],
      },
    ]);
  });
});

describe("activityTypeDisplay", () => {
  it("maps each scheduled activity type to its icon and label", () => {
    expect(activityTypeDisplay("todo")).toEqual({ icon: "square-check", label: "to-do" });
    expect(activityTypeDisplay("unknown")).toEqual({ icon: "circle-check", label: "unknown" });
  });
});
