import * as v from "valibot";
import { describe, expect, it } from "vitest";
import { KanbanViewDeclarationSchema } from "./kanban-manifest-types.js";

const base = {
  name: "leads_kanban",
  type: "kanban",
  resource: "crm.lead",
  label: "Pipeline",
  group_by: "stage",
  card_fields: ["display_name"],
};

describe("KanbanViewDeclarationSchema", () => {
  it("accepts a minimal valid declaration", () => {
    expect(v.safeParse(KanbanViewDeclarationSchema, base).success).toBe(true);
  });

  it("requires group_by and card_fields", () => {
    const { group_by: _groupBy, ...withoutGroupBy } = base;
    expect(v.safeParse(KanbanViewDeclarationSchema, withoutGroupBy).success).toBe(false);
    const { card_fields: _cardFields, ...withoutCardFields } = base;
    expect(v.safeParse(KanbanViewDeclarationSchema, withoutCardFields).success).toBe(false);
  });

  it("reuses ListActionSchema for card_actions/column_actions", () => {
    const result = v.safeParse(KanbanViewDeclarationSchema, {
      ...base,
      card_actions: [{ label: "Mark Won", type: "route", route: "crm.markWon" }],
      column_actions: [{ label: "Rename", type: "route", route: "crm.renameStage" }],
    });
    expect(result.success).toBe(true);
  });

  it("rejects a malformed nested action instead of silently accepting it", () => {
    const result = v.safeParse(KanbanViewDeclarationSchema, { ...base, card_actions: [{ label: "Bad" }] });
    expect(result.success).toBe(false);
  });

  it("accepts manifest-spec.md §9.3's own canonical example verbatim, explicit nulls included", () => {
    const result = v.safeParse(KanbanViewDeclarationSchema, {
      name: "contacts_kanban",
      type: "kanban",
      resource: "contacts.contact",
      label: "Contacts",
      group_by: "type",
      group_values: ["person", "company"],
      group_label_field: null,
      group_color_field: null,
      card_fields: ["avatar_file_id", "display_name", "email", "phone"],
      card_component: null,
      card_actions: [],
      column_actions: [],
      quick_create: false,
      quick_create_fields: [],
      allow_drag: true,
      drag_updates_field: null,
      max_cards_per_column: null,
      default_filters: {},
      filters: [],
      actions: [],
    });
    expect(result.success).toBe(true);
    expect(result.success && result.output.group_label_field).toBeUndefined();
  });
});
